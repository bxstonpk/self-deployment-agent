import pytest

from mcp_server.envelope import ErrorCode, ToolError
from mcp_server.tools import observability


async def test_get_application_logs_raises_honest_not_implemented_error():
    with pytest.raises(ToolError) as exc_info:
        await observability.get_application_logs("app-1", "dev")
    assert exc_info.value.code == ErrorCode.INTERNAL_ERROR
    assert "Module S" in exc_info.value.message
    assert "do not retry" in exc_info.value.message.lower()


async def test_get_application_metrics_raises_honest_not_implemented_error():
    with pytest.raises(ToolError) as exc_info:
        await observability.get_application_metrics("app-1", "dev")
    assert exc_info.value.code == ErrorCode.INTERNAL_ERROR
    assert "Module T" in exc_info.value.message
