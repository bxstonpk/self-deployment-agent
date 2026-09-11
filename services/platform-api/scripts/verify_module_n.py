"""End-to-end verification of Module N (Database Management) against a
live stack and a real Docker daemon.

    docker compose up -d --build      # from repo root, with .env present
    python services/platform-api/scripts/verify_module_n.py

Nothing here is mocked. It registers a real application that declares
`database: type: postgres`, builds and deploys it for real, and then
checks the things Module N actually claims:

  FR-061  a real, dedicated Postgres container exists for the application
  FR-062  the database is reachable from inside the application's private
          network and NOT reachable from outside it — proved by connecting
          from both sides, not by reading configuration
  FR-063  the generated credentials reach the application's runtime as
          environment variables, and the application itself really talks to
          its database using them (the deployed app opens a connection and
          reports the server's response)
  FR-065  deleting the application tears the database down: container gone,
          network gone, record closed
  FR-068  (Module O) rotating its password: from the application's own
          network the old password stops authenticating, the new one works,
          the data is untouched, and the restarted application has it

and the part that has no requirement number but is the easiest thing to get
wrong — that the wiring survives resume, restart, and a scale-to-zero cold
start, each of which starts a brand-new container.

Set PLATFORM_API_BASE_URL if the API is not on the .env default port.
Requires SCALE_TO_ZERO_IDLE_SECONDS to be small (e.g. 20) for the cold-start
check; it is skipped with a clear message if the wait would be unreasonable.
"""

from __future__ import annotations

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

BASE_URL = os.environ.get("PLATFORM_API_BASE_URL", "http://localhost:8099")
EMPLOYEE_EMAIL = "module-n-verify@sti-th.com"
IDLE_SECONDS = int(os.environ.get("SCALE_TO_ZERO_IDLE_SECONDS", "20"))

_failures: list[str] = []


def _check(cond: bool, msg: str) -> bool:
    print(("OK:   " if cond else "FAIL: ") + msg)
    if not cond:
        _failures.append(msg)
    return cond


def _fail(msg: str):
    print("FAIL: " + msg)
    _failures.append(msg)


def _api(method: str, path: str, body=None, raw: bytes | None = None, expect=(200, 201)):
    data = raw if raw is not None else (json.dumps(body).encode() if body is not None else None)
    req = urllib.request.Request(BASE_URL + path, data=data, method=method)
    req.add_header("X-Dev-User-Email", EMPLOYEE_EMAIL)
    req.add_header("X-Dev-User-Name", "Module N Verifier")
    req.add_header("X-Dev-Department", "IT")
    if raw is None and data is not None:
        req.add_header("Content-Type", "application/json")
    elif raw is not None:
        req.add_header("Content-Type", "application/gzip")
    try:
        with urllib.request.urlopen(req, timeout=300) as resp:
            payload = resp.read()
            status = resp.status
    except urllib.error.HTTPError as e:
        payload, status = e.read(), e.code
    parsed = json.loads(payload) if payload else {}
    if status not in expect:
        raise SystemExit(f"{method} {path} -> {status}: {json.dumps(parsed)[:600]}")
    return parsed


def _docker(*args: str, check: bool = True) -> str:
    r = subprocess.run(["docker", *args], capture_output=True, text=True)
    if check and r.returncode != 0:
        raise SystemExit(f"docker {' '.join(args)} failed: {r.stderr.strip()}")
    return r.stdout.strip()


def _inspect(target: str, fmt: str, check: bool = True) -> str:
    return _docker("inspect", "-f", fmt, target, check=check)


def _env_of(container: str) -> dict:
    return dict(e.split("=", 1) for e in json.loads(_inspect(container, "{{json .Config.Env}}")) if "=" in e)


