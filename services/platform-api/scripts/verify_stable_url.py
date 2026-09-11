"""End-to-end verification that an application's reported URL is the
platform's own address, and that it survives the container behind it being
replaced.

    docker compose up -d --build      # from repo root
    python services/platform-api/scripts/verify_stable_url.py

The platform used to report `http://localhost:<hostPort>` — the port Docker
published for one particular container. That URL broke as soon as the
container was replaced, and it sent traffic straight past the proxy that
cold-starts a scaled-to-zero service (FR-053) and counts requests, errors
and latency (FR-090). What this checks, against a live stack:

  FR-053  every endpoint that reports a deployment reports
          {base}/run/{application}/{service}, and the published port is
          still available separately as host_port
  FR-053  the reported URL serves the application, and keeps serving it
          after a restart replaces the container and its published port —
          which the old URL would not have survived
  FR-090  requests to the reported URL are counted by Module T, because
          they pass through the platform rather than around it
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
import verify_module_t as vt  # noqa: E402

BASE_URL = vo.BASE_URL
OWNER = "stable-url-verify@sti-th.com"

_failures: list[str] = []


def _check(cond, msg: str) -> bool:
    print(("OK:   " if cond else "FAIL: ") + msg)
    if not cond:
        _failures.append(msg)
    return bool(cond)


def _api(method: str, path: str, body=None, raw: bytes | None = None):
    return vo._api(method, path, body, raw, OWNER)


_APP_SOURCE = r'''package main

import (
	"fmt"
	"net/http"
	"os"
)

func main() {
	host, _ := os.Hostname()
	fmt.Println("stable-url app starting in " + host)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// The container's own hostname is its id: proof of which instance
		// answered, not just that something did.
		fmt.Fprintf(w, "served by %s\n", host)
	})
	http.ListenAndServe(":8080", nil)
}
'''


def _get(url: str, timeout: int = 30) -> tuple[int, str]:
    try:
        with urllib.request.urlopen(url, timeout=timeout) as resp:
            return resp.status, resp.read().decode("utf-8", "replace").strip()
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8", "replace").strip()
    except Exception as e:
        return 0, str(e)


def _serve(url: str, attempts: int = 20) -> tuple[int, str]:
    """A just-restarted container may need a moment."""
    status, body = 0, ""
    for _ in range(attempts):
        status, body = _get(url)
        if status == 200:
            return status, body
        time.sleep(1)
    return status, body


def main() -> int:
    os.chdir(REPO_ROOT)
    name = f"stableurl{uuid.uuid4().hex[:6]}"
    dept = _api("GET", "/departments")["departments"][0]
    dept_id = dept.get("ID") or dept["id"]
    print(f"=== stable application URL verification against {BASE_URL} ===\n")

    yaml = (f"app:\n  name: {name}\n  owner: IT\n"
            "services:\n  api:\n    runtime: go\n    port: 8080\n"
            "scaling:\n  min: 1\n")
    app = _api("POST", "/applications", {
        "name": name, "description": "Stable URL verification",
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

    print("--- what the platform reports ---")
    expected = f"{BASE_URL}/run/{name}/api"
    reported = deployment["containers"]["api"]["url"]
    first_port = deployment["containers"]["api"]["host_port"]
    _check(reported == expected, f"the deploy response reports the platform's own address ({reported})")
    _check(isinstance(first_port, int) and first_port > 0,
           f"the container's published port is still reported separately, as host_port ({first_port})")
    _check(f"localhost:{first_port}" not in reported, "and the URL is not that port")

    for label, path in (("latest deployment", f"/applications/{app_id}/deployments/latest"),
                        ("deployment history", f"/applications/{app_id}/deployments")):
        body = _api("GET", path)
        record = body[0] if isinstance(body, list) else body
        _check(record["containers"]["api"]["url"] == expected, f"so does {label}")

    print("\n--- it serves the application ---")
    status, served = _serve(expected)
    _check(status == 200 and served.startswith("served by "), f"the reported URL answers ({status}: {served})")
    first_instance = served

    print("\n--- and survives the container being replaced ---")
    restarted = _api("POST", f"/applications/{app_id}/restart")
    second_port = restarted["containers"]["api"]["host_port"]
    _check(restarted["containers"]["api"]["url"] == expected,
           "after a restart, the platform reports the same URL as before")
    _check(second_port != first_port,
           f"even though the container's published port changed ({first_port} then {second_port})")

    status, served = _serve(expected)
    _check(status == 200 and served.startswith("served by "), f"the same URL still answers ({status})")
    _check(served != first_instance, f"from the new container ({first_instance} then {served})")

    # The URL the platform used to hand out: the previous container's port,
    # which is gone. Nothing should be serving this application there.
    old_status, old_body = _get(f"http://localhost:{first_port}/", timeout=5)
    _check(old_status != 200 or not old_body.startswith("served by "),
           f"the address the platform used to report is dead, as it always was ({old_status or 'unreachable'})")

    print("\n--- and the platform can see the traffic (FR-090) ---")
    for _ in range(3):
        _get(expected)
    time.sleep(vt.INTERVAL + 3)
    status, body, _text = vo._call("GET", f"/applications/{app_id}/metrics", as_email=OWNER)
    summary = (body or {}).get("summary", {})
    _check(status == 200 and summary.get("requests", 0) >= 4,
           f"requests to the reported URL are counted ({summary.get('requests')})")

    print("\n--- teardown ---")
    vo._teardown(app_id, OWNER)
    _check(not vs._containers_of(app_id), "the application is deleted, with no container left")

    print()
    if _failures:
        print(f"=== {len(_failures)} CHECK(S) FAILED ===")
        for f in _failures:
            print("  - " + f)
        return 1
    print("=== all stable-URL checks passed ===")
    return 0


if __name__ == "__main__":
    sys.exit(main())
