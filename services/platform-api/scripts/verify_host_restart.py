"""Verifies the platform and a deployed application come back by themselves
after the host's Docker daemon restarts — what a reboot does to them.

Found on the first real LAN host: after a reboot the platform's own
containers stayed down (no restart policy in docker-compose.yml), and once
started by hand every application's API and frontend were replaced by the
health sweeper, but its MySQL container stayed stopped — the application
answered as healthy with its database disconnected, and its backup failed.

    # on the platform host, from the repo root, with the stack running
    PLATFORM_API_BASE_URL=http://127.0.0.1:8090 python3 services/platform-api/scripts/verify_host_restart.py

It deploys one application (React + RUNTIME, default go, + MySQL) from the
templates, writes an item, then runs RESTART_COMMAND (default
`sudo systemctl restart docker`, so it needs passwordless sudo) and checks:

  - the platform's own containers come back and the API answers again
  - the application's database container is running again, on its own
  - the application answers with its database connected, through the proxy
  - the item written before the restart is still there

Refuses to run if the daemon has live-restore enabled: containers would then
survive the restart and nothing would be proven. Restarting the daemon stops
every container on the host for a few seconds; don't run it on a host that
serves anything else.
"""

from __future__ import annotations

import json
import os
import shlex
import subprocess
import sys
import time
import urllib.request
import uuid
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import verify_lan_stack as vls  # noqa: E402
from platform_auth import api_key_for  # noqa: E402

RUNTIME = os.environ.get("RUNTIME", "go")
RESTART_COMMAND = os.environ.get("RESTART_COMMAND", "sudo -n systemctl restart docker")


def _platform_healthy(timeout: float = 180) -> bool:
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(vls.BASE_URL + "/healthz", timeout=5) as resp:
                if resp.status == 200:
                    return True
        except Exception:
            pass
        time.sleep(3)
    return False


def main() -> int:
    if vls._docker("info", "--format", "{{.LiveRestoreEnabled}}") == "true":
        raise SystemExit("the Docker daemon has live-restore enabled: containers survive a daemon restart, so this proves nothing")

    vls._api_key = api_key_for(vls.BASE_URL, vls.EMPLOYEE_EMAIL, "verify_host_restart.py")
    print(f"=== host restart verification against {vls.BASE_URL} ({RUNTIME}) ===\n", flush=True)
    department = vls._api("GET", "/departments")["departments"][0]
    dept_id = department.get("ID") or department["id"]
    app_name = f"restart-{RUNTIME}-{uuid.uuid4().hex[:6]}"

    app_id = vls._deploy(dept_id, app_name, RUNTIME)
    if app_id is None:
        return 1
    try:
        health = vls._wait_connected(app_name)
        vls._check(health.get("database") == "connected", f"before: {app_name} answers with its database connected")
        title = f"written before the restart {uuid.uuid4().hex[:6]}"
        status, _, _ = vls._run(f"/run/{app_name}/api/items", "POST", {"title": title})
        vls._check(status == 201, f"before: an item is written through the proxy ({status})")
        db = vls._db_container(app_name)
        policy = vls._docker("inspect", "-f", "{{.HostConfig.RestartPolicy.Name}}", db)
        vls._check(policy == "unless-stopped", f"before: the database container's restart policy is {policy!r}")

        print(f"\n--- {RESTART_COMMAND} ---", flush=True)
        started = time.time()
        r = subprocess.run(shlex.split(RESTART_COMMAND), capture_output=True, text=True, timeout=300)
        if r.returncode != 0:
            raise SystemExit(f"{RESTART_COMMAND} failed: {r.stderr.strip()}")

        vls._check(_platform_healthy(), f"after: the platform API answers again ({time.time() - started:.0f}s)")
        compose = vls._docker("ps", "--filter", "label=com.docker.compose.service", "--format", "{{.Names}}\t{{.Status}}")
        vls._check(compose.count("\tUp") >= 3, "after: the platform's postgres, platform-api and gateway are up\n      " + compose.replace("\n", "\n      "))
        db_state = vls._docker("inspect", "-f", "{{.State.Status}}", db, check=False)
        vls._check(db_state == "running", f"after: the database container is {db_state!r} without anyone starting it")
        health = vls._wait_connected(app_name, timeout=240)
        vls._check(health.get("database") == "connected",
                   f"after: {app_name} answers with its database connected ({json.dumps(health)[:120]}, {time.time() - started:.0f}s)")
        status, _, body = vls._run(f"/run/{app_name}/api/items")
        titles = [i.get("title") for i in json.loads(body)] if status == 200 else []
        vls._check(title in titles, "after: the item written before the restart is still there")
    finally:
        if os.environ.get("KEEP_APPS") != "1":
            print("\n--- teardown ---", flush=True)
            vls._teardown(app_id, app_name)

    print()
    if vls._failures:
        print(f"{len(vls._failures)} check(s) FAILED:")
        for f in vls._failures:
            print("  - " + f)
        return 1
    print("All checks passed.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
