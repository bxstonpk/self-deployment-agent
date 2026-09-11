# Company Deployment MCP Server

Python implementation of Module Y
([`../../docs/02_Functional_Requirements.md`](../../docs/02_Functional_Requirements.md))
per the full contract in
[`../../docs/07_MCP_Requirements.md`](../../docs/07_MCP_Requirements.md) —
the business-capability interface between Claude Code and the Company
Platform API ([`../platform-api/`](../platform-api/)).

Per that document's own Section 2 architecture: this server has **no
business logic of its own**. It authenticates as the single employee
identity its process is bound to, does no independent authorization beyond
that, translates each tool call into the corresponding Platform API HTTP
call(s), and relays the result inside the structured envelope Section 8
defines. Every real allow/deny decision is the Platform API's, re-derived
on every call — this server cannot short-circuit that.

## What's implemented

All 13 tools listed in Section 13 (the doc's prose says "12 business
tools" in a few places — Section 13 itself has subsections 13.1 through
13.13; this is a numbering inconsistency in the source document, not
something this server works around by dropping one):

| Tool | Section | Platform API call(s) | Notes |
|---|---|---|---|
| `get_platform_info` | 13.1 | `GET /supported-stacks` | Synthesizes the rest — see **Known gaps** |
| `get_supported_stacks` | 13.2 | `GET /supported-stacks` | `version_range` always `null` — not tracked by the catalog |
| `get_deployment_requirements` | 13.3 | `GET /applications/{id}` (optional), `GET /supported-stacks` | `deployment_yaml` shape is hand-encoded, not served as data anywhere — see **Known gaps** |
| `create_application` | 13.4 | `GET /departments`, `POST /applications`, `PUT .../deployment-yaml` | Resolves a department **name** to the UUID `POST /applications` needs |
| `validate_application` | 13.5 | `PUT .../deployment-yaml` (optional), `POST .../validate` | A failed validation is `status: success` with `passed: false` findings — not a transport error |
| `deploy_application` | 13.6 | `POST .../build` (if `source_archive_base64` given), `POST .../deploy` | Builds first when given source — see **How deploy_application closes the build gap** |
| `get_application_status` | 13.7 | `GET /applications/{id}`, `GET .../deployments/latest`, `GET .../secrets` | Includes the application's **secret names**, never values — 13.7's "secret references"; see **Why no MCP tool accepts a secret value** |
| `get_deployment_status` | 13.8 | `GET /deployments/{id}` | New Platform API endpoint added in this PR — see below |
| `get_application_logs` | 13.9 | *(none)* | Always a clear `INTERNAL_ERROR` — Module S doesn't exist |
| `get_application_metrics` | 13.10 | *(none)* | Always a clear `INTERNAL_ERROR` — Module T doesn't exist |
| `rollback_application` | 13.11 | `GET .../deployments` (for `target_version="previous"`), `POST .../rollback` | |
| `restart_application` | 13.12 | `POST .../restart` | |
| `delete_application` | 13.13 | `GET /applications/{id}`, `POST .../archive` (if `running`), `POST .../delete` | Orchestrates two Platform API calls behind one tool — see below |

Plus eight tools **beyond** Section 13's original catalog — Modules W
(Audit Log), X (Notification), AB (Reporting) and E's ownership
management all shipped after that catalog was written, so the doc has no
section number for them:

| Tool | Platform API call | Notes |
|---|---|---|
| `query_audit_log` | `GET /audit-log` | Same server-side scoping as the Admin Portal's Audit Log page — entries the caller performed themselves, or that concern an application they own |
| `list_notifications` | `GET /notifications` | The caller's own deployment-status/approval-request inbox — see `platform-api/README.md`'s "How Notifications work" |
| `mark_notification_read` | `POST /notifications/{id}/read` | |
| `get_application_inventory` | `GET /reports/application-inventory` | **The only tool that can enumerate an employee's applications at all** — see below |
| `get_deployment_activity` | `GET /reports/deployment-activity` | Succeeded/failed/rolled-back totals over a range, by environment and department |
| `list_application_owners` | `GET /applications/{id}/owners` | |
| `grant_application_access` | `POST /applications/{id}/owners` | `access_level` is `co_owner` or `contributor`; anything else (including `primary`) is rejected before the platform is called |
| `revoke_application_access` | `DELETE /applications/{id}/owners/{userId}` | |

