"""End-to-end verification of development governance against a live stack,
through the real MCP server over stdio (as Claude Code runs it):

    docker compose up -d --build        # from the repo root
    cd services/mcp-server
    .venv/Scripts/python.exe scripts/verify_governance.py

platform-api is restarted with PLATFORM_ADMIN_EMAILS set to a throwaway
administrator made here, and restarted without it at the end. Don't run it
alongside another script that restarts platform-api. Checks:

  - get_standards, and start_project refusing a stack the standard doesn't allow
  - start_project registers the application with its profile; scaffold_project
    lays out the company templates
  - only an administrator adds a dependency rule; the rule here forbids the
    templates' own MySQL driver, so the unmodified template is what's blocked
  - check_project reports it, and the real build fails on it as the source's
    problem, before anything is built
  - request_exception: the administrator is notified; the owner can't decide
    it; a rejection needs a reason; approving it notifies the owner
  - with the exception approved, check_project passes and the same project
    builds and deploys for real
  - an application registered without a profile can't be built
"""

from __future__ import annotations

import asyncio
import json
import os
import subprocess
import sys
import tempfile
import time
import urllib.request
import uuid
from pathlib import Path

from mcp import ClientSession
from mcp.client.stdio import StdioServerParameters, stdio_client

REPO = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(REPO / "services" / "platform-api" / "scripts"))
from platform_auth import api_key_for, call  # noqa: E402

BASE = os.environ.get("PLATFORM_API_BASE_URL", "http://localhost:8099")
SUFFIX = uuid.uuid4().hex[:5]
ADMIN = f"gov-admin-{SUFFIX}@sti-th.com"
EMPLOYEE = f"gov-dev-{SUFFIX}@sti-th.com"
DRIVER = "github.com/go-sql-driver/mysql"
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


async def _tool(session: ClientSession, name: str, args: dict) -> dict:
    result = await session.call_tool(name, args)
    payload = result.structured_content or {}
    shown = json.dumps(payload, ensure_ascii=False, default=str)
    print(f"  {name} -> {shown[:400]}{'…' if len(shown) > 400 else ''}", flush=True)
    return payload


def _titles(token: str) -> list[str]:
    _, body = call(BASE, "GET", "/notifications", token=token)
    return [n["title"] for n in body.get("notifications", []) if n.get("category") == "standard_exception"]


