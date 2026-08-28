import base64

import pytest

from mcp_server.envelope import ErrorCode, ToolError
from mcp_server.idempotency import IdempotencyStore
from mcp_server.tools import deployment

from .fakes import FakePlatformClient


async def _registered_app(client: FakePlatformClient) -> dict:
    return await client.register_application("overtime", "desc", "dept-eng")


async def test_deploy_application_dev_succeeds_immediately():
    client = FakePlatformClient()
    idem = IdempotencyStore(ttl_seconds=60)
    app = await _registered_app(client)
    result = await deployment.deploy_application(client, idem, app["id"], "dev")
    assert result["status"] == "success"
    assert result["data"]["status"] == "running"


async def test_deploy_application_production_is_pending_approval_not_an_error():
    client = FakePlatformClient()
    idem = IdempotencyStore(ttl_seconds=60)
    app = await _registered_app(client)
    result = await deployment.deploy_application(client, idem, app["id"], "production")
    assert result["status"] == "success"  # PENDING_APPROVAL is Section 8's "not a failure"
    assert result["data"]["mcp_status"] == "PENDING_APPROVAL"


async def test_deploy_application_no_successful_build_and_no_source_gives_actionable_message():
    client = FakePlatformClient()
    idem = IdempotencyStore(ttl_seconds=60)

    async def fail_deploy(application_id, environment):
        raise ToolError(ErrorCode.VALIDATION_ERROR, "application has no successful build to deploy", platform_code="no_successful_build")

    client.deploy_application = fail_deploy  # type: ignore[method-assign]
    with pytest.raises(ToolError) as exc_info:
        await deployment.deploy_application(client, idem, "app-1", "dev")
    assert "source_archive_base64" in exc_info.value.message


async def test_deploy_application_with_source_triggers_build_then_deploys():
    client = FakePlatformClient()
    idem = IdempotencyStore(ttl_seconds=60)
    app = await _registered_app(client)
    source_b64 = base64.b64encode(b"fake-tar-gz-bytes").decode()

    result = await deployment.deploy_application(
        client, idem, app["id"], "dev", source_archive_base64=source_b64
    )
    assert result["status"] == "success"
    assert result["data"]["status"] == "running"
    build_calls = [c for c in client.calls if c[0] == "trigger_build"]
    assert len(build_calls) == 1
    assert build_calls[0][1] == (app["id"], b"fake-tar-gz-bytes")


async def test_deploy_application_invalid_base64_is_rejected_before_any_platform_call():
    client = FakePlatformClient()
    idem = IdempotencyStore(ttl_seconds=60)
    app = await _registered_app(client)

    with pytest.raises(ToolError) as exc_info:
        await deployment.deploy_application(
            client, idem, app["id"], "dev", source_archive_base64="not-valid-base64!!"
        )
    assert exc_info.value.code == ErrorCode.VALIDATION_ERROR
    assert not any(c[0] in ("trigger_build", "deploy_application") for c in client.calls)


async def test_deploy_application_source_build_failure_source_category_is_validation_error():
    client = FakePlatformClient()
    idem = IdempotencyStore(ttl_seconds=60)
    app = await _registered_app(client)
    client.next_build_result = {"status": "failed", "error_category": "source", "error_detail": "npm install failed"}
    source_b64 = base64.b64encode(b"broken-source").decode()

    with pytest.raises(ToolError) as exc_info:
        await deployment.deploy_application(client, idem, app["id"], "dev", source_archive_base64=source_b64)
    assert exc_info.value.code == ErrorCode.VALIDATION_ERROR
    assert "npm install failed" in exc_info.value.message
    deploy_calls = [c for c in client.calls if c[0] == "deploy_application"]
    assert not deploy_calls  # never reached deploy — build failed first


