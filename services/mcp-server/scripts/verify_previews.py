"""End-to-end verification of previews (DEC-029 part 3) against a live
stack, through the real MCP server over stdio:

    docker compose up -d --build        # from the repo root
    cd services/mcp-server
    .venv/Scripts/python.exe scripts/verify_previews.py

platform-api is restarted with APPROVAL_REQUIRED_ENVIRONMENTS=dev,production
(as on the LAN server) and restarted without it at the end. Don't run it
alongside another script that restarts platform-api. Checks:

  - the application, deployed to dev (which waits for approval here), holds
    an item in its database
  - preview_application: tests, then a separate application at its own
    address, deployed to "preview" without waiting for approval; its
    database is empty; the application and its data are untouched
  - another preview replaces it (new address, the old one gone with its
    database); a preview of a preview and a "preview" deploy of an ordinary
    application are refused
  - stop_preview removes it; an expired one goes on the sweep; archiving the
    application removes its preview
"""

from __future__ import annotations

import asyncio
import json
import os
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
import uuid
from pathlib import Path

from mcp import ClientSession
from mcp.client.stdio import StdioServerParameters, stdio_client

REPO = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(REPO / "services" / "platform-api" / "scripts"))
from platform_auth import api_key_for, call  # noqa: E402

BASE = os.environ.get("PLATFORM_API_BASE_URL", "http://localhost:8099")
GATEWAY = os.environ.get("GATEWAY_URL", "http://localhost:8098")
SUFFIX = uuid.uuid4().hex[:5]
OWNER = f"preview-dev-{SUFFIX}@sti-th.com"
_failures: list[str] = []


def _check(cond, msg):
    print(("OK:   " if cond else "FAIL: ") + msg, flush=True)
    if not cond:
        _failures.append(msg)
    return cond


def _restart(extra: dict[str, str]) -> None:
    subprocess.run(["docker", "compose", "up", "-d", "--force-recreate", "platform-api"], cwd=REPO,
                   env={**os.environ, **extra}, capture_output=True, check=True)
    for _ in range(90):
        try:
            urllib.request.urlopen(BASE + "/healthz", timeout=5)
            return
        except Exception:
            time.sleep(1)


def _http(method: str, url: str, body: dict | None = None, tries: int = 30):
    """An application's own API through the gateway (cold starts retry)."""
    for _ in range(tries):
        req = urllib.request.Request(url, method=method, data=json.dumps(body).encode() if body is not None else None,
                                     headers={"Content-Type": "application/json"})
        try:
            with urllib.request.urlopen(req, timeout=30) as r:
                return r.status, json.loads(r.read() or b"null")
        except urllib.error.HTTPError as e:
            if e.code in (502, 503, 504):
                time.sleep(2)
                continue
            return e.code, None
        except Exception:
            time.sleep(2)
    return 0, None


def _db_containers(name: str) -> list[str]:
    out = subprocess.run(["docker", "ps", "-a", "--format", "{{.Names}}", "--filter", f"name=platform-db-{name}-"],
                         capture_output=True, text=True).stdout
    return [line for line in out.split() if line]


def _sql(q: str) -> str:
    # On stdin: `docker compose exec` doesn't pass this shell's variables in.
    return subprocess.run(["docker", "compose", "exec", "-T", "postgres", "sh", "-c",
                           'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -tA'],
                          cwd=REPO, input=q, capture_output=True, text=True).stdout.strip()


async def _tool(session: ClientSession, name: str, args: dict) -> dict:
    payload = (await session.call_tool(name, args)).structured_content or {}
    shown = json.dumps(payload, ensure_ascii=False, default=str)
    print(f"  {name} -> {shown[:300]}{'…' if len(shown) > 300 else ''}", flush=True)
    return payload


