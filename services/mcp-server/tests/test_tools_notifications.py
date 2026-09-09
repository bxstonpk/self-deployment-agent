import pytest

from mcp_server.envelope import ErrorCode, ToolError
from mcp_server.tools import notifications

from .fakes import FakePlatformClient


async def test_list_notifications_returns_all_by_default():
    client = FakePlatformClient()
    client.notifications = [
        {"id": "n1", "title": "deployed", "read_at": None},
        {"id": "n2", "title": "approved", "read_at": "2026-01-01T00:00:00Z"},
    ]
    result = await notifications.list_notifications(client)
    assert result["status"] == "success"
    assert [n["id"] for n in result["data"]["notifications"]] == ["n1", "n2"]


async def test_list_notifications_unread_only_filters_read_ones_out():
    client = FakePlatformClient()
    client.notifications = [
        {"id": "n1", "title": "deployed", "read_at": None},
        {"id": "n2", "title": "approved", "read_at": "2026-01-01T00:00:00Z"},
    ]
    result = await notifications.list_notifications(client, unread_only=True)
    assert [n["id"] for n in result["data"]["notifications"]] == ["n1"]


async def test_mark_notification_read_returns_the_updated_notification():
    client = FakePlatformClient()
    client.notifications = [{"id": "n1", "title": "deployed", "read_at": None}]
    result = await notifications.mark_notification_read(client, "n1")
    assert result["status"] == "success"
    assert result["data"]["id"] == "n1"
    assert result["data"]["read_at"] is not None


async def test_mark_notification_read_unknown_id_raises_not_found():
    client = FakePlatformClient()
    with pytest.raises(ToolError) as exc_info:
        await notifications.mark_notification_read(client, "does-not-exist")
    assert exc_info.value.code == ErrorCode.NOT_FOUND
