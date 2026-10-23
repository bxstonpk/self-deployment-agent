import io
import tarfile
import tempfile
from pathlib import Path

import pytest

from mcp_server import source_packer
from mcp_server.envelope import ErrorCode, ToolError
from mcp_server.idempotency import IdempotencyStore
from mcp_server.source_packer import PackError, pack_project
from mcp_server.tools import deployment

from .fakes import FakePlatformClient


def _project() -> Path:
    root = Path(tempfile.mkdtemp()) / "leave-tracker"
    files = {
        "deployment.yaml": "app:\n  name: leave\n",
        "frontend/package.json": "{}",
        "frontend/src/App.jsx": "export default 1",
        "frontend/node_modules/react/index.js": "huge",
        "frontend/build/index.html": "old output",
        "frontend/.env": "VITE_SECRET=1",
        "frontend/.env.example": "VITE_API=",
        "api/app.py": "print('hi')",
        "api/requirements.txt": "flask",
        "api/.env.local": "DATABASE_PASSWORD=hunter2",
        "api/certs/server.key": "-----BEGIN PRIVATE KEY-----",
        "api/__pycache__/app.cpython-313.pyc": "x",
        "api/.git/config": "x",
        "notes/private.txt": "not a service",
    }
    for rel, content in files.items():
        path = root / rel
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content)
    return root


def _names(archive: bytes) -> set[str]:
    with tarfile.open(fileobj=io.BytesIO(archive), mode="r:gz") as tar:
        return set(tar.getnames())


def test_packs_only_service_folders_and_leaves_out_dependencies_output_and_secrets():
    root = _project()
    archive, summary = pack_project(str(root), ["frontend", "api"])
    names = _names(archive)
    assert names == {
        "frontend/package.json",
        "frontend/src/App.jsx",
        "frontend/.env.example",
        "api/app.py",
        "api/requirements.txt",
    }
    assert sorted(summary.skipped_secret_files) == ["api/.env.local", "api/certs/server.key", "frontend/.env"]
    assert b"hunter2" not in archive  # compressed, but check the contents too
    with tarfile.open(fileobj=io.BytesIO(archive), mode="r:gz") as tar:
        assert all(b"hunter2" not in tar.extractfile(m).read() for m in tar.getmembers() if m.isfile())
        assert all(m.uid == 0 and m.uname == "" for m in tar.getmembers())


def test_requires_an_absolute_path_and_a_folder_per_service():
    root = _project()
    with pytest.raises(PackError, match="absolute"):
        pack_project("leave-tracker", ["api"])
    with pytest.raises(PackError, match="not a directory"):
        pack_project(str(root / "nope"), ["api"])
    with pytest.raises(PackError, match="missing: worker"):
        pack_project(str(root), ["api", "worker"])


def test_refuses_an_archive_over_the_platform_limit(monkeypatch):
    monkeypatch.setattr(source_packer, "MAX_ARCHIVE_BYTES", 10)
    with pytest.raises(PackError, match="limit"):
        pack_project(str(_project()), ["api"])


async def test_deploy_application_packs_project_path_by_the_saved_services():
    client = FakePlatformClient()
    idem = IdempotencyStore(ttl_seconds=60)
    app = await client.register_application("leave", "desc", "dept-eng")
    await client.save_deployment_yaml(
        app["id"],
        "app:\n  name: leave\n  owner: IT\nservices:\n  frontend:\n    runtime: react\n  api:\n    runtime: python\n    port: 8080\n",
    )
    root = _project()

    result = await deployment.deploy_application(client, idem, app["id"], "dev", project_path=str(root))

    assert result["status"] == "success"
    build_calls = [c for c in client.calls if c[0] == "trigger_build"]
    assert len(build_calls) == 1
    names = _names(build_calls[0][1][1])
    assert "api/app.py" in names and "frontend/src/App.jsx" in names
    assert not any(n.startswith("notes/") or "node_modules" in n or ".env.local" in n for n in names)
    assert result["data"]["source"]["files"] == 5
    assert "api/.env.local" in result["data"]["source_note"]


async def test_deploy_application_rejects_both_kinds_of_source_and_unsaved_yaml():
    client = FakePlatformClient()
    idem = IdempotencyStore(ttl_seconds=60)
    app = await client.register_application("leave", "desc", "dept-eng")
    with pytest.raises(ToolError) as exc:
        await deployment.deploy_application(client, idem, app["id"], "dev", source_archive_base64="eA==", project_path="/x")
    assert exc.value.code == ErrorCode.VALIDATION_ERROR
    with pytest.raises(ToolError) as exc:
        await deployment.deploy_application(client, idem, app["id"], "dev", project_path=str(_project()))
    assert "validate_application" in exc.value.message
