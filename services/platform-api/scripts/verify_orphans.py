"""End-to-end verification that no application container outlives its
deployment, and that a restart can't leave a build in flight for good,
against a live stack:

    python services/platform-api/scripts/verify_orphans.py

Uses the local stack's short scale-to-zero settings (.env:
SCALE_TO_ZERO_IDLE_SECONDS=20, SCALE_SWEEP_INTERVAL_SECONDS=5) and restarts
platform-api once, mid-build. Checks:

  - an API that scaled to zero and was woken again runs in a new container;
    deploying a new version stops that one too (found on the LAN host as 38
    superseded deployments' API containers, still running)
  - with the application asleep, its database sleeps
  - a container whose deployment is no longer live (one left by any path)
    is removed by the orphan sweep within about a minute; the live
    deployment's are kept
  - a build interrupted by a platform-api restart is closed as failed, the
    application is back to Running, and the next build goes through (it was
    refused as already in flight for good)
"""

from __future__ import annotations

import io
import json
import os
import subprocess
import sys
import tarfile
import threading
import time
import urllib.error
import urllib.request
import uuid
from pathlib import Path

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from platform_auth import api_key_for, call  # noqa: E402

BASE_URL = os.environ.get("PLATFORM_API_BASE_URL", "http://localhost:8099")
REPO = Path(__file__).resolve().parents[3]
TEMPLATES = REPO / "company-deployment-skill" / "templates"
EXCLUDED = {"node_modules", "build", "dist", "__pycache__", ".git"}
_failures: list[str] = []


def _check(cond, msg):
    print(("OK:   " if cond else "FAIL: ") + msg, flush=True)
    if not cond:
        _failures.append(msg)
    return cond


def _docker(*args) -> str:
    return subprocess.run(["docker", *args], capture_output=True, text=True).stdout.strip()


def _containers(label: str, running_only=True) -> list[str]:
    args = ["ps", "--format", "{{.Names}}", "--filter", f"label={label}"]
    if not running_only:
        args.insert(1, "-a")
    return [n for n in _docker(*args).splitlines() if n]


def _api(deployment_id: str) -> list[str]:
    """The deployment's running API container(s), by label: a deploy names
    it platform-run-<app>-api-<id>, a cold start platform-run-<id>-api."""
    return _containers(f"platform.deployment_id={deployment_id}") and         [n for n in _docker("ps", "--format", "{{.Names}}", "--filter", f"label=platform.deployment_id={deployment_id}",
                            "--filter", "label=platform.service=api").splitlines() if n]


def _wait(pred, timeout, step=2.0):
    deadline = time.time() + timeout
    while time.time() < deadline:
        v = pred()
        if v:
            return v
        time.sleep(step)
    return pred()


def _source_archive(marker: str) -> bytes:
    """The react + go templates, with a file that changes every build so the
    API's build can't come wholly from Docker's cache."""
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w:gz") as tar:
        for service, folder in (("frontend", "frontend-react"), ("api", "api-go")):
            root = TEMPLATES / folder
            for path in sorted(root.rglob("*")):
                rel = path.relative_to(root)
                if any(part in EXCLUDED for part in rel.parts) or not path.is_file():
                    continue
                tar.add(path, arcname=f"{service}/{rel.as_posix()}")
        data = f"package main\n\nconst buildMarker = {json.dumps(marker)}\n".encode()
        info = tarfile.TarInfo("api/marker.go")
        info.size = len(data)
        tar.addfile(info, io.BytesIO(data))
    return buf.getvalue()


def _build(app_id, key, marker, timeout=1200):
    req = urllib.request.Request(f"{BASE_URL}/applications/{app_id}/build", data=_source_archive(marker), method="POST",
                                 headers={"Authorization": f"Bearer {key}", "Content-Type": "application/gzip"})
    try:
        return json.loads(urllib.request.urlopen(req, timeout=timeout).read())
    except urllib.error.HTTPError as e:
        return {"http_status": e.code, "body": e.read().decode(errors="replace")}
    except Exception as e:  # the restart cuts the connection
        return {"error": str(e)}


def _deploy(app_id, key):
    """A deploy waits for the scan, the database and the health check: longer
    than call()'s 60 s the first time."""
    req = urllib.request.Request(f"{BASE_URL}/applications/{app_id}/deploy", data=json.dumps({"environment": "dev"}).encode(),
                                 method="POST", headers={"Authorization": f"Bearer {key}", "Content-Type": "application/json"})
    try:
        return json.loads(urllib.request.urlopen(req, timeout=900).read())
    except urllib.error.HTTPError as e:
        return {"http_status": e.code, "body": e.read().decode(errors="replace")}


def _get(url, timeout=60):
    try:
        with urllib.request.urlopen(url, timeout=timeout) as r:
            return r.status, r.read()
    except urllib.error.HTTPError as e:
        return e.code, e.read()
    except Exception:
        return 0, b""


def _healthy():
    return _get(BASE_URL + "/healthz", timeout=3)[0] == 200


