"""Verifies an administrator's suspension (DEC-030) against the running stack.

Restarts platform-api with a fresh administrator in PLATFORM_ADMIN_EMAILS,
deploys a real Go application to dev and gives it a dev copy, then checks:

  - only an administrator can suspend, and only with a reason
  - the administrator's suspension stops it (no container, not served) and
    tells the owners the reason
  - the owners can't resume, archive or delete it, nor deploy its dev copy
  - the dashboard shows the hold
  - the administrator's resume brings it back (served again), tells the
    owners, lifts the hold, and the owners have their application back
  - an administrator's resume of an owner's own suspension is refused
  - a held dev copy can't be deployed or reset by its owners

Cleans up after itself (archives and deletes the application) and restarts
platform-api with its normal environment.

    python services/platform-api/scripts/verify_admin_hold.py
"""

from __future__ import annotations

import io
import os
import subprocess
import sys
import tarfile
import time
import urllib.error
import urllib.request
import uuid

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from platform_auth import api_key_for, call, session_for  # noqa: E402

BASE_URL = os.environ.get("PLATFORM_API_BASE_URL", "http://localhost:8099")
REPO = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "..", ".."))
_failures: list[str] = []

APP = '''package main

import (
	"fmt"
	"net/http"
)

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "ok") })
	http.ListenAndServe(":8080", nil)
}
'''


def _check(cond, msg):
    print(("OK:   " if cond else "FAIL: ") + msg, flush=True)
    if not cond:
        _failures.append(msg)
    return cond


def _restart_platform(extra_env):
    env = {**os.environ, **extra_env}
    subprocess.run(["docker", "compose", "up", "-d", "--force-recreate", "platform-api"], cwd=REPO, env=env,
                   capture_output=True, check=True)
    for _ in range(90):
        try:
            urllib.request.urlopen(BASE_URL + "/healthz", timeout=5)
            return
        except Exception:
            time.sleep(1)


def _source() -> bytes:
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w:gz") as tar:
        for fname, content in (("api/main.go", APP), ("api/go.mod", "module holdcheck\n\ngo 1.25\n")):
            data = content.encode()
            info = tarfile.TarInfo(fname)
            info.size = len(data)
            tar.addfile(info, io.BytesIO(data))
    return buf.getvalue()


def _post_archive(path, token):
    req = urllib.request.Request(f"{BASE_URL}{path}", data=_source(), method="POST",
                                 headers={"Authorization": f"Bearer {token}", "Content-Type": "application/gzip"})
    try:
        with urllib.request.urlopen(req, timeout=900) as resp:
            return resp.status, resp.read().decode()
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode()


def _served(name) -> int:
    try:
        with urllib.request.urlopen(f"{BASE_URL}/run/{name}/api/", timeout=60) as resp:
            return resp.status
    except urllib.error.HTTPError as e:
        return e.code
    except Exception:
        return 0


def _containers(app_name_part: str) -> list[str]:
    out = subprocess.run(["docker", "ps", "--format", "{{.Names}}"], capture_output=True, text=True).stdout
    return [n for n in out.split() if n.startswith("platform-run-") and app_name_part in n]


def _running_containers_of(deployment_id: str) -> list[str]:
    return _containers(deployment_id.replace("-", "")[:8]) or _containers(deployment_id[:8])


def _notes(token, name):
    _, body = call(BASE_URL, "GET", "/notifications", token=token)
    return [n for n in body.get("notifications", []) if name in n.get("title", "")]


def _overview_row(admin, app_id):
    _, o = call(BASE_URL, "GET", "/admin/overview", token=admin)
    return next((a for a in o.get("applications", []) if a["id"] == app_id), None)


def _err(body):
    return (body or {}).get("error", {}) if isinstance(body, dict) else {}


