from pathlib import Path

import pytest

from mcp_server.envelope import ErrorCode, ToolError
from mcp_server.idempotency import IdempotencyStore
from mcp_server.tools import governance

from .fakes import FakePlatformClient

REPO_TEMPLATES = Path(__file__).resolve().parents[3] / "company-deployment-skill" / "templates"


async def _start(client: FakePlatformClient, **kw) -> dict:
    args = {"name": "overtime", "purpose": "HR overtime requests", "handles_personal_data": True,
            "users": "HR, about 20 people", "data_description": "employee IDs and names", "api_runtime": "go"}
    args.update(kw)
    return await governance.start_project(client, IdempotencyStore(60), **args)


async def test_get_standards_lists_only_what_is_allowed():
    client = FakePlatformClient()
    result = await governance.get_standards(client)
    data = result["data"]
    assert data["allowed_stacks"] == {"frontend": ["react"], "backend": ["go", "python"], "database": ["mysql"]}
    assert data["not_allowed_libraries"] == [{"ecosystem": "npm", "package": "left-pad", "reason": "unmaintained"}]
    assert "start_project" in data["how_to_start"]


async def test_start_project_registers_with_the_standard_yaml_and_records_the_profile():
    client = FakePlatformClient()
    result = await _start(client)
    data = result["data"]
    assert data["registered"] is True
    app = client.applications[data["application_id"]]
    assert "runtime: go" in app["deployment_yaml_draft"] and "type: mysql" in app["deployment_yaml_draft"]
    assert "owner: Engineering" in app["deployment_yaml_draft"]
    profile = client.profiles[data["application_id"]]
    assert profile["purpose"] == "HR overtime requests"
    assert profile["handles_personal_data"] is True
    assert profile["backend"] == "go"


async def test_start_project_refuses_a_stack_the_standard_doesnt_allow_before_registering():
    client = FakePlatformClient()
    with pytest.raises(ToolError) as exc:
        await _start(client, api_runtime="php")
    assert exc.value.code == ErrorCode.VALIDATION_ERROR
    assert "request_exception" in exc.value.details[0].reason
    assert not any(c[0] == "register_application" for c in client.calls)


async def test_start_project_personal_data_needs_saying_which():
    client = FakePlatformClient()
    with pytest.raises(ToolError) as exc:
        await _start(client, data_description="  ")
    assert "data_description" in exc.value.message
    assert not client.applications


async def test_start_project_says_how_to_finish_when_the_profile_didnt_save():
    client = FakePlatformClient()
    client.profile_error = ToolError(ErrorCode.INTERNAL_ERROR, "boom")
    with pytest.raises(ToolError) as exc:
        await _start(client)
    app_id = next(iter(client.applications))
    assert f"application_id={app_id}" in exc.value.message

    client.profile_error = None
    result = await _start(client, application_id=app_id, api_runtime="")
    assert result["data"]["registered"] is False
    assert app_id in client.profiles
    assert sum(1 for c in client.calls if c[0] == "register_application") == 1


async def test_scaffold_project_copies_the_company_templates_for_the_stack(tmp_path):
    client = FakePlatformClient()
    app_id = (await _start(client))["data"]["application_id"]
    project = tmp_path / "overtime"
    result = await governance.scaffold_project(client, app_id, str(project), templates=REPO_TEMPLATES)
    files = result["data"]["files"]
    assert "api/main.go" in files and "api/go.mod" in files
    assert "frontend/package.json" in files and "frontend/src/App.jsx" in files
    assert not any("node_modules" in f for f in files)
    assert (project / "deployment.yaml").read_text(encoding="utf-8") == client.applications[app_id]["deployment_yaml_draft"]
    review = (project / "REVIEW.md").read_text(encoding="utf-8")
    assert review.startswith("# overtime ")
    assert (project / "STANDARD.md").is_file()


async def test_scaffold_project_never_overwrites(tmp_path):
    client = FakePlatformClient()
    app_id = (await _start(client))["data"]["application_id"]
    (tmp_path / "keep.txt").write_text("mine")
    with pytest.raises(ToolError) as exc:
        await governance.scaffold_project(client, app_id, str(tmp_path), templates=REPO_TEMPLATES)
    assert "isn't empty" in exc.value.message
    assert (tmp_path / "keep.txt").read_text() == "mine"


