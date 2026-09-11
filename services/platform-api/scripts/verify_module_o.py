"""End-to-end verification of Module O (Secret Management) against a live
stack and a real Docker daemon.

    docker compose up -d --build      # from repo root, with PLATFORM_SECRET_KEY in .env
    python services/platform-api/scripts/verify_module_o.py [--legacy-app NAME]

Nothing here is mocked, and the checks are about what an attacker or an
accident would actually see, not about what the code says it does:

  FR-066  a submitted value appears nowhere in a full dump of the
          platform's database — only ciphertext does
  FR-067  the value reaches the running application (which reports only a
          hash of it) and appears nowhere in the built image at rest; a
          replaced value takes effect at the next start, not before
  FR-069  another employee can't list, set or delete the secret; and a
          ciphertext copied straight into another application's row — the
          database-level attack — gets that application a failed start,
          not the secret
  FR-070  no endpoint returns a value; the audit trail records set and
          delete, and its CSV export contains no value
  FR-067  exception flow: restart the platform with a different
          PLATFORM_SECRET_KEY and applications fail closed — they are not
          started without their secrets — then recover once the key is
          restored

--legacy-app NAME also checks the upgrade path: an application deployed
with a database BEFORE Module O existed, so its password was stored in
plaintext. After the upgrade no plaintext password may be left anywhere in
the platform database, and the application must still reach its database
with the password decrypted from the new store. To create one, deploy an
application with `database: type: postgres` on a build from before this
module, then upgrade. Without the flag those checks are skipped (and say
so), and a throwaway application plays the victim in the cross-application
check instead.

Restarts platform-api twice for the key-change check (skip it with
--skip-key-change) and always restores the original key, even on failure.
Never prints a secret value.
"""

from __future__ import annotations

import argparse
import base64
import hashlib
import json
import os
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.error
import urllib.request
import uuid
from pathlib import Path

HERE = Path(__file__).resolve().parent
REPO_ROOT = HERE.parents[2]
sys.path.insert(0, str(HERE))
from verify_module_n import _app_containers, _docker, _inspect, _psql  # noqa: E402

BASE_URL = os.environ.get("PLATFORM_API_BASE_URL", "http://localhost:8099")
OWNER = "module-o-verify@sti-th.com"
INTRUDER = "module-o-intruder@sti-th.com"
LEGACY_OWNER = "module-n-verify@sti-th.com"

_failures: list[str] = []


def _check(cond, msg: str) -> bool:
    print(("OK:   " if cond else "FAIL: ") + msg)
    if not cond:
        _failures.append(msg)
    return bool(cond)


def _call(method: str, path: str, body=None, raw: bytes | None = None, as_email: str = OWNER):
    data = raw if raw is not None else (json.dumps(body).encode() if body is not None else None)
    req = urllib.request.Request(BASE_URL + path, data=data, method=method)
    req.add_header("X-Dev-User-Email", as_email)
    req.add_header("X-Dev-User-Name", as_email.split("@")[0])
    req.add_header("X-Dev-Department", "IT")
    if raw is not None:
        req.add_header("Content-Type", "application/gzip")
    elif data is not None:
        req.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(req, timeout=300) as resp:
            status, payload = resp.status, resp.read()
    except urllib.error.HTTPError as e:
        status, payload = e.code, e.read()
    text = payload.decode("utf-8", "replace")
    try:
        parsed = json.loads(text) if text else {}
    except ValueError:
        parsed = None
    return status, parsed, text


def _api(method: str, path: str, body=None, raw: bytes | None = None, as_email: str = OWNER,
         expect=(200, 201, 204)):
    status, parsed, text = _call(method, path, body, raw, as_email)
    if status not in expect:
        raise SystemExit(f"{method} {path} -> {status}: {text[:600]}")
    return parsed


# The deployed application reports whether a secret reached it and a hash
# of it — never the value — so the check can compare without the value
# ever crossing HTTP or landing in a log.
_APP_SOURCE = r'''package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"strings"
)

func main() {
	http.HandleFunc("/secretcheck/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/secretcheck/")
		value, present := os.LookupEnv(name)
		out := map[string]any{"name": name, "present": present}
		if present {
			sum := sha256.Sum256([]byte(value))
			out["sha256"] = hex.EncodeToString(sum[:])
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(out)
	})
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("module-o verifier app\n"))
	})
	http.ListenAndServe(":8080", nil)
}
'''


