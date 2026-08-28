import pytest

from mcp_server.envelope import ErrorCode, ToolError
from mcp_server.idempotency import IdempotencyStore
from mcp_server.tools import lifecycle

from .fakes import FakePlatformClient


async def test_delete_application_from_running_archives_first_then_deletes():
    client = FakePlatformClient()
    idem = IdempotencyStore(ttl_seconds=60)
    app = await client.register_application("overtime", "desc", "dept-eng")
    client.applications[app["id"]]["lifecycle_status"] = "running"

    result = await lifecycle.delete_application(client, idem, app["id"], confirmation="overtime")
    assert result["status"] == "success"
    assert result["data"]["status"] == "DELETED"
    names_called = [c[0] for c in client.calls]
    assert "archive_application" in names_called
    assert "delete_application" in names_called
    assert names_called.index("archive_application") < names_called.index("delete_application")


async def test_delete_application_already_suspended_skips_archive():
    client = FakePlatformClient()
    idem = IdempotencyStore(ttl_seconds=60)
    app = await client.register_application("overtime", "desc", "dept-eng")
    client.applications[app["id"]]["lifecycle_status"] = "suspended"

    await lifecycle.delete_application(client, idem, app["id"], confirmation="overtime")
    names_called = [c[0] for c in client.calls]
    assert "archive_application" not in names_called
    assert "delete_application" in names_called


async def test_delete_application_wrong_confirmation_is_rejected_without_calling_platform():
    client = FakePlatformClient()
    idem = IdempotencyStore(ttl_seconds=60)
    app = await client.register_application("overtime", "desc", "dept-eng")
    client.applications[app["id"]]["lifecycle_status"] = "running"

    with pytest.raises(ToolError) as exc_info:
        await lifecycle.delete_application(client, idem, app["id"], confirmation="wrong-name")
    assert exc_info.value.code == ErrorCode.VALIDATION_ERROR
    assert not any(c[0] in ("archive_application", "delete_application") for c in client.calls)


async def test_delete_application_from_undeleteable_state_is_rejected():
    client = FakePlatformClient()
    idem = IdempotencyStore(ttl_seconds=60)
    app = await client.register_application("overtime", "desc", "dept-eng")
    # still "draft" — never deployed, nothing to archive or delete

    with pytest.raises(ToolError) as exc_info:
        await lifecycle.delete_application(client, idem, app["id"], confirmation="overtime")
    assert exc_info.value.code == ErrorCode.VALIDATION_ERROR