def _wait_gone(app_id: str, owner: str, seconds: int = 100) -> bool:
    for _ in range(seconds // 5):
        _, app = call(BASE, "GET", f"/applications/{app_id}", token=owner)
        if (app or {}).get("lifecycle_status") == "deleted":
            return True
        time.sleep(5)
    return False


async def main() -> None:
    _restart({"APPROVAL_REQUIRED_ENVIRONMENTS": "dev,production", "PREVIEW_TTL_HOURS": "2"})
    owner = api_key_for(BASE, OWNER, "previews verify")
    tmp = Path(tempfile.mkdtemp(prefix="previews-"))
    creds = tmp / "credentials.json"
    creds.write_text(json.dumps({"platform_url": BASE, "api_key": owner, "email": OWNER, "key_id": "",
                                 "key_name": "previews", "expires_at": ""}))
    params = StdioServerParameters(command=sys.executable, args=["-m", "mcp_server.server"], env={
        **os.environ, "PLATFORM_API_BASE_URL": BASE, "COMPANY_DEPLOY_CREDENTIALS": str(creds),
        "COMPANY_DEPLOY_TEMPLATES": str(REPO / "company-deployment-skill" / "templates"),
    })
    name = f"pv-{SUFFIX}"
    app_id = None
    try:
        async with stdio_client(params) as (read, write):
            async with ClientSession(read, write) as s:
                await s.initialize()
                tools = {t.name for t in (await s.list_tools()).tools}
                _check({"preview_application", "stop_preview"} <= tools, "the preview tools are there")
                started = await _tool(s, "start_project", {"name": name, "purpose": "previews verification",
                                                           "handles_personal_data": False, "api_runtime": "go"})
                app_id = started["data"]["application_id"]
                project = tmp / name
                await _tool(s, "scaffold_project", {"application_id": app_id, "project_path": str(project)})
                await _tool(s, "validate_application", {"application_id": app_id})

                dep = await _tool(s, "deploy_application", {"application_id": app_id, "target_environment": "dev",
                                                           "project_path": str(project), "run_tests": False})
                _check(dep["data"].get("status") == "pending_approval", "dev waits for approval here")
                status, _ = call(BASE, "POST", f"/deployments/{dep['data']['deployment_id']}/approve",
                                 {"decision": "approve"}, token=owner)
                status, item = _http("POST", f"{GATEWAY}/{name}/api/items", {"title": "ข้อมูลจริงของแอป"})
                _check(status in (200, 201), f"the application holds an item in its own database ({status})")
                _, parent_dep = call(BASE, "GET", f"/applications/{app_id}/deployments/latest", token=owner)

                t0 = time.time()
                pv = await _tool(s, "preview_application", {"application_id": app_id, "project_path": str(project)})
                data = pv.get("data") or {}
                p1 = data.get("preview", {})
                _check(pv["status"] == "success" and data.get("deployment_status") == "running"
                       and p1.get("name", "").startswith(f"{name}-preview-"),
                       f"tested, built and running as its own application, without waiting ({time.time() - t0:.0f}s)")
                _check(data.get("url", "").endswith(f"/{p1.get('name')}/") and data.get("test_run_id"),
                       f"its own address ({data.get('url')}), after the platform's tests")
                hours = (time.mktime(time.strptime(p1["expires_at"], "%Y-%m-%dT%H:%M:%SZ"))
                         - time.mktime(time.strptime(p1["created_at"], "%Y-%m-%dT%H:%M:%SZ"))) / 3600
                _check(abs(hours - 2) < 0.01, f"expires after PREVIEW_TTL_HOURS ({hours:.2f} h)")
                _check(_sql(f"SELECT environment FROM deployments WHERE application_id = '{p1['application_id']}'") == "preview",
                       "deployed to the preview environment")
                status, items = _http("GET", f"{GATEWAY}/{p1['name']}/api/items")
                _check(status == 200 and items == [], f"its database is empty ({status}, {items})")
                status, page = _http("GET", f"{GATEWAY}/{p1['name']}/")
                status, original = _http("GET", f"{GATEWAY}/{name}/api/items")
                _check(status == 200 and any(i.get("title") == "ข้อมูลจริงของแอป" for i in original or []),
                       "the application's own data is untouched")
                _, after = call(BASE, "GET", f"/applications/{app_id}/deployments/latest", token=owner)
                _check(after.get("id") == parent_dep.get("id") and after.get("status") == "running",
                       "the application's deployment is the same one, still running")

                pv2 = await _tool(s, "preview_application", {"application_id": app_id, "project_path": str(project),
                                                            "run_tests": False})
                p2 = (pv2.get("data") or {}).get("preview", {})
                _check(p2.get("name") and p2["name"] != p1["name"], f"another preview replaces it ({p2.get('name')})")
                _, old = call(BASE, "GET", f"/applications/{p1['application_id']}", token=owner)
                _check(old.get("lifecycle_status") == "deleted" and not _db_containers(p1["name"]),
                       "the old one is deleted, its database container with it")

                pp = await _tool(s, "preview_application", {"application_id": p2["application_id"],
                                                           "project_path": str(project), "run_tests": False})
                _check(pp["status"] == "error", "a preview of a preview is refused")
                wrong = await _tool(s, "deploy_application", {"application_id": app_id, "target_environment": "preview",
                                                             "run_tests": False})
                _check(wrong["status"] == "error" and "preview" in wrong["error"]["message"],
                       "an ordinary application can't deploy to the preview environment")

                stop = await _tool(s, "stop_preview", {"application_id": app_id})
                _, gone = call(BASE, "GET", f"/applications/{p2['application_id']}", token=owner)
                _check(stop["status"] == "success" and gone.get("lifecycle_status") == "deleted"
                       and not _db_containers(p2["name"]), "stop_preview removes it and its database")

                pv3 = await _tool(s, "preview_application", {"application_id": app_id, "project_path": str(project),
                                                            "run_tests": False})
                p3 = (pv3.get("data") or {}).get("preview", {})
                _sql(f"UPDATE application_previews SET expires_at = now() - interval '1 minute' "
                     f"WHERE application_id = '{p3['application_id']}'")
                _check(_wait_gone(p3["application_id"], owner), "an expired preview goes on the sweep")

                pv4 = await _tool(s, "preview_application", {"application_id": app_id, "project_path": str(project),
                                                            "run_tests": False})
                p4 = (pv4.get("data") or {}).get("preview", {})
                status, _ = call(BASE, "POST", f"/applications/{app_id}/archive", {}, token=owner)
                _check(status == 200 and _wait_gone(p4["application_id"], owner),
                       "archiving the application removes its preview")
    finally:
        if app_id:
            call(BASE, "POST", f"/applications/{app_id}/archive", {}, token=owner)
            status, _ = call(BASE, "POST", f"/applications/{app_id}/delete", {"confirm": True}, token=owner)
            print(f"cleanup: delete -> {status}")
        _restart({})

    print()
    if _failures:
        print(f"{len(_failures)} check(s) FAILED:")
        for f in _failures:
            print("  -", f)
        raise SystemExit(1)
    print("All preview checks passed.")


if __name__ == "__main__":
    asyncio.run(main())