def _source_archive() -> bytes:
    with tempfile.TemporaryDirectory() as tmp:
        api = Path(tmp) / "api"
        api.mkdir()
        (api / "main.go").write_text(_APP_SOURCE)
        (api / "go.mod").write_text("module secretverify\n\ngo 1.25\n")
        archive = Path(tmp) / "src.tar.gz"
        with tarfile.open(archive, "w:gz") as tar:
            tar.add(api, arcname="api")
        return archive.read_bytes()


def _register_and_build(dept_id: str, name: str) -> tuple[str, str]:
    """Pinned (scaling.min: 1), so restart really does start a new container."""
    yaml = (f"app:\n  name: {name}\n  owner: IT\n"
            "services:\n  api:\n    runtime: go\n    port: 8080\n"
            "scaling:\n  min: 1\n")
    app = _api("POST", "/applications", {
        "name": name, "description": "Module O end-to-end verification",
        "owning_department_id": dept_id, "deployment_yaml_draft": yaml,
    })
    app_id = app["id"]
    _api("PUT", f"/applications/{app_id}/deployment-yaml", {"deployment_yaml": yaml})
    if not _api("POST", f"/applications/{app_id}/validate").get("report", {}).get("valid"):
        raise SystemExit(f"{name}: validation failed")
    build = _api("POST", f"/applications/{app_id}/build", raw=_source_archive())
    if str(build.get("status")).lower() != "succeeded":
        raise SystemExit(f"{name}: build failed: {json.dumps(build)[:800]}")
    return app_id, build["image_refs"]["api"]


def _deploy(app_id: str, as_email: str = OWNER) -> None:
    d = _api("POST", f"/applications/{app_id}/deploy", {"environment": "dev"}, as_email=as_email)
    if str(d.get("status")).lower() != "running":
        raise SystemExit(f"deploy did not reach running: {d.get('status')} / {d.get('failure_reason')}")


def _secretcheck(app_name: str, name: str = "API_KEY") -> dict:
    url = f"{BASE_URL}/run/{app_name}/api/secretcheck/{name}"
    last = ""
    for _ in range(30):
        try:
            with urllib.request.urlopen(url, timeout=60) as resp:
                return json.loads(resp.read())
        except Exception as e:  # a just-restarted container may need a moment
            last = str(e)
            time.sleep(2)
    return {"error": last}


def _sha(value: str) -> str:
    return hashlib.sha256(value.encode()).hexdigest()


def _running(app_name: str) -> str | None:
    names = _app_containers(app_name)
    return names[0] if len(names) == 1 else None


def _container_id(name: str | None) -> str | None:
    return _inspect(name, "{{.Id}}") if name else None


def _container_env(name: str) -> dict:
    return dict(e.split("=", 1) for e in json.loads(_inspect(name, "{{json .Config.Env}}")) if "=" in e)


def _platform_pg() -> tuple[str, str, str]:
    pg = _docker("compose", "ps", "-q", "postgres")
    env = _container_env(pg)
    return pg, env.get("POSTGRES_USER", "postgres"), env.get("POSTGRES_DB", "platform")


def _platform_sql(sql: str) -> str:
    pg, user, db = _platform_pg()
    r = subprocess.run(["docker", "exec", "-i", pg, "psql", "-U", user, "-d", db, "-tAc", sql],
                       capture_output=True, text=True)
    if r.returncode != 0:
        raise SystemExit(f"platform SQL failed: {r.stderr.strip()}")
    return r.stdout.strip()


def _platform_dump() -> bytes:
    pg, user, db = _platform_pg()
    r = subprocess.run(["docker", "exec", pg, "pg_dump", "-U", user, db], capture_output=True)
    if r.returncode != 0:
        raise SystemExit(f"pg_dump failed: {r.stderr.decode(errors='replace').strip()}")
    return r.stdout


def _absent_from(blob: bytes, value: str) -> bool:
    """Neither as text, nor as the hex pg_dump would render a bytea as."""
    return value.encode() not in blob and value.encode().hex().encode() not in blob


def _audit_entries(app_id: str, as_email: str = OWNER) -> list[dict]:
    body = _api("GET", f"/audit-log?resource_id={app_id}", as_email=as_email)
    if isinstance(body, list):
        return body
    return next((v for v in body.values() if isinstance(v, list)), [])


def _audit_has(entries: list[dict], action: str, outcome: str, detail_contains: str) -> bool:
    return any(e.get("action") == action and e.get("outcome") == outcome
               and detail_contains in (e.get("detail") or "") for e in entries)


