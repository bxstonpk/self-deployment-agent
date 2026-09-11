# Company AI Application Deployment Platform

An internal, self-service platform that lets employees using AI coding
agents (Claude Code) build, validate, and deploy approved internal
applications without one-off IT intervention per deployment. Full business
case and requirements: [`docs/01_BRD.md`](docs/01_BRD.md).

This README orients a new contributor/maintainer picking up the project:
what exists, what doesn't, how the pieces fit together, and where to start.

## Status: early-stage, ~35–40% of the full documented scope

**What's real and tested** (every claim below has been verified against a
live, running system — Docker builds, real HTTP calls, a real headless
browser — not just written and assumed correct; see each component's own
README for exactly what was tested and how):

- The full Application Lifecycle (Draft → Validated → Build → Deploying →
  Running → Suspended → Rolled Back → Archived → Deleted) — backend + a
  browser-driven frontend.
- An append-only, hash-chained Audit Log (Module W) recording every
  state-changing action across that lifecycle — verified against a real
  database, including that direct `UPDATE`/`DELETE` attempts are rejected
  and a simulated out-of-band tamper is detected.
- An in-app Notification inbox (Module X) for deployment status and
  production-approval events — verified against a real deploy/build/
  approve flow, including that a rejected production deployment notifies
  every owner, not just the one who rejected it.
- Co-Owner/Contributor Management (Module E, `FR-017`) — an application's
  primary owner can grant/revoke a teammate day-to-day access, using data
  model support (`application_owners`) that existed since the very first
  PR but was never wired to an actual endpoint until now. Verifying it for
  real surfaced and fixed a genuine, previously-unreachable bug in Module
  W: audit entries for a deploy/build weren't visible to the application's
  own primary owner unless they happened to be the one who performed the
  action — invisible before this PR because there was never a second
  owner to expose it.
- Ownership Transfer (Module E, `FR-016`) — the primary owner nominates a
  new one, who is notified and must explicitly accept before anything
  changes; the prior owner's record is revoked, not deleted, for audit
  history. Verified with a real (not mocked) expiry: set the acceptance
  window to 2 real seconds, waited 3, confirmed acceptance was correctly
  rejected — which is also how a real gap in `docker-compose.yml` (the new
  config value wasn't being forwarded into the container at all) was
  found and fixed.
- An MCP server exposing that lifecycle to an AI agent (Claude Code),
  matching the platform's own `docs/07_MCP_Requirements.md` tool catalog —
  plus eight tools beyond it, exposing Audit Log, Notification, Reporting
  and ownership management, all of which shipped after that catalog was
  written. One of them, `get_application_inventory`, closes a gap older
  than the modules themselves: the documented catalog is entirely
  single-application, so until now nothing could answer "which
  applications do I have?" at all. Ownership *transfer* is deliberately
  left out — making someone accountable for an application is a decision
  a human should take in their own name, not one an agent takes for
  them.
- A Markdown skill package instructing Claude Code how to use that MCP
  server correctly.
- A React admin portal covering the same lifecycle for a human, calling
  the Platform API directly — including an Audit Log view (per-application
  and platform-wide, with filtering, CSV export, and chain-integrity
  verification), a Notifications inbox (unread-count header badge,
  mark-read, unread-only filter), an Owners section for granting/
  revoking co-owner/contributor access, a Transfer primary ownership
  flow (nominate, notify, accept), a Reports page (a KPI row of
  deployment outcomes over a selectable range, plus the application
  inventory), a Secrets section where an owner sets write-only values
  the page can never show again, a Logs section showing what an
  application's containers printed, secrets already redacted and
  owners-only, and a Metrics section with the platform's own measurements
  (requests, errors, latency, CPU and memory, with sparklines) — verified
  with up to three genuinely
  independent signed-in identities at once (separate browser contexts),
  confirming a newly-granted co-owner gets real day-to-day access, a
  revoked one is genuinely locked out on their very next request, and a
  non-nominee can't accept someone else's transfer. That verification
  also caught and fixed a real API design mistake: checking for a pending
  transfer 404'd on every single page view (the ordinary case for most
  applications most of the time), not just an occasional mistaken lookup —
  fixed to a real `200` with a null payload instead.
- Reporting (Module AB, `FR-127`/`FR-128`) — an application inventory and
  a deployment activity report (outcomes by environment and department),
  both derived entirely from data the platform already holds. Verified
  against FR-128's own acceptance criterion for real: ran two real
  deploys and a real rollback, then confirmed the report's
  succeeded/failed/rolled-back totals reconcile exactly with the raw
  audit log queried independently.
- Database Management (Module N, `FR-061`/`062`/`063`/`065`) — declaring
  `database: type: postgres` in `deployment.yaml` provisions a real,
  dedicated Postgres container on the application's own private network,
  with a platform-generated password injected into the application's
  runtime and kept sealed in Module O's secret store (below) — never in
  `deployment.yaml`, source control or build logs; deleting the
  application tears it down. Verified by connecting rather
  than by reading configuration (see
  `services/platform-api/scripts/verify_module_n.py`): a client on the
  private network reads and writes real data, the identical connection
  from off that network fails by name *and* by raw IP, and the deployed
  application itself completes a Postgres handshake using the injected
  environment — which survives resume, restart and a real scale-to-zero
  cold start, each of which starts a brand-new container. That
  verification is what caught the isolation being wrong in the first
  place: the database container was on Docker's default bridge as well as
  its private network, reachable by every other application, and it
  passed every unit test while it was.
