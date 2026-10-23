"""The device authorization a machine runs to fetch its API key — shared
by the MCP `login`/`complete_login` tools and the `company-deploy login`
command, so both behave identically.

1. The machine asks the platform for a code (`start`).
2. The employee opens the Admin Portal, signs in with their own password,
   checks the code shown there matches the one on this machine, and
   approves it.
3. The machine collects its key (`poll_once`) and stores it
   (credentials.py). The key is never shown — not to the employee, not to
   the AI agent.
"""

from __future__ import annotations

import getpass
import socket
import time
from dataclasses import dataclass

from . import credentials
from .envelope import ErrorCode, ToolError
from .platform_client import PlatformClient


@dataclass
class PendingLogin:
    device_code: str
    user_code: str
    verification_uri: str
    verification_uri_complete: str
    interval: float
    deadline: float  # time.monotonic()

    def expired(self) -> bool:
        return time.monotonic() >= self.deadline


def machine_name() -> str:
    """What the key will be called in the employee's list of keys."""
    try:
        user = getpass.getuser()
    except Exception:  # pragma: no cover - no login name available
        user = "unknown user"
    return f"{socket.gethostname()} ({user}, Claude Code)"


async def start(client: PlatformClient, name: str | None = None) -> PendingLogin:
    data = await client.start_device_authorization(name or machine_name())
    return PendingLogin(
        device_code=data["device_code"],
        user_code=data["user_code"],
        verification_uri=data["verification_uri"],
        verification_uri_complete=data["verification_uri_complete"],
        interval=float(data.get("interval", 5)),
        deadline=time.monotonic() + float(data.get("expires_in", 600)),
    )


async def poll_once(client: PlatformClient, pending: PendingLogin) -> credentials.StoredCredential | None:
    """None while the employee hasn't decided. Raises ToolError if they
    denied it or it expired; stores and returns the credential once
    approved."""
    try:
        data = await client.poll_device_authorization(pending.device_code)
    except ToolError as exc:
        if exc.platform_code == "authorization_pending":
            return None
        if exc.platform_code == "access_denied":
            raise ToolError(ErrorCode.UNAUTHORIZED, "The employee denied this machine's sign-in request.") from exc
        if exc.platform_code == "expired_token":
            raise ToolError(
                ErrorCode.VALIDATION_ERROR,
                "This sign-in request expired or was already used. Start again with `login`.",
            ) from exc
        raise
    cred = credentials.StoredCredential(
        platform_url=client.base_url,
        api_key=data["api_key"],
        key_id=data["credential"]["id"],
        key_name=data["credential"]["name"],
        email=data["user"]["email"],
        expires_at=data["credential"]["expires_at"],
    )
    credentials.save(cred, client._credentials_path)  # noqa: SLF001 - same package
    return cred
