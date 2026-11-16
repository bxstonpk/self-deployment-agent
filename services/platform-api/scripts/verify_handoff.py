"""End-to-end verification of the IT handoff (DEC-033) against a live stack:
a real application promoted to production with a real signed approval, its
package made by the platform, downloaded, and then run the way IT would —
`docker load` and `docker compose up` from the package alone, away from
the platform.

    services/mcp-server/.venv/Scripts/python.exe services/platform-api/scripts/verify_handoff.py

(the mcp-server venv: approving uses `company-deploy` and `cryptography`.)
platform-api is restarted with PLATFORM_APPROVER_KEYS set to a key made
here, and restarted without it at the end. Checks:

  - a production deployment that goes live is packed by itself (trigger
    "promotion"), and only administrators and approvers see it
  - data put in afterwards is in the package made again on request
    (trigger "manual"), which replaces the first one and its file
  - the download is the recorded size and SHA-256, every file matches
    SHA256SUMS, a setting's value is nowhere in it (its name is), and the
    download is in the audit log
  - from the package alone: the images load, `docker compose up` imports
    the database, and the application answers at / (the React page and its
    bundle) and /api/ (connected, with the data that was put in)
  - deleting the application deletes its packages
"""

from __future__ import annotations

import hashlib
import io
import json
import os
import socket
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.error
import urllib.request
import uuid
from pathlib import Path

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from platform_auth import api_key_for, call, session_for  # noqa: E402

BASE_URL = os.environ.get("PLATFORM_API_BASE_URL", "http://localhost:8099")
REPO = Path(__file__).resolve().parents[3]
TEMPLATES = REPO / "company-deployment-skill" / "templates"
EXCLUDED = {"node_modules", "build", "dist", "__pycache__", ".git"}
CLI = REPO / "services" / "mcp-server" / ".venv" / "Scripts" / "company-deploy.exe"
if not CLI.exists():
    CLI = REPO / "services" / "mcp-server" / ".venv" / "bin" / "company-deploy"
SECRET_VALUE = "handoff-secret-" + uuid.uuid4().hex
_failures: list[str] = []


def _check(cond, msg):
    print(("OK:   " if cond else "FAIL: ") + msg, flush=True)
    if not cond:
        _failures.append(msg)
    return cond


def _restart(extra):
    subprocess.run(["docker", "compose", "up", "-d", "--force-recreate", "platform-api"], cwd=REPO,
                   env={**os.environ, **extra}, capture_output=True, check=True)
    for _ in range(90):
        try:
            urllib.request.urlopen(BASE_URL + "/healthz", timeout=5)
            return
        except Exception:
            time.sleep(1)


def _cli(args, env):
    r = subprocess.run([str(CLI), *args], capture_output=True, text=True, timeout=900,
                       env={**os.environ, **env, "PYTHONIOENCODING": "utf-8"})
    return r.returncode, r.stdout + r.stderr


def _source_archive() -> bytes:
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w:gz") as tar:
        for service, folder in (("frontend", "frontend-react"), ("api", "api-go")):
            root = TEMPLATES / folder
            for path in sorted(root.rglob("*")):
                rel = path.relative_to(root)
                if any(part in EXCLUDED for part in rel.parts) or not path.is_file():
                    continue
                tar.add(path, arcname=f"{service}/{rel.as_posix()}")
    return buf.getvalue()


def _yaml(name: str) -> str:
    text = (TEMPLATES / "deployment.yaml").read_text()
    text = text.replace("<application-name>", name).replace("<department>", "IT")
    text = text.replace("<python | nodejs | typescript | go>", "go")
    return text.replace("  min: 0\n", "  min: 1\n")


def _wait_handoff(token, app_id, want, timeout=900):
    """The application's entry once its ready package is `want` (a predicate)."""
    deadline = time.time() + timeout
    entry = {}
    while time.time() < deadline:
        status, body = call(BASE_URL, "GET", "/handoffs", token=token)
        entry = next((e for e in body.get("handoffs", []) if e["application_id"] == app_id), {}) if status == 200 else {}
        if entry.get("ready") and want(entry["ready"]):
            return entry
        if (entry.get("latest") or {}).get("status") == "failed":
            return entry
        time.sleep(3)
    return entry


