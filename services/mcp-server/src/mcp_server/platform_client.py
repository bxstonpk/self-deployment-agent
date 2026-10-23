"""Thin async HTTP client wrapping every Company Platform API call the MCP
tools need. Per docs/07_MCP_Requirements.md Section 2.3 ("No business logic
in the MCP layer... forwards the request to the Platform API, and relays the
Platform API's authoritative response"), this module does no independent
authorization or validation — every check is the Platform API's, re-derived
server-side on every call. This module's only job is: shape the HTTP call,
authenticate it with this machine's API key (credentials.py), and
translate the Platform API's {"error": {"code": ..., "message": ...}}
responses into the vocabulary the rest of this server uses (a typed
exception with an MCP ErrorCode).
"""

from __future__ import annotations

from typing import Any

import httpx

from . import credentials
from .config import Config
from .envelope import ErrorCode, ToolError

NOT_SIGNED_IN = (
    "This machine is not signed in to the Company Platform, or its key has "
    "expired or been revoked. Call the `login` tool, ask the employee to "
    "approve the code it shows in the Admin Portal, then call "
    "`complete_login` (or run `company-deploy login` in a terminal)."
)

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
    if status_code == 401 and platform_code == "unauthorized":
        message = NOT_SIGNED_IN

    if platform_code in _WRONG_STATE_CODES:
        mcp_code = ErrorCode.VALIDATION_ERROR
    elif platform_code in _CONFLICT_CODES:
        mcp_code = ErrorCode.CONFLICT
    else:
        mcp_code = _STATUS_TO_CODE.get(status_code, ErrorCode.INTERNAL_ERROR)

    return ToolError(mcp_code, message, platform_code=platform_code or None)


