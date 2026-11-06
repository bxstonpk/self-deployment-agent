"""Verifies source history against the running stack: every build's kept
source can be read at any time, the application's and its dev copy's.

Builds a real Go application, deploys it to dev, deploys two versions to
its dev copy, then checks:

  - GET /applications/{id}/sources lists all three builds, newest first,
    the dev copy's marked as such, from the application and from the copy
  - each build's archive downloads byte for byte as it was uploaded
  - another employee can't list or download them (403)
  - a build of another application can't be read through this one (404)

Cleans up after itself (archives and deletes the application, which also
removes its dev copy). Doesn't restart platform-api.

    python services/platform-api/scripts/verify_source_history.py
"""

from __future__ import annotations

import io
import os
import sys
import tarfile
import urllib.error
import urllib.request
import uuid

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from platform_auth import api_key_for, call  # noqa: E402

BASE_URL = os.environ.get("PLATFORM_API_BASE_URL", "http://localhost:8099")
_failures: list[str] = []

APP = '''package main

import (
	"fmt"
	"net/http"
)

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "%s") })
	http.ListenAndServe(":8080", nil)
}
'''


def _check(cond, msg):
    print(("OK:   " if cond else "FAIL: ") + msg, flush=True)
    if not cond:
        _failures.append(msg)
    return cond


def _source(version: str) -> bytes:
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w:gz") as tar:
        for fname, content in (("api/main.go", APP % version), ("api/go.mod", "module sourcecheck\n\ngo 1.25\n")):
            data = content.encode()
            info = tarfile.TarInfo(fname)
            info.size = len(data)
            tar.addfile(info, io.BytesIO(data))
    return buf.getvalue()


def _post_archive(path, token, archive):
    req = urllib.request.Request(f"{BASE_URL}{path}", data=archive, method="POST",
                                 headers={"Authorization": f"Bearer {token}", "Content-Type": "application/gzip"})
    try:
        with urllib.request.urlopen(req, timeout=900) as resp:
            return resp.status, resp.read().decode()
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode()


def _download(path, token):
    req = urllib.request.Request(f"{BASE_URL}{path}", headers={"Authorization": f"Bearer {token}"})
    try:
        with urllib.request.urlopen(req, timeout=60) as resp:
            return resp.status, resp.read(), resp.headers.get("Content-Disposition", "")
    except urllib.error.HTTPError as e:
        return e.code, e.read(), ""


def _register(key, dept, name):
    yaml = f"app:\n  name: {name}\n  owner: IT\nservices:\n  api:\n    runtime: go\n    port: 8080\nscaling:\n  min: 1\n"
    status, app = call(BASE_URL, "POST", "/applications", {"name": name, "description": "source history",
                                                           "owning_department_id": dept, "deployment_yaml_draft": yaml}, token=key)
    if status != 201:
        raise SystemExit(f"register {name}: {status} {app}")
    call(BASE_URL, "PUT", f"/applications/{app['id']}/profile", {"purpose": "source history verification",
                                                                  "handles_personal_data": False}, token=key)
    call(BASE_URL, "POST", f"/applications/{app['id']}/validate", token=key)
    return app["id"]


def _remove(key, app_id):
    call(BASE_URL, "POST", f"/applications/{app_id}/archive", token=key)
    call(BASE_URL, "POST", f"/applications/{app_id}/delete", {"confirm": True}, token=key)


