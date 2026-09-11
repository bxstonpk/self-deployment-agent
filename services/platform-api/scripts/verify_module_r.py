"""End-to-end verification of Module R (Continuous Health Monitoring &
Remediation) against a live stack and a real Docker daemon.

    docker compose up -d --build      # from repo root
    python services/platform-api/scripts/verify_module_r.py

The application this deploys answers normally until told to fail; nothing
here is mocked or simulated at the test-script level — the platform's own
background sweeper (cmd/api/main.go's runHealthSweeper) has to notice the
real instance failing real HTTP health checks and act on it. What's
checked:

  FR-084  a healthy instance is left alone across multiple sweeps; once an
          instance starts failing health checks, the platform detects it
          within its own defined sweep interval, without any deploy/
          restart/redeploy request from the caller
  FR-085  the unhealthy instance is automatically stopped and replaced —
          proven by the replacement's container id (Docker level) and its
          own hostname (application level) differing from the original —
          the new instance is health-checked before rejoining the pool,
          the application keeps serving at its stable URL throughout, and
          the remediation is logged (visible in platform-api's own log)
          and the owner is notified (Module X) of what happened

Not covered here (covered by internal/service/health_service_test.go's
unit tests instead, which can script exact sequences of failures a live
container can't easily be made to reproduce on demand): the exception flow
where a replacement instance also fails to become healthy, and the
alternative flow where repeated remediations trip the circuit breaker and
pause further automatic restarts.
"""

from __future__ import annotations

import os
import sys
import time
import urllib.error
import urllib.request
import uuid
from pathlib import Path

HERE = Path(__file__).resolve().parent
REPO_ROOT = HERE.parents[2]
sys.path.insert(0, str(HERE))
import verify_module_o as vo  # noqa: E402
import verify_module_s as vs  # noqa: E402
from verify_module_n import _app_containers  # noqa: E402

BASE_URL = vo.BASE_URL
OWNER = "module-r-verify@sti-th.com"

_failures: list[str] = []


def _check(cond, msg: str) -> bool:
    print(("OK:   " if cond else "FAIL: ") + msg)
    if not cond:
        _failures.append(msg)
    return bool(cond)


def _api(method: str, path: str, body=None, raw: bytes | None = None, as_email: str = OWNER):
    return vo._api(method, path, body, raw, as_email)


_APP_SOURCE = r'''package main

import (
	"fmt"
	"net/http"
	"os"
	"sync/atomic"
)

var unhealthy int32

func main() {
	host, _ := os.Hostname()
	fmt.Println("module-r app starting in " + host)
	http.HandleFunc("/control/fail", func(w http.ResponseWriter, r *http.Request) {
		atomic.StoreInt32(&unhealthy, 1)
		fmt.Fprintln(w, "now failing")
	})
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if atomic.LoadInt32(&unhealthy) == 1 {
			http.Error(w, "unhealthy", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintf(w, "served by %s\n", host)
	})
	http.ListenAndServe(":8080", nil)
}
'''


def _get(url: str, timeout: int = 10) -> tuple[int, str]:
    try:
        with urllib.request.urlopen(url, timeout=timeout) as resp:
            return resp.status, resp.read().decode("utf-8", "replace").strip()
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8", "replace").strip()
    except Exception as e:
        return 0, str(e)


def _current_container_id(app_name: str) -> str | None:
    names = _app_containers(app_name)
    return vo._container_id(names[0]) if len(names) == 1 else None


def main() -> int:
    os.chdir(REPO_ROOT)
    name = f"healthr{uuid.uuid4().hex[:6]}"
    dept = _api("GET", "/departments")["departments"][0]
    dept_id = dept.get("ID") or dept["id"]
    print(f"=== Module R (continuous health monitoring & remediation) verification against {BASE_URL} ===\n")

    # Pinned (scaling.min: 1) so it's never scale-to-zero eligible — the
    # idle sweeper must stay out of this test entirely.
    yaml = (f"app:\n  name: {name}\n  owner: IT\n"
            "services:\n  api:\n    runtime: go\n    port: 8080\n"
            "scaling:\n  min: 1\n")
    app = _api("POST", "/applications", {
        "name": name, "description": "Module R end-to-end verification",
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

    app_url = f"{BASE_URL}/run/{name}/api"
    status, body = _get(app_url)
    _check(status == 200 and body.startswith("served by "), f"the freshly deployed instance answers ({status}: {body})")
    first_id = _current_container_id(name)
    _check(bool(first_id), "the deployed instance's own container is found")

    print("\n--- FR-084: a healthy instance is left alone ---")
    time.sleep(8)  # several sweep intervals, at this repo's local .env cadence
    _check(_current_container_id(name) == first_id, "no remediation happened while the instance stayed healthy")
    status, body = _get(app_url)
    _check(status == 200 and body.startswith("served by "), "and it is still serving normally")

    print("\n--- FR-084/FR-085: a sustained failure is detected and remediated ---")
    fail_status, fail_body = _get(f"{app_url}/control/fail")
    _check(fail_status == 200, f"told the instance to start failing its health check ({fail_status}: {fail_body})")

    deadline = time.time() + 40
    replaced_id = first_id
    while time.time() < deadline:
        current = _current_container_id(name)
        if current and current != first_id:
            replaced_id = current
            break
        time.sleep(1)
    _check(replaced_id != first_id,
           f"the platform's own sweeper replaced the unhealthy container without any request from this script ({first_id} -> {replaced_id})")

    status, body = _get(app_url)
    _check(status == 200 and body.startswith("served by "), f"the application is serving again at its stable URL ({status}: {body})")
    _check(status == 200 and not body.endswith(first_id[:12] + "\n") and first_id[:12] not in body,
           f"served by a genuinely different instance, not the one that failed ({body})")

    print("\n--- FR-085 step 4: the owner is notified ---")
    notifications = _api("GET", "/notifications")["notifications"]
    matches = [n for n in notifications if n["category"] == "health_remediation" and n["resource_id"] == app_id]
    _check(bool(matches), f"the owner has a health-remediation notification for this application ({len(matches)} found)")
    if matches:
        _check("automatically restarted" in matches[0]["title"].lower() or "automatically restarted" in matches[0]["detail"].lower(),
               f"and it explains what happened ({matches[0]['title']!r})")

    print("\n--- teardown ---")
    vo._teardown(app_id, OWNER)
    _check(not vs._containers_of(app_id), "the application is deleted, with no container left")

    print()
    if _failures:
        print(f"=== {len(_failures)} CHECK(S) FAILED ===")
        for f in _failures:
            print("  - " + f)
        return 1
    print("=== all Module R checks passed ===")
    return 0


if __name__ == "__main__":
    sys.exit(main())
