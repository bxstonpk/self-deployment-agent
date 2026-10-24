"""End-to-end verification of the LAN development standard: a React
frontend, an API in each supported backend language, and a MySQL database —
built from the starter templates in company-deployment-skill/templates/
and deployed for real.

    docker compose up -d --build      # from repo root, with .env present
    python services/platform-api/scripts/verify_lan_stack.py

Nothing is mocked. For each of python, nodejs, typescript and go it
registers an application declaring `database: type: mysql`, uploads the
templates as its source, builds, deploys to dev, and then checks:

  - the API answers at its stable /run/{app}/api/ address, reports its own
    runtime, and reaches its database (it creates its table at start-up)
  - data really goes through MySQL: an item POSTed through the proxy comes
    back from GET /items
  - the React frontend is served at /run/{app}/frontend/, the bare address
    redirects there, and its relatively-addressed JavaScript bundle loads
  - the database container is MySQL, publishes no host port and sits only
    on the application's own network; a client off that network can't
    reach it
  - (once) rotating DATABASE_PASSWORD: the old password stops working, the
    new one works, the data survives and the restarted API reconnects
  - deleting the application removes the database container

Optionally, with PLAYWRIGHT_MODULE set to a Node Playwright install
(the path to its package directory), one frontend is also driven in a real
headless browser: the page renders, shows the API connected, and an item
typed into its form appears in the list.

It deploys the way a Claude Code machine does: with an API key fetched
through a device approval (see platform_auth.py), not a self-declared
identity.

Each application is also checked at its short address, /{app}/ (frontend)
and /{app}/api/ (API): through platform-api's own /app/ route always, and
through the gateway as an employee would reach it when GATEWAY_URL is set
(e.g. http://192.168.10.25) — where the Admin Portal's pages must still be
the Portal's.

Set PLATFORM_API_BASE_URL if the API is not on the .env default port, and
RUNTIMES (comma-separated) to verify a subset.
"""

from __future__ import annotations

import io
import json
import os
import subprocess
import sys
import tarfile
import time
import urllib.error
import urllib.request
import uuid
from pathlib import Path

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from platform_auth import api_key_for  # noqa: E402

BASE_URL = os.environ.get("PLATFORM_API_BASE_URL", "http://localhost:8099")
GATEWAY_URL = os.environ.get("GATEWAY_URL", "").rstrip("/")
EMPLOYEE_EMAIL = "lan-stack-verify@sti-th.com"
RUNTIMES = [r for r in os.environ.get("RUNTIMES", "python,nodejs,typescript,go").split(",") if r]
TEMPLATES = Path(__file__).resolve().parents[3] / "company-deployment-skill" / "templates"
# Never part of a build context: dependencies are installed and output is
# produced inside the build, from the lockfiles.
EXCLUDED = {"node_modules", "build", "dist", "__pycache__", ".git"}

_failures: list[str] = []
_api_key = ""


def _check(cond: bool, msg: str) -> bool:
    print(("OK:   " if cond else "FAIL: ") + msg, flush=True)
    if not cond:
        _failures.append(msg)
    return cond


def _api(method: str, path: str, body=None, raw: bytes | None = None, expect=(200, 201)):
    data = raw if raw is not None else (json.dumps(body).encode() if body is not None else None)
    req = urllib.request.Request(BASE_URL + path, data=data, method=method)
    req.add_header("Authorization", f"Bearer {_api_key}")
    if raw is None and data is not None:
        req.add_header("Content-Type", "application/json")
    elif raw is not None:
        req.add_header("Content-Type", "application/gzip")
    try:
        with urllib.request.urlopen(req, timeout=900) as resp:
            payload, status = resp.read(), resp.status
    except urllib.error.HTTPError as e:
        payload, status = e.read(), e.code
    parsed = json.loads(payload) if payload else {}
    if status not in expect:
        raise SystemExit(f"{method} {path} -> {status}: {json.dumps(parsed)[:800]}")
    return parsed


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


