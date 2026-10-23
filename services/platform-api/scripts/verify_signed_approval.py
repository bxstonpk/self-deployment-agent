"""End-to-end verification of signed approvals (DEC-017) against a live
stack: a real approver key made by `company-deploy approver keygen`, real
production deployments, and decisions signed by `company-deploy approve`.

    python services/platform-api/scripts/verify_signed_approval.py

platform-api is restarted with PLATFORM_APPROVER_KEYS set to the key made
here, and restarted without it at the end. Checks:

  - a production deploy with a test report pauses for approval, and the
    approver is notified
  - the owner can't approve it the old way, and can't see the approval queue
  - `company-deploy approvals` lists it for the approver
  - a decision signed with any other key, or replayed for another
    deployment, is refused
  - a wrong passphrase signs nothing
  - `company-deploy approve` shows the test report, signs, and the
    deployment goes live; the audit log and the approval record name the
    signing key
  - `company-deploy approve --reject` rejects the next one
  - dev deployments still go straight through
"""

from __future__ import annotations

import base64
import io
import json
import os
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.request
import uuid
from pathlib import Path

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from platform_auth import api_key_for, call, session_for  # noqa: E402

BASE_URL = os.environ.get("PLATFORM_API_BASE_URL", "http://localhost:8099")
REPO = Path(__file__).resolve().parents[3]
CLI = REPO / "services" / "mcp-server" / ".venv" / "Scripts" / "company-deploy.exe"
if not CLI.exists():
    CLI = REPO / "services" / "mcp-server" / ".venv" / "bin" / "company-deploy"
_failures: list[str] = []


def _check(cond, msg):
    print(("OK:   " if cond else "FAIL: ") + msg, flush=True)
    if not cond:
        _failures.append(msg)
    return cond


def _restart(extra):
    subprocess.run(["docker", "compose", "up", "-d", "--force-recreate", "platform-api"], cwd=REPO,
                   env={**os.environ, **extra}, capture_output=True, check=True)
    for _ in range(90):
        try:
            urllib.request.urlopen(BASE_URL + "/healthz", timeout=5)
            return
        except Exception:
            time.sleep(1)


def _cli(args, env, stdin=None):
    r = subprocess.run([str(CLI), *args], capture_output=True, text=True, env={**os.environ, **env, "PYTHONIOENCODING": "utf-8"},
                       input=stdin, timeout=600)
    return r.returncode, r.stdout + r.stderr


def _deploy(key, app_id, env, report=None):
    body = {"environment": env}
    if report:
        body["test_report"] = report
    return call(BASE_URL, "POST", f"/applications/{app_id}/deploy", body, token=key)[1]


