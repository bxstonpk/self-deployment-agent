"""`company-deploy` — sign this machine in to the Company Platform from a
terminal, without going through an AI agent.

    company-deploy login [--url http://platform-host:8090]
    company-deploy whoami
    company-deploy logout

and, for a platform administrator who approves deployments (see approver.py):

    company-deploy approver keygen
    company-deploy approvals
    company-deploy approve <deployment-id> [--reject] [--reason TEXT]

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

import getpass
import textwrap
from pathlib import Path

from . import approver, credentials, login_flow
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


def _passphrase(confirm: bool = False) -> bytes:
    """From COMPANY_DEPLOY_APPROVER_PASSPHRASE when set (for automation),
    otherwise asked for without echo."""
    env = os.environ.get(approver.PASSPHRASE_ENV)
    if env:
        return env.encode()
    first = getpass.getpass("Approver key passphrase: ")
    if confirm and getpass.getpass("Again: ") != first:
        raise SystemExit("The passphrases didn't match.")
    return first.encode()


def _keygen(path: Path) -> int:
    """Offline: the email comes from this machine's sign-in, if any."""
    stored = credentials.load()
    email = stored.email if stored else "<your email>"
    try:
        key = approver.generate(path, _passphrase(confirm=True))
    except (FileExistsError, ValueError) as exc:
        print(exc)
        return 1
    print(f"Approver key written to {path} (encrypted with your passphrase, readable only by you).")
    print(f"Fingerprint: {approver.fingerprint(key)}")
    print("\nAdd this to the platform host's .env and restart platform-api:\n")
    print(f"  PLATFORM_APPROVER_KEYS={approver.public_key_line(email, key)}\n")
    print("Keep an offline copy of the key file. It is the only way to approve deployments.")
    return 0


async def _approvals(client: PlatformClient) -> int:
    try:
        pending = await client.pending_approvals()
    except ToolError as exc:
        print(exc.message)
        return 1
    if not pending:
        print("Nothing is waiting for approval.")
        return 0
    for p in pending:
        print(f"{p['deployment_id']}  {p['application_name']}  {p['environment']}  "
              f"requested by {p['requested_by']} at {p['requested_at']}")
    return 0


def _show(req: dict) -> None:
    print(f"Application:  {req['application_name']} ({req['application_id']})")
    print(f"Environment:  {req['environment']}")
    print(f"Requested by: {req['requested_by']} at {req['requested_at']}")
    print(f"Build:        {req['build_id']}")
    for svc, ref in sorted(req["images"].items()):
        scan = req["scans"].get(svc, {})
        print(f"  {svc:<12} {ref}  scan: {'passed' if scan.get('passed') else 'NOT passed'}, "
              f"{scan.get('critical', '?')} critical, {scan.get('high', '?')} high")
    print("Test report:" if req["test_report"] else "Test report:  (none attached)")
    if req["test_report"]:
        print(textwrap.indent(req["test_report"], "  | "))


async def _approve(client: PlatformClient, deployment_id: str, reject: bool, reason: str, yes: bool, key_path: Path) -> int:
    try:
        req = await client.approval_request(deployment_id)
    except ToolError as exc:
        print(exc.message)
        return 1
    if req["status"] != "pending_approval":
        print(f"Deployment {deployment_id} is {req['status']}, not waiting for approval.")
        return 1
    _show(req)
    decision = "reject" if reject else "approve"
    if not yes:
        answer = input(f"\n{decision.capitalize()} this deployment? Type '{decision}' to sign: ").strip().lower()
        if answer != decision:
            print("Nothing was signed.")
            return 1
    try:
        key = approver.load(key_path, _passphrase())
    except (OSError, ValueError, TypeError) as exc:
        print(f"Could not open the approver key {key_path}: {exc}")
        return 1
    signed_at, signature = approver.sign(key, req["signing_template"], decision, reason)
    try:
        out = await client.decide_approval(deployment_id, decision, reason, signed_at, signature)
    except ToolError as exc:
        print(exc.message)
        return 1
    print(f"Signed with {out['signer_fingerprint']}: deployment {deployment_id} is now {out['deployment']['status']}.")
    return 0


def main(argv: list[str] | None = None) -> None:
    parser = argparse.ArgumentParser(prog="company-deploy", description=__doc__.split("\n\n")[0])
    sub = parser.add_subparsers(dest="command", required=True)
    login = sub.add_parser("login", help="sign this machine in (an employee approves it in the Admin Portal)")
    login.add_argument("--url", help="the platform's address, e.g. http://platform-host:8090")
    login.add_argument("--name", help="what to call this machine's key (default: hostname and user)")
    for cmd, text in (("whoami", "show who this machine is signed in as"), ("logout", "revoke and forget this machine's key"),
                      ("approvals", "list deployments waiting for your signed approval")):
        p = sub.add_parser(cmd, help=text)
        p.add_argument("--url", help=argparse.SUPPRESS)
    keygen = sub.add_parser("approver", help="manage your approver key").add_subparsers(dest="approver_command", required=True)
    kg = keygen.add_parser("keygen", help="create your approver key and print the public half for the platform")
    kg.add_argument("--key", type=Path, default=approver.default_key_path())
    kg.add_argument("--url", help=argparse.SUPPRESS)
    ap = sub.add_parser("approve", help="review a deployment and sign your decision")
    ap.add_argument("deployment_id")
    ap.add_argument("--reject", action="store_true", help="sign a rejection instead")
    ap.add_argument("--reason", default="", help="why (shown to the requester; part of what you sign)")
    ap.add_argument("--yes", action="store_true", help="don't ask for confirmation")
    ap.add_argument("--key", type=Path, default=approver.default_key_path())
    ap.add_argument("--url", help=argparse.SUPPRESS)
    args = parser.parse_args(argv)
    if args.command == "approver":
        sys.exit(_keygen(args.key))

    if args.command not in ("login", "approver") and not (args.url or os.environ.get("PLATFORM_API_BASE_URL") or credentials.load()):
        print("This machine is not signed in. Run: company-deploy login --url <platform address>")
        sys.exit(1)
    client = _client(_platform_url(args.url))

    async def run() -> int:
        try:
            if args.command == "login":
                return await _login(client, args.name)
            if args.command == "whoami":
                return await _whoami(client)
            if args.command == "approvals":
                return await _approvals(client)
            if args.command == "approve":
                return await _approve(client, args.deployment_id, args.reject, args.reason, args.yes, args.key)
            return await _logout(client)
        finally:
            await client.aclose()

    try:
        sys.exit(asyncio.run(run()))
    except KeyboardInterrupt:
        sys.exit(130)


if __name__ == "__main__":
    main()