#### `get_application_inventory` closes a gap older than Module AB

Section 13's entire catalog is single-application: `get_application_status`
takes one `application_id`, `get_deployment_status` one `deployment_id`,
and so on. Nothing in it could answer **"which applications do I have?"** —
an agent had no way to enumerate an employee's applications without being
handed the ids first, which in practice meant asking the employee to go
look them up in the Admin Portal. Module AB's inventory report happens to
be exactly that list, so exposing it makes this the natural first call in
most conversations rather than merely a reporting nicety.

#### Why FR-016 (ownership transfer) is deliberately NOT exposed

Granting or revoking co-owner access is reversible, scoped to a single
application, and is ordinary team-membership housekeeping an employee
would reasonably ask an agent to do for them. **Accepting an ownership
transfer is categorically different**: it makes a specific person
*accountable* for an application — FR-016's own business rule calls it "a
pure accountability change" — and that is a decision a human should take
in their own name through the Admin Portal, not one an agent should take
on their behalf. Initiating a transfer is left out for the same reason: an
agent nominating someone commits that person to a decision they then have
to field. This is the same discipline Section 13.13 applies to deletion
via its explicit-confirmation rule, pointed at accountability instead of
destruction.

#### Why no MCP tool accepts a secret value

The agent gets exactly one view of an application's secrets (Module O):
their **names**, in `get_application_status`'s `secrets` field — what
Section 13.7's "secret references" anticipates, and what an agent needs to
tell an employee which ones to set before a deploy. It never gets a value,
and there is deliberately no tool that accepts one.
`docs/11_Security_Requirements.md` SEC-SECRET-3 says a secret value must
never transit "the Company Deployment MCP, the Claude Code agent process,
or any conversational/agent transcript", and limits the agent to declaring
*that* a secret is needed and *which* name to use. A
`set_application_secret` tool would put the value in exactly those places:
the tool call's arguments, the transcript, and this server's own
structured audit stream. `company-deployment-skill/SKILL.md` instead tells
the agent to name the secret and have the employee set its value in the
Admin Portal.

Two details in how the names are relayed. They pass through an explicit
allowlist of fields (`name`, `managed_by`, `version`, `updated_at`), so a
value could not ride along even if the Platform API one day returned one.
And for a caller who can't see them, they come back as `secrets: null`
with a `secrets_note` — never `[]`, which would tell the agent there are
none.

Verified over the real stdio protocol by `scripts/e2e_verify.py`: no
tool's input schema has a parameter that could carry a secret (`secret`,
`password`, `token`, `value`); a secret the employee set on the platform
directly shows up in `get_application_status` by name while its value
appears nowhere in the MCP response; a second MCP session, signed in as
an employee who isn't an owner, gets `secrets: null` with the note; and
once the application is deleted, its status lists no secrets at all.

### Two small Platform API additions this PR needed

Building the MCP layer surfaced two real, small gaps in the Business API
that had nothing to do with MCP-specific design — they were just never
needed until something (this server) actually had to call them:

1. **`GET /departments`** — `create_application`'s spec (13.4) takes a
   department *name*, but `POST /applications` needs a department *UUID*,
   and there was no way to resolve one to the other outside direct
   database access. `platform-api/README.md`'s own "Running locally"
   section already flagged this exact gap ("extend this flow with a
   `GET /departments` endpoint in a future state") — this is that future
   state.
2. **`GET /deployments/{deploymentId}`** — `get_deployment_status` (13.8)
   polls by `deployment_id`, not `application_id`, but the Platform API
   only had `GET /applications/{id}/deployments/latest`. Added the
   deployment-id-keyed lookup its `DeploymentService.GetByID` already
   supported internally but never exposed over HTTP.

A third, smaller fix: `deploymentResponse` (Go) never included
`updated_at` at all — found for real when `get_deployment_status` and
`restart_application` both came back with `updated_at`/`restarted_at`
always `null` during manual verification, even though the underlying
`deployments.updated_at` column was being written correctly (e.g. by
Restart, which bumps it without changing `completed_at`). Fixed by adding
the field to the response DTO.

## How it works

- **`config.py`** — env-var-only configuration, fail-fast at startup.
  `MCP_ENV` must be `"dev"`; the server refuses to start otherwise (see
  **Dev-mode identity** below).
- **`platform_client.py`** — the only thing that talks HTTP to the
  Platform API. Attaches the bound employee's identity via the same
  `X-Dev-User-Email`/`X-Dev-User-Name`/`X-Dev-Department` headers
  `platform-api`'s own dev-mode auth stub trusts (no separate mechanism
  invented here). Translates every Platform API
  `{"error": {"code": ..., "message": ...}}` response into a `ToolError`
  carrying one of Section 8's fixed `ErrorCode`s, using the HTTP status as
  the primary signal and a handful of Platform API `code` strings as
  overrides where the status alone isn't precise enough (e.g. a `409` for
  "wrong lifecycle state" maps to `VALIDATION_ERROR`, not `CONFLICT` —
  Section 8 reserves `CONFLICT` specifically for idempotency-key reuse,
  duplicate in-flight operations, and name collisions).
