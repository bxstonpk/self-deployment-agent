"""Company Deployment MCP server (Module Y,
docs/02_Functional_Requirements.md / docs/07_MCP_Requirements.md) — the
thin, policy-aware layer between Claude Code and the Company Platform API.

Per Section 2's architecture: this module does no business logic of its
own. It authenticates as the one employee identity this process is bound to
(config.py), does no independent authorization beyond that (every real
allow/deny is re-derived by the Platform API on every call), translates
each of Section 13's tool calls into the corresponding Platform API
call(s), and relays the result inside the structured envelope Section 8
defines. Also registers eight tools beyond Section 13's original
catalog, added as later modules shipped: query_audit_log (Module W),
list_notifications / mark_notification_read (Module X),
get_application_inventory / get_deployment_activity (Module AB), and
list_application_owners / grant_application_access /
revoke_application_access (Module E). Each tool module's own docstring
says why — including, in tools/ownership.py, why FR-016's ownership
*transfer* is deliberately not among them.
"""

from __future__ import annotations

import sys
from typing import Any

from mcp.server.mcpserver import MCPServer

from .audit import AuditEvent
from .config import ConfigError, load_config
from .envelope import ErrorCode, ToolError, error
from .idempotency import IdempotencyStore
from .platform_client import PlatformClient
from .tools import (
    application,
    audit_log,
    deployment,
    discovery,
    lifecycle,
    notifications,
    observability,
    ownership,
    reporting,
)


async def _run_tool(
    event: AuditEvent,
    call: Any,
) -> dict[str, Any]:
    try:
        result = await call
    except ToolError as exc:
        event.finish("error", {"code": exc.code.value, "message": exc.message})
        return error(exc.code, exc.message, exc.details)
    except Exception as exc:  # pragma: no cover - defensive catch-all
        event.finish("error", {"code": "INTERNAL_ERROR", "message": str(exc)})
        return error(ErrorCode.INTERNAL_ERROR, f"unexpected server error: {exc}")
    else:
        event.finish(result.get("status", "success"), {"envelope_status": result.get("status")})
        return result