class PlatformClient:
    """One instance per MCP server process. Every request carries this
    machine's API key, read from the credentials file on each call — so a
    `login` completed mid-session, or `company-deploy login` run in another
    terminal, takes effect without restarting the server. With no key, a
    request is not even attempted."""

    def __init__(self, config: Config, http_client: httpx.AsyncClient | None = None):
        self._base_url = config.platform_api_base_url
        self._credentials_path = config.credentials_path
        self._client = http_client or httpx.AsyncClient(
            base_url=self._base_url, timeout=config.platform_api_timeout_seconds
        )

    async def aclose(self) -> None:
        await self._client.aclose()

    @property
    def base_url(self) -> str:
        return self._base_url

    def stored_credential(self) -> credentials.StoredCredential | None:
        return credentials.for_platform(self._base_url, self._credentials_path)

    def _headers(self) -> dict[str, str]:
        cred = self.stored_credential()
        if cred is None:
            raise ToolError(ErrorCode.UNAUTHORIZED, NOT_SIGNED_IN, platform_code="not_signed_in")
        return {"Authorization": f"Bearer {cred.api_key}"}

    async def _request(
        self, method: str, path: str, extra_headers: dict[str, str] | None = None,
        authenticated: bool = True, **kwargs: Any
    ) -> dict[str, Any]:
        headers = self._headers() if authenticated else {}
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

    # --- Alerts (FR-092) ---------------------------------------------------

    async def get_alerts(self, application_id: str) -> dict[str, Any]:
        return await self._request("GET", f"/applications/{application_id}/alerts")

    # --- Authentication (device authorization) ---------------------------

    async def start_device_authorization(self, client_name: str) -> dict[str, Any]:
        return await self._request("POST", "/auth/device", authenticated=False, json={"client_name": client_name})

    async def poll_device_authorization(self, device_code: str) -> dict[str, Any]:
        return await self._request(
            "POST", "/auth/device/token", authenticated=False, json={"device_code": device_code}
        )

    async def whoami(self) -> dict[str, Any]:
        return await self._request("GET", "/auth/me")

    async def revoke_own_key(self) -> None:
        await self._request("POST", "/auth/logout")

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

    async def deploy_application(self, application_id: str, environment: str, test_report: str | None = None) -> dict[str, Any]:
        body: dict[str, Any] = {"environment": environment}
        if test_report:
            body["test_report"] = test_report
        return await self._request("POST", f"/applications/{application_id}/deploy", json=body)

    # --- Signed approvals (DEC-017) -----------------------------------------

    async def pending_approvals(self) -> list[dict[str, Any]]:
        return (await self._request("GET", "/approvals/pending")).get("pending", [])

    async def approval_request(self, deployment_id: str) -> dict[str, Any]:
        return await self._request("GET", f"/approvals/{deployment_id}")

    async def decide_approval(self, deployment_id: str, decision: str, reason: str, signed_at: str, signature: str) -> dict[str, Any]:
        return await self._request("POST", f"/approvals/{deployment_id}/decision", json={
            "decision": decision, "reason": reason, "signed_at": signed_at, "signature": signature,
        })

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

    # --- Audit Log (Module W) / Notifications (Module X) --------------------
    # Not part of docs/07_MCP_Requirements.md Section 13's original tool
    # catalog — both modules shipped after that catalog was written. Added
    # so Claude Code has the same read access to its own action history and
    # pending events that a human gets through the Admin Portal, consistent
    # with the MCP server's stated purpose (Section 1) even though neither
    # tool is enumerated in Section 13 itself. See tools/audit_log.py and
    # tools/notifications.py's module docs.

    async def query_audit_log(self, params: dict[str, Any]) -> list[dict[str, Any]]:
        data = await self._request("GET", "/audit-log", params={k: v for k, v in params.items() if v is not None})
        return data.get("entries", [])

    async def list_notifications(self, unread_only: bool) -> list[dict[str, Any]]:
        data = await self._request("GET", "/notifications", params={"unread_only": "true"} if unread_only else None)
        return data.get("notifications", [])

    async def mark_notification_read(self, notification_id: str) -> dict[str, Any]:
        return await self._request("POST", f"/notifications/{notification_id}/read")

    # --- Reporting (Module AB) / Ownership (Module E) ----------------------
    # Also outside Section 13's original catalog — both modules shipped
    # after it was written. See tools/reporting.py and tools/ownership.py.

    async def application_inventory(self) -> list[dict[str, Any]]:
        data = await self._request("GET", "/reports/application-inventory")
        return data.get("applications", [])

    async def deployment_activity(self, from_time: str | None, to_time: str | None) -> dict[str, Any]:
        params = {k: v for k, v in {"from": from_time, "to": to_time}.items() if v is not None}
        return await self._request("GET", "/reports/deployment-activity", params=params or None)

    async def list_owners(self, application_id: str) -> list[dict[str, Any]]:
        data = await self._request("GET", f"/applications/{application_id}/owners")
        return data.get("owners", [])

    async def list_secrets(self, application_id: str) -> list[dict[str, Any]]:
        # Names and metadata only: the Platform API has no endpoint that
        # returns a secret's value, so nothing on this path could relay one.
        # There is deliberately no method here that sends one either
        # (SEC-SECRET-3) — see README.md's "Why no MCP tool accepts a
        # secret value".
        data = await self._request("GET", f"/applications/{application_id}/secrets")
        return data.get("secrets", [])

    async def get_metrics(self, application_id: str, params: dict[str, Any]) -> dict[str, Any]:
        # Module T: resource samples, per-minute traffic, and a "collection"
        # block saying whether sampling is keeping up.
        return await self._request(
            "GET", f"/applications/{application_id}/metrics", params={k: v for k, v in params.items() if v is not None}
        )

    async def get_logs(self, application_id: str, params: dict[str, Any]) -> dict[str, Any]:
        # Module S: {"entries": [...], "next_cursor": ...}, newest first,
        # already redacted at collection time.
        return await self._request(
            "GET", f"/applications/{application_id}/logs", params={k: v for k, v in params.items() if v is not None}
        )

    async def grant_owner(self, application_id: str, email: str, ownership_role: str) -> dict[str, Any]:
        return await self._request(
            "POST",
            f"/applications/{application_id}/owners",
            json={"email": email, "ownership_role": ownership_role},
        )

    async def revoke_owner(self, application_id: str, user_id: str) -> dict[str, Any]:
        return await self._request("DELETE", f"/applications/{application_id}/owners/{user_id}")