def _get(url, timeout=10):
    try:
        with urllib.request.urlopen(url, timeout=timeout) as r:
            return r.status, r.read()
    except urllib.error.HTTPError as e:
        return e.code, e.read()
    except Exception:
        return 0, b""


def _free_port() -> int:
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def _volume_has(volume: str, path: str) -> bool:
    r = subprocess.run(["docker", "run", "--rm", "-v", f"{volume}:/v:ro", "alpine:3.20", "test", "-e", f"/v/{path}"],
                       capture_output=True)
    return r.returncode == 0


def main():
    suffix = uuid.uuid4().hex[:6]
    owner_email, admin_email = f"handoff-owner-{suffix}@sti-th.com", f"handoff-approver-{suffix}@sti-th.com"
    owner_key = api_key_for(BASE_URL, owner_email, "handoff verify (owner)")
    approver = session_for(BASE_URL, admin_email)
    tmp = Path(tempfile.mkdtemp(prefix="handoff-"))
    creds = tmp / "credentials.json"
    creds.write_text(json.dumps({"platform_url": BASE_URL, "api_key": api_key_for(BASE_URL, admin_email, "approver laptop"),
                                 "key_id": "", "key_name": "approver laptop", "email": admin_email, "expires_at": ""}))
    key_file = tmp / "approver_ed25519.pem"
    approver_env = {"COMPANY_DEPLOY_CREDENTIALS": str(creds), "COMPANY_DEPLOY_APPROVER_PASSPHRASE": "handoff verify passphrase",
                    "PLATFORM_API_BASE_URL": BASE_URL}
    print(f"=== IT handoff verification against {BASE_URL} ===\n", flush=True)

    code, out = _cli(["approver", "keygen", "--key", str(key_file)], approver_env)
    line = next((l.strip().split("=", 1)[1] for l in out.splitlines() if "PLATFORM_APPROVER_KEYS=" in l), "")
    if not _check(code == 0 and line, "an approver key is made"):
        print(out)
        return 1
    _restart({"PLATFORM_APPROVER_KEYS": line})

    name = f"handoff-{suffix}"
    app_id = None
    project = f"handoffverify{suffix}"
    extract = tmp / "extract"
    try:
        _, deps = call(BASE_URL, "GET", "/departments", token=owner_key)
        dept = deps["departments"][0].get("ID") or deps["departments"][0]["id"]
        _, app = call(BASE_URL, "POST", "/applications", {"name": name, "description": "IT handoff verification",
                                                          "owning_department_id": dept, "deployment_yaml_draft": _yaml(name)}, token=owner_key)
        app_id = app["id"]
        call(BASE_URL, "PUT", f"/applications/{app_id}/profile", {
            "purpose": "IT handoff verification", "handles_personal_data": False,
            "frontend": "react", "backend": "go", "database": "mysql"}, token=owner_key)
        _, v = call(BASE_URL, "POST", f"/applications/{app_id}/validate", token=owner_key)
        _check(v.get("report", {}).get("valid") is True, "react + go + mysql validates")
        status, _ = call(BASE_URL, "PUT", f"/applications/{app_id}/secrets/PARTNER_TOKEN", {"value": SECRET_VALUE}, token=owner_key)
        _check(status in (200, 201), f"a setting of the application's own is set ({status})")
        started = time.time()
        req = urllib.request.Request(f"{BASE_URL}/applications/{app_id}/build", data=_source_archive(), method="POST",
                                     headers={"Authorization": f"Bearer {owner_key}", "Content-Type": "application/gzip"})
        build = json.loads(urllib.request.urlopen(req, timeout=1200).read())
        if not _check(str(build.get("status")).lower() == "succeeded", f"the templates build ({time.time() - started:.0f}s)"):
            print(json.dumps(build, indent=2)[:2000])
            return 1

        _, dep = call(BASE_URL, "POST", f"/applications/{app_id}/deploy",
                      {"environment": "production", "test_report": "go test ./... ok",
                       "review_document": (TEMPLATES / "REVIEW.md").read_text(encoding="utf-8")}, token=owner_key)
        if not _check(dep.get("status") == "pending_approval", f"production waits for approval ({dep.get('status')})"):
            print(json.dumps(dep, indent=2)[:1500])
            return 1
        status, body = call(BASE_URL, "GET", "/handoffs", token=approver)
        _check(status == 200 and not any(e["application_id"] == app_id for e in body.get("handoffs", [])),
               "nothing is handed off before it goes live")
        code, out = _cli(["approve", dep["id"], "--yes", "--reason", "handoff verify", "--key", str(key_file)], approver_env)
        _check(code == 0 and "now running" in out, "the approver signs and it goes live")

        entry = _wait_handoff(approver, app_id, lambda p: True)
        first = entry.get("ready") or {}
        if not _check(first.get("trigger") == "promotion" and first.get("deployment_id") == dep["id"],
                      f"going live packs it by itself ({first.get('trigger')}, {entry.get('latest')})"):
            return 1
        status, _ = call(BASE_URL, "GET", "/handoffs", token=owner_key)
        _check(status == 403, f"an owner who is neither administrator nor approver can't see packages ({status})")
        status, _ = call(BASE_URL, "POST", f"/applications/{app_id}/handoffs", token=owner_key)
        _check(status == 403, f"nor make one ({status})")

        # Data put in after the promotion, then the package made again.
        marker = f"handed-to-it-{suffix}"
        deadline = time.time() + 120
        while time.time() < deadline:
            status, body = _get(f"{BASE_URL}/run/{name}/api/")
            if status == 200 and json.loads(body).get("database") == "connected":
                break
            time.sleep(2)
        req = urllib.request.Request(f"{BASE_URL}/run/{name}/api/items", data=json.dumps({"title": marker}).encode(),
                                     method="POST", headers={"Content-Type": "application/json"})
        _check(urllib.request.urlopen(req, timeout=30).status == 201, "an item is saved in production")
        status, body = call(BASE_URL, "POST", f"/applications/{app_id}/handoffs", token=approver)
        _check(status == 202 and body["handoff"]["status"] == "building", f"the approver makes it again ({status})")
        wanted = body["handoff"]["id"]
        status, again = call(BASE_URL, "POST", f"/applications/{app_id}/handoffs", token=approver)
        if status == 202:  # the first had already finished: this one is the newest
            wanted = again["handoff"]["id"]
            print("      (the package was made before a second request could find it in progress)")
        else:
            _check(status == 409 and again["error"]["code"] == "in_progress", f"not twice at once ({status})")
        entry = _wait_handoff(approver, app_id, lambda p: p["id"] == wanted)
        pkg = entry.get("ready") or {}
        if not _check(pkg.get("id") == wanted and pkg.get("trigger") == "manual",
                      f"the new package is ready and is the one offered ({pkg.get('trigger')}, {entry.get('latest')})"):
            return 1
        row = subprocess.run(["docker", "compose", "exec", "-T", "postgres", "psql", "-U", "postgres", "-d", "platform", "-tAc",
                              f"select status from handoff_packages where id = '{first['id']}'"],
                             cwd=REPO, capture_output=True, text=True).stdout.strip()
        _check(row == "replaced", f"the first package is replaced ({row})")
        _check(not _volume_has("self-deployment_agent_platform-handoffs", f"{app_id}/{first['id']}.tar")
               and _volume_has("self-deployment_agent_platform-handoffs", f"{app_id}/{pkg['id']}.tar"),
               "with its file gone; the new one's is there")

        # Download.
        req = urllib.request.Request(f"{BASE_URL}/handoffs/{pkg['id']}/file", headers={"Authorization": f"Bearer {approver}"})
        with urllib.request.urlopen(req, timeout=600) as r:
            data = r.read()
            disposition = r.headers.get("Content-Disposition", "")
        _check(len(data) == pkg["size_bytes"] and hashlib.sha256(data).hexdigest() == pkg["sha256"],
               f"the download is the recorded size and SHA-256 ({len(data)} bytes)")
        _check(pkg["file_name"] in disposition and pkg["file_name"].startswith(f"{name}-handoff-"), f"named {pkg['file_name']}")
        _, audit = call(BASE_URL, "GET", f"/audit-log?resource_type=application&resource_id={app_id}", token=approver)
        actions = [e.get("action") for e in audit.get("entries", [])]
        _check("handoff.download" in actions and "handoff.build" in actions, f"the build and the download are audited ({actions[:4]})")
        _check(SECRET_VALUE.encode() not in data, "the setting's value is nowhere in the package")

        extract.mkdir()
        with tarfile.open(fileobj=io.BytesIO(data)) as tar:
            tar.extractall(extract, filter="data")
        root = extract / f"{name}-handoff"
        sums = dict(reversed(l.split("  ", 1)) for l in (root / "SHA256SUMS").read_text().splitlines())
        files = [p for p in root.rglob("*") if p.is_file() and p.name != "SHA256SUMS"]
        bad = [str(p.relative_to(root)) for p in files
               if sums.get(p.relative_to(root).as_posix()) != hashlib.sha256(p.read_bytes()).hexdigest()]
        _check(not bad and len(files) == len(sums), f"every file matches SHA256SUMS ({len(files)} files, bad: {bad[:3]})")
        for want in ("README.md", "docker-compose.yml", ".env.example", "proxy/default.conf", "images/images.tar",
                     "database/01-data.sql", "source/api/main.go", "source/api/Dockerfile", "source/frontend/Dockerfile"):
            _check((root / want).is_file(), f"has {want}")
        env_example = (root / ".env.example").read_text()
        _check("PARTNER_TOKEN=\n" in env_example and "DATABASE_PASSWORD=\n" in env_example,
               "the setting is named in .env.example, empty")
        _check(marker in (root / "database" / "01-data.sql").read_text(errors="replace"), "the dump has the item saved in production")
        readme = (root / "README.md").read_text()
        _check(dep["id"] in readme and owner_email in readme, "the README names the deployment and the owner")

        # Run it the way IT would: the package alone.
        r = subprocess.run(["docker", "load", "-i", str(root / "images" / "images.tar")], capture_output=True, text=True)
        _check(r.returncode == 0 and f"platform-build/{name}-api" in r.stdout and f"platform-build/{name}-frontend" in r.stdout,
               f"docker load reads the images ({r.stdout.strip()[:160]})")
        port = _free_port()
        env_text = env_example.replace("HTTP_PORT=8080", f"HTTP_PORT={port}")
        env_text = env_text.replace("DATABASE_PASSWORD=\n", f"DATABASE_PASSWORD=it{suffix}pw\n").replace("PARTNER_TOKEN=\n", "PARTNER_TOKEN=x\n")
        (root / ".env").write_text(env_text)
        up = subprocess.run(["docker", "compose", "-p", project, "up", "-d"], cwd=root, capture_output=True, text=True, timeout=900)
        _check(up.returncode == 0, f"docker compose up starts it ({up.stderr.strip()[-300:]})")
        health = {}
        deadline = time.time() + 600
        while time.time() < deadline:
            status, body = _get(f"http://127.0.0.1:{port}/api/")
            if status == 200:
                health = json.loads(body)
                if health.get("database") == "connected":
                    break
            time.sleep(3)
        _check(health.get("database") == "connected", f"/api/ answers, connected to the imported database ({health})")
        status, body = _get(f"http://127.0.0.1:{port}/api/items")
        titles = [i.get("title") for i in json.loads(body)] if status == 200 else []
        _check(marker in titles, f"with the item saved in production ({status}, {titles[:3]})")
        status, html = _get(f"http://127.0.0.1:{port}/")
        page = html.decode(errors="replace")
        script = page.split('src="./', 1)[1].split('"', 1)[0] if 'src="./' in page else ""
        _check(status == 200 and script.startswith("assets/"), f"/ is the React page ({status}, {script or 'no bundle'})")
        if script:
            status, js = _get(f"http://127.0.0.1:{port}/{script}")
            _check(status == 200 and len(js) > 10_000, f"and its bundle loads ({status}, {len(js)} bytes)")
        status, _ = _get(f"http://127.0.0.1:{port}/api/items/999999999")
        _check(status == 404, f"the API's own 404 comes through the proxy ({status})")
    finally:
        if extract.exists():
            subprocess.run(["docker", "compose", "-p", project, "down", "-v"], cwd=extract / f"{name}-handoff",
                           capture_output=True, timeout=300)
        if app_id:
            call(BASE_URL, "POST", f"/applications/{app_id}/suspend", token=owner_key)
            call(BASE_URL, "POST", f"/applications/{app_id}/archive", token=owner_key)
            status, _ = call(BASE_URL, "POST", f"/applications/{app_id}/delete", {"confirm": True}, token=owner_key)
            _check(status == 200 and not _volume_has("self-deployment_agent_platform-handoffs", app_id),
                   f"deleting the application deletes its packages ({status})")
        _restart({})

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
