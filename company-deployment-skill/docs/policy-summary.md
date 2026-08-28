# Policy Summary — cached snapshot

**This is a cache, not a source of truth.** `get_platform_info` returns the
live `approval_rules_summary` and `policy_version` — see `SKILL.md`, Step 3.
This file exists for fast offline reference and to be honest about what's
*actually* enforced today versus what the platform's own longer-term design
intends, so you don't assume protections that don't exist yet.

## Environments

Two environments exist: `dev` and `production`. There is no `staging`
environment in this platform as built, regardless of what any other
document references.

## Production approval gate

A `production` deploy pauses as `PENDING_APPROVAL` after the image scan
passes, before Build proceeds. An authorized human (today: any active
owner of the application) approves or rejects it through the Platform API
directly — there is no MCP tool that grants approval, by design.

**Known limitation, not hidden**: the approver is *not* currently required
to be a different person than the requester. Real role-based access
control (a distinct "Platform Administrator" / "Security Administrator"
role) doesn't exist yet — every authorization check on this platform today
is "are you an owner of this application," nothing more granular. Don't
imply to an employee that production approval carries independent
oversight beyond that.

## What's enforced today

- **Schema validation** — `deployment.yaml`'s structure, required fields,
  and the fixed six-top-level-key shape (`app`/`services`/`database`/
  `scaling`/`resources`/`domain`). Any other top-level field is rejected
  outright.
- **Stack compliance** — every declared runtime/database must be `active`
  in the live Supported Stack catalog.
- **Image vulnerability scanning** — every built image is scanned; any
  CRITICAL-severity finding blocks deployment. There's no published,
  configurable severity threshold beyond that yet.
- **Ownership** — only an active owner of an application can validate,
  deploy, suspend, resume, restart, roll back, archive, or delete it.

## What's *not* enforced yet — say so honestly if asked

- **Resource quotas.** `validate_application`'s response always reports
  `resource_quota` as `skipped`, not passed — there is no department or
  application quota system yet. Don't tell an employee a quota check
  happened when it didn't.
- **Role-based access control beyond ownership.** No distinct IT
  Administrator / Platform Administrator / Security Administrator /
  Auditor roles exist in enforcement — see the approval-gate note above.
- **Rate limiting.** No `RATE_LIMITED` condition is currently reachable in
  practice — there's no rate limiter implemented. Still handle the error
  code per the troubleshooting guide in case this changes.
- **Secret management.** There is no Secret Manager on this platform yet.
  If an application needs a credential/connection string, there is
  currently no platform-native place to put it — say so plainly rather
  than inventing a workaround.
- **Domain/DNS management.** `domain.visibility` is recorded but doesn't
  yet drive real DNS/TLS provisioning.
- **Audit logging.** Tool calls are logged by the MCP server itself
  (structured output, not a durable/queryable store) — there is no
  platform-wide audit log an Auditor could query yet.
- **Logging and metrics.** `get_application_logs` and
  `get_application_metrics` always return an error explaining this
  outright — see `troubleshooting.md`.

## Idempotency

Mutating tools accept an `idempotency_key`. It is honored on a
best-effort, non-durable basis by the MCP server process itself (an
in-memory cache with a TTL) — it protects against the most common retry
scenario (a network hiccup right after a call already succeeded, from the
same server process) but does not survive a server restart and offers no
guarantee across multiple server instances. Still pass one on every
mutating call; it costs nothing and helps in the common case.
