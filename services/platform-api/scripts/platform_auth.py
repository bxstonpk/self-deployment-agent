"""How a verification script signs in under PLATFORM_AUTH_MODE=token.

It registers (or signs in) an employee with the enrollment code and gets
an API key the same way a Claude Code machine does — a device
authorization, approved with the employee's own session — so a script
exercises the real path rather than a shortcut.

The enrollment code is read from PLATFORM_ENROLLMENT_CODE, or else from
the repo-root .env; it is never printed.
"""

from __future__ import annotations

import json
import os
import urllib.error
import urllib.request
from pathlib import Path

PASSWORD = "verification-only-password"


def enrollment_code() -> str:
    code = os.environ.get("PLATFORM_ENROLLMENT_CODE", "")
    if code:
        return code
    env = Path(__file__).resolve().parents[3] / ".env"
    if env.exists():
        for line in env.read_text().splitlines():
            if line.startswith("PLATFORM_ENROLLMENT_CODE="):
                return line.split("=", 1)[1].strip()
    raise SystemExit("PLATFORM_ENROLLMENT_CODE is not set and not in the repo-root .env")


def call(base_url: str, method: str, path: str, body=None, token: str | None = None):
    """One JSON request. Returns (status, parsed body)."""
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(base_url + path, data=data, method=method)
    if data is not None:
        req.add_header("Content-Type", "application/json")
    if token:
        req.add_header("Authorization", f"Bearer {token}")
    try:
        with urllib.request.urlopen(req, timeout=60) as resp:
            raw, status = resp.read(), resp.status
    except urllib.error.HTTPError as e:
        raw, status = e.read(), e.code
    return status, (json.loads(raw) if raw else {})


def session_for(base_url: str, email: str, full_name: str = "Verifier", department: str = "IT") -> str:
    """Registers the employee if needed, otherwise signs in. Returns a session token."""
    status, body = call(base_url, "POST", "/auth/register", {
        "email": email, "password": PASSWORD, "full_name": full_name,
        "department": department, "enrollment_code": enrollment_code(),
    })
    if status == 201:
        return body["token"]
    if status != 409:
        raise SystemExit(f"register {email} -> {status}: {body}")
    status, body = call(base_url, "POST", "/auth/login", {"email": email, "password": PASSWORD})
    if status != 200:
        raise SystemExit(f"login {email} -> {status}: {body}")
    return body["token"]


def api_key_for(base_url: str, email: str, client_name: str = "verification script") -> str:
    """An API key for this employee, fetched through a real device approval."""
    session = session_for(base_url, email)
    status, start = call(base_url, "POST", "/auth/device", {"client_name": client_name})
    if status != 201:
        raise SystemExit(f"start device -> {status}: {start}")
    status, body = call(base_url, "POST", f"/auth/device/{start['user_code']}/approve", token=session)
    if status != 200:
        raise SystemExit(f"approve device -> {status}: {body}")
    status, body = call(base_url, "POST", "/auth/device/token", {"device_code": start["device_code"]})
    if status != 200:
        raise SystemExit(f"collect key -> {status}: {body}")
    return body["api_key"]
