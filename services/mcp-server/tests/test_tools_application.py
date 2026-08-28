import pytest

from mcp_server.envelope import ErrorCode, ToolError
from mcp_server.idempotency import IdempotencyStore
from mcp_server.tools import application

from .fakes import FakePlatformClient


async def test_create_application_resolves_department_name_to_id():
    client = FakePlatformClient()
    idem = IdempotencyStore(ttl_seconds=60)
    result = await application.create_application(
        client, idem, "overtime", "HR overtime tracker", "Engineering"
    )
    assert result["status"] == "success"
    call = next(c for c in client.calls if c[0] == "register_application")
    assert call[1] == ("overtime", "HR overtime tracker", "dept-eng")


async def test_create_application_unknown_department_is_validation_error():
    client = FakePlatformClient()
    idem = IdempotencyStore(ttl_seconds=60)
    with pytest.raises(ToolError) as exc_info:
        await application.create_application(client, idem, "overtime", "desc", "Nonexistent Dept")
    assert exc_info.value.code == ErrorCode.VALIDATION_ERROR
    assert "Engineering" in exc_info.value.details[0].reason


async def test_create_application_also_saves_deployment_yaml_when_given():
    client = FakePlatformClient()
    idem = IdempotencyStore(ttl_seconds=60)
    result = await application.create_application(
        client, idem, "overtime", "desc", "Engineering", deployment_yaml="app:\n  name: overtime\n"
    )
    assert result["data"]["deployment_yaml_draft"] == "app:\n  name: overtime\n"


async def test_create_application_idempotency_key_returns_cached_result_without_reregistering():
    client = FakePlatformClient()
    idem = IdempotencyStore(ttl_seconds=60)
    first = await application.create_application(
        client, idem, "overtime", "desc", "Engineering", idempotency_key="key-1"
    )
    second = await application.create_application(
        client, idem, "overtime", "desc", "Engineering", idempotency_key="key-1"
    )
    assert first == second
    register_calls = [c for c in client.calls if c[0] == "register_application"]
    assert len(register_calls) == 1


async def test_create_application_idempotency_key_reused_with_different_input_conflicts():
    client = FakePlatformClient()
    idem = IdempotencyStore(ttl_seconds=60)
    await application.create_application(
        client, idem, "overtime", "desc", "Engineering", idempotency_key="key-1"
    )
    with pytest.raises(ToolError) as exc_info:
        await application.create_application(
            client, idem, "payroll", "different app", "Engineering", idempotency_key="key-1"
        )
    assert exc_info.value.code == ErrorCode.CONFLICT


async def test_validate_application_returns_findings_as_success_not_error():
    client = FakePlatformClient()
    app = await client.register_application("overtime", "desc", "dept-eng")
    result = await application.validate_application(client, app["id"])
    assert result["status"] == "success"
    assert result["data"]["validation_result"]["passed"] is True
    assert result["data"]["lifecycle_state"] == "validated"


async def test_validate_application_saves_updated_yaml_first_when_given():
    client = FakePlatformClient()
    app = await client.register_application("overtime", "desc", "dept-eng")
    await application.validate_application(client, app["id"], deployment_yaml="app:\n  name: overtime\n")
    save_calls = [c for c in client.calls if c[0] == "save_deployment_yaml"]
    assert len(save_calls) == 1
