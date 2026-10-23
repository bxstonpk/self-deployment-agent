"""`company-deploy` — sign this machine in to the Company Platform from a
terminal, without going through an AI agent.

    company-deploy login [--url http://platform-host:8090]
    company-deploy whoami
    company-deploy logout

`login` shows a code and a link; the employee opens the link, signs in to
the Admin Portal with their own password, checks the code matches and
approves it. The key this machine receives is stored in
~/.company-deploy/credentials.json (see credentials.py) and is never
printed. The MCP server reads the same file, so after this Claude Code is
signed in too.
"""

from __future__ import annotations

import argparse
import asyncio
import os
import sys

from . import credentials, login_flow
from .config import Config
from .envelope import ToolError
from .platform_client import PlatformClient


def _platform_url(arg: str | None) -> str:
    url = (arg or os.environ.get("PLATFORM_API_BASE_URL", "")).strip()
    if not url:
        stored = credentials.load()
        url = stored.platform_url if stored else ""
    if not url:
        raise SystemExit("company-deploy: give the platform's address with --url (e.g. --url http://platform-host:8090)")
    return url.rstrip("/")


def _client(url: str) -> PlatformClient:
    return PlatformClient(Config(platform_api_base_url=url, platform_api_timeout_seconds=30, idempotency_ttl_seconds=60))


async def _whoami(client: PlatformClient) -> int:
    if client.stored_credential() is None:
        print(f"Not signed in to {client.base_url}. Run: company-deploy login")
        return 1
    try:
        me = await client.whoami()
    except ToolError as exc:
        print(exc.message)
        return 1
    print(f"Signed in to {client.base_url} as {me['user']['email']}")
    print(f"Key: {me['credential']['name']} ({me['credential']['prefix']}…), expires {me['credential']['expires_at']}")
    return 0


async def _login(client: PlatformClient, name: str | None) -> int:
    if client.stored_credential() is not None:
        try:
            me = await client.whoami()
            print(f"Already signed in as {me['user']['email']}. Run `company-deploy logout` first to switch.")
            return 0
        except ToolError:
            pass  # expired or revoked: sign in again
    pending = await login_flow.start(client, name)
    print("To sign this machine in to the Company Platform:")
    print(f"  1. Open {pending.verification_uri_complete}")
    print("  2. Sign in with your own email and password")
    print(f"  3. Check the code shown is  {pending.user_code}  and approve it")
    print("Waiting for approval (Ctrl+C to cancel)...", flush=True)
    while not pending.expired():
        await asyncio.sleep(pending.interval)
        try:
            cred = await login_flow.poll_once(client, pending)
        except ToolError as exc:
            print(exc.message)
            return 1
        if cred is not None:
            print(f"Signed in as {cred.email}. Key {cred.key_name!r} expires {cred.expires_at}.")
            print(f"Stored in {credentials.default_path()}")
            return 0
    print("The request expired before it was approved. Run `company-deploy login` again.")
    return 1


async def _logout(client: PlatformClient) -> int:
    if client.stored_credential() is not None:
        try:
            await client.revoke_own_key()
            print("This machine's key was revoked on the platform.")
        except ToolError as exc:
            print(f"Could not revoke the key on the platform ({exc.message}); removing it locally anyway.")
    removed = credentials.delete()
    print("Signed out." if removed else "This machine was not signed in.")
    return 0


def main(argv: list[str] | None = None) -> None:
    parser = argparse.ArgumentParser(prog="company-deploy", description=__doc__.split("\n\n")[0])
    sub = parser.add_subparsers(dest="command", required=True)
    login = sub.add_parser("login", help="sign this machine in (an employee approves it in the Admin Portal)")
    login.add_argument("--url", help="the platform's address, e.g. http://platform-host:8090")
    login.add_argument("--name", help="what to call this machine's key (default: hostname and user)")
    for cmd, text in (("whoami", "show who this machine is signed in as"), ("logout", "revoke and forget this machine's key")):
        p = sub.add_parser(cmd, help=text)
        p.add_argument("--url", help=argparse.SUPPRESS)
    args = parser.parse_args(argv)

    if args.command != "login" and not (args.url or os.environ.get("PLATFORM_API_BASE_URL") or credentials.load()):
        print("This machine is not signed in. Run: company-deploy login --url <platform address>")
        sys.exit(1)
    client = _client(_platform_url(args.url))

    async def run() -> int:
        try:
            if args.command == "login":
                return await _login(client, args.name)
            if args.command == "whoami":
                return await _whoami(client)
            return await _logout(client)
        finally:
            await client.aclose()

    try:
        sys.exit(asyncio.run(run()))
    except KeyboardInterrupt:
        sys.exit(130)


if __name__ == "__main__":
    main()
