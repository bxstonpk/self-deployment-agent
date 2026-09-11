"""End-to-end verification of Module S (Logging) against a live stack and a
real Docker daemon.

    docker compose up -d --build      # from repo root
    python services/platform-api/scripts/verify_module_s.py

Nothing here is mocked. The application it deploys prints its own secrets on
purpose, a line a second, and a last line when it's stopped; a second one
crashes on start. What's checked:

  FR-086  what a running application prints reaches the central store,
          tagged with its service, stream, deployment and container; the
          secrets the platform injected (an employee's API_KEY, the
          platform's DATABASE_URL and DATABASE_PASSWORD) are redacted before
          they're stored, and appear nowhere in a full dump of the platform
          database; a replaced container's history — down to the line it
          printed while being stopped — outlives it; a container that
          crashed on its first deploy leaves the reason behind; and after
          platform-api itself is stopped and started again, every line is
          there exactly once, including those written while it was down
  FR-087  filters (text, service, environment, time, limit), and paging by
          cursor that neither skips nor repeats a line
  FR-087/089  someone who isn't an owner gets exactly the 404 a
          nonexistent application gets, and the refusal leaves no trace in
          an audit log they can read
  FR-089  every successful read is audited, without the text searched for

Stops and restarts platform-api once (skip with --skip-platform-restart).
Never prints a secret value.
"""

from __future__ import annotations

import argparse
import io
import os
import subprocess
import sys
import tarfile
import time
import urllib.parse
import urllib.request
import uuid
from datetime import datetime, timedelta, timezone
from pathlib import Path

HERE = Path(__file__).resolve().parent
REPO_ROOT = HERE.parents[2]
sys.path.insert(0, str(HERE))
import verify_module_o as vo  # noqa: E402
from verify_module_n import _app_containers, _docker  # noqa: E402

BASE_URL = vo.BASE_URL
OWNER = "module-s-verify@sti-th.com"
INTRUDER = "module-s-intruder@sti-th.com"

_failures: list[str] = []
_responses: list[str] = []  # every log response, searched for secrets at the end
_owner_reads = 0            # successful reads by OWNER, each of which must be audited


def _check(cond, msg: str) -> bool:
    print(("OK:   " if cond else "FAIL: ") + msg)
    if not cond:
        _failures.append(msg)
    return bool(cond)


def _call(method: str, path: str, body=None, raw: bytes | None = None, as_email: str = OWNER):
    return vo._call(method, path, body, raw, as_email)


def _api(method: str, path: str, body=None, raw: bytes | None = None, as_email: str = OWNER):
    return vo._api(method, path, body, raw, as_email)


def _logs(app_id: str, as_email: str = OWNER, **params):
    global _owner_reads
    query = urllib.parse.urlencode({k: v for k, v in params.items() if v is not None})
    status, body, text = _call("GET", f"/applications/{app_id}/logs" + (f"?{query}" if query else ""), as_email=as_email)
    _responses.append(text)
    if status == 200 and as_email == OWNER:
        _owner_reads += 1
    return status, body or {}, text


def _entries(app_id: str, **params) -> list[dict]:
    status, body, text = _logs(app_id, **params)
    if status != 200:
        raise SystemExit(f"reading logs failed: HTTP {status}: {text[:300]}")
    return body.get("entries", [])


def _wait_for(app_id: str, predicate, timeout: float = 30.0, **params) -> list[dict]:
    deadline = time.time() + timeout
    while True:
        entries = _entries(app_id, **params)
        if predicate(entries) or time.time() > deadline:
            return entries
        time.sleep(0.5)


def _find(entries: list[dict], text: str, instance: str | None = None) -> list[dict]:
    return [e for e in entries if text in e["message"] and (instance is None or e["instance"] == instance)]


def _ts(value: str) -> datetime:
    """The API's RFC 3339 timestamps, e.g. 2026-09-11T05:06:07.12345Z."""
    base, _, frac = value.rstrip("Z").partition(".")
    micros = int((frac + "000000")[:6]) if frac else 0
    return datetime.strptime(base, "%Y-%m-%dT%H:%M:%S").replace(tzinfo=timezone.utc) + timedelta(microseconds=micros)


