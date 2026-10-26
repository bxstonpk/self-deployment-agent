"""The client release check (release.py): an MCP server or skill older than
the platform's release is refused until the machine is updated."""

from __future__ import annotations

import tempfile
from pathlib import Path

import httpx
import pytest

from mcp_server import release
from mcp_server.audit import AuditEvent
from mcp_server.config import Config
from mcp_server.envelope import ErrorCode, success
from mcp_server.platform_client import PlatformClient
from mcp_server.server import _UNGATED_TOOLS, _run_tool
from mcp_server.tools import auth


class FakeClient:
    """Only what ReleaseCheck uses."""

    def __init__(self, latest: str | None, base_url: str = "http://platform.test/api") -> None:
        self.latest = latest
        self.base_url = base_url
        self.fetched: list[str] = []

    async def get_client_release(self, url: str) -> str | None:
        self.fetched.append(url)
        return self.latest


@pytest.fixture
def machine(monkeypatch, tmp_path):
    """This machine: the release its server runs, on disk, and its skill's."""

    def set_up(running: str | None, installed: str | None = "same", skill: str | None = "same") -> None:
        installed = running if installed == "same" else installed
        skill = running if skill == "same" else skill
        monkeypatch.setattr(release, "_BUILT_RELEASE", running)
        monkeypatch.setattr(release, "installed_release", lambda: installed)
        version_file = tmp_path / "VERSION"
        if skill is not None:
            version_file.write_text(skill + "\n", encoding="utf-8")
        monkeypatch.setattr(release, "SKILL_VERSION_FILE", version_file)

    return set_up


def _event(tool: str) -> AuditEvent:
    return AuditEvent(tool_name=tool, employee_identity="t", input_parameters={}, target_application_id=None)


async def _ok() -> dict:
    return success({"done": True})


def test_downloads_url_only_through_the_gateway():
    assert release.downloads_url("http://host:8080/api") == "http://host:8080/downloads"
    assert release.downloads_url("http://host/api/") == "http://host/downloads"
    assert release.downloads_url("http://localhost:8099") is None


def test_update_command_skips_sign_in(monkeypatch):
    monkeypatch.setattr(release.sys, "platform", "win32")
    cmd = release.update_command("http://host/api")
    assert "http://host/downloads/install.ps1" in cmd and "-SkipLogin" in cmd
    monkeypatch.setattr(release.sys, "platform", "linux")
    cmd = release.update_command("http://host/api")
    assert cmd == "curl -fsSL http://host/downloads/install.sh | sh -s -- --skip-login"


async def test_current_machine_is_not_refused(machine):
    machine("abc123")
    client = FakeClient("abc123")
    result = await _run_tool(_event("deploy_application"), _ok(), release.ReleaseCheck(client).require_current)
    assert result["status"] == "success"


async def test_older_server_is_refused_with_the_update_command(machine):
    machine("old111")
    client = FakeClient("new222")
    checks = release.ReleaseCheck(client)
    call = _ok()
    result = await _run_tool(_event("deploy_application"), call, checks.require_current)
    assert result["status"] == "error"
    assert result["error"]["code"] == ErrorCode.UPDATE_REQUIRED.value
    message = result["error"]["message"]
    assert "old111" in message and "new222" in message
    assert "/downloads/install" in message
    assert "claude --continue" in message
    assert call.cr_frame is None  # closed, never run


async def test_older_skill_is_refused(machine):
    machine("new222", skill="old111")
    checks = release.ReleaseCheck(FakeClient("new222"))
    result = await _run_tool(_event("start_project"), _ok(), checks.require_current)
    assert result["error"]["code"] == "UPDATE_REQUIRED"


async def test_missing_skill_is_refused(machine):
    machine("new222", skill=None)
    checks = release.ReleaseCheck(FakeClient("new222"))
    result = await _run_tool(_event("start_project"), _ok(), checks.require_current)
    assert result["error"]["code"] == "UPDATE_REQUIRED"
    assert "not installed" in result["error"]["message"]