def _run(path: str, method: str = "GET", body=None, follow: bool = True, timeout: int = 60, base: str = ""):
    """A request to a deployed application, through the platform's proxy."""
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request((base or BASE_URL) + path, data=data, method=method)
    if data is not None:
        req.add_header("Content-Type", "application/json")
    opener = urllib.request.build_opener() if follow else urllib.request.build_opener(_NoRedirect)
    try:
        with opener.open(req, timeout=timeout) as resp:
            return resp.status, dict(resp.headers), resp.read()
    except urllib.error.HTTPError as e:
        return e.code, dict(e.headers), e.read()


def _docker(*args: str, check: bool = True) -> str:
    r = subprocess.run(["docker", *args], capture_output=True, text=True)
    if check and r.returncode != 0:
        raise SystemExit(f"docker {' '.join(args)} failed: {r.stderr.strip()}")
    return r.stdout.strip()


def _source_archive(runtime: str) -> bytes:
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w:gz") as tar:
        for service, folder in (("frontend", "frontend-react"), ("api", f"api-{runtime}")):
            root = TEMPLATES / folder
            for path in sorted(root.rglob("*")):
                rel = path.relative_to(root)
                if any(part in EXCLUDED for part in rel.parts) or not path.is_file():
                    continue
                tar.add(path, arcname=f"{service}/{rel.as_posix()}")
    return buf.getvalue()


def _deployment_yaml(app_name: str, runtime: str) -> str:
    text = (TEMPLATES / "deployment.yaml").read_text()
    text = text.replace("<application-name>", app_name).replace("<department>", "IT")
    text = text.replace("<python | nodejs | typescript | go>", runtime)
    # Pinned to one instance, so every check sees the same container rather
    # than racing a scale-to-zero sweep.
    return text.replace("  min: 0\n", "  min: 1\n")


def _deploy(dept_id: str, app_name: str, runtime: str) -> str | None:
    yaml = _deployment_yaml(app_name, runtime)
    app = _api("POST", "/applications", {
        "name": app_name,
        "description": f"LAN standard verification ({runtime})",
        "owning_department_id": dept_id,
        "deployment_yaml_draft": yaml,
    })
    app_id = app["id"]
    # Started the company way: a build refuses an application with no profile.
    _api("PUT", f"/applications/{app_id}/profile", {
        "purpose": f"LAN standard verification ({runtime})", "handles_personal_data": False,
        "frontend": "react", "backend": runtime, "database": "mysql",
    })
    _api("PUT", f"/applications/{app_id}/deployment-yaml", {"deployment_yaml": yaml})
    validation = _api("POST", f"/applications/{app_id}/validate")
    if not _check(validation.get("report", {}).get("valid") is True,
                  f"{runtime}: react + {runtime} + mysql validates against the live catalog"):
        print(json.dumps(validation, indent=2)[:1500])
        return None
    started = time.time()
    build = _api("POST", f"/applications/{app_id}/build", raw=_source_archive(runtime))
    if not _check(str(build.get("status")).lower() == "succeeded",
                  f"{runtime}: the templates build ({build.get('status')}, {time.time() - started:.0f}s)"):
        print(json.dumps(build, indent=2)[:3000])
        return None
    started = time.time()
    deployment = _api("POST", f"/applications/{app_id}/deploy", {"environment": "dev"})
    if not _check(str(deployment.get("status")).lower() == "running",
                  f"{runtime}: deployed and running ({deployment.get('status')} / "
                  f"{deployment.get('failure_reason')}, {time.time() - started:.0f}s)"):
        print(json.dumps(deployment, indent=2)[:3000])
        return None
    return app_id


def _wait_connected(app_name: str, timeout: float = 120) -> dict:
    deadline = time.time() + timeout
    last: dict = {}
    while time.time() < deadline:
        status, _, body = _run(f"/run/{app_name}/api/")
        if status == 200:
            last = json.loads(body)
            if last.get("database") == "connected":
                return last
        time.sleep(2)
    return last


