"""End-to-end verification of dev copies and their data (DEC-029 part 3)
against a live stack, through the real MCP server over stdio:

    docker compose up -d --build        # from the repo root
    cd services/mcp-server
    .venv/Scripts/python.exe scripts/verify_dev_copies.py

platform-api is restarted with a throwaway approver key and
APPROVAL_REQUIRED_ENVIRONMENTS=dev,production (as on the LAN server), and
restarted without them at the end. Don't run it alongside another script
that restarts platform-api. Checks:

  - scaffold_project puts a new application up on dev at once and returns
    the link: "<name>-dev", its own application, without waiting for
    approval even with dev gated; its page is the application's own, with
    no banner added
  - its data stays across the next dev deployment (same address)
  - record_test_data keeps Claude's note with the copy
  - the first production deployment asks about the dev data (naming
    Claude's test data) and builds nothing until answered
  - dev_data="copy": the approver sees and signs it; once live, production
    has the dev copy's data; the dev copy keeps its own
  - a service added to a dev copy that holds data: revised in place, data kept
  - a later production deployment can't copy again (MCP and API)
  - running production: its deployment.yaml revised in place; its database type can't change
  - reset_dev removes the copy and its database; the next is a new, empty
    copy under the same name and address
  - archiving the application removes its dev copy
"""

from __future__ import annotations

import asyncio
import json
import os
import shutil
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

from mcp_server import approver  # noqa: E402

BASE = os.environ.get("PLATFORM_API_BASE_URL", "http://localhost:8099")
GATEWAY = os.environ.get("GATEWAY_URL", "http://localhost:8098")
SUFFIX = uuid.uuid4().hex[:5]
OWNER = f"devcopy-dev-{SUFFIX}@sti-th.com"
APPROVER = f"devcopy-approver-{SUFFIX}@sti-th.com"
REAL_ITEM = "ใบลาจริงของพนักงาน"
TEST_ITEM = "claude-test-item"
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


def _http(method: str, url: str, body: dict | None = None, raw: bool = False, tries: int = 30):
    """An application's own address through the gateway (cold starts retry)."""
    for _ in range(tries):
        req = urllib.request.Request(url, method=method, data=json.dumps(body).encode() if body is not None else None,
                                     headers={"Content-Type": "application/json"})
        try:
            with urllib.request.urlopen(req, timeout=30) as r:
                data = r.read()
                return r.status, (data.decode("utf-8", "replace") if raw else json.loads(data or b"null"))
        except urllib.error.HTTPError as e:
            if e.code in (502, 503, 504):
                time.sleep(2)
                continue
            return e.code, None
        except Exception:
            time.sleep(2)
    return 0, None


def _titles(items) -> list[str]:
    return sorted(i.get("title") for i in (items or []))


def _db_containers(name: str) -> list[str]:
    out = subprocess.run(["docker", "ps", "-a", "--format", "{{.Names}}", "--filter", f"name=platform-db-{name}-"],
                         capture_output=True, text=True).stdout
    return [line for line in out.split() if line]


async def _tool(session: ClientSession, name: str, args: dict) -> dict:
    payload = (await session.call_tool(name, args)).structured_content or {}
    shown = json.dumps(payload, ensure_ascii=False, default=str)
    print(f"  {name} -> {shown[:300]}{'…' if len(shown) > 300 else ''}", flush=True)
    return payload