def main():
    suffix = uuid.uuid4().hex[:6]
    owner_email, admin_email = f"appr-owner-{suffix}@sti-th.com", f"appr-admin-{suffix}@sti-th.com"
    owner = session_for(BASE_URL, owner_email)
    admin_session = session_for(BASE_URL, admin_email)
    owner_key = api_key_for(BASE_URL, owner_email, "approval verify (owner)")
    tmp = Path(tempfile.mkdtemp(prefix="approver-"))
    admin_creds = tmp / "credentials.json"
    admin_creds.write_text(json.dumps({"platform_url": BASE_URL, "api_key": api_key_for(BASE_URL, admin_email, "admin laptop"),
                                       "key_id": "", "key_name": "admin laptop", "email": admin_email, "expires_at": ""}))
    key_file = tmp / "approver_ed25519.pem"
    admin_env = {"COMPANY_DEPLOY_CREDENTIALS": str(admin_creds), "COMPANY_DEPLOY_APPROVER_PASSPHRASE": "correct horse battery staple",
                 "PLATFORM_API_BASE_URL": BASE_URL}
    print(f"=== signed approval verification against {BASE_URL} ===\n", flush=True)

    code, out = _cli(["approver", "keygen", "--key", str(key_file)], admin_env)
    line = next((l.strip().split("=", 1)[1] for l in out.splitlines() if "PLATFORM_APPROVER_KEYS=" in l), "")
    pem = key_file.read_text() if key_file.exists() else ""
    _check(code == 0 and line.startswith(admin_email + "=ed25519:") and "ENCRYPTED PRIVATE KEY" in pem,
           "keygen writes a passphrase-encrypted private key and prints the public half for the platform")
    _restart({"PLATFORM_APPROVER_KEYS": line})

    app_id = None
    try:
        _, deps = call(BASE_URL, "GET", "/departments", token=owner_key)
        dept = deps["departments"][0].get("ID") or deps["departments"][0]["id"]
        name = f"appr-{suffix}"
        yaml = f"app:\n  name: {name}\n  owner: IT\nservices:\n  api:\n    runtime: go\n    port: 8080\nscaling:\n  min: 1\n"
        _, app = call(BASE_URL, "POST", "/applications", {"name": name, "description": "approvals", "owning_department_id": dept,
                                                          "deployment_yaml_draft": yaml}, token=owner_key)
        app_id = app["id"]
        call(BASE_URL, "POST", f"/applications/{app_id}/validate", token=owner_key)
        src = b'package main\nimport ("fmt";"net/http")\nfunc main(){http.HandleFunc("/",func(w http.ResponseWriter,r *http.Request){fmt.Fprintln(w,"approved build")});http.ListenAndServe(":8080",nil)}\n'
        buf = io.BytesIO()
        with tarfile.open(fileobj=buf, mode="w:gz") as tar:
            for fname, data in (("api/main.go", src), ("api/go.mod", b"module appr\n\ngo 1.25\n")):
                info = tarfile.TarInfo(fname)
                info.size = len(data)
                tar.addfile(info, io.BytesIO(data))
        req = urllib.request.Request(f"{BASE_URL}/applications/{app_id}/build", data=buf.getvalue(), method="POST",
                                     headers={"Authorization": f"Bearer {owner_key}", "Content-Type": "application/gzip"})
        urllib.request.urlopen(req, timeout=600).read()

        report = "go vet ./...  ok\ngo test ./...  ok  (12 tests)\nmanual check: / answers 200"
        dep = _deploy(owner_key, app_id, "production", report)
        dep_id = dep.get("id")
        _check(dep.get("status") == "pending_approval", f"a production deploy pauses for approval ({dep.get('status')})")
        _, notes = call(BASE_URL, "GET", "/notifications", token=admin_session)
        _check(any(dep_id in n.get("detail", "") and "signed approval" in n.get("title", "") for n in notes.get("notifications", [])),
               "the approver is told, with the command to run")

        status, body = call(BASE_URL, "POST", f"/deployments/{dep_id}/approve", {"decision": "approve"}, token=owner)
        _check(status == 403 and body["error"]["code"] == "signed_approval_required", f"the owner can't approve it the old way ({status})")
        status, _ = call(BASE_URL, "GET", "/approvals/pending", token=owner_key)
        _check(status == 403, f"the owner can't see the approval queue ({status})")

        code, out = _cli(["approvals"], admin_env)
        _check(code == 0 and dep_id in out and name in out, "company-deploy approvals lists it for the approver")

        _, req = call(BASE_URL, "GET", f"/approvals/{dep_id}", token=admin_session)
        from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
        sys.path.insert(0, str(REPO / "services" / "mcp-server" / "src"))
        from mcp_server import approver as approver_lib
        signed_at, forged = approver_lib.sign(Ed25519PrivateKey.generate(), req["signing_template"], "approve", "")
        status, body = call(BASE_URL, "POST", f"/approvals/{dep_id}/decision",
                            {"decision": "approve", "reason": "", "signed_at": signed_at, "signature": forged}, token=admin_session)
        _check(status == 403 and body["error"]["code"] == "invalid_signature", f"a decision signed with another key is refused ({status})")

        code, out = _cli(["approve", dep_id, "--yes", "--reason", "tests reviewed", "--key", str(key_file)],
                         {**admin_env, "COMPANY_DEPLOY_APPROVER_PASSPHRASE": "wrong passphrase"})
        _check(code != 0 and "Could not open the approver key" in out and "No such file" not in out, "a wrong passphrase signs nothing")
        _, still = call(BASE_URL, "GET", f"/deployments/{dep_id}", token=owner_key)
        _check(still.get("status") == "pending_approval", "and the deployment is still waiting")

        code, out = _cli(["approve", dep_id, "--yes", "--reason", "tests reviewed", "--key", str(key_file)], admin_env)
        print("    " + out.strip().replace("\n", "\n    "))
        _check(code == 0 and "go test ./...  ok" in out and "now running" in out,
               "company-deploy approve shows the test report, signs, and the deployment goes live")
        page = urllib.request.urlopen(f"{BASE_URL}/run/{name}/api/", timeout=60).read().decode()
        _check("approved build" in page, "the approved build answers at its address")
        _, audit = call(BASE_URL, "GET", f"/audit-log?resource_type=deployment&resource_id={dep_id}", token=owner_key)
        details = [e.get("detail", "") for e in audit.get("entries", [])]
        _check(any("signed with key SHA256:" in d for d in details), f"the audit log names the signing key ({details[:2]})")
        row = subprocess.run(["docker", "compose", "exec", "-T", "postgres", "psql", "-U", "postgres", "-d", "platform", "-tAc",
                              f"select signer_fingerprint is not null, signed_at is not null, length(signature), length(test_report) from deployment_approvals where deployment_id = '{dep_id}'"],
                             cwd=REPO, capture_output=True, text=True).stdout.strip()
        _check(row.startswith("t|t|88|"), f"the approval record keeps the signer, time, signature and test report ({row})")

        _, sigrow = call(BASE_URL, "GET", f"/approvals/{dep_id}", token=admin_session)
        status, body = call(BASE_URL, "POST", f"/approvals/{dep_id}/decision",
                            {"decision": "approve", "reason": "tests reviewed", "signed_at": signed_at, "signature": forged}, token=admin_session)
        _check(status in (403, 409), f"a decided deployment can't be decided again ({status})")

        dep2 = _deploy(owner_key, app_id, "production", "no tests run")
        code, out = _cli(["approve", dep2["id"], "--reject", "--reason", "please run the test suite", "--yes", "--key", str(key_file)], admin_env)
        _, after = call(BASE_URL, "GET", f"/deployments/{dep2['id']}", token=owner_key)
        _check(code == 0 and after.get("status") == "rejected", f"approve --reject rejects it ({after.get('status')})")

        dev = _deploy(owner_key, app_id, "dev")
        _check(dev.get("status") == "running", f"a dev deployment still goes straight through ({dev.get('status')})")
    finally:
        if app_id:
            call(BASE_URL, "POST", f"/applications/{app_id}/suspend", token=owner_key)
            call(BASE_URL, "POST", f"/applications/{app_id}/archive", token=owner_key)
            call(BASE_URL, "POST", f"/applications/{app_id}/delete", {"confirm": True}, token=owner_key)
        _restart({})

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