def build_server(client: PlatformClient, idempotency: IdempotencyStore, employee_email: str) -> MCPServer:
    mcp = MCPServer(
        name="company-deployment-mcp",
        title="Company Deployment MCP",
        instructions=(
            "Business-capability interface to the Company Platform API. "
            "Every tool call acts as the single authenticated employee this "
            "server session is bound to. Call get_platform_info and "
            "get_supported_stacks first in any deployment workflow — never "
            "assume cached knowledge of what the platform currently supports."
        ),
    )

    def audit(tool_name: str, params: dict[str, Any], application_id: str | None) -> AuditEvent:
        return AuditEvent(
            tool_name=tool_name,
            employee_identity=employee_email,
            input_parameters=params,
            target_application_id=application_id,
        )

    # --- 13.1-13.3: discovery ------------------------------------------

    @mcp.tool()
    async def get_platform_info(client_skill_version: str | None = None) -> dict[str, Any]:
        params = {"client_skill_version": client_skill_version}
        return await _run_tool(
            audit("get_platform_info", params, None),
            discovery.get_platform_info(client, client_skill_version),
        )

    @mcp.tool()
    async def get_supported_stacks(category: str | None = None) -> dict[str, Any]:
        params = {"category": category}
        return await _run_tool(
            audit("get_supported_stacks", params, None),
            discovery.get_supported_stacks(client, category),
        )

    @mcp.tool()
    async def get_deployment_requirements(
        application_id: str | None = None, proposed_shape: dict[str, Any] | None = None
    ) -> dict[str, Any]:
        params = {"application_id": application_id, "proposed_shape": proposed_shape}
        return await _run_tool(
            audit("get_deployment_requirements", params, application_id),
            discovery.get_deployment_requirements(client, application_id, proposed_shape),
        )

    # --- 13.4-13.5: application registration/validation -----------------

    @mcp.tool()
    async def create_application(
        name: str,
        description: str,
        department: str,
        deployment_yaml: str | None = None,
        idempotency_key: str | None = None,
    ) -> dict[str, Any]:
        params = {
            "name": name,
            "description": description,
            "department": department,
            "deployment_yaml": deployment_yaml,
            "idempotency_key": idempotency_key,
        }
        return await _run_tool(
            audit("create_application", params, None),
            application.create_application(
                client, idempotency, name, description, department, deployment_yaml, idempotency_key
            ),
        )

    @mcp.tool()
    async def validate_application(
        application_id: str, deployment_yaml: str | None = None
    ) -> dict[str, Any]:
        params = {"application_id": application_id, "deployment_yaml": deployment_yaml}
        return await _run_tool(
            audit("validate_application", params, application_id),
            application.validate_application(client, application_id, deployment_yaml),
        )

    # --- 13.6-13.8, 13.11-13.12: deployment lifecycle -------------------

    @mcp.tool()
    async def deploy_application(
        application_id: str,
        target_environment: str,
        version_reference: str | None = None,
        source_archive_base64: str | None = None,
        idempotency_key: str | None = None,
    ) -> dict[str, Any]:
        params = {
            "application_id": application_id,
            "target_environment": target_environment,
            "version_reference": version_reference,
            "source_archive_base64": source_archive_base64,
            "idempotency_key": idempotency_key,
        }
        return await _run_tool(
            audit("deploy_application", params, application_id),
            deployment.deploy_application(
                client,
                idempotency,
                application_id,
                target_environment,
                version_reference,
                source_archive_base64,
                idempotency_key,
            ),
        )

    @mcp.tool()
    async def get_application_status(application_id: str) -> dict[str, Any]:
        params = {"application_id": application_id}
        return await _run_tool(
            audit("get_application_status", params, application_id),
            deployment.get_application_status(client, application_id),
        )

    @mcp.tool()
    async def get_deployment_status(deployment_id: str) -> dict[str, Any]:
        params = {"deployment_id": deployment_id}
        return await _run_tool(
            audit("get_deployment_status", params, None),
            deployment.get_deployment_status(client, deployment_id),
        )

    @mcp.tool()
    async def rollback_application(
        application_id: str,
        target_version: str,
        environment: str | None = None,
        idempotency_key: str | None = None,
    ) -> dict[str, Any]:
        params = {
            "application_id": application_id,
            "target_version": target_version,
            "environment": environment,
            "idempotency_key": idempotency_key,
        }
        return await _run_tool(
            audit("rollback_application", params, application_id),
            deployment.rollback_application(
                client, idempotency, application_id, target_version, idempotency_key
            ),
        )

    @mcp.tool()
    async def restart_application(
        application_id: str, environment: str | None = None, idempotency_key: str | None = None
    ) -> dict[str, Any]:
        params = {
            "application_id": application_id,
            "environment": environment,
            "idempotency_key": idempotency_key,
        }
        return await _run_tool(
            audit("restart_application", params, application_id),
            deployment.restart_application(client, idempotency, application_id, idempotency_key),
        )

    # --- 13.9-13.10: observability (logs via Module S, metrics via Module T) --

    @mcp.tool()
    async def get_application_logs(
        application_id: str,
        environment: str,
        tail_lines: int | None = None,
        time_range: str | None = None,
        service: str | None = None,
        severity: str | None = None,
        cursor: str | None = None,
    ) -> dict[str, Any]:
        params = {
            "application_id": application_id,
            "environment": environment,
            "tail_lines": tail_lines,
            "time_range": time_range,
            "service": service,
            "severity": severity,
            "cursor": cursor,
        }
        return await _run_tool(
            audit("get_application_logs", params, application_id),
            observability.get_application_logs(
                client, application_id, environment, tail_lines, time_range, service, severity, cursor
            ),
        )

    @mcp.tool()
    async def get_application_metrics(
        application_id: str,
        environment: str,
        time_range: str | None = None,
        metric_types: list[str] | None = None,
    ) -> dict[str, Any]:
        params = {
            "application_id": application_id,
            "environment": environment,
            "time_range": time_range,
            "metric_types": metric_types,
        }
        return await _run_tool(
            audit("get_application_metrics", params, application_id),
            observability.get_application_metrics(client, application_id, environment, time_range, metric_types),
        )

    # --- 13.13: deletion --------------------------------------------------

    @mcp.tool()
    async def delete_application(
        application_id: str, confirmation: str, idempotency_key: str | None = None
    ) -> dict[str, Any]:
        params = {
            "application_id": application_id,
            "confirmation": confirmation,
            "idempotency_key": idempotency_key,
        }
        return await _run_tool(
            audit("delete_application", params, application_id),
            lifecycle.delete_application(
                client, idempotency, application_id, confirmation, idempotency_key
            ),
        )

    # --- Beyond Section 13: query_audit_log, list_notifications,
    # mark_notification_read (Modules W/X shipped after the tool catalog
    # was written — see this module's docstring and tools/audit_log.py /
    # tools/notifications.py) ------------------------------------------

    @mcp.tool()
    async def query_audit_log(
        resource_type: str | None = None,
        resource_id: str | None = None,
        action: str | None = None,
        from_time: str | None = None,
        to_time: str | None = None,
        limit: int | None = None,
    ) -> dict[str, Any]:
        params = {
            "resource_type": resource_type,
            "resource_id": resource_id,
            "action": action,
            "from_time": from_time,
            "to_time": to_time,
            "limit": limit,
        }
        return await _run_tool(
            audit("query_audit_log", params, resource_id if resource_type == "application" else None),
            audit_log.query_audit_log(client, resource_type, resource_id, action, from_time, to_time, limit),
        )

    @mcp.tool()
    async def list_notifications(unread_only: bool = False) -> dict[str, Any]:
        params = {"unread_only": unread_only}
        return await _run_tool(
            audit("list_notifications", params, None),
            notifications.list_notifications(client, unread_only),
        )

    @mcp.tool()
    async def mark_notification_read(notification_id: str) -> dict[str, Any]:
        params = {"notification_id": notification_id}
        return await _run_tool(
            audit("mark_notification_read", params, None),
            notifications.mark_notification_read(client, notification_id),
        )

    # --- Module AB (Reporting) and Module E (ownership management),
    # both shipped after Section 13's catalog — see tools/reporting.py and
    # tools/ownership.py, including why FR-016 transfer is NOT here -----

    @mcp.tool()
    async def get_application_inventory() -> dict[str, Any]:
        return await _run_tool(
            audit("get_application_inventory", {}, None),
            reporting.get_application_inventory(client),
        )

    @mcp.tool()
    async def get_deployment_activity(
        from_time: str | None = None, to_time: str | None = None
    ) -> dict[str, Any]:
        params = {"from_time": from_time, "to_time": to_time}
        return await _run_tool(
            audit("get_deployment_activity", params, None),
            reporting.get_deployment_activity(client, from_time, to_time),
        )

    @mcp.tool()
    async def list_application_owners(application_id: str) -> dict[str, Any]:
        params = {"application_id": application_id}
        return await _run_tool(
            audit("list_application_owners", params, application_id),
            ownership.list_application_owners(client, application_id),
        )

    @mcp.tool()
    async def grant_application_access(
        application_id: str, email: str, access_level: str
    ) -> dict[str, Any]:
        params = {"application_id": application_id, "email": email, "access_level": access_level}
        return await _run_tool(
            audit("grant_application_access", params, application_id),
            ownership.grant_application_access(client, application_id, email, access_level),
        )

    @mcp.tool()
    async def revoke_application_access(application_id: str, user_id: str) -> dict[str, Any]:
        params = {"application_id": application_id, "user_id": user_id}
        return await _run_tool(
            audit("revoke_application_access", params, application_id),
            ownership.revoke_application_access(client, application_id, user_id),
        )

    return mcp


def main() -> None:
    try:
        config = load_config()
    except ConfigError as exc:
        print(f"company-deployment-mcp: configuration error: {exc}", file=sys.stderr)
        raise SystemExit(1) from exc

    client = PlatformClient(config)
    idempotency = IdempotencyStore(ttl_seconds=config.idempotency_ttl_seconds)
    mcp = build_server(client, idempotency, config.employee.email)
    mcp.run(transport="stdio")


if __name__ == "__main__":
    main()