- Secret Management (Module O, `FR-066`/`067`/`069`/`070`) — an owner
  registers a secret by name, in the Admin Portal or through the API; the
  platform stores it AES-256-GCM
  encrypted, injects it into every container the application starts as an
  environment variable, and never returns the value again, from any
  endpoint or the audit log. Module N's database passwords moved into it,
  existing plaintext ones are sealed at startup, and an owner can rotate
  one: the old password stops authenticating and running instances are
  restarted onto the new one. Verified the way an
  attacker would look (see `services/platform-api/scripts/verify_module_o.py`):
  a full `pg_dump` of the platform database, every layer of the built
  image and the platform's own logs contain the value nowhere, while the
  running application received exactly it; a ciphertext copied straight
  into another application's database row gets that application a failed
  start, not the secret; and restarting the platform with a different key
  makes applications fail closed — without stopping the ones already
  running — until the key is restored. Over MCP, an agent sees secret
  *names* only (in `get_application_status`, so it can tell an employee
  which ones to set before a deploy) — never a value, and no tool accepts
  one: the security requirements forbid a secret value ever passing
  through the agent or its transcript.
- Logging (Module S, `FR-086`/`087`/`089`) — every line an application's
  containers print is collected as it's written and stored centrally,
  tagged with its deployment, service, stream and container, with every
  secret value the platform injected redacted before it's stored. Owners
  read their application's logs, filtered and paged, through the
  Platform API or the MCP server's `get_application_logs`; anyone else
  gets the same 404 a nonexistent application gets, and every read is
  audited. Verified against real containers (see
  `services/platform-api/scripts/verify_module_s.py`): an application
  that prints its own secrets on purpose has them redacted, and they
  appear nowhere in a full `pg_dump` of the platform database; a crash on
  the first deploy leaves its reason readable after the platform has
  removed the container; a replaced container's last line, printed as it
  was being stopped, is kept; and after platform-api itself is stopped
  and started again, every line is there exactly once, including the
  ones written while it was down.
- Monitoring (Module T, `FR-090`/`091`) — CPU and memory are read from
  each running container on an interval, and requests, errors and latency
  are counted at the platform's own proxy, so an application gets metrics
  without instrumenting anything. That proxy is also the address the
  platform reports as an application's URL
  (`/run/{application}/{service}`), which is what keeps it working after
  the container behind it is replaced — it used to report the container's
  own published port, which broke on every restart and went around the
  proxy entirely. Owners query them through the Platform
  API or the MCP server's `get_application_metrics`; anyone else gets the
  same 404 a nonexistent application gets. Verified against real work (see
  `services/platform-api/scripts/verify_module_t.py`): an application that
  burns CPU on demand moved the reading from 0% to 97.7%, allocating
  96 MiB moved memory from 1.6 MB to 103 MB, the proxy's counts matched
  the requests actually made — a 5xx counted as an error, a 404 only as a
  request — the deliberately slow request showed up in the latency
  maximum, and a suspended application produced no samples at all, with
  the answer saying why rather than looking like an outage.

**What doesn't exist at all yet**: real authentication/RBAC (every
authorization check today is "are you a registered owner of this
application," full stop — no IT/Platform/Security Administrator roles),
Domain/Network management, Resource quotas.
See "Known gaps" below and each
component's own README for the honest, itemized list — nothing here claims
these exist when they don't. Note in particular that Module O above ships
with a real limitation of its own, listed there — a module being
implemented is not the same as it being safe to rely on.

## Repository map

```
docs/                     BRD, Architecture, SDLC, and 15 more requirements/
                           design documents — the original specification
                           this implementation works from. Written before
                           any code; read docs/README.md first.

services/platform-api/    Go. The single authoritative backend — the ONLY
                           thing that talks to Postgres/Docker directly.
                           Everything else (MCP server, admin portal) is a
                           client of this API. Start here to understand
                           what's actually implemented server-side.

services/mcp-server/      Python. The Model Context Protocol server Claude
                           Code calls to act on an employee's behalf —
                           translates MCP tool calls into Platform API
                           calls, nothing more.

company-deployment-skill/ Markdown. The instruction set Claude Code reads
                           to know HOW to use the MCP server correctly
                           (when to call what, how to handle failures,
                           what it must never do).

apps/admin-portal/        React + TypeScript. A human-operated web UI,
                           calling the Platform API directly (not through
                           the MCP server) — the same capabilities Claude
                           Code has, reachable without an AI agent.
```

Each directory above has its own README with real implementation detail,
an honest gap list specific to that component, and exactly what was
verified and how. **Read those, not just this file** — this README is an
orientation map, not a substitute for them.

## Running everything locally

Requires Docker, Go 1.25+, Python 3.11+, and Node 20+.

