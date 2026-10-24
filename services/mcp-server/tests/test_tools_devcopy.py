import pytest

from mcp_server.envelope import ErrorCode, ToolError
from mcp_server.idempotency import IdempotencyStore
from mcp_server.tools import deployment, devcopy

from .fakes import FakePlatformClient

YAML = "app:\n  name: leave\n  owner: HR\nservices:\n  api:\n    runtime: go\n    port: 8080\n"


async def _app(client: FakePlatformClient) -> str:
    client.policy = {"approval_required_environments": ["dev", "production"], "signed_approval": True,
                     "approvers": ["a@x"], "dev_copies": True}
    app = await client.register_application("leave", "d", "dept-eng")
    await client.save_deployment_yaml(app["id"], YAML)
    await client.validate_application(app["id"])
    return app["id"]


def _project(tmp_path, review: bool = False):
    (tmp_path / "api").mkdir(exist_ok=True)
    (tmp_path / "api" / "main.go").write_text("package main\n")
    if review:
        (tmp_path / "REVIEW.md").write_text("# leave")
    return str(tmp_path)


async def _deploy(client, app_id, env, tmp_path, **kw):
    return await deployment.deploy_application(client, IdempotencyStore(60), app_id, env, project_path=_project(tmp_path, True), **kw)


async def test_dev_goes_to_the_dev_copy_fast_without_review_or_approval(tmp_path):
    client = FakePlatformClient()
    app_id = await _app(client)
    data = (await deployment.deploy_application(client, IdempotencyStore(60), app_id, "dev",
                                                project_path=_project(tmp_path)))["data"]
    names = [c[0] for c in client.calls]
    assert "deploy_dev" in names and "run_tests" not in names  # dev is fast: tests only when asked
    assert "trigger_build" not in names and "deploy_application" not in names
    assert data["url"].endswith("/leave-dev/") and data["environment"] == "dev"
    tell = data["tell_the_employee"]
    assert "Give the employee the url now" in tell and "not a local file" in tell and "record_test_data" in tell
    assert "don't push it" in tell


async def test_dev_runs_the_tests_when_asked(tmp_path):
    client = FakePlatformClient()
    app_id = await _app(client)
    await deployment.deploy_application(client, IdempotencyStore(60), app_id, "dev",
                                        project_path=_project(tmp_path), run_tests=True)
    names = [c[0] for c in client.calls]
    assert names.index("run_tests") < names.index("deploy_dev")


async def test_production_runs_the_tests_by_default(tmp_path):
    client = FakePlatformClient()
    app_id = await _app(client)
    await _deploy(client, app_id, "production", tmp_path)
    assert any(c[0] == "run_tests" for c in client.calls)


async def test_scaffold_puts_the_new_application_up_on_dev_and_returns_the_link(tmp_path):
    from pathlib import Path

    from mcp_server.tools import governance

    templates = Path(__file__).resolve().parents[3] / "company-deployment-skill" / "templates"
    client = FakePlatformClient()
    client.policy = {"approval_required_environments": ["dev", "production"], "dev_copies": True}
    app = await client.register_application("leave", "d", "dept-eng")
    await client.save_deployment_yaml(app["id"], YAML.replace("services:\n", "services:\n  frontend:\n    runtime: react\n"))
    data = (await governance.scaffold_project(client, app["id"], str(tmp_path / "leave"), templates=templates))["data"]
    assert data["url"].endswith("/leave-dev/") and data["dev"]["deployed"] is True
    assert "Give the employee the url now" in data["tell_the_employee"]
    sent = next(c for c in client.calls if c[0] == "deploy_dev")[1][1]
    assert sent[:2] == b"\x1f\x8b"


async def test_production_first_asks_about_the_dev_data_naming_claudes_test_data(tmp_path):
    client = FakePlatformClient()
    app_id = await _app(client)
    await deployment.deploy_application(client, IdempotencyStore(60), app_id, "dev", project_path=_project(tmp_path))
    await devcopy.record_test_data(client, app_id, "3 test leave requests")
    with pytest.raises(ToolError) as exc:
        await _deploy(client, app_id, "production", tmp_path)
    msg = exc.value.message
    assert exc.value.code == ErrorCode.VALIDATION_ERROR and "ASK the employee" in msg
    assert "3 test leave requests" in msg and "Nothing was built" in msg
    assert not any(c[0] == "trigger_build" for c in client.calls)

    await _deploy(client, app_id, "production", tmp_path, dev_data="copy")
    assert client.last_copy_dev_data is True


async def test_leave_deploys_production_empty(tmp_path):
    client = FakePlatformClient()
    app_id = await _app(client)
    await deployment.deploy_application(client, IdempotencyStore(60), app_id, "dev", project_path=_project(tmp_path))
    await _deploy(client, app_id, "production", tmp_path, dev_data="leave")
    assert client.last_copy_dev_data is False


async def test_no_question_once_production_has_run(tmp_path):
    client = FakePlatformClient()
    app_id = await _app(client)
    await deployment.deploy_application(client, IdempotencyStore(60), app_id, "dev", project_path=_project(tmp_path))
    client.deployments["earlier"] = {"id": "earlier", "application_id": app_id, "environment": "production",
                                     "status": "superseded", "created_at": "2026-01-01T00:00:00Z"}
    await _deploy(client, app_id, "production", tmp_path)
    assert client.last_copy_dev_data is False
    with pytest.raises(ToolError) as exc:
        await _deploy(client, app_id, "production", tmp_path, dev_data="copy")
    assert "only by the first production deployment" in exc.value.message


async def test_no_dev_copy_no_question(tmp_path):
    client = FakePlatformClient()
    app_id = await _app(client)
    await _deploy(client, app_id, "production", tmp_path)
    assert client.last_copy_dev_data is False


async def test_a_dev_copy_that_doesnt_build_says_its_running_version_stays(tmp_path):
    client = FakePlatformClient()
    app_id = await _app(client)
    client.next_dev = {**client.next_dev, "build": {"status": "failed", "error_category": "source",
                                                    "error_detail": "build failed: main.go:3: syntax error"}}
    with pytest.raises(ToolError) as exc:
        await deployment.deploy_application(client, IdempotencyStore(60), app_id, "dev", project_path=_project(tmp_path))
    assert "syntax error" in exc.value.message and "as they were" in exc.value.message


async def test_reset_dev():
    client = FakePlatformClient()
    data = (await devcopy.reset_dev(client, "app-1"))["data"]
    assert data["removed"] == "leave-dev"
