"""Section 13.9-13.10: get_application_logs, get_application_metrics.

get_application_logs reads Module S's central log store through the Platform
API (GET /applications/{id}/logs): the lines an application's containers
wrote, newest first, tagged with service, stream and instance, with every
secret value the platform injected already redacted at collection time. A
caller who isn't an owner gets NOT_FOUND — the Platform API won't confirm
the application exists (FR-087).

get_application_metrics reads Module T's two series through the Platform API
(GET /applications/{id}/metrics): CPU and memory sampled from the container
runtime, and requests, errors and latency counted at the platform's proxy.
Neither asks the application to instrument anything. The answer carries the
platform's own account of whether collection is keeping up, so an empty
window is never passed off as a quiet application.
"""

from __future__ import annotations

import re
from datetime import datetime, timedelta, timezone
from typing import Any

from ..envelope import ErrorCode, ErrorDetail, ToolError, success
from ..platform_client import PlatformClient

_METRIC_TYPES = ("cpu", "memory", "requests", "errors", "latency")

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
    client: PlatformClient,
    application_id: str,
    environment: str,
    time_range: str | None = None,
    metric_types: list[str] | None = None,
) -> dict[str, Any]:
    wanted = [t.strip().lower() for t in (metric_types or _METRIC_TYPES)]
    unknown = [t for t in wanted if t not in _METRIC_TYPES]
    if unknown:
        raise ToolError(
            ErrorCode.VALIDATION_ERROR,
            f"unknown metric type(s): {', '.join(unknown)}",
            details=[ErrorDetail(field="metric_types", reason=f"supported: {', '.join(_METRIC_TYPES)}")],
        )
    page = await client.get_metrics(application_id, {
        "environment": environment or None,
        "from": _since(time_range),
    })
    resource = page.get("resource") or []
    traffic = page.get("traffic") or []

    series: dict[str, Any] = {}
    if "cpu" in wanted:
        series["cpu_percent"] = [
            {"timestamp": p["timestamp"], "service": p["service"], "instance": p["instance"], "value": p["cpu_percent"]}
            for p in resource
        ]
    if "memory" in wanted:
        series["memory_bytes"] = [
            {"timestamp": p["timestamp"], "service": p["service"], "instance": p["instance"],
             "value": p["memory_bytes"], "limit": p["memory_limit_bytes"]}
            for p in resource
        ]
    if "requests" in wanted:
        series["requests_per_minute"] = [
            {"minute": b["minute"], "service": b["service"], "value": b["requests"]} for b in traffic
        ]
    if "errors" in wanted:
        series["errors_per_minute"] = [
            {"minute": b["minute"], "service": b["service"], "value": b["errors"], "error_rate": b["error_rate"]}
            for b in traffic
        ]
    if "latency" in wanted:
        series["latency_ms"] = [
            {"minute": b["minute"], "service": b["service"], "mean": b["latency_ms_mean"], "max": b["latency_ms_max"]}
            for b in traffic
        ]

    collection = page.get("collection") or {}
    notes = [
        "Latency is a mean and a max per minute; no percentiles are computed. An error is a 5xx "
        "(or no answer at all), never a 4xx.",
    ]
    if collection.get("note"):
        notes.append(collection["note"])
    return success({
        "application_id": application_id,
        "window": {"from": page.get("from"), "to": page.get("to")},
        "summary": page.get("summary") or {},
        "instances": page.get("instances") or [],
        "last_scale_event": page.get("last_scale_event"),
        "series": series,
        "collecting": collection.get("collecting", True),
        "note": " ".join(notes),
    })