def _recreate_platform_api(secret_key: str | None) -> None:
    """None means "whatever .env says" — the original key."""
    env = os.environ.copy()
    env.pop("PLATFORM_SECRET_KEY", None)
    if secret_key is not None:
        env["PLATFORM_SECRET_KEY"] = secret_key
    r = subprocess.run(["docker", "compose", "up", "-d", "--force-recreate", "--no-deps", "platform-api"],
                       env=env, capture_output=True, text=True, cwd=REPO_ROOT)
    if r.returncode != 0:
        raise SystemExit(f"recreating platform-api failed: {r.stderr.strip()}")
    deadline = time.time() + 90
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(BASE_URL + "/healthz", timeout=5) as resp:
                if resp.status == 200:
                    return
        except Exception:
            pass
        time.sleep(1)
    raise SystemExit("platform-api did not become healthy after being recreated")


def _key_id() -> str | None:
    logs = _docker("compose", "logs", "--no-log-prefix", "--tail", "80", "platform-api")
    ids = [line.rsplit(" ", 1)[-1] for line in logs.splitlines() if "sealing with key" in line]
    return ids[-1] if ids else None


def _find_app(name: str, as_email: str) -> str:
    body = _api("GET", "/applications?limit=100", as_email=as_email)
    items = body if isinstance(body, list) else next((v for v in body.values() if isinstance(v, list)), [])
    for a in items:
        if a.get("name") == name:
            return a["id"]
    raise SystemExit(f"application {name!r} not found for {as_email}")


def _teardown(app_id: str, as_email: str) -> None:
    _call("POST", f"/applications/{app_id}/suspend", as_email=as_email)
    _api("POST", f"/applications/{app_id}/archive", as_email=as_email)
    _api("POST", f"/applications/{app_id}/delete", {"confirm": True}, as_email=as_email)


