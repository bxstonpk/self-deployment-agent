import tempfile
from pathlib import Path

import httpx
import pytest

from mcp_server import credentials
from mcp_server.config import Config
from mcp_server.envelope import ErrorCode, ToolError
from mcp_server.platform_client import NOT_SIGNED_IN, PlatformClient

BASE = "http://platform-api.test"


def _client_with_transport(handler, signed_in: bool = True, platform_url: str = BASE) -> PlatformClient:
    path = Path(tempfile.mkdtemp()) / "credentials.json"
    if signed_in:
        credentials.save(credentials.StoredCredential(
            platform_url=platform_url, api_key="cpk_test-key", key_id="k1", key_name="laptop",
            email="alice@example.com", expires_at="2026-12-01T00:00:00Z",
        ), path)
    config = Config(
        platform_api_base_url=BASE,
        platform_api_timeout_seconds=5,
        idempotency_ttl_seconds=60,
        credentials_path=path,
    )
    transport = httpx.MockTransport(handler)
    http_client = httpx.AsyncClient(base_url=config.platform_api_base_url, transport=transport)
    return PlatformClient(config, http_client=http_client)


async def test_sends_the_machine_api_key_and_no_self_declared_identity():
    seen_headers = {}

    def handler(request: httpx.Request) -> httpx.Response:
        seen_headers.update(request.headers)
        return httpx.Response(200, json={"id": "app-1"})

    client = _client_with_transport(handler)
    await client.get_application("app-1")
    assert seen_headers["authorization"] == "Bearer cpk_test-key"
    assert not any(h.startswith("x-dev-") for h in seen_headers)


async def test_without_a_key_nothing_is_sent_and_the_agent_is_told_to_log_in():
    requests = []

    def handler(request: httpx.Request) -> httpx.Response:
        requests.append(request)
        return httpx.Response(200, json={})

    client = _client_with_transport(handler, signed_in=False)
    with pytest.raises(ToolError) as exc:
        await client.get_application("app-1")
    assert exc.value.code == ErrorCode.UNAUTHORIZED
    assert "login" in exc.value.message
    assert requests == []


async def test_a_key_for_another_platform_is_not_used():
    client = _client_with_transport(lambda r: httpx.Response(200, json={}), platform_url="http://elsewhere:8090")
    with pytest.raises(ToolError) as exc:
        await client.get_application("app-1")
    assert exc.value.code == ErrorCode.UNAUTHORIZED


async def test_a_rejected_key_says_how_to_sign_in_again():
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(401, json={"error": {"code": "unauthorized", "message": "missing, unknown, expired or revoked credential"}})

    client = _client_with_transport(handler)
    with pytest.raises(ToolError) as exc:
        await client.get_application("app-1")
    assert exc.value.code == ErrorCode.UNAUTHORIZED
    assert exc.value.message == NOT_SIGNED_IN


@pytest.mark.parametrize(
    "status,platform_code,expected",
    [
        (400, "invalid_environment", ErrorCode.VALIDATION_ERROR),
        (401, "unauthorized", ErrorCode.UNAUTHORIZED),
        (403, "forbidden", ErrorCode.UNAUTHORIZED),
        (404, "not_found", ErrorCode.NOT_FOUND),
        (409, "name_taken", ErrorCode.CONFLICT),
        (409, "not_running", ErrorCode.VALIDATION_ERROR),  # wrong-state, not a real conflict
        (409, "invalid_rollback_target", ErrorCode.VALIDATION_ERROR),
        (409, "admin_hold", ErrorCode.POLICY_VIOLATION),  # an administrator's suspension (DEC-030)
        (413, "source_archive_too_large", ErrorCode.QUOTA_EXCEEDED),
        (500, "internal_error", ErrorCode.INTERNAL_ERROR),
    ],
)
async def test_error_status_and_code_mapping(status, platform_code, expected):
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(status, json={"error": {"code": platform_code, "message": "boom"}})

    client = _client_with_transport(handler)
    with pytest.raises(ToolError) as exc_info:
        await client.get_application("app-1")
    assert exc_info.value.code == expected
    assert exc_info.value.platform_code == platform_code


async def test_network_failure_maps_to_internal_error():
    def handler(request: httpx.Request) -> httpx.Response:
        raise httpx.ConnectError("connection refused")

    client = _client_with_transport(handler)
    with pytest.raises(ToolError) as exc_info:
        await client.get_application("app-1")
    assert exc_info.value.code == ErrorCode.INTERNAL_ERROR


async def test_list_departments_normalizes_go_pascal_case_keys():
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(
            200,
            json={
                "departments": [
                    {"ID": "d1", "Name": "Engineering", "CostCenterCode": "ENG-01", "Status": "active"}
                ]
            },
        )

    client = _client_with_transport(handler)
    departments = await client.list_departments()
    assert departments == [
        {"id": "d1", "name": "Engineering", "cost_center_code": "ENG-01", "status": "active"}
    ]


async def test_trigger_build_sends_raw_bytes_with_gzip_content_type():
    seen = {}

    def handler(request: httpx.Request) -> httpx.Response:
        seen["content_type"] = request.headers.get("content-type")
        seen["body"] = request.content
        seen["authorization"] = request.headers.get("authorization")
        return httpx.Response(200, json={"id": "b1", "status": "succeeded"})

    client = _client_with_transport(handler)
    result = await client.trigger_build("app-1", b"raw-tar-gz-bytes")
    assert seen["content_type"] == "application/gzip"
    assert seen["body"] == b"raw-tar-gz-bytes"
    assert seen["authorization"] == "Bearer cpk_test-key"  # the key is still attached
    assert result["status"] == "succeeded"


async def test_trigger_build_failure_is_a_normal_200_response_not_a_toolerror():
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(
            200, json={"id": "b1", "status": "failed", "error_category": "source", "error_detail": "boom"}
        )

    client = _client_with_transport(handler)
    result = await client.trigger_build("app-1", b"bytes")
    assert result["status"] == "failed"
    assert result["error_category"] == "source"


async def test_query_audit_log_omits_none_valued_filters_from_the_query_string():
    seen = {}

    def handler(request: httpx.Request) -> httpx.Response:
        seen["path"] = request.url.path
        seen["params"] = dict(request.url.params)
        return httpx.Response(200, json={"entries": [{"id": "a1"}]})

    client = _client_with_transport(handler)
    entries = await client.query_audit_log({"resource_type": "application", "resource_id": None, "action": None})
    assert seen["path"] == "/audit-log"
    assert seen["params"] == {"resource_type": "application"}
    assert entries == [{"id": "a1"}]


async def test_list_notifications_sends_unread_only_flag_only_when_true():
    seen = {}

    def handler(request: httpx.Request) -> httpx.Response:
        seen["params"] = dict(request.url.params)
        return httpx.Response(200, json={"notifications": [{"id": "n1"}]})

    client = _client_with_transport(handler)
    await client.list_notifications(unread_only=True)
    assert seen["params"] == {"unread_only": "true"}

    await client.list_notifications(unread_only=False)
    assert seen["params"] == {}


async def test_mark_notification_read_posts_to_the_right_path():
    seen = {}

    def handler(request: httpx.Request) -> httpx.Response:
        seen["method"] = request.method
        seen["path"] = request.url.path
        return httpx.Response(200, json={"id": "n1", "read_at": "2026-01-01T00:00:00Z"})

    client = _client_with_transport(handler)
    result = await client.mark_notification_read("n1")
    assert seen["method"] == "POST"
    assert seen["path"] == "/notifications/n1/read"
    assert result["read_at"] == "2026-01-01T00:00:00Z"