def main():
    suffix = uuid.uuid4().hex[:6]
    owner_email, admin_email = f"hold-owner-{suffix}@sti-th.com", f"hold-admin-{suffix}@sti-th.com"
    owner = session_for(BASE_URL, owner_email)
    admin = session_for(BASE_URL, admin_email)
    key = api_key_for(BASE_URL, owner_email, "verify_admin_hold.py")
    print(f"=== administrator's suspension against {BASE_URL} ===", flush=True)
    _restart_platform({"PLATFORM_ADMIN_EMAILS": admin_email})
    app_id = None
    try:
        _, deps = call(BASE_URL, "GET", "/departments", token=key)
        dept = deps["departments"][0].get("ID") or deps["departments"][0]["id"]
        name = f"hold-{suffix}"
        yaml = f"app:\n  name: {name}\n  owner: IT\nservices:\n  api:\n    runtime: go\n    port: 8080\nscaling:\n  min: 1\n"
        _, app = call(BASE_URL, "POST", "/applications", {"name": name, "description": "admin hold",
                                                          "owning_department_id": dept, "deployment_yaml_draft": yaml}, token=key)
        app_id = app["id"]
        call(BASE_URL, "PUT", f"/applications/{app_id}/profile", {"purpose": "admin hold verification",
                                                                    "handles_personal_data": False}, token=key)
        call(BASE_URL, "POST", f"/applications/{app_id}/validate", token=key)
        status, body = _post_archive(f"/applications/{app_id}/build", key)
        if status >= 300:
            raise SystemExit(f"build failed: {status} {body[:300]}")
        _, dep = call(BASE_URL, "POST", f"/applications/{app_id}/deploy", {"environment": "dev"}, token=key)
        if dep.get("status") != "running":
            raise SystemExit(f"deploy failed: {dep}")
        _check(_served(name) == 200, "the application is up and served")
        status, body = _post_archive(f"/applications/{app_id}/dev", key)
        _check(status == 200, f"it has a dev copy ({status})")

        print("\n--- who may suspend ---", flush=True)
        status, body = call(BASE_URL, "POST", f"/admin/applications/{app_id}/suspend", {"reason": "x"}, token=owner)
        _check(status == 403, f"an owner can't use the administrator's suspension ({status})")
        status, body = call(BASE_URL, "POST", f"/admin/applications/{app_id}/suspend", {"reason": "  "}, token=admin)
        _check(status == 400 and _err(body).get("code") == "reason_required", f"a reason is required ({status} {_err(body)})")
        _check(_served(name) == 200, "neither attempt touched it")

        print("\n--- the administrator suspends it ---", flush=True)
        reason = "sends customer data to an outside address"
        status, hold = call(BASE_URL, "POST", f"/admin/applications/{app_id}/suspend", {"reason": reason}, token=admin)
        _check(status == 200 and hold.get("reason") == reason and hold.get("held_by_email") == admin_email,
               f"suspended, with the reason and who ({status} {hold})")
        _, app = call(BASE_URL, "GET", f"/applications/{app_id}", token=key)
        _check(app.get("lifecycle_status") == "suspended", f"the application is suspended ({app.get('lifecycle_status')})")
        _check(not _running_containers_of(dep["id"]), f"its containers stopped ({_running_containers_of(dep['id'])})")
        _check(_served(name) != 200, f"it is no longer served ({_served(name)})")
        notes = _notes(owner, name)
        _check(any("suspended by a platform administrator" in n["title"] and reason in n.get("detail", "")
                   and admin_email in n.get("detail", "") for n in notes),
               f"the owner is told the reason and whom to contact ({[n['title'] for n in notes]})")
        row = _overview_row(admin, app_id)
        _check(row is not None and (row.get("hold") or {}).get("reason") == reason, f"the dashboard shows the hold ({row and row.get('hold')})")

        print("\n--- the owners can't bring it back ---", flush=True)
        status, body = call(BASE_URL, "POST", f"/applications/{app_id}/resume", token=key)
        _check(status == 409 and _err(body).get("code") == "admin_hold" and reason in _err(body).get("message", ""),
               f"resume is refused, saying why ({status} {_err(body)})")
        status, body = call(BASE_URL, "POST", f"/applications/{app_id}/archive", token=key)
        _check(status == 409 and _err(body).get("code") == "admin_hold", f"archive is refused ({status})")
        status, body = call(BASE_URL, "POST", f"/applications/{app_id}/delete", {"confirm": True}, token=key)
        _check(status == 409 and _err(body).get("code") == "admin_hold", f"delete is refused ({status})")
        status, body = _post_archive(f"/applications/{app_id}/dev", key)
        _check(status == 409 and "admin_hold" in body, f"its dev copy can't be deployed ({status})")
        _check(_served(name) != 200, "it is still not served")

        print("\n--- the administrator resumes it ---", flush=True)
        status, body = call(BASE_URL, "POST", f"/admin/applications/{app_id}/resume", token=owner)
        _check(status == 403, f"an owner can't use the administrator's resume ({status})")
        status, app = call(BASE_URL, "POST", f"/admin/applications/{app_id}/resume", token=admin)
        _check(status == 200 and app.get("lifecycle_status") == "running", f"resumed ({status} {app.get('lifecycle_status')})")
        _check(_served(name) == 200, f"served again ({_served(name)})")
        _check(any("resumed by a platform administrator" in n["title"] for n in _notes(owner, name)), "the owner is told")
        row = _overview_row(admin, app_id)
        _check(row is not None and row.get("hold") is None, "the hold is lifted on the dashboard")
        status, _ = call(BASE_URL, "POST", f"/applications/{app_id}/suspend", token=key)
        status2, _ = call(BASE_URL, "POST", f"/applications/{app_id}/resume", token=key)
        _check(status == 200 and status2 == 200, f"the owners have their application back: suspend {status}, resume {status2}")

        print("\n--- an owner's own suspension ---", flush=True)
        call(BASE_URL, "POST", f"/applications/{app_id}/suspend", token=key)
        status, body = call(BASE_URL, "POST", f"/admin/applications/{app_id}/resume", token=admin)
        _check(status == 409 and _err(body).get("code") == "not_admin_held", f"the administrator's resume doesn't apply ({status})")
        status, _ = call(BASE_URL, "POST", f"/admin/applications/{app_id}/suspend", {"reason": "under review"}, token=admin)
        status2, body = call(BASE_URL, "POST", f"/applications/{app_id}/resume", token=key)
        _check(status == 200 and status2 == 409, f"an administrator can hold one its owners suspended ({status}, owner resume {status2})")
        call(BASE_URL, "POST", f"/admin/applications/{app_id}/resume", token=admin)

        print("\n--- a held dev copy ---", flush=True)
        _, dev = call(BASE_URL, "GET", f"/applications/{app_id}/dev", token=key)
        copy_id = (dev.get("dev_copy") or dev).get("application_id")
        status, _ = call(BASE_URL, "POST", f"/admin/applications/{copy_id}/suspend", {"reason": "dev copy floods the host"}, token=admin)
        _check(status == 200, f"the administrator suspends the dev copy ({status})")
        status, body = _post_archive(f"/applications/{app_id}/dev", key)
        _check(status == 409 and "admin_hold" in body, f"it can't be deployed ({status})")
        status, body = call(BASE_URL, "DELETE", f"/applications/{app_id}/dev", token=key)
        _check(status == 409 and _err(body).get("code") == "admin_hold", f"nor reset ({status} {_err(body)})")
        _check(_served(name) == 200, "the application itself is untouched")
        status, _ = call(BASE_URL, "POST", f"/admin/applications/{copy_id}/resume", token=admin)
        _check(status == 200, f"the administrator resumes the dev copy ({status})")
    finally:
        if app_id:
            call(BASE_URL, "POST", f"/applications/{app_id}/suspend", token=key)
            call(BASE_URL, "POST", f"/applications/{app_id}/archive", token=key)
            call(BASE_URL, "POST", f"/applications/{app_id}/delete", {"confirm": True}, token=key)
        _restart_platform({})

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