async def test_scaffold_project_without_templates_says_where_to_get_them(tmp_path):
    client = FakePlatformClient()
    app_id = (await _start(client))["data"]["application_id"]
    with pytest.raises(ToolError) as exc:
        await governance.scaffold_project(client, app_id, str(tmp_path / "p"), templates=tmp_path / "missing")
    assert "COMPANY_DEPLOY_TEMPLATES" in exc.value.message


async def _scaffolded(client: FakePlatformClient, tmp_path: Path) -> tuple[str, Path]:
    app_id = (await _start(client))["data"]["application_id"]
    project = tmp_path / "overtime"
    await governance.scaffold_project(client, app_id, str(project), templates=REPO_TEMPLATES)
    return app_id, project


async def test_check_project_reports_a_library_the_company_doesnt_allow(tmp_path):
    client = FakePlatformClient()
    app_id, project = await _scaffolded(client, tmp_path)
    client.next_standards_check = {
        "passed": False, "has_profile": True, "profile_required": True, "dependencies_found": 4,
        "violations": [{"ecosystem": "npm", "package": "left-pad", "manifest": "frontend/package.json",
                        "reason": "unmaintained", "exception_subject": "npm:left-pad"}],
        "allowed_by_exception": [],
    }
    data = (await governance.check_project(client, app_id, str(project)))["data"]
    assert data["passed"] is False
    deps = [f for f in data["findings"] if f["check"] == "dependencies"]
    assert len(deps) == 1 and "npm:left-pad" in deps[0]["message"]
    sent = next(c for c in client.calls if c[0] == "check_standards")
    assert sent[1][0] == app_id and sent[1][1][:2] == b"\x1f\x8b"


async def test_check_project_reports_hash_routing(tmp_path):
    client = FakePlatformClient()
    app_id, project = await _scaffolded(client, tmp_path)
    client.next_standards_check = {
        "passed": False, "has_profile": True, "profile_required": True, "dependencies_found": 3,
        "violations": [], "allowed_by_exception": [],
        "hash_routing": [{"service": "frontend", "file": "frontend/src/main.jsx", "line": 4, "api": "HashRouter",
                          "exception_subject": "hash-routing"}],
    }
    data = (await governance.check_project(client, app_id, str(project)))["data"]
    assert data["passed"] is False
    routing = [f for f in data["findings"] if f["check"] == "routing"]
    assert len(routing) == 1 and routing[0]["severity"] == "error"
    assert "frontend/src/main.jsx:4" in routing[0]["message"] and "?q=" in routing[0]["message"]


async def test_check_project_flags_an_unfilled_review_and_pending_exceptions(tmp_path):
    client = FakePlatformClient()
    app_id, project = await _scaffolded(client, tmp_path)
    await governance.request_exception(client, app_id, "stack", "vue", "the team knows it")
    data = (await governance.check_project(client, app_id, str(project)))["data"]
    checks = {(f["check"], f["severity"]) for f in data["findings"]}
    assert ("REVIEW.md", "warning") in checks
    assert ("exceptions", "warning") in checks
    assert data["passed"] is True  # warnings, no errors


async def test_check_project_without_review_is_an_error_where_approval_waits(tmp_path):
    client = FakePlatformClient()
    app_id, project = await _scaffolded(client, tmp_path)
    (project / "REVIEW.md").unlink()
    data = (await governance.check_project(client, app_id, str(project)))["data"]
    assert data["passed"] is False
    assert any(f["check"] == "REVIEW.md" and f["severity"] == "error" for f in data["findings"])


async def test_request_exception_relays_the_platforms_answer():
    client = FakePlatformClient()
    app_id = (await _start(client))["data"]["application_id"]
    data = (await governance.request_exception(client, app_id, "dependency", "npm:left-pad", "a vendor SDK"))["data"]
    assert data["created"] is True and data["exception"]["status"] == "pending"
    assert "Until it is approved" in data["note"]
    listed = (await governance.list_exceptions(client, app_id))["data"]["exceptions"]
    assert [e["subject"] for e in listed] == ["npm:left-pad"]
