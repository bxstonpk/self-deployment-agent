import pytest

from mcp_server.envelope import ErrorCode, ToolError
from mcp_server.tools import observability

from .fakes import FakePlatformClient


def _entry(ts: str, message: str, stream: str = "stdout") -> dict:
    return {"timestamp": ts, "service": "api", "stream": stream, "instance": "abc123def456", "message": message}


async def _app(client: FakePlatformClient) -> str:
    return (await client.register_application("overtime", "desc", "dept-eng"))["id"]


async def test_get_application_logs_returns_the_platform_entries():
    client = FakePlatformClient()
    app_id = await _app(client)
    client.logs[app_id] = [_entry("2026-01-02T00:00:01Z", "crashed", "stderr"), _entry("2026-01-02T00:00:00Z", "listening")]

    result = await observability.get_application_logs(client, app_id, "dev")
    assert result["status"] == "success"
    assert [e["message"] for e in result["data"]["entries"]] == ["crashed", "listening"]
    assert result["data"]["entries"][0]["stream"] == "stderr"
    assert result["data"]["entries"][0]["level"] is None  # levels aren't parsed, and nothing pretends otherwise
    assert client.last_log_params["environment"] == "dev"
    assert client.last_log_params["limit"] == 200


async def test_time_range_becomes_since_and_the_cursor_is_passed_through():
    client = FakePlatformClient()
    app_id = await _app(client)
    await observability.get_application_logs(client, app_id, "dev", time_range="15m", cursor="1757563506123456.42")
    assert client.last_log_params["since"]
    assert client.last_log_params["cursor"] == "1757563506123456.42"


async def test_invalid_time_range_is_rejected_without_calling_the_platform():
    client = FakePlatformClient()
    app_id = await _app(client)
    with pytest.raises(ToolError) as exc_info:
        await observability.get_application_logs(client, app_id, "dev", time_range="yesterday")
    assert exc_info.value.code == ErrorCode.VALIDATION_ERROR
    assert client.last_log_params is None


async def test_tail_lines_out_of_range_is_rejected():
    client = FakePlatformClient()
    app_id = await _app(client)
    with pytest.raises(ToolError) as exc_info:
        await observability.get_application_logs(client, app_id, "dev", tail_lines=5000)
    assert exc_info.value.code == ErrorCode.VALIDATION_ERROR


async def test_a_severity_filter_is_not_silently_ignored():
    client = FakePlatformClient()
    app_id = await _app(client)
    result = await observability.get_application_logs(client, app_id, "dev", severity="error")
    assert "severity='error' was not applied" in result["data"]["note"]


async def test_next_cursor_only_when_the_page_is_full():
    client = FakePlatformClient()
    app_id = await _app(client)
    client.logs[app_id] = [_entry("2026-01-02T00:00:01Z", "b"), _entry("2026-01-02T00:00:00Z", "a")]
    full = await observability.get_application_logs(client, app_id, "dev", tail_lines=2)
    partial = await observability.get_application_logs(client, app_id, "dev", tail_lines=3)
    assert full["data"]["next_cursor"] == "cursor-after-2"
    assert partial["data"]["next_cursor"] is None


async def test_someone_elses_application_is_not_found():
    client = FakePlatformClient()
    with pytest.raises(ToolError) as exc_info:
        await observability.get_application_logs(client, "not-mine", "dev")
    assert exc_info.value.code == ErrorCode.NOT_FOUND


async def test_get_application_metrics_returns_the_platform_series():
    client = FakePlatformClient()
    app_id = await _app(client)

    result = await observability.get_application_metrics(client, app_id, "dev")
    data = result["data"]
    assert result["status"] == "success"
    assert data["summary"]["requests"] == 10
    assert data["series"]["cpu_percent"][0]["value"] == 12.5
    assert data["series"]["requests_per_minute"][0]["value"] == 10
    assert data["series"]["latency_ms"][0]["max"] == 40.0
    # The MCP contract asks for these alongside the series (§13.10).
    assert data["instances"][0]["instances"] == 1
    assert data["last_scale_event"]["direction"] == "scaled_up"
    assert client.last_metric_params["environment"] == "dev"


async def test_metric_types_select_the_series_returned():
    client = FakePlatformClient()
    app_id = await _app(client)

    result = await observability.get_application_metrics(client, app_id, "dev", metric_types=["cpu"])
    assert list(result["data"]["series"]) == ["cpu_percent"]


async def test_an_unknown_metric_type_is_rejected_with_the_supported_ones():
    client = FakePlatformClient()
    app_id = await _app(client)

    with pytest.raises(ToolError) as exc_info:
        await observability.get_application_metrics(client, app_id, "dev", metric_types=["cpu", "disk"])
    assert exc_info.value.code == ErrorCode.VALIDATION_ERROR
    assert "disk" in exc_info.value.message
    assert client.last_metric_params is None


async def test_metrics_time_range_becomes_a_from_timestamp():
    client = FakePlatformClient()
    app_id = await _app(client)

    await observability.get_application_metrics(client, app_id, "dev", time_range="24h")
    assert client.last_metric_params["from"]


async def test_metrics_pass_on_the_platforms_own_account_of_collection():
    client = FakePlatformClient()
    app_id = await _app(client)
    client.metrics = dict(client.metrics)
    client.metrics["collection"] = {"collecting": False, "last_sample_at": None,
                                    "note": "A container is running, but no resource reading has been taken yet."}

    result = await observability.get_application_metrics(client, app_id, "dev")
    assert result["data"]["collecting"] is False
    assert "no resource reading has been taken yet" in result["data"]["note"]


async def test_metrics_for_someone_elses_application_are_not_found():
    client = FakePlatformClient()
    with pytest.raises(ToolError) as exc_info:
        await observability.get_application_metrics(client, "not-mine", "dev")
    assert exc_info.value.code == ErrorCode.NOT_FOUND
