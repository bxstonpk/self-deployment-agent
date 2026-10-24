"""Development governance: the company standard from the moment an
application is started, not only when it is deployed.

    get_standards     what the company allows: stacks, rules, the libraries it doesn't allow
    start_project     register the application with its profile (what it is for, who uses it,
                      personal data, stack). Registering, not asking permission: nobody approves it.
    scaffold_project  the project folder from the company templates, already filled in
    check_project     the build's dependency check, plus what the deployment will need, without building
    request_exception ask the administrator to allow something outside the standard
    list_exceptions   the application's requests and their decisions

Every rule is the Platform API's: the build refuses an application with no
profile and a library the company doesn't allow, whatever this module says.
These tools let a project meet the standard while it is being written,
instead of finding out at deployment.
"""

from __future__ import annotations

import os
import shutil
from pathlib import Path
from typing import Any

import yaml

from ..envelope import ErrorCode, ErrorDetail, ToolError, success
from ..idempotency import IdempotencyConflict, IdempotencyStore
from ..platform_client import PlatformClient
from ..review import ReviewError, read_review_document
from ..source_packer import PackError, pack_project
from . import application

# Where the installer puts the skill's templates (deploy/gateway/client/install.*).
_DEFAULT_TEMPLATES = Path.home() / ".claude" / "skills" / "company-deployment" / "templates"

# What a template folder may carry that a new project mustn't copy.
_NOT_COPIED = shutil.ignore_patterns("node_modules", "build", "dist", "__pycache__", ".venv", "*.pyc")

_FRONTEND_TEMPLATES = {"react": "frontend-react"}
_API_TEMPLATES = {"python": "api-python", "nodejs": "api-nodejs", "typescript": "api-typescript", "go": "api-go"}


def templates_dir() -> Path:
    return Path(os.environ.get("COMPANY_DEPLOY_TEMPLATES") or _DEFAULT_TEMPLATES)


async def get_standards(client: PlatformClient) -> dict[str, Any]:
    st = await client.get_standards()
    stacks = st.get("stacks", [])
    return success({
        "rules": st.get("rules", []),
        "allowed_stacks": {
            kind: sorted(s["name"] for s in stacks if s.get("kind") == kind and s.get("status") == "active")
            for kind in ("frontend", "backend", "database")
        },
        "not_allowed_libraries": [
            {"ecosystem": r["ecosystem"], "package": r["package"], "reason": r["reason"]}
            for r in st.get("dependency_rules", [])
        ],
        "profile_required_to_build": st.get("profile_required_to_build", True),
        "exception_kinds": st.get("exception_kinds", ["dependency", "stack", "other"]),
        "how_to_start": (
            "start_project (registers the application with its profile), then scaffold_project "
            "(the company templates), then write the application, check_project as you go, and "
            "deploy_application when it's done. Anything outside these rules: request_exception "
            "first, and wait for the administrator's decision."
        ),
    })


def _deployment_yaml(name: str, department: str, frontend: str, api_runtime: str, database: str) -> str:
    """The standard deployment.yaml (templates/deployment.yaml), filled in."""
    return (
        "app:\n"
        f"  name: {name}\n"
        f"  owner: {department}\n"
        "services:\n"
        "  frontend:\n"
        f"    runtime: {frontend}\n"
        "  api:\n"
        f"    runtime: {api_runtime}\n"
        "    port: 8080\n"
        "database:\n"
        f"  type: {database}\n"
        "scaling:\n"
        "  min: 0\n"
        "  max: 1\n"
        "resources:\n"
        "  tier: small\n"
        "domain:\n"
        "  visibility: internal\n"
    )


def _require_allowed(stacks: list[dict[str, Any]], kind: str, name: str, field: str) -> None:
    allowed = sorted(s["name"] for s in stacks if s.get("kind") == kind and s.get("status") == "active")
    if name not in allowed:
        raise ToolError(
            ErrorCode.VALIDATION_ERROR,
            f"{name!r} is not a {kind} the company standard allows",
            details=[ErrorDetail(field=field, reason=(
                f"allowed: {', '.join(allowed) or '(none)'}. Something else needs request_exception "
                "(kind 'stack') and the administrator's approval first."
            ))],
        )