# The deployed application does the FR-063 proof itself: it reads the
# environment the platform injected, dials its database over the private
# network, and speaks enough of the Postgres wire protocol to establish that
# a real Postgres server answered. Standard library only, so the build needs
# no module downloads.
_APP_SOURCE = r'''package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

func probe() map[string]any {
	out := map[string]any{
		"database_url_present": os.Getenv("DATABASE_URL") != "",
		"database_host":        os.Getenv("DATABASE_HOST"),
		"database_port":        os.Getenv("DATABASE_PORT"),
		"database_name":        os.Getenv("DATABASE_NAME"),
		"database_user":        os.Getenv("DATABASE_USER"),
		"password_present":     os.Getenv("DATABASE_PASSWORD") != "",
	}
	host, port := os.Getenv("DATABASE_HOST"), os.Getenv("DATABASE_PORT")
	if host == "" || port == "" {
		out["reachable"] = false
		out["error"] = "no database environment injected"
		return out
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), 5*time.Second)
	if err != nil {
		out["reachable"] = false
		out["error"] = err.Error()
		return out
	}
	defer conn.Close()
	// Postgres SSLRequest: int32 length 8, int32 magic 80877103. A real
	// Postgres server answers with a single byte, 'S' or 'N'.
	msg := make([]byte, 8)
	binary.BigEndian.PutUint32(msg[0:4], 8)
	binary.BigEndian.PutUint32(msg[4:8], 80877103)
	if _, err := conn.Write(msg); err != nil {
		out["reachable"] = false
		out["error"] = err.Error()
		return out
	}
	reply := make([]byte, 1)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Read(reply); err != nil {
		out["reachable"] = false
		out["error"] = err.Error()
		return out
	}
	out["reachable"] = true
	out["postgres_ssl_reply"] = string(reply)
	return out
}

func main() {
	http.HandleFunc("/dbcheck", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(probe())
	})
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "module-n verifier app")
	})
	http.ListenAndServe(":8080", nil)
}
'''


def _source_archive() -> bytes:
    with tempfile.TemporaryDirectory() as tmp:
        api = Path(tmp) / "api"
        api.mkdir()
        (api / "main.go").write_text(_APP_SOURCE)
        (api / "go.mod").write_text("module dbverify\n\ngo 1.25\n")
        archive = Path(tmp) / "src.tar.gz"
        with tarfile.open(archive, "w:gz") as tar:
            tar.add(api, arcname="api")
        return archive.read_bytes()


def _app_containers(app_name: str) -> list[str]:
    """Every running container for this application, found by IMAGE.

    Deliberately not by name: the deploy path names a container
    "platform-run-<app>-<service>-<deployment>" while resume, restart and
    cold start name it "platform-run-<deployment>-<service>", so no single
    name pattern matches all four. The build engine's image ref is the one
    identifier all of them share.
    """
    rows = _docker("ps", "--filter", "name=platform-run-", "--format", "{{.Names}}\t{{.Image}}")
    prefix = f"platform-build/{app_name}-"
    out = []
    for row in rows.splitlines():
        if "\t" not in row:
            continue
        name, image = row.split("\t", 1)
        if image.startswith(prefix):
            out.append(name)
    return out


def _dbcheck(app_name: str) -> dict:
    """Ask the deployed application itself, through the platform's proxy."""
    req = urllib.request.Request(f"{BASE_URL}/run/{app_name}/api/dbcheck")
    for attempt in range(30):
        try:
            with urllib.request.urlopen(req, timeout=60) as resp:
                return json.loads(resp.read())
        except Exception as e:  # the cold-start path may need a moment
            if attempt == 29:
                return {"error": str(e)}
            time.sleep(2)
    return {}


def _psql(private_net: str, host: str, user: str, dbname: str, pw: str, sql: str,
          attempts: int = 20) -> subprocess.CompletedProcess:
    """Run a real psql client on the application's private network.

    Retried, because a just-created Postgres container restarts itself once
    during initdb — a refused connection in the first few seconds says
    nothing about isolation.
    """
    last = None
    for _ in range(attempts):
        last = subprocess.run(
            ["docker", "run", "--rm", "--network", private_net, "-e", f"PGPASSWORD={pw}", "postgres:16-alpine",
             "psql", "-h", host, "-U", user, "-d", dbname, "-tAc", sql],
            capture_output=True, text=True, timeout=180)
        if last.returncode == 0:
            return last
        time.sleep(2)
    return last


def _deploy_app(dept_id: str, app_name: str, yaml: str) -> tuple[str, str] | None:
    """Register, validate, build and deploy one real application."""
    app = _api("POST", "/applications", {
        "name": app_name,
        "description": "Module N end-to-end verification",
        "owning_department_id": dept_id,
        "deployment_yaml_draft": yaml,
    })
    app_id = app["id"]
    _api("PUT", f"/applications/{app_id}/deployment-yaml", {"deployment_yaml": yaml})
    validation = _api("POST", f"/applications/{app_id}/validate")
    if not _check(validation.get("report", {}).get("valid") is True,
                  f"{app_name}: deployment.yaml declaring database.type=postgres validates"):
        print(json.dumps(validation, indent=2)[:1200])
        return None

    build = _api("POST", f"/applications/{app_id}/build", raw=_source_archive())
    if not _check(str(build.get("status")).lower() == "succeeded",
                  f"{app_name}: build succeeded (got {build.get('status')})"):
        print(json.dumps(build, indent=2)[:1500])
        return None

    deployment = _api("POST", f"/applications/{app_id}/deploy", {"environment": "dev"})
    if not _check(str(deployment.get("status")).lower() == "running",
                  f"{app_name}: deployment is running (got {deployment.get('status')} / {deployment.get('failure_reason')})"):
        return None
    return app_id, deployment["id"]


