import base64
import io
import tarfile

import pytest

from mcp_server.envelope import ErrorCode, ToolError
from mcp_server.idempotency import IdempotencyStore
from mcp_server.tools import deployment, governance

from .fakes import FakePlatformClient

YAML = "app:\n  name: leave\n  owner: HR\nservices:\n  api:\n    runtime: go\n    port: 8080\n"


async def _app(client: FakePlatformClient) -> str:
    app = await client.register_application("leave", "d", "dept-eng")
    await client.save_deployment_yaml(app["id"], YAML)
    await client.validate_application(app["id"])
    return app["id"]


def _project(tmp_path):
    (tmp_path / "api").mkdir()
    (tmp_path / "api" / "main.go").write_text("package main\n")
    (tmp_path / "api" / "go.mod").write_text("module leave\n")
    return str(tmp_path)


def _archive_b64() -> str:
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w:gz") as tar:
        data = b"package main\n"
        info = tarfile.TarInfo("api/main.go")
        info.size = len(data)
        tar.addfile(info, io.BytesIO(data))
    return base64.b64encode(buf.getvalue()).decode()


async def test_deploy_runs_the_tests_on_the_platform_first_and_carries_the_run(tmp_path):
    client = FakePlatformClient()
    app_id = await _app(client)
    result = await deployment.deploy_application(client, IdempotencyStore(60), app_id, "dev", project_path=_project(tmp_path))
    names = [c[0] for c in client.calls]
    assert names.index("run_tests") < names.index("trigger_build") < names.index("deploy_application")
    tested = next(c for c in client.calls if c[0] == "run_tests")[1][1]
    built = next(c for c in client.calls if c[0] == "trigger_build")[1][1]
    assert tested == built  # the tests ran on exactly what was built
    assert client.last_test_run_id == "run-1"
    assert result["data"]["test_run_id"] == "run-1"


async def test_failing_tests_build_nothing_and_show_the_report(tmp_path):
    client = FakePlatformClient()
    app_id = await _app(client)
    client.next_test_run = {"id": "run-2", "status": "failed",
                            "report": "Tests run by the platform\n== api (go): failed\n--- FAIL: TestLeave\n"}
    with pytest.raises(ToolError) as exc:
        await deployment.deploy_application(client, IdempotencyStore(60), app_id, "dev", project_path=_project(tmp_path))
    assert exc.value.code == ErrorCode.VALIDATION_ERROR
    assert "--- FAIL: TestLeave" in exc.value.message and "nothing was built" in exc.value.message
    assert not any(c[0] in ("trigger_build", "deploy_application") for c in client.calls)


async def test_run_tests_false_skips_them(tmp_path):
    client = FakePlatformClient()
    app_id = await _app(client)
    await deployment.deploy_application(client, IdempotencyStore(60), app_id, "dev", source_archive_base64=_archive_b64(),
                                        run_tests=False)
    assert not any(c[0] == "run_tests" for c in client.calls)
    assert client.last_test_run_id is None


async def test_a_given_test_run_is_passed_on_not_run_again(tmp_path):
    client = FakePlatformClient()
    app_id = await _app(client)
    await deployment.deploy_application(client, IdempotencyStore(60), app_id, "dev", project_path=_project(tmp_path),
                                        test_run_id="run-earlier")
    assert not any(c[0] == "run_tests" for c in client.calls)
    assert client.last_test_run_id == "run-earlier"


async def test_run_tests_tool_packs_the_saved_services_and_returns_the_report(tmp_path):
    client = FakePlatformClient()
    app_id = await _app(client)
    data = (await governance.run_tests(client, app_id, _project(tmp_path)))["data"]
    assert data["passed"] is True and data["test_run_id"] == "run-1"
    assert "Tests run by the platform" in data["report"]
    assert data["packed"]["services"] == ["api"]