async def start_project(
    client: PlatformClient,
    idempotency: IdempotencyStore,
    name: str,
    purpose: str,
    handles_personal_data: bool,
    users: str = "",
    data_description: str = "",
    api_runtime: str = "",
    frontend: str = "react",
    database: str = "mysql",
    department: str = "",
    application_id: str = "",
    idempotency_key: str | None = None,
) -> dict[str, Any]:
    if not purpose.strip():
        raise ToolError(ErrorCode.VALIDATION_ERROR, "purpose is required: what the application is for")
    if handles_personal_data and not data_description.strip():
        raise ToolError(
            ErrorCode.VALIDATION_ERROR,
            "it handles personal data: say what data in data_description (e.g. names, employee IDs, phone numbers)",
        )
    fingerprint = (name, purpose, handles_personal_data, users, data_description, api_runtime, frontend, database,
                   department, application_id)
    if idempotency_key:
        try:
            cached = idempotency.get_cached(idempotency_key, fingerprint)
        except IdempotencyConflict as exc:
            raise ToolError(ErrorCode.CONFLICT, str(exc)) from exc
        if cached is not None:
            return cached

    created = False
    deployment_yaml = None
    dept_name = ""
    if application_id:
        # The profile of an application already registered: one created
        # before governance, or a retry after the profile didn't save.
        app = await client.get_application(application_id)
        name = app.get("name", name)
    else:
        if not api_runtime:
            raise ToolError(ErrorCode.VALIDATION_ERROR, "api_runtime is required: python, nodejs, typescript or go")
        st = await client.get_standards()
        stacks = st.get("stacks", [])
        _require_allowed(stacks, "frontend", frontend, "frontend")
        _require_allowed(stacks, "backend", api_runtime, "api_runtime")
        _require_allowed(stacks, "database", database, "database")
        _, dept_name, _ = await application._resolve_department(client, department)
        deployment_yaml = _deployment_yaml(name, dept_name, frontend, api_runtime, database)
        reg = await application.create_application(client, idempotency, name, purpose.strip()[:500], dept_name, deployment_yaml)
        application_id = reg["data"]["application_id"]
        created = True

    try:
        profile = await client.put_profile(application_id, {
            "purpose": purpose, "users": users, "handles_personal_data": handles_personal_data,
            "data_description": data_description, "frontend": frontend, "backend": api_runtime, "database": database,
        })
    except ToolError as exc:
        if not created:
            raise
        raise ToolError(exc.code, (
            f"{exc.message} — the application was registered ({application_id}) but its profile wasn't "
            f"saved; call start_project again with application_id={application_id}"
        ), exc.details, exc.platform_code) from exc

    result = success({
        "application_id": application_id,
        "name": name,
        "registered": created,
        "department": dept_name or None,
        "profile": profile,
        "deployment_yaml": deployment_yaml,
        "next": (
            "scaffold_project with this application_id and the project folder, then write the application. "
            "Run check_project as you go; deploy_application when it's done."
        ),
    })
    if idempotency_key:
        idempotency.store(idempotency_key, fingerprint, result)
    return result


def _runtimes(deployment_yaml: str) -> tuple[str, str]:
    try:
        parsed = yaml.safe_load(deployment_yaml or "") or {}
    except yaml.YAMLError as exc:
        raise ToolError(ErrorCode.VALIDATION_ERROR, f"the saved deployment.yaml doesn't parse: {exc}") from exc
    services = parsed.get("services") if isinstance(parsed, dict) else None
    if not isinstance(services, dict):
        raise ToolError(ErrorCode.VALIDATION_ERROR, "the application has no deployment.yaml with services; start it with start_project")
    frontend = str((services.get("frontend") or {}).get("runtime", ""))
    api = str((services.get("api") or {}).get("runtime", ""))
    return frontend, api


async def scaffold_project(
    client: PlatformClient, application_id: str, project_path: str, templates: Path | None = None,
) -> dict[str, Any]:
    templates = templates or templates_dir()
    if not templates.is_dir():
        raise ToolError(
            ErrorCode.VALIDATION_ERROR,
            f"the company templates aren't at {templates}: install the Company Deployment skill "
            "(the installer from the platform's /downloads/) or set COMPANY_DEPLOY_TEMPLATES",
        )
    project = Path(project_path).expanduser()
    if not project.is_absolute():
        raise ToolError(ErrorCode.VALIDATION_ERROR, "project_path must be an absolute path")
    if project.exists() and any(project.iterdir()):
        raise ToolError(
            ErrorCode.VALIDATION_ERROR,
            f"{project} isn't empty: scaffold_project only starts a new project, it never overwrites one",
        )

    app = await client.get_application(application_id)
    name = app.get("name", "")
    deployment_yaml = app.get("deployment_yaml_draft") or ""
    frontend, api = _runtimes(deployment_yaml)
    if frontend not in _FRONTEND_TEMPLATES or api not in _API_TEMPLATES:
        raise ToolError(
            ErrorCode.VALIDATION_ERROR,
            f"there is no company template for frontend {frontend!r} with API {api!r} "
            f"(templates: frontend {', '.join(_FRONTEND_TEMPLATES)}; API {', '.join(_API_TEMPLATES)})",
        )

    project.mkdir(parents=True, exist_ok=True)
    shutil.copytree(templates / _FRONTEND_TEMPLATES[frontend], project / "frontend", ignore=_NOT_COPIED)
    shutil.copytree(templates / _API_TEMPLATES[api], project / "api", ignore=_NOT_COPIED)
    (project / "deployment.yaml").write_text(deployment_yaml, encoding="utf-8")
    review = (templates / "REVIEW.md").read_text(encoding="utf-8").replace("<application-name>", name)
    (project / "REVIEW.md").write_text(review, encoding="utf-8")
    if (templates / "README.md").is_file():
        (project / "STANDARD.md").write_text((templates / "README.md").read_text(encoding="utf-8"), encoding="utf-8")

    files = sorted(str(p.relative_to(project)).replace(os.sep, "/") for p in project.rglob("*") if p.is_file())
    return success({
        "application_id": application_id,
        "project_path": str(project),
        "frontend": frontend,
        "api": api,
        "files": files,
        "next": (
            "Replace the template's example endpoints (GET/POST /items) with the application's own, keep "
            "the conventions in STANDARD.md, and fill in REVIEW.md as you go. Run check_project before deploying."
        ),
    })