- **`envelope.py`** — the exact `{status, data, error, request_id,
  server_time}` shape from Section 8, plus the fixed `ErrorCode` enum and
  the `ToolError` exception every tool implementation raises on failure —
  caught once, centrally, in `server.py`, not scattered per-tool.
- **`idempotency.py`**, **`audit.py`** — both explicitly best-effort
  stand-ins for infrastructure that doesn't exist yet. See **Known gaps**.
- **`tools/`** — one pure, independently-testable async function per tool
  (or logical group), taking a `PlatformClient` explicitly rather than a
  module-level global — this is what lets the unit tests inject a fake
  client with no network involved, the same fake-over-mock pattern
  `platform-api`'s Go tests use.
- **`server.py`** — wires everything into an `MCPServer` (the `mcp` SDK's
  current API — v2.x renamed `FastMCP` to `MCPServer`; this targets that
  current API, not the deprecated name). Every `@mcp.tool()`-decorated
  function has an explicit, typed signature matching each tool's real
  input shape (needed for correct JSON-schema generation and protocol-level
  discovery — see Section 5), and delegates to the pure function in
  `tools/`. A single `_run_tool` helper catches `ToolError`, builds the
  audit event, and converts to the envelope — shared across all 13
  registrations rather than duplicated in each.

### How `create_application` and `delete_application` orchestrate multiple calls

Two tools intentionally call the Platform API more than once, because
Section 13's tool boundary is coarser than the Business API's:

- `create_application` (13.4) takes `deployment_yaml` content in the same
  call that registers the application — the Platform API only supports
  that as two separate calls (`POST /applications` then
  `PUT .../deployment-yaml`), so this tool makes both.
- `delete_application` (13.13) is framed as one action "moving it through
  Archived -> Deleted" — the Platform API keeps Archive and Delete as
  separate, independently-preconditioned lifecycle operations
  (`platform-api`'s Module K). This tool calls `GET /applications/{id}`
  first to check the current state, calls `archive_application` only if
  the application is currently `running` (Delete already accepts
  `suspended` directly — archiving an already-suspended app would be a
  needless extra round trip), then calls `delete_application`. It also
  checks the caller's `confirmation` string against the application's
  actual name **before** calling the Platform API at all — Section
  13.13's "explicit confirmation... a single ambiguous instruction must
  never trigger deletion" — as a real check in addition to, not instead
  of, the Platform API's own `confirm: true` boolean.

### How `deploy_application` closes the build gap

Section 13.6 frames deployment as triggering "Build -> Image Scan ->
Deploy -> Health Check -> Traffic Activation" as one pipeline, but this
Platform API kept Build as a separate step requiring a source-archive
upload — and uploading source wasn't (and still isn't) a field in 13.6's
declared input shape, nor one of the 13 tools on its own. Rather than add
a 14th tool (Section 1 is explicit that a capability not expressible as
one of the fixed tool set is out of scope for the MCP, not a gap to fill
with a lower-level tool), `deploy_application` gained an **optional**
`source_archive_base64` parameter: a base64-encoded `tar.gz`, same
top-level-directory-per-service convention `platform-api`'s direct
`POST /applications/{id}/build` endpoint already uses. When given, this
tool calls `trigger_build` first and only proceeds to deploy if it
succeeded; a `source`-category build failure (compiler/dependency error)
comes back as `VALIDATION_ERROR` with the actual build output, a
`platform`-category one as `INTERNAL_ERROR` — mirroring FR-038's
source-vs-platform distinction rather than collapsing both into one
generic failure.

