"""login / complete_login / whoami / logout against a scripted Platform API
that speaks the real device-authorization contract."""

import json
import tempfile
from pathlib import Path

import httpx
import pytest

from mcp_server import credentials
from mcp_server.config import Config
from mcp_server.envelope import ErrorCode, ToolError
from mcp_server.platform_client import PlatformClient
from mcp_server.tools import auth

BASE = "http://platform-api.test"
KEY = "cpk_the-secret-key-value"


class FakePlatform:
    def __init__(self) -> None:
        self.state = "pending"  # pending | approved | denied | consumed
        self.revoked = False
        self.client_names: list[str] = []

    def handler(self, request: httpx.Request) -> httpx.Response:
        path = request.url.path
        if path == "/auth/device":
            self.client_names.append(json.loads(request.content)["client_name"])
            return httpx.Response(201, json={
                "device_code": "cpd_device", "user_code": "BCDF-GHJK",
                "verification_uri": "http://portal.test/device",
                "verification_uri_complete": "http://portal.test/device?code=BCDF-GHJK",
                "expires_in": 600, "interval": 0,
            })
        if path == "/auth/device/token":
            if self.state == "pending":
                return httpx.Response(400, json={"error": {"code": "authorization_pending", "message": "wait"}})
            if self.state == "denied":
                return httpx.Response(400, json={"error": {"code": "access_denied", "message": "no"}})
            if self.state == "consumed":
                return httpx.Response(400, json={"error": {"code": "expired_token", "message": "used"}})
            self.state = "consumed"
            return httpx.Response(200, json={
                "api_key": KEY,
                "credential": {"id": "k1", "name": "laptop (alice, Claude Code)", "prefix": KEY[:12],
                               "expires_at": "2026-11-22T00:00:00Z", "kind": "api_key"},
                "user": {"id": "u1", "email": "alice@sti-th.com"},
            })
        auth_header = request.headers.get("authorization", "")
        if auth_header != f"Bearer {KEY}" or self.revoked:
            return httpx.Response(401, json={"error": {"code": "unauthorized", "message": "bad credential"}})
        if path == "/auth/me":
            return httpx.Response(200, json={
                "user": {"id": "u1", "email": "alice@sti-th.com"},
                "credential": {"id": "k1", "name": "laptop (alice, Claude Code)", "prefix": KEY[:12],
                               "expires_at": "2026-11-22T00:00:00Z", "kind": "api_key"},
            })
        if path == "/auth/logout":
            self.revoked = True
            return httpx.Response(204)
        return httpx.Response(404, json={"error": {"code": "not_found", "message": path}})


@pytest.fixture
def setup():
    auth._pending = None
    platform = FakePlatform()
    path = Path(tempfile.mkdtemp()) / "credentials.json"
    config = Config(platform_api_base_url=BASE, platform_api_timeout_seconds=5,
                    idempotency_ttl_seconds=60, credentials_path=path)
    http = httpx.AsyncClient(base_url=BASE, transport=httpx.MockTransport(platform.handler))
    yield platform, PlatformClient(config, http_client=http), path
    auth._pending = None


async def test_login_shows_only_a_code_and_url_then_stores_the_key_on_approval(setup):
    platform, client, path = setup

    started = await auth.login(client)
    assert started["status"] == "success"
    data = started["data"]
    assert data["signed_in"] is False and data["user_code"] == "BCDF-GHJK"
    assert data["verification_url"].endswith("code=BCDF-GHJK")
    assert "password yourself" in data["next_step"]
    assert "Claude Code" in platform.client_names[0]

    waiting = await auth.complete_login(client, wait_seconds=0)
    assert waiting["data"]["state"] == "waiting_for_approval"
    assert not path.exists()

    platform.state = "approved"
    done = await auth.complete_login(client, wait_seconds=0)
    assert done["data"]["signed_in"] is True and done["data"]["email"] == "alice@sti-th.com"
    # The key itself is never handed to the agent.
    assert KEY not in json.dumps(started) + json.dumps(waiting) + json.dumps(done)
    assert credentials.load(path).api_key == KEY

    me = await auth.whoami(client)
    assert me["data"]["signed_in"] is True and me["data"]["key_name"].startswith("laptop")
    again = await auth.login(client)
    assert again["data"]["signed_in"] is True and len(platform.client_names) == 1


async def test_a_denied_login_stores_nothing(setup):
    platform, client, path = setup
    await auth.login(client)
    platform.state = "denied"
    with pytest.raises(ToolError) as exc:
        await auth.complete_login(client, wait_seconds=0)
    assert exc.value.code == ErrorCode.UNAUTHORIZED
    assert not path.exists()
    with pytest.raises(ToolError):
        await auth.complete_login(client, wait_seconds=0)  # nothing in progress any more


async def test_logout_revokes_on_the_platform_and_forgets_the_key(setup):
    platform, client, path = setup
    await auth.login(client)
    platform.state = "approved"
    await auth.complete_login(client, wait_seconds=0)

    out = await auth.logout(client)
    assert out["data"] == {"signed_in": False, "key_revoked": True, "credentials_removed": True}
    assert platform.revoked and not path.exists()
    me = await auth.whoami(client)
    assert me["data"]["signed_in"] is False


async def test_a_revoked_key_reads_as_signed_out(setup):
    platform, client, path = setup
    await auth.login(client)
    platform.state = "approved"
    await auth.complete_login(client, wait_seconds=0)
    platform.revoked = True  # e.g. the employee revoked it in the Portal
    me = await auth.whoami(client)
    assert me["data"]["signed_in"] is False