# Prints its secrets on purpose — the point is to see them redacted.
_APP_SOURCE = r'''package main

import (
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	fmt.Println("module-s app starting")
	fmt.Fprintln(os.Stderr, "module-s app: this line goes to stderr")
	fmt.Println("module-s app: using API_KEY=" + os.Getenv("API_KEY"))
	fmt.Println("module-s app: connecting to " + os.Getenv("DATABASE_URL"))
	fmt.Println("module-s app: database password is " + os.Getenv("DATABASE_PASSWORD"))

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, os.Interrupt)
	go func() {
		<-stop
		fmt.Println("module-s app: received SIGTERM, shutting down")
		os.Exit(0)
	}()
	go func() {
		for i := 1; ; i++ {
			fmt.Printf("tick %d\n", i)
			time.Sleep(time.Second)
		}
	}()
	http.HandleFunc("/say", func(w http.ResponseWriter, r *http.Request) {
		fmt.Println(r.URL.Query().Get("msg"))
		w.Write([]byte("ok\n"))
	})
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("module-s verifier app\n"))
	})
	http.ListenAndServe(":8080", nil)
}
'''

_CRASH_SOURCE = r'''package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Println("crash-verify: loading configuration")
	fmt.Fprintln(os.Stderr, "crash-verify: fatal: REQUIRED_SETTING is not set")
	os.Exit(1)
}
'''


def _archive(source: str) -> bytes:
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w:gz") as tar:
        d = tarfile.TarInfo("api")
        d.type, d.mode = tarfile.DIRTYPE, 0o755
        tar.addfile(d)
        for name, content in (("api/main.go", source), ("api/go.mod", "module logverify\n\ngo 1.25\n")):
            data = content.encode()
            info = tarfile.TarInfo(name)
            info.size = len(data)
            tar.addfile(info, io.BytesIO(data))
    return buf.getvalue()


def _register_and_build(dept_id: str, name: str, source: str, database: bool = False) -> str:
    """Pinned (scaling.min: 1), so restart really does replace a container."""
    yaml = (f"app:\n  name: {name}\n  owner: IT\n"
            "services:\n  api:\n    runtime: go\n    port: 8080\n"
            "scaling:\n  min: 1\n" + ("database:\n  type: postgres\n" if database else ""))
    app = _api("POST", "/applications", {
        "name": name, "description": "Module S end-to-end verification",
        "owning_department_id": dept_id, "deployment_yaml_draft": yaml,
    })
    app_id = app["id"]
    _api("PUT", f"/applications/{app_id}/deployment-yaml", {"deployment_yaml": yaml})
    if not _api("POST", f"/applications/{app_id}/validate").get("report", {}).get("valid"):
        raise SystemExit(f"{name}: validation failed")
    build = _api("POST", f"/applications/{app_id}/build", raw=_archive(source))
    if str(build.get("status")).lower() != "succeeded":
        raise SystemExit(f"{name}: build failed: {str(build)[:800]}")
    return app_id


