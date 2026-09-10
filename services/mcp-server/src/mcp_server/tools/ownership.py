"""list_application_owners, grant_application_access,
revoke_application_access — Module E's FR-017 (Co-Owner/Contributor
Management).

Not in docs/07_MCP_Requirements.md Section 13's original catalog; Module
E's ownership-management half shipped after it was written.

**FR-016 (Transfer Ownership) is deliberately NOT exposed here**, and the
line is worth stating plainly rather than leaving as an omission:
granting or revoking co-owner access is reversible, scoped to one
application, and is the kind of day-to-day team-membership change an
employee would reasonably ask an agent to make for them. Accepting an
ownership transfer is categorically different — it makes a specific
person *accountable* for an application (FR-016's own business rule calls
it "a pure accountability change"), and that is a decision a human should
take in their own name through the Admin Portal, not one an agent should
take on their behalf. Initiating a transfer is left out for the same
reason: an agent nominating someone commits that person to a decision
they then have to field. Same discipline as Section 13.13's
explicit-confirmation rule for deletion, applied to accountability rather
than destruction.

All three are thin per Section 2.3: the Platform API's
ApplicationService enforces the real rules (only the primary owner may
grant or revoke; the target must already be a known, active platform
user), and this layer re-derives none of them.
"""

from __future__ import annotations

from typing import Any

from ..envelope import ErrorCode, ToolError, success
from ..platform_client import PlatformClient

# The two grantable levels, mapped from the platform's own ownership_role
# vocabulary to the words FR-017 uses for them. "primary" is deliberately
# absent: it's never granted, only assigned at registration or transferred
# (FR-016), which this module doesn't expose at all.
_ACCESS_LEVELS = {"co_owner": "secondary", "contributor": "technical"}


async def list_application_owners(client: PlatformClient, application_id: str) -> dict[str, Any]:
    owners = await client.list_owners(application_id)
    return success({"application_id": application_id, "owners": owners})


async def grant_application_access(
    client: PlatformClient, application_id: str, email: str, access_level: str
) -> dict[str, Any]:
    ownership_role = _ACCESS_LEVELS.get(access_level)
    if ownership_role is None:
        raise ToolError(
            ErrorCode.VALIDATION_ERROR,
            f'access_level must be "co_owner" or "contributor", got {access_level!r}',
        )
    owner = await client.grant_owner(application_id, email, ownership_role)
    return success({"application_id": application_id, "granted": owner, "access_level": access_level})


async def revoke_application_access(
    client: PlatformClient, application_id: str, user_id: str
) -> dict[str, Any]:
    await client.revoke_owner(application_id, user_id)
    return success({"application_id": application_id, "revoked_user_id": user_id})
