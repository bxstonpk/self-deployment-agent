"""Section 13.4-13.5: create_application, validate_application."""

from __future__ import annotations

from typing import Any

from ..envelope import ErrorCode, ErrorDetail, ToolError, success
from ..idempotency import IdempotencyConflict, IdempotencyStore
from ..platform_client import PlatformClient


async def _resolve_department(client: PlatformClient, department_name: str) -> tuple[str, str, bool]:
    """Returns (department id, its name, whether it was just created).

    Any department is accepted: one that doesn't exist yet is created, the
    way registering under a new department name always created it — the
    platform has no fixed list. With no name given, the application belongs
    to the signed-in employee's own department.
    """
    name = " ".join((department_name or "").split())
    departments = await client.list_departments()
    if not name:
        me = await client.whoami()
        own = (me.get("user") or {}).get("department_id")
        for d in departments:
            if d["id"] == own:
                return d["id"], d["name"], False
        known = ", ".join(sorted(d["name"] for d in departments)) or "(none yet)"
        raise ToolError(
            ErrorCode.VALIDATION_ERROR,
            "no department given, and the signed-in account has none to default to",
            details=[ErrorDetail(field="department", reason=f"name one; existing departments: {known}")],
        )
    for d in departments:
        if d["name"].lower() == name.lower():
            return d["id"], d["name"], False
    created = await client.create_department(name)
    return created["id"], created["name"], True


async def create_application(
    client: PlatformClient,
    idempotency: IdempotencyStore,
    name: str,
    description: str,
    department: str = "",
    deployment_yaml: str | None = None,
    idempotency_key: str | None = None,
) -> dict[str, Any]:
    fingerprint = (name, description, department, deployment_yaml)
    if idempotency_key:
        try:
            cached = idempotency.get_cached(idempotency_key, fingerprint)
        except IdempotencyConflict as exc:
            raise ToolError(ErrorCode.CONFLICT, str(exc)) from exc
        if cached is not None:
            return cached

    department_id, department_name, department_created = await _resolve_department(client, department)
    app = await client.register_application(name, description, department_id)

    if deployment_yaml:
        app = await client.save_deployment_yaml(app["id"], deployment_yaml)

    result = success(
        {
            "application_id": app["id"],
            "status": app.get("lifecycle_status"),
            "created_at": app.get("created_at"),
            "deployment_yaml_draft": app.get("deployment_yaml_draft"),
            "department": department_name,
            "department_created": department_created,
        }
    )
    if idempotency_key:
        idempotency.store(idempotency_key, fingerprint, result)
    return result


async def validate_application(
    client: PlatformClient,
    application_id: str,
    deployment_yaml: str | None = None,
) -> dict[str, Any]:
    if deployment_yaml:
        await client.save_deployment_yaml(application_id, deployment_yaml)

    resp = await client.validate_application(application_id)
    report = resp.get("report", {})
    app = resp.get("application", {})

    # Section 13.5: "A failed validation itself is a normal successful tool
    # call carrying passed: false findings — not a transport-level error."
    return success(
        {
            "application_id": application_id,
            "validation_result": {
                "passed": report.get("valid", False),
                "findings": report.get("checks", []),
            },
            "lifecycle_state": app.get("lifecycle_status"),
        }
    )