def _say(app_name: str, msg: str) -> None:
    url = f"{BASE_URL}/run/{app_name}/api/say?" + urllib.parse.urlencode({"msg": msg})
    last = ""
    for _ in range(30):
        try:
            with urllib.request.urlopen(url, timeout=60) as resp:
                if resp.status == 200:
                    return
        except Exception as e:  # a just-restarted container may need a moment
            last = str(e)
        time.sleep(1)
    raise SystemExit(f"could not reach {app_name}: {last}")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--skip-platform-restart", action="store_true", help="don't stop and restart platform-api")
    args = parser.parse_args()
    os.chdir(REPO_ROOT)

    suffix = uuid.uuid4().hex[:6]
    dept = _api("GET", "/departments")["departments"][0]
    dept_id = dept.get("ID") or dept["id"]
    secret = f"sk-logs-{uuid.uuid4().hex}"
    needle = f"needle-{uuid.uuid4().hex[:12]}"
    print(f"=== Module S verification against {BASE_URL} ===\n")

    print("--- FR-086: what a running application prints, collected and redacted ---")
    app_name = f"logs{suffix}"
    app_id = _register_and_build(dept_id, app_name, _APP_SOURCE, database=True)
    _api("PUT", f"/applications/{app_id}/secrets/API_KEY", {"value": secret})
    status, dep, text = _call("POST", f"/applications/{app_id}/deploy", {"environment": "dev"})
    if str((dep or {}).get("status")).lower() != "running":
        raise SystemExit(f"deploy did not reach running: HTTP {status}: {text[:400]}")
    first = vo._running(app_name)
    first_id = vo._container_id(first) or ""
    db_container = _docker("ps", "--filter", f"name=platform-db-{app_name}-", "--format", "{{.Names}}")
    db_password = vo._container_env(db_container).get("POSTGRES_PASSWORD") if db_container else None
    _check(first_id and db_password, "the application runs with its own database, so DATABASE_URL and DATABASE_PASSWORD are injected too")

    entries = _wait_for(app_id, lambda es: _find(es, "database password is") and _find(es, "app starting"))
    start = _find(entries, "module-s app starting")
    _check(len(start) == 1 and start[0]["service"] == "api" and start[0]["stream"] == "stdout"
           and start[0]["deployment_id"] == dep["id"] and start[0]["instance"] == first_id[:12],
           "the first line it printed is stored, tagged with its service, stream, deployment and container")
    err = _find(entries, "this line goes to stderr")
    _check(len(err) == 1 and err[0]["stream"] == "stderr", "stderr is collected too, and marked as such")
    for label, marker, expected in (
            ("the employee's API_KEY", "using API_KEY=", "module-s app: using API_KEY=[REDACTED:API_KEY]"),
            ("the platform's DATABASE_URL, whole", "connecting to", "module-s app: connecting to [REDACTED:DATABASE_URL]"),
            ("and DATABASE_PASSWORD", "database password is", "module-s app: database password is [REDACTED:DATABASE_PASSWORD]")):
        # Compared, never printed: if redaction failed, the message is the secret.
        _check([e["message"] for e in _find(entries, marker)] == [expected], f"{label} is redacted before it is stored")
    dump = vo._platform_dump()
    _check(vo._absent_from(dump, secret) and vo._absent_from(dump, db_password or secret),
           "neither value appears anywhere in a full dump of the platform database")
    _check(secret not in _docker("compose", "logs", "--no-log-prefix", "platform-api"), "nor in platform-api's own logs")
    raw = subprocess.run(["docker", "logs", first], capture_output=True, text=True, errors="replace")
    if secret in raw.stdout + raw.stderr:
        print("INFO: Docker's own log of the running container still has the raw line; it goes when the container is removed (see the README)")

    print("\n--- FR-087: filters ---")
    _say(app_name, f"{needle} progress 100% done")
    _say(app_name, f"{needle} second line")
    mine = _wait_for(app_id, lambda es: len(es) >= 2, contains=needle)
    _check([e["message"] for e in mine] == [f"{needle} second line", f"{needle} progress 100% done"],
           "a text filter finds exactly the matching lines, newest first")
    _check(len(_entries(app_id, contains=needle.upper())) == 2, "case-insensitively")
    _check(len(_entries(app_id, contains=f"{needle} progress 100%")) == 1, "% in a search is a literal character")
    _check(len(_entries(app_id, contains=f"{needle} progress 1_0")) == 0, "and so is _: neither is a wildcard")
    _check(_entries(app_id, service="api") and not _entries(app_id, service="nope"), "by service")
    _check(_entries(app_id, environment="dev") and not _entries(app_id, environment="prod"), "by environment")
    newer = mine[0]["timestamp"]
    _check([e["message"] for e in _entries(app_id, contains=needle, since=newer)] == [mine[0]["message"]]
           and [e["message"] for e in _entries(app_id, contains=needle, until=newer)] == [mine[1]["message"]],
           "by time: since is inclusive, until exclusive")
    _check(len(_entries(app_id, limit=3)) == 3, "limit caps the number of lines")
    for label, params, code in (("a malformed time", {"since": "yesterday"}, "invalid_time"),
                                ("a malformed limit", {"limit": "zero"}, "invalid_limit"),
                                ("a malformed cursor", {"cursor": "nope"}, "invalid_cursor")):
        status, _, text = _logs(app_id, **params)
        _check(status == 400 and code in text, f"{label} is rejected ({code}, HTTP {status})")

    print("\n--- FR-087: paging ---")
    _wait_for(app_id, lambda es: len(es) > 14, contains="tick ", limit=1000)
    until = (datetime.now(timezone.utc) - timedelta(seconds=2)).isoformat()
    whole = _entries(app_id, contains="tick ", until=until, limit=1000)
    paged, cursor, pages = [], None, 0
    while pages < 500:
        _, body, _ = _logs(app_id, contains="tick ", until=until, limit=3, cursor=cursor)
        paged += body.get("entries", [])
        pages += 1
        cursor = body.get("next_cursor")
        if not cursor:
            break

    def key(e):
        return e["timestamp"], e["instance"], e["message"]
    _check(len(whole) > 9 and [key(e) for e in paged] == [key(e) for e in whole],
           f"following next_cursor 3 at a time returns the same {len(whole)} lines, in order, none skipped or repeated ({pages} pages)")

    print("\n--- FR-087/FR-089: someone who isn't an owner ---")
    refused = _logs(app_id, as_email=INTRUDER)
    missing = _logs(str(uuid.uuid4()), as_email=INTRUDER)
    _check(refused[0] == 404 and (refused[0], refused[2]) == (missing[0], missing[2]),
           f"gets exactly what a nonexistent application gets (HTTP {refused[0]}, byte-identical body)")
    status, _, text = _logs(app_id, as_email=INTRUDER, contains=needle)
    _check(status == 404 and needle not in text, "with filters too")
    left = vo._platform_sql("SELECT count(*) FROM audit_log a JOIN users u ON u.id = a.actor_user_id "
                            f"WHERE u.email = '{INTRUDER}' AND a.action = 'application.read_logs'")
    _check(left == "0", "and the refusal leaves no audit entry, which their own audit view would show")

    print("\n--- FR-089: reads are audited ---")
    reads = vo._platform_sql("SELECT count(*) FROM audit_log a JOIN users u ON u.id = a.actor_user_id "
                             f"WHERE u.email = '{OWNER}' AND a.action = 'application.read_logs' "
                             f"AND a.resource_id = '{app_id}' AND a.outcome = 'success'")
    _check(int(reads) == _owner_reads, f"every successful read by the owner is in the audit trail ({reads} of {_owner_reads})")
    leaked = vo._platform_sql(f"SELECT count(*) FROM audit_log WHERE detail LIKE '%{needle}%'")
    _check(leaked == "0", "without the text that was searched for")
    status, _, csv_text = _call("GET", f"/audit-log/export?resource_id={app_id}")
    _check(status == 200 and "application.read_logs" in csv_text and needle not in csv_text,
           "and the CSV export shows them the same way")

    print("\n--- a replaced container keeps its history ---")
    _api("POST", f"/applications/{app_id}/restart")
    second_id = vo._container_id(vo._running(app_name)) or ""
    gone = subprocess.run(["docker", "inspect", first_id], capture_output=True).returncode != 0
    _check(second_id and second_id != first_id and gone, "restart replaced the container, and the old one is gone")
    entries = _entries(app_id, limit=1000)
    _check(_find(entries, "received SIGTERM", first_id[:12]),
           "the old container's very last line, printed as it was being stopped, was stored before it was removed")
    _check(_find(entries, "module-s app starting", first_id[:12]), "and its earlier lines are still there")
    entries = _wait_for(app_id, lambda es: _find(es, "module-s app starting", second_id[:12]), limit=1000)
    _check(_find(entries, "module-s app starting", second_id[:12]), "the new container's output is collected under its own instance")

    if args.skip_platform_restart:
        print("\nSKIP: platform-api restart (--skip-platform-restart)")
    else:
        print("\n--- platform-api stops and starts: collection resumes where it left off ---")
        try:
            _docker("compose", "stop", "platform-api")
            stopped_at = datetime.now(timezone.utc)
            time.sleep(6)  # the application keeps printing; nothing is collecting
            restarted_at = datetime.now(timezone.utc)
        finally:
            vo._recreate_platform_api(None)
        _check("resumed collection for" in _docker("compose", "logs", "--no-log-prefix", "platform-api"),
               "platform-api picked collection back up on start")
        _say(app_name, f"{needle} after the restart")
        _check(len(_wait_for(app_id, lambda es: len(es) == 3, contains=needle)) == 3, "and collects new output")
        ticks = [e for e in _entries(app_id, contains="tick ", limit=1000) if e["instance"] == second_id[:12]]
        numbers = sorted(int(e["message"].split()[1]) for e in ticks)
        _check(numbers and numbers == list(range(1, len(numbers) + 1)),
               f"every line since the container started is stored exactly once: ticks 1-{len(numbers)}, no gaps, no repeats")
        during = [e for e in ticks if stopped_at < _ts(e["timestamp"]) < restarted_at]
        _check(len(during) >= 3, f"including the {len(during)} written while platform-api was down")
        starts = _find(_entries(app_id, contains="module-s app starting"), "module-s app starting", second_id[:12])
        _check(len(starts) == 1, "and no line from before the restart was stored twice")

    print("\n--- FR-086: a container that crashes on its first deploy ---")
    crash_name = f"crash{suffix}"
    crash_id = _register_and_build(dept_id, crash_name, _CRASH_SOURCE)
    status, body, _ = _call("POST", f"/applications/{crash_id}/deploy", {"environment": "dev"})
    _check(status >= 400 or str((body or {}).get("status")).lower() == "failed",
           f"the deploy fails (HTTP {status}, status {(body or {}).get('status')})")
    _check(not _app_containers(crash_name), "and the platform removed the container")
    crash = _wait_for(crash_id, lambda es: len(es) >= 2, timeout=10)
    fatal = _find(crash, "REQUIRED_SETTING is not set")
    _check(fatal and fatal[0]["stream"] == "stderr", "yet what it printed before crashing can still be read: the reason, on stderr")
    _check(_find(crash, "loading configuration"), "along with what it printed before that")

    _check(all(secret not in t and (not db_password or db_password not in t) for t in _responses),
           f"no log response, across all {len(_responses)} of them, contained either secret value")

    print("\n--- deletion ---")
    vo._teardown(app_id, OWNER)
    status, _, _ = _call("POST", f"/applications/{crash_id}/delete", {"confirm": True})
    _check(status == 200, f"both applications are deleted (HTTP {status})")
    leftovers = _app_containers(app_name) + _app_containers(crash_name)
    db_left = _docker("ps", "-a", "--filter", f"name=platform-db-{app_name}-", "--format", "{{.Names}}")
    _check(not leftovers and not db_left, "no container of either is left")
    kept = vo._platform_sql(f"SELECT count(*) FROM application_logs WHERE application_id IN ('{app_id}', '{crash_id}')")
    print(f"INFO: {kept} log line(s) of the deleted applications are still in the store: FR-088 (retention and purge) "
          "isn't implemented, so nothing is purged yet")

    print()
    if _failures:
        print(f"=== {len(_failures)} CHECK(S) FAILED ===")
        for f in _failures:
            print("  - " + f)
        return 1
    print("=== all Module S checks passed ===")
    return 0


if __name__ == "__main__":
    sys.exit(main())
