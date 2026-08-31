# Company AI Application Deployment Platform

An internal, self-service platform that lets employees using AI coding
agents (Claude Code) build, validate, and deploy approved internal
applications without one-off IT intervention per deployment. Full business
case and requirements: [`docs/01_BRD.md`](docs/01_BRD.md).

This README orients a new contributor/maintainer picking up the project:
what exists, what doesn't, how the pieces fit together, and where to start.

## Status: early-stage, ~30–35% of the full documented scope

**What's real and tested** (every claim below has been verified against a
live, running system — Docker builds, real HTTP calls, a real headless
browser — not just written and assumed correct; see each component's own
README for exactly what was tested and how):

- The full Application Lifecycle (Draft → Validated → Build → Deploying →
  Running → Suspended → Rolled Back → Archived → Deleted) — backend + a
  browser-driven frontend.
- An MCP server exposing that lifecycle to an AI agent (Claude Code),
  matching the platform's own `docs/07_MCP_Requirements.md` tool catalog.
- A Markdown skill package instructing Claude Code how to use that MCP
  server correctly.
- A React admin portal covering the same lifecycle for a human, calling
  the Platform API directly.

**What doesn't exist at all yet**: real authentication/RBAC (every
authorization check today is "are you a registered owner of this
application," full stop — no IT/Platform/Security Administrator roles),
Database/Secret/Domain/Network management, Logging, Monitoring, Audit Log,
Notification, Resource quotas, Reporting. See "Known gaps" below and each
component's own README for the honest, itemized list — nothing here claims
these exist when they don't.

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
   See [`services/platform-api/README.md`](services/platform-api/README.md).

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
- **No Database, Secret, Domain, or Network management** (Modules N/O/P/Q)
  — `deployment.yaml` can declare `database.type: postgres`, but nothing
  actually provisions one.
- **No Logging, Monitoring, Audit Log, Notification, or Reporting**
  (Modules S/T/W/X/AB) — the MCP server's log/metric tools return an
  honest "not implemented" error rather than fabricating data.
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