def _legacy_checks(legacy: str, legacy_id: str) -> None:
    print("--- upgrade path: a database provisioned before Module O existed ---")
    db_container = _docker("ps", "--filter", f"name=platform-db-{legacy}-", "--format", "{{.Names}}")
    if not _check(db_container, f"the legacy application's database is still running ({db_container})"):
        return
    real_pw = _container_env(db_container)["POSTGRES_PASSWORD"]
    private_net = next(iter(json.loads(_inspect(db_container, "{{json .NetworkSettings.Networks}}"))))

    left = _platform_sql("SELECT count(*) FROM provisioned_databases WHERE password IS NOT NULL")
    _check(left == "0", f"no plaintext password is left in provisioned_databases ({left} row(s))")
    row = _platform_sql("SELECT managed_by || ',' || octet_length(ciphertext) FROM application_secrets "
                        f"WHERE application_id = '{legacy_id}' AND name = 'DATABASE_PASSWORD'")
    _check(row.startswith("platform,"), f"its password now lives in the secret store, platform-managed ({row or 'missing'})")
    _check(_absent_from(_platform_dump(), real_pw),
           "the old plaintext password appears nowhere in a full dump of the platform database")

    status, listing, text = _call("GET", f"/applications/{legacy_id}/secrets", as_email=LEGACY_OWNER)
    managed = {s["name"]: s["managed_by"] for s in (listing or {}).get("secrets", [])}
    _check(status == 200 and managed.get("DATABASE_PASSWORD") == "platform",
           "the owner can see DATABASE_PASSWORD exists, as platform-managed")
    _check(real_pw not in text, "...and cannot see its value")

    before = _container_id(_running(legacy))
    _api("POST", f"/applications/{legacy_id}/restart", as_email=LEGACY_OWNER)
    after = _running(legacy)
    _check(after and _container_id(after) != before, "restart started a brand-new container")
    injected = _container_env(after).get("DATABASE_PASSWORD") if after else None
    _check(injected == real_pw, "the password decrypted from the store is exactly the database's real one")
    auth = _psql(private_net, db_container, "appuser", "appdb", injected or "", "SELECT 1")
    _check(auth.returncode == 0 and auth.stdout.strip() == "1", "...and it authenticates against the database")

    status, _, _ = _call("DELETE", f"/applications/{legacy_id}/secrets/DATABASE_PASSWORD", as_email=LEGACY_OWNER)
    _check(status == 409, f"the owner cannot delete the platform-managed password (HTTP {status})")
    status, _, text = _call("PUT", f"/applications/{legacy_id}/secrets/DATABASE_URL",
                            {"value": "postgres://someone-else"}, as_email=LEGACY_OWNER)
    _check(status == 400 and "reserved_secret_name" in text,
           f"nor shadow the platform's own DATABASE_URL with a secret of that name (HTTP {status})")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--legacy-app", help="name of an application deployed with a database before Module O")
    parser.add_argument("--skip-key-change", action="store_true", help="don't restart platform-api with a wrong key")
    args = parser.parse_args()
    os.chdir(REPO_ROOT)

    suffix = uuid.uuid4().hex[:6]
    dept = _api("GET", "/departments")["departments"][0]
    dept_id = dept.get("ID") or dept["id"]
    token1, token2, token3 = (f"sk-verify-{uuid.uuid4().hex}" for _ in range(3))
    print(f"=== Module O verification against {BASE_URL} ===\n")

    legacy_id = _find_app(args.legacy_app, LEGACY_OWNER) if args.legacy_app else None
    if legacy_id:
        _legacy_checks(args.legacy_app, legacy_id)
    else:
        print("SKIP: upgrade-path checks (no --legacy-app given; see the module docstring)")

    print("\n--- FR-066: submit a secret before the first deploy ---")
    app_name = f"secrets{suffix}"
    app_id, image = _register_and_build(dept_id, app_name)
    status, body, text = _call("PUT", f"/applications/{app_id}/secrets/API_KEY", {"value": token1})
    _check(status == 200 and body.get("version") == 1 and body.get("managed_by") == "employee",
           f"the owner sets API_KEY (HTTP {status}, version {body.get('version') if body else None})")
    _check("value" not in (body or {}) and token1 not in text, "the response does not echo the value back")
    status, listing, text = _call("GET", f"/applications/{app_id}/secrets")
    _check(status == 200 and [s["name"] for s in listing["secrets"]] == ["API_KEY"] and token1 not in text,
           "listing shows the name and version, never the value")

    for label, method, payload in (("list", "GET", None), ("set", "PUT", {"value": "intruder-value"}),
                                   ("delete", "DELETE", None)):
        path = f"/applications/{app_id}/secrets" + ("" if method == "GET" else "/API_KEY")
        status, _, _ = _call(method, path, payload, as_email=INTRUDER)
        _check(status == 403, f"FR-069: another employee cannot {label} it (HTTP {status})")
    for label, name, payload, code in (
            ("an invalid name", "api-key", {"value": "x"}, "invalid_secret_name"),
            ("a reserved name", "DATABASE_URL", {"value": "x"}, "reserved_secret_name"),
            ("an empty value", "OTHER_KEY", {"value": ""}, "invalid_secret_value"),
            ("a body without a value", "OTHER_KEY", {}, "invalid_body")):
        status, _, text = _call("PUT", f"/applications/{app_id}/secrets/{name}", payload)
        _check(status == 400 and code in text, f"{label} is rejected ({code}, HTTP {status})")

    print("\n--- FR-067: injected at start, and nowhere at rest ---")
    _deploy(app_id)
    probe = _secretcheck(app_name)
    _check(probe.get("present") is True and probe.get("sha256") == _sha(token1),
           f"the running application received exactly the submitted value ({probe.get('error') or 'hash matches'})")
    image_env = json.dumps(json.loads(_inspect(image, "{{json .Config.Env}}")))
    _check(token1 not in image_env, "the built image's own environment does not contain it")
    saved = subprocess.run(["docker", "save", image], capture_output=True).stdout
    _check(len(saved) > 0 and _absent_from(saved, token1),
           f"nor does any layer of the image ({len(saved) // 1024} KiB searched)")
    _check(_absent_from(_platform_dump(), token1), "FR-066: nor does a full dump of the platform database")

    print("\n--- replacing a value: takes effect at the next start ---")
    status, body, _ = _call("PUT", f"/applications/{app_id}/secrets/API_KEY", {"value": token2})
    _check(status == 200 and body.get("version") == 2, f"replacing it bumps the version to {body.get('version')}")
    _check(_secretcheck(app_name).get("sha256") == _sha(token1), "the running container still has the old value")
    _api("POST", f"/applications/{app_id}/restart")
    _check(_secretcheck(app_name).get("sha256") == _sha(token2), "after a restart it has the new one")

    print("\n--- FR-069 at the database layer: a ciphertext moved to another application ---")
    if legacy_id:
        victim, victim_id, victim_owner, made_victim = args.legacy_app, legacy_id, LEGACY_OWNER, False
    else:
        victim = f"victim{suffix}"
        victim_id, _ = _register_and_build(dept_id, victim)
        _deploy(victim_id)
        victim_owner, made_victim = OWNER, True
    _platform_sql(
        "INSERT INTO application_secrets (application_id, name, managed_by, ciphertext, key_id, created_by, updated_by) "
        f"SELECT '{victim_id}', name, managed_by, ciphertext, key_id, created_by, updated_by "
        f"FROM application_secrets WHERE application_id = '{app_id}' AND name = 'API_KEY'")
    before = _container_id(_running(victim))
    status, _, _ = _call("POST", f"/applications/{victim_id}/restart", as_email=victim_owner)
    after_name = _running(victim)
    _check(status >= 400, f"restarting the application the ciphertext was planted in fails (HTTP {status})")
    _check(_container_id(after_name) == before, "...before stopping anything: its existing container is still running")
    _check(after_name and token2 not in json.dumps(_container_env(after_name)), "...and it never received the value")
    _check(_audit_has(_audit_entries(victim_id, victim_owner), "application.restart", "failure", "could not be decrypted"),
           "the failed restart is in the audit trail, with the reason")
    _platform_sql(f"DELETE FROM application_secrets WHERE application_id = '{victim_id}' AND name = 'API_KEY'")
    _api("POST", f"/applications/{victim_id}/restart", as_email=victim_owner)
    _check(_container_id(_running(victim)) != before, "with the planted row removed, the same restart succeeds")

    print("\n--- FR-070: the audit trail ---")
    entries = _audit_entries(app_id)
    _check(sum(1 for e in entries if e.get("action") == "secret.set") == 2, "both sets are audited")
    status, _, csv_text = _call("GET", f"/audit-log/export?resource_id={app_id}")
    _check(status == 200 and "secret.set" in csv_text and token1 not in csv_text and token2 not in csv_text,
           "the CSV export records them without either value")

    print("\n--- deleting a secret ---")
    status, _, _ = _call("DELETE", f"/applications/{app_id}/secrets/API_KEY")
    _check(status == 204, f"the owner deletes it (HTTP {status})")
    _api("POST", f"/applications/{app_id}/restart")
    _check(_secretcheck(app_name).get("present") is False, "the next container no longer has it")
    _check(_audit_has(_audit_entries(app_id), "secret.delete", "success", "API_KEY"), "the deletion is audited")

    # Checked before the key-change step, which recreates platform-api and
    # takes the logs of every request so far with it.
    logs = _docker("compose", "logs", "--no-log-prefix", "platform-api")
    _check(logs and token1 not in logs and token2 not in logs,
           f"FR-070: neither value appears in platform-api's own logs ({len(logs.splitlines())} lines searched)")

    if args.skip_key_change:
        print("\nSKIP: key-change check (--skip-key-change)")
    else:
        print("\n--- FR-067 exception flow: the platform's key changes ---")
        _api("PUT", f"/applications/{app_id}/secrets/API_KEY", {"value": token3})
        _api("POST", f"/applications/{app_id}/restart")
        original_key_id = _key_id()
        running_before = _container_id(_running(app_name))
        try:
            _recreate_platform_api(base64.b64encode(os.urandom(32)).decode())
            _check(_key_id() not in (None, original_key_id), "platform-api is now running with a different key")
            status, _, _ = _call("POST", f"/applications/{app_id}/restart")
            _check(status >= 400, f"restarting an application with a secret fails (HTTP {status})")
            _check(_container_id(_running(app_name)) == running_before,
                   "...closed and without an outage: the running container was left alone")
            _check(_audit_has(_audit_entries(app_id), "application.restart", "failure", "different platform key"),
                   "the audit trail says why: a different platform key")
        finally:
            _recreate_platform_api(None)
        _check(_key_id() == original_key_id, "the original key is restored")
        _api("POST", f"/applications/{app_id}/restart")
        _check(_secretcheck(app_name).get("sha256") == _sha(token3), "and the application starts with its secret again")

    print("\n--- FR-050: deleting the application purges its secrets ---")
    doomed = [(app_id, OWNER)] + ([(victim_id, OWNER)] if made_victim else []) \
        + ([(legacy_id, LEGACY_OWNER)] if legacy_id else [])
    for doomed_id, owner in doomed:
        _teardown(doomed_id, owner)
    ids = ",".join(f"'{d}'" for d, _ in doomed)
    left = _platform_sql(f"SELECT count(*) FROM application_secrets WHERE application_id IN ({ids})")
    _check(left == "0", f"no secret of a deleted application is left in the store ({left} row(s))")

    print()
    if _failures:
        print(f"=== {len(_failures)} CHECK(S) FAILED ===")
        for f in _failures:
            print("  - " + f)
        return 1
    print("=== all Module O checks passed ===")
    return 0


if __name__ == "__main__":
    sys.exit(main())
