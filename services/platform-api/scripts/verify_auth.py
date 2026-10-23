"""End-to-end verification of authentication (DEC-001/DEC-003 revised
2026-10-23) against a live platform-api in PLATFORM_AUTH_MODE=token.

    docker compose up -d --build      # from repo root, with .env present
    python services/platform-api/scripts/verify_auth.py

Nothing is mocked. It checks, over real HTTP and against the real
database:

  - nothing but /healthz and the application proxy answers without a
    credential, and the old self-declared X-Dev-* headers no longer work
  - registering needs the enrollment code; sign-in gives the same answer
    for an unknown email and a wrong password, and locks out after
    repeated failures
  - a machine gets its key only through a device authorization: pending
    until the employee approves it with their own session, collected
    exactly once, named after the machine
  - that key deploys: it registers and validates an application, which
    is then owned by the employee who approved it
  - a key can do nothing to credentials: it can't list, issue or revoke
    keys, or approve another device
  - a revoked key and a signed-out session stop working at once
  - every one of those events is in the employee's own audit log
  - the platform database holds no token and no password in a usable form
    — checked with a full pg_dump

Set PLATFORM_API_BASE_URL if the API is not on the .env default port.
"""

from __future__ import annotations

import os
import subprocess
import sys
import uuid

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from platform_auth import PASSWORD, call, enrollment_code  # noqa: E402

BASE_URL = os.environ.get("PLATFORM_API_BASE_URL", "http://localhost:8099")
_failures: list[str] = []


def _check(cond: bool, msg: str) -> bool:
    print(("OK:   " if cond else "FAIL: ") + msg, flush=True)
    if not cond:
        _failures.append(msg)
    return cond


def api(method, path, body=None, token=None):
    return call(BASE_URL, method, path, body, token)


