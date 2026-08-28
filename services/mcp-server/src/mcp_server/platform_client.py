"""Thin async HTTP client wrapping every Company Platform API call the MCP
tools need. Per docs/07_MCP_Requirements.md Section 2.3 ("No business logic
in the MCP layer... forwards the request to the Platform API, and relays the
Platform API's authoritative response"), this module does no independent
authorization or validation — every check is the Platform API's, re-derived
server-side on every call. This module's only job is: shape the HTTP call,
translate the Platform API's dev-mode-header identity and its
{"error": {"code": ..., "message": ...}} responses into the vocabulary the
rest of this server uses (a typed exception with an MCP ErrorCode).
"""

from __future__ import annotations

from typing import Any

import httpx

from .config import Config
from .envelope import ErrorCode, ToolError

# Platform API error `code` strings that specifically mean "the operation is
# well-formed but the target is in the wrong lifecycle state for it" —
# mapped to VALIDATION_ERROR per Section 8's definition ("fails a
# structural/business rule"), NOT to CONFLICT, which Section 8 reserves for
# idempotency-key reuse, duplicate in-flight operations, and name
# collisions specifically.
_WRONG_STATE_CODES = {
    "not_running",
    "not_suspended",
    "not_validated",
    "invalid_lifecycle_transition",
    "no_successful_build",
    "not_pending_approval",
    "invalid_rollback_target",
}

# Codes that genuinely are Section 8's narrower CONFLICT definition.
_CONFLICT_CODES = {"build_in_flight", "deployment_in_flight", "name_taken"}

_STATUS_TO_CODE = {
    400: ErrorCode.VALIDATION_ERROR,
    401: ErrorCode.UNAUTHORIZED,
    403: ErrorCode.UNAUTHORIZED,  # Section 8: UNAUTHORIZED covers "lacks permission" too.
    404: ErrorCode.NOT_FOUND,
    409: ErrorCode.CONFLICT,
    413: ErrorCode.QUOTA_EXCEEDED,
    422: ErrorCode.INTERNAL_ERROR,  # platform catalog gap (e.g. no_base_image), not caller error
    429: ErrorCode.RATE_LIMITED,
}


def _map_error(status_code: int, body: dict[str, Any] | None) -> ToolError:
    platform_code = ""
    message = f"Platform API returned HTTP {status_code}"
    if body and isinstance(body.get("error"), dict):
        platform_code = str(body["error"].get("code", ""))
        message = str(body["error"].get("message", message))

    if platform_code in _WRONG_STATE_CODES:
        mcp_code = ErrorCode.VALIDATION_ERROR
    elif platform_code in _CONFLICT_CODES:
        mcp_code = ErrorCode.CONFLICT
    else:
        mcp_code = _STATUS_TO_CODE.get(status_code, ErrorCode.INTERNAL_ERROR)

    return ToolError(mcp_code, message, platform_code=platform_code or None)


