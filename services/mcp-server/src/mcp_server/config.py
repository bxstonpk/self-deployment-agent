"""Server configuration, sourced entirely from environment variables.

Mirrors services/platform-api/internal/config/config.go's pattern: no config
files, no CLI flags, just env vars with fail-fast validation at startup.
"""

from __future__ import annotations

import os
from dataclasses import dataclass


class ConfigError(Exception):
    pass


@dataclass(frozen=True)
class EmployeeIdentity:
    """The single employee this MCP server process acts on behalf of.

    See DEC-003 (docs/17_Decision_Log.md) — the real design is a per-call,
    short-lived, revocable delegated credential; that mechanism is Open,
    same as DEC-001 which platform-api's own dev-mode auth stub is blocked
    on. Binding one MCP server process to one employee identity at startup
    is the same category of adaptation platform-api already uses (see its
    README's "Dev-mode auth" section) applied at the MCP layer: an MCP
    session already IS meant to be 1:1 with one employee (Section 3 of
    07_MCP_Requirements.md), so a process-lifetime identity is a reasonable
    stand-in for a real per-session token, not an unrelated shortcut.
    """

    email: str
    full_name: str | None
    department: str | None


@dataclass(frozen=True)
class Config:
    mcp_env: str
    platform_api_base_url: str
    platform_api_timeout_seconds: float
    employee: EmployeeIdentity
    idempotency_ttl_seconds: float


def load_config() -> Config:
    mcp_env = os.environ.get("MCP_ENV", "").strip()
    if mcp_env != "dev":
        raise ConfigError(
            'MCP_ENV must be "dev" — this server only supports the dev-mode '
            "employee-identity stand-in described in config.py's module doc "
            "(DEC-003 is still Open). Refusing to start under any other value "
            "so this never accidentally runs against a real deployment "
            "without real MCP session-token authentication."
        )

    base_url = os.environ.get("PLATFORM_API_BASE_URL", "").strip()
    if not base_url:
        raise ConfigError("PLATFORM_API_BASE_URL is required")

    email = os.environ.get("MCP_EMPLOYEE_EMAIL", "").strip()
    if not email:
        raise ConfigError(
            "MCP_EMPLOYEE_EMAIL is required — the employee identity this "
            "server session acts on behalf of (see EmployeeIdentity's doc "
            "comment)"
        )

    full_name = os.environ.get("MCP_EMPLOYEE_NAME", "").strip() or None
    department = os.environ.get("MCP_EMPLOYEE_DEPARTMENT", "").strip() or None

    timeout_raw = os.environ.get("PLATFORM_API_TIMEOUT_SECONDS", "30")
    try:
        timeout = float(timeout_raw)
    except ValueError as exc:
        raise ConfigError(
            f"PLATFORM_API_TIMEOUT_SECONDS must be a number, got {timeout_raw!r}"
        ) from exc

    idempotency_ttl_raw = os.environ.get("MCP_IDEMPOTENCY_TTL_SECONDS", "86400")
    try:
        idempotency_ttl = float(idempotency_ttl_raw)
    except ValueError as exc:
        raise ConfigError(
            f"MCP_IDEMPOTENCY_TTL_SECONDS must be a number, got {idempotency_ttl_raw!r}"
        ) from exc

    return Config(
        mcp_env=mcp_env,
        platform_api_base_url=base_url.rstrip("/"),
        platform_api_timeout_seconds=timeout,
        employee=EmployeeIdentity(email=email, full_name=full_name, department=department),
        idempotency_ttl_seconds=idempotency_ttl,
    )
