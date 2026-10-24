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


async def test_get_platform_info_reports_the_platforms_own_approval_rules():
    client = FakePlatformClient()
    client.policy = {
        "approval_required_environments": ["dev", "production"],
        "signed_approval": True,
        "approvers": ["prasit.k@sti-th.com"],
    }
    data = (await discovery.get_platform_info(client))["data"]
    assert data["approval_required_environments"] == ["dev", "production"]
    summary = data["approval_rules_summary"]
    assert "dev, production" in summary and "prasit.k@sti-th.com" in summary and "signed" in summary
    assert "activate immediately" not in summary  # nothing is ungated


async def test_get_platform_info_production_only_says_dev_is_immediate():
    client = FakePlatformClient()
    data = (await discovery.get_platform_info(client))["data"]
    assert data["approval_required_environments"] == ["production"]
    assert "dev deployments activate immediately" in data["approval_rules_summary"]
    assert "an application owner's approval" in data["approval_rules_summary"]


async def test_get_platform_info_from_a_platform_without_policy_falls_back():
    client = FakePlatformClient()
    client.policy = None
    data = (await discovery.get_platform_info(client))["data"]
    assert data["approval_required_environments"] == ["production"]
    assert "production deployments pause" in data["approval_rules_summary"]