class PlatformClient:
    """One instance per MCP server process, bound to the single employee
    identity that process acts on behalf of (see config.py). Every request
    carries that identity via the same X-Dev-* headers
    services/platform-api/internal/httpapi/devauth.go trusts — the same
    DEC-001-blocked stand-in platform-api's own console path uses, not a
    separate mechanism invented here."""

    def __init__(self, config: Config, http_client: httpx.AsyncClient | None = None):
        self._base_url = config.platform_api_base_url
        self._employee = config.employee
        self._client = http_client or httpx.AsyncClient(
            base_url=self._base_url, timeout=config.platform_api_timeout_seconds
        )

    async def aclose(self) -> None:
        await self._client.aclose()

    def _headers(self) -> dict[str, str]:
        headers = {"X-Dev-User-Email": self._employee.email}
        if self._employee.full_name:
            headers["X-Dev-User-Name"] = self._employee.full_name
        if self._employee.department:
            headers["X-Dev-Department"] = self._employee.department
        return headers

    async def _request(
        self, method: str, path: str, extra_headers: dict[str, str] | None = None, **kwargs: Any
    ) -> dict[str, Any]:
        headers = self._headers()
        if extra_headers:
            headers.update(extra_headers)
        try:
            resp = await self._client.request(method, path, headers=headers, **kwargs)
        except httpx.RequestError as exc:
            raise ToolError(
                ErrorCode.INTERNAL_ERROR, f"could not reach the Platform API: {exc}"
            ) from exc

        if resp.status_code >= 400:
            body: dict[str, Any] | None
            try:
                body = resp.json()
            except ValueError:
                body = None
            raise _map_error(resp.status_code, body)

        if not resp.content:
            return {}
        return resp.json()

    # --- Departments -----------------------------------------------------

    async def list_departments(self) -> list[dict[str, Any]]:
        # domain.Department (Go) has no json tags, so the wire format is
        # PascalCase ("ID", "Name", ...) — Go's default. Normalized to
        # snake_case here so the MCP-facing contract doesn't leak that
        # implementation detail to Claude Code.
        data = await self._request("GET", "/departments")
        return [
            {
                "id": d.get("ID"),
                "name": d.get("Name"),
                "cost_center_code": d.get("CostCenterCode"),
                "status": d.get("Status"),
            }
            for d in data.get("departments", [])
        ]

    # --- Applications ------------------------------------------------------

    async def register_application(
        self, name: str, description: str, owning_department_id: str
    ) -> dict[str, Any]:
        return await self._request(
            "POST",
            "/applications",
            json={
                "name": name,
                "description": description,
                "owning_department_id": owning_department_id,
            },
        )

    async def get_application(self, application_id: str) -> dict[str, Any]:
        return await self._request("GET", f"/applications/{application_id}")

    async def save_deployment_yaml(self, application_id: str, deployment_yaml: str) -> dict[str, Any]:
        return await self._request(
            "PUT",
            f"/applications/{application_id}/deployment-yaml",
            json={"deployment_yaml": deployment_yaml},
        )

    async def trigger_build(self, application_id: str, source_archive: bytes) -> dict[str, Any]:
        """Not one of Section 13's tools on its own — deploy_application
        calls this internally when given source code, closing the gap
        documented in tools/deployment.py's module doc. Callable now for a
        Validated application (first build) OR a Running/Failed one (a
        rebuild) — see build_service.go's TriggerBuild doc comment for why
        that precondition was widened as part of this same fix."""
        return await self._request(
            "POST",
            f"/applications/{application_id}/build",
            extra_headers={"Content-Type": "application/gzip"},
            content=source_archive,
        )

    async def validate_application(self, application_id: str) -> dict[str, Any]:
        return await self._request("POST", f"/applications/{application_id}/validate")

    async def get_supported_stacks(self) -> list[dict[str, Any]]:
        # Same PascalCase-from-Go note as list_departments above —
        # domain.SupportedStack has no json tags either.
        data = await self._request("GET", "/supported-stacks")
        return [
            {
                "id": s.get("ID"),
                "category": s.get("Kind"),
                "runtime": s.get("Name"),
                "status": s.get("Status"),
            }
            for s in data.get("stacks", [])
        ]

    # --- Deployments -------------------------------------------------------

    async def deploy_application(self, application_id: str, environment: str) -> dict[str, Any]:
        return await self._request(
            "POST", f"/applications/{application_id}/deploy", json={"environment": environment}
        )

    async def latest_deployment(self, application_id: str) -> dict[str, Any]:
        return await self._request("GET", f"/applications/{application_id}/deployments/latest")

    async def deployment_history(self, application_id: str) -> list[dict[str, Any]]:
        data = await self._request("GET", f"/applications/{application_id}/deployments")
        return data if isinstance(data, list) else data.get("deployments", [])

    async def get_deployment(self, deployment_id: str) -> dict[str, Any]:
        return await self._request("GET", f"/deployments/{deployment_id}")

    async def rollback_application(self, application_id: str, target_deployment_id: str) -> dict[str, Any]:
        return await self._request(
            "POST",
            f"/applications/{application_id}/rollback",
            json={"target_deployment_id": target_deployment_id},
        )

    async def restart_application(self, application_id: str) -> dict[str, Any]:
        return await self._request("POST", f"/applications/{application_id}/restart")

    # --- Lifecycle: Archive/Delete ------------------------------------------

    async def archive_application(self, application_id: str) -> dict[str, Any]:
        return await self._request("POST", f"/applications/{application_id}/archive")

    async def delete_application(self, application_id: str) -> dict[str, Any]:
        return await self._request(
            "POST", f"/applications/{application_id}/delete", json={"confirm": True}
        )
