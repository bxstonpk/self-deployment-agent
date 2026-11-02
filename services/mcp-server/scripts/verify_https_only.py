"""Verifies closing HTTP (GATEWAY_HTTP_MODE=redirect, DEC-031) against a real
gateway that has a certificate and HTTP closed.

  1. HTTP: every address redirects to HTTPS; the installers and the company
     CA are still served, and an installer points the MCP server at HTTPS.
  2. A new machine (a throwaway user profile): the installer, fetched over
     HTTP, installs the CA; its MCP server, over real stdio, signs in and
     works over HTTPS, trusting the company CA.
  3. A machine from before: an older release pointed at the HTTP address,
     signed in there. Its first tool is refused with UPDATE_REQUIRED; the
     update command it is given (over HTTP) moves it to HTTPS; restarted,
     it is still signed in, as the same employee, and works.

Nothing outside the throwaway profiles is touched; they are removed at the
end. The real ~/.claude.json is never written.

    GATEWAY_HTTP=http://localhost:8098 GATEWAY_HTTPS=https://localhost:8443 \\
      python scripts/verify_https_only.py
"""

from __future__ import annotations

import asyncio
import json
import os
import shutil
import subprocess
import sys
import tempfile
import uuid
from pathlib import Path

import httpx
from mcp import ClientSession
from mcp.client.stdio import StdioServerParameters, stdio_client

sys.path.insert(0, str(Path(__file__).resolve().parents[2] / "platform-api" / "scripts"))
from platform_auth import api_key_for, call, session_for  # noqa: E402

HTTP = os.environ.get("GATEWAY_HTTP", "http://localhost:8098").rstrip("/")
HTTPS = os.environ.get("GATEWAY_HTTPS", "https://localhost:8443").rstrip("/")
API_DIRECT = os.environ.get("PLATFORM_API_BASE_URL", "http://localhost:8099")
WINDOWS = sys.platform == "win32"
failures: list[str] = []


def _check(ok: bool, what: str) -> None:
    print(("  PASS  " if ok else "  FAIL  ") + what, flush=True)
    if not ok:
        failures.append(what)


def _profile_env(home: Path) -> dict[str, str]:
    env = {**os.environ, "HOME": str(home), "USERPROFILE": str(home),
           "APPDATA": str(home / "AppData" / "Roaming"), "LOCALAPPDATA": str(home / "AppData" / "Local")}
    for k in ("PLATFORM_API_BASE_URL", "COMPANY_PLATFORM_CA", "COMPANY_DOWNLOADS_URL", "COMPANY_DEPLOY_CREDENTIALS"):
        env.pop(k, None)
    return env


def _install(env: dict[str, str], what: str) -> None:
    if WINDOWS:
        cmd: str | list[str] = ["powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command",
                                f"& ([scriptblock]::Create((irm {HTTP}/downloads/install.ps1))) -SkipLogin"]
    else:
        cmd = f"curl -fsSL {HTTP}/downloads/install.sh | sh -s -- --skip-login"
    _run(cmd, env, what)


def _run(cmd: str | list[str], env: dict[str, str], what: str) -> None:
    proc = subprocess.run(cmd, env=env, shell=isinstance(cmd, str), capture_output=True, text=True, timeout=900)
    _check(proc.returncode == 0, f"{what} (exit {proc.returncode})")
    if proc.returncode != 0:
        print(proc.stdout[-3000:], proc.stderr[-3000:])


def _python(home: Path) -> Path:
    venv = home / ".company-deploy" / "venv"
    return venv / "Scripts" / "python.exe" if WINDOWS else venv / "bin" / "python"


def _registered_env(home: Path) -> dict[str, str]:
    """The env the installer registered the MCP server with (claude's config)."""
    config = json.loads((home / ".claude.json").read_text(encoding="utf-8"))
    return config["mcpServers"]["company-deployment"]["env"]


def _server(home: Path, env: dict[str, str], api: str, extra: dict[str, str] | None = None) -> StdioServerParameters:
    return StdioServerParameters(command=str(_python(home)), args=["-m", "mcp_server.server"],
                                 env={**env, "PLATFORM_API_BASE_URL": api, **(extra or {})})


async def _tool(session: ClientSession, name: str, args: dict | None = None) -> dict:
    return json.loads((await session.call_tool(name, args or {})).content[0].text)


async def _sign_in(session: ClientSession, employee_session: str) -> None:
    """The device flow, approved by the employee's own Portal session."""
    started = await _tool(session, "login")
    code = started["data"]["user_code"]
    status, body = call(API_DIRECT, "POST", f"/auth/device/{code}/approve", token=employee_session)
    _check(status == 200, f"the employee approves the code ({status} {body if status != 200 else ''})")
    done = await _tool(session, "complete_login", {"wait_seconds": 30})
    _check(done["status"] == "success" and done["data"].get("signed_in"), f"complete_login over HTTPS: {done['data'] or done['error']}")


