"""Real, manual end-to-end verification: spawns the actual MCP server as a
subprocess (stdio transport, exactly how Claude Code would launch it) and
drives it through a full workflow via a real MCP client session, against a
REAL running Platform API (docker compose). Not part of the pytest suite —
run manually:

    docker compose up -d --build   # from repo root, with .env present
    cd services/mcp-server
    python scripts/e2e_verify.py

Exercises deploy_application's source_archive_base64 path end to end,
including a REBUILD of a running application (a real gap that used to make
this impossible through the MCP, closed alongside this script) — not a
workaround, this is the actual intended path now. Also checks Module O's
boundary: secret names are visible through get_application_status, a
value never is, and no tool takes a parameter that could carry one.
"""

from __future__ import annotations

import asyncio
import base64
import json
import os
import sys
import tarfile
import tempfile
import uuid
from pathlib import Path

import httpx
from mcp import ClientSession
from mcp.client.stdio import StdioServerParameters, stdio_client

PLATFORM_API_BASE_URL = os.environ.get("PLATFORM_API_BASE_URL", "http://localhost:8090")
EMPLOYEE_EMAIL = "mcp-e2e@sti-th.com"
# Signs in to the platform but owns nothing: what a non-owner's agent sees.
OTHER_EMPLOYEE_EMAIL = "mcp-e2e-other@sti-th.com"
# Unique per run: a deleted application keeps its name, so a fixed name
# made this script single-use against any one database.
APP_NAME = f"mcptest{uuid.uuid4().hex[:6]}"


def _fail(msg: str) -> None:
    print(f"FAIL: {msg}", file=sys.stderr)
    raise SystemExit(1)


def _check(cond: bool, msg: str) -> None:
    if not cond:
        _fail(msg)
    print(f"OK: {msg}")


def _source_archive_base64(main_go_body: str) -> str:
    with tempfile.TemporaryDirectory() as tmp:
        api_dir = Path(tmp) / "api"
        api_dir.mkdir()
        (api_dir / "main.go").write_text(main_go_body)
        (api_dir / "go.mod").write_text("module mcptest\n\ngo 1.25\n")
        archive_path = Path(tmp) / "src.tar.gz"
        with tarfile.open(archive_path, "w:gz") as tar:
            tar.add(api_dir, arcname="api")
        return base64.b64encode(archive_path.read_bytes()).decode()


_V1_SOURCE = (
    'package main\n\nimport (\n\t"fmt"\n\t"net/http"\n)\n\nfunc main() {\n'
    '\tfmt.Println("mcptest v1 listening on :8080")\n'
    '\thttp.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {\n'
    '\t\tfmt.Fprintln(w, "hello from mcptest v1")\n\t})\n'
    '\thttp.ListenAndServe(":8080", nil)\n}\n'
)
_V2_SOURCE = _V1_SOURCE.replace("v1", "v2")
_BROKEN_SOURCE = "package main\n\nthis is not valid go at all\n"


def _print_result(label: str, result) -> dict:
    payload = result.structured_content or {}
    print(f"--- {label} ---")
    print(json.dumps(payload, indent=2, default=str)[:1500])
    if result.is_error:
        _fail(
            f"{label} returned an MCP protocol-level error (should never happen — "
            f"Section 8 wants data-level errors, not transport faults): {payload}"
        )
    return payload


def _server_params(employee_email: str) -> StdioServerParameters:
    return StdioServerParameters(
        command=sys.executable,
        args=["-m", "mcp_server.server"],
        env={
            **os.environ,
            "MCP_ENV": "dev",
            "PLATFORM_API_BASE_URL": PLATFORM_API_BASE_URL,
            "MCP_EMPLOYEE_EMAIL": employee_email,
            "MCP_EMPLOYEE_NAME": "MCP E2E Tester",
            "MCP_EMPLOYEE_DEPARTMENT": "Engineering",
        },
    )


