"""Verifies the client release check (release.py) against a real gateway.

In a throwaway user profile it:
1. installs the MCP server and skill with the gateway's own installer;
2. winds both back to an older release, as on a machine not updated since;
3. starts that MCP server over stdio: whoami reports the update, and a
   working tool is refused with UPDATE_REQUIRED and the update command;
4. runs that command, exactly as given, while the old server still runs;
5. the old server now asks only for a restart;
6. a restarted server is current and lets tools through.

Nothing outside the throwaway profile is touched; it is removed at the end.
The machine is never signed in, so a tool that gets past the check answers
UNAUTHORIZED. Windows runs install.ps1; macOS/Linux install.sh.

    PLATFORM_GATEWAY_URL=http://localhost:8098 python scripts/verify_client_update.py
"""

from __future__ import annotations

import asyncio
import json
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

import httpx
from mcp import ClientSession
from mcp.client.stdio import StdioServerParameters, stdio_client

GATEWAY = os.environ.get("PLATFORM_GATEWAY_URL", "http://localhost:8080").rstrip("/")
OLD = "old000000000"
WINDOWS = sys.platform == "win32"

failures: list[str] = []


def _check(ok: bool, what: str) -> None:
    print(("  PASS  " if ok else "  FAIL  ") + what)
    if not ok:
        failures.append(what)


def _profile_env(home: Path) -> dict[str, str]:
    env = {**os.environ, "HOME": str(home), "USERPROFILE": str(home),
           "APPDATA": str(home / "AppData" / "Roaming"), "LOCALAPPDATA": str(home / "AppData" / "Local")}
    env.pop("PLATFORM_API_BASE_URL", None)
    return env


def _run(cmd: str | list[str], env: dict[str, str], what: str) -> None:
    proc = subprocess.run(cmd, env=env, shell=isinstance(cmd, str), capture_output=True, text=True, timeout=900)
    _check(proc.returncode == 0, f"{what} (exit {proc.returncode})")
    if proc.returncode != 0:
        print(proc.stdout[-3000:], proc.stderr[-3000:])


def _venv_python(home: Path) -> Path:
    venv = home / ".company-deploy" / "venv"
    return venv / "Scripts" / "python.exe" if WINDOWS else venv / "bin" / "python"


async def _call(params: StdioServerParameters, *tools: str) -> list[dict]:
    async with stdio_client(params) as (read, write):
        async with ClientSession(read, write) as session:
            await session.initialize()
            out = []
            for tool in tools:
                result = await session.call_tool(tool, {})
                out.append(json.loads(result.content[0].text))
            return out


async def main() -> None:
    latest = httpx.get(f"{GATEWAY}/downloads/version.json", timeout=10).json()["client_version"]
    print(f"gateway {GATEWAY} publishes client release {latest}")
    home = Path(tempfile.mkdtemp(prefix="client-update-"))
    env = _profile_env(home)
    try:
        print("1. install into a throwaway profile")
        if WINDOWS:
            _run(["powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command",
                  f"& ([scriptblock]::Create((irm {GATEWAY}/downloads/install.ps1))) -SkipLogin"], env, "install.ps1")
        else:
            _run(f"curl -fsSL {GATEWAY}/downloads/install.sh | sh -s -- --skip-login", env, "install.sh")
        python = _venv_python(home)
        located = subprocess.run([str(python), "-c", "import mcp_server._release as r; print(r.__file__)"],
                                 capture_output=True, text=True, env=env)
        release_file = Path(located.stdout.strip())
        skill_file = home / ".claude" / "skills" / "company-deployment" / "VERSION"
        _check(f'RELEASE = "{latest}"' in release_file.read_text(), "the installed server carries the release")
        _check(skill_file.read_text().strip() == latest, "the installed skill carries the release")

        print("2. wind the machine back to an older release")
        release_file.write_text(f'RELEASE = "{OLD}"\n', encoding="utf-8")
        skill_file.write_text(OLD + "\n", encoding="utf-8")

        params = StdioServerParameters(command=str(python), args=["-m", "mcp_server.server"], env={
            **env, "PLATFORM_API_BASE_URL": f"{GATEWAY}/api",
            "COMPANY_DEPLOY_CREDENTIALS": str(home / "credentials.json"),
        })

        print("3-5. an old server: refused, updated by its own command, then asks for a restart")
        async with stdio_client(params) as (read, write):
            async with ClientSession(read, write) as session:
                await session.initialize()
                raw = (await session.call_tool("whoami", {})).content[0].text
                who = json.loads(raw)["data"]
                _check(who["update_required"] is True and who["client_release"] == OLD
                       and who["latest_release"] == latest, f"whoami reports the update: {who}")
                refused = json.loads((await session.call_tool("get_supported_stacks", {})).content[0].text)
                code = (refused.get("error") or {}).get("code")
                _check(code == "UPDATE_REQUIRED", f"a working tool is refused: {code}")
                message = refused["error"]["message"]
                command = next((line for line in message.splitlines() if "/downloads/install." in line), None)
                _check(command is not None, f"the refusal gives the update command: {command}")
                if command:
                    _run(command, env, "the update command, run as given, under the running server")
                again = json.loads((await session.call_tool("get_supported_stacks", {})).content[0].text)
                again_message = (again.get("error") or {}).get("message", "")
                _check((again.get("error") or {}).get("code") == "UPDATE_REQUIRED"
                       and "restart Claude Code" in again_message and "/downloads/install." not in again_message,
                       f"the still-running old server asks only for a restart: {again_message[:160]}")

        _check(f'RELEASE = "{latest}"' in release_file.read_text(), "the server on disk is the platform's release")
        _check(skill_file.read_text().strip() == latest, "the skill on disk is the platform's release")

        print("6. after the restart")
        who, stacks = await _call(params, "whoami", "get_supported_stacks")
        _check(who["data"]["update_required"] is False and who["data"]["client_release"] == latest
               and who["data"]["skill_release"] == latest, f"whoami: current {who['data']}")
        code = (stacks.get("error") or {}).get("code")
        _check(code != "UPDATE_REQUIRED", f"tools get past the check (unsigned machine: {code})")

        claude_config = home / ".claude.json"
        if shutil.which("claude"):
            registered = claude_config.exists() and "company-deployment" in claude_config.read_text(encoding="utf-8")
            _check(registered, "the installer registered the server in the throwaway profile only")
    finally:
        shutil.rmtree(home, ignore_errors=True)

    print()
    if failures:
        print(f"{len(failures)} check(s) FAILED")
        sys.exit(1)
    print("all checks passed")


if __name__ == "__main__":
    asyncio.run(main())
