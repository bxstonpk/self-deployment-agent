import pytest

from mcp_server.envelope import ErrorCode, ToolError
from mcp_server.tools import ownership, reporting

from .fakes import FakePlatformClient


# --- Module AB: reporting ---


async def test_get_application_inventory_returns_rows_and_a_count():
    client = FakePlatformClient()
    client.inventory = [
        {"application_id": "app-1", "name": "overtime", "lifecycle_status": "running"},
        {"application_id": "app-2", "name": "payroll", "lifecycle_status": "draft"},
    ]
    result = await reporting.get_application_inventory(client)
    assert result["status"] == "success"
    assert result["data"]["count"] == 2
    assert [a["name"] for a in result["data"]["applications"]] == ["overtime", "payroll"]


async def test_get_application_inventory_empty_is_a_success_not_an_error():
    client = FakePlatformClient()
    result = await reporting.get_application_inventory(client)
    assert result["status"] == "success"
    assert result["data"]["count"] == 0


async def test_get_deployment_activity_forwards_the_range_and_relays_the_report():
    client = FakePlatformClient()
    client.activity = {
        "from": "2026-08-01T00:00:00Z",
        "to": "2026-09-01T00:00:00Z",
        "total": {"succeeded": 4, "failed": 1, "rolled_back": 2},
        "by_environment": {"dev": {"succeeded": 4, "failed": 1, "rolled_back": 2}},
        "by_department": {"Engineering": {"succeeded": 4, "failed": 1, "rolled_back": 2}},
    }
    result = await reporting.get_deployment_activity(client, "2026-08-01T00:00:00Z", "2026-09-01T00:00:00Z")
    assert result["status"] == "success"
    assert result["data"]["total"]["rolled_back"] == 2

    name, args = client.calls[-1]
    assert name == "deployment_activity"
    assert args == ("2026-08-01T00:00:00Z", "2026-09-01T00:00:00Z")


async def test_get_deployment_activity_omits_an_unset_range():
    client = FakePlatformClient()
    await reporting.get_deployment_activity(client)
    _, args = client.calls[-1]
    assert args == (None, None)


# --- Module E: ownership management ---


async def test_list_application_owners_relays_the_owner_list():
    client = FakePlatformClient()
    client.owners["app-1"] = [{"user_id": "u1", "ownership_role": "primary", "status": "active"}]
    result = await ownership.list_application_owners(client, "app-1")
    assert result["status"] == "success"
    assert result["data"]["owners"][0]["ownership_role"] == "primary"


async def test_grant_application_access_maps_co_owner_to_the_platforms_role_name():
    client = FakePlatformClient()
    result = await ownership.grant_application_access(client, "app-1", "bob@example.com", "co_owner")
    assert result["status"] == "success"
    assert result["data"]["granted"]["ownership_role"] == "secondary"

    name, args = client.calls[-1]
    assert name == "grant_owner"
    assert args == ("app-1", "bob@example.com", "secondary")


async def test_grant_application_access_maps_contributor_to_technical():
    client = FakePlatformClient()
    await ownership.grant_application_access(client, "app-1", "bob@example.com", "contributor")
    _, args = client.calls[-1]
    assert args == ("app-1", "bob@example.com", "technical")


# "primary" is never grantable — that's registration or FR-016 transfer,
# and this module deliberately exposes neither.
@pytest.mark.parametrize("level", ["primary", "owner", "admin", ""])
async def test_grant_application_access_rejects_any_other_level_without_calling_the_platform(level):
    client = FakePlatformClient()
    with pytest.raises(ToolError) as exc_info:
        await ownership.grant_application_access(client, "app-1", "bob@example.com", level)
    assert exc_info.value.code == ErrorCode.VALIDATION_ERROR
    assert not any(name == "grant_owner" for name, _ in client.calls)


async def test_revoke_application_access_reports_who_was_revoked():
    client = FakePlatformClient()
    await ownership.grant_application_access(client, "app-1", "bob@example.com", "co_owner")
    result = await ownership.revoke_application_access(client, "app-1", "user-bob@example.com")
    assert result["status"] == "success"
    assert result["data"]["revoked_user_id"] == "user-bob@example.com"
    assert client.owners["app-1"] == []


async def test_revoke_application_access_unknown_grant_surfaces_the_platforms_not_found():
    client = FakePlatformClient()
    with pytest.raises(ToolError) as exc_info:
        await ownership.revoke_application_access(client, "app-1", "nobody")
    assert exc_info.value.code == ErrorCode.NOT_FOUND
