"""End-to-end verification of Module T (Monitoring) against a live stack and
a real Docker daemon.

    docker compose up -d --build      # from repo root
    python services/platform-api/scripts/verify_module_t.py

Set METRICS_SAMPLE_INTERVAL_SECONDS=5 in the repo-root .env first, or this
waits 15 seconds for every reading it needs.

Nothing is mocked. The application it deploys can burn CPU, allocate
memory, fail on demand and answer slowly, so every number checked here is
one the platform measured about real work:

  FR-090  a running container's CPU and memory are sampled without the
          application instrumenting anything, and the numbers move when the
          application actually burns CPU or allocates memory; requests,
          errors and latency are counted at the proxy every request already
          passes through; a 4xx counts as a request and not as an error; a
          stopped application produces no samples at all rather than
          interpolated zeros, and the answer says so
  FR-091  owners only, with the same 404 a nonexistent application gets;
          filters and window bounds; instance count and last scale event

Never prints a secret value.
"""

from __future__ import annotations

import os
import subprocess
import sys
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
import verify_module_s as vs  # noqa: E402

BASE_URL = vo.BASE_URL
OWNER = "module-t-verify@sti-th.com"
INTRUDER = "module-t-intruder@sti-th.com"


def _sample_interval() -> int:
    """What platform-api is actually sampling at: the repo-root .env the
    stack was started with, not this shell's environment."""
    if v := os.environ.get("METRICS_SAMPLE_INTERVAL_SECONDS"):
        return int(v)
    env = REPO_ROOT / ".env"
    if env.exists():
        for raw in env.read_text(encoding="utf-8").splitlines():
            key, _, value = raw.partition("=")
            if key.strip() == "METRICS_SAMPLE_INTERVAL_SECONDS" and value.strip():
                return int(value.strip())
    return 15


INTERVAL = _sample_interval()

_failures: list[str] = []


def _check(cond, msg: str) -> bool:
    print(("OK:   " if cond else "FAIL: ") + msg)
    if not cond:
        _failures.append(msg)
    return bool(cond)


def _call(method: str, path: str, body=None, raw: bytes | None = None, as_email: str = OWNER):
    return vo._call(method, path, body, raw, as_email)


def _api(method: str, path: str, body=None, raw: bytes | None = None, as_email: str = OWNER):
    return vo._api(method, path, body, raw, as_email)


def _metrics(app_id: str, as_email: str = OWNER, **params):
    query = urllib.parse.urlencode({k: v for k, v in params.items() if v is not None})
    status, body, text = _call("GET", f"/applications/{app_id}/metrics" + (f"?{query}" if query else ""), as_email=as_email)
    return status, body or {}, text


def _snapshot(app_id: str, **params) -> dict:
    status, body, text = _metrics(app_id, **params)
    if status != 200:
        raise SystemExit(f"reading metrics failed: HTTP {status}: {text[:300]}")
    return body


def _wait_for_sample(app_id: str, after: datetime, timeout: float | None = None) -> dict | None:
    """The newest resource sample taken after `after`, or None."""
    deadline = time.time() + (timeout if timeout is not None else INTERVAL * 3 + 10)
    while time.time() < deadline:
        for point in _snapshot(app_id).get("resource", []):
            if vs._ts(point["timestamp"]) > after:
                return point
        time.sleep(1)
    return None


# Burns CPU, allocates memory, fails and stalls on demand: every number
# this script checks is about work the application really did.
_APP_SOURCE = r'''package main

import (
	"fmt"
	"net/http"
	"time"
)

var hold [][]byte

func main() {
	fmt.Println("module-t app starting")
	http.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok\n"))
	})
	http.HandleFunc("/fail", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "deliberate failure", http.StatusInternalServerError)
	})
	http.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.Write([]byte("slow\n"))
	})
	http.HandleFunc("/burn", func(w http.ResponseWriter, r *http.Request) {
		deadline := time.Now().Add(6 * time.Second)
		x := 0.0
		for time.Now().Before(deadline) {
			x += 1.000001
		}
		fmt.Fprintf(w, "%f\n", x)
	})
	http.HandleFunc("/grow", func(w http.ResponseWriter, r *http.Request) {
		block := make([]byte, 96<<20)
		for i := range block {
			block[i] = byte(i)
		}
		hold = append(hold, block)
		w.Write([]byte("grew\n"))
	})
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Go's default mux routes every unmatched path here, so an unknown
		// one has to be turned away explicitly — the check that a 4xx
		// counts as a request and not an error needs a real 404.
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte("module-t verifier app\n"))
	})
	http.ListenAndServe(":8080", nil)
}
'''


