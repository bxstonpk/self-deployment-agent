"""login, complete_login, whoami, logout — how a Claude Code machine
fetches the API key it needs before it can deploy (DEC-001/DEC-003
revised 2026-10-23).

None of these returns the key. `login` returns only the short code the
employee checks and approves in the Admin Portal; `complete_login` stores
the key in this machine's credentials file and reports who it acts for.
A secret never passes through the agent or its transcript, the same rule
the secret tools follow.
"""

from __future__ import annotations

import asyncio
import time
from typing import Any

from .. import credentials, login_flow
from ..envelope import ErrorCode, ToolError, success
from ..platform_client import PlatformClient
from ..release import ReleaseCheck

# One pending sign-in per server process: the server serves one Claude
# Code session on one machine.
_pending: login_flow.PendingLogin | None = None


def _signed_in(cred: credentials.StoredCredential, me: dict[str, Any]) -> dict[str, Any]:
    return {
        "signed_in": True,
        "email": me["user"]["email"],
        "key_name": me["credential"]["name"],
        "key_expires_at": me["credential"]["expires_at"],
        "platform": cred.platform_url,
    }


async def _current(client: PlatformClient) -> dict[str, Any] | None:
    cred = client.stored_credential()
    if cred is None:
        return None
    try:
        me = await client.whoami()
    except ToolError as exc:
        if exc.code == ErrorCode.UNAUTHORIZED:
            return None
        raise
    return _signed_in(cred, me)


async def login(client: PlatformClient) -> dict[str, Any]:
    global _pending
    current = await _current(client)
    if current is not None:
        return success({**current, "next_step": "Already signed in; nothing to do."})
    _pending = await login_flow.start(client)
    return success({
        "signed_in": False,
        "user_code": _pending.user_code,
        "verification_url": _pending.verification_uri_complete,
        "expires_in_seconds": int(_pending.deadline - time.monotonic()),
        "next_step": (
            f"Ask the employee to open {_pending.verification_uri_complete}, sign in with their own "
            f"password, check the code shown there is {_pending.user_code}, and approve it. Then call "
            "complete_login. Never ask the employee for their password yourself."
        ),
    })


async def complete_login(client: PlatformClient, wait_seconds: float = 60) -> dict[str, Any]:
    global _pending
    if _pending is None:
        current = await _current(client)
        if current is not None:
            return success(current)
        raise ToolError(ErrorCode.VALIDATION_ERROR, "No sign-in is in progress. Call `login` first.")
    deadline = time.monotonic() + max(0.0, min(wait_seconds, 300.0))
    while True:
        if _pending.expired():
            _pending = None
            raise ToolError(ErrorCode.VALIDATION_ERROR, "The sign-in request expired. Call `login` again.")
        try:
            cred = await login_flow.poll_once(client, _pending)
        except ToolError:
            _pending = None
            raise
        if cred is not None:
            _pending = None
            me = await client.whoami()
            return success({**_signed_in(cred, me), "credentials_file": str(client._credentials_path)})  # noqa: SLF001
        if time.monotonic() + _pending.interval > deadline:
            return success({
                "signed_in": False,
                "state": "waiting_for_approval",
                "user_code": _pending.user_code,
                "verification_url": _pending.verification_uri_complete,
                "next_step": "Not approved yet. Remind the employee, then call complete_login again.",
            })
        await asyncio.sleep(_pending.interval)


async def whoami(client: PlatformClient, releases: ReleaseCheck | None = None) -> dict[str, Any]:
    current = await _current(client)
    if current is None:
        data = {
            "signed_in": False,
            "platform": client.base_url,
            "next_step": "Call `login` before using any other tool.",
        }
    else:
        data = dict(current)
    if releases is not None:
        status = await releases.status()
        data.update(status.as_dict())
        if status.update_required:
            then = " After that, call `login`." if current is None else ""
            data["next_step"] = releases.instructions(status) + then
    return success(data)


async def logout(client: PlatformClient) -> dict[str, Any]:
    """Revokes this machine's key on the platform, then forgets it."""
    revoked = False
    if client.stored_credential() is not None:
        try:
            await client.revoke_own_key()
            revoked = True
        except ToolError as exc:
            if exc.code != ErrorCode.UNAUTHORIZED:
                raise
    removed = credentials.delete(client._credentials_path)  # noqa: SLF001
    return success({"signed_in": False, "key_revoked": revoked, "credentials_removed": removed})
