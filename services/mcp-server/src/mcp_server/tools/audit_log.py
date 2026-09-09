"""query_audit_log — read access to Module W (Audit Log).

Not one of docs/07_MCP_Requirements.md Section 13's originally-catalogued
tools: Module W didn't exist when that catalog was written. Added so
Claude Code can answer "what happened to my last action" / "what's the
history on this application" without asking a human to check the Admin
Portal, matching the MCP server's stated purpose (Section 1: expose
business capabilities, not just mutate state) even though this specific
tool predates no doc section describing its schema. Kept intentionally
thin — this forwards the query as-is and relays the Platform API's
response, per Section 2.3's "no business logic in the MCP layer" rule; the
Platform API's own AuditService.Query does all real scoping (the caller
only ever sees entries they performed themselves or that concern an
application they own — see services/platform-api/README.md's "How Audit
Logging works").
"""

from __future__ import annotations

from typing import Any

from ..envelope import success
from ..platform_client import PlatformClient


async def query_audit_log(
    client: PlatformClient,
    resource_type: str | None = None,
    resource_id: str | None = None,
    action: str | None = None,
    from_time: str | None = None,
    to_time: str | None = None,
    limit: int | None = None,
) -> dict[str, Any]:
    entries = await client.query_audit_log(
        {
            "resource_type": resource_type,
            "resource_id": resource_id,
            "action": action,
            "from": from_time,
            "to": to_time,
            "limit": limit,
        }
    )
    return success({"entries": entries})
