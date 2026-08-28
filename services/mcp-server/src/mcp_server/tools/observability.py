"""Section 13.9-13.10: get_application_logs, get_application_metrics.

Neither has anything real to call: Module S (Logging) and Module T
(Monitoring) don't exist anywhere in this platform yet — no log storage, no
metrics storage, on the Go side or anywhere else. Both tools are still
registered (Section 5's protocol-level discovery must enumerate all 12
tools so Claude Code can see their schemas), but every call returns a
clear, honest INTERNAL_ERROR explaining exactly what's missing, per Section
8's own guidance for that code ("if persistent, tell the employee to
contact IT/Platform Administrator rather than attempting a workaround") —
this failure IS persistent, not transient, so callers should say so rather
than retrying.
"""

from __future__ import annotations

from typing import Any

from ..envelope import ErrorCode, ToolError

_LOGS_MESSAGE = (
    "application logging is not implemented — Module S (Logging) does not "
    "exist in the Platform API. There is no log storage anywhere to query. "
    "This is a persistent gap, not a transient failure; do not retry."
)

_METRICS_MESSAGE = (
    "application metrics are not implemented — Module T (Monitoring) does not "
    "exist in the Platform API. There is no metrics storage anywhere to query. "
    "This is a persistent gap, not a transient failure; do not retry."
)


async def get_application_logs(
    application_id: str,
    environment: str,
    tail_lines: int | None = None,
    time_range: str | None = None,
    service: str | None = None,
    severity: str | None = None,
) -> dict[str, Any]:
    raise ToolError(ErrorCode.INTERNAL_ERROR, _LOGS_MESSAGE)


async def get_application_metrics(
    application_id: str,
    environment: str,
    time_range: str | None = None,
    metric_types: list[str] | None = None,
) -> dict[str, Any]:
    raise ToolError(ErrorCode.INTERNAL_ERROR, _METRICS_MESSAGE)