async def main() -> None:
    server_params = _server_params(EMPLOYEE_EMAIL)

    async with stdio_client(server_params) as (read, write):
        async with ClientSession(read, write) as session:
            await session.initialize()
            tools = await session.list_tools()
            tool_names = sorted(t.name for t in tools.tools)
            _check(
                len(tool_names) == 21,
                f"all 13 Section-13 tools plus the 8 later-module tools (Modules W/X/AB/E) "
                f"discovered via MCP protocol: {tool_names}",
            )

            info = _print_result("get_platform_info", await session.call_tool("get_platform_info", {}))
            _check(info["status"] == "success", "get_platform_info succeeded")

            stacks = _print_result(
                "get_supported_stacks", await session.call_tool("get_supported_stacks", {"category": "backend"})
            )
            _check(any(s["runtime"] == "go" for s in stacks["data"]["stacks"]), "go is a supported backend runtime")

            reqs = _print_result(
                "get_deployment_requirements", await session.call_tool("get_deployment_requirements", {})
            )
            _check(reqs["status"] == "success", "get_deployment_requirements succeeded with no application_id")

            created = _print_result(
                "create_application (unknown department -> VALIDATION_ERROR)",
                await session.call_tool(
                    "create_application",
                    {"name": APP_NAME, "description": "MCP e2e test app", "department": "Nonexistent"},
                ),
            )
            _check(created["status"] == "error" and created["error"]["code"] == "VALIDATION_ERROR", "unknown department rejected")

            created = _print_result(
                "create_application",
                await session.call_tool(
                    "create_application",
                    {
                        "name": APP_NAME,
                        "description": "MCP e2e test app",
                        "department": "Engineering",
                        "deployment_yaml": (
                            "app:\n  name: " + APP_NAME + "\n  owner: Engineering\n"
                            "services:\n  api:\n    runtime: go\n    port: 8080\n"
                        ),
                    },
                ),
            )
            _check(created["status"] == "success", "create_application succeeded")
            app_id = created["data"]["application_id"]

            validated = _print_result(
                "validate_application", await session.call_tool("validate_application", {"application_id": app_id})
            )
            _check(validated["data"]["validation_result"]["passed"] is True, "validate_application passed")
            _check(validated["data"]["lifecycle_state"] == "validated", "application moved to validated")

            print("--- deploying v1: build + deploy in ONE MCP call via source_archive_base64 (the closed gap) ---")
            deployed = _print_result(
                "deploy_application (v1, dev, with source)",
                await session.call_tool(
                    "deploy_application",
                    {
                        "application_id": app_id,
                        "target_environment": "dev",
                        "source_archive_base64": _source_archive_base64(_V1_SOURCE),
                    },
                ),
            )
            _check(deployed["status"] == "success" and deployed["data"]["status"] == "running", "v1 built and deployed via a single MCP call")
            v1_deployment_id = deployed["data"]["deployment_id"]

            status = _print_result(
                "get_application_status", await session.call_tool("get_application_status", {"application_id": app_id})
            )
            _check(status["data"]["current_lifecycle_state"] == "running", "application status reports running")
            _check(bool(status["data"]["url"]), "application status reports a live URL")
            async with httpx.AsyncClient(timeout=10) as http:
                live_v1 = await http.get(status["data"]["url"])
            _check("v1" in live_v1.text, f"live traffic serves v1's response: {live_v1.text.strip()!r}")

            print("--- Module O: the agent sees secret NAMES, never values, and cannot send one ---")
            params = [p for t in tools.tools for p in (t.input_schema or {}).get("properties", {})]
            risky = [p for p in params if any(w in p.lower() for w in ("secret", "password", "token", "value"))]
            _check(not risky, f"no tool takes a parameter that could carry a secret value (SEC-SECRET-3) {risky}")
            secret_value = f"sk-mcp-e2e-{uuid.uuid4().hex}"
            # Set the way SKILL.md sends the employee to: on the platform
            # directly, never through this MCP session.
            async with httpx.AsyncClient(timeout=30) as http:
                put = await http.put(
                    f"{PLATFORM_API_BASE_URL}/applications/{app_id}/secrets/PAYMENTS_API_KEY",
                    json={"value": secret_value},
                    headers={"X-Dev-User-Email": EMPLOYEE_EMAIL, "X-Dev-User-Name": "MCP E2E Tester",
                             "X-Dev-Department": "Engineering"},
                )
            _check(put.status_code == 200, f"the employee set PAYMENTS_API_KEY on the platform directly (HTTP {put.status_code})")
            raw = await session.call_tool("get_application_status", {"application_id": app_id})
            with_secret = _print_result("get_application_status (a secret registered)", raw)
            names = [s["name"] for s in with_secret["data"]["secrets"] or []]
            _check(names == ["PAYMENTS_API_KEY"], f"get_application_status lists the secret's name {names}")
            _check(secret_value not in json.dumps(raw.structured_content) and secret_value not in str(raw.content),
                   "...and its value appears nowhere in the MCP response")
            async with stdio_client(_server_params(OTHER_EMPLOYEE_EMAIL)) as (other_read, other_write):
                async with ClientSession(other_read, other_write) as other:
                    await other.initialize()
                    theirs = _print_result(
                        "get_application_status (an employee who isn't an owner)",
                        await other.call_tool("get_application_status", {"application_id": app_id}),
                    )
            _check(theirs["status"] == "success" and theirs["data"]["secrets"] is None
                   and bool(theirs["data"]["secrets_note"]),
                   "a non-owner's agent is told the names aren't visible to them - not that there are none")

            print("--- query_audit_log / list_notifications: beyond Section 13's original catalog ---")
            app_audit_entries = _print_result(
                "query_audit_log (resource_type=application, this application)",
                await session.call_tool("query_audit_log", {"resource_type": "application", "resource_id": app_id}),
            )
            app_actions_seen = {e["action"] for e in app_audit_entries["data"]["entries"]}
            _check(
                {"application.register", "application.validate"} <= app_actions_seen,
                f"audit log (application-scoped) shows register/validate for this real run: {sorted(app_actions_seen)}",
            )

            deploy_audit_entries = _print_result(
                "query_audit_log (resource_type=deployment, v1's deployment)",
                await session.call_tool(
                    "query_audit_log", {"resource_type": "deployment", "resource_id": v1_deployment_id}
                ),
            )
            deploy_actions_seen = {e["action"] for e in deploy_audit_entries["data"]["entries"]}
            _check(
                "deployment.deploy" in deploy_actions_seen,
                f"audit log (deployment-scoped, not application-scoped — deploy_service.go's "
                f"auditDeployOutcome records these under resource_type=deployment) shows deployment.deploy: "
                f"{sorted(deploy_actions_seen)}",
            )

            unread = _print_result(
                "list_notifications (unread_only)",
                await session.call_tool("list_notifications", {"unread_only": True}),
            )
            deploy_notifications = [
                n for n in unread["data"]["notifications"] if n["resource_type"] == "deployment" and n["resource_id"] == v1_deployment_id
            ]
            _check(len(deploy_notifications) == 1, f"real deployment_status notification for v1's deploy: {deploy_notifications}")

            marked = _print_result(
                "mark_notification_read", await session.call_tool("mark_notification_read", {"notification_id": deploy_notifications[0]["id"]})
            )
            _check(marked["data"]["read_at"] is not None, "mark_notification_read set a real read_at")

            unread_after = _print_result(
                "list_notifications (unread_only, after marking read)",
                await session.call_tool("list_notifications", {"unread_only": True}),
            )
            _check(
                all(n["id"] != deploy_notifications[0]["id"] for n in unread_after["data"]["notifications"]),
                "the marked-read notification no longer appears in the unread list",
            )

            print("--- Module AB (reporting) and Module E (ownership): also beyond Section 13 ---")
            inventory = _print_result(
                "get_application_inventory", await session.call_tool("get_application_inventory", {})
            )
            mine = [a for a in inventory["data"]["applications"] if a["application_id"] == app_id]
            _check(len(mine) == 1, "the inventory lists this employee's own application")
            _check(
                mine[0]["runtimes"] == ["go"] and mine[0]["environment"] == "dev",
                f"inventory read the real stack and environment off the live application: {mine[0]}",
            )
            _check(
                inventory["data"]["count"] == len(inventory["data"]["applications"]),
                "inventory count matches the rows returned",
            )

            activity = _print_result(
                "get_deployment_activity", await session.call_tool("get_deployment_activity", {})
            )
            _check(
                activity["data"]["total"]["succeeded"] >= 1,
                f"deployment activity counts this run's real deploy: {activity['data']['total']}",
            )
            _check("dev" in activity["data"]["by_environment"], "activity is broken down by the real environment")

            owners = _print_result(
                "list_application_owners", await session.call_tool("list_application_owners", {"application_id": app_id})
            )
            _check(
                any(o["ownership_role"] == "primary" and o["status"] == "active" for o in owners["data"]["owners"]),
                "the application reports its real active primary owner",
            )

            bad_grant = _print_result(
                "grant_application_access (invalid level -> VALIDATION_ERROR)",
                await session.call_tool(
                    "grant_application_access",
                    {"application_id": app_id, "email": "nobody@sti-th.com", "access_level": "primary"},
                ),
            )
            _check(
                bad_grant["status"] == "error" and bad_grant["error"]["code"] == "VALIDATION_ERROR",
                "granting 'primary' is rejected — it is never grantable, only assigned or transferred",
            )

            unknown_grant = _print_result(
                "grant_application_access (unknown email -> NOT_FOUND)",
                await session.call_tool(
                    "grant_application_access",
                    {"application_id": app_id, "email": "never-signed-in@sti-th.com", "access_level": "co_owner"},
                ),
            )
            _check(
                unknown_grant["status"] == "error" and unknown_grant["error"]["code"] == "NOT_FOUND",
                "granting access to an employee who has never signed in is rejected by the Platform API",
            )

            dep_status = _print_result(
                "get_deployment_status", await session.call_tool("get_deployment_status", {"deployment_id": v1_deployment_id})
            )
            _check(dep_status["data"]["phase"] == "COMPLETED", "deployment status phase is COMPLETED")
            _check(bool(dep_status["data"]["updated_at"]), "deployment status reports a real updated_at (was always null before this fix)")

            restarted = _print_result(
                "restart_application", await session.call_tool("restart_application", {"application_id": app_id})
            )
            _check(restarted["data"]["status"] == "COMPLETED", "restart_application completed")
            _check(bool(restarted["data"]["restarted_at"]), "restart_application reports a real restarted_at (was always null before this fix)")

            logs = _print_result(
                "get_application_logs (Module S)",
                await session.call_tool("get_application_logs", {"application_id": app_id, "environment": "dev"}),
            )
            logged = [e["message"] for e in (logs.get("data") or {}).get("entries", [])]
            _check(logs["status"] == "success" and any("mcptest v1 listening" in m for m in logged),
                   f"get_application_logs returns what the running application printed ({len(logged)} line(s))")

            # Traffic is counted where the platform can see it: Module L's
            # stable /run address. get_application_status reports the
            # container's own published port instead, which bypasses the
            # proxy entirely — so ask for the platform's address here.
            async with httpx.AsyncClient(timeout=30) as traffic_http:
                for _ in range(3):
                    await traffic_http.get(f"{PLATFORM_API_BASE_URL}/run/{APP_NAME}/api/")
            metrics_result = None
            for _ in range(20):  # the platform flushes what it counted on its own sampling interval
                metrics_result = await session.call_tool(
                    "get_application_metrics", {"application_id": app_id, "environment": "dev"}
                )
                counted = ((metrics_result.structured_content or {}).get("data") or {}).get("summary", {})
                if counted.get("requests", 0) >= 1:
                    break
                await asyncio.sleep(2)
            metrics = _print_result("get_application_metrics (Module T)", metrics_result)
            metric_data = metrics.get("data") or {}
            _check(metrics["status"] == "success" and "cpu_percent" in (metric_data.get("series") or {}),
                   "get_application_metrics returns the platform's own series for this application")
            _check(metric_data.get("summary", {}).get("requests", 0) >= 1,
                   f"including the requests this run made through the proxy ({metric_data.get('summary', {}).get('requests')})")
            bad_type = _print_result(
                "get_application_metrics (unknown metric type)",
                await session.call_tool("get_application_metrics",
                                        {"application_id": app_id, "environment": "dev", "metric_types": ["disk"]}),
            )
            _check(bad_type["status"] == "error" and bad_type["error"]["code"] == "VALIDATION_ERROR",
                   "an unsupported metric type is refused rather than silently ignored")

            print("--- deploying v2: REBUILD of an already-`running` application via the SAME MCP call shape ---")
            print("--- (this used to be entirely impossible - Build required Validated - now fixed at the source) ---")
            deployed_v2 = _print_result(
                "deploy_application (v2, dev, with source, app already running)",
                await session.call_tool(
                    "deploy_application",
                    {
                        "application_id": app_id,
                        "target_environment": "dev",
                        "source_archive_base64": _source_archive_base64(_V2_SOURCE),
                    },
                ),
            )
            _check(deployed_v2["status"] == "success" and deployed_v2["data"]["status"] == "running", "v2 rebuilt and deployed via MCP while app was already running")

            status_v2 = _print_result(
                "get_application_status (v2 live)",
                await session.call_tool("get_application_status", {"application_id": app_id}),
            )
            async with httpx.AsyncClient(timeout=10) as http:
                live_v2 = await http.get(status_v2["data"]["url"])
            _check("v2" in live_v2.text, f"live traffic serves v2's response: {live_v2.text.strip()!r}")

            print("--- source-category build failure: broken Go source rejected as VALIDATION_ERROR, not a crash ---")
            broken = _print_result(
                "deploy_application (broken source -> VALIDATION_ERROR)",
                await session.call_tool(
                    "deploy_application",
                    {
                        "application_id": app_id,
                        "target_environment": "dev",
                        "source_archive_base64": _source_archive_base64(_BROKEN_SOURCE),
                    },
                ),
            )
            _check(broken["status"] == "error" and broken["error"]["code"] == "VALIDATION_ERROR", "broken source rejected as VALIDATION_ERROR (source-category build failure)")

            status_after_broken = _print_result(
                "get_application_status (after failed rebuild attempt)",
                await session.call_tool("get_application_status", {"application_id": app_id}),
            )
            _check(status_after_broken["data"]["current_lifecycle_state"] == "running", "app stays Running after a failed rebuild attempt — v2 untouched")
            async with httpx.AsyncClient(timeout=10) as http:
                live_after_broken = await http.get(status_after_broken["data"]["url"])
            _check("v2" in live_after_broken.text, "traffic still serves v2 — the failed rebuild attempt never touched it")

            rolled_back = _print_result(
                "rollback_application (target_version='previous')",
                await session.call_tool(
                    "rollback_application", {"application_id": app_id, "target_version": "previous"}
                ),
            )
            _check(rolled_back["status"] == "success", "rollback_application succeeded")
            _check(rolled_back["data"]["target_version"] == v1_deployment_id, "rollback resolved 'previous' to v1's deployment id")

            status_after_rollback = _print_result(
                "get_application_status (after rollback)",
                await session.call_tool("get_application_status", {"application_id": app_id}),
            )
            async with httpx.AsyncClient(timeout=10) as http:
                live_after = await http.get(status_after_rollback["data"]["url"])
            _check("v1" in live_after.text, f"live traffic flipped back to v1's response after rollback: {live_after.text.strip()!r}")

            print("--- delete_application (wrong confirmation -> VALIDATION_ERROR) ---")
            bad_delete = _print_result(
                "delete_application (wrong confirmation)",
                await session.call_tool(
                    "delete_application", {"application_id": app_id, "confirmation": "not-the-name"}
                ),
            )
            _check(bad_delete["status"] == "error" and bad_delete["error"]["code"] == "VALIDATION_ERROR", "wrong confirmation rejected")

            deleted = _print_result(
                "delete_application",
                await session.call_tool(
                    "delete_application", {"application_id": app_id, "confirmation": APP_NAME}
                ),
            )
            _check(deleted["status"] == "success" and deleted["data"]["status"] == "DELETED", "delete_application succeeded (archived then deleted)")

            final_status = _print_result(
                "get_application_status (after delete)",
                await session.call_tool("get_application_status", {"application_id": app_id}),
            )
            _check(final_status["data"]["current_lifecycle_state"] == "deleted", "application is terminally deleted")
            _check(final_status["data"]["secrets"] == [], "its secrets were purged with it - status lists none")

    print("\nALL CHECKS PASSED")


if __name__ == "__main__":
    asyncio.run(main())