def main():
    suffix = uuid.uuid4().hex[:6]
    key = api_key_for(BASE_URL, f"orphans-{suffix}@sti-th.com", "orphan verify")
    name = f"orphans-{suffix}"
    app_id = None
    stray = f"platform-run-stray{suffix}-api"
    keep = f"platform-run-keep{suffix}-api"
    print(f"=== Orphan containers and interrupted builds against {BASE_URL} ===\n", flush=True)
    try:
        _, deps = call(BASE_URL, "GET", "/departments", token=key)
        dept = deps["departments"][0].get("ID") or deps["departments"][0]["id"]
        yaml = (TEMPLATES / "deployment.yaml").read_text().replace("<application-name>", name) \
            .replace("<department>", "IT").replace("<python | nodejs | typescript | go>", "go")
        _, app = call(BASE_URL, "POST", "/applications", {"name": name, "description": "orphan verification",
                                                          "owning_department_id": dept, "deployment_yaml_draft": yaml}, token=key)
        app_id = app["id"]
        call(BASE_URL, "PUT", f"/applications/{app_id}/profile", {
            "purpose": "orphan verification", "handles_personal_data": False,
            "frontend": "react", "backend": "go", "database": "mysql"}, token=key)
        _, v = call(BASE_URL, "POST", f"/applications/{app_id}/validate", token=key)
        _check(v.get("report", {}).get("valid") is True, "react + go + mysql validates")

        # --- 1. a woken API is stopped when its deployment is superseded --
        b = _build(app_id, key, "first")
        if not _check(str(b.get("status")).lower() == "succeeded", "the first build"):
            print(json.dumps(b, indent=2)[:2000])
            return 1
        first = _deploy(app_id, key)
        if not _check(first.get("status") == "running", f"the first version runs ({first.get('status')})"):
            print(json.dumps(first, indent=2)[:1500])
            return 1
        api_of_first = f"platform.deployment_id={first['id']}"
        started_with = _api(first["id"])
        asleep = _wait(lambda: not _api(first["id"]), 120)
        _check(started_with and asleep, f"its API scales to zero ({started_with} stopped)")
        status, body = _get(f"{BASE_URL}/run/{name}/api/")
        woken = _api(first["id"])
        _check(status == 200 and woken and woken != started_with,
               f"a request wakes it in a new container ({status}, {woken})")

        b = _build(app_id, key, "second")
        _check(str(b.get("status")).lower() == "succeeded", "the second build")
        second = _deploy(app_id, key)
        _check(second.get("status") == "running", f"the second version runs ({second.get('status')})")
        left = _containers(api_of_first, running_only=False)
        _check(not left, f"nothing of the first version is left, the woken API included ({left})")

        # --- 2. the database sleeps with its application -----------------
        _wait(lambda: not _api(second["id"]), 120)
        db = [n for n in _docker("ps", "-a", "--format", "{{.Names}}").splitlines() if n.startswith("platform-db-" + name)]
        slept = _wait(lambda: db and _docker("inspect", db[0], "--format", "{{.State.Running}}") == "false", 120)
        _check(slept, f"with its API asleep, its database sleeps ({db})")

        # --- 3. the orphan sweep ------------------------------------------
        for cname, dep_id in ((stray, first["id"]), (keep, second["id"])):
            _docker("run", "-d", "--name", cname, "--label", f"platform.deployment_id={dep_id}",
                    "--label", f"platform.application_id={app_id}", "--label", "platform.service=api",
                    "alpine:3.20", "sleep", "3600")
        _check(_containers(f"platform.deployment_id={first['id']}"), "a container of the superseded version is started by hand")
        gone = _wait(lambda: stray not in _docker("ps", "-a", "--format", "{{.Names}}").splitlines(), 100, step=5)
        _check(gone, "the orphan sweep removes it within about a minute")
        _check(keep in _docker("ps", "--format", "{{.Names}}").splitlines(), "and keeps the live version's")
        _docker("rm", "-f", keep)
        # The hand-made one kept the database awake; let it sleep again
        # before the build below wakes it, so the run stays quick.

        # --- 4. a build interrupted by a restart -------------------------
        result: dict = {}
        t = threading.Thread(target=lambda: result.update(_build(app_id, key, "interrupted-" + uuid.uuid4().hex)))
        t.start()
        in_flight = _wait(lambda: call(BASE_URL, "GET", f"/applications/{app_id}/builds/latest", token=key)[1]
                          .get("status") == "in_progress", 60, step=0.3)
        _check(in_flight, "a third build is in progress")
        subprocess.run(["docker", "compose", "restart", "platform-api"], cwd=REPO, capture_output=True, check=True)
        _wait(_healthy, 120, step=1)
        t.join(timeout=60)
        _, latest = call(BASE_URL, "GET", f"/applications/{app_id}/builds/latest", token=key)
        detail = str(latest.get("error_detail") or latest.get("ErrorDetail") or "")
        if str(result.get("status")).lower() == "succeeded":
            _check(False, "the build finished before the restart: nothing was interrupted (run again)")
        else:
            _check(latest.get("status") == "failed" and "restarted" in detail,
                   f"after the restart the build is closed as failed ({latest.get('status')}: {detail})")
        _, a = call(BASE_URL, "GET", f"/applications/{app_id}", token=key)
        _check(a.get("lifecycle_status") == "running", f"the application is back to Running ({a.get('lifecycle_status')})")
        b = _build(app_id, key, "after-restart")
        _check(str(b.get("status")).lower() == "succeeded", f"and the next build goes through ({b.get('status') or b})")
        third = _deploy(app_id, key)
        _check(third.get("status") == "running", f"and deploys ({third.get('status')})")
        left = _containers(f"platform.deployment_id={second['id']}", running_only=False)
        _check(not left, f"nothing of the second version is left ({left})")
    finally:
        for cname in (stray, keep):
            subprocess.run(["docker", "rm", "-f", cname], capture_output=True)
        if app_id:
            call(BASE_URL, "POST", f"/applications/{app_id}/archive", token=key)
            status, _ = call(BASE_URL, "POST", f"/applications/{app_id}/delete", {"confirm": True}, token=key)
            print(f"cleanup: application deleted ({status})")

    print()
    if _failures:
        print(f"{len(_failures)} check(s) failed:")
        for f in _failures:
            print("  - " + f)
        return 1
    print("All checks passed.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