This is a different situation from `query_audit_log`/`list_notifications`/
`mark_notification_read` above, worth distinguishing rather than reading
as an inconsistent application of Section 1's "don't add a tool for a gap
in an existing one" principle: `trigger_build` wasn't added as a
standalone tool because a source-upload *parameter* fits inside
`deploy_application`'s existing shape. Modules W/X are different in kind —
they're whole business capabilities (query an audit trail, check pending
notifications) that didn't exist in *any* form when Section 13 was
written, not a missing parameter on an existing tool. Section 1's actual
principle ("expose only high-level business capabilities") argues *for*
giving them their own tools, not against it.

This also required a real Platform API change:
`build_service.go`'s `TriggerBuild` used to only accept a `Validated`
application (the first-ever build). It now accepts `Running`/`Failed`
too — a *rebuild* of new source for an application that's already live —
mirroring how `InitiateDeploy` already allows redeploying while `Running`.
Without that change, this parameter would only ever work for an
application's very first deploy; every subsequent `deploy_application`
call with new source on an already-`running` application would still hit
the same wall. Verified for real (see below): a v1 built-and-deployed via
one `deploy_application` call, then a v2 with different source deployed
via a *second* `deploy_application` call on the same, already-`running`
application — genuinely rebuilding and redeploying, not a workaround.

**A real concurrency/robustness bug found and fixed alongside this
change**, in `platform-api` (see its own README for the full account):
the failure-cleanup write for a failed build used the same
request-scoped context as the build attempt itself, so a client
disconnect/timeout during a genuinely slow (not hung) build could leave
the build stuck `in_progress` and the application stuck at
`lifecycle_status = 'build'` — a status nothing accepts as a valid
starting point, permanently unrecoverable through the API. Confirmed for
real during this server's own manual verification: an early attempt at
the v2-rebuild check above tripped a client-side timeout and produced
exactly that stuck state. Fixed on the `platform-api` side, and this
server's own default `PLATFORM_API_TIMEOUT_SECONDS` was raised from 30s
to 120s to match how long a real `docker build` can legitimately take
under load, rather than disconnecting prematurely on a merely-slow
operation in the first place.

## Known gaps (documented, not hidden)

Several of these mirror gaps already documented in
[`../platform-api/README.md`](../platform-api/README.md) — RBAC, audit
logging, and monitoring don't exist on the Go side either, so nothing here
can paper over them.

- **Dev-mode identity, not real MCP session tokens** (`config.py`,
  `platform_client.py`). Section 3 wants a short-lived, per-call,
  revocable, IdP-backed token; `DEC-003` (the mechanism) is still Open, the
  same way `DEC-001` blocks `platform-api`'s own dev-mode auth. This
  server binds ONE employee identity to the whole process lifetime instead
  — refuses to start unless `MCP_ENV=dev`, exactly mirroring
  `platform-api`'s `DevOnlyGuard`.
- **No real RBAC beyond ownership** (Section 4, Section 6's permission
  matrix). There is no IT/Platform/Security Administrator or
  Management/Auditor role anywhere in this platform — every tool call is
  authorized exactly the way `platform-api`'s console path is: ownership
  only, via `ApplicationOwner` rows. Section 6's matrix rows for elevated
  roles are simply not enforceable yet (`DEC-001`/`DEC-002`).
- **Idempotency is best-effort and non-durable** (`idempotency.py`). An
  in-process dict with a TTL, scoped to this one server instance's
  lifetime — protects the single most common agentic-retry scenario (a
  network hiccup right after a call already reached the Platform API, from
  the SAME process) but nothing survives a restart or helps across
  multiple server replicas. Section 10 wants the Platform API itself
  storing the key against the operation; the Platform API has no
  idempotency-key concept on any mutating endpoint today.
- **Audit logging is a structured stdout stream, not Module W**
  (`audit.py`). Covers every field Section 7 lists as a minimum, but has
  none of the append-only, tamper-resistant, centrally-queryable
  guarantees a real audit store provides — it's exactly as durable as
  whatever captures this process's stderr.
