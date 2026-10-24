"""End-to-end verification of databases' memory and of removing failed
applications that never ran (the platform owner, 2026-10-24), against a
live stack, through the real MCP server over stdio:

    docker compose up -d --build        # from the repo root
    cd services/mcp-server
    .venv/Scripts/python.exe scripts/verify_db_memory.py

platform-api is restarted with a 60 s idle timeout and a 10 s sweep, and
with its defaults at the end. Don't run it alongside another script that
restarts platform-api. Checks:

  - a new MySQL database starts with the platform's configuration
    (performance_schema off, a 64 MB buffer pool) and uses far less memory
  - once its application's API has scaled to zero for the idle timeout, the
    database is stopped (not removed); opening the application wakes it,
    with its data
  - a dev copy whose first build fails is removed and its name freed; the
    next dev deployment makes it again at the same address
  - an application whose first build fails is removed, its owner told,
    and its name can be registered again
  - an application with a dev copy is never removed that way
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
OWNER = f"dbmem-{SUFFIX}@sti-th.com"
BROKEN_GO = "package main\n\nthis is not go\n"
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


def _docker(*args: str) -> str:
    return subprocess.run(["docker", *args], capture_output=True, text=True).stdout.strip()


def _db_container(app_name: str) -> str:
    return _docker("ps", "-a", "--format", "{{.Names}}", "--filter", f"name=platform-db-{app_name}-").split("\n")[0]


def _mysql(container: str, sql: str) -> str:
    return _docker("exec", container, "sh", "-c",
                   f'MYSQL_PWD=$MYSQL_PASSWORD mysql -u$MYSQL_USER $MYSQL_DATABASE -N --default-character-set=utf8mb4 -e "{sql}"')


def _mem_mib(container: str) -> float:
    used = _docker("stats", "--no-stream", "--format", "{{.MemUsage}}", container).split("/")[0].strip()
    num = float("".join(ch for ch in used if ch.isdigit() or ch == ".") or 0)
    return num / 1024 if used.endswith("KiB") else num * 1024 if used.endswith("GiB") else num


def _http(method: str, url: str, body: dict | None = None, tries: int = 40):
    for _ in range(tries):
        req = urllib.request.Request(url, method=method, data=json.dumps(body).encode() if body is not None else None,
                                     headers={"Content-Type": "application/json"})
        try:
            with urllib.request.urlopen(req, timeout=120) as r:
                return r.status, json.loads(r.read() or b"null")
        except urllib.error.HTTPError as e:
            if e.code in (502, 503, 504):
                time.sleep(2)
                continue
            return e.code, None
        except Exception:
            time.sleep(2)
    return 0, None


async def _tool(session: ClientSession, name: str, args: dict) -> dict:
    payload = (await session.call_tool(name, args)).structured_content or {}
    shown = json.dumps(payload, ensure_ascii=False, default=str)
    print(f"  {name} -> {shown[:260]}{'…' if len(shown) > 260 else ''}", flush=True)
    return payload


def _wait(cond, seconds: int, step: int = 5) -> bool:
    for _ in range(seconds // step):
        if cond():
            return True
        time.sleep(step)
    return cond()


async def _new_app(s, tmp: Path, name: str) -> tuple[str, Path]:
    started = await _tool(s, "start_project", {"name": name, "purpose": "database memory verification",
                                               "handles_personal_data": False, "api_runtime": "go"})
    app_id = started["data"]["application_id"]
    project = tmp / name
    args = {"application_id": app_id, "project_path": str(project)}
    sc = await _tool(s, "scaffold_project", args)
    if (sc.get("data") or {}).get("dev") is None:
        pass  # this platform's scaffold doesn't deploy by itself
    await _tool(s, "validate_application", {"application_id": app_id})
    return app_id, project


async def main() -> None:
    _restart({"SCALE_TO_ZERO_IDLE_SECONDS": "60", "SCALE_SWEEP_INTERVAL_SECONDS": "10"})
    owner = api_key_for(BASE, OWNER, "db memory verify")
    tmp = Path(tempfile.mkdtemp(prefix="dbmem-"))
    creds = tmp / "credentials.json"
    creds.write_text(json.dumps({"platform_url": BASE, "api_key": owner, "email": OWNER, "key_id": "",
                                 "key_name": "dbmem", "expires_at": ""}))
    params = StdioServerParameters(command=sys.executable, args=["-m", "mcp_server.server"], env={
        **os.environ, "PLATFORM_API_BASE_URL": BASE, "COMPANY_DEPLOY_CREDENTIALS": str(creds),
        "COMPANY_DEPLOY_TEMPLATES": str(REPO / "company-deployment-skill" / "templates"),
    })
    keep: list[str] = []
    try:
        async with stdio_client(params) as (read, write):
            async with ClientSession(read, write) as s:
                await s.initialize()

                # 1. A new database, sized for a small application.
                name = f"dbm-{SUFFIX}"
                app_id, project = await _new_app(s, tmp, name)
                keep.append(app_id)
                dev = await _tool(s, "deploy_application", {"application_id": app_id, "target_environment": "dev",
                                                           "project_path": str(project), "run_tests": False})
                _check(dev["status"] == "success", "the dev copy is up")
                db = _db_container(f"{name}-dev")
                settings = _mysql(db, "SELECT @@performance_schema, @@innodb_buffer_pool_size DIV 1048576")
                mem = _mem_mib(db)
                _check(settings.split() == ["0", "64"] and 0 < mem < 220,
                       f"a new MySQL starts with the platform's configuration ({settings!r}) and uses {mem:.0f} MiB")
                status, _ = _http("POST", f"{GATEWAY}/{name}-dev/api/items", {"title": "ข้อมูลก่อนหลับ"})
                _check(status == 201, "an item is written")

                # 2. Asleep once the API has been idle, awake when opened.
                asleep = _wait(lambda: _docker("inspect", "-f", "{{.State.Running}}", db) == "false", 300)
                _check(asleep and db in _docker("ps", "-a", "--format", "{{.Names}}"),
                       "after the API scaled to zero for the idle timeout, the database is stopped, not removed")
                _check(_mem_mib(db) == 0, "…and holds no memory")
                t0 = time.time()
                status, items = _http("GET", f"{GATEWAY}/{name}-dev/api/items")
                _check(status == 200 and [i["title"] for i in items or []] == ["ข้อมูลก่อนหลับ"]
                       and _docker("inspect", "-f", "{{.State.Running}}", db) == "true",
                       f"opening the application woke it, data intact ({time.time() - t0:.0f}s for the first answer)")

                # 3. A dev copy whose first build fails is removed, its name freed.
                name2 = f"dbf-{SUFFIX}"
                app2, project2 = await _new_app(s, tmp, name2)
                keep.append(app2)
                (project2 / "api" / "main.go").write_text(BROKEN_GO, encoding="utf-8")
                failed = await _tool(s, "deploy_application", {"application_id": app2, "target_environment": "dev",
                                                              "project_path": str(project2), "run_tests": False})
                _check(failed["status"] == "error" and "never ran" in failed["error"]["message"],
                       "a dev copy's first build fails, and Claude is told what happens next")
                _, copy = call(BASE, "GET", f"/applications/{app2}/dev", token=owner)
                copy_id = (copy.get("dev_copy") or {}).get("application_id")
                gone = _wait(lambda: (call(BASE, "GET", f"/applications/{copy_id}", token=owner)[1] or {}).get(
                    "lifecycle_status") == "deleted", 120)
                _, deleted_copy = call(BASE, "GET", f"/applications/{copy_id}", token=owner)
                _check(gone and deleted_copy.get("name", "").startswith("deleted-"),
                       f"…the copy that never ran is removed and its name freed ({deleted_copy.get('name')})")
                (project2 / "api" / "main.go").write_text(
                    (REPO / "company-deployment-skill" / "templates" / "api-go" / "main.go").read_text(encoding="utf-8"),
                    encoding="utf-8")
                fixed = await _tool(s, "deploy_application", {"application_id": app2, "target_environment": "dev",
                                                             "project_path": str(project2), "run_tests": False})
                _check(fixed["status"] == "success" and fixed["data"]["dev_copy"]["name"] == f"{name2}-dev",
                       "fixed, the next dev deployment makes it again at the same address")

                # 4. An application whose first build fails is removed; its owner told; the name reusable.
                name3 = f"dbx-{SUFFIX}"
                app3, project3 = await _new_app(s, tmp, name3)
                (project3 / "api" / "main.go").write_text(BROKEN_GO, encoding="utf-8")
                (project3 / "REVIEW.md").write_text("# x", encoding="utf-8")
                bad = await _tool(s, "deploy_application", {"application_id": app3, "target_environment": "production",
                                                           "project_path": str(project3), "run_tests": False})
                _check(bad["status"] == "error" and "start it again under the same name" in bad["error"]["message"],
                       "the application's first build fails, and Claude is told to start it again under the same name")
                removed = _wait(lambda: (call(BASE, "GET", f"/applications/{app3}", token=owner)[1] or {}).get(
                    "lifecycle_status") == "deleted", 120)
                _, notes = call(BASE, "GET", "/notifications", token=owner)
                told = [n for n in notes.get("notifications", []) if n["title"].startswith(f"{name3} was removed")]
                _check(removed and told and "syntax" in told[0]["detail"].lower() and "same name" in told[0]["detail"],
                       "…it is removed within the sweep, and its owner is told why and to deploy again")
                again = await _tool(s, "start_project", {"name": name3, "purpose": "again", "handles_personal_data": False,
                                                         "api_runtime": "go"})
                _check(again["status"] == "success", "the same name is registered again")
                if again["status"] == "success":
                    keep.append(again["data"]["application_id"])

                # 5. An application with a dev copy is never removed this way.
                (project2 / "REVIEW.md").write_text("# x", encoding="utf-8")
                (project2 / "api" / "main.go").write_text(BROKEN_GO, encoding="utf-8")
                await _tool(s, "deploy_application", {"application_id": app2, "target_environment": "production",
                                                      "project_path": str(project2), "run_tests": False, "dev_data": "leave"})
                time.sleep(45)
                _, still = call(BASE, "GET", f"/applications/{app2}", token=owner)
                _check(still.get("lifecycle_status") == "failed",
                       f"an application with a dev copy whose production build failed is kept ({still.get('lifecycle_status')})")
    finally:
        for app_id in keep:
            call(BASE, "POST", f"/applications/{app_id}/archive", {}, token=owner)
            status, _ = call(BASE, "POST", f"/applications/{app_id}/delete", {"confirm": True}, token=owner)
            print(f"cleanup: delete {app_id} -> {status}")
        _restart({})

    print()
    if _failures:
        print(f"{len(_failures)} check(s) FAILED:")
        for f in _failures:
            print("  -", f)
        raise SystemExit(1)
    print("All database memory checks passed.")


if __name__ == "__main__":
    asyncio.run(main())
