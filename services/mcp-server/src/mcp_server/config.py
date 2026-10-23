"""Server configuration, sourced entirely from environment variables.

Mirrors services/platform-api/internal/config/config.go's pattern: no config
files, no CLI flags, just env vars with fail-fast validation at startup.

Who the server acts for is NOT configuration. Until DEC-001/DEC-003 were
revised (2026-10-23), the employee's email was set here and sent as a
self-declared header. Now each machine signs in — `login` /
`complete_login`, or `company-deploy login` — and the API key it receives
is what identifies the employee; see credentials.py.
"""

from __future__ import annotations

import os
from dataclasses import dataclass, field
from pathlib import Path

from . import credentials


class ConfigError(Exception):
    pass


@dataclass(frozen=True)
class Config:
    platform_api_base_url: str
    platform_api_timeout_seconds: float
    idempotency_ttl_seconds: float
    credentials_path: Path = field(default_factory=credentials.default_path)


def _seconds(name: str, default: str) -> float:
    raw = os.environ.get(name, default)
    try:
        return float(raw)
    except ValueError as exc:
        raise ConfigError(f"{name} must be a number, got {raw!r}") from exc


def load_config() -> Config:
    base_url = os.environ.get("PLATFORM_API_BASE_URL", "").strip()
    if not base_url:
        stored = credentials.load()
        base_url = stored.platform_url if stored else ""
    if not base_url:
        raise ConfigError(
            "PLATFORM_API_BASE_URL is required (or sign in once with "
            "`company-deploy login --url <platform address>`, which remembers it)"
        )

    # 120s, not 30s: deploy_application's build step is a real `docker
    # build` (compile + image export) that can legitimately take well
    # past 30s, especially with a cold layer cache or a busy Docker
    # daemon — found for real when a merely-slow (not hung) rebuild
    # tripped a 30s client timeout during manual verification, which
    # also surfaced a separate server-side bug (now fixed) where the
    # resulting disconnect could leave the build/application stuck.
    timeout = _seconds("PLATFORM_API_TIMEOUT_SECONDS", "120")
    idempotency_ttl = _seconds("MCP_IDEMPOTENCY_TTL_SECONDS", "86400")

    return Config(
        platform_api_base_url=base_url.rstrip("/"),
        platform_api_timeout_seconds=timeout,
        idempotency_ttl_seconds=idempotency_ttl,
        credentials_path=credentials.default_path(),
    )
