"""Section 13.1-13.3: get_platform_info, get_supported_stacks,
get_deployment_requirements — the three read-only discovery tools every
Company Deployment Skill workflow is instructed to call first (Section 5),
so the skill never hardcodes assumptions about what the platform currently
supports.
"""

from __future__ import annotations

import hashlib
import json
from typing import Any

from .. import __version__
from ..envelope import ErrorCode, ToolError, success
from ..platform_client import PlatformClient

# Only for a platform too old to have GET /policy: what it did before
# approval rules became configurable.
_LEGACY_APPROVAL_RULES_SUMMARY = (
    "dev deployments activate immediately after a passing image scan. "
    "production deployments pause as pending_approval after the same scan "
    "gate until approved outside the MCP."
)


def _approval_rules_summary(policy: dict[str, Any] | None) -> str:
    """The platform's own rules (GET /policy), in a sentence Claude can relay."""
    if policy is None:
        return _LEGACY_APPROVAL_RULES_SUMMARY
    gated = policy.get("approval_required_environments") or []
    if not gated:
        return "No environment needs approval: every deployment activates once its image scan passes."
    if policy.get("signed_approval"):
        who = ", ".join(policy.get("approvers") or []) or "the platform administrator"
        by = f"the platform administrator's signed approval ({who}, with their own key)"
    else:
        by = "an application owner's approval"
    others = [e for e in ("dev", "production") if e not in gated]
    rest = f" {', '.join(others)} deployments activate immediately." if others else ""
    return (
        f"Deployments to {', '.join(gated)} pause as pending_approval after the image scan passes "
        f"and wait for {by}, given outside the MCP. Waiting is normal, not a failure; always attach "
        f"a test_report.{rest}"
    )


def _stack_fingerprint(stacks: list[dict[str, Any]]) -> str:
    """A real, content-addressed version reference (Section 5's "drift
    detection"): changes deterministically whenever the catalog's content
    changes, without the platform having any actual version-numbering
    scheme (none exists — Module F has no version counter)."""
    canonical = json.dumps(sorted(stacks, key=lambda s: (s.get("category") or "", s.get("runtime") or "")))
    return hashlib.sha256(canonical.encode("utf-8")).hexdigest()[:16]


async def get_platform_info(client: PlatformClient, client_skill_version: str | None = None) -> dict[str, Any]:
    stacks = await client.get_supported_stacks()
    policy = await client.get_policy()
    data = {
        "platform_version": (
            "unversioned — the Platform API does not expose its own build/release "
            "version anywhere yet; this is a known gap, not a real semver"
        ),
        "policy_version": "static-v0 (no Module M policy-versioning system exists yet)",
        "supported_environments": ["dev", "production"],
        "approval_rules_summary": _approval_rules_summary(policy),
        "approval_required_environments": (
            policy.get("approval_required_environments", []) if policy is not None else ["production"]
        ),
        "supported_stack_version_ref": _stack_fingerprint(stacks),
        "tool_manifest_version": __version__,
        "client_skill_version_received": client_skill_version,
    }
    return success(data)


async def get_supported_stacks(client: PlatformClient, category: str | None = None) -> dict[str, Any]:
    stacks = await client.get_supported_stacks()
    if category:
        stacks = [s for s in stacks if (s.get("category") or "").lower() == category.lower()]
    # version_range is part of Section 13.2's output shape but the catalog
    # (migration 0002) has no such column — only runtime name + status are
    # tracked, per platform-api's README "Stack version governance" gap.
    # Reported as null rather than fabricated.
    enriched = [{**s, "version_range": None} for s in stacks]
    return success({"stacks": enriched, "stack_list_version": _stack_fingerprint(stacks)})


_DEPLOYMENT_YAML_SHAPE = {
    "top_level_fields_allowed": ["app", "services", "database", "scaling", "resources", "domain"],
    "note": (
        "any top-level field outside this set is rejected by validate_application's "
        "security precheck — mirrors internal/service/validation_service.go's "
        "actual enforcement as of this writing. There is no Platform API endpoint "
        "that serves this shape as data yet, so this tool hand-encodes it; if the "
        "validation engine's rules change, this description can drift out of sync "
        "with the real enforcement until updated here too."
    ),
    "app": {"name": "required, DNS-label-valid", "owner": "required department name"},
    "services": {
        "<service_name>": {
            "runtime": "must be an active entry from get_supported_stacks(category='backend'|'frontend')",
            "port": "required for backend-kind runtimes",
        }
    },
    "scaling": {"min": "0 (default, scale-to-zero eligible) or >=1 (opts out)"},
}


async def get_deployment_requirements(
    client: PlatformClient, application_id: str | None = None, proposed_shape: dict[str, Any] | None = None
) -> dict[str, Any]:
    if application_id and proposed_shape:
        raise ToolError(
            ErrorCode.VALIDATION_ERROR,
            "supply either application_id or proposed_shape, not both",
        )

    result: dict[str, Any] = {"deployment_yaml_shape": _DEPLOYMENT_YAML_SHAPE}

    if application_id:
        app = await client.get_application(application_id)
        result["application_id"] = application_id
        result["current_lifecycle_state"] = app.get("lifecycle_status")
        result["current_deployment_yaml_draft"] = app.get("deployment_yaml_draft")

    stacks = await client.get_supported_stacks()
    result["allowed_runtimes"] = stacks

    return success(result)
