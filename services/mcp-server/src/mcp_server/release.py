"""Is this machine's MCP server and skill the platform's current release?

The gateway image stamps one release, computed from the content of the MCP
server and the skill, into the wheel (`_release.py`), the skill (`VERSION`)
and `/downloads/version.json`. Before any work, the server compares its own
release and the installed skill's with the platform's. An older machine is
refused with UPDATE_REQUIRED, and the message tells Claude the one command
that updates it (the installer, without signing in again). Then Claude Code
restarts to load the new server and skill, and the work carries on.

A source checkout (no `_release.py`, e.g. a developer's editable install or
the verify scripts) is not a release and is never refused. Nor is a machine
whose platform publishes no version.json (an older gateway), or one that
can't fetch it: the platform calls that follow say what is wrong.
"""

from __future__ import annotations

import sys
import time
from dataclasses import dataclass
from pathlib import Path

from .envelope import ErrorCode, ToolError

try:
    from ._release import RELEASE as _BUILT_RELEASE
except ImportError:  # a source checkout, not an installed release
    _BUILT_RELEASE = None

# Where the installers put the skill.
SKILL_VERSION_FILE = Path.home() / ".claude" / "skills" / "company-deployment" / "VERSION"
_PACKAGE_DIR = Path(__file__).resolve().parent

# How long a "current" answer is trusted before asking the platform again.
_FRESH_SECONDS = 300.0


def running_release() -> str | None:
    """The release this server process was started from."""
    return _BUILT_RELEASE


def _read(path: Path, prefix: str = "") -> str | None:
    try:
        text = path.read_text(encoding="utf-8").strip()
    except OSError:
        return None
    if prefix:
        if not text.startswith(prefix):
            return None
        text = text[len(prefix):].strip().strip('"')
    return text or None


def installed_release() -> str | None:
    """The release installed on disk now: differs from running_release()
    once the installer has replaced the package under a running server."""
    return _read(_PACKAGE_DIR / "_release.py", "RELEASE =")


def skill_release() -> str | None:
    return _read(SKILL_VERSION_FILE)


def downloads_url(api_base_url: str) -> str | None:
    """The gateway's /downloads/ for an API reached through it at /api."""
    base = api_base_url.rstrip("/")
    if not base.endswith("/api"):
        return None  # the API's own port: no gateway, no downloads
    return base[: -len("/api")] + "/downloads"


def update_command(api_base_url: str) -> str:
    downloads = downloads_url(api_base_url) or "<platform>/downloads"
    if sys.platform == "win32":
        return (
            'powershell -NoProfile -ExecutionPolicy Bypass -Command '
            f'"& ([scriptblock]::Create((irm {downloads}/install.ps1))) -SkipLogin"'
        )
    return f"curl -fsSL {downloads}/install.sh | sh -s -- --skip-login"


@dataclass(frozen=True)
class Status:
    running: str | None
    installed: str | None
    skill: str | None
    latest: str | None  # None: the platform publishes none, or couldn't be reached

    @property
    def checked(self) -> bool:
        return self.running is not None and self.latest is not None

    @property
    def update_required(self) -> bool:
        return self.checked and (self.running != self.latest or self.skill != self.latest)

    @property
    def restart_only(self) -> bool:
        """Updated on disk, but this process still runs the old server."""
        return self.update_required and self.installed == self.latest and self.skill == self.latest

    def as_dict(self) -> dict[str, object]:
        return {
            "client_release": self.running,
            "skill_release": self.skill,
            "latest_release": self.latest,
            "update_required": self.update_required,
        }


class ReleaseCheck:
    """One per server process."""

    def __init__(self, client) -> None:
        self._client = client
        self._current_until = 0.0

    async def status(self) -> Status:
        running = running_release()
        latest = None
        if running is not None:
            url = downloads_url(self._client.base_url)
            if url is not None:
                latest = await self._client.get_client_release(url + "/version.json")
        return Status(running=running, installed=installed_release(), skill=skill_release(), latest=latest)

    def instructions(self, status: Status) -> str:
        restart = (
            "ask the employee to restart Claude Code (`claude --continue` keeps this conversation) "
            "so the new MCP server and skill load, and carry on with what they asked for."
        )
        if status.restart_only:
            return (
                f"This machine is updated to {status.latest}, but Claude Code is still running the old "
                f"MCP server ({status.running}): {restart}"
            )
        return (
            f"This machine's Company Deployment MCP server ({status.running}) or skill "
            f"({status.skill or 'not installed'}) is older than the platform's ({status.latest}). "
            "Update it before doing anything else: run this command, which installs the current server and "
            f"skill and keeps the machine's sign-in:\n{update_command(self._client.base_url)}\nThen {restart}"
        )

    async def require_current(self) -> None:
        if time.monotonic() < self._current_until:
            return
        status = await self.status()
        if status.update_required:
            raise ToolError(ErrorCode.UPDATE_REQUIRED, self.instructions(status))
        if status.checked:
            self._current_until = time.monotonic() + _FRESH_SECONDS
