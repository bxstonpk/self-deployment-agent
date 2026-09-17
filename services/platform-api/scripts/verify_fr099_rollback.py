"""End-to-end verification of FR-099 (Automated Rollback on Failed Health
Check) against a live stack and a real Docker daemon.

    docker compose up -d --build      # from repo root
    python services/platform-api/scripts/verify_fr099_rollback.py

Deploys a genuinely healthy version (v1), then a second version (v2) whose
code passes its own pre-activation health check (FR-083) but then answers
unhealthy from its second request onward — deterministically, regardless
of which container instance is asked, so restarting it in place (Module
R's own first line of defense, FR-085) can never fix it. The platform's
own background health sweeper has to notice this for real and, because v2
was activated recently, escalate past Module R's restart-in-place and
circuit breaker into an actual automatic rollback — not simulated at the
test-script level. What's checked:

  FR-099  a recently-activated version that keeps failing health checks
          even across restarts gets automatically rolled back to the
          last known-good version, with no request from this script;
          the application keeps its stable URL serving throughout is
          the goal (a low-traffic window around the rollback itself is
          tolerated — Module R's own restart cycles briefly interrupt
          it too); the regressed version ends up Superseded, like any
          rollback; the owner is notified why
  FR-100  the automatic rollback picks a target that was itself a real,
          successfully completed deployment (v1), not an arbitrary one

Takes a couple of minutes: Module R's restart-in-place has to visibly
fail to fix v2 a few times (tripping its own circuit breaker) before
FR-099 escalates to a rollback — the same real background sweeper timing
verify_module_r.py exercises, not sped up here.
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
OWNER = "fr099-verify@sti-th.com"

_failures: list[str] = []


def _check(cond, msg: str) -> bool:
    print(("OK:   " if cond else "FAIL: ") + msg)
    if not cond:
        _failures.append(msg)
    return bool(cond)


def _api(method: str, path: str, body=None, raw: bytes | None = None, as_email: str = OWNER):
    return vo._api(method, path, body, raw, as_email)


_V1_SOURCE = r'''package main

import (
	"fmt"
	"net/http"
	"os"
)

func main() {
	host, _ := os.Hostname()
	fmt.Println("fr099 v1 (good) starting in " + host)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "v1 served by %s\n", host)
	})
	http.ListenAndServe(":8080", nil)
}
'''

# Healthy for its very first request (so it passes FR-083's pre-activation
# gate, once), unhealthy from the second request onward — deterministically,
# for THIS OR ANY OTHER instance of this same image. A restart produces a
# fresh process with its own counter, so it looks healthy again for exactly
# one more request before regressing the same way: restart-in-place can
# never permanently fix it, only delay the next detected failure by one
# sweep cycle, which is what should eventually trip Module R's circuit
# breaker and hand this off to FR-099.
_V2_SOURCE = r'''package main

import (
	"fmt"
	"net/http"
	"os"
	"sync/atomic"
)

var requests int32

func main() {
	host, _ := os.Hostname()
	fmt.Println("fr099 v2 (regresses after 1 request) starting in " + host)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&requests, 1) > 1 {
			http.Error(w, "unhealthy", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintf(w, "v2 served by %s\n", host)
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


def _register_and_build(dept_id: str, name: str, source: str) -> tuple[str, str]:
    """Pinned (scaling.min: 1): FR-099 is about a version regressing, not
    about scale-to-zero — keep the idle sweeper out of this entirely."""
    yaml = (f"app:\n  name: {name}\n  owner: IT\n"
            "services:\n  api:\n    runtime: go\n    port: 8080\n"
            "scaling:\n  min: 1\n")
    app = _api("POST", "/applications", {
        "name": name, "description": "FR-099 automatic rollback verification",
        "owning_department_id": dept_id, "deployment_yaml_draft": yaml,
    })
    app_id = app["id"]
    _api("PUT", f"/applications/{app_id}/deployment-yaml", {"deployment_yaml": yaml})
    if not _api("POST", f"/applications/{app_id}/validate").get("report", {}).get("valid"):
        raise SystemExit(f"{name}: validation failed")
    build = _api("POST", f"/applications/{app_id}/build", raw=vs._archive(source))
    if str(build.get("status")).lower() != "succeeded":
        raise SystemExit(f"{name}: build failed: {str(build)[:600]}")
    return app_id, build["id"]


def main() -> int:
    os.chdir(REPO_ROOT)
    name = f"fr099{uuid.uuid4().hex[:6]}"
    dept = _api("GET", "/departments")["departments"][0]
    dept_id = dept.get("ID") or dept["id"]
    print(f"=== FR-099 (automatic rollback) verification against {BASE_URL} ===\n")

    app_id, _v1_build_id = _register_and_build(dept_id, name, _V1_SOURCE)
    deployment = _api("POST", f"/applications/{app_id}/deploy", {"environment": "dev"})
    if str(deployment.get("status")).lower() != "running":
        raise SystemExit(f"v1 did not reach running: {deployment.get('failure_reason')}")
    v1_id = deployment["id"]
    app_url = f"{BASE_URL}/run/{name}/api"
    status, body = _get(app_url)
    _check(status == 200 and body.startswith("v1 served by "), f"v1 (the version to roll back to) is genuinely running ({status}: {body})")

    print("\n--- deploying v2, which will regress after its first request ---")
    # A second build for the same application, same flow verify_module_s.py
    # and others use for a redeploy.
    build2 = _api("POST", f"/applications/{app_id}/build", raw=vs._archive(_V2_SOURCE))
    if str(build2.get("status")).lower() != "succeeded":
        raise SystemExit(f"v2 build failed: {str(build2)[:600]}")
    deployment2 = _api("POST", f"/applications/{app_id}/deploy", {"environment": "dev"})
    if str(deployment2.get("status")).lower() != "running":
        raise SystemExit(f"v2 did not reach running: {deployment2.get('failure_reason')}")
    v2_id = deployment2["id"]
    _check(v2_id != v1_id, "v2 is a genuinely new deployment")
    # Not re-checked with a GET here: v2 is only healthy for its very FIRST
    # request ever, which the platform's own pre-activation health check
    # (already passed, or `deploy` above would have failed) just consumed —
    # a second request from this script would only prove the same 503 the
    # rest of this test relies on the platform observing for itself.

    print("\n--- waiting for the platform's own background sweeper to notice and roll back ---")
    print("(this takes a while: Module R has to fail to fix v2 a few times first)")
    deadline = time.time() + 150
    rolled_back_id = None
    while time.time() < deadline:
        history = _api("GET", f"/applications/{app_id}/deployments")
        newest = history[0] if history else {}
        if newest.get("id") not in (v1_id, v2_id) and str(newest.get("status")).lower() == "running":
            rolled_back_id = newest["id"]
            break
        time.sleep(3)
    _check(rolled_back_id is not None, "a new deployment (the automatic rollback) reached Running, with no request from this script")

    if rolled_back_id:
        rolled_back = _api("GET", f"/applications/{app_id}/deployments/latest")
        _check(rolled_back.get("build_id") == _v1_build_id, "the automatic rollback redeployed v1's build, not v2's or a new one (FR-100's target validation)")

        status, body = _get(app_url)
        _check(status == 200 and body.startswith("v1 served by "), f"the application is serving v1 again at its stable URL ({status}: {body})")

        history = _api("GET", f"/applications/{app_id}/deployments")
        v2_record = next((d for d in history if d["id"] == v2_id), {})
        _check(str(v2_record.get("status")).lower() == "superseded", f"v2 (the version that regressed) ended up Superseded, same as any rollback ({v2_record.get('status')})")

    print("\n--- and the owner was notified why ---")
    # TriggerAutomaticRollback's own notifications are scoped to the
    # deployment it created/acted on (resource_type "deployment"), not the
    # application — same convention deploy_service.go's other deployment-
    # outcome notifications already use, distinct from Module R's own
    # per-application escalation notifications. This test has only ever
    # created one application, so filtering by category alone is enough.
    notifications = _api("GET", "/notifications")["notifications"]
    matches = [n for n in notifications if n["category"] == "health_remediation"]
    rollback_mentions = [n for n in matches if "rollback" in n["title"].lower() or "rollback" in n["detail"].lower()]
    _check(bool(rollback_mentions), f"the owner has a notification mentioning the automatic rollback ({len(matches)} health_remediation notifications total)")

    print("\n--- teardown ---")
    vo._teardown(app_id, OWNER)
    _check(not vs._containers_of(app_id), "the application is deleted, with no container left")

    print()
    if _failures:
        print(f"=== {len(_failures)} CHECK(S) FAILED ===")
        for f in _failures:
            print("  - " + f)
        return 1
    print("=== all FR-099 checks passed ===")
    return 0


if __name__ == "__main__":
    sys.exit(main())