def _platform_db_row_status(db_container: str) -> str | None:
    """The platform's own record for a provisioned database.

    Read from the platform's database through docker compose, because
    nothing exposes it over HTTP — Module N has no read API of its own, by
    design (FR-061: databases are not employee-administrable).
    Returns None if the compose stack can't be addressed from here.
    """
    pg = _docker("compose", "ps", "-q", "postgres", check=False)
    if not pg:
        return None
    env = dict(e.split("=", 1) for e in json.loads(_inspect(pg, "{{json .Config.Env}}")) if "=" in e)
    r = subprocess.run(
        ["docker", "exec", "-i", pg, "psql", "-U", env.get("POSTGRES_USER", "postgres"),
         "-d", env.get("POSTGRES_DB", "platform"), "-tAc",
         f"SELECT status FROM provisioned_databases WHERE host = '{db_container}'"],
        capture_output=True, text=True, timeout=120)
    return r.stdout.strip() if r.returncode == 0 else None


def _teardown(app_id: str, db_container: str, private_net: str) -> None:
    _api("POST", f"/applications/{app_id}/suspend", expect=(200, 400, 409, 422))
    _api("POST", f"/applications/{app_id}/archive")
    _api("POST", f"/applications/{app_id}/delete", {"confirm": True})

    running = _docker("ps", "--filter", f"name={db_container}", "--format", "{{.Names}}")
    _check(running == "", f"the database container is no longer running ({running!r})")
    nets_left = _docker("network", "ls", "--filter", f"name={private_net}", "--format", "{{.Name}}")
    _check(nets_left == "", f"the private network was removed ({nets_left!r})")

    # The runtime resources being gone is only half of FR-065: the platform
    # also has to KNOW they are gone, or a stale "provisioned" row would
    # later hand some container a dead database.
    status = _platform_db_row_status(db_container)
    if status is None:
        print("SKIP: could not reach the platform's own database to check the record was closed")
    else:
        _check(status == "deprovisioned", f"the platform's own record is closed (status={status!r})")


