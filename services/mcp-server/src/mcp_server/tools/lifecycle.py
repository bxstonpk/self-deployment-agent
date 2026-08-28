"""Section 13.13: delete_application.

Orchestrates TWO Platform API calls (Archive, then Delete) behind one MCP
tool, because Section 13.13 frames deletion as a single conceptual action
("moving it through Archived -> Deleted") while this Platform API keeps
Archive and Delete as separate, independently-preconditioned lifecycle
operations (Module K). This is the same category of MCP-layer
orchestration create_application already does (register + save
deployment.yaml as one tool call) — translating a coarser business-level
tool into the right sequence of finer platform calls, per Section 2's
"translates tool calls into Platform API calls" (plural).
"""

from __future__ import annotations

from typing import Any

from ..envelope import ErrorCode, ErrorDetail, ToolError, success
from ..idempotency import IdempotencyConflict, IdempotencyStore
from ..platform_client import PlatformClient

# Delete itself accepts these directly (see lifecycle_service.go's Delete)
# — Archive is only needed as an orchestrated first step when the
# application is 'running', the one state Delete does NOT accept even
# though Archive does. Deliberately narrower than "every status Archive
# would accept" — calling Archive on an already-'suspended' app would be a
# needless extra round trip Delete doesn't require.
_NEEDS_ARCHIVE_FIRST = {"running"}
_DIRECTLY_DELETABLE = {"archived", "suspended"}


async def delete_application(
    client: PlatformClient,
    idempotency: IdempotencyStore,
    application_id: str,
    confirmation: str,
    idempotency_key: str | None = None,
) -> dict[str, Any]:
    fingerprint = (application_id, confirmation)
    if idempotency_key:
        try:
            cached = idempotency.get_cached(idempotency_key, fingerprint)
        except IdempotencyConflict as exc:
            raise ToolError(ErrorCode.CONFLICT, str(exc)) from exc
        if cached is not None:
            return cached

    app = await client.get_application(application_id)
    app_name = app.get("name", "")
    # Section 13.13: "explicit confirmation required (a single ambiguous
    # instruction must never trigger deletion)" — checked here, in
    # ADDITION to Platform API's own confirm:true boolean, specifically so
    # an agent can't satisfy this by just always passing confirm=true; it
    # must have the actual application name in hand.
    if confirmation != app_name:
        raise ToolError(
            ErrorCode.VALIDATION_ERROR,
            "confirmation does not match the application's name — this must be "
            "an explicit, employee-confirmed instruction, never inferred",
            details=[ErrorDetail(field="confirmation", reason=f"expected {app_name!r}")],
        )

    status = app.get("lifecycle_status")
    if status in _NEEDS_ARCHIVE_FIRST:
        await client.archive_application(application_id)
    elif status not in _DIRECTLY_DELETABLE:
        raise ToolError(
            ErrorCode.VALIDATION_ERROR,
            f"application is in lifecycle state {status!r}, which is neither "
            "archivable nor directly deletable",
        )

    deleted = await client.delete_application(application_id)
    result = success(
        {
            "status": "DELETED",
            "application_id": application_id,
            "lifecycle_state": deleted.get("lifecycle_status"),
        }
    )
    if idempotency_key:
        idempotency.store(idempotency_key, fingerprint, result)
    return result
