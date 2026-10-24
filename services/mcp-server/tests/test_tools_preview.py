import pytest

from mcp_server.envelope import ErrorCode, ToolError
from mcp_server.tools import preview

from .fakes import FakePlatformClient

YAML = "app:\n  name: leave\n  owner: HR\nservices:\n  api:\n    runtime: go\n    port: 8080\n"


async def _app(client: FakePlatformClient) -> str:
    app = await client.register_application("leave", "d", "dept-eng")
    await client.save_deployment_yaml(app["id"], YAML)
    return app["id"]


def _project(tmp_path):
    (tmp_path / "api").mkdir()
    (tmp_path / "api" / "main.go").write_text("package main\n")
    return str(tmp_path)


async def test_preview_tests_then_starts_on_the_same_source(tmp_path):
    client = FakePlatformClient()
    app_id = await _app(client)
    data = (await preview.preview_application(client, app_id, _project(tmp_path)))["data"]
    names = [c[0] for c in client.calls]
    assert names.index("run_tests") < names.index("start_preview")
    tested = next(c for c in client.calls if c[0] == "run_tests")[1][1]
    started = next(c for c in client.calls if c[0] == "start_preview")[1][1]
    assert tested == started
    assert data["url"].endswith("/leave-preview-a1b2/") and data["expires_at"]
    assert "EMPTY database" in data["note"]


async def test_failing_tests_make_no_preview(tmp_path):
    client = FakePlatformClient()
    app_id = await _app(client)
    client.next_test_run = {"id": "r", "status": "failed", "report": "--- FAIL: TestLeave\n"}
    with pytest.raises(ToolError) as exc:
        await preview.preview_application(client, app_id, _project(tmp_path))
    assert exc.value.code == ErrorCode.VALIDATION_ERROR and "TestLeave" in exc.value.message
    assert not any(c[0] == "start_preview" for c in client.calls)


async def test_a_preview_that_doesnt_build_says_why(tmp_path):
    client = FakePlatformClient()
    app_id = await _app(client)
    client.next_preview = {"preview": {"name": "leave-preview-a1b2"},
                           "build": {"status": "failed", "error_detail": "build failed: main.go:3: syntax error"}}
    with pytest.raises(ToolError) as exc:
        await preview.preview_application(client, app_id, _project(tmp_path), run_tests=False)
    assert "it was removed" in exc.value.message and "syntax error" in exc.value.message


async def test_stop_preview():
    client = FakePlatformClient()
    data = (await preview.stop_preview(client, "app-1"))["data"]
    assert data["stopped"] == "leave-preview-a1b2"
