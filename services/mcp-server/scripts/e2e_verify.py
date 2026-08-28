"""Real, manual end-to-end verification: spawns the actual MCP server as a
subprocess (stdio transport, exactly how Claude Code would launch it) and
drives it through a full workflow via a real MCP client session, against a
REAL running Platform API (docker compose). Not part of the pytest suite —
run manually:

    docker compose up -d --build   # from repo root, with .env present
    cd services/mcp-server
    python scripts/e2e_verify.py

Requires: a source archive already built for the app being deployed (see
this script's own BUILD step, which — same documented gap as
tools/deployment.py's _NO_BUILD_MESSAGE — goes around the MCP layer via
direct HTTP, since building isn't one of the 12 MCP tools).
"""

from __future__ import annotations

import asyncio
import json
import os
import subprocess
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


async def _build_via_direct_http(app_id: str, source: str) -> None:
    """Bypasses the MCP layer on purpose — see this module's doc comment."""
    with tempfile.TemporaryDirectory() as tmp:
        api_dir = Path(tmp) / "api"
        api_dir.mkdir()
        (api_dir / "main.go").write_text(source)
        (api_dir / "go.mod").write_text("module mcptest\n\ngo 1.25\n")
        archive_path = Path(tmp) / "src.tar.gz"
        with tarfile.open(archive_path, "w:gz") as tar:
            tar.add(api_dir, arcname="api")

        async with httpx.AsyncClient(timeout=120) as http:
            resp = await http.post(
                f"{PLATFORM_API_BASE_URL}/applications/{app_id}/build",
                headers={
                    "X-Dev-User-Email": EMPLOYEE_EMAIL,
                    "Content-Type": "application/gzip",
                },
                content=archive_path.read_bytes(),
            )
        body = resp.json()
        _check(resp.status_code == 200 and body.get("status") == "succeeded", f"direct build succeeded: {body}")


def _insert_v2_build_row(app_id: str, employee_user_id: str, image_ref: str) -> str:
    """Simulates a second Build-state run the way the equivalent Go
    verification for the Rollback PR did: inserts a `builds` row directly
    via psql. This is a STAND-IN for the real gap documented in
    tools/deployment.py's module doc (deploy_application requires an
    existing successful build, and there's no MCP-reachable way to trigger
    one for an already-`running` application) — not something an actual
    employee/agent could do, only how this script exercises
    rollback_application against two genuinely different real deployments.
    """
    out = subprocess.run(
        [
            "docker", "exec", "self-deployment_agent-postgres-1",
            "psql", "-U", "postgres", "-d", "platform", "-t", "-c",
            (
                "INSERT INTO builds (application_id, triggered_by, status, image_refs, started_at, completed_at) "
                f"VALUES ('{app_id}', '{employee_user_id}', 'succeeded', "
                f"'{{\"api\": \"{image_ref}\"}}'::jsonb, now(), now()) RETURNING id;"
            ),
        ],
        capture_output=True, text=True, check=True,
    )
    build_id = out.stdout.strip()
    _check(bool(build_id), f"inserted v2 build row: {build_id}")
    return build_id


def _lookup_employee_user_id() -> str:
    out = subprocess.run(
        [
            "docker", "exec", "self-deployment_agent-postgres-1",
            "psql", "-U", "postgres", "-d", "platform", "-t", "-c",
            f"SELECT id FROM users WHERE email = '{EMPLOYEE_EMAIL}';",
        ],
        capture_output=True, text=True, check=True,
    )
    return out.stdout.strip()


def _docker_build_v2_image(image_ref: str) -> None:
    with tempfile.TemporaryDirectory() as tmp:
        api_dir = Path(tmp)
        (api_dir / "main.go").write_text(
            'package main\n\nimport (\n\t"fmt"\n\t"net/http"\n)\n\nfunc main() {\n'
            '\thttp.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {\n'
            '\t\tfmt.Fprintln(w, "hello from mcptest v2")\n\t})\n'
            '\thttp.ListenAndServe(":8080", nil)\n}\n'
        )
        (api_dir / "go.mod").write_text("module mcptest\n\ngo 1.25\n")
        (api_dir / "Dockerfile").write_text(
            "FROM golang:1.25-alpine AS build\nWORKDIR /src\nCOPY . .\n"
            "RUN go build -o /out/app .\n\nFROM alpine:3.21\n"
            "COPY --from=build /out/app /app\nEXPOSE 8080\nCMD [\"/app\"]\n"
        )
        subprocess.run(["docker", "build", "-t", image_ref, str(api_dir)], check=True)


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

            print("--- building v1 via direct HTTP (documented gap: no MCP build tool) ---")
            await _build_via_direct_http(app_id, 'package main\n\nimport (\n\t"fmt"\n\t"net/http"\n)\n\nfunc main() {\n\thttp.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {\n\t\tfmt.Fprintln(w, "hello from mcptest v1")\n\t})\n\thttp.ListenAndServe(":8080", nil)\n}\n')

            deployed = _print_result(
                "deploy_application (v1, dev)",
                await session.call_tool(
                    "deploy_application", {"application_id": app_id, "target_environment": "dev"}
                ),
            )
            _check(deployed["status"] == "success" and deployed["data"]["status"] == "running", "v1 deployed and running")
            v1_deployment_id = deployed["data"]["deployment_id"]

            status = _print_result(
                "get_application_status", await session.call_tool("get_application_status", {"application_id": app_id})
            )
            _check(status["data"]["current_lifecycle_state"] == "running", "application status reports running")
            _check(bool(status["data"]["url"]), "application status reports a live URL")

            dep_status = _print_result(
                "get_deployment_status", await session.call_tool("get_deployment_status", {"deployment_id": v1_deployment_id})
            )
            _check(dep_status["data"]["phase"] == "COMPLETED", "deployment status phase is COMPLETED")

            restarted = _print_result(
                "restart_application", await session.call_tool("restart_application", {"application_id": app_id})
            )
            _check(restarted["data"]["status"] == "COMPLETED", "restart_application completed")

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

            print("--- confirming the documented gap: build is unreachable via direct HTTP once running too ---")
            async with httpx.AsyncClient(timeout=30) as http:
                resp = await http.post(
                    f"{PLATFORM_API_BASE_URL}/applications/{app_id}/build",
                    headers={"X-Dev-User-Email": EMPLOYEE_EMAIL},
                )
            _check(resp.status_code != 200, "build correctly rejected once running (not_validated) — matches deploy_application's documented gap")

            print("--- exercising rollback_application for real: v2 build (docker build) + deploy via MCP + rollback via MCP ---")
            image_ref = "platform-build/mcptest-api:v2-e2e"
            _docker_build_v2_image(image_ref)
            employee_user_id = _lookup_employee_user_id()
            _check(bool(employee_user_id), f"looked up employee user id: {employee_user_id}")
            _insert_v2_build_row(app_id, employee_user_id, image_ref)

            v2 = _print_result(
                "deploy_application (v2, dev)",
                await session.call_tool(
                    "deploy_application", {"application_id": app_id, "target_environment": "dev"}
                ),
            )
            _check(v2["status"] == "success" and v2["data"]["status"] == "running", "v2 deployed and running")

            status_url = _print_result(
                "get_application_status (v2 live)",
                await session.call_tool("get_application_status", {"application_id": app_id}),
            )
            async with httpx.AsyncClient(timeout=10) as http:
                live = await http.get(status_url["data"]["url"])
            _check("v2" in live.text, f"live traffic serves v2's response: {live.text.strip()!r}")

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