def _db_container(app_name: str) -> str:
    return _docker("ps", "--filter", f"name=platform-db-{app_name}-", "--format", "{{.Names}}")


def _mysql(network: str, host: str, password: str, sql: str) -> subprocess.CompletedProcess:
    return subprocess.run(
        ["docker", "run", "--rm", "--network", network, "-e", f"MYSQL_PWD={password}", "mysql:8.4",
         "mysql", "-h", host, "-u", "appuser", "-D", "appdb", "-N", "-e", sql],
        capture_output=True, text=True, timeout=180)


def _api_container_env(app_name: str) -> dict:
    rows = _docker("ps", "--filter", "name=platform-run-", "--format", "{{.Names}}\t{{.Image}}")
    for row in rows.splitlines():
        name, image = row.split("\t", 1)
        if image.startswith(f"platform-build/{app_name}-") and "-api" in image:
            env = json.loads(_docker("inspect", "-f", "{{json .Config.Env}}", name))
            return dict(e.split("=", 1) for e in env if "=" in e)
    return {}


def _verify_app(app_name: str, runtime: str) -> None:
    health = _wait_connected(app_name)
    _check(health.get("runtime") == runtime and health.get("database") == "connected",
           f"{runtime}: /run/{app_name}/api/ answers as {health.get('runtime')!r}, database {health.get('database')!r}")

    title = f"hello from {runtime} {uuid.uuid4().hex[:6]}"
    status, _, body = _run(f"/run/{app_name}/api/items", "POST", {"title": title})
    _check(status == 201, f"{runtime}: POST /items through the proxy -> {status}")
    status, _, body = _run(f"/run/{app_name}/api/items")
    titles = [i.get("title") for i in json.loads(body)] if status == 200 else []
    _check(title in titles, f"{runtime}: the item comes back from MySQL via GET /items")

    status, headers, _ = _run(f"/run/{app_name}/frontend", follow=False)
    location = headers.get("Location") or headers.get("location")
    _check(status == 308 and location == f"/run/{app_name}/frontend/",
           f"{runtime}: the bare frontend address redirects to its slashed form ({status} -> {location})")
    status, _, html = _run(f"/run/{app_name}/frontend/")
    page = html.decode(errors="replace")
    script = page.split('src="./', 1)[1].split('"', 1)[0] if 'src="./' in page else ""
    _check(status == 200 and script.startswith("assets/"),
           f"{runtime}: the React page is served with a relative bundle path ({script or 'none'})")
    if script:
        status, _, js = _run(f"/run/{app_name}/frontend/{script}")
        _check(status == 200 and len(js) > 10_000, f"{runtime}: its bundle loads ({status}, {len(js)} bytes)")

    _verify_short_address(app_name, runtime, BASE_URL, "/app")
    if GATEWAY_URL:
        _verify_short_address(app_name, runtime, GATEWAY_URL, "")

    db = _db_container(app_name)
    image = _docker("inspect", "-f", "{{.Config.Image}}", db) if db else ""
    ports = _docker("inspect", "-f", "{{json .NetworkSettings.Ports}}", db) if db else ""
    networks = json.loads(_docker("inspect", "-f", "{{json .NetworkSettings.Networks}}", db)) if db else {}
    _check(image.startswith("mysql:"), f"{runtime}: the database container runs {image or 'nothing'}")
    # The image EXPOSEs 3306/33060, so they're listed — with no binding.
    # A published port would carry a HostPort.
    _check(bool(db) and "HostPort" not in ports, f"{runtime}: it publishes no host port ({ports})")
    _check(len(networks) == 1 and next(iter(networks)).startswith("platform-net-"),
           f"{runtime}: it is attached only to its application network ({list(networks)})")