async def main() -> None:
    _restart({"PLATFORM_ADMIN_EMAILS": ADMIN})
    admin = api_key_for(BASE, ADMIN, "governance verify (admin)")
    owner = api_key_for(BASE, EMPLOYEE, "governance verify")
    creds = Path(tempfile.mkdtemp()) / "credentials.json"
    creds.write_text(json.dumps({"platform_url": BASE, "api_key": owner, "email": EMPLOYEE, "key_id": "",
                                 "key_name": "governance", "expires_at": ""}))
    params = StdioServerParameters(command=sys.executable, args=["-m", "mcp_server.server"], env={
        **os.environ, "PLATFORM_API_BASE_URL": BASE, "COMPANY_DEPLOY_CREDENTIALS": str(creds),
        "COMPANY_DEPLOY_TEMPLATES": str(REPO / "company-deployment-skill" / "templates"),
    })
    name = f"gov-{SUFFIX}"
    apps: list[str] = []
    rule_id = None
    try:
        async with stdio_client(params) as (read, write):
            async with ClientSession(read, write) as s:
                await s.initialize()
                tools = {t.name for t in (await s.list_tools()).tools}
                _check({"get_standards", "start_project", "scaffold_project", "check_project", "request_exception",
                        "list_exceptions"} <= tools, "the six governance tools are in the same MCP server")

                st = await _tool(s, "get_standards", {})
                _check(st["status"] == "success" and "go" in st["data"]["allowed_stacks"]["backend"]
                       and st["data"]["profile_required_to_build"] is True, "get_standards: the allowed stacks and the rules")

                bad = await _tool(s, "start_project", {"name": name, "purpose": "x", "handles_personal_data": False,
                                                       "api_runtime": "php"})
                _check(bad["status"] == "error" and "request_exception" in json.dumps(bad["error"]),
                       "start_project refuses a stack the standard doesn't allow, and says how to ask")
                _, listed = call(BASE, "GET", "/applications", token=owner)
                _check(not any(a["name"] == name for a in listed.get("applications", [])), "…and registers nothing")

                started = await _tool(s, "start_project", {
                    "name": name, "purpose": "คำขอทำงานล่วงเวลา (verification)", "handles_personal_data": True,
                    "users": "HR, about 20 people", "data_description": "employee IDs and names", "api_runtime": "go",
                })
                app_id = started.get("data", {}).get("application_id")
                if app_id:
                    apps.append(app_id)
                _check(started["status"] == "success" and started["data"]["registered"], "start_project registers it")
                _, prof = call(BASE, "GET", f"/applications/{app_id}/profile", token=admin)
                _check((prof.get("profile") or {}).get("purpose", "").startswith("คำขอ")
                       and prof["profile"]["handles_personal_data"] is True,
                       "the administrator reads the profile, Thai intact")

                project = Path(tempfile.mkdtemp()) / name
                sc = await _tool(s, "scaffold_project", {"application_id": app_id, "project_path": str(project)})
                files = sc.get("data", {}).get("files", [])
                _check(sc["status"] == "success" and "api/main.go" in files and "frontend/src/App.jsx" in files
                       and "REVIEW.md" in files and "deployment.yaml" in files, "scaffold_project lays out the templates")

                status, _ = call(BASE, "POST", "/standards/dependency-rules",
                                 {"ecosystem": "go", "package": DRIVER, "reason": "verification"}, token=owner)
                _check(status == 403, f"an employee can't add a dependency rule ({status})")
                status, added = call(BASE, "POST", "/standards/dependency-rules",
                                     {"ecosystem": "go", "package": DRIVER, "reason": "verification: blocks the template's driver"},
                                     token=admin)
                rule_id = (added.get("dependency_rule") or {}).get("id")
                _check(status == 201 and rule_id, f"the administrator adds one ({status})")

                chk = await _tool(s, "check_project", {"application_id": app_id, "project_path": str(project)})
                deps = [f for f in chk.get("data", {}).get("findings", []) if f["check"] == "dependencies"]
                _check(chk["data"]["passed"] is False and len(deps) == 1 and DRIVER in deps[0]["message"],
                       "check_project finds the library the company doesn't allow")

                v = await _tool(s, "validate_application", {"application_id": app_id})
                _check(v["data"]["validation_result"]["passed"] is True, "validate_application passes")
                failed = await _tool(s, "deploy_application", {"application_id": app_id, "target_environment": "dev",
                                                              "project_path": str(project)})
                _check(failed["status"] == "error" and DRIVER in failed["error"]["message"]
                       and "request_exception" in failed["error"]["message"],
                       "the real build fails on it, naming the library and what to do")
                # Dev deployments build on the application's dev copy.
                _, devc = call(BASE, "GET", f"/applications/{app_id}/dev", token=owner)
                copy_id = (devc.get("dev_copy") or {}).get("application_id")
                _, build = call(BASE, "GET", f"/applications/{copy_id}/builds/latest", token=owner)
                _check(build.get("status") == "failed" and build.get("error_category") == "source",
                       f"…as the source's problem ({build.get('status')}, {build.get('error_category')})")

                req = await _tool(s, "request_exception", {"application_id": app_id, "kind": "dependency",
                                                           "subject": f"go:{DRIVER}", "reason": "the MySQL driver"})
                exc_id = (req.get("data", {}).get("exception") or {}).get("id")
                _check(req["status"] == "success" and req["data"]["created"] and req["data"]["exception"]["status"] == "pending",
                       "request_exception asks the administrator")
                _check(any(name in t for t in _titles(admin)), "the administrator is notified")
                status, _ = call(BASE, "POST", f"/standards/exceptions/{exc_id}/decision", {"decision": "approve"}, token=owner)
                _check(status == 403, f"the owner can't decide their own exception ({status})")
                status, _ = call(BASE, "POST", f"/standards/exceptions/{exc_id}/decision", {"decision": "reject", "reason": " "},
                                 token=admin)
                _check(status == 400, f"a rejection needs a reason ({status})")
                status, decided = call(BASE, "POST", f"/standards/exceptions/{exc_id}/decision", {"decision": "approve"},
                                       token=admin)
                _check(status == 200 and decided["exception"]["status"] == "approved"
                       and decided["exception"]["decided_by"] == ADMIN, "the administrator approves it")
                _check(any("approved" in t for t in _titles(owner)), "the owner hears the decision")

                chk = await _tool(s, "check_project", {"application_id": app_id, "project_path": str(project)})
                _check(not [f for f in chk["data"]["findings"] if f["check"] == "dependencies"]
                       and any(d["package"] == DRIVER for d in chk["data"]["allowed_by_exception"]),
                       "check_project: allowed by the exception now")
                started_at = time.time()
                ok = await _tool(s, "deploy_application", {"application_id": app_id, "target_environment": "dev",
                                                          "project_path": str(project)})
                _check(ok["status"] == "success" and ok["data"].get("status") == "running",
                       f"the same project builds and deploys for real ({time.time() - started_at:.0f}s)")

                bare = await _tool(s, "create_application", {
                    "name": f"{name}-bare", "description": "no profile",
                    "deployment_yaml": f"app:\n  name: {name}-bare\n  owner: IT\nservices:\n  api:\n    runtime: go\n    port: 8080\n",
                })
                bare_id = bare.get("data", {}).get("application_id")
                if bare_id:
                    apps.append(bare_id)
                await _tool(s, "validate_application", {"application_id": bare_id})
                refused = await _tool(s, "deploy_application", {"application_id": bare_id, "target_environment": "dev",
                                                               "project_path": str(project)})
                _check(refused["status"] == "error" and "start_project" in refused["error"]["message"],
                       "an application with no profile can't be built, and the error says what to do")
                _, devc = call(BASE, "GET", f"/applications/{bare_id}/dev", token=owner)
                bare_copy = (devc.get("dev_copy") or {}).get("application_id")
                status, _ = call(BASE, "GET", f"/applications/{bare_copy}/builds/latest", token=owner)
                _check(bare_copy and status == 404, f"…and no build was attempted ({status})")
    finally:
        for app_id in apps:
            call(BASE, "POST", f"/applications/{app_id}/archive", {}, token=owner)
            status, _ = call(BASE, "POST", f"/applications/{app_id}/delete", {"confirm": True}, token=owner)
            print(f"cleanup: delete {app_id} -> {status}")
        if rule_id:
            status, _ = call(BASE, "DELETE", f"/standards/dependency-rules/{rule_id}", token=admin)
            print(f"cleanup: remove the rule -> {status}")
        _restart({})

    print()
    if _failures:
        print(f"{len(_failures)} check(s) FAILED:")
        for f in _failures:
            print("  -", f)
        raise SystemExit(1)
    print("All governance checks passed.")


if __name__ == "__main__":
    asyncio.run(main())
