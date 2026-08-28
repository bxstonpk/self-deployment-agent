"""The structured response envelope every tool returns, per
docs/07_MCP_Requirements.md Section 8 — so Claude Code reacts
programmatically instead of parsing prose.
"""

from __future__ import annotations

import uuid
from dataclasses import dataclass
from datetime import datetime, timezone
from enum import Enum
from typing import Any


class ErrorCode(str, Enum):
    """The fixed error taxonomy from Section 8. No tool may invent a new
    code outside this set — an unmapped platform-side failure becomes
    INTERNAL_ERROR, not a bespoke string, so the agent's fixed
    what-to-do-next table (Section 8) always applies."""

    VALIDATION_ERROR = "VALIDATION_ERROR"
    POLICY_VIOLATION = "POLICY_VIOLATION"
    UNSUPPORTED_STACK = "UNSUPPORTED_STACK"
    QUOTA_EXCEEDED = "QUOTA_EXCEEDED"
    UNAUTHORIZED = "UNAUTHORIZED"
    NOT_FOUND = "NOT_FOUND"
    CONFLICT = "CONFLICT"
    RATE_LIMITED = "RATE_LIMITED"
    PENDING_APPROVAL = "PENDING_APPROVAL"
    INTERNAL_ERROR = "INTERNAL_ERROR"


@dataclass(frozen=True)
class ErrorDetail:
    field: str | None
    reason: str


class ToolError(Exception):
    """Raised by tool implementations to short-circuit into an error
    envelope. Caught at the MCP tool-registration boundary (server.py) —
    never leaks out as an MCP protocol-level error, since Section 8 wants
    errors as structured DATA the agent parses, not a transport fault."""

    def __init__(
        self,
        code: ErrorCode,
        message: str,
        details: list[ErrorDetail] | None = None,
        platform_code: str | None = None,
    ):
        super().__init__(message)
        self.code = code
        self.message = message
        self.details = details or []
        # The raw Platform API `error.code` string this was translated
        # from, if any — lets a tool match on the precise underlying
        # condition (e.g. "no_successful_build") instead of scanning the
        # human-readable message text, which is for display, not matching.
        self.platform_code = platform_code


def success(data: dict[str, Any], request_id: str | None = None) -> dict[str, Any]:
    return {
        "status": "success",
        "data": data,
        "error": None,
        "request_id": request_id or str(uuid.uuid4()),
        "server_time": _now(),
    }


def error(
    code: ErrorCode,
    message: str,
    details: list[ErrorDetail] | None = None,
    request_id: str | None = None,
) -> dict[str, Any]:
    return {
        "status": "error",
        "data": None,
        "error": {
            "code": code.value,
            "message": message,
            "details": [{"field": d.field, "reason": d.reason} for d in (details or [])],
        },
        "request_id": request_id or str(uuid.uuid4()),
        "server_time": _now(),
    }


def pending_approval(data: dict[str, Any], request_id: str | None = None) -> dict[str, Any]:
    """PENDING_APPROVAL is explicitly "not a failure" (Section 8) — modeled
    as a success envelope whose data carries the pending state, matching
    Section 12 step 2's framing ("returns status = PENDING_APPROVAL"), not
    the error envelope."""
    payload = dict(data)
    payload.setdefault("mcp_status", ErrorCode.PENDING_APPROVAL.value)
    return success(payload, request_id=request_id)


def _now() -> str:
    return datetime.now(timezone.utc).isoformat()