- **No real async job/poll pattern** (Section 9). `deploy_application`,
  `rollback_application`, and `restart_application` call synchronous
  Platform API endpoints that already run their entire pipeline within the
  request (see `platform-api/README.md`'s own "no background job/worker
  model" gap) — so the "immediate ack, poll separately" shape Section 9
  wants doesn't reflect reality yet. Every response says so explicitly in
  its `note` field rather than pretending otherwise.
  `get_deployment_status` still works correctly afterward regardless —
  the deployment record is queryable no matter how it got there.
- **`get_platform_info`'s `platform_version`/`policy_version` are not
  real version identifiers** — the Platform API doesn't expose its own
  build/release version anywhere, and there's no Module M policy-versioning
  system. Both fields say so in their own value rather than fabricating a
  plausible-looking version string. `supported_stack_version_ref` and
  `stack_list_version`, by contrast, ARE real: a content hash of the
  current catalog, so drift detection (Section 5) genuinely works.
- **`get_deployment_requirements`'s `deployment_yaml` shape is
  hand-encoded** in `tools/discovery.py`, mirroring
  `internal/service/validation_service.go`'s actual enforcement as of this
  writing — there's no Platform API endpoint that serves this as data. If
  the validation engine's rules change, this description can silently
  drift out of sync until someone updates it here too.
- **Production approval (Section 12) has no independent-approver
  guarantee** — same gap as `platform-api`'s `DecideApproval`: the
  approver isn't required to be a different person than the requester,
  which needs real RBAC that doesn't exist.
- **`get_application_status` answers for any signed-in employee, not just
  owners.** Section 13.7 limits it to owners and contributors, but the
  Platform API endpoints it reads (`GET /applications/{id}`,
  `.../deployments/latest`) aren't owner-gated — the same catalog-wide
  visibility the Admin Portal has. Only the secret names are owner-only,
  which is why a non-owner gets `secrets: null` rather than an error.
  Closing this properly means deciding what employees may see of each
  other's applications, which is RBAC (`DEC-001`/`DEC-002`) territory.
- **No transport beyond stdio.** Section 2's "exact transport binding...
  is an implementation decision" is left as stdio only (the most common
  local Claude Code integration) — `mcp.run(transport="stdio")` in
  `server.py`. Remote HTTP/SSE transport, and the hosting-topology
  decision that goes with it, is future work, not designed against here.

## Dev-mode identity (temporary — see DEC-003)

```
MCP_ENV=dev
MCP_EMPLOYEE_EMAIL=alice@example.com
MCP_EMPLOYEE_NAME=Alice Employee        # optional
MCP_EMPLOYEE_DEPARTMENT=Engineering     # optional
```

