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
workaround, this is the actual intended path now.
"""

from __future__ import annotations

import asyncio
import base64
import json
import os
import sys
import tarfile
import tempfile
from pathlib import Path

import httpx
from mcp import ClientSession
from mcp.client.stdio import StdioServerParameters, stdio_client

PLATFORM_API_BASE_URL = os.environ.get("PLATFORM_API_BASE_URL", "http://localhost:8090")
EMPLOYEE_EMAIL = "mcp-e2e@sti-th.com"


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


async def main() -> None:
    server_params = StdioServerParameters(
        command=sys.executable,
        args=["-m", "mcp_server.server"],
        env={
            **os.environ,
            "MCP_ENV": "dev",
            "PLATFORM_API_BASE_URL": PLATFORM_API_BASE_URL,
            "MCP_EMPLOYEE_EMAIL": EMPLOYEE_EMAIL,
            "MCP_EMPLOYEE_NAME": "MCP E2E Tester",
            "MCP_EMPLOYEE_DEPARTMENT": "Engineering",
        },
    )

    async with stdio_client(server_params) as (read, write):
        async with ClientSession(read, write) as session:
            await session.initialize()
            tools = await session.list_tools()
            tool_names = sorted(t.name for t in tools.tools)
            _check(len(tool_names) == 13, f"all 13 Section-13 tools discovered via MCP protocol: {tool_names}")

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
                    {"name": "mcptest", "description": "MCP e2e test app", "department": "Nonexistent"},
                ),
            )
            _check(created["status"] == "error" and created["error"]["code"] == "VALIDATION_ERROR", "unknown department rejected")

            created = _print_result(
                "create_application",
                await session.call_tool(
                    "create_application",
                    {
                        "name": "mcptest",
                        "description": "MCP e2e test app",
                        "department": "Engineering",
                        "deployment_yaml": (
                            "app:\n  name: mcptest\n  owner: Engineering\n"
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
                "get_application_logs (expected honest not-implemented error)",
                await session.call_tool("get_application_logs", {"application_id": app_id, "environment": "dev"}),
            )
            _check(logs["status"] == "error" and logs["error"]["code"] == "INTERNAL_ERROR" and "Module S" in logs["error"]["message"], "logs tool honestly reports Module S doesn't exist")

            metrics = _print_result(
                "get_application_metrics (expected honest not-implemented error)",
                await session.call_tool("get_application_metrics", {"application_id": app_id, "environment": "dev"}),
            )
            _check(metrics["status"] == "error" and "Module T" in metrics["error"]["message"], "metrics tool honestly reports Module T doesn't exist")

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
                    "delete_application", {"application_id": app_id, "confirmation": "mcptest"}
                ),
            )
            _check(deleted["status"] == "success" and deleted["data"]["status"] == "DELETED", "delete_application succeeded (archived then deleted)")

            final_status = _print_result(
                "get_application_status (after delete)",
                await session.call_tool("get_application_status", {"application_id": app_id}),
            )
            _check(final_status["data"]["current_lifecycle_state"] == "deleted", "application is terminally deleted")

    print("\nALL CHECKS PASSED")


if __name__ == "__main__":
    asyncio.run(main())
