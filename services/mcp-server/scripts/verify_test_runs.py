"""End-to-end verification of tests run by the platform (DEC-029 part 2)
against a live stack, through the real MCP server over stdio:

    docker compose up -d --build        # from the repo root
    cd services/mcp-server
    .venv/Scripts/python.exe scripts/verify_test_runs.py

platform-api is restarted with a throwaway approver key (made here with
`company-deploy`'s own code) and production needing approval, and
restarted without them at the end. Don't run it alongside another script
that restarts platform-api. Checks:

  - run_tests on the scaffolded React + Go project: go vet, go test, the
    React build, "no tests to run" said as such
  - the tests run without the Docker socket (a test checks for it)
  - a failing test: run_tests reports it; deploy_application builds nothing
  - a second run while one is running is refused, not queued
  - fixed: deploy_application runs the tests on what it builds and deploys
  - a passed run of other source is refused for a build of changed source
  - production: the approval request carries the platform's report, with
    the requester's notes after it, bound into what the approver signs
"""

from __future__ import annotations

import asyncio
import json
import os
import subprocess
import sys
import tempfile
import threading
import time
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
SUFFIX = uuid.uuid4().hex[:5]
OWNER = f"tests-dev-{SUFFIX}@sti-th.com"
APPROVER = f"tests-approver-{SUFFIX}@sti-th.com"
_failures: list[str] = []

PASSING_TEST = '''package main

import (
	"os"
	"testing"
)

func TestNoDockerSocket(t *testing.T) {
	if _, err := os.Stat("/var/run/docker.sock"); err == nil {
		t.Fatal("the Docker socket is reachable from a test run")
	}
}

func TestThaiName(t *testing.T) {
	if len([]rune("วันลา")) != 5 {
		t.Fatal("วันลา")
	}
}
'''
FAILING_TEST = '''package main

import "testing"

func TestLeaveDays(t *testing.T) {
	if 1+1 != 3 {
		t.Fatal("วันลาคำนวณผิด: expected 3")
	}
}
'''


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
    payload = (await session.call_tool(name, args)).structured_content or {}
    shown = json.dumps(payload, ensure_ascii=False, default=str)
    print(f"  {name} -> {shown[:300]}{'…' if len(shown) > 300 else ''}", flush=True)
    return payload