async def test_updated_on_disk_asks_only_for_a_restart(machine):
    machine("old111", installed="new222", skill="new222")
    checks = release.ReleaseCheck(FakeClient("new222"))
    status = await checks.status()
    assert status.restart_only
    message = checks.instructions(status)
    assert "restart Claude Code" in message
    assert "install.ps1" not in message and "install.sh" not in message


async def test_source_checkout_is_never_refused(machine):
    machine(None)
    client = FakeClient("new222")
    result = await _run_tool(_event("deploy_application"), _ok(), release.ReleaseCheck(client).require_current)
    assert result["status"] == "success"
    assert client.fetched == []


async def test_platform_without_a_release_is_not_refused(machine):
    machine("old111")
    result = await _run_tool(_event("deploy_application"), _ok(), release.ReleaseCheck(FakeClient(None)).require_current)
    assert result["status"] == "success"


async def test_a_current_answer_is_trusted_for_a_while(machine):
    machine("abc123")
    client = FakeClient("abc123")
    checks = release.ReleaseCheck(client)
    await checks.require_current()
    await checks.require_current()
    assert len(client.fetched) == 1


async def test_a_refusal_asks_again_every_time(machine):
    machine("old111")
    client = FakeClient("new222")
    checks = release.ReleaseCheck(client)
    for _ in range(2):
        result = await _run_tool(_event("deploy_application"), _ok(), checks.require_current)
        assert result["error"]["code"] == "UPDATE_REQUIRED"
    assert len(client.fetched) == 2


def test_sign_in_and_whoami_are_never_gated():
    assert _UNGATED_TOOLS == {"whoami", "login", "complete_login", "logout"}


async def test_whoami_says_to_update_first(machine):
    machine("old111")

    class SignedOut(FakeClient):
        def stored_credential(self):
            return None

    client = SignedOut("new222")
    result = await auth.whoami(client, release.ReleaseCheck(client))
    data = result["data"]
    assert data["update_required"] is True
    assert data["client_release"] == "old111" and data["latest_release"] == "new222"
    assert "install" in data["next_step"] and data["next_step"].endswith("call `login`.")


async def test_platform_client_reads_version_json():
    def handler(request: httpx.Request) -> httpx.Response:
        if request.url.path == "/downloads/version.json":
            return httpx.Response(200, json={"client_version": "abc123"})
        return httpx.Response(404)

    with tempfile.TemporaryDirectory() as d:
        config = Config(platform_api_base_url="http://gw.test/api", platform_api_timeout_seconds=5,
                        idempotency_ttl_seconds=60, credentials_path=Path(d) / "c.json")
        client = PlatformClient(config, http_client=httpx.AsyncClient(
            base_url="http://gw.test/api", transport=httpx.MockTransport(handler)))
        assert await client.get_client_release("http://gw.test/downloads/version.json") == "abc123"
        assert await client.get_client_release("http://gw.test/downloads/missing.json") is None


class ServedClient(FakeClient):
    """Enough of a platform client to serve tools through build_server."""

    def stored_credential(self):
        return None

    async def get_supported_stacks(self):
        return [{"id": "stack-go", "category": "backend", "runtime": "go", "status": "active"}]


async def _served(server, tool: str) -> dict:
    import json

    result = await server.call_tool(tool, {})
    return json.loads(result.content[0].text)


async def test_the_served_tools_go_through_the_check(machine):
    from mcp_server.idempotency import IdempotencyStore
    from mcp_server.server import build_server

    machine("old111")
    server = build_server(ServedClient("new222"), IdempotencyStore(60))
    refused = await _served(server, "get_supported_stacks")
    assert refused["error"]["code"] == "UPDATE_REQUIRED"
    who = await _served(server, "whoami")
    assert who["status"] == "success" and who["data"]["update_required"] is True

    machine("new222")
    current = build_server(ServedClient("new222"), IdempotencyStore(60))
    stacks = await _served(current, "get_supported_stacks")
    assert stacks["status"] == "success"
