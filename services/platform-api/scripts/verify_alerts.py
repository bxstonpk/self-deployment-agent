"""End-to-end verification of alerting (FR-092, DEC-028) against a live
stack, a real deployed application and real traffic through the
platform's proxy.

    python services/platform-api/scripts/verify_alerts.py

It restarts platform-api with short windows so the run takes minutes,
not hours, and puts the normal settings back at the end:

    evaluation every 5s, traffic window 120s, CPU window 40s,
    reminders every 45s, disk warning at 1% (so the platform-level rule
    fires on any real disk), PLATFORM_ADMIN_EMAILS = a verification admin

The thresholds themselves are the decided ones (set B). It checks, with
the notifications each employee actually receives:

  - latency: slow responses -> "Warning: <app> latency"; diluted -> "Resolved"
  - error rate: 7% 5xx -> warning; more failures -> "Now critical" (once);
    silence while it lasts; restarting platform-api doesn't announce it
    again; after the reminder interval -> "Still critical" (once);
    healthy traffic -> "Resolved"
  - CPU: a real busy loop in the container, sustained -> a CPU alert,
    then "Resolved" when it stops
  - disk: the platform-level alert reaches the admin, not the owner
  - GET /applications/{id}/alerts shows what's firing and the history,
    to an owner only
"""

from __future__ import annotations

import io
import json
import os
import subprocess
import sys
import tarfile
import threading
import time
import urllib.error
import urllib.request
import uuid

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from platform_auth import api_key_for, call, session_for  # noqa: E402

BASE_URL = os.environ.get("PLATFORM_API_BASE_URL", "http://localhost:8099")
REPO = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "..", ".."))
_failures: list[str] = []

TEST_ENV = {
    "ALERT_EVAL_INTERVAL_SECONDS": "5", "ALERT_TRAFFIC_WINDOW_SECONDS": "120", "ALERT_CPU_WINDOW_SECONDS": "40",
    "ALERT_REMINDER_SECONDS": "45", "ALERT_DISK_WARNING_PERCENT": "1", "METRICS_SAMPLE_INTERVAL_SECONDS": "5",
}

APP = r'''package main

import (
	"fmt"
	"net/http"
	"strconv"
	"time"
)

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "ok") })
	http.HandleFunc("/fail", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "boom", 500) })
	http.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		ms, _ := strconv.Atoi(r.URL.Query().Get("ms"))
		time.Sleep(time.Duration(ms) * time.Millisecond)
		fmt.Fprintln(w, "slow")
	})
	http.HandleFunc("/burn", func(w http.ResponseWriter, r *http.Request) {
		s, _ := strconv.Atoi(r.URL.Query().Get("seconds"))
		go func() {
			end := time.Now().Add(time.Duration(s) * time.Second)
			x := 0
			for time.Now().Before(end) {
				x++
			}
		}()
		fmt.Fprintln(w, "burning")
	})
	http.ListenAndServe(":8080", nil)
}
'''


def _check(cond, msg):
    print(("OK:   " if cond else "FAIL: ") + msg, flush=True)
    if not cond:
        _failures.append(msg)
    return cond


def _restart_platform(extra_env):
    env = {**os.environ, **extra_env}
    subprocess.run(["docker", "compose", "up", "-d", "--force-recreate", "platform-api"], cwd=REPO, env=env,
                   capture_output=True, check=True)
    for _ in range(90):
        try:
            urllib.request.urlopen(BASE_URL + "/healthz", timeout=5)
            return
        except Exception:
            time.sleep(1)


def _hit(path, n, parallel=8):
    def one():
        try:
            urllib.request.urlopen(BASE_URL + path, timeout=60).read()
        except urllib.error.HTTPError:
            pass
    threads = []
    for i in range(n):
        t = threading.Thread(target=one)
        t.start()
        threads.append(t)
        if len(threads) >= parallel:
            for t in threads:
                t.join()
            threads = []
    for t in threads:
        t.join()


def _titles(token, app_name=None):
    _, body = call(BASE_URL, "GET", "/notifications", token=token)
    out = [n["title"] for n in body.get("notifications", []) if n.get("category") == "alert"]
    return [t for t in out if app_name is None or app_name in t]


def _wait_for(token, needle, app_name=None, timeout=150):
    deadline = time.time() + timeout
    while time.time() < deadline:
        titles = _titles(token, app_name)
        if any(needle in t for t in titles):
            return titles
        time.sleep(2)
    return _titles(token, app_name)