async def main() -> None:
    print(f"HTTP {HTTP}, HTTPS {HTTPS}")
    print("1. HTTP is closed but for the installers and the CA")
    r = httpx.get(f"{HTTP}/api/healthz", follow_redirects=False)
    _check(r.status_code == 302 and r.headers.get("location") == f"{HTTPS}/api/healthz",
           f"HTTP /api/healthz redirects to HTTPS ({r.status_code} -> {r.headers.get('location')})")
    r = httpx.get(f"{HTTP}/applications", follow_redirects=False)
    _check(r.status_code == 302, f"HTTP Portal pages redirect ({r.status_code})")
    ca = httpx.get(f"{HTTP}/downloads/company-platform-ca.crt")
    _check(ca.status_code == 200 and b"BEGIN CERTIFICATE" in ca.content, "the company CA is served over HTTP")
    script = httpx.get(f"{HTTP}/downloads/install.ps1").text
    _check(f'$PlatformUrl = "{HTTPS}"' in script and f'$DownloadsUrl = "{HTTP}/downloads"' in script,
           "an installer fetched over HTTP points the MCP at HTTPS and downloads over HTTP")
    tmp_ca = Path(tempfile.mkdtemp(prefix="https-only-ca-")) / "ca.crt"
    tmp_ca.write_bytes(ca.content)
    r = httpx.get(f"{HTTPS}/api/healthz", verify=str(tmp_ca))
    _check(r.status_code == 200, f"HTTPS works, verified against that CA ({r.status_code})")
    shutil.rmtree(tmp_ca.parent, ignore_errors=True)

    suffix = uuid.uuid4().hex[:6]
    email = f"https-only-{suffix}@sti-th.com"
    employee = session_for(API_DIRECT, email)

    print("2. A new machine")
    home = Path(tempfile.mkdtemp(prefix="https-new-"))
    env = _profile_env(home)
    try:
        _install(env, "the installer, fetched over HTTP")
        reg = _registered_env(home)
        _check(reg.get("PLATFORM_API_BASE_URL") == f"{HTTPS}/api" and reg.get("COMPANY_DOWNLOADS_URL") == f"{HTTP}/downloads",
               f"the MCP server is registered with HTTPS and downloads over HTTP ({reg})")
        _check((home / ".company-deploy" / "company-platform-ca.crt").is_file(), "the company CA is installed next to the key")
        params = _server(home, env, reg["PLATFORM_API_BASE_URL"], {"COMPANY_DOWNLOADS_URL": reg["COMPANY_DOWNLOADS_URL"]})
        async with stdio_client(params) as (read, write):
            async with ClientSession(read, write) as session:
                await session.initialize()
                who = await _tool(session, "whoami")
                _check(who["status"] == "success" and who["data"]["signed_in"] is False and not who["data"].get("update_required"),
                       f"whoami over HTTPS (not signed in yet): {who['data'] or who['error']}")
                await _sign_in(session, employee)
                stacks = await _tool(session, "get_supported_stacks")
                _check(stacks["status"] == "success", f"a tool works over HTTPS ({stacks['status']} {stacks.get('error')})")
    finally:
        shutil.rmtree(home, ignore_errors=True)

    print("3. A machine from before, on the HTTP address")
    home = Path(tempfile.mkdtemp(prefix="https-old-"))
    env = _profile_env(home)
    try:
        _install(env, "set up")
        python = _python(home)
        release_file = Path(subprocess.run([str(python), "-c", "import mcp_server._release as r; print(r.__file__)"],
                                           capture_output=True, text=True, env=env).stdout.strip())
        latest = httpx.get(f"{HTTP}/downloads/version.json").json()["client_version"]
        # As it was before: an older release, registered with the HTTP
        # address, signed in there, with no record of where it came from.
        release_file.write_text('RELEASE = "old000000000"\n', encoding="utf-8")
        (home / ".claude" / "skills" / "company-deployment" / "VERSION").write_text("old000000000\n", encoding="utf-8")
        (home / ".company-deploy" / "company-platform-ca.crt").unlink()
        old_api = f"{HTTP}/api"
        key = api_key_for(API_DIRECT, email, "an HTTP-era machine")
        (home / ".company-deploy" / "credentials.json").write_text(json.dumps({
            "platform_url": old_api, "api_key": key, "key_id": "", "key_name": "an HTTP-era machine",
            "email": email, "expires_at": "",
        }), encoding="utf-8")
        async with stdio_client(_server(home, env, old_api)) as (read, write):
            async with ClientSession(read, write) as session:
                await session.initialize()
                who = await _tool(session, "whoami")
                _check(who["status"] == "success" and who["data"].get("update_required") is True,
                       f"whoami asks for the update first: {str(who['data'])[:200]}")
                refused = await _tool(session, "get_supported_stacks")
                message = (refused.get("error") or {}).get("message", "")
                _check((refused.get("error") or {}).get("code") == "UPDATE_REQUIRED", "a tool is refused with UPDATE_REQUIRED")
                command = next((l for l in message.splitlines() if "/downloads/install." in l), "")
                _check(f"{HTTP}/downloads/install." in command, f"the update command downloads over HTTP: {command}")
                if command:
                    _run(command, env, "the update command, run as given")
        reg = _registered_env(home)
        _check(reg.get("PLATFORM_API_BASE_URL") == f"{HTTPS}/api", f"the machine now points at HTTPS ({reg.get('PLATFORM_API_BASE_URL')})")
        _check(f'RELEASE = "{latest}"' in release_file.read_text(), "the server on disk is the platform's release")
        params = _server(home, env, reg["PLATFORM_API_BASE_URL"], {"COMPANY_DOWNLOADS_URL": reg["COMPANY_DOWNLOADS_URL"]})
        async with stdio_client(params) as (read, write):
            async with ClientSession(read, write) as session:
                await session.initialize()
                who = await _tool(session, "whoami")
                _check(who["status"] == "success" and who["data"].get("signed_in") is True and who["data"].get("email") == email,
                       f"restarted: still signed in, as the same employee, over HTTPS ({str(who['data'])[:200]})")
                stacks = await _tool(session, "get_supported_stacks")
                _check(stacks["status"] == "success", "and its tools work")
    finally:
        shutil.rmtree(home, ignore_errors=True)

    print()
    if failures:
        print(f"{len(failures)} check(s) FAILED")
        sys.exit(1)
    print("all checks passed")


if __name__ == "__main__":
    asyncio.run(main())
