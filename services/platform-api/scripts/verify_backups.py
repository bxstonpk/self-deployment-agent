"""End-to-end verification of database backups (FR-064, DEC-025) against a
live platform-api and real MySQL and Postgres databases.

    docker compose up -d --build      # from repo root, with .env present
    python services/platform-api/scripts/verify_backups.py

For each engine it deploys an application with a database, writes real
rows into it from a client on the application's private network, and
checks:

  - the scheduler takes a backup by itself (platform-api restarted after
    the deploy; the first backup of a database is always a full)
  - a backup file exists in the platform-backups volume, and the rows'
    contents appear nowhere in it (sealed)
  - after a change, the next backup is a differential against that full,
    and far smaller than it
  - restore needs the application's exact name; restoring the
    differential brings back exactly the data at that point after the
    table was dropped, restoring the full brings back the earlier data,
    and each restore first takes a pre-restore backup
  - a new full retires the previous set: records and files
  - deleting the application deletes its backups

and, once: a deployment.yaml with `database: backup: monthly` fails
validation, and one with `backup: none` is not backed up by the scheduler.

Set PLATFORM_API_BASE_URL if the API is not on the .env default port.
"""

from __future__ import annotations

import io
import json
import os
import subprocess
import sys
import tarfile
import time
import urllib.error
import urllib.request
import uuid
from pathlib import Path

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from platform_auth import api_key_for  # noqa: E402

BASE_URL = os.environ.get("PLATFORM_API_BASE_URL", "http://localhost:8099")
TEMPLATES = Path(__file__).resolve().parents[3] / "company-deployment-skill" / "templates"
_failures: list[str] = []
_key = ""


def _check(cond: bool, msg: str) -> bool:
    print(("OK:   " if cond else "FAIL: ") + msg, flush=True)
    if not cond:
        _failures.append(msg)
    return cond


def _api(method, path, body=None, raw=None, expect=(200, 201)):
    data = raw if raw is not None else (json.dumps(body).encode() if body is not None else None)
    req = urllib.request.Request(BASE_URL + path, data=data, method=method)
    req.add_header("Authorization", f"Bearer {_key}")
    req.add_header("Content-Type", "application/gzip" if raw is not None else "application/json")
    try:
        with urllib.request.urlopen(req, timeout=900) as resp:
            payload, status = resp.read(), resp.status
    except urllib.error.HTTPError as e:
        payload, status = e.read(), e.code
    parsed = json.loads(payload) if payload else {}
    if expect and status not in expect:
        raise SystemExit(f"{method} {path} -> {status}: {json.dumps(parsed)[:600]}")
    return status, parsed


def _docker(*args, check=True, stdin=None):
    r = subprocess.run(["docker", *args], capture_output=True, text=True, input=stdin)
    if check and r.returncode != 0:
        raise SystemExit(f"docker {' '.join(args[:3])} failed: {r.stderr.strip()[:400]}")
    return r.stdout.strip()


def _archive() -> bytes:
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w:gz") as tar:
        root = TEMPLATES / "api-go"
        for p in sorted(root.rglob("*")):
            if p.is_file():
                tar.add(p, arcname=f"api/{p.relative_to(root).as_posix()}")
    return buf.getvalue()


def _yaml(name, engine, backup=None):
    extra = f"  backup: {backup}\n" if backup else ""
    return (f"app:\n  name: {name}\n  owner: IT\nservices:\n  api:\n    runtime: go\n    port: 8080\n"
            f"database:\n  type: {engine}\n{extra}scaling:\n  min: 1\n  max: 1\n")


def _deploy(dept, name, engine, backup=None):
    _, app = _api("POST", "/applications", {"name": name, "description": "backup verification",
                                            "owning_department_id": dept, "deployment_yaml_draft": _yaml(name, engine, backup)})
    _api("PUT", f"/applications/{app['id']}/profile", {"purpose": "backup verification", "handles_personal_data": False})
    _, v = _api("POST", f"/applications/{app['id']}/validate")
    if not v["report"]["valid"]:
        raise SystemExit(f"validation failed: {v}")
    _api("POST", f"/applications/{app['id']}/build", raw=_archive())
    _, d = _api("POST", f"/applications/{app['id']}/deploy", {"environment": "dev"})
    if d.get("status") != "running":
        raise SystemExit(f"deploy failed: {d.get('failure_reason')}")
    return app["id"]


