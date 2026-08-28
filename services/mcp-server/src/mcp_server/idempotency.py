"""Best-effort idempotency key handling, per
docs/07_MCP_Requirements.md Section 10.

Real gap, documented not hidden: Section 10 wants the Platform API itself
storing the key against the resulting operation so retries are safe no
matter which MCP server process (or replica) handles them. The Platform API
has no such store — mutating endpoints have no idempotency-key concept at
all. This module is a stopgap at the MCP layer only: an in-process,
non-durable, per-server-instance cache. It protects the single most common
agentic-retry scenario (a network hiccup right after a tool call already
reached the Platform API, from the SAME server process) but does nothing
for a retry that lands on a different process, after a restart, or after
the TTL expires. A correct implementation belongs in the Platform API,
tracked as a real gap rather than silently declared "done" because
something with the right shape exists here.
"""

from __future__ import annotations

import time
from dataclasses import dataclass
from typing import Any


@dataclass
class _Entry:
    input_fingerprint: Any
    result: dict[str, Any]
    expires_at: float


class IdempotencyConflict(Exception):
    """Same key, different input — Section 10's CONFLICT case."""


class IdempotencyStore:
    def __init__(self, ttl_seconds: float):
        self._ttl = ttl_seconds
        self._entries: dict[str, _Entry] = {}

    def _now(self) -> float:
        return time.monotonic()

    def _prune(self) -> None:
        now = self._now()
        expired = [k for k, e in self._entries.items() if e.expires_at <= now]
        for k in expired:
            del self._entries[k]

    def get_cached(self, key: str, input_fingerprint: Any) -> dict[str, Any] | None:
        """Returns the prior result for (key, matching input), or None if
        this key hasn't been seen. Raises IdempotencyConflict if the key was
        seen before with DIFFERENT input."""
        self._prune()
        entry = self._entries.get(key)
        if entry is None:
            return None
        if entry.input_fingerprint != input_fingerprint:
            raise IdempotencyConflict(
                f"idempotency_key {key!r} was already used with different input"
            )
        return entry.result

    def store(self, key: str, input_fingerprint: Any, result: dict[str, Any]) -> None:
        self._entries[key] = _Entry(
            input_fingerprint=input_fingerprint,
            result=result,
            expires_at=self._now() + self._ttl,
        )