def _hit(app_name: str, path: str, timeout: int = 60) -> int:
    url = f"{BASE_URL}/run/{app_name}/api{path}"
    req = urllib.request.Request(url)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return resp.status
    except urllib.error.HTTPError as e:
        return e.code
    except Exception:
        return 0


def main() -> int:
    os.chdir(REPO_ROOT)
    suffix = uuid.uuid4().hex[:6]
    app_name = f"metrics{suffix}"
    dept = _api("GET", "/departments")["departments"][0]
    dept_id = dept.get("ID") or dept["id"]
    print(f"=== Module T verification against {BASE_URL} (sampling every {INTERVAL}s) ===\n")

    print("--- FR-090: a running container is sampled without being asked ---")
    yaml = (f"app:\n  name: {app_name}\n  owner: IT\n"
            "services:\n  api:\n    runtime: go\n    port: 8080\n"
            "scaling:\n  min: 1\n")
    app = _api("POST", "/applications", {
        "name": app_name, "description": "Module T end-to-end verification",
        "owning_department_id": dept_id, "deployment_yaml_draft": yaml,
    })
    app_id = app["id"]
    _api("PUT", f"/applications/{app_id}/deployment-yaml", {"deployment_yaml": yaml})
    if not _api("POST", f"/applications/{app_id}/validate").get("report", {}).get("valid"):
        raise SystemExit("validation failed")
    build = _api("POST", f"/applications/{app_id}/build", raw=vs._archive(_APP_SOURCE))
    if str(build.get("status")).lower() != "succeeded":
        raise SystemExit(f"build failed: {str(build)[:600]}")
    deployment = _api("POST", f"/applications/{app_id}/deploy", {"environment": "dev"})
    if str(deployment.get("status")).lower() != "running":
        raise SystemExit(f"deploy did not reach running: {deployment.get('failure_reason')}")
    container = vo._container_id(vo._running(app_name)) or ""

    started = datetime.now(timezone.utc)
    sample = _wait_for_sample(app_id, started - timedelta(seconds=INTERVAL * 2))
    _check(sample is not None, f"a resource sample appears within {INTERVAL * 3 + 10}s of deploying")
    if sample:
        _check(sample["service"] == "api" and sample["instance"] == container[:12],
               f"it is attributed to the right service and container ({sample['service']}, {sample['instance']})")
        _check(sample["memory_bytes"] > 1024 and sample["memory_limit_bytes"] > 0,
               f"memory is a real reading ({sample['memory_bytes']} bytes of {sample['memory_limit_bytes']})")

    print("\n--- FR-090: the numbers move when the application does real work ---")
    idle_cpu = sample["cpu_percent"] if sample else 0
    before = datetime.now(timezone.utc)
    _hit(app_name, "/burn", timeout=90)
    busy = _wait_for_sample(app_id, before)
    _check(busy is not None and busy["cpu_percent"] > idle_cpu + 10,
           f"CPU rises while the application burns CPU (idle {idle_cpu}%, busy {busy['cpu_percent'] if busy else 'no sample'}%)")

    idle_memory = sample["memory_bytes"] if sample else 0
    before = datetime.now(timezone.utc)
    _hit(app_name, "/grow")
    grown = _wait_for_sample(app_id, before)
    _check(grown is not None and grown["memory_bytes"] - idle_memory > 40 * 1024 * 1024,
           f"memory rises after the application allocates 96 MiB ({idle_memory} then {grown['memory_bytes'] if grown else 'no sample'})")

    print("\n--- FR-090: traffic is counted at the proxy ---")
    for _ in range(5):
        _hit(app_name, "/ok")
    for _ in range(2):
        _hit(app_name, "/fail")
    _hit(app_name, "/slow")
    not_found = _hit(app_name, "/nope")
    _check(not_found == 404, f"the application answered 404 for an unknown path (HTTP {not_found})")
    time.sleep(INTERVAL + 3)  # the next sweep flushes what the proxy counted

    summary = _snapshot(app_id)["summary"]
    _check(summary["requests"] >= 9, f"every request through the proxy is counted ({summary['requests']})")
    _check(summary["errors"] == 2, f"only the application's own failures count as errors ({summary['errors']})")
    _check(0 < summary["error_rate"] < 1, f"the error rate is the ratio of the two ({summary['error_rate']})")
    # The maximum belongs to whichever request took longest — the CPU
    # burn, in practice — not to the deliberately sleepy one.
    _check(summary["latency_ms_max"] >= 290,
           f"latency reflects the slowest request the application really served ({summary['latency_ms_max']} ms max)")
    _check(summary["latency_ms_mean"] < summary["latency_ms_max"],
           f"and the mean is below it ({summary['latency_ms_mean']} ms)")

    print("\n--- FR-091: what the query offers ---")
    snapshot = _snapshot(app_id)
    _check(snapshot["collection"]["collecting"] is True and snapshot["collection"]["last_sample_at"],
           "the answer says collection is keeping up, and when it last read something")
    _check(any(i["service"] == "api" and i["instances"] == 1 for i in snapshot["instances"]),
           f"it reports the current instance count ({snapshot['instances']})")
    _check(snapshot.get("last_scale_event") is not None, "and the last scale event")
    _check(_snapshot(app_id, service="api")["resource"] and not _snapshot(app_id, service="nope")["resource"],
           "filtering by service works")
    _check(_snapshot(app_id, environment="dev")["traffic"] and not _snapshot(app_id, environment="prod")["traffic"],
           "so does filtering by environment")
    far_past = (datetime.now(timezone.utc) - timedelta(days=2)).isoformat()
    old_window = _snapshot(app_id, **{"from": far_past, "to": (datetime.now(timezone.utc) - timedelta(days=1)).isoformat()})
    _check(not old_window["resource"] and not old_window["traffic"], "a window before the application existed is empty")
    for label, params, code in (("a malformed time", {"from": "yesterday"}, "invalid_time"),
                                ("a backwards window", {"from": datetime.now(timezone.utc).isoformat(),
                                                        "to": far_past}, "invalid_window")):
        status, _, text = _metrics(app_id, **params)
        _check(status == 400 and code in text, f"{label} is rejected ({code}, HTTP {status})")

    print("\n--- FR-091: someone who isn't an owner ---")
    refused = _metrics(app_id, as_email=INTRUDER)
    missing = _metrics(str(uuid.uuid4()), as_email=INTRUDER)
    _check(refused[0] == 404 and (refused[0], refused[2]) == (missing[0], missing[2]),
           f"gets exactly what a nonexistent application gets (HTTP {refused[0]}, byte-identical body)")

    print("\n--- FR-090: a gap is a gap, not a zero ---")
    _api("POST", f"/applications/{app_id}/suspend")
    time.sleep(INTERVAL + 3)
    stopped_at = datetime.now(timezone.utc)
    time.sleep(INTERVAL + 3)
    snapshot = _snapshot(app_id)
    after_stop = [p for p in snapshot["resource"] if vs._ts(p["timestamp"]) > stopped_at]
    _check(not after_stop, f"a stopped application produces no samples at all ({len(after_stop)} found)")
    _check(snapshot["collection"]["collecting"] is True and "nothing to sample" in snapshot["collection"]["note"],
           f"and the answer says why, rather than looking like an outage ({snapshot['collection']['note'][:60]})")

    print("\n--- the audit trail ---")
    reads = vo._platform_sql("SELECT count(*) FROM audit_log WHERE action LIKE '%metric%'")
    _check(reads == "0",
           "reading metrics is deliberately not audited: nothing in FR-091 asks for it, and CPU numbers carry "
           "nothing of what an application did (unlike logs, where every read is recorded)")

    print("\n--- deletion ---")
    _api("POST", f"/applications/{app_id}/archive")
    _api("POST", f"/applications/{app_id}/delete", {"confirm": True})
    left = subprocess.run(["docker", "ps", "-a", "--filter", f"label=platform.application_id={app_id}", "--format", "{{.Names}}"],
                          capture_output=True, text=True).stdout.strip()
    _check(not left, "no container of the deleted application is left")
    kept = vo._platform_sql(f"SELECT count(*) FROM resource_samples WHERE application_id = '{app_id}'")
    print(f"INFO: {kept} resource sample(s) of the deleted application remain: NFR-032's retention durations are TBD, "
          "so nothing is purged yet")

    print()
    if _failures:
        print(f"=== {len(_failures)} CHECK(S) FAILED ===")
        for f in _failures:
            print("  - " + f)
        return 1
    print("=== all Module T checks passed ===")
    return 0


if __name__ == "__main__":
    sys.exit(main())
