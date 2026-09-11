"""Section 13.9-13.10: get_application_logs, get_application_metrics.

get_application_logs reads Module S's central log store through the Platform
API (GET /applications/{id}/logs): the lines an application's containers
wrote, newest first, tagged with service, stream and instance, with every
secret value the platform injected already redacted at collection time. A
caller who isn't an owner gets NOT_FOUND — the Platform API won't confirm
the application exists (FR-087).

get_application_metrics still has nothing to call: Module T (Monitoring)
doesn't exist, and the tool says so rather than inventing numbers.
"""

from __future__ import annotations

import re
from datetime import datetime, timedelta, timezone
from typing import Any

from ..envelope import ErrorCode, ErrorDetail, ToolError, success
from ..platform_client import PlatformClient

_METRICS_MESSAGE = (
    "application metrics are not implemented — Module T (Monitoring) does not "
    "exist in the Platform API. There is no metrics storage anywhere to query. "
    "This is a persistent gap, not a transient failure; do not retry."
)

_DEFAULT_LINES = 200
_MAX_LINES = 1000
_DURATION = re.compile(r"^(\d+)([smhd])$")
_UNIT_SECONDS = {"s": 1, "m": 60, "h": 3600, "d": 86400}


def _since(time_range: str | None) -> str | None:
    if not time_range:
        return None
    match = _DURATION.match(time_range.strip())
    if not match:
        raise ToolError(
            ErrorCode.VALIDATION_ERROR,
            "time_range must be a duration such as 15m, 1h, 24h or 7d",
            details=[ErrorDetail(field="time_range", reason="expected <number><s|m|h|d>")],
        )
    seconds = int(match.group(1)) * _UNIT_SECONDS[match.group(2)]
    return (datetime.now(timezone.utc) - timedelta(seconds=seconds)).isoformat()


async def get_application_logs(
    client: PlatformClient,
    application_id: str,
    environment: str,
    tail_lines: int | None = None,
    time_range: str | None = None,
    service: str | None = None,
    severity: str | None = None,
    cursor: str | None = None,
) -> dict[str, Any]:
    limit = tail_lines if tail_lines is not None else _DEFAULT_LINES
    if not 1 <= limit <= _MAX_LINES:
        raise ToolError(
            ErrorCode.VALIDATION_ERROR,
            f"tail_lines must be between 1 and {_MAX_LINES}",
            details=[ErrorDetail(field="tail_lines", reason=f"got {tail_lines}")],
        )
    page = await client.get_logs(application_id, {
        "environment": environment or None,
        "service": service,
        "since": _since(time_range),
        "cursor": cursor,
        "limit": limit,
    })
    entries = page.get("entries") or []

    notes = ["Newest first. Log levels are not parsed; each entry carries its stream (stdout or stderr) instead."]
    if severity:
        notes.append(f"severity={severity!r} was not applied — there are no parsed levels to filter on.")
    return success({
        "application_id": application_id,
        "entries": [
            {
                "timestamp": e.get("timestamp"),
                "service": e.get("service"),
                "level": None,
                "stream": e.get("stream"),
                "instance": e.get("instance"),
                "message": e.get("message"),
            }
            for e in entries
        ],
        # Pass back as `cursor` for the next, older page.
        "next_cursor": page.get("next_cursor"),
        "note": " ".join(notes),
    })


async def get_application_metrics(
    application_id: str,
    environment: str,
    time_range: str | None = None,
    metric_types: list[str] | None = None,
) -> dict[str, Any]:
    raise ToolError(ErrorCode.INTERNAL_ERROR, _METRICS_MESSAGE)