Every tool call in this server process acts as this one employee — there
is no per-call identity, because there is no real MCP session-token
mechanism yet (Section 3, `DEC-003`). This is a process-startup binding,
not a security boundary of its own; the Platform API's dev-mode auth stub
(`platform-api`'s own `DEC-001` gap) is what actually authenticates every
downstream call.

## Running locally

```
cd services/mcp-server
python -m venv .venv
source .venv/Scripts/activate   # or .venv/bin/activate on Linux/macOS
pip install -e ".[dev]"
cp .env.example .env            # then edit MCP_EMPLOYEE_EMAIL etc.
```

Start the Platform API first (from the repo root — see
[`../platform-api/README.md`](../platform-api/README.md)'s "Running
locally"):

```
cp .env.example .env   # repo root
docker compose up --build
```

Then, with `services/mcp-server/.env` sourced into the environment:

```
python -m mcp_server.server
```

This is what a real MCP client (Claude Code, or `scripts/e2e_verify.py`
below) spawns as a subprocess over stdio — running it directly like this
will just sit waiting for stdio protocol frames, which is expected.

## Running tests

```
cd services/mcp-server
pip install -e ".[dev]"
pytest
```

All unit tests (`tests/test_*.py`) use `tests/fakes.py`'s
`FakePlatformClient` — an in-memory fake mirroring `platform-api`'s own
`fakeXRepo` pattern, no network or Docker involved. `test_platform_client.py`
specifically tests the HTTP-error-to-`ErrorCode` mapping logic using
`httpx.MockTransport`, also without a real server.

### Real end-to-end verification

`scripts/e2e_verify.py` is **not** part of the pytest suite — it spawns
the actual server as a subprocess (exactly how Claude Code would) and
drives it through a full workflow via a real `mcp` protocol client
session, against a REAL running Platform API:

```
# from repo root: docker compose up -d --build (with .env present)
cd services/mcp-server
python scripts/e2e_verify.py
```

It registers its application under a fresh name each run
(`mcptest<random>`): a deleted application keeps its name, so the
original fixed `mcptest` made the script single-use against any one
database — found when re-running it for Module O.

This is what was actually run to verify this server for real, not just
unit-tested against fakes. What it covers, in order, all through the real
MCP stdio protocol (not calling Python functions directly):

1. Confirms all 21 tools (13 from Section 13, plus the eight
   later-module tools for Modules W/X/AB/E) are discovered via
   protocol-level `list_tools()`.
2. `get_platform_info`, `get_supported_stacks`, `get_deployment_requirements`
   — real reads against the real catalog.
3. `create_application` with an unknown department — confirmed rejected
   with `VALIDATION_ERROR` before any Platform API mutation.
4. `create_application` (real), `validate_application` (real, `passed:
   true`) for a real Go "hello world" service.
5. `deploy_application` with `source_archive_base64` set to a real Go
   "hello world" service — builds **and** deploys v1 in one MCP call,
   confirmed `running`, confirmed `get_application_status` reports a live
   URL whose actual HTTP response is v1's text (not just a status field),
   confirmed `get_deployment_status` reports `COMPLETED` with a real
   `updated_at` (previously always `null` — see **How
   `deploy_application` closes the build gap**).
6. `restart_application` — confirmed `COMPLETED` with a real
   `restarted_at` (same previously-`null` bug, same fix).
7. `get_application_logs`/`get_application_metrics` — confirmed both
   return the honest Module-S/Module-T-doesn't-exist `INTERNAL_ERROR`,
   not a crash or a fabricated empty result.
8. `deploy_application` again, different source, **same already-`running`
   application** — a genuine rebuild-and-redeploy of v2, confirmed the
   live URL's actual HTTP response changed to v2's text. This is the
   closed gap exercised directly, not a workaround around it.
9. `deploy_application` a third time with deliberately broken Go source —
   confirmed rejected as `VALIDATION_ERROR` carrying the real compiler
   error, confirmed the application stayed `running` and traffic kept
   serving v2's response throughout (a failed rebuild attempt never
   touches the still-live previous version).
10. `rollback_application` with `target_version="previous"` — confirmed
    it resolved to v1's actual deployment id (not a guess), confirmed the
    live URL's response flipped back to v1's text.
11. `delete_application` with the wrong confirmation string — confirmed
    rejected with `VALIDATION_ERROR` and neither `archive` nor `delete`
    called on the Platform API. Then with the correct confirmation —
    confirmed it archived-then-deleted (verified call order), confirmed
    the application's final state is `deleted`.

Between steps 5 and 6, `query_audit_log` and `list_notifications` /
`mark_notification_read` are also exercised for real: confirmed the
application-scoped audit query shows the real `application.register`/
`application.validate` entries, confirmed a *separate*, deployment-scoped
query shows `deployment.deploy` — found via this exact run that
`deploy_service.go`'s audit entries are recorded under
`resource_type=deployment`, not `application`, which an earlier draft of
this script got wrong (fixed here, not a Platform API bug) — confirmed
`list_notifications(unread_only=true)` shows a real `deployment_status`
notification for v1's deploy, confirmed `mark_notification_read` sets a
real `read_at`, and confirmed the notification then drops out of the
unread list.

Between steps 6 and 7 it also exercises Module AB and Module E's tools
against that same live application: confirmed `get_application_inventory`
listed it with the real stack (`["go"]`) and environment (`dev`) read off
the live record, confirmed the inventory's own `count` matched its rows,
confirmed `get_deployment_activity` counted this run's real deploy and
broke it down by the real environment, confirmed
`list_application_owners` reported the real active primary owner, and
confirmed both of `grant_application_access`'s rejections for real — an
`access_level` of `primary` refused before the platform is even called,
and a genuinely unknown employee refused by the Platform API itself.

Every one of the 21 tools was exercised for real in this run — including
both the two that will never succeed (`get_application_logs`,
`get_application_metrics`) — not just the ones that were easy to make
pass. The first full run of this exact script (back when it covered 13)
is also what surfaced the context-cancellation bug described above: it
failed partway through step 8 with a stuck build, which is what led to
finding and fixing the root cause rather than just retrying past it.