async def test_deploy_application_source_build_failure_platform_category_is_internal_error():
    client = FakePlatformClient()
    idem = IdempotencyStore(ttl_seconds=60)
    app = await _registered_app(client)
    client.next_build_result = {"status": "failed", "error_category": "platform", "error_detail": "registry pull timeout"}
    source_b64 = base64.b64encode(b"source").decode()

    with pytest.raises(ToolError) as exc_info:
        await deployment.deploy_application(client, idem, app["id"], "dev", source_archive_base64=source_b64)
    assert exc_info.value.code == ErrorCode.INTERNAL_ERROR


async def test_deploy_application_idempotency_key_prevents_double_deploy():
    client = FakePlatformClient()
    idem = IdempotencyStore(ttl_seconds=60)
    app = await _registered_app(client)
    await deployment.deploy_application(client, idem, app["id"], "dev", idempotency_key="k1")
    await deployment.deploy_application(client, idem, app["id"], "dev", idempotency_key="k1")
    deploy_calls = [c for c in client.calls if c[0] == "deploy_application"]
    assert len(deploy_calls) == 1


async def test_get_application_status_reports_url_when_running():
    client = FakePlatformClient()
    idem = IdempotencyStore(ttl_seconds=60)
    app = await _registered_app(client)
    await deployment.deploy_application(client, idem, app["id"], "dev")
    result = await deployment.get_application_status(client, app["id"])
    assert result["data"]["current_lifecycle_state"] == "running"
    assert result["data"]["url"] == "http://localhost:9999"


async def test_get_application_status_no_deployment_yet_does_not_error():
    client = FakePlatformClient()
    app = await _registered_app(client)
    result = await deployment.get_application_status(client, app["id"])
    assert result["status"] == "success"
    assert result["data"]["latest_deployment_id"] is None


async def test_get_deployment_status_maps_platform_status_to_mcp_phase():
    client = FakePlatformClient()
    idem = IdempotencyStore(ttl_seconds=60)
    app = await _registered_app(client)
    deployed = await deployment.deploy_application(client, idem, app["id"], "dev")
    result = await deployment.get_deployment_status(client, deployed["data"]["deployment_id"])
    assert result["data"]["phase"] == "COMPLETED"
    assert result["data"]["result"]["url"] == "http://localhost:9999"


async def test_rollback_application_resolves_previous_keyword():
    client = FakePlatformClient()
    idem = IdempotencyStore(ttl_seconds=60)
    app = await _registered_app(client)
    v1 = await deployment.deploy_application(client, idem, app["id"], "dev")
    v1_id = v1["data"]["deployment_id"]
    # Force a second, distinct (and strictly later, per _tick()) deployment
    # so v1 becomes superseded and history has a real newest-first order.
    client.deployments[v1_id]["status"] = "superseded"
    v2_id = "dep-v2"
    client.deployments[v2_id] = {
        **client.deployments[v1_id],
        "id": v2_id,
        "status": "running",
        "created_at": client._tick(),
    }
    client.applications[app["id"]]["_latest_deployment_id"] = v2_id

    result = await deployment.rollback_application(client, idem, app["id"], "previous")
    assert result["status"] == "success"
    assert result["data"]["target_version"] == v1_id


async def test_rollback_application_previous_with_no_prior_version_is_not_found():
    client = FakePlatformClient()
    idem = IdempotencyStore(ttl_seconds=60)
    app = await _registered_app(client)
    with pytest.raises(ToolError) as exc_info:
        await deployment.rollback_application(client, idem, app["id"], "previous")
    assert exc_info.value.code == ErrorCode.NOT_FOUND


async def test_restart_application_returns_completed_status():
    client = FakePlatformClient()
    idem = IdempotencyStore(ttl_seconds=60)
    app = await _registered_app(client)
    await deployment.deploy_application(client, idem, app["id"], "dev")
    result = await deployment.restart_application(client, idem, app["id"])
    assert result["status"] == "success"
    assert result["data"]["status"] == "COMPLETED"