def main():
    suffix = uuid.uuid4().hex[:6]
    owner_email, other_email = f"src-owner-{suffix}@sti-th.com", f"src-other-{suffix}@sti-th.com"
    key = api_key_for(BASE_URL, owner_email, "verify_source_history.py")
    other = api_key_for(BASE_URL, other_email, "verify_source_history.py")
    print(f"=== source history against {BASE_URL} ===", flush=True)
    app_id = other_id = None
    try:
        _, deps = call(BASE_URL, "GET", "/departments", token=key)
        dept = deps["departments"][0].get("ID") or deps["departments"][0]["id"]
        name = f"src-{suffix}"
        app_id = _register(key, dept, name)
        uploads = {}

        print("\n--- a build of the application, two of its dev copy ---", flush=True)
        prod_archive = _source("v1")
        status, body = _post_archive(f"/applications/{app_id}/build", key, prod_archive)
        if status >= 300:
            raise SystemExit(f"build failed: {status} {body[:300]}")
        _, dep = call(BASE_URL, "POST", f"/applications/{app_id}/deploy", {"environment": "dev"}, token=key)
        _check(dep.get("status") == "running", f"the application is deployed ({dep.get('status')})")
        for version in ("dev-1", "dev-2"):
            status, body = _post_archive(f"/applications/{app_id}/dev", key, _source(version))
            _check(status == 200, f"dev copy deploy {version} ({status} {body[:200] if status != 200 else ''})")
        _, latest = call(BASE_URL, "GET", f"/applications/{app_id}/builds/latest", token=key)
        uploads[latest["id"]] = prod_archive
        status, copy = call(BASE_URL, "GET", f"/applications/{app_id}/dev", token=key)
        copy_id = copy["dev_copy"]["application_id"]

        print("\n--- the history ---", flush=True)
        status, hist = call(BASE_URL, "GET", f"/applications/{app_id}/sources", token=key)
        builds = hist.get("builds", [])
        _check(status == 200 and len(builds) == 3, f"three builds listed ({status}, {len(builds)})")
        names = [(b["application_name"], b["dev_copy"]) for b in builds]
        _check(names == [(f"{name}-dev", True), (f"{name}-dev", True), (name, False)],
               f"newest first, the dev copy's marked ({names})")
        _check(all(b["kept"] and b["source_bytes"] > 0 for b in builds), "every build's source is kept, with its size")
        _check(all(b["triggered_by"] == owner_email for b in builds), f"who built each ({[b['triggered_by'] for b in builds]})")
        prod = builds[-1]
        _check(prod["build_id"] == latest["id"] and any(d["environment"] == "dev" and d["status"] == "running" for d in prod["deployments"]),
               f"the application's build shows its deployment ({prod['deployments']})")
        status, from_copy = call(BASE_URL, "GET", f"/applications/{copy_id}/sources", token=key)
        _check(status == 200 and [b["build_id"] for b in from_copy.get("builds", [])] == [b["build_id"] for b in builds],
               "the same history from the dev copy's own page")

        print("\n--- the files ---", flush=True)
        dev_versions = {"dev-2": builds[0], "dev-1": builds[1]}
        for version, b in dev_versions.items():
            uploads[b["build_id"]] = _source(version)
        for b in builds:
            status, data, disposition = _download(f"/applications/{app_id}/sources/{b['build_id']}", key)
            ok = status == 200 and b["application_name"] in disposition
            if b["build_id"] == prod["build_id"]:
                ok = ok and data == prod_archive
            else:
                with tarfile.open(fileobj=io.BytesIO(data), mode="r:gz") as tar:
                    main_go = tar.extractfile("api/main.go").read().decode()
                want = next(v for v, x in dev_versions.items() if x["build_id"] == b["build_id"])
                ok = ok and f'"{want}"' in main_go
            _check(ok, f"{b['application_name']} {b['build_id'][:8]} downloads as uploaded ({status}, {len(data)} bytes, {disposition})")

        print("\n--- who may read them ---", flush=True)
        status, _ = call(BASE_URL, "GET", f"/applications/{app_id}/sources", token=other)
        _check(status == 403, f"another employee can't list them ({status})")
        status, _, _ = _download(f"/applications/{app_id}/sources/{prod['build_id']}", other)
        _check(status == 403, f"nor download one ({status})")
        other_id = _register(other, dept, f"src-other-{suffix}")
        status, body = _post_archive(f"/applications/{other_id}/build", other, _source("other"))
        _check(status < 300, f"another employee's application builds ({status})")
        _, other_build = call(BASE_URL, "GET", f"/applications/{other_id}/builds/latest", token=other)
        status, _, _ = _download(f"/applications/{app_id}/sources/{other_build['id']}", key)
        _check(status == 404, f"its build can't be read through this application ({status})")
    finally:
        if other_id:
            _remove(other, other_id)
        if app_id:
            _remove(key, app_id)

    print()
    if _failures:
        print(f"{len(_failures)} check(s) failed")
        sys.exit(1)
    print("all checks passed")


if __name__ == "__main__":
    main()