1. **Platform API** (start this first — everything else depends on it):
   ```
   cp .env.example .env
   docker compose up --build
   ```
   `.env.example` leaves `PLATFORM_SECRET_KEY` empty on purpose: generate
   one (`openssl rand -base64 32`) and set it in `.env`, or compose
   refuses to start the platform rather than let it store secrets it can't
   encrypt. See [`services/platform-api/README.md`](services/platform-api/README.md).

   Deployed applications run as containers on the same Docker daemon,
   outside this compose project — `platform-run-*` for application
   containers and, for an application that declares a database,
   `platform-db-*` plus a `platform-net-*` network. `docker compose down`
   does not remove those; deleting the application through the API does.

2. **MCP server** (optional — only needed to test the AI-agent path):
   ```
   cd services/mcp-server
   python -m venv .venv && source .venv/Scripts/activate  # or .venv/bin/activate
   pip install -e ".[dev]"
   cp .env.example .env   # then source it into your shell
   python -m mcp_server.server
   ```
   See [`services/mcp-server/README.md`](services/mcp-server/README.md).

3. **Admin portal** (optional — only needed to test the human-facing UI):
   ```
   cd apps/admin-portal
   npm install
   cp .env.example .env.local
   npm run dev
   ```
   If your `CORS_ALLOWED_ORIGINS` (root `.env`) doesn't match the port Vite
   actually prints, add it — see
   [`apps/admin-portal/README.md`](apps/admin-portal/README.md)'s
   port-collision note.

## Known gaps (the honest, load-bearing ones)

These block real production use, not just missing polish:

- **No real authentication.** Every service uses a dev-mode header stub
  (`X-Dev-User-Email`) that trusts whatever identity it's given. Blocked
  on `DEC-001`/`DEC-003` (`docs/17_Decision_Log.md`) — choosing a real
  IdP/SSO integration is a decision for whoever owns this platform next,
  not something this implementation could resolve on its own.
- **No RBAC beyond application ownership.** No IT/Platform/Security
  Administrator role exists anywhere. Blocked on `DEC-002`.
- **`deploy_application`/the admin portal's Deploy runs synchronously**,
  not as a real queued/async job — fine at today's scale, a real gap
  before this could serve many concurrent deployments.
- **The secret store's key is not managed.** Module O encrypts every
  secret — and every generated database password — with one static key
  from `PLATFORM_SECRET_KEY`. There is no key rotation or re-encryption
  tool, and anyone who can read platform-api's environment can read the
  key and, with it, every secret. The platform database alone no longer
  yields anything, which is what the requirements ask of the store;
  taking the key off the host is what a real backend chosen under
  `DEC-006` (still Open) would do. See `services/platform-api/README.md`'s
  "How Secret Management works" for this and Module O's other gaps
  (rotation covers only what the platform itself issues, on demand;
  production-secret approval and injection auditing are missing).
- **No Domain or Network management** (Modules P/Q). Module Q's absence
  means `FR-062`'s *detection* half is missing: a cross-application
  database connection attempt is prevented, but not detected or logged as
  a policy violation. `FR-064` (database backups) is not implemented
  either — it needs a scheduler this platform doesn't have and a retention
  policy the requirement itself marks TBD. See
  `services/platform-api/README.md`'s "How Database Management works" for
  the full scope.
- **Observability stops short of alerting and retention.** Logging
  (Module S) and Monitoring (Module T) both collect and serve, but
  `FR-088`'s and `NFR-032`'s retention periods are TBD, so nothing is
  purged; `FR-092`'s alert thresholds are specified nowhere, so no alert
  is raised on numbers nobody agreed to; `FR-093`'s platform-wide
  dashboard needs administrator roles that don't exist; log levels aren't
  parsed; latency has no percentiles; and nothing streams — reading logs
  is a page-at-a-time query in the Portal and over MCP alike. See
  `services/platform-api/README.md`'s "How Logging works" and "How
  Monitoring works". (Modules W, X and AB — Audit Log, Notification and
  Reporting — are implemented; see `services/platform-api/README.md`'s
  "How Audit Logging works", "How Notifications work" and "How Reporting
  works" sections for what each does and doesn't cover. Notably Module X
  is in-app only, no email/Slack/webhook delivery, and Module AB covers
  `FR-127`/`FR-128` but not `FR-129`, which reports against quotas that
  Module M would have to define first.)
- **No resource quota enforcement** (Module M) — `validate_application`
  always reports this check as `skipped`, never a fake pass.
- **The Admin Portal is intentionally scoped to MOD-19 (Application
  Catalog), not the full MOD-18 (Administration Portal)** the original
  tech-stack decision named — MOD-18 needs the RBAC that doesn't exist.
  See `apps/admin-portal/README.md`'s "Scope" section for the full
  reasoning.

## Contributing / continuing this work

Every change so far has gone through a feature branch and a pull request
against `main` — `git log --oneline` and the closed PRs on GitHub are a
reasonably complete build log of what was built, in what order, and why
(commit messages document real bugs found during manual verification, not
just what changed). Follow the same pattern: branch, implement, verify for
real (not just unit tests — every component's README explains what "real"
verification meant for it), open a PR.