async def check_project(client: PlatformClient, application_id: str, project_path: str) -> dict[str, Any]:
    project = Path(project_path).expanduser()
    app = await client.get_application(application_id)
    findings: list[dict[str, str]] = []

    services: list[str] = []
    try:
        parsed = yaml.safe_load(app.get("deployment_yaml_draft") or "") or {}
        services = [str(s) for s in (parsed.get("services") or {})] if isinstance(parsed, dict) else []
    except yaml.YAMLError:
        pass
    if not services:
        findings.append({"check": "deployment.yaml", "severity": "error",
                         "message": "the application has no saved deployment.yaml with services (start_project writes one)"})

    result: dict[str, Any] = {}
    packed: dict[str, Any] | None = None
    if services:
        try:
            archive, summary = pack_project(str(project), services)
            packed = summary.as_dict()
            result = await client.check_standards(application_id, archive)
        except PackError as exc:
            findings.append({"check": "source", "severity": "error", "message": str(exc)})

    for v in result.get("violations", []):
        findings.append({
            "check": "dependencies", "severity": "error",
            "message": (
                f"{v['ecosystem']} {v['package']!r} in {v['manifest']} isn't allowed: {v['reason']}. Remove it, "
                f"or request_exception kind 'dependency' subject {v['exception_subject']!r} and wait for the decision."
            ),
        })
    if result and not result.get("has_profile"):
        findings.append({
            "check": "profile", "severity": "error" if result.get("profile_required") else "warning",
            "message": f"no profile: call start_project with application_id={application_id}",
        })

    policy = await client.get_policy()
    gated = (policy or {}).get("approval_required_environments", ["production"]) if policy is not None else ["production"]
    try:
        review = read_review_document(str(project))
    except ReviewError as exc:
        review = None
        findings.append({"check": "REVIEW.md", "severity": "error", "message": str(exc)})
    else:
        if not (review or "").strip():
            findings.append({
                "check": "REVIEW.md", "severity": "error" if gated else "warning",
                "message": f"no REVIEW.md in the project root; deployments to {', '.join(gated) or 'no environment'} need one",
            })
        elif "<!--" in review or "<application-name>" in review:
            findings.append({"check": "REVIEW.md", "severity": "warning",
                             "message": "REVIEW.md still has the template's comments or placeholders: fill in every section"})

    exceptions = await client.list_exceptions(application_id)
    pending = [e for e in exceptions if e.get("status") == "pending"]
    for e in pending:
        findings.append({"check": "exceptions", "severity": "warning",
                         "message": f"waiting for the administrator: {e['kind']} {e['subject']}"})

    return success({
        "application_id": application_id,
        "passed": not any(f["severity"] == "error" for f in findings),
        "findings": findings,
        "dependencies_found": result.get("dependencies_found", 0),
        "allowed_by_exception": result.get("allowed_by_exception", []),
        "packed": packed,
    })


async def request_exception(
    client: PlatformClient, application_id: str, kind: str, subject: str, reason: str,
) -> dict[str, Any]:
    resp = await client.request_exception(application_id, kind, subject, reason)
    e = resp.get("exception", {})
    return success({
        "exception": e,
        "created": resp.get("created", False),
        "note": (
            "The administrator decides; the owners get a notification either way (list_notifications). "
            "Until it is approved, don't use what it asks for: a dependency stays blocked at build."
            if e.get("status") == "pending" else f"Already {e.get('status')}."
        ),
    })


async def list_exceptions(client: PlatformClient, application_id: str) -> dict[str, Any]:
    return success({"application_id": application_id, "exceptions": await client.list_exceptions(application_id)})
