"""get_application_inventory, get_deployment_activity — read access to
Module AB (Reporting).

Not in docs/07_MCP_Requirements.md Section 13's original catalog: Module
AB didn't exist when it was written. Same reasoning as tools/audit_log.py
and tools/notifications.py — these are whole business capabilities that
had no representation at all, not missing parameters on an existing tool.

`get_application_inventory` closes a gap that predates Module AB itself:
until now nothing in this server could answer "which applications do I
have?" at all. Section 13's catalog is entirely single-application
(get_application_status takes one application_id), so an agent had no way
to enumerate an employee's applications without being handed the ids
first. That makes this the natural entry point for most conversations,
not just a reporting nicety.

Both are thin per Section 2.3 ("no business logic in the MCP layer"): the
Platform API's ReportingService already scopes every row to applications
the caller owns, so nothing is re-derived here.
"""

from __future__ import annotations

from typing import Any

from ..envelope import success
from ..platform_client import PlatformClient


async def get_application_inventory(client: PlatformClient) -> dict[str, Any]:
    applications = await client.application_inventory()
    return success({"applications": applications, "count": len(applications)})


async def get_deployment_activity(
    client: PlatformClient, from_time: str | None = None, to_time: str | None = None
) -> dict[str, Any]:
    report = await client.deployment_activity(from_time, to_time)
    return success(report)