def main():
    suffix = uuid.uuid4().hex[:6]
    owner_email, admin_email = f"alert-owner-{suffix}@sti-th.com", f"alert-admin-{suffix}@sti-th.com"
    owner = session_for(BASE_URL, owner_email)
    admin = session_for(BASE_URL, admin_email)
    key = api_key_for(BASE_URL, owner_email, "verify_alerts.py")
    print(f"=== alert verification against {BASE_URL} ===", flush=True)
    _restart_platform({**TEST_ENV, "PLATFORM_ADMIN_EMAILS": admin_email})
    app_id = None
    try:
        _, deps = call(BASE_URL, "GET", "/departments", token=key)
        dept = deps["departments"][0].get("ID") or deps["departments"][0]["id"]
        name = f"alerts-{suffix}"
        yaml = f"app:\n  name: {name}\n  owner: IT\nservices:\n  api:\n    runtime: go\n    port: 8080\nscaling:\n  min: 1\n"
        _, app = call(BASE_URL, "POST", "/applications", {"name": name, "description": "alerts", "owning_department_id": dept,
                                                          "deployment_yaml_draft": yaml}, token=key)
        app_id = app["id"]
        call(BASE_URL, "POST", f"/applications/{app_id}/validate", token=key)
        buf = io.BytesIO()
        with tarfile.open(fileobj=buf, mode="w:gz") as tar:
            for fname, content in (("api/main.go", APP), ("api/go.mod", "module alerts\n\ngo 1.25\n")):
                data = content.encode()
                info = tarfile.TarInfo(fname)
                info.size = len(data)
                tar.addfile(info, io.BytesIO(data))
        req = urllib.request.Request(f"{BASE_URL}/applications/{app_id}/build", data=buf.getvalue(), method="POST",
                                     headers={"Authorization": f"Bearer {key}", "Content-Type": "application/gzip"})
        urllib.request.urlopen(req, timeout=600).read()
        status, dep = call(BASE_URL, "POST", f"/applications/{app_id}/deploy", {"environment": "dev"}, token=key)
        if dep.get("status") != "running":
            raise SystemExit(f"deploy failed: {dep}")
        run = f"/run/{name}/api"

        print("\n--- latency ---", flush=True)
        _hit(f"{run}/slow?ms=1500", 6)
        titles = _wait_for(owner, "Warning: " + name + " latency", name)
        _check(any(t == f"Warning: {name} latency" for t in titles), f"slow responses raise a latency warning ({titles})")

        print("\n--- error rate ---", flush=True)
        _hit(f"{run}/", 300)
        _hit(f"{run}/fail", 25)
        titles = _wait_for(owner, f"Resolved: {name} latency", name)
        _check(f"Resolved: {name} latency" in titles, "diluted with fast requests, the latency alert resolves")
        titles = _wait_for(owner, f"Warning: {name} error_rate", name)
        _check(f"Warning: {name} error_rate" in titles, "7-8% 5xx raises an error-rate warning")
        _hit(f"{run}/fail", 120)
        titles = _wait_for(owner, f"Now critical: {name} error_rate", name)
        _check(titles.count(f"Now critical: {name} error_rate") == 1, "more failures escalate it to critical, announced once")

        before = len(_titles(owner, name))
        _restart_platform({**TEST_ENV, "PLATFORM_ADMIN_EMAILS": admin_email})
        time.sleep(20)
        _check(len(_titles(owner, name)) == before, "restarting platform-api doesn't announce a firing alert again")
        titles = _wait_for(owner, f"Still critical: {name} error_rate", name, timeout=90)
        _check(titles.count(f"Still critical: {name} error_rate") == 1, "after the reminder interval, one reminder")

        _, alerts = call(BASE_URL, "GET", f"/applications/{app_id}/alerts", token=owner)
        active = [a["rule"] for a in alerts.get("active", [])]
        kinds = [e["kind"] for e in alerts.get("recent", []) if e["rule"] == "error_rate"]
        _check("error_rate" in active and kinds[:3] == ["reminded", "escalated", "fired"],
               f"the owner's alerts endpoint shows it firing, with its history ({active}, {kinds})")
        stranger = session_for(BASE_URL, f"alert-stranger-{suffix}@sti-th.com")
        status, _ = call(BASE_URL, "GET", f"/applications/{app_id}/alerts", token=stranger)
        _check(status == 404, f"another employee can't read it ({status})")

        _hit(f"{run}/", 3000, parallel=16)
        titles = _wait_for(owner, f"Resolved: {name} error_rate", name)
        _check(f"Resolved: {name} error_rate" in titles, "healthy traffic resolves it")

        print("\n--- CPU ---", flush=True)
        _hit(f"{run}/burn?seconds=75", 1)
        titles = _wait_for(owner, f"{name} cpu", name, timeout=120)
        fired = [t for t in titles if t.endswith(f"{name} cpu") and not t.startswith("Resolved")]
        _check(bool(fired), f"a sustained busy loop raises a CPU alert ({fired})")
        titles = _wait_for(owner, f"Resolved: {name} cpu", name, timeout=150)
        _check(f"Resolved: {name} cpu" in titles, "when it stops, the CPU alert resolves")

        print("\n--- platform-level ---", flush=True)
        admin_titles = _wait_for(admin, "platform host_disk", timeout=60)
        _check(any("platform host_disk" in t for t in admin_titles), f"the disk alert reaches the admin ({admin_titles[:2]})")
        _check(not any("host_disk" in t for t in _titles(owner)), "and not an application owner")
    finally:
        if app_id:
            call(BASE_URL, "POST", f"/applications/{app_id}/suspend", token=key)
            call(BASE_URL, "POST", f"/applications/{app_id}/archive", token=key)
            call(BASE_URL, "POST", f"/applications/{app_id}/delete", {"confirm": True}, token=key)
        _restart_platform({})

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