def main() -> int:
    suffix = uuid.uuid4().hex[:6]
    alice = f"alice-{suffix}@sti-th.com"
    print(f"=== authentication verification against {BASE_URL} ===\n", flush=True)

    print("--- nothing without a credential ---")
    for method, path in [("GET", "/applications"), ("GET", "/supported-stacks"), ("GET", "/audit-log"),
                         ("POST", "/applications/x/deploy"), ("GET", "/auth/me")]:
        status, _ = api(method, path)
        _check(status == 401, f"{method} {path} without a credential -> {status}")
    import urllib.request
    req = urllib.request.Request(BASE_URL + "/applications", headers={"X-Dev-User-Email": alice})
    try:
        urllib.request.urlopen(req, timeout=30)
        status = 200
    except urllib.error.HTTPError as e:
        status = e.code
    _check(status == 401, f"the old self-declared X-Dev-User-Email header is refused ({status})")
    status, _ = api("GET", "/healthz")
    _check(status == 200, "/healthz stays open")

    print("\n--- registration and sign-in ---")
    status, body = api("POST", "/auth/register", {"email": alice, "password": PASSWORD, "enrollment_code": "wrong-code"})
    _check(status == 403 and body.get("error", {}).get("code") == "invalid_enrollment_code",
           f"registering with a wrong enrollment code is refused ({status})")
    status, body = api("POST", "/auth/register", {"email": alice, "password": "short", "enrollment_code": enrollment_code()})
    _check(status == 400, f"a short password is refused ({status})")
    status, body = api("POST", "/auth/register", {"email": alice, "password": PASSWORD, "full_name": "Alice",
                                                  "department": "IT", "enrollment_code": enrollment_code()})
    _check(status == 201 and body.get("token", "").startswith("cps_"), f"registering with the code signs Alice in ({status})")
    status, _ = api("POST", "/auth/register", {"email": alice.upper(), "password": PASSWORD, "enrollment_code": enrollment_code()})
    _check(status == 409, f"the same email, differently cased, can't register twice ({status})")

    unknown = api("POST", "/auth/login", {"email": f"nobody-{suffix}@sti-th.com", "password": PASSWORD})
    wrong = api("POST", "/auth/login", {"email": alice, "password": "not-the-password"})
    _check(unknown[0] == wrong[0] == 401 and unknown[1] == wrong[1],
           "an unknown email and a wrong password get the identical answer")
    status, body = api("POST", "/auth/login", {"email": alice, "password": PASSWORD})
    _check(status == 200, f"the right password signs in ({status})")
    session = body.get("token", "")
    status, me = api("GET", "/auth/me", token=session)
    _check(status == 200 and me["user"]["email"] == alice and me["credential"]["kind"] == "session",
           "/auth/me identifies the session's owner")

    victim = f"locked-{suffix}@sti-th.com"
    api("POST", "/auth/register", {"email": victim, "password": PASSWORD, "enrollment_code": enrollment_code()})
    for _ in range(5):
        api("POST", "/auth/login", {"email": victim, "password": "guess"})
    status, body = api("POST", "/auth/login", {"email": victim, "password": PASSWORD})
    _check(status == 429, f"after 5 wrong passwords even the right one is refused for now ({status})")

    print("\n--- a machine fetches its key ---")
    status, start = api("POST", "/auth/device", {"client_name": f"DESKTOP-{suffix} (Claude Code)"})
    _check(status == 201 and len(start.get("user_code", "")) == 9 and start.get("verification_uri_complete", "").endswith(start.get("user_code", "")),
           f"a machine starts a device authorization and gets a user code ({start.get('user_code')})")
    status, body = api("POST", "/auth/device/token", {"device_code": start["device_code"]})
    _check(status == 400 and body["error"]["code"] == "authorization_pending", "polling before approval -> authorization_pending")
    status, body = api("GET", f"/auth/device/{start['user_code'].lower().replace('-', '')}", token=session)
    _check(status == 200 and body["device"]["client_name"].startswith("DESKTOP-"),
           "the employee sees which machine is asking, however they type the code")
    status, _ = api("POST", f"/auth/device/{start['user_code']}/approve")
    _check(status == 401, f"approving without signing in is refused ({status})")
    status, _ = api("POST", f"/auth/device/{start['user_code']}/approve", token=session)
    _check(status == 200, "Alice approves it with her session")
    status, body = api("POST", "/auth/device/token", {"device_code": start["device_code"]})
    key = body.get("api_key", "")
    _check(status == 200 and key.startswith("cpk_") and body["user"]["email"] == alice and body["credential"]["name"].startswith("DESKTOP-"),
           "the machine collects an API key acting as Alice, named after the machine")
    status, body = api("POST", "/auth/device/token", {"device_code": start["device_code"]})
    _check(status == 400 and body["error"]["code"] == "expired_token", "the device code can't be used a second time")

    denied_start = api("POST", "/auth/device", {"client_name": "unknown box"})[1]
    api("POST", f"/auth/device/{denied_start['user_code']}/deny", token=session)
    status, body = api("POST", "/auth/device/token", {"device_code": denied_start["device_code"]})
    _check(status == 400 and body["error"]["code"] == "access_denied", "a denied machine gets access_denied, no key")

    print("\n--- the key deploys, and can do nothing to credentials ---")
    department = api("GET", "/departments", token=key)[1]["departments"][0]
    dept_id = department.get("ID") or department["id"]
    app_name = f"auth-{suffix}"
    yaml = f"app:\n  name: {app_name}\n  owner: IT\nservices:\n  api:\n    runtime: go\n    port: 8080\n"
    status, app = api("POST", "/applications", {"name": app_name, "description": "auth verification",
                                                 "owning_department_id": dept_id, "deployment_yaml_draft": yaml}, token=key)
    _check(status == 201, f"the key registers an application ({status})")
    app_id = app.get("id", "")
    status, validation = api("POST", f"/applications/{app_id}/validate", token=key)
    _check(status == 200 and validation.get("report", {}).get("valid") is True, "the key validates it")
    status, owners = api("GET", f"/applications/{app_id}/owners", token=session)
    primary = [o["user_id"] for o in owners.get("owners", []) if o.get("ownership_role") == "primary"]
    _check(status == 200 and primary == [me["user"]["id"]], f"the application's primary owner is Alice, who approved the key ({primary})")

    # The catalog is readable by every signed-in employee by design
    # (MOD-19); acting on someone else's application is not.
    bob_session = api("POST", "/auth/register", {"email": f"bob-{suffix}@sti-th.com", "password": PASSWORD,
                                                 "enrollment_code": enrollment_code()})[1]["token"]
    status, _ = api("POST", f"/applications/{app_id}/validate", token=bob_session)
    _check(status in (403, 404), f"another employee can't act on it ({status})")

    status, _ = api("GET", "/auth/api-keys", token=key)
    _check(status == 403, f"the key can't list keys ({status})")
    status, _ = api("POST", "/auth/api-keys", {"name": "escalate"}, token=key)
    _check(status == 403, f"the key can't issue itself another key ({status})")
    another = api("POST", "/auth/device", {"client_name": "self-approval attempt"})[1]
    status, _ = api("POST", f"/auth/device/{another['user_code']}/approve", token=key)
    _check(status == 403, f"the key can't approve a device ({status})")

    status, keys = api("GET", "/auth/api-keys", token=session)
    listed = keys.get("api_keys", [])
    _check(status == 200 and any(k["prefix"] == key[:12] for k in listed) and all("token" not in k for k in listed),
           "Alice's session lists the key by prefix, never by value")
    key_id = next((k["id"] for k in listed if k["prefix"] == key[:12]), "")
    status, _ = api("DELETE", f"/auth/api-keys/{key_id}", token=bob_session)
    _check(status == 404, f"Bob can't revoke Alice's key ({status})")
    status, _ = api("DELETE", f"/auth/api-keys/{key_id}", token=session)
    _check(status == 200, "Alice revokes it")
    status, _ = api("GET", "/applications", token=key)
    _check(status == 401, f"the revoked key stops working on the very next request ({status})")

    status, _ = api("POST", "/auth/logout", token=session)
    status, _ = api("GET", "/applications", token=session)
    _check(status == 401, f"a signed-out session stops working ({status})")

    print("\n--- audit and storage ---")
    fresh = api("POST", "/auth/login", {"email": alice, "password": PASSWORD})[1]["token"]
    status, audit = api("GET", "/audit-log?resource_type=credential", token=fresh)
    actions = {e.get("action") or e.get("Action") for e in audit.get("entries", [])}
    for action in ["auth.register", "auth.login", "auth.decide_device", "auth.issue_api_key", "auth.revoke_credential"]:
        _check(action in actions, f"Alice's audit log records {action}")

    dump = subprocess.run(["docker", "compose", "exec", "-T", "postgres", "pg_dump", "-U", "postgres", "-d", "platform"],
                          capture_output=True, text=True, timeout=300)
    if _check(dump.returncode == 0 and len(dump.stdout) > 10_000, f"took a full pg_dump ({len(dump.stdout)} bytes)"):
        for label, secret in [("the API key", key), ("the session token", session), ("the password", PASSWORD),
                              ("the device code", start["device_code"])]:
            _check(secret not in dump.stdout, f"{label} appears nowhere in it")

    api("POST", f"/applications/{app_id}/archive", token=fresh)
    api("POST", f"/applications/{app_id}/delete", {"confirm": True}, token=fresh)

    print()
    if _failures:
        print(f"{len(_failures)} check(s) FAILED:")
        for f in _failures:
            print("  - " + f)
        return 1
    print("All checks passed.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
