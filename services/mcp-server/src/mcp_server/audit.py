"""Best-effort audit trail, per docs/07_MCP_Requirements.md Section 7.

Real gap, documented not hidden: Section 7 wants an append-only,
tamper-resistant, centrally-queryable audit store (Module W, Audit Log —
not built anywhere in this platform yet, Go side included). This module is
NOT that. It emits one structured JSON line per tool call to this process's
own stdout/stderr (wherever the process supervisor captures it), covering
every field Section 7 lists as a minimum, but with none of the durability,
append-only, or tamper-resistance guarantees a real audit store provides —
anyone with access to this process's logs can see or lose these lines like
any other log output. A real implementation persists these to the Platform
API (Module W) so Auditors/Security Administrators can query them
independent of wherever this MCP server happened to run.
"""

from __future__ import annotations

import json
import logging
import sys
import time
import uuid
from dataclasses import dataclass, field
from typing import Any

_logger = logging.getLogger("mcp_server.audit")
if not _logger.handlers:
    handler = logging.StreamHandler(sys.stderr)
    handler.setFormatter(logging.Formatter("%(message)s"))
    _logger.addHandler(handler)
    _logger.setLevel(logging.INFO)
    _logger.propagate = False

# One correlation base per server process — see this module's doc comment
# for why this is a simplification, not a real per-employee-session id.
_PROCESS_SESSION_ID = str(uuid.uuid4())

# Keys anywhere in input_parameters whose VALUE is redacted before logging —
# Section 7: "input_parameters: Full request payload, with secret-shaped
# values redacted before storage." deployment_yaml/source_archive_base64
# aren't secrets, just large payloads not worth logging in full.
_REDACT_KEYS = {"token", "password", "secret", "confirm_token", "deployment_yaml", "source_archive_base64"}


def _redact(value: Any) -> Any:
    if isinstance(value, dict):
        return {
            k: ("<redacted>" if k.lower() in _REDACT_KEYS else _redact(v))
            for k, v in value.items()
        }
    if isinstance(value, list):
        return [_redact(v) for v in value]
    return value


@dataclass
class AuditEvent:
    tool_name: str
    employee_identity: str
    input_parameters: dict[str, Any]
    target_application_id: str | None = None
    _started_at: float = field(default_factory=time.monotonic, repr=False)

    def finish(self, result: str, result_summary: dict[str, Any] | None = None) -> None:
        duration_ms = round((time.monotonic() - self._started_at) * 1000, 2)
        record = {
            "timestamp": _iso_now(),
            "employee_identity": self.employee_identity,
            "session_id": _PROCESS_SESSION_ID,
            "tool_name": self.tool_name,
            "input_parameters": _redact(self.input_parameters),
            "target_application_id": self.target_application_id,
            # No independent MCP-layer authorization decision exists to
            # report (see platform_client.py's doc comment): every real
            # allow/deny is the Platform API's, reflected here via `result`.
            "authorization_decision": "delegated_to_platform_api",
            "result": result,
            "result_summary": result_summary or {},
            "duration_ms": duration_ms,
        }
        _logger.info(json.dumps(record, default=str))


def _iso_now() -> str:
    from datetime import datetime, timezone

    return datetime.now(timezone.utc).isoformat()