async def main() -> None:
    tmp = Path(tempfile.mkdtemp(prefix="test-runs-"))
    key_line = approver.public_key_line(APPROVER, approver.generate(tmp / "approver.pem", b"throwaway-passphrase"))
    _restart({"PLATFORM_APPROVER_KEYS": key_line, "APPROVAL_REQUIRED_ENVIRONMENTS": "production"})
    owner = api_key_for(BASE, OWNER, "test runs verify")
    approver_key = api_key_for(BASE, APPROVER, "test runs verify (approver)")
    creds = tmp / "credentials.json"
    creds.write_text(json.dumps({"platform_url": BASE, "api_key": owner, "email": OWNER, "key_id": "",
                                 "key_name": "tests", "expires_at": ""}))
    params = StdioServerParameters(command=sys.executable, args=["-m", "mcp_server.server"], env={
        **os.environ, "PLATFORM_API_BASE_URL": BASE, "COMPANY_DEPLOY_CREDENTIALS": str(creds),
        "COMPANY_DEPLOY_TEMPLATES": str(REPO / "company-deployment-skill" / "templates"),
    })
    name = f"tests-{SUFFIX}"
    app_id = None
    pending_id = None
    try:
        async with stdio_client(params) as (read, write):
            async with ClientSession(read, write) as s:
                await s.initialize()
                _check("run_tests" in {t.name for t in (await s.list_tools()).tools}, "run_tests is a tool")
                started = await _tool(s, "start_project", {"name": name, "purpose": "test runs verification",
                                                           "handles_personal_data": False, "api_runtime": "go"})
                app_id = started["data"]["application_id"]
                project = tmp / name
                await _tool(s, "scaffold_project", {"application_id": app_id, "project_path": str(project),
                                                    "deploy": False})
                (project / "api" / "main_test.go").write_text(PASSING_TEST, encoding="utf-8")

                t0 = time.time()
                run = await _tool(s, "run_tests", {"application_id": app_id, "project_path": str(project)})
                report = run["data"]["report"]
                print("---- report ----\n" + report[:3000] + "\n----------------")
                _check(run["data"]["passed"] is True, f"run_tests passes on the scaffolded project ({time.time() - t0:.0f}s)")
                _check("Tests run by the platform" in report and "== go vet ./..." in report and "== go test ./..." in report
                       and "ok" in report, "the report shows go vet and go test ran")
                _check("== npm run build" in report and "no tests to run" in report,
                       "the React build ran, and 'no tests to run' is said as such")
                _check({x["service"]: x["status"] for x in run["data"]["services"]} == {"api": "passed", "frontend": "passed"},
                       "each service's result")

                (project / "api" / "leave_test.go").write_text(FAILING_TEST, encoding="utf-8")
                failed = await _tool(s, "run_tests", {"application_id": app_id, "project_path": str(project)})
                _check(failed["data"]["status"] == "failed" and "วันลาคำนวณผิด" in failed["data"]["report"]
                       and "--- FAIL: TestLeaveDays" in failed["data"]["report"],
                       "a failing test fails the run, with its output, Thai intact")
                await _tool(s, "validate_application", {"application_id": app_id})
                refused = await _tool(s, "deploy_application", {"application_id": app_id, "target_environment": "dev",
                                                               "project_path": str(project), "run_tests": True})
                _check(refused["status"] == "error" and "nothing was built" in refused["error"]["message"]
                       and "TestLeaveDays" in refused["error"]["message"], "deploy_application refuses, showing the failure")
                status, _ = call(BASE, "GET", f"/applications/{app_id}/builds/latest", token=owner)
                _check(status == 404, f"…and built nothing ({status})")

                # Two at once: the second is refused while the first runs.
                import io
                import tarfile
                buf = io.BytesIO()
                with tarfile.open(fileobj=buf, mode="w:gz") as tar:
                    for f in sorted((project / "api").iterdir()):
                        tar.add(f, arcname=f"api/{f.name}")
                    for f in (project / "frontend").rglob("*"):
                        if f.is_file() and "node_modules" not in f.parts:
                            tar.add(f, arcname="frontend/" + f.relative_to(project / "frontend").as_posix())
                archive = buf.getvalue()

                def first():
                    req = urllib.request.Request(f"{BASE}/applications/{app_id}/test-runs", data=archive, method="POST",
                                                 headers={"Authorization": f"Bearer {owner}", "Content-Type": "application/gzip"})
                    try:
                        urllib.request.urlopen(req, timeout=900).read()
                    except Exception:
                        pass
                th = threading.Thread(target=first)
                th.start()
                time.sleep(2)
                req = urllib.request.Request(f"{BASE}/applications/{app_id}/test-runs", data=archive, method="POST",
                                             headers={"Authorization": f"Bearer {owner}", "Content-Type": "application/gzip"})
                try:
                    urllib.request.urlopen(req, timeout=60)
                    second = 200
                except urllib.error.HTTPError as e:
                    second = e.code
                th.join()
                _check(second == 429, f"a second run while one runs is refused, not queued ({second})")

                (project / "api" / "leave_test.go").unlink()
                t0 = time.time()
                dev = await _tool(s, "deploy_application", {"application_id": app_id, "target_environment": "dev",
                                                           "project_path": str(project), "run_tests": True})
                passed_id = dev.get("data", {}).get("test_run_id")
                _check(dev["status"] == "success" and dev["data"]["status"] == "running" and passed_id,
                       f"fixed: tested on the platform, built and running ({time.time() - t0:.0f}s)")

                (project / "api" / "main.go").write_text(
                    (project / "api" / "main.go").read_text(encoding="utf-8") + "\n// changed after the tests\n", encoding="utf-8")
                # Production (dev goes to the dev copy, which carries no report).
                mismatch = await _tool(s, "deploy_application", {"application_id": app_id, "target_environment": "production",
                                                                "project_path": str(project), "test_run_id": passed_id,
                                                                "dev_data": "leave"})
                _check(mismatch["status"] == "error" and "different source" in mismatch["error"]["message"],
                       "a passed run of other source isn't accepted for a build of changed source")

                t0 = time.time()
                dep = await _tool(s, "deploy_application", {"application_id": app_id, "target_environment": "production",
                                                           "project_path": str(project), "test_report": "ทดสอบเพิ่มเองบนเครื่อง",
                                                           "dev_data": "leave"})
                dep_id = dep.get("data", {}).get("deployment_id")
                pending_id = dep_id
                _check(dep["data"].get("status") == "pending_approval" and dep["data"].get("test_run_id"),
                       f"production: tested again on what it built, and waiting for approval ({time.time() - t0:.0f}s)")
                status, req_body = call(BASE, "GET", f"/approvals/{dep_id}", token=approver_key)
                tr = req_body.get("test_report", "")
                _check(status == 200 and tr.startswith("Tests run by the platform")
                       and f"test run: {dep['data']['test_run_id']}" in tr and "\x1b[" not in tr,
                       "the approver sees the platform's report, without terminal codes")
                _check("--- from the requester (not run by the platform) ---\nทดสอบเพิ่มเองบนเครื่อง" in tr,
                       "the requester's own notes come after it, marked as theirs")
                _check(f"test-report-sha256: {req_body.get('test_report_sha256')}" in req_body.get("signing_template", ""),
                       "the report is bound into what the approver signs")
                # Signed with the throwaway key, so the application can be deleted.
                key = approver.load(tmp / "approver.pem", b"throwaway-passphrase")
                signed_at, signature = approver.sign(key, req_body["signing_template"], "reject", "verification cleanup")
                status, _ = call(BASE, "POST", f"/approvals/{dep_id}/decision", {
                    "decision": "reject", "reason": "verification cleanup", "signed_at": signed_at, "signature": signature,
                }, token=approver_key)
                if status == 200:
                    pending_id = None
    finally:
        if pending_id:
            print("cleanup: a production deployment is still waiting; reject it before deleting", pending_id)
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
    print("All test-run checks passed.")


if __name__ == "__main__":
    asyncio.run(main())
