import pytest

from mcp_server.envelope import ErrorCode, ToolError
from mcp_server.tools import discovery

from .fakes import FakePlatformClient


async def test_get_platform_info_returns_stable_stack_fingerprint():
    client = FakePlatformClient()
    result = await discovery.get_platform_info(client, client_skill_version="1.2.3")
    assert result["status"] == "success"
    data = result["data"]
    assert data["client_skill_version_received"] == "1.2.3"
    assert data["supported_environments"] == ["dev", "production"]
    assert data["supported_stack_version_ref"]

    # Same catalog content -> same fingerprint (real drift detection needs this).
    result2 = await discovery.get_platform_info(client)
    assert result2["data"]["supported_stack_version_ref"] == data["supported_stack_version_ref"]


async def test_get_platform_info_fingerprint_changes_when_catalog_changes():
    client = FakePlatformClient()
    before = (await discovery.get_platform_info(client))["data"]["supported_stack_version_ref"]
    client.stacks.append({"id": "stack-python", "category": "backend", "runtime": "python", "status": "active"})
    after = (await discovery.get_platform_info(client))["data"]["supported_stack_version_ref"]
    assert before != after


async def test_get_supported_stacks_filters_by_category():
    client = FakePlatformClient()
    result = await discovery.get_supported_stacks(client, category="backend")
    assert result["status"] == "success"
    runtimes = [s["runtime"] for s in result["data"]["stacks"]]
    assert runtimes == ["go"]


async def test_get_supported_stacks_reports_version_range_as_null_not_fabricated():
    client = FakePlatformClient()
    result = await discovery.get_supported_stacks(client)
    for stack in result["data"]["stacks"]:
        assert stack["version_range"] is None


async def test_get_deployment_requirements_rejects_both_application_id_and_proposed_shape():
    client = FakePlatformClient()
    with pytest.raises(ToolError) as exc_info:
        await discovery.get_deployment_requirements(
            client, application_id="app-1", proposed_shape={"services": {}}
        )
    assert exc_info.value.code == ErrorCode.VALIDATION_ERROR


async def test_get_deployment_requirements_includes_application_context_when_given():
    client = FakePlatformClient()
    app = await client.register_application("overtime", "HR tool", "dept-eng")
    result = await discovery.get_deployment_requirements(client, application_id=app["id"])
    assert result["status"] == "success"
    assert result["data"]["application_id"] == app["id"]
    assert result["data"]["current_lifecycle_state"] == "draft"