def _verify_short_address(app_name: str, runtime: str, base: str, prefix: str) -> None:
    """/{app}/ is the frontend and /{app}/api/ the API. Through platform-api
    directly the gateway's /app prefix is added by hand; through the gateway
    it's the address an employee shares."""
    where = "gateway" if not prefix else "platform-api /app"
    status, headers, _ = _run(f"{prefix}/{app_name}", follow=False, base=base)
    location = headers.get("Location") or headers.get("location")
    _check(status == 308 and location == f"/{app_name}/",
           f"{runtime}: [{where}] /{app_name} redirects to /{app_name}/ ({status} -> {location})")
    status, _, body = _run(f"{prefix}/{app_name}/api/", base=base)
    health = json.loads(body) if status == 200 else {}
    _check(health.get("runtime") == runtime and health.get("database") == "connected",
           f"{runtime}: [{where}] /{app_name}/api/ answers as {health.get('runtime')!r}, database {health.get('database')!r}")
    status, _, html = _run(f"{prefix}/{app_name}/", base=base)
    page = html.decode(errors="replace")
    script = page.split('src="./', 1)[1].split('"', 1)[0] if 'src="./' in page else ""
    _check(status == 200 and script.startswith("assets/"),
           f"{runtime}: [{where}] /{app_name}/ serves the React page ({status}, {script or 'no bundle'})")
    if script:
        status, _, js = _run(f"{prefix}/{app_name}/{script}", base=base)
        _check(status == 200 and len(js) > 10_000, f"{runtime}: [{where}] its bundle loads at /{app_name}/{script} ({status})")
    if not prefix:
        status, _, portal = _run("/applications", base=base)
        _check(status == 200 and b'id="root"' in portal,
               f"[gateway] /applications is still the Admin Portal ({status})")


def _verify_isolation_and_rotation(app_id: str, app_name: str) -> None:
    db = _db_container(app_name)
    network = next(iter(json.loads(_docker("inspect", "-f", "{{json .NetworkSettings.Networks}}", db))))
    old_pw = _api_container_env(app_name).get("DATABASE_PASSWORD", "")

    inside = _mysql(network, db, old_pw, "SELECT COUNT(*) FROM items")
    _check(inside.returncode == 0, f"a client on {network} reads the items table ({inside.stdout.strip() or inside.stderr.strip()[:200]})")
    outside = _mysql("bridge", db, old_pw, "SELECT 1")
    _check(outside.returncode != 0, "the identical client on the default bridge cannot reach the database")

    rotated = _api("POST", f"/applications/{app_id}/secrets/DATABASE_PASSWORD/rotate")
    _check(rotated.get("restarted") is True, f"DATABASE_PASSWORD rotated; instances restarted ({rotated.get('restarted')})")
    new_pw = _api_container_env(app_name).get("DATABASE_PASSWORD", "")
    _check(bool(new_pw) and new_pw != old_pw, "the restarted API holds a new password")
    _check(_mysql(network, db, old_pw, "SELECT 1").returncode != 0, "the old password no longer signs in")
    _check(_mysql(network, db, new_pw, "SELECT 1").returncode == 0, "the new password signs in")

    health = _wait_connected(app_name)
    _check(health.get("database") == "connected", "the restarted API reconnects to MySQL")
    status, _, body = _run(f"/run/{app_name}/api/items")
    _check(status == 200 and len(json.loads(body)) >= 1, "the data written before rotation is still there")


