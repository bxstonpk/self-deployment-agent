from mcp_server.tools import audit_log

from .fakes import FakePlatformClient


async def test_query_audit_log_returns_entries():
    client = FakePlatformClient()
    client.audit_entries = [
        {"id": "a1", "action": "application.register", "resource_type": "application", "resource_id": "app-1"},
        {"id": "a2", "action": "application.suspend", "resource_type": "application", "resource_id": "app-1"},
    ]
    result = await audit_log.query_audit_log(client)
    assert result["status"] == "success"
    assert [e["id"] for e in result["data"]["entries"]] == ["a1", "a2"]


async def test_query_audit_log_filters_by_action():
    client = FakePlatformClient()
    client.audit_entries = [
        {"id": "a1", "action": "application.register", "resource_type": "application", "resource_id": "app-1"},
        {"id": "a2", "action": "application.suspend", "resource_type": "application", "resource_id": "app-1"},
    ]
    result = await audit_log.query_audit_log(client, action="application.suspend")
    assert [e["id"] for e in result["data"]["entries"]] == ["a2"]


async def test_query_audit_log_forwards_all_filters_to_the_client():
    client = FakePlatformClient()
    await audit_log.query_audit_log(
        client,
        resource_type="application",
        resource_id="app-1",
        action="application.suspend",
        from_time="2026-01-01T00:00:00Z",
        to_time="2026-01-02T00:00:00Z",
        limit=10,
    )
    name, args = client.calls[-1]
    assert name == "query_audit_log"
    assert args[0] == {
        "resource_type": "application",
        "resource_id": "app-1",
        "action": "application.suspend",
        "from": "2026-01-01T00:00:00Z",
        "to": "2026-01-02T00:00:00Z",
        "limit": 10,
    }