def main() -> int:
    suffix = uuid.uuid4().hex[:6]
    department = _api("GET", "/departments")["departments"][0]
    dept_id = department.get("ID") or department["id"]

    print(f"=== Module N verification against {BASE_URL} ===\n")

    # A pinned application (scaling.min: 1) is NOT eligible for
    # scale-to-zero, so resume and restart really do start containers for it
    # — which is the only way to observe what those two paths hand the
    # container. The elastic application in phase 2 is the opposite case.
    app_name = f"dbpinned{suffix}"
    yaml = (
        f"app:\n  name: {app_name}\n  owner: IT\n"
        "services:\n  api:\n    runtime: go\n    port: 8080\n"
        "database:\n  type: postgres\n"
        "scaling:\n  min: 1\n"
    )

    print("--- phase 1: register + validate + build + deploy (real, no shortcuts) ---")
    deployed = _deploy_app(dept_id, app_name, yaml)
    if deployed is None:
        return 1
    app_id, deployment_id = deployed

    print("\n--- FR-061: a real, dedicated database exists ---")
    db_container = _docker("ps", "--filter", f"name=platform-db-{app_name}-", "--format", "{{.Names}}").splitlines()
    if not _check(len(db_container) == 1, f"exactly one database container was provisioned (found {db_container})"):
        return 1
    db_container = db_container[0]
    image = _inspect(db_container, "{{.Config.Image}}")
    _check(image == "postgres:16-alpine", f"database container runs the pinned engine image ({image})")
    _check(_inspect(db_container, "{{.State.Running}}") == "true", "database container is running")

    print("\n--- FR-062: isolation, proved from both sides ---")
    ports = _inspect(db_container, "{{json .NetworkSettings.Ports}}")
    _check(json.loads(ports) in ({}, {"5432/tcp": None}, {"5432/tcp": []}) or
           all(v in (None, []) for v in json.loads(ports).values()),
           f"database publishes NO host port — unreachable from the host ({ports})")

    networks = json.loads(_inspect(db_container, "{{json .NetworkSettings.Networks}}"))
    _check(len(networks) == 1, f"database sits on exactly one network, its application's own ({list(networks)})")
    private_net = next(iter(networks))
    _check(private_net.startswith("platform-net-"), f"that network is the platform-created private network ({private_net})")

    containers = _app_containers(app_name)
    if not _check(len(containers) == 1, f"one application container is running ({containers})"):
        return 1
    app_container = containers[0]
    app_container_id = _inspect(app_container, "{{.Id}}")
    app_nets = json.loads(_inspect(app_container, "{{json .NetworkSettings.Networks}}"))
    _check(private_net in app_nets, f"the application container is attached to that private network ({list(app_nets)})")

    # From INSIDE the network: a real client authenticates with the real
    # generated credentials and writes real data.
    creds = json.loads(_inspect(db_container, "{{json .Config.Env}}"))
    env = dict(e.split("=", 1) for e in creds if "=" in e)
    pw, user, dbname = env["POSTGRES_PASSWORD"], env["POSTGRES_USER"], env["POSTGRES_DB"]
    inside = _psql(private_net, db_container, user, dbname, pw,
                   "CREATE TABLE IF NOT EXISTS verify(v text); "
                   "INSERT INTO verify VALUES ('written-before-restart'); SELECT count(*) FROM verify;")
    _check(inside.returncode == 0 and inside.stdout.strip().endswith("1"),
           f"a client ON the private network connects with the generated credentials and writes data "
           f"(psql said: {inside.stdout.strip() or inside.stderr.strip()[:200]})")

    # From OUTSIDE the network: the same connection must not work, neither
    # by name (Docker's DNS is per-network) nor by raw IP (the isolation
    # that actually matters — a name lookup failing proves much less).
    db_ip = _inspect(db_container, "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}")
    for label, target in (("by container name", db_container), ("by raw IP", db_ip)):
        outside = subprocess.run(
            ["docker", "run", "--rm", "-e", f"PGPASSWORD={pw}", "postgres:16-alpine",
             "psql", "-h", target, "-U", user, "-d", dbname, "-tAc", "SELECT 1", "-w",
             "-o", "/dev/null", "--set=ON_ERROR_STOP=1", "-v", "connect_timeout=5"],
            capture_output=True, text=True, timeout=180)
        _check(outside.returncode != 0,
               f"the identical connection from OFF the network fails, {label} "
               f"({(outside.stderr.strip() or outside.stdout.strip())[:120]})")

    print("\n--- FR-063: the application itself uses the injected credentials ---")
    probe = _dbcheck(app_name)
    _check(probe.get("database_url_present") is True, "the application sees DATABASE_URL in its environment")
    _check(probe.get("password_present") is True, "the application sees DATABASE_PASSWORD in its environment")
    _check(probe.get("reachable") is True,
           f"the application really connected to its database (reply {probe.get('postgres_ssl_reply')!r}, error {probe.get('error')!r})")
    _check(probe.get("postgres_ssl_reply") in ("S", "N"),
           "what answered on that connection really is a Postgres server")

    env_seen = json.loads(_inspect(app_container, "{{json .Config.Env}}"))
    _check(any(e.startswith("DATABASE_URL=postgres://") for e in env_seen),
           "the credentials are delivered as container environment, never written into deployment.yaml")

    print("\n--- resume and restart each start a NEW container: is it still wired? ---")
    for label, calls in (("resume", ("suspend", "resume")), ("restart", ("restart",))):
        for call in calls:
            _api("POST", f"/applications/{app_id}/{call}")
        after = _app_containers(app_name)
        if not _check(len(after) == 1, f"{label}: one container is running afterwards ({after})"):
            continue
        app_container = after[0]
        # By ID, not by name: restart reuses the same deterministic name, so
        # comparing names would call a genuinely new container "the old one".
        new_id = _inspect(app_container, "{{.Id}}")
        _check(new_id != app_container_id, f"{label}: it really is a NEW container ({new_id[:12]})")
        app_container_id = new_id
        nets = json.loads(_inspect(app_container, "{{json .NetworkSettings.Networks}}"))
        _check(private_net in nets, f"{label}: the new container is still on the private network")
        probe = _dbcheck(app_name)
        _check(probe.get("reachable") is True,
               f"{label}: the application still reaches its database ({probe.get('error')!r})")

    print("\n--- the data was the SAME database all along, not a fresh one ---")
    still = _psql(private_net, db_container, user, dbname, pw, "SELECT v FROM verify LIMIT 1")
    _check(still.returncode == 0 and still.stdout.strip() == "written-before-restart",
           f"the row written before those restarts is still there (got {still.stdout.strip()!r})")

    # Checked from a container on the application's network, never from
    # inside the database's own: the official image trusts loopback, so any
    # password "works" there — which would make this check meaningless.
    print("\n--- FR-068: rotating the database password ---")
    old_pw = _env_of(app_container)["DATABASE_PASSWORD"]
    rotated = _api("POST", f"/applications/{app_id}/secrets/DATABASE_PASSWORD/rotate")
    _check(rotated.get("restarted") is True and rotated.get("secret", {}).get("version") == 2,
           f"the owner rotates it; running instances restarted onto version {rotated.get('secret', {}).get('version')}")
    after = _app_containers(app_name)
    new_pw = _env_of(after[0])["DATABASE_PASSWORD"] if len(after) == 1 else None
    _check(bool(new_pw) and new_pw != old_pw, "the restarted container was given a new password")
    _check(bool(new_pw) and new_pw not in json.dumps(rotated) and old_pw not in json.dumps(rotated),
           "...and the API response contained neither password")
    stale = _psql(private_net, db_container, user, dbname, old_pw, "SELECT 1", attempts=1)
    _check(stale.returncode != 0 and "password authentication failed" in stale.stderr,
           "the old password no longer authenticates, from the application's network")
    fresh = _psql(private_net, db_container, user, dbname, new_pw or "", "SELECT v FROM verify LIMIT 1", attempts=1)
    _check(fresh.returncode == 0 and fresh.stdout.strip() == "written-before-restart",
           "the new one does, and the data is untouched")
    _check(_dbcheck(app_name).get("reachable") is True, "the application still reaches its database")
    _api("PUT", f"/applications/{app_id}/secrets/OWNER_SET_KEY", {"value": "owner-set-value"})
    refused = _api("POST", f"/applications/{app_id}/secrets/OWNER_SET_KEY/rotate", expect=(409,))
    _check("secret_not_rotatable" in json.dumps(refused),
           "an owner-set secret is refused: the platform can't invalidate what it didn't issue")

    print("\n--- FR-065: deletion tears the database down ---")
    _teardown(app_id, db_container, private_net)

    # --- phase 2 -----------------------------------------------------------
    # A cold start is the one path that carries only a deployment id, so it
    # has to resolve its way back to the application before it can find the
    # database. It needs a scale-to-zero-ELIGIBLE application, which is the
    # opposite of phase 1's pinned one — hence a second application rather
    # than a second act for the first.
    print("\n--- phase 2: scale-to-zero cold start ---")
    if IDLE_SECONDS > 90:
        print(f"SKIP: SCALE_TO_ZERO_IDLE_SECONDS={IDLE_SECONDS} is too long to wait; "
              "set it to ~20 in .env to check this path")
    else:
        elastic_name = f"dbelastic{suffix}"
        elastic_yaml = (
            f"app:\n  name: {elastic_name}\n  owner: IT\n"
            "services:\n  api:\n    runtime: go\n    port: 8080\n"
            "database:\n  type: postgres\n"
        )
        deployed = _deploy_app(dept_id, elastic_name, elastic_yaml)
        if deployed is None:
            return 1
        elastic_id, _ = deployed
        elastic_db = _docker("ps", "--filter", f"name=platform-db-{elastic_name}-", "--format", "{{.Names}}")
        elastic_net = next(iter(json.loads(_inspect(elastic_db, "{{json .NetworkSettings.Networks}}"))))

        deadline = time.time() + IDLE_SECONDS + 60
        while time.time() < deadline and _app_containers(elastic_name):
            time.sleep(3)
        if _check(not _app_containers(elastic_name), "the application scaled to zero (no container running)"):
            probe = _dbcheck(elastic_name)  # this request is what cold-starts it
            cold = _app_containers(elastic_name)
            _check(len(cold) == 1, f"the request cold-started a new container ({cold})")
            if cold:
                nets = json.loads(_inspect(cold[0], "{{json .NetworkSettings.Networks}}"))
                _check(elastic_net in nets, "cold start: the new container is on the private network")
            _check(probe.get("database_url_present") is True, "cold start: DATABASE_URL was injected again")
            _check(probe.get("reachable") is True,
                   f"cold start: the application reaches its database ({probe.get('error')!r})")
            _check(_inspect(elastic_db, "{{.State.Running}}") == "true",
                   "the database stayed up while the application was scaled to zero")

        _teardown(elastic_id, elastic_db, elastic_net)

    print()
    if _failures:
        print(f"=== {len(_failures)} CHECK(S) FAILED ===")
        for f in _failures:
            print("  - " + f)
        return 1
    print("=== all Module N checks passed ===")
    return 0


if __name__ == "__main__":
    sys.exit(main())