def _db(name):
    c = _docker("ps", "--filter", f"name=platform-db-{name}-", "--format", "{{.Names}}")
    net = next(iter(json.loads(_docker("inspect", "-f", "{{json .NetworkSettings.Networks}}", c))))
    return c, net


def _password(name):
    rows = _docker("ps", "--filter", "name=platform-run-", "--format", "{{.Names}}\t{{.Image}}")
    for row in rows.splitlines():
        cname, image = row.split("\t", 1)
        if image.startswith(f"platform-build/{name}-"):
            env = dict(e.split("=", 1) for e in json.loads(_docker("inspect", "-f", "{{json .Config.Env}}", cname)) if "=" in e)
            return env["DATABASE_PASSWORD"]
    raise SystemExit("no application container")


def _sql(engine, name, sql):
    """Run SQL from a client on the application's private network."""
    host, net = _db(name)
    pw = _password(name)
    if engine == "mysql":
        cmd = ["run", "--rm", "--network", net, "-e", f"MYSQL_PWD={pw}", "mysql:8.4", "mysql", "-h", host,
               "-u", "appuser", "-D", "appdb", "-N", "-B", "-e", sql]
    else:
        cmd = ["run", "--rm", "--network", net, "-e", f"PGPASSWORD={pw}", "postgres:16-alpine", "psql", "-h", host,
               "-U", "appuser", "-d", "appdb", "-tA", "-v", "ON_ERROR_STOP=1", "-c", sql]
    for _ in range(20):
        r = subprocess.run(["docker", *cmd], capture_output=True, text=True)
        if r.returncode == 0:
            return r.stdout.strip()
        time.sleep(2)
    raise SystemExit(f"sql failed: {r.stderr.strip()[:300]}")


def _rows(engine, name):
    return _sql(engine, name, "SELECT id, title FROM notes ORDER BY id").replace("|", "\t").splitlines()


def _in_backup_volume(script):
    """Look inside the backup volume from a throwaway container: the
    platform-api image has no shell."""
    volume = next(v for v in _docker("volume", "ls", "--format", "{{.Name}}").split() if v.endswith("platform-backups"))
    return _docker("run", "--rm", "-v", f"{volume}:/b:ro", "alpine:3.21", "sh", "-c", script)


def _backup_files(app_id):
    out = _in_backup_volume(f"ls /b/{app_id} 2>/dev/null || true")
    return sorted(f for f in out.split() if f)


def _restart_platform():
    _docker("compose", "restart", "platform-api")
    for _ in range(60):
        try:
            urllib.request.urlopen(BASE_URL + "/healthz", timeout=5)
            return
        except Exception:
            time.sleep(1)


def _wait_for_backup(app_id, timeout=120):
    deadline = time.time() + timeout
    while time.time() < deadline:
        _, out = _api("GET", f"/applications/{app_id}/backups")
        if out["backups"]:
            return out
        time.sleep(2)
    return out