def _wait_deleted(app_id: str, token: str, seconds: int = 120) -> bool:
    for _ in range(seconds // 5):
        _, app = call(BASE, "GET", f"/applications/{app_id}", token=token)
        if (app or {}).get("lifecycle_status") == "deleted":
            return True
        time.sleep(5)
    return False


async def main() -> None:
    tmp = Path(tempfile.mkdtemp(prefix="devcopies-"))
    key_path, passphrase = tmp / "approver.pem", b"throwaway-passphrase"
    key_line = approver.public_key_line(APPROVER, approver.generate(key_path, passphrase))
    _restart({"PLATFORM_APPROVER_KEYS": key_line, "APPROVAL_REQUIRED_ENVIRONMENTS": "dev,production"})
    owner = api_key_for(BASE, OWNER, "dev copies verify")
    approver_key = api_key_for(BASE, APPROVER, "dev copies verify (approver)")
    creds = tmp / "credentials.json"
    creds.write_text(json.dumps({"platform_url": BASE, "api_key": owner, "email": OWNER, "key_id": "",
                                 "key_name": "devcopies", "expires_at": ""}))
    params = StdioServerParameters(command=sys.executable, args=["-m", "mcp_server.server"], env={
        **os.environ, "PLATFORM_API_BASE_URL": BASE, "COMPANY_DEPLOY_CREDENTIALS": str(creds),
        "COMPANY_DEPLOY_TEMPLATES": str(REPO / "company-deployment-skill" / "templates"),
    })
    name = f"dc-{SUFFIX}"
    app_id = None
    try:
        async with stdio_client(params) as (read, write):
            async with ClientSession(read, write) as s:
                await s.initialize()
                tools = {t.name for t in (await s.list_tools()).tools}
                _check({"record_test_data", "reset_dev"} <= tools and "preview_application" not in tools,
                       "the dev copy tools are there")
                started = await _tool(s, "start_project", {"name": name, "purpose": "dev copies verification",
                                                           "handles_personal_data": False, "api_runtime": "go"})
                app_id = started["data"]["application_id"]
                project = tmp / name
                # Like a PaaS: the new application is up at its dev address
                # as soon as it's made, with a link to give the employee.
                t0 = time.time()
                dev = await _tool(s, "scaffold_project", {"application_id": app_id, "project_path": str(project)})
                d = dev.get("data") or {}
                copy = (d.get("dev") or {}).get("dev_copy") or {}
                _check(dev["status"] == "success" and (d.get("dev") or {}).get("status") == "running"
                       and copy.get("name") == f"{name}-dev" and (d.get("url") or "").endswith(f"/{name}-dev/"),
                       f"scaffold_project put it up on dev at once, without approval though dev is gated, and returned "
                       f"the link ({time.time() - t0:.0f}s)")
                _check("Give the employee the url now" in (d.get("tell_the_employee") or "")
                       and "don't push it" in d["tell_the_employee"],
                       "the result tells Claude to give the link now, production only when asked")
                await _tool(s, "validate_application", {"application_id": app_id})
                status, _ = call(BASE, "GET", f"/applications/{app_id}/deployments/latest", token=owner)
                _check(status == 404, "the application itself has no deployment")

                status, page = _http("GET", f"{GATEWAY}/{name}-dev/", raw=True)
                _check(status == 200 and page is not None and "DEV —" not in page,
                       "its page is the application's own: no DEV banner")
                s1, _ = _http("POST", f"{GATEWAY}/{name}-dev/api/items", {"title": REAL_ITEM})
                s2, _ = _http("POST", f"{GATEWAY}/{name}-dev/api/items", {"title": TEST_ITEM})
                rec = await _tool(s, "record_test_data", {"application_id": app_id, "note": f"1 item '{TEST_ITEM}'"})
                _check(s1 == 201 and s2 == 201 and rec["status"] == "success" and rec["data"]["dev_copy"]["test_data"],
                       "an employee's item and Claude's recorded test item in the dev copy")

                (project / "frontend" / "src" / "App.jsx").write_text(
                    (project / "frontend" / "src" / "App.jsx").read_text(encoding="utf-8") + "\n// v2\n", encoding="utf-8")
                again = await _tool(s, "deploy_application", {"application_id": app_id, "target_environment": "dev",
                                                             "project_path": str(project), "run_tests": False})
                _, items = _http("GET", f"{GATEWAY}/{name}-dev/api/items")
                _check(again["data"].get("created") is False and again["data"]["dev_copy"]["name"] == f"{name}-dev"
                       and _titles(items) == sorted([REAL_ITEM, TEST_ITEM]),
                       "the next dev deployment: the same copy and address, its data still there")

                # A service added to a dev copy that holds data: its
                # deployment.yaml is revised in place, nothing reset.
                shutil.copytree(project / "api", project / "worker")
                _, parent = call(BASE, "GET", f"/applications/{app_id}", token=owner)
                with_worker = parent["deployment_yaml_draft"].replace(
                    "database:", "  worker:\n    runtime: go\n    port: 8080\ndatabase:", 1)
                v = await _tool(s, "validate_application", {"application_id": app_id, "deployment_yaml": with_worker})
                _check(v["data"]["validation_result"]["passed"] is True, "the application's deployment.yaml gains a worker service")
                added = await _tool(s, "deploy_application", {"application_id": app_id, "target_environment": "dev",
                                                             "project_path": str(project), "run_tests": False})
                _, items = _http("GET", f"{GATEWAY}/{name}-dev/api/items")
                status, worker = _http("GET", f"{GATEWAY}/{name}-dev/worker/")
                _check(added["status"] == "success" and added["data"].get("created") is False
                       and added["data"].get("status") == "running"
                       and _titles(items) == sorted([REAL_ITEM, TEST_ITEM]) and status == 200
                       and (worker or {}).get("database") == "connected",
                       f"the dev copy took the new service in place: worker answers ({status}), the data is still there")

                asked = await _tool(s, "deploy_application", {"application_id": app_id, "target_environment": "production",
                                                             "project_path": str(project), "run_tests": False})
                msg = (asked.get("error") or {}).get("message", "")
                _check(asked["status"] == "error" and "ASK the employee" in msg and TEST_ITEM in msg,
                       "the first production deployment asks about the data, naming Claude's test data")
                status, _ = call(BASE, "GET", f"/applications/{app_id}/builds/latest", token=owner)
                _check(status == 404, "…and builds nothing until answered")

                prod = await _tool(s, "deploy_application", {"application_id": app_id, "target_environment": "production",
                                                            "project_path": str(project), "run_tests": False,
                                                            "dev_data": "copy"})
                dep_id = (prod.get("data") or {}).get("deployment_id")
                _check(prod["data"].get("status") == "pending_approval", "dev_data=copy: production waits for approval")
                status, req = call(BASE, "GET", f"/approvals/{dep_id}", token=approver_key)
                _check(status == 200 and f"copy from {name}-dev" in req.get("dev_data", "") and TEST_ITEM in req["dev_data"],
                       "the approver sees where the data comes from and Claude's test data")
                _check(f"dev-data: copy from {name}-dev" in req.get("signing_template", ""),
                       "…and it is in what they sign")
                key = approver.load(key_path, passphrase)
                signed_at, signature = approver.sign(key, req["signing_template"], "approve", "")
                status, decided = call(BASE, "POST", f"/approvals/{dep_id}/decision", {
                    "decision": "approve", "reason": "", "signed_at": signed_at, "signature": signature}, token=approver_key)
                _check(status == 200 and decided["deployment"]["status"] == "running", f"approved and live ({status})")
                _, live = call(BASE, "GET", f"/deployments/{dep_id}", token=owner)
                _check(bool(live.get("dev_data_copied_at")) and not live.get("dev_data_copy_error"),
                       "the copy is recorded as done")
                _, prod_items = _http("GET", f"{GATEWAY}/{name}/api/items")
                _check(_titles(prod_items) == sorted([REAL_ITEM, TEST_ITEM]), f"production has the dev copy's data ({_titles(prod_items)})")
                _, dev_items = _http("GET", f"{GATEWAY}/{name}-dev/api/items")
                _check(_titles(dev_items) == sorted([REAL_ITEM, TEST_ITEM]), "the dev copy keeps its own data")

                second = await _tool(s, "deploy_application", {"application_id": app_id, "target_environment": "production",
                                                              "project_path": str(project), "run_tests": False,
                                                              "dev_data": "copy"})
                _check(second["status"] == "error" and "only by the first" in second["error"]["message"],
                       "a later production deployment can't copy again (MCP)")
                status, body = call(BASE, "POST", f"/applications/{app_id}/deploy", {
                    "environment": "production", "copy_dev_data": True, "review_document": "# x"}, token=owner)
                _check(status == 409 and body["error"]["code"] == "dev_data_not_copyable", f"…nor through the API ({status})")

                # Running production's deployment.yaml: revised in place, but
                # never its database.
                _, parent = call(BASE, "GET", f"/applications/{app_id}", token=owner)
                status, body = call(BASE, "PUT", f"/applications/{app_id}/deployment-yaml", {
                    "deployment_yaml": parent["deployment_yaml_draft"].replace("type: mysql", "type: postgres")}, token=owner)
                _check(status == 409 and body["error"]["code"] == "database_change_loses_data",
                       f"changing a deployed application's database type is refused ({status})")
                scaled = parent["deployment_yaml_draft"].replace("max: 1", "max: 2")
                v = await _tool(s, "validate_application", {"application_id": app_id, "deployment_yaml": scaled})
                _, after = call(BASE, "GET", f"/applications/{app_id}", token=owner)
                _check(v["data"]["validation_result"]["passed"] is True and after["lifecycle_status"] == "running"
                       and "max: 2" in after["deployment_yaml_draft"],
                       "a running production's deployment.yaml is revised in place, still running")

                reset = await _tool(s, "reset_dev", {"application_id": app_id})
                _check(reset["status"] == "success" and not _db_containers(f"{name}-dev"),
                       "reset_dev removes the copy and its database")
                _, prod_items = _http("GET", f"{GATEWAY}/{name}/api/items")
                _check(len(prod_items or []) == 2, "production's data is untouched")
                new = await _tool(s, "deploy_application", {"application_id": app_id, "target_environment": "dev",
                                                           "project_path": str(project), "run_tests": False})
                _, new_items = _http("GET", f"{GATEWAY}/{name}-dev/api/items")
                new_copy = new["data"]["dev_copy"]
                _check(new_copy["name"] == f"{name}-dev" and new_copy["application_id"] != again["data"]["dev_copy"]["application_id"]
                       and new_items == [],
                       f"the next dev deployment makes a new, empty copy at the same address ({new_copy['name']}, {new_items})")

                new_copy_id = new["data"]["dev_copy"]["application_id"]
                status, _ = call(BASE, "POST", f"/applications/{app_id}/archive", {}, token=owner)
                _check(status == 200 and _wait_deleted(new_copy_id, owner), "archiving the application removes its dev copy")
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
    print("All dev copy checks passed.")


if __name__ == "__main__":
    asyncio.run(main())
