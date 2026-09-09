"""list_notifications, mark_notification_read — read/ack access to Module X
(Notification).

Not part of docs/07_MCP_Requirements.md Section 13's original tool
catalog: Module X didn't exist when that catalog was written. Added so
Claude Code can check "is there anything pending for me" across every
application it acts on, rather than polling get_application_status one
application at a time — real incremental value get_application_status
doesn't give, since that tool is scoped to a single application per call.

Recipients are scoped server-side to the caller (every active owner of an
application, per NotificationService.NotifyOwners) — there is no
parameter here to read someone else's notifications, matching
services/platform-api/README.md's "How Notifications work".
"""

from __future__ import annotations

from typing import Any

from ..envelope import success
from ..platform_client import PlatformClient


async def list_notifications(client: PlatformClient, unread_only: bool = False) -> dict[str, Any]:
    notifications = await client.list_notifications(unread_only)
    return success({"notifications": notifications})


async def mark_notification_read(client: PlatformClient, notification_id: str) -> dict[str, Any]:
    notification = await client.mark_notification_read(notification_id)
    return success(notification)