def verify_engine(dept, engine, suffix):
    name = f"bk-{engine}-{suffix}"
    print(f"\n--- {engine} ---", flush=True)
    app_id = _deploy(dept, name, engine)
    create = ("CREATE TABLE notes (id INT PRIMARY KEY, title VARCHAR(100))" if engine == "mysql"
              else "CREATE TABLE notes (id INT PRIMARY KEY, title VARCHAR(100))")
    _sql(engine, name, create)
    _sql(engine, name, "INSERT INTO notes VALUES " + ",".join(f"({i},'payroll-row-{i}')" for i in range(1, 201)))

    _restart_platform()  # the scheduler runs at startup, and it's past the backup hour
    overview = _wait_for_backup(app_id)
    first = overview["backups"][0] if overview["backups"] else {}
    _check(first.get("trigger") == "scheduled" and first.get("kind") == "full" and first.get("status") == "succeeded",
           f"the scheduler backed the new database up by itself, as a full ({first.get('trigger')}, {first.get('kind')}, {first.get('status')})")
    _check(overview["policy"] == "weekly" and overview["schedule"]["full_weekday"] == "Sunday",
           f"the policy is weekly, full on {overview['schedule']['full_weekday']} at {overview['schedule']['hour']:02d}:00 {overview['schedule']['timezone']}")
    files = _backup_files(app_id)
    _check(files == [first.get("id", "") + ".bak"], f"its file is in the backup volume ({files})")
    leaked = _in_backup_volume(f"grep -c payroll-row /b/{app_id}/*.bak || true")
    _check(leaked.strip() in ("0", ""), "the rows' contents appear nowhere in the file (sealed)")

    _sql(engine, name, "UPDATE notes SET title='edited' WHERE id=3")
    _sql(engine, name, "INSERT INTO notes VALUES (201,'added-later')")
    state_at_diff = _rows(engine, name)
    _, nowb = _api("POST", f"/applications/{app_id}/backups", {})
    diff = nowb["backup"]
    _check(diff["kind"] == "differential" and diff["base_backup_id"] == first.get("id"),
           f"the next backup is a differential against that full ({diff['kind']})")
    _check(diff["stored_bytes"] * 3 < first.get("stored_bytes", 0),
           f"and far smaller: {diff['stored_bytes']} bytes against the full's {first.get('stored_bytes')}")

    _sql(engine, name, "DROP TABLE notes")
    status, _ = _api("POST", f"/applications/{app_id}/backups/{diff['id']}/restore", {"confirm": "wrong"}, expect=None)
    _check(status == 400, f"a restore without the exact application name is refused ({status})")
    status, restored = _api("POST", f"/applications/{app_id}/backups/{diff['id']}/restore", {"confirm": name}, expect=None)
    _check(status == 200 and restored["pre_restore_backup"]["trigger"] == "pre_restore",
           f"restoring the differential first backed up the current contents ({status})")
    _check(_rows(engine, name) == state_at_diff,
           f"the dropped table is back exactly as it was at the differential ({len(state_at_diff)} rows, row 3 edited)")

    _api("POST", f"/applications/{app_id}/backups/{first['id']}/restore", {"confirm": name})
    rows = _rows(engine, name)
    _check(len(rows) == 200 and rows[2].endswith("payroll-row-3"),
           f"restoring the full brings back the earlier data ({len(rows)} rows, row 3 original)")

    _, newfull = _api("POST", f"/applications/{app_id}/backups", {"kind": "full"})
    _, overview = _api("GET", f"/applications/{app_id}/backups")
    ids = [b["id"] for b in overview["backups"]]
    _check(ids == [newfull["backup"]["id"]], f"a new full retires the whole previous set ({len(ids)} backup(s) left)")
    _check(_backup_files(app_id) == [newfull["backup"]["id"] + ".bak"], "and its files")
    return app_id, name


def main() -> int:
    global _key
    _key = api_key_for(BASE_URL, "backup-verify@sti-th.com", "verify_backups.py")
    print(f"=== backup verification against {BASE_URL} ===", flush=True)
    _, deps = _api("GET", "/departments")
    dept = deps["departments"][0].get("ID") or deps["departments"][0]["id"]
    suffix = uuid.uuid4().hex[:6]

    print("\n--- policy ---")
    _, bad = _api("POST", "/applications", {"name": f"bk-bad-{suffix}", "description": "x", "owning_department_id": dept,
                                             "deployment_yaml_draft": _yaml(f"bk-bad-{suffix}", "mysql", "monthly")})
    _, v = _api("POST", f"/applications/{bad['id']}/validate")
    details = json.dumps(v["report"])
    _check(not v["report"]["valid"] and "database.backup must be" in details, "database.backup: monthly fails validation")
    _api("POST", f"/applications/{bad['id']}/delete", {"confirm": True}, expect=None)

    apps = []
    for engine in ("mysql", "postgres"):
        apps.append(verify_engine(dept, engine, suffix))

    print("\n--- opting out ---")
    optout = _deploy(dept, f"bk-none-{suffix}", "mysql", backup="none")
    _restart_platform()
    time.sleep(5)
    _, overview = _api("GET", f"/applications/{optout}/backups")
    _check(overview["policy"] == "none" and overview["backups"] == [],
           "an application with database.backup: none is not backed up by the scheduler")
    apps.append((optout, f"bk-none-{suffix}"))

    print("\n--- teardown ---")
    for app_id, _name in apps:
        _api("POST", f"/applications/{app_id}/suspend", expect=None)
        _api("POST", f"/applications/{app_id}/archive", expect=None)
        _api("POST", f"/applications/{app_id}/delete", {"confirm": True})
        _check(_backup_files(app_id) == [], f"deleting the application deleted its backups ({app_id[:8]})")

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
