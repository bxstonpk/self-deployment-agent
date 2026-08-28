"""Section 13.6-13.8, 13.11-13.12: deploy_application, get_application_status,
get_deployment_status, rollback_application, restart_application.
"""

from __future__ import annotations

from typing import Any

from ..envelope import ErrorCode, ToolError, pending_approval, success
from ..idempotency import IdempotencyConflict, IdempotencyStore
from ..platform_client import PlatformClient

_NO_BUILD_MESSAGE = (
    "no successful build exists for this application. Section 13.6 of the MCP "
    "spec frames deploy_application as triggering Build -> Image Scan -> Deploy "
    "-> Health Check -> Traffic Activation, but this Platform API keeps Build as "
    "a separate step requiring a source-archive upload (POST "
    "/applications/{id}/build), which is NOT exposed as an MCP tool (it isn't "
    "one of the 12). Ask the employee to build via the Platform API/console "
    "directly first — this is a genuine, documented gap in the AI-agent path, "
    "not a transient failure worth retrying."
)


async def deploy_application(
    client: PlatformClient,
    idempotency: IdempotencyStore,
    application_id: str,
    target_environment: str,
    version_reference: str | None = None,
    idempotency_key: str | None = None,
) -> dict[str, Any]:
    # version_reference is accepted for shape-compliance with Section 13.6
    # but not enforced: this Platform API always deploys the application's
    # LATEST successful build — there is no "deploy this specific older
    # build" path (that's rollback_application, which takes an explicit
    # target). Silently accepting and ignoring it would hide the gap;
    # silently honoring only "latest"/None would be surprising if the
    # caller expected real version pinning. Documented here instead.
    fingerprint = (application_id, target_environment, version_reference)
    if idempotency_key:
        try:
            cached = idempotency.get_cached(idempotency_key, fingerprint)
        except IdempotencyConflict as exc:
            raise ToolError(ErrorCode.CONFLICT, str(exc)) from exc
        if cached is not None:
            return cached

    try:
        deployment = await client.deploy_application(application_id, target_environment)
    except ToolError as exc:
        if exc.platform_code == "no_successful_build":
            raise ToolError(ErrorCode.VALIDATION_ERROR, _NO_BUILD_MESSAGE) from exc
        raise

    status = deployment.get("status")
    # This Platform API's deploy pipeline runs synchronously within the
    # request (see platform-api/README.md's "What's deliberately NOT here
    # yet" — no background job/worker model). Section 9 wants an
    # IMMEDIATE ack with a queued/building phase and separate polling; here
    # the "ack" and the terminal result are the same call, because there is
    # no queue to ack into. get_deployment_status still works correctly
    # afterward (the deployment record is always queryable regardless of
    # how it got there) — only the "return before completion" property is
    # not real yet.
    data = {
        "deployment_id": deployment.get("id"),
        "application_id": application_id,
        "status": status,
        "note": (
            "this call already ran the full pipeline synchronously and "
            "returned its terminal result — see this tool's module doc "
            "comment for why 'immediate async ack' isn't real here yet"
        ),
    }

    if status == "pending_approval":
        result = pending_approval(data)
    else:
        result = success(data)

    if idempotency_key:
        idempotency.store(idempotency_key, fingerprint, result)
    return result


async def get_application_status(client: PlatformClient, application_id: str) -> dict[str, Any]:
    app = await client.get_application(application_id)
    latest_deployment_id = None
    url = None
    try:
        latest = await client.latest_deployment(application_id)
        latest_deployment_id = latest.get("id")
        if latest.get("status") == "running":
            containers = latest.get("containers") or {}
            urls = [c.get("url") for c in containers.values() if c.get("url")]
            url = urls[0] if urls else None
    except ToolError as exc:
        if exc.code != ErrorCode.NOT_FOUND:
            raise

    return success(
        {
            "application_id": application_id,
            "name": app.get("name"),
            "current_lifecycle_state": app.get("lifecycle_status"),
            "latest_deployment_id": latest_deployment_id,
            "url": url,
        }
    )


async def get_deployment_status(client: PlatformClient, deployment_id: str) -> dict[str, Any]:
    deployment = await client.get_deployment(deployment_id)
    phase_map = {
        "scanning": "IMAGE_SCAN",
        "pending_approval": "PENDING_APPROVAL",
        "deploying": "DEPLOYING",
        "health_check": "HEALTH_CHECK",
        "running": "COMPLETED",
        "failed": "FAILED",
        "rejected": "FAILED",
        "superseded": "COMPLETED",
        "suspended": "COMPLETED",
        "archived": "COMPLETED",
    }
    phase = phase_map.get(deployment.get("status", ""), "UNKNOWN")

    result_summary: dict[str, Any] = {}
    if phase == "COMPLETED" and deployment.get("status") == "running":
        containers = deployment.get("containers") or {}
        urls = [c.get("url") for c in containers.values() if c.get("url")]
        result_summary["url"] = urls[0] if urls else None
    if phase == "FAILED":
        result_summary["reason"] = deployment.get("failure_reason") or deployment.get("rejection_reason")

    return success(
        {
            "deployment_id": deployment_id,
            "application_id": deployment.get("application_id"),
            "phase": phase,
            "started_at": deployment.get("created_at"),
            "updated_at": deployment.get("updated_at"),
            "result": result_summary,
        }
    )


async def rollback_application(
    client: PlatformClient,
    idempotency: IdempotencyStore,
    application_id: str,
    target_version: str,
    idempotency_key: str | None = None,
) -> dict[str, Any]:
    fingerprint = (application_id, target_version)
    if idempotency_key:
        try:
            cached = idempotency.get_cached(idempotency_key, fingerprint)
        except IdempotencyConflict as exc:
            raise ToolError(ErrorCode.CONFLICT, str(exc)) from exc
        if cached is not None:
            return cached

    target_deployment_id = target_version
    if target_version.strip().lower() == "previous":
        history = await client.deployment_history(application_id)
        # history is newest-first (see platform-api's ListForApplication);
        # entry 0 is the current one, so the most recent PRIOR successful
        # deployment is the first "superseded"/"running" entry after it.
        candidates = [
            d for d in history[1:] if d.get("status") in ("running", "superseded")
        ]
        if not candidates:
            raise ToolError(
                ErrorCode.NOT_FOUND,
                'target_version="previous" requested, but this application has no '
                "prior successful deployment to roll back to",
            )
        target_deployment_id = candidates[0]["id"]

    deployment = await client.rollback_application(application_id, target_deployment_id)
    result = success(
        {
            "deployment_id": deployment.get("id"),
            "application_id": application_id,
            "status": deployment.get("status"),
            "target_version": target_deployment_id,
        }
    )
    if idempotency_key:
        idempotency.store(idempotency_key, fingerprint, result)
    return result


async def restart_application(
    client: PlatformClient,
    idempotency: IdempotencyStore,
    application_id: str,
    idempotency_key: str | None = None,
) -> dict[str, Any]:
    fingerprint = (application_id,)
    if idempotency_key:
        try:
            cached = idempotency.get_cached(idempotency_key, fingerprint)
        except IdempotencyConflict as exc:
            raise ToolError(ErrorCode.CONFLICT, str(exc)) from exc
        if cached is not None:
            return cached

    deployment = await client.restart_application(application_id)
    result = success(
        {
            "status": "COMPLETED",
            "application_id": application_id,
            "restarted_at": deployment.get("updated_at"),
        }
    )
    if idempotency_key:
        idempotency.store(idempotency_key, fingerprint, result)
    return result