def _verify_in_browser(app_name: str) -> None:
    module = os.environ.get("PLAYWRIGHT_MODULE", "")
    if not module:
        print("SKIP: browser check (set PLAYWRIGHT_MODULE to a Node Playwright package directory)")
        return
    item = f"from the browser {uuid.uuid4().hex[:6]}"
    script = f"""
const {{ chromium }} = require({json.dumps(module)});
(async () => {{
  const browser = await chromium.launch({{ args: ['--no-sandbox'] }});
  const page = await browser.newPage();
  await page.goto({json.dumps(f"{BASE_URL}/run/{app_name}/frontend")});
  await page.getByTestId('status').filter({{ hasText: 'database connected' }}).waitFor({{ timeout: 30000 }});
  await page.getByLabel('New item').fill({json.dumps(item)});
  await page.getByRole('button', {{ name: 'Add' }}).click();
  await page.getByText({json.dumps(item)}).waitFor({{ timeout: 15000 }});
  console.log(JSON.stringify({{ url: page.url(), status: await page.getByTestId('status').textContent() }}));
  await browser.close();
}})().catch((e) => {{ console.error(e.message); process.exit(1); }});
"""
    r = subprocess.run(["node", "-e", script], capture_output=True, text=True, timeout=120)
    _check(r.returncode == 0,
           f"a real browser renders the React app, sees the API connected and adds an item ({(r.stdout or r.stderr).strip()[:300]})")


def _exists(kind: str, name: str) -> bool:
    """docker {volume,network} inspect prints [] for a missing one, so only
    its exit code says whether it exists."""
    return subprocess.run(["docker", kind, "inspect", name], capture_output=True).returncode == 0


def _teardown(app_id: str, app_name: str) -> None:
    db = _db_container(app_name)
    volumes = _docker("inspect", "-f", "{{range .Mounts}}{{.Name}} {{end}}", db).split() if db else []
    networks = list(json.loads(_docker("inspect", "-f", "{{json .NetworkSettings.Networks}}", db))) if db else []
    _api("POST", f"/applications/{app_id}/suspend", expect=(200, 400, 409, 422))
    _api("POST", f"/applications/{app_id}/archive")
    _api("POST", f"/applications/{app_id}/delete", {"confirm": True})
    _check(_db_container(app_name) == "", f"{app_name}: deleting the application removed its database")
    # The data, not only the container: the database images keep their
    # files in an anonymous volume, which used to outlive the container.
    left = [v for v in volumes if _exists("volume", v)]
    _check(bool(volumes) and not left, f"{app_name}: ...and its data volume ({len(volumes)} before, {len(left)} left)")
    left = [n for n in networks if _exists("network", n)]
    _check(not left, f"{app_name}: ...and its private network ({left or 'gone'})")


def main() -> int:
    global _api_key
    _api_key = api_key_for(BASE_URL, EMPLOYEE_EMAIL, "verify_lan_stack.py")
    print(f"=== LAN standard verification against {BASE_URL} — runtimes {RUNTIMES} ===\n", flush=True)
    catalog = _api("GET", "/supported-stacks")
    names = {(s.get("kind") or s.get("Kind"), s.get("name") or s.get("Name")) for s in catalog.get("stacks", catalog.get("supported_stacks", []))}
    for kind, name in [("frontend", "react"), ("database", "mysql")] + [("backend", r) for r in RUNTIMES]:
        _check((kind, name) in names, f"the live catalog lists {kind} {name!r}")

    department = _api("GET", "/departments")["departments"][0]
    dept_id = department.get("ID") or department["id"]
    suffix = uuid.uuid4().hex[:6]

    deployed: list[tuple[str, str, str]] = []
    try:
        for runtime in RUNTIMES:
            print(f"\n--- {runtime} ---", flush=True)
            app_name = f"std-{runtime}-{suffix}"
            app_id = _deploy(dept_id, app_name, runtime)
            if app_id is None:
                continue
            deployed.append((app_id, app_name, runtime))
            _verify_app(app_name, runtime)
        if deployed:
            app_id, app_name, runtime = deployed[0]
            print(f"\n--- browser, isolation and rotation ({runtime}) ---", flush=True)
            _verify_in_browser(app_name)
            _verify_isolation_and_rotation(app_id, app_name)
    finally:
        if os.environ.get("KEEP_APPS") != "1":
            print("\n--- teardown ---", flush=True)
            for app_id, app_name, _ in deployed:
                _teardown(app_id, app_name)

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
