# Platform API — Draft through Archive/Delete (State 1–8) + Audit Log, Notifications, Ownership, Reporting

Go implementation of the Business API, built one Application Lifecycle state
at a time. Currently covers **Draft**, **Validated**, **Build**,
**Deploying**, **Scale-to-Zero** (the ongoing behavior of a `Running`
application), **Suspend/Resume/Restart**, **Rollback**, **Archive/Delete**
— every Application Lifecycle state reachable without Module P (Domain
Management), which doesn't exist yet — together with **Modules N and O**
(Database and Secret Management), **Module S (Logging)**, **Module T
(Monitoring)** and **Module R (Continuous Health Monitoring &
Remediation)**, plus
**Module W (Audit Log)**, an append-only, hash-chained record of every
state-changing action across all of the above; **Module X
(Notification)**, an in-app notification inbox for deployment status and
production-approval events; **Module E**'s ownership management
(co-owner/contributor grants and primary-ownership transfer); and
**Module AB (Reporting)**, two read-only reports derived from data the
platform already holds.

Implements from
[`../../docs/02_Functional_Requirements.md`](../../docs/02_Functional_Requirements.md):
`FR-011`, `FR-012`, `FR-013`, `FR-015` (Modules D/E — Draft state);
`FR-019`, `FR-021`, `FR-023`, `FR-024`, `FR-029`, `FR-030`, `FR-031`(partial),
`FR-033`, `FR-034` (Modules F/G/H — Validated state; `FR-032` resource quota
is honestly reported as **skipped**, not faked — see below);
`FR-035`, `FR-036`, `FR-037`, `FR-038` (Module I — Build state, real Docker
builds, not mocked); `FR-039`–`FR-044` (Module J — Deploying state: real
Trivy image scanning, a real production approval gate, and real container
start/health-check/traffic-activation); `FR-051`–`FR-056` (Module L —
Scale-to-Zero: real idle detection, real cold-start-on-request through a
stable proxy URL, real event logging); `FR-045`, `FR-047`, `FR-048`,
`FR-049`, `FR-050` (Module K — the full Application Lifecycle model, plus
Suspend/Resume/Restart and Archive/Delete); `FR-095`, `FR-098`, `FR-099`,
`FR-100`, `FR-101` (Module V — Rollback, both the requester-initiated and
the automatic path — see below); `FR-103`, `FR-104`,
`FR-105`, `FR-106` (Module W — Audit Log — see below); `FR-107`,
`FR-108` (Module X — Notification — see below); `FR-016`, `FR-017`
(Module E — Co-Owner/Contributor Management and Transfer Ownership — see
below); `FR-127`, `FR-128` (Module AB — Reporting — see below); and
`FR-086`, `FR-087`, `FR-089` (Module S — Logging — see below);
`FR-090`, `FR-091` (Module T — Monitoring — see below); and `FR-084`,
`FR-085` (Module R — Continuous Health Monitoring & Remediation — see
below; `FR-082`, `FR-083` — default health check and the deploy-time gate
— predate this module, see **How Deploy works**). See
[`../../docs/13_API_Requirements.md`](../../docs/13_API_Requirements.md) for
the Business API this implements, and
[`../../docs/10_System_Architecture.md`](../../docs/10_System_Architecture.md)
for how it fits the Control Plane.

## What's implemented

| Endpoint | FR | Notes |
|---|---|---|
| `POST /applications` | FR-011, FR-012, FR-015 | Registers an app in `draft` state; caller becomes primary owner |
| `GET /applications` | — | Paginated list |
| `GET /applications/{id}` | — | |
| `PATCH /applications/{id}` | FR-013 | Metadata only — never changes lifecycle state; owner-only |
| `GET /applications/{id}/owners` | FR-015 | |
| `POST /applications/{id}/owners` | FR-017 | Grants co-owner (`secondary`) or contributor (`technical`) access by email; primary-owner-only — see **How Co-Owner/Contributor Management works** |
| `DELETE /applications/{id}/owners/{userId}` | FR-017 | Revokes a previously-granted co-owner/contributor; primary-owner-only; never touches the primary owner's own row |
| `POST /applications/{id}/ownership-transfer` | FR-016 | Nominates a new primary owner by email; primary-owner-only; notifies the nominee (Module X) — see **How Ownership Transfer works** |
| `GET /applications/{id}/ownership-transfer` | FR-016 | `{"transfer": ...}`, or `{"transfer": null}` — always `200`, never `404`, for "nothing pending" (the ordinary state most of the time — see the handler's own doc comment for why, and the Admin Portal PR that found this the hard way) |
| `POST /ownership-transfers/{transferId}/accept` | FR-016 | Only the nominated new owner may call this |
| `PUT /applications/{id}/deployment-yaml` | FR-023 | Saves a `deployment.yaml` draft (must parse as YAML); reverts `validated` back to `draft` since the contract changed; owner-only |
| `POST /applications/{id}/validate` | FR-029–034 | Runs the aggregate validation pass; `draft` → `validated` on success. Only callable from `draft`. Owner-only |
| `GET /supported-stacks` | FR-019 | Lists the IT-governed Supported Stack catalog (seeded by migration `0002`) |
| `POST /applications/{id}/build` | FR-035–038 | Triggers a real Docker build from an uploaded source archive; `validated` → `build` (success) or `failed`. Only callable from `validated`. Owner-only |
| `GET /applications/{id}/builds/latest` | FR-036 | Queryable build status |
| `POST /applications/{id}/deploy` | FR-039–044, FR-061 | Runs the deploy pipeline (Image Scan → [prod approval gate] → Deploy → Health Check → Traffic Activation). Provisions the application's database first if `deployment.yaml` declares one — see **How Database Management works**. Only callable with a successful build. Owner-only |
| `GET /applications/{id}/deployments/latest` | FR-043 | Queryable deployment status |
| `GET /applications/{id}/deployments` | FR-095 | Full deployment/version history, newest first — what a Rollback target is picked from |
| `POST /applications/{id}/rollback` | FR-098, FR-100, FR-101 | Redeploys a previously-successful deployment's build artifact; requires `{"target_deployment_id": "..."}`. Owner-only |
| `POST /deployments/{deploymentId}/approve` | FR-042 | Approve/reject a `pending_approval` production deployment. Owner-only |
| `GET /applications/{id}/scale-events` | FR-056 | Every scale-up (incl. cold-start) / scale-down (incl. scale-to-zero) event for the app's current deployment |
| `ANY /run/{appName}/{serviceName}/*` | FR-051–053 | **The public, stable URL for a deployed application.** Not behind platform auth — see below |
| `POST /applications/{id}/suspend` | FR-047 | `running` → `suspended`: stops every container (not just scale-to-zero-eligible ones), retains config. Owner-only |
| `POST /applications/{id}/resume` | FR-048 | `suspended` → `running`: restarts non-eligible services immediately; eligible ones stay at zero and cold-start on demand as usual. Owner-only |
| `POST /applications/{id}/restart` | FR-048 | Recycles currently-running instances in place — same version, fresh containers, no redeploy. Owner-only |
| `POST /applications/{id}/archive` | FR-049 | `running`/`suspended` → `archived`: releases compute more permanently than Suspend, retains config/history. Owner-only |
| `POST /applications/{id}/delete` | FR-050, FR-065 | `archived`/`suspended` → `deleted` (terminal) — or straight from `draft`/`validated`/`build`/`failed` while nothing is running or in progress (`409 application_still_live` otherwise); requires `{"confirm": true}`. Deprovisions the application's database, if it has one, and deletes its secrets, before the status moves. Owner-only |
| `GET /applications/{id}/secrets` | FR-066, FR-070 | Names, versions and who last set each secret — never a value. Includes platform-managed ones (the database password). Owner-only — see **How Secret Management works** |
| `PUT /applications/{id}/secrets/{name}` | FR-066 | Sets or replaces a secret from `{"value": "..."}`. Write-only: the response is metadata, and no endpoint ever returns a value. Takes effect at the application's next container start. Owner-only |
| `DELETE /applications/{id}/secrets/{name}` | FR-066 | Removes an owner-set secret; a platform-managed one is refused with `409`. Owner-only |
| `POST /applications/{id}/secrets/{name}/rotate` | FR-068 | Rotates a platform-generated secret — today, the database password: a new one is set on the database (the old one stops authenticating), stored, and running instances are restarted onto it. `409 secret_not_rotatable` for an owner-set secret. Owner-only — see **Rotating the database password** |
| `GET /applications/{id}/logs` | FR-086, FR-087, FR-089 | What the application's containers printed, newest first, with every secret the platform injected redacted. Filter by `service`/`environment`/`contains`/`since`/`until`/`limit`; a full page carries a `next_cursor`. Owner-only, and a non-owner gets the same `404` as a nonexistent application; every read is audited — see **How Logging works** |
| `GET /applications/{id}/metrics` | FR-090, FR-091 | CPU and memory sampled from the application's containers, plus requests, errors and latency counted at the proxy. Filter by `service`/`environment`/`from`/`to` (default: the last hour). Always says whether collection is keeping up. Owner-only, with the same `404` as a nonexistent application — see **How Monitoring works** |
| `GET /audit-log` | FR-104 | Filter by `actor_user_id`/`resource_type`/`resource_id`/`action`/`from`/`to`/`limit`; scoped to entries the caller performed or that concern an application they own |
| `GET /audit-log/export` | FR-105 | Same filters, CSV response; the export itself is recorded as a new audit entry |
| `GET /audit-log/integrity` | FR-106 | Recomputes the hash chain end-to-end; reports the first `seq` where it breaks, if any |
| `GET /notifications` | FR-107, FR-108, FR-085 | The caller's own notifications, newest first; `?unread_only=true` filters to unread. Also where a Module R automatic-remediation notification shows up — see **How Continuous Health Monitoring works** |
| `POST /notifications/{id}/read` | — | Marks one of the caller's own notifications read; a different user's notification id 404s, not 403 — see **How Notifications work** |
| `GET /reports/application-inventory` | FR-127 | Current-state inventory of every application the caller owns — see **How Reporting works** |
| `GET /reports/deployment-activity` | FR-128 | Deployment outcomes over a period (`?from=&to=` RFC3339, default last 30 days), broken down by environment and department |

### How Build works

The request body to `POST /applications/{id}/build` **is** the source: a
`tar.gz` archive with a top-level directory per declared service name (e.g.
`frontend/`, `api/`), matching `deployment.yaml`'s `services` keys. For each
service, the Build Engine (`internal/buildengine`, real Docker Engine API
via the mounted socket — see `docker-compose.yml`) extracts that service's
subtree, generates a Dockerfile from a fixed per-runtime template (FR-037 —
the employee/agent never chooses or supplies a base image; `deployment.yaml`
has no field for one), and runs a real `docker build`.

**Why an uploaded archive instead of a git URL:** the docs never specify a
git-hosting/branch convention (`FR-035` only says "source is accessible to
the platform's build system") — inventing one wasn't defensible. This is
documented explicitly as a v1 convention, not a permanent design decision.

**Per-runtime build convention** (also a documented v1 simplification, since
`deployment.yaml` has no custom build/start-command fields):

| Runtime | Convention | Multi-stage? |
|---|---|---|
| `go` | `go.mod` at the service root, buildable via `go build .` | Yes — builder uses the governed image, final stage is a fixed minimal `alpine:3.21` runtime (see below for why) |
| `nodejs` | `package.json` with a `start` script | Yes — production dependencies on the same Node image, with npm/npx/corepack removed; the `start` script is captured at build time and run by `sh` with `node_modules/.bin` on `PATH` |
| `typescript` | `package.json` with a `build` script (e.g. `tsc`) and a `start` script | Yes — built with devDependencies, pruned to production ones, then run like `nodejs` |
| `python` | `requirements.txt` (optional) + `app.py` | No — the interpreter is needed at runtime anyway |
| `react` | `package.json` with a `build` script, output in `build/` (Create React App's default; Vite with `build.outDir: 'build'`) | Yes — final stage is `nginx:1.30-alpine` holding only the static output, with an `index.html` fallback for client-side routes |
| `vue` | `package.json` with a `build` script, output in `dist/` (Vite convention) | Yes — same as react |
| `nextjs` | `package.json` with a `build` script, served via its `start` script (`next start`) | Yes, but only to drop npm — it still ships all of `node_modules`; a properly optimized build needs Next's "standalone output" mode, not implemented here (documented gap, not silent) |

**Runtime images carry no package manager.** The first real deploy of the
LAN starter templates (React + each API language + MySQL) was blocked by
the Image Scan gate: every Node-based image carried a CRITICAL in npm's
own bundled `tar` (CVE-2026-59873) — npm, a build tool, was in the runtime
image only because the old recipes ran `npm start` / `serve`. Static
frontends now run on nginx and Node services run their `start` script
without npm, so a `start` script must not itself call `npm`. The same
deploy prompted moving the Node build images from `node:20-alpine` (out
of upstream support since April 2026) to `node:22-alpine` (migration
0017). Builds use the base image the Docker daemon already has; nothing
pulls a newer one automatically, so keeping them patched (`docker pull`
on the platform host) is part of IT's FR-022 governance.

**Why `go` needed fixing to multi-stage — found for real, not assumed:**
a single-stage `golang:*-alpine` build leaves the entire Go SDK/toolchain in
the final runtime image. When the Image Scan gate (below) was exercised for
real, Trivy correctly flagged 22 CRITICAL findings on the toolchain binaries
themselves (`usr/local/go/bin/go`, `.../pkg/tool/...`) — real CVEs, just
irrelevant ones, since nothing in the runtime image should ever execute the
Go compiler. Multi-stage (copy only the compiled binary into a minimal final
image) fixed this and dropped image size from 338MB to ~24MB. The governed
base_images catalog (FR-037) still only names the BUILD-stage image per
runtime; the fixed minimal runtime-stage image for `go`/`react`/`vue` (and
the npm-free Node stage) is a
platform constant, not (yet) catalog-governed — a reasonable Module F
enhancement for later, flagged in code, not hidden.

A failed build is a normal, fully-reported outcome (FR-038), not an HTTP
error — the response's `error_category` is `source` (compiler/dependency
error — the employee/agent's problem to fix) or `platform` (build
infrastructure problem — not their fault), and `error_detail` includes the
actual tail of the build log, not just Docker's generic "non-zero exit
code" summary. This categorization was itself found buggy during manual
testing (a base-image registry pull timeout was originally reported as
`source`, blaming the employee for a network blip) and fixed — see
`classifyBuildError` in `internal/buildengine/docker.go`.

**`TriggerBuild` accepts `Validated` (first build), or `Running`/`Failed`
(a rebuild)** — not just `Validated`. This closes a real gap found while
building `../mcp-server`: `deploy_application`'s MCP spec assumes Build
happens invisibly as the first stage of its own pipeline, but there was
previously no way to build a *second* version for an already-`running`
application at all, through the API or otherwise. Mirrors
`deploy_service.go`'s `InitiateDeploy`, which already allows redeploying
while `Running` — a rebuild is the same category of operation one step
earlier. A failed rebuild attempt leaves a previously-`running`
application exactly as it was (a failed *build* never touches running
infrastructure in the first place, so there's even less reason to move it
to `Failed` than a failed redeploy has); only a first-ever build or a
retry from `Failed` has nothing good to fall back to.

**A real concurrency/robustness bug found and fixed alongside that
change:** the failure-cleanup path (`MarkFailed` + reverting the
application's status) used the same request-scoped `context.Context` as
the build attempt itself. A client that disconnects or times out during a
genuinely slow (not hung) build cancels that context — which is *why*
`s.engine.Build` returns an error in the first place — but the cleanup
write meant to *record* that failure then used the same already-cancelled
context too, so it failed as well. Confirmed for real: a client-side
timeout during manual verification left a `builds` row stuck
`in_progress` forever (no `completed_at`) and the application stuck at
`lifecycle_status = 'build'` — a status nothing else accepts as a valid
starting point, making the application permanently unrecoverable through
the API. Fixed by giving the cleanup write its own
`context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)` — it
keeps the parent context's values but survives the parent's cancellation,
with its own bound so a genuinely dead database doesn't hang forever
either. The identical pattern was found and fixed in
`deploy_service.go`'s `markDeploymentFailedFrom` too (health checks alone
can take up to 15s, the same failure mode applies). The same category of
bug likely exists in other failure/cleanup paths across this codebase that
weren't specifically exercised by a slow-enough operation during
verification — worth an audit, not something this fix claims to have
swept comprehensively.

## How Deploy works (Module J)

`POST /applications/{id}/deploy` — body `{"environment": "dev"}` (or
`"production"`; defaults to `dev`) — requires the application to have a
successful build, and runs:

1. **Image Scan** (FR-041) — every service's built image is scanned by a
   real [Trivy](https://github.com/aquasecurity/trivy) container (sibling to
   `platform-api` on the same Docker daemon — see `internal/imagescan`).
   Any **CRITICAL**-severity finding fails the gate (the most defensible
   default in the absence of a published Security Administrator threshold —
   worth adding as a `DEC-xxx` item). Scan results, per-service pass/fail,
   and counts are all persisted and returned, not just a boolean.
2. **Production Approval Gate** (FR-042) — `dev` skips straight to step 3.
   `production` pauses with `status: "pending_approval"` and creates a
   `deployment_approvals` row (with any `test_report` the request
   carried). With **`PLATFORM_APPROVER_KEYS`** set, the normal case per
   `DEC-017`, only a decision **signed with a platform administrator's
   private key** moves it on; see **How signed approvals work** below.
   Without keys, `POST /deployments/{id}/approve` with
   `{"decision": "approve"|"reject", "reason": "..."}` from an owner
   resumes or rejects it, as it did before `DEC-017`.
   `APPROVAL_REQUIRED_ENVIRONMENTS` (default `production`) can add `dev`.
   `GET /policy` reports these rules from the configuration
   (`approval_required_environments`, `signed_approval`, `approvers`). The
   MCP server's `get_platform_info` relays them to Claude, so the skill
   needn't assume dev never waits.
3. **Deploy** — `internal/runtimeengine` (real Docker Engine API — the
   platform's actual, permanent runtime per `DEC-004`'s 2026-09-11
   revision, a single existing on-prem Docker host rather than a future
   Kubernetes migration; still swappable later per `NFR-046` if that ever
   changes) starts one container per service, port published dynamically.
4. **Health Check** — polls the container until it responds or a 15s
   timeout elapses (`FR-083`'s pre-activation gate; a simplified stand-in
   for `FR-082`'s configurable check — it always polls `/`). Failure here
   stops the containers just started for *this* attempt only. The same
   check keeps running after activation, continuously, for as long as the
   instance is `Running` — see **How Continuous Health Monitoring works
   (Module R)** below.
5. **Traffic Activation** — on success, the application moves to `running`
   and the deployment's `containers` field carries each service's reachable
   URL. If a different deployment was already `running` for this
   application, a successful new one **supersedes and stops it** — a clean
   cutover, verified for real (see Test plan below), not just asserted.

**FR-044 (failure handling)**, implemented precisely: a failure that happens
while a *previous* version is already `running` (a failed redeploy attempt)
never touches that previous version — the application's `lifecycle_status`
only moves to `failed` when there was no prior good version to protect. This
was unit-tested explicitly (`TestInitiateDeploy_HealthCheckFailure_Redeploy_LeavesAppRunning`).
An *automatic* rollback triggered by a post-activation health regression
(FR-099) is now wired up too: when Module R's continuous health monitoring
(below) exhausts remediation on a deployment still within its own
post-activation window, `DeploymentService.TriggerAutomaticRollback` rolls
back to the last known-good version with no human involved — see **How
Rollback works** below for both the deliberate, requester-initiated path
(FR-098) and this automatic one.

**A subtle networking fix worth knowing about:** health checks happen
*from inside* the `platform-api` container, so `http://localhost:<port>`
(meaningful only from the host machine's browser, and what's actually
returned to callers as the deployment's URL) does **not** reach the sibling
container just started. Health checks use `http://host.docker.internal:<port>`
instead — see the comment in `deploy_service.go` and the `extra_hosts` entry
in `docker-compose.yml` (needed for portability to Linux Docker Engine,
where that hostname isn't automatic like it is on Docker Desktop).

## How Scale-to-Zero works (Module L)

**The core problem this solves:** once deployed, a service's Docker-published
host port changes every time it stops and starts (`StartContainer` in
`internal/runtimeengine` always asks Docker for a free port). Nothing can
hand out that port as "the" URL for a scale-to-zero-managed service — it
won't be the same port next time. `ANY /run/{appName}/{serviceName}/*`
(`internal/httpapi/handlers_proxy.go`) is the fixed address that stays
constant across scale events; it resolves to the current live container on
every request, cold-starting one first if needed. This is deliberately
**not** behind platform auth — it's the deployed application's own public
traffic path, not a platform management endpoint.

1. **Eligibility (FR-051)**, determined once when a deployment activates and
   never re-derived from later employee input: a service is scale-to-zero
   eligible only if its runtime is `backend`-kind (per the Supported Stack
   catalog) **and** the app-wide `scaling.min` is `0` or unset. Static
   frontend services are never eligible, full stop — matching FR-051's
   business rule that this "cannot be overridden by configuration."
   `scaling.min >= 1` is FR-055's opt-out, already implicit in this same
   rule — no separate code path needed.
2. **Idle detection (FR-052)** — a background sweeper
   (`runScaleSweeper` in `cmd/api/main.go`) ticks every
   `SCALE_SWEEP_INTERVAL_SECONDS` (default 300s/30s — see `.env.example`;
   both configurable, since FR-052's business rule frames the idle
   threshold as a *platform-defined* default, not something
   `deployment.yaml` should expose to employees) and stops the container
   for any eligible service idle past `SCALE_TO_ZERO_IDLE_SECONDS`.
3. **Cold-start (FR-053)** — `ScaleService.EnsureRunning` starts a fresh
   container (reusing a deterministic name — Docker's port/name get freed
   on stop, so this is safe across repeated scale cycles), health-checks it
   the same way the deploy pipeline does, then hands the request through.
   Concurrent requests during a cold start are coalesced via
   `golang.org/x/sync/singleflight` — verified for real (see Test plan):
   5 simultaneous requests during one cold start produced exactly one
   `cold_start` scale event, not five.
4. **Scale events (FR-056)** — every transition (`initial_activation`,
   `cold_start`, `idle_timeout`) is recorded and queryable via
   `GET /applications/{id}/scale-events`.

**A documented race, not a hidden one:** the idle sweeper and a concurrent
cold-start aren't coordinated by a shared lock (see the comment on
`ScaleService.coldStart` for why that would actually be wrong — singleflight
sharing one result across *different* intended operations is unsafe here).
The actual data-safety guard is a compare-and-swap in
`ServiceRuntimeStateRepo.ClearContainer`: it only clears a row if
`container_id` still matches what the sweeper observed, so a concurrent
cold-start's new container can never be silently lost from the database —
worst case is a narrow, logged window where a request lands right at a
sweep boundary.

**FR-054 (min/max), scoped honestly:** this implementation only ever runs 0
or 1 instance per service — there's no real horizontal multi-instance
scaling built (that would need a load balancer in front of N containers,
well beyond this state's scope). `scaling.max` isn't enforced as a real
ceiling above 1 for the same reason. This is a known simplification, not an
oversight.

## How an application's URL works (FR-053)

An application is served on the platform's own address:

```
{PLATFORM_PUBLIC_BASE_URL}/run/{application}/{service}
```

That is what every endpoint reporting a deployment returns as each
service's `url`, and what the MCP server hands an agent to pass on to an
employee. It is the one address that survives the container behind it
being replaced — a restart, a redeploy, a scale-to-zero cold start —
because the platform resolves it to whatever container is current, and
starts one if none is (see **How Scale-to-Zero works**).

Until this was fixed, the platform reported `http://localhost:<hostPort>`
instead: the port Docker published for one particular container. It broke
the moment that container was replaced, and it routed traffic around the
proxy — so a scaled-to-zero service never cold-started for it, and Module
T counted none of it. The published port is still reported, as
`host_port`, because it is genuinely useful when debugging from the host
machine itself. It just isn't what the platform calls the application's
URL.

`PLATFORM_PUBLIC_BASE_URL` is how this API is reached from outside its own
container. It can't be derived from the port the API listens on, since
Docker publishes it on a different one; it defaults to
`http://localhost:<PORT>`, and docker-compose sets it from
`PLATFORM_API_HOST_PORT`.

### Verifying it

`scripts/verify_stable_url.py` deploys an application, reads the URL the
platform reports, and fetches it: the response names the container that
answered. It then restarts the application — a new container, on a new
published port — and fetches the same URL again, which answers from the
new container, while the address the platform used to report is dead. It
also confirms the deploy response, the latest-deployment endpoint and the
deployment history all report the same URL, and that requests to it are
counted by Module T, which is what going through the platform rather than
around it buys.

## How Suspend/Resume/Restart works (Module K)

Reuses the `deployments` table rather than a parallel one — `suspended` is
just a new valid `deployments.status` (migration `0006`). This is why the
scale-to-zero proxy needs **zero code changes** to correctly refuse a
suspended application: its existing lookup filters `WHERE status =
'running'`, so a suspended deployment simply stops being found — verified
for real (see Test plan): hitting `/run/{app}/{service}/` on a suspended
app returns a 404, it does not cold-start.

- **Suspend (FR-047)** stops *every* service's container, not just
  scale-to-zero-eligible ones — a suspended frontend shouldn't keep running
  just because it was never scale-to-zero-managed. Config, build history,
  and `service_runtime_state`'s per-service image/port metadata are all
  retained for Resume.
- **Resume (FR-048)** only immediately starts services that were *not*
  scale-to-zero eligible (frontends, or backends with `scaling.min >= 1`)
  — eligible services correctly stay at zero and cold-start on their next
  request, exactly like any other idle period. This satisfies "at least
  `scaling.min` running instances" without special-casing eligible
  services, since their `min` is 0 by definition. A resume whose health
  check fails reports the failure and leaves the app `Suspended` rather
  than silently marking it `Running` (FR-048's alternative flow).
- **Restart (FR-048)** recycles only services that currently *have* a
  container — an idle scale-to-zero service has nothing to recycle, so
  it's left alone. Same image, same port, fresh container id; status and
  version never change.
- Deterministic container naming (`platform-run-<deployment>-<service>`,
  shared with the deploy pipeline and the scale-to-zero proxy) means
  repeated suspend/resume/restart cycles never accumulate orphaned
  containers — confirmed via `docker ps` after a full real cycle, not just
  assumed.

**Two real bugs found while testing this against Docker, both fixed:**
1. The migration that adds `'suspended'` to `deployments.status`'s CHECK
   constraint originally looked up the existing constraint by pattern-
   matching `pg_get_constraintdef()`'s rendered text for `...IN...` —
   but Postgres actually renders `CHECK (x IN (...))` back as
   `CHECK ((x = ANY (ARRAY[...])))`, so the pattern never matched, the old
   constraint was never dropped, and `ADD CONSTRAINT` then failed because
   the (coincidentally identically-named) old one already existed. Fixed
   to find the constraint by its actual constrained *column* (joining
   `pg_constraint`/`pg_attribute`) instead of guessing at rendered SQL
   text — a real lesson in not trusting a system catalog's pretty-printer
   to preserve the original syntax.
2. `Suspend`/`Resume`/`Restart` updated `service_runtime_state` correctly
   (so routing was always right) but never refreshed
   `deployments.containers` — the point-in-time snapshot `toDeploymentResponse`
   serializes. Found by literally reading the API response after a resume
   and noticing it still showed the pre-suspend `container_id`/`host_port`,
   which no longer existed. Fixed by adding `UpdateContainers` and calling
   it from all three operations.

**By decision, not a gap awaiting one:** FR-047 names a Security
Administrator force-suspend path (bypassing the owner-initiated flow, e.g.
on a policy violation) as an alternative flow. There is no distinct
Security Administrator role, by the same `DEC-001`/`DEC-002` decision
(Decided 2026-09-11) as the production approval gate above — this
platform will not have one. Only owner-initiated suspend is implemented.

## How Rollback works (Module V)

Mechanically, a rollback **is** a forward deploy — `Rollback` sources its
build/image refs from a previously-successful `deployments` row instead of
the application's latest build, then runs through the exact same
Deploy/HealthCheck/Activate pipeline (`deployAndActivate`, shared with
`InitiateDeploy`/`DecideApproval`) rather than a separate code path. FR-101
itself frames rollback this way — "redeploying the target version's build
artifact" — so reusing the pipeline isn't a shortcut, it's the literal
requirement. The only difference is the application's transient
lifecycle status: `rolled_back` instead of `deploying`, per FR-101's business
rule (`Running` → `Rolled Back` → `Running`, mirroring how `Deploying` is
already transient for a forward deploy).

- **`GET /applications/{id}/deployments`** (FR-095) lists every deployment
  ever attempted for the application, newest first — this is what a caller
  picks a `target_deployment_id` from. Each `deployments` row already *is* a
  version record (build, config-at-the-time via the linked build, timestamps,
  outcome); no separate `ApplicationVersion` table was needed.
- **`POST /applications/{id}/rollback`** (FR-098) is callable from `Running`
  (rolling away from a live-but-misbehaving version) or `Failed` (the
  forward deploy attempt itself failed outright) — not from a transient
  pipeline state, and not from `Suspended`, which has no live traffic to
  roll back in the first place.
- **Target validation** (FR-100) rejects a target that wasn't itself a
  successfully-completed deployment (`running` or `superseded` only — never
  `failed`/`rejected`/still in flight) or whose build artifact is no longer
  available.
- **Execution** (FR-101) creates a **new** `deployments` row rather than
  reactivating the old one — the old row's stale container id/host port are
  long gone by rollback time, and a fresh row keeps deployment history
  fully auditable (matches how a normal redeploy already works, never
  mutating a past row's identity). On success, whatever was previously
  `running` is superseded and its container stopped, same clean cutover as
  any other successful deploy.

**Scope adaptations, documented not hidden:**
- Rollback **skips** the Image Scan and production approval gates that a
  forward deploy goes through — the target was already a completed,
  previously-`running` deployment, so it passed both gates the first time.
  FR-098's alternative flow allows requiring the same approval gate for a
  production rollback depending on policy severity classification, which
  needs policy-tier modeling this platform doesn't have — same
  `DEC-001`/`DEC-002` decision (Decided 2026-09-11) as the approval gate's
  approver-independence point above.
- FR-102 (rollback notification) is now partially real: since `Rollback`
  shares `deployAndActivate`/`markDeploymentFailedFrom` with a forward
  deploy (see above), it automatically triggers the same Module X
  notification on both success and failure — an owner genuinely gets
  notified. What's missing is FR-102's specific content requirements for
  the *manual* path: the notification text is the generic "deployment
  succeeded/failed" wording, not rollback-specific ("rolled back to version
  X", trigger reason, prior vs. new active version). `TriggerAutomaticRollback`
  below does write rollback-specific content — a manual rollback still
  doesn't.

### FR-099 — automatic rollback, triggered by Module R

`DeploymentService.TriggerAutomaticRollback` (called from
`health_service.go`, not from any HTTP endpoint — there's no "trigger a
rollback for me automatically" request to make) closes what used to be a
documented gap: when Module R's continuous health monitoring (below)
exhausts remediation (its own restart-in-place fails, or its circuit
breaker opens) on a deployment still within its own post-activation window
(`POST_ACTIVATION_ROLLBACK_WINDOW_SECONDS`, default 300s — an engineering
default, nothing in the requirements sets it), it rolls back to the last
known-good version with no human involved. Verified for real against a
live Docker daemon (`scripts/verify_fr099_rollback.py`): a version that
passes its own pre-activation health check but then fails every
subsequent one — even from a freshly restarted container, so Module R's
own fix can never actually fix it — gets automatically rolled back to the
previous version, which resumes serving at the application's stable URL,
with the regressed version left `superseded` like any other rollback.

An established (not recently activated) deployment that degrades does
**not** trigger this — only Module R's existing restart-in-place/escalate
handling applies there, per FR-099's own business rule that automatic
rollback is specifically about a version regression, not routine instance
flakiness the platform already knows how to fix in place.

**Scope adaptations, same spirit as the manual path above:**
- **Attribution.** `requested_by` is a real, required foreign key to a
  user — there's no "system" account in this platform's identity model
  (`DEC-001`/`DEC-002`). An automatic rollback is attributed to the
  application's own primary owner, the only accountable party this
  platform's ownership-only permission model has.
- **No prior version to roll back to** (FR-099's alternative flow): the
  application is marked `Failed` and the owner is notified why, rather
  than pretending a rollback happened. Same outcome if the only
  otherwise-eligible prior version's build artifact isn't usable (FR-100).
- **The rollback itself failing** (FR-099's exception flow): escalated
  immediately to the owner — there's no Platform Administrator to also
  escalate to (`DEC-002`).
- **A known, narrow gap, not hidden:** `deployAndActivate` decides whether
  a *failed* attempt leaves the application `Running` or `Failed` based on
  `app.LifecycleStatus` at the moment it's called — but Module R's own
  remediation never updates that field when it gives up on a service. In
  the rare case where Module R had already silently taken the previous
  deployment's containers down before this fires, *and* the rollback
  attempt's own health check also fails, the application can end up
  reporting `Running` with nothing actually running. The same latent gap
  already existed on the manual `Rollback` path whenever its target also
  fails; automating the trigger makes it reachable without a human in the
  loop, not new. Closing it fully needs `LifecycleStatus` to reflect live
  container state directly — see `TriggerAutomaticRollback`'s own doc
  comment in `deploy_service.go`.

**A real bug found via the new unit tests, not just manual testing:**
`deployAndActivate`'s final `apps.UpdateLifecycleStatus` call (the one that
lands the application on `Running`) had `domain.StatusDeploying` hardcoded
as the transition's `from` value, left over from before the function took a
`transientAppStatus` parameter. For every *forward* deploy this was
invisible — `transientAppStatus` is always `StatusDeploying` there too, so
the hardcoded value happened to match. Rollback was the first caller to pass
a *different* transient status (`StatusRolledBack`), which made the
mismatch concrete: the CAS update failed with `ErrInvalidLifecycleTransition`
and the whole rollback errored out even though every precondition was
correctly satisfied. Fixed by using `transientAppStatus` consistently at
all three call sites in `deployAndActivate`/`markDeploymentFailedFrom`, not
just two of them. Caught immediately by
`TestRollback_ToSupersededVersion_ReactivatesItAndSupersedesCurrent`
failing before this code ever ran against real Docker — exactly the kind of
bug a shared-pipeline refactor risks, exactly why the new tests were written
before the manual verification pass, not after.

## How Archive/Delete work (Module K)

The last two Application Lifecycle states, and the last states reachable
without Modules N/O/P (Database/Secret/Domain Management) actually existing.
Same reuse-the-existing-`deployments`-table pattern as Suspend
(migration `0006`) — `archived` is migration `0007`'s new
`deployments.status` value, so the scale-to-zero proxy needs no code change
to refuse an archived application either.

- **Archive (FR-049)** is callable from `Running` or `Suspended`. From
  `Running` it stops every container the same way Suspend does (shares the
  same `stopAllContainers` helper) before transitioning; from `Suspended`
  there's nothing left to stop, only the status changes. Rejected while a
  deployment is actively in progress (FR-049's exception flow) — there's no
  cancel-in-flight-deployment path, so the caller has to wait for it to
  finish one way or another. **Business rule enforced literally:** FR-049
  says an archived application "cannot be directly resumed to Running —
  reactivation requires an explicit un-archive action treated as a new
  deployment cycle." No un-archive endpoint exists (FR-049 doesn't specify
  one with its own acceptance criteria), so Archive is, today, a genuine
  one-way door — recoverable only via direct database intervention. This is
  the literal, honest reading of the business rule, not an oversight.
- **Delete (FR-050)** is callable from `Archived` or `Suspended` — and from
  a state that never went live, see the next item. FR-050's precondition
  explicitly excludes deleting directly from `Running` ("requires an
  explicit stop-first confirmation"), implemented by requiring
  Suspend or Archive as a genuinely separate prior action rather than a
  same-request flag. Requires `{"confirm": true}` in the body — FR-050's
  main flow literally says "requester confirms deletion, acknowledging
  irreversibility"; omitting it is a `400`, not silently ignored. Defensively
  stops any container still recorded for the deployment before transitioning
  (shares `stopAllContainers` too) rather than trusting that Suspend/Archive
  already did it. `Deleted` is enforced as terminal implicitly: no method
  anywhere accepts `Deleted` as a valid `from` status, so every other
  lifecycle action already rejects it via the same
  `ErrInvalidLifecycleTransition` path — verified for real (see Test plan).
- **Deleting an application that never went live.** Until this was added,
  an application that had never been deployed could not be deleted by
  anyone: Delete required `Archived`/`Suspended`, and Archive requires
  `Running`. Found for real — two applications abandoned during this
  repo's own verification runs, one `validated` and one `build`, were stuck
  exactly that way. `docs/05_Process_Flows.md` specifies the missing
  transition (`Draft → Deleted`, guarded by "no active deployment attempt
  exists") and `docs/01_BRD.md` generalises it ("any pre-Running state may
  terminate to Deleted directly if abandoned"), so Delete now also accepts
  `Draft`, `Validated`, `Build` and `Failed` — guarded by what is actually
  running, not by the state's name. Neither `Build` nor `Failed` promises
  nothing is live: a rebuild of a running application leaves it in `Build`
  with the previous version still serving, and a failed redeploy of that
  leaves it in `Failed` the same way, with the *latest* deployment record
  being the failed one — so the guard reads every deployment, not just the
  latest. Anything serving, any deployment in progress, or any build queued
  or running is refused with `409 application_still_live`. The same change
  fixed a quieter case: a first deploy that provisioned a database and then
  failed left the application `Failed` with that database running, and
  nothing could ever remove it.

**Known gaps, documented not hidden — this is the module where "not built
yet" is most visible, because FR-050 in particular is *mostly* about
deprovisioning resources that don't exist yet:**
- Domain release (Module P) is still a no-op on both Archive and Delete —
  not because it isn't built yet, but because `DEC-007` (Deferred
  2026-09-11) is that Module P isn't needed at all for this LAN-only
  deployment (see **Self-declared identity** below). Databases and
  secrets are no longer in that list: Delete really does tear down the
  application's database (see
  **How Database Management works**) and purge its secrets (see **How
  Secret Management works**). Archive does neither, by design — it
  retains configuration (FR-049). What Delete DOES do for real:
  guarantees no container, database instance or stored secret is left
  for the application.
- An application in `Build` or `Failed` whose previous version is still
  serving can't be deleted — correctly — but it can't be suspended or
  archived either, since both require `Running`; the only way to retire it
  is to deploy it again first. A pre-existing restriction that the delete
  change made visible, not one it introduced.
- FR-050's production-deletion approval gate ("mirrors FR-014") isn't
  enforced — needs the de-registration approval workflow (Module C), the
  same category of gap as the production-deploy approval gate's
  approver-independence limitation.
- FR-050's audit tombstone ("audit records ... are never deleted") is now
  partially real: Module W exists (see **How Audit Logging works** below),
  so Delete produces a real, immutable `application.delete` audit entry.
  What's still missing is a dedicated tombstone record distinct from the
  general audit trail — the application row itself is simply left in place
  with `lifecycle_status = 'deleted'`, queryable, not erased.
- No un-archive/reactivation path — see Archive's note above.

## How Audit Logging works (Module W)

An append-only, hash-chained record of significant state-changing actions
across every module above: `POST /applications` (register), `.../validate`,
`.../build`, `.../deploy`, `.../rollback`, `.../suspend`, `.../resume`,
`.../restart`, `.../archive`, `.../delete`, and `POST /deployments/{id}/approve`.
Implements `FR-103`–`FR-106`.

**Write path (`FR-103`).** Each instrumented service method calls
`AuditService.Record` immediately after its own state-changing work
succeeds or fails, via a `defer` closing over the method's named return
values — this covers every terminal outcome (including ones buried inside
`deployAndActivate`/`markDeploymentFailedFrom`'s internal branching) without
touching that branching at all. A rejection *before* any real action is
attempted (bad input, unauthorized caller, wrong lifecycle state) is **not**
audited — see each instrumented method's own "audited from here on" comment
for exactly where the line is drawn. `AuditService.Record` uses the same
detached-context pattern as `build_service.go`'s `TriggerBuild`/
`deploy_service.go`'s `markDeploymentFailedFrom`: the write must survive the
triggering request's own context being cancelled, for the same reason —
losing the audit trail matters most exactly when a client disconnects mid
-action.

**Tamper-evidence (`FR-106`).** Every entry stores `prev_hash`/`entry_hash`
— a SHA-256 chain over each entry's own content plus the previous entry's
hash, computed in `AuditRepo.Record` under a Postgres advisory lock
(`pg_advisory_xact_lock`) so concurrent writers can't race and fork the
chain. `GET /audit-log/integrity` (`AuditRepo.VerifyChain`) walks the whole
table oldest-first, recomputing and comparing every hash, and reports the
first `seq` where the chain breaks. On top of that, `UPDATE`/`DELETE` on
`audit_log` are rejected by a database trigger (`audit_log_immutable()`),
not just "the service code never calls them" — verified for real: a raw
`UPDATE audit_log SET detail=... WHERE seq=1` via `psql` as the same
Postgres user the application itself connects as was rejected outright.
Bypassing the trigger entirely (`ALTER TABLE ... DISABLE TRIGGER`, i.e.
simulating a fully compromised superuser) and then editing a row *is*
correctly caught by `VerifyIntegrity` — the two controls compose as
prevention (trigger) plus detection (hash chain), matching FR-106's own
framing.

**Query/export (`FR-104`/`FR-105`), scoped down from the spec.** FR-104
names an Auditor/Security Administrator/Platform Administrator role that
doesn't exist, by the same `DEC-001`/`DEC-002` decision as everywhere else
in this platform — `AuditService.Query` substitutes the same owner-based scoping
used throughout: a requester sees an entry if they performed the action
themselves, or it concerns an application they own. `GET /audit-log/export`
(CSV) implements FR-105's main flow, including step 4 — the export itself
is recorded as a new `audit_log.export` entry. FR-104's "every audit query
is itself logged" business rule is **not** implemented (would need a
Record call on every read, risking a logging feedback loop worth its own
design rather than bolting on here).

**Verified for real**, not just via unit tests (`audit_service_test.go`,
plus one test per instrumented service confirming it records the right
entry): ran the full stack via `docker compose up --build`, registered and
validated a real application through the HTTP API, confirmed the resulting
`audit_log` rows via `psql` and via `GET /audit-log`, confirmed
`GET /audit-log/integrity` reports intact, confirmed a stranger's own query
sees nothing, and confirmed `UPDATE`/`DELETE` are rejected by the trigger.
This real run caught a genuine bug before it shipped: the very first entry
ever written failed its own integrity check, because the hash was computed
over a Go `time.Time` at nanosecond precision while Postgres's `timestamptz`
column only stores microseconds — every entry's hash was unreproducible the
moment it was read back. Fixed by truncating to microsecond precision
*before* hashing (`AuditRepo.Record`), so the value hashed and the value
that round-trips through the database are byte-for-byte identical.

**Known gaps, documented not hidden:**
- No distinct AI-agent/system actor identity (`FR-117`'s "attribute to both
  the agent and the employee") — an MCP-initiated call authenticates as the
  same employee dev-auth identity a direct API/Admin Portal call would use,
  so there is nothing yet to distinguish.
- Not atomic with the state change it's auditing — no cross-repository
  transaction wrapper exists in this codebase, so the audit write happens
  immediately *after* the state change succeeds, not in the same database
  transaction. Its failure is still surfaced to the caller as an error
  (never a bare, silent success for a critical action), it just can't undo
  the state change that already happened.
- Coverage is scoped to the state-changing actions listed above, not every
  FR-103 example verbatim ("authentication, ... secret operations, ...") —
  there's no login flow or Secret Management module (O) yet to audit.
- A fully compromised database superuser could `TRUNCATE audit_log` (or
  disable triggers, insert fabricated-but-internally-consistent rows, and
  re-enable them) — the trigger and hash chain together give strong
  in-band tamper *detection*, not protection against that threat model.

## How Notifications work (Module X)

An in-app, per-recipient notification inbox for `FR-107` (Deployment
Status Notifications) and `FR-108` (Approval Request Notifications).
`FR-109` (Security and Policy Violation Notifications) is a documented
gap for two different reasons: it names a Security Administrator
recipient, and by decision (`DEC-002`) this platform doesn't have one —
that part isn't coming. Separately, and still a genuine buildable gap
regardless of that decision, nothing here proactively re-checks for
violations in the background (e.g. periodically re-running Module W's
`VerifyChain`), unlike the scale-to-zero sweeper.

**Delivery is in-app only** — a queryable list, not email/Slack/webhook.
There is no outbound delivery channel configured anywhere in this
platform, and FR-107's "configured channel(s)" is squarely a Module X
follow-up, not invented here.

**Recipients.** Every notification goes to every *active* owner of the
application — standing in for both "the requester" (who is, by
construction, always an active owner; every deployment-pipeline action
`requireOwner`-gates on exactly that) and FR-108's "designated
approver(s)" (there is no distinct approver role — the same gap
`DecideApproval`'s own doc comment names).

**Trigger points**, all inside `deploy_service.go` since every notified
event is a deployment-pipeline milestone:
- `deployAndActivate`'s successful `-> Running` transition (succeeded —
  covers both a forward deploy and a rollback, since both call this same
  function).
- `markDeploymentFailedFrom` (failed — the single shared failure path used
  by every pipeline stage's failure branch, from image-scan rejection
  through a health-check failure).
- `runScanThenBeyond`'s "pipeline pauses here" return (awaiting
  approval).
- `DecideApproval`'s rejection branch (rejected) — added after manually
  verifying the approval-request notification: an approver rejecting a
  deployment already knows they just did it, but no other owner found out
  until this was added. A real, small gap caught by using the feature, not
  a pre-planned design.

**Best-effort by design, not by omission.** FR-107's own exception flow
says "failure to deliver does not block the underlying deployment
pipeline itself" — unlike `AuditRecorder`, `NotificationRecorder`'s
`NotifyOwners` method returns nothing at all for a caller to react to,
mirroring `scale_event_repo.go`'s `Record` (the other place this codebase
already treats a side-effect write as genuinely best-effort). A failed
write is only logged.

**Verified for real**, not just via unit tests (`notification_service_test.go`
plus dedicated tests in `deploy_service_test.go` for each trigger point):
ran the full stack via `docker compose up --build`, registered, built, and
deployed a real application — confirmed a real `deployment_status`
notification appeared on a successful dev deploy, confirmed `unread_only`
and mark-read work and that a different user can't mark someone else's
notification read (404, not 403 — doesn't even confirm the id exists to an
unauthorized caller), confirmed a production deploy produces a real
`approval_request` notification, and confirmed rejecting it produces the
`deployment_status` rejection notification described above — which is
exactly how the gap above was found, not hypothesized.

**Known gaps, documented not hidden:**
- FR-107's exception flow calls for delivery retry "per policy" on
  failure — there is no retry queue, a failed write is only logged.
- FR-108's "reminder notifications ... until the approval expires" isn't
  implemented — there is no approval-expiry concept anywhere in this
  platform yet.
- FR-109 (security/policy violation notifications) isn't implemented at
  all — see this section's opening paragraph.
- "Success notifications may be configurable (digest vs. immediate) per
  employee preference" (FR-107) doesn't exist — every notification is
  immediate, with no preference model.

### Known gap: no retry path out of Failed yet

A failed build moves the application to the `failed` lifecycle state. There
is currently no endpoint to get it back to `draft`/`validated` for a retry —
`PUT .../deployment-yaml` only accepts `draft`/`validated` as source states.
FR-038's alternative flow ("Claude Code parses the failure and attempts a
fix before resubmitting") implies this should exist; it's an honest gap for
a future increment (likely part of a fuller Module K lifecycle-transition
implementation), not something silently worked around here. Hit directly
while manually verifying Rollback: a build failure (bad source archive
layout) left a test application stuck `failed` with no successful
deployment behind it — Rollback correctly can't help here either, since
there's nothing to roll back *to*. The verification continued with a fresh
application rather than working around this gap.

### Validation report shape

`POST /applications/{id}/validate` returns `{"application": {...}, "report": {"valid": bool, "checks": [...]}}`.
Each check is `passed`, `failed`, or **`skipped`**. `resource_quota` is
always `passed`, because `DEC-014` (decided 2026-10-23) is that there are no
quotas. Its detail says so, so nobody reads it as a limit that was checked.
Until that decision it was reported `skipped` rather than given a fake pass.

The one concretely-enforced part of FR-031 (security pre-check) so far: any
top-level field outside `app/services/database/scaling/resources/domain` is
rejected outright (`security_precheck` check, via strict YAML field
checking) — there is no way to smuggle raw Kubernetes/Docker config through
`deployment.yaml`, regardless of who or what generated it.

## How Co-Owner/Contributor Management works (Module E, FR-017)

`domain.ApplicationOwner`'s `OwnershipRole` type (`primary`/`secondary`/
`technical`) and the `application_owners` table's schema (migration
`0001`) already anticipated multiple owners per application from the very
first PR — but until this one, nothing ever actually *granted* a second
owner: `Register` always creates exactly one `primary` row, and there was
no endpoint to add another. This closes that gap.

- **Grant** (`POST /applications/{id}/owners`, body `{"email", "ownership_role"}`)
  — only the current **primary** owner may call this (`requirePrimaryOwner`,
  stricter than every other service's plain "any active owner"
  `requireOwner`). The target must already be a known platform user
  (`UserRepository.GetByEmail`) — granting access to an email that has
  never signed in is rejected (`target_user_unknown`), not silently
  auto-provisioned; only dev-auth's own self-service upsert, driven by
  that person's own authenticated request, creates a user record. A
  deactivated target is rejected too (`target_user_inactive`), though
  since Module B (User Management) has no deactivation endpoint either,
  this check is currently unreachable in practice — implemented anyway
  per FR-017's exception flow, not for a scenario that can occur yet.
  Re-granting the same role to someone whose access was previously
  revoked is idempotent (an `UPSERT` on the `(application_id, user_id,
  ownership_role)` unique constraint), not an error.
- **Revoke** (`DELETE /applications/{id}/owners/{userId}`) — same
  primary-owner-only gate. Its `UPDATE` WHERE clause explicitly excludes
  `ownership_role = 'primary'`, so there is no way to revoke the primary
  owner this way even by passing their own id — that's Module V/FR-016's
  (not yet built) Transfer Ownership flow.
- **Day-to-day access requires no new code at all**: every other
  service's `requireOwner` check already accepts *any* active owner row
  regardless of `OwnershipRole` — a granted co-owner or contributor
  immediately gets real deploy/build/validate/config access the moment
  the grant lands, exactly matching FR-017's business rule ("co-owners
  may perform day-to-day configuration and deployment actions"). Verified
  live: granted a second user co-owner access, had them build and deploy
  the application themselves, confirmed the primary owner could still act
  on it too, then revoked access and confirmed the same user was
  immediately rejected (`forbidden`) on the next call.

**A real, pre-existing bug found and fixed while verifying this, in code
that shipped two PRs ago (Module W, Audit Log):** `AuditService.Query`
used to check `resource_type == "application"` directly to decide whether
a non-actor requester owned the application an entry concerned — but
`deploy_service.go` records deploy/rollback/approval-decision entries
under `resource_type="deployment"`, and `build_service.go` records builds
under `resource_type="build"`, *never* `"application"`. Every such entry
was therefore invisible to every owner except whoever performed the
action — including the application's own primary owner. This was
unreachable before this PR: with only ever one owner per application,
"the actor" and "an owner" were always the same person, so the gap never
manifested. Verified live: granted a co-owner, had them deploy, confirmed
the primary owner's `GET /audit-log?resource_type=deployment&resource_id=...`
query returned nothing for it — then fixed `Query` to resolve a
deployment/build id back to its owning application
(`deploymentApplicationLookup`/`buildApplicationLookup`, thin adapters
over `DeploymentRepository`/`BuildRepository`'s existing `GetByID`) before
the ownership check, rebuilt, and confirmed the same query now correctly
returns the entry — while a genuinely uninvolved third party still sees
nothing.

**Known gaps, documented not hidden:**
- FR-017's "contributor" access level is not actually distinguished from
  "co-owner" anywhere in authorization logic — both `secondary` and
  `technical` roles get identical access via the shared `requireOwner`
  check. FR-017 doesn't specify what a contributor should be blocked from
  that a co-owner isn't; implementing a real distinction would mean
  inventing that boundary rather than reading it from the spec.

## How Ownership Transfer works (Module E, FR-016)

FR-016's main flow, exactly: the current primary owner nominates a new
one (`POST /applications/{id}/ownership-transfer`, body `{"email"}`) —
same `requirePrimaryOwner` gate as granting a co-owner, and the same
"target must already be a known platform user" / "target must be active"
checks. The nominee is notified (`domain.NotificationOwnershipTransfer`,
via a new `NotificationService.NotifyUser` — the first notification in
this platform addressed to one specific person rather than "every active
owner of an application", since the nominee may not be an owner at all
yet). Nothing about the application's ownership actually changes until
the nominee explicitly accepts (`POST /ownership-transfers/{id}/accept`)
— matching FR-016's business rule that this is "a pure accountability
change" with its own explicit step, not an immediate side effect of
nomination.

**Accepting**: only the nominated user may accept (`not_transfer_nominee`
otherwise), and only within the **policy window**
(`OWNERSHIP_TRANSFER_WINDOW_SECONDS`, default 7 days — exact value TBD
per FR-016's own business rule, same "reasonable default pending a real
Decision Log entry" status as the scale-to-zero timeouts). There is no
background sweeper marking transfers expired on a timer (unlike the
scale-to-zero idle sweeper) — expiry is checked **lazily**, the moment
someone actually tries to accept a transfer whose window has passed; an
expired-but-never-touched transfer just sits `pending` in the database
until then. On acceptance: the prior primary owner's row is **revoked,
not deleted** (`ReplacePrimaryOwner`, retained for audit history per
FR-016's own business rule) and the nominee becomes the new active
primary — cleanly, even if they were previously a co-owner/contributor on
the same application (that now-redundant role is revoked too, so they
don't end up listed twice under two simultaneous roles).

Only one transfer may be pending per application at a time
(`one_pending_transfer_per_application`, migration `0010`) — a second
nomination while one is outstanding is rejected
(`transfer_already_pending`), not silently superseding it.

**Scope adaptation**: only the owner-initiated, nominee-accepted main flow
is implemented. FR-016's alternative flow ("Administrator performs a
forced transfer without new-owner acceptance during offboarding") needs
the Platform Administrator role this platform doesn't have — by
`DEC-002`'s decision (Decided 2026-09-11), the same basis every other
admin-only flow in this codebase already documents.

**Verified for real** against a running Postgres instance, including the
one thing that's easy to get wrong testing an expiry mechanism: actually
*waiting it out*, not just asserting a past timestamp in a unit test.
`docker compose`'s `platform-api` service didn't originally forward
`OWNERSHIP_TRANSFER_WINDOW_SECONDS` from `.env` into the container at all
— a real gap in `docker-compose.yml` found by setting it to `2`, watching
the created transfer's `expires_at` come back exactly 7 days out anyway,
and realizing the container had silently kept the code-level default.
Fixed by adding it alongside `SCALE_TO_ZERO_IDLE_SECONDS`'s existing
`environment:` entry (the same variable this codebase already had to get
right once before). Re-verified with the fix: nominated a real user,
waited 3 real seconds past a real 2-second window, confirmed `accept`
correctly returned `transfer_expired`, confirmed the transfer's status
was durably marked `expired` (not just rejected in that one response),
and confirmed the original owner was completely untouched.

### The owner-less window, found, demonstrated and closed

When Transfer Ownership first shipped, the note here said FR-018's
"no active owner exists" guard had gone from *vacuously* satisfied (back
when nothing could remove an application's only owner) to a real, if
narrow, gap: `ReplacePrimaryOwner`'s statements ran sequentially, so
between revoking the prior primary and inserting the new one an
application briefly had **zero** active primary owners. That note has now
been made good on rather than left standing.

It was worse than "narrow" suggested. Replaying those statements
unguarded against a real database — with the second one failing, exactly
as it would if the nominated user were removed between nomination and
acceptance — left the application with zero active primary owners
**permanently**: nothing retries, `Revoke` refuses to touch primary rows
by design, and every path that could repair it (grant, transfer) requires
an active owner to authorize it. The application became untouchable by
anyone, including the employee who created it — `not_primary_owner` on
every ownership action, `forbidden` on everything else. FR-018's
"orphaned application operating unaccountably", reached for real.

`ReplacePrimaryOwner` now runs its three statements inside a real
database transaction — **the only place in this codebase that opens
one**, and worth the exception precisely because the invariant it
protects ("an application always has exactly one active primary owner")
is what every owner-gated action depends on. Verified the same way the
gap was found: the identical failing sequence, wrapped, leaves the prior
owner `active` and the application fully usable, and a real
nominate-and-accept transfer still commits correctly end to end.

**A response-shape fix, made in the follow-up Admin Portal PR, not this
one:** `GET /applications/{id}/ownership-transfer` originally 404'd when
nothing was pending, mirroring the sentinel `domain.ErrTransferNotFound`
error underneath it a little too literally. Real browser testing of the
Admin Portal UI caught this as console noise on *every* application
detail page view (nothing pending is the ordinary state for most
applications most of the time, not an exceptional one) — fixed to always
return `200` with `{"transfer": ...}` or `{"transfer": null}`. See the
handler's own doc comment for the full reasoning, including why this is
different from e.g. `GetDeployment`, which correctly still 404s.

## How Reporting works (Module AB, FR-127/FR-128)

Two read-only reports derived entirely from data this platform already
holds — no new tables, no new tracking, nothing invented:

- **`GET /reports/application-inventory`** (FR-127) — current state of
  every application the caller owns: department (resolved to its name),
  every *active* owner, lifecycle state, stack, and the environment of
  its most recent deployment. "Stack" is read from the application's
  current `deployment.yaml` draft; a draft that no longer parses yields
  an empty runtime list rather than failing the whole report over one bad
  application. Per FR-127's own business rule this is deliberately
  current-state only — history is Module W's job, not this report's.
- **`GET /reports/deployment-activity`** (FR-128) — deployment outcomes
  over a period, broken down by environment and by department.

**Both are owner-scoped**, which is a scope adaptation worth being
explicit about: FR-127's main flow describes a platform-wide report for a
"Management/Auditor, Platform Administrator" holding a reporting-access
role, and no such role exists anywhere in this platform (`DEC-002`). By
FR-127's own exception flow — "requester's scope exceeds their
authorization → scope is limited to what they are authorized to see, not
rejected outright" — every caller today falls into its *alternative*
flow: "Application Owner views a scoped inventory limited to applications
they own." Treating everyone as an unprivileged owner is the safer
reading than treating everyone as a de-facto Auditor, and it matches how
Module W and Module X already scope their own reads. Note this is
deliberately *stricter* than `GET /applications`, which has been
unscoped since the first PR — a pre-existing inconsistency this doesn't
copy forward.

**Why the succeeded/failed/rolled-back split reads the audit log.** It
can't come from `deployments.status` alone: a rollback produces an
ordinary deployment row that ends up `running` or `failed` like any
other — there is no `rolled_back` deployment status (see
`domain.DeploymentStatus`). What distinguishes them is *which action
created it*, and only the audit log records that
(`deployment.deploy` vs `deployment.rollback`, both written against the
new deployment's own id by `auditDeployOutcome`). That's exactly what
FR-128's acceptance criterion asks for — "a generated report's totals
reconcile with the underlying audit log for the same period and scope" —
so the audit log is the source of truth for the classification rather
than a second, drifting copy of it on the deployments table. One audit
query per report, not one per deployment: every rollback in the window is
fetched once and turned into a set to test membership against.

In-flight deployments (`scanning`/`pending_approval`/`deploying`/
`health_check`) are counted in **none** of the three buckets — FR-128
counts *outcomes*, and one that hasn't reached an outcome isn't a success
or a failure to report yet. And per FR-128's exception flow, a requested
range starting before any data exists comes back with `available_from`
naming the earliest data actually held, rather than silently reporting
zeros for a period there was nothing to report on.

**Verified for real** against a running stack, including the part that
matters most — FR-128's reconciliation criterion, checked rather than
assumed: registered and validated a real application (confirmed the
inventory read `runtimes: ["go"]` from the real `deployment.yaml`, the
real department name, and an empty environment for a never-deployed
app), then ran two real `docker build` + deploy cycles and a real
rollback, and confirmed the report returned `succeeded: 2, failed: 0,
rolled_back: 1` — reconciling exactly with the raw audit log queried
independently (2 × `deployment.deploy`, 1 × `deployment.rollback`).
Confirmed an unrelated user's report comes back empty rather than
exposing someone else's applications, confirmed `available_from` appears
for an over-wide range, and confirmed malformed and backwards
`from`/`to` values are rejected with `400`s.

**Known gaps, documented not hidden:**
- **FR-129 (Resource Utilization Report) is not implemented.** It reports
  consumption *against quota*, "building on the real-time usage
  visibility of Module M" — and Module M (Resource Management) doesn't
  exist. There is no quota to report against and nothing tracking
  allocation, so any number this produced would be invented rather than
  measured.
- FR-127's platform-wide administrator view (its main flow) needs the
  reporting-access role described above; only the owner-scoped
  alternative flow is built.
- FR-127's "scheduled periodic generation where the platform supports it"
  isn't built — both reports are ad-hoc query-time only. There is no
  scheduler here beyond the scale-to-zero sweeper, and adding one for
  reports would be inventing a delivery mechanism (to where? in what
  format?) the spec doesn't describe.
- Neither report paginates. Both are bounded by how many applications one
  person owns, which is small by construction today; a real gap if a
  single owner ever accumulates hundreds.

## How Database Management works (Module N, FR-061/062/063/065)

Module N has **no endpoints of its own**, and that is the design, not an
omission: FR-061's business rule is that provisioned databases are "never
directly reachable by the employee/agent for raw admin commands". An
employee declares `database: type: mysql` or `database: type: postgres`
in `deployment.yaml` and gets a database; there is no API to poke at it
with.

What actually happens, on deploy:

1. **`EnsureProvisioned` (FR-061).** A private Docker network is created
   for the application, then a dedicated `postgres:16-alpine` or
   `mysql:8.4` container is started on it with a platform-generated
   password (`crypto/rand`, base64url so it can't corrupt a DSN).
   Idempotent: an application that already has a live database keeps it,
   so a redeploy never silently replaces the database and loses its data.
   A declared type the runtime doesn't know how to start is rejected
   before anything touches it. MySQL's root account gets a random
   password nobody records and may only sign in from inside its own
   container; the application has its own account, and rotation (FR-068)
   has that account change its own password, signing in with the current
   one — so the platform never needs root.
2. **`WiringFor` (FR-062/063).** Every path that starts an application
   container asks this one helper for `{Env, NetworkID}` and passes it
   straight through. Both are zero-valued for an application with no
   database, which is why no call site branches on whether one exists.
3. **`Deprovision` (FR-065).** Delete stops the container, removes the
   network and closes the record — ordered *before* the lifecycle status
   moves, per FR-065's "deletion is not marked fully complete until
   confirmed". Best-effort on the runtime teardown itself: a container or
   network a previous partial attempt already removed is a success, so a
   repeated deletion can always finish.

### Four start paths, one helper — and why that matters

Deploy, resume, restart and scale-to-zero cold start each create a brand
new container. A version of this module that wired up only the deploy path
would look completely fine in a demo and then silently drop an
application's database the first time it was restarted — the container
starts, the health check passes, and the app just can't reach its data.
That failure mode is why `WiringFor` is a single function rather than four
copies of a lookup, and why `internal/service/database_wiring_test.go`
asserts the wiring reaches all four paths, cold start included (that one
carries only a deployment id, so it has to resolve its way back to the
application first).

### FR-062 isolation is enforced at the network layer, and it was wrong at first

The database container publishes **no host port** and sits on **exactly
one** network — its application's. Its host is its own container name,
which Docker resolves inside that network and nowhere else.

The first end-to-end run of `scripts/verify_module_n.py` found that this
was not actually true. `StartDatabase` created the container and then
attached the private network with `NetworkConnect`, the same two-step
`StartContainer` uses — which leaves the container on Docker's **default
bridge as well**, where every other application's container also sits. The
isolation the code's own comment claimed was not real: any container on
the default bridge could reach the database directly.

The fix attaches the network at *create* time (`NetworkMode` +
`EndpointsConfig`), and `StartDatabase` now inspects the container it just
started and refuses to return success if it is attached to more than one
network — an unisolated database is a failure to provision, not something
to hand over quietly.

Both halves are then proved from outside the code, by connecting:

- a `psql` client **on** the private network authenticates with the
  generated credentials and reads and writes real data;
- the identical connection **off** that network fails — by container name
  (Docker's DNS is per-network) *and* by raw IP, which is the check that
  actually matters, since a name lookup failing on its own proves much
  less;
- the deployed application itself opens a TCP connection to its database
  and completes a Postgres `SSLRequest` handshake, so what answered really
  is Postgres, reached with the environment the platform injected.

### Known gaps

- **The plaintext database password — closed by Module O.** This list
  originally led with the fact that the generated password was stored in
  plaintext in `provisioned_databases`, readable by anyone with read
  access to the platform database. It now lives in the secret store as a
  sealed, platform-managed `DATABASE_PASSWORD`, and rows from before that
  are moved there at startup — see **How Secret Management works** below,
  including the part that is still not solved: the key itself.
- **`FR-064` (Backup Scheduling)** is implemented with `DEC-025`'s policy
  — see **How Database Backups work** below.
- **`FR-065`'s "purge or retain-then-purge, per policy" is purge, full
  stop.** Deleting an application deletes its database *and its backups*;
  no policy asks for a final backup to be kept.
- **`FR-062`'s detection half** ("a cross-application connection attempt is
  detected and logged as a policy violation") is not implemented — that
  needs Module Q's network policy layer. Only the *prevention* half is
  real, which is the half that stops the attempt succeeding.
- **Postgres and MySQL only.** `supported_stacks` also lists `redis`
  under `cache`, and nothing provisions one.
- **The database container's data lives in the container**, not a named
  volume — it survives restarts and cold starts (verified), but not a
  `docker rm` of the database container itself.

### Verifying it

MySQL is covered by `scripts/verify_lan_stack.py` (see **The LAN
development standard** below): a real MySQL per application, isolated from
the default bridge, reached by the application through the injected
variables, and rotated with the old password refused afterwards.

`scripts/verify_module_n.py` runs the whole thing against a live stack and
a real Docker daemon — two real applications built and deployed from
source, no mocks anywhere:

```bash
docker compose up -d --build      # from the repo root, with .env present
python services/platform-api/scripts/verify_module_n.py
```

Phase 1 uses a `scaling.min: 1` application (not scale-to-zero-eligible,
so resume and restart really do start containers for it) to check
provisioning, isolation from both sides, credential delivery, resume,
restart, data surviving all of it, and deprovision-on-delete. Phase 2 uses
an ordinary elastic application to check the cold-start path, waiting for
a real scale-to-zero first. Both phases delete their application at the
end and confirm the container, the network *and* the platform's own record
are gone. It needs a low `SCALE_TO_ZERO_IDLE_SECONDS` (e.g. `20`) for the
cold-start phase, and says so rather than hanging if it isn't.

## The LAN development standard (React + MySQL + Python/JavaScript/TypeScript/Go)

The company's standard application shape is a React frontend, one API in
Python, JavaScript (`nodejs`), TypeScript or Go, and a MySQL database.
Migration 0016 adds the two catalog entries the v1 baseline lacked —
backend `typescript` (base image `node:22-alpine`) and database `mysql` —
and `company-deployment-skill/templates/` holds a starter for each part.
Supporting it needed four platform changes, three of them found by
deploying the templates rather than by reading code:

- **MySQL provisioning** (Module N, above) — same isolation, secret
  handling, deprovisioning and rotation as Postgres.
- **`PORT` is injected** into every application container, set to the
  port the platform routes to, so a service listens where it's told
  instead of repeating a number from `deployment.yaml`.
- **`/run/{app}/{service}` redirects to `/run/{app}/{service}/`** for `GET`
  and `HEAD`. A browser resolves a page's relative links against the URL
  up to its last slash, so without this a frontend built with relative
  asset paths (the only kind that works under a prefix it can't know in
  advance) loads its bundle from `/run/{app}/` and renders blank.
- **Runtime images carry no package manager**, and the Node build images
  moved to Node 22 — see **How Build works**. The first real deploy was
  blocked by the image scan on exactly that.
- **Database images are pulled when missing.** `ContainerCreate` doesn't
  pull, so provisioning had only ever worked for Postgres, whose image the
  platform's own database already put on the host. The first MySQL
  deploy failed with `No such image: mysql:8.4`.

### Verifying it

```bash
docker compose up -d --build      # from the repo root, with .env present
python services/platform-api/scripts/verify_lan_stack.py
```

For each of `python`, `nodejs`, `typescript` and `go` it builds and
deploys the templates — React + that API + MySQL — then checks, through
the platform's own `/run/...` address:
- the API reports its runtime and that it reached MySQL;
- an item `POST`ed comes back from `GET /items`;
- the bare frontend address redirects and the page's relative bundle loads;
- the database container is `mysql:8.4`, publishes no host port, and is
  on its application network only.

For one application it then checks four more things:
- a MySQL client on that network reads the table, and the identical client
  on the default bridge can't reach it;
- after `DATABASE_PASSWORD` is rotated, the old password is refused, the
  new one works, the restarted API reconnects and the data is still there;
- with `PLAYWRIGHT_MODULE` pointing at a Node Playwright install, a real
  headless browser loads the page, sees the API connected, adds an item
  through the form and sees it listed;
- deleting each application removes its database container.

## How Database Backups work (FR-064, DEC-025)

The policy the platform owner decided on 2026-10-23:

- **When:** a **full** backup every Sunday, and a **differential** Monday
  to Saturday (what changed since that Sunday's full), each from 01:00
  Asia/Bangkok. Set by `BACKUP_FULL_WEEKDAY`, `BACKUP_HOUR` and
  `BACKUP_TIMEZONE`.
- **Retention:** once a new full backup **succeeds**, every older backup of
  that database is deleted, file and all. A failed Sunday therefore never
  leaves a database without a backup.
- **Opting out:** an application can declare `database: backup: none`.
  Validation rejects any other value than `weekly` or `none`, and
  rejects `backup` without a `database.type`.

How it works (`internal/service/backup_service.go`):

- **Dump.** A logical dump is taken inside the database's own container, as
  the application's own account: `mysqldump --single-transaction
  --skip-extended-insert --order-by-primary --skip-dump-date …` or
  `pg_dump --clean --if-exists …`. Both put one row per line in a stable
  order.
- **Differential.** A differential is a line-level delta of the day's dump
  against Sunday's (`internal/backupdelta`). An unchanged table costs one
  instruction, a changed row one line. Restoring it is Sunday's dump plus
  that one delta, never a chain.
- **Sealing.** Every backup is gzip-compressed and **sealed with
  `PLATFORM_SECRET_KEY`** (AES-256-GCM, bound to its own record) before
  it's written to the `platform-backups` volume. A SHA-256 of the complete
  dump is checked before anything is restored.
- **Scheduler.** It runs at startup and every 10 minutes
  (`BACKUP_SWEEP_INTERVAL_SECONDS`) and backs up each live database once
  a day. The first backup of a database is always a full. A failure is
  recorded with its reason, owners get one notification per day
  (`database_backup`), and it's retried after an hour.
- **Owners** (`GET/POST /applications/{id}/backups`, Admin Portal
  **Backups**) see the policy and every backup, and can take one now. An
  on-request full replaces the week's set like Sunday's does.
- **Restore** (`POST /applications/{id}/backups/{backupId}/restore`,
  `{"confirm": "<application name>"}`, owner-only, audited) replaces the
  database's contents. It **first takes a differential of what is there**,
  so a restore can itself be undone. The MCP server deliberately has no
  restore tool: replacing data is a person's decision.

### Known gaps

- **Same host.** Backups live in a Docker volume on the platform host. They
  protect against mistakes and bad deploys, not against losing that
  host's disk. Copy the `platform-backups` volume off the host; the files
  are sealed, so the copy holds nothing readable without the key.
- **The key is the same one as the secret store's** (`DEC-006`). Losing
  `PLATFORM_SECRET_KEY` makes every backup unreadable, so back up the key
  separately from the backups.
- **Logical dumps.** Every backup, differential included, takes a full
  dump of the database to compute what changed. Fine for the small
  databases this platform hosts; a large one would want engine-level
  incremental backups (binlog/WAL).
- **No RPO/RTO figure was set.** What this gives is up to a day of data
  at risk.

### Verifying it

```bash
python services/platform-api/scripts/verify_backups.py
```

It runs against real MySQL **and** Postgres databases, with 200 real rows
written from a client on each application's private network. Results:

- With platform-api restarted after the deploy, the **scheduler** took a
  full by itself.
- The file is in the volume, and the rows' contents appear nowhere in it.
- After a change, the next backup was a differential against that full
  (143 bytes against the full's 1,967 for MySQL).
- A restore without the exact name was refused. After `DROP TABLE`,
  restoring the differential brought back exactly the rows at that point,
  and restoring the full the earlier ones. Each restore made a
  pre-restore backup first.
- A new full deleted the previous set, records and files.
- `backup: monthly` failed validation, and a `backup: none` application
  was skipped by the scheduler.
- Deleting each application deleted its backups.

In a real browser, the Portal's **Backups** section took a backup on
request. After a row was changed, restoring with the name confirmation put
it back.

## How Secret Management works (Module O, FR-066/067/069/070)

An application owner registers a secret by name; the platform encrypts
it, stores only the ciphertext, and injects it into every container the
application starts, as an environment variable of the same name. Nothing
ever returns the value again — not the API, not the listing, not the
audit log.

```
PUT    /applications/{id}/secrets/API_KEY   {"value": "..."}   → metadata, never the value
GET    /applications/{id}/secrets                               → names, versions, who set them
DELETE /applications/{id}/secrets/API_KEY
```

### What protects a value, layer by layer

- **At rest (FR-066):** AES-256-GCM (`internal/secretbox`), keyed by
  `PLATFORM_SECRET_KEY` — which lives in platform-api's environment, never
  in the database it protects. `application_secrets` has no plaintext
  column at all.
- **Bound to its application (FR-069):** each ciphertext is sealed with
  its application id and name as associated data. Copy a ciphertext into
  another application's row — the attack that needs only database write
  access, no API — and it does not decrypt there: that application fails
  to start instead of receiving the value. The owner check on the API is
  the first layer; this is the one that still holds when the API isn't
  the way in.
- **Write-only (FR-070):** no endpoint returns a value. That is enforced
  by the endpoint not existing, not by a permission check that could be
  misconfigured later. Set and delete are audited with the secret's name
  and version, never its value.
- **Delivered at start, and only then (FR-067):** values are decrypted in
  `ApplicationResources.WiringFor` — the same single call deploy, resume,
  restart and cold start already make for Module N, so Module O added no
  new call site to get wrong — and handed straight to the container.
  They are never written into a built image.
- **Fails closed:** a secret that can't be decrypted — a changed platform
  key, a tampered or misplaced row — fails the start with
  `secret_unavailable`, naming the secret and the cause (never a value).
  Resume and Restart resolve the wiring *before* stopping anything, so
  the failure leaves the running container untouched instead of taking
  the application down.

### Module N's database password moved here

The password Module N generates is now a platform-managed secret,
`DATABASE_PASSWORD`: sealed into the store *before* the database starts,
read back only at container start to build `DATABASE_URL`, and visible to
owners by name only. It can't be changed or deleted through the API
(`409`), and `DATABASE_*`/`PLATFORM_*` names are reserved, so an owner's
secret can't shadow anything the platform injects.

Databases provisioned before Module O had their password in plaintext in
`provisioned_databases.password`. Migration `0012` clears it for
deprovisioned rows (no key needed); live ones are sealed into the store
at startup by `MigrateLegacyPlaintextPasswords`, which needs the key and
so can't be SQL. Verified against a real application deployed with a
database on the pre-Module-O build: startup logged `moved 2 legacy
plaintext database password(s)`, a full `pg_dump` of the platform
database no longer contains the old password anywhere, and after a
restart the application received exactly its database's real password —
decrypted from the store — and authenticated with it.

### Rotating the database password (FR-068)

`POST /applications/{id}/secrets/DATABASE_PASSWORD/rotate` does what
FR-068 asks, in an order chosen so the database and the secret store never
disagree about which password is current:

1. A new password is set on the database — `ALTER ROLE`, run over the
   database container's own Unix socket, so no current password is needed.
2. It's stored, sealed. If storing fails, step 1 is undone.
3. Running instances are restarted onto it; a service scaled to zero gets
   it at its next cold start. If the restart fails, the response is
   `rotation_incomplete`: the new password is in force, and a Restart
   finishes the job.

The response and the `secret.rotate` audit entry carry the version, never
a value. An owner-set secret is refused with `409 secret_not_rotatable` —
the platform didn't issue it and can't invalidate it. The Admin Portal
offers this as a **Rotate** button on the database password's row in an
application's Secrets section.

**How it was checked, and a trap in checking it.** The first probe of
`ALTER ROLE` against a throwaway Postgres said the *old* password still
worked afterwards. It did — but only because the probe connected over
127.0.0.1 inside the database's own network namespace, which the official
image trusts outright: over loopback, any password "works". Every
connection an application makes arrives over its private network, where
passwords are enforced, so that is where rotation has to be verified.
`scripts/verify_module_n.py` now does exactly that on a real deployed
application: after rotating, a client on the application's network is
refused with the old password (`password authentication failed`), gets in
with the new one and finds the data untouched, and the restarted
application holds the new password and still reaches its database.

The database container's own `POSTGRES_PASSWORD` variable keeps the
original value forever — Postgres reads it only at first initialisation —
so it is not a record of the current password. The secret store is.

### Also fixed here: databases were reported ready before they were

Found while setting up that legacy application, and a Module N defect
rather than a Module O one. `StartDatabase` returned as soon as the
database container started, but Postgres's official image runs `initdb`
and restarts itself once before it accepts connections. The platform
reported the deployment `running` while the application's first
connection attempt was still being refused — so an application that
connects at startup, and exits if it can't, would fail its first deploy
and succeed on the retry. `EnsureProvisioned` now waits for `pg_isready`
before recording the database as provisioned, and stops the container if
it never gets there. Over TCP (`-h 127.0.0.1`) deliberately: during
`initdb` the temporary server listens on its Unix socket only, and a
socket check would call it ready a moment too early.

### Known gaps

- **The key is sealed with the host's TPM** (`DEC-006`, decided
  2026-10-23). On the platform host it is unsealed into RAM at boot and
  read from `PLATFORM_SECRET_KEY_FILE`, so it is neither in `.env` nor in
  the container's environment. Setup, recovery and verification are in
  `deploy/host/README.md`. Without that setup (a development machine),
  `PLATFORM_SECRET_KEY` still works, and setting both is refused. Still
  missing: **rotation**. There is no re-encryption tool, so changing the
  key makes every stored secret (and every backup) unreadable. That
  fails closed, as above, but is an outage.
- **Anyone with access to the Docker daemon can read injected values**
  from a running container's environment. That is inherent to FR-067's
  environment-variable delivery, and Docker daemon access is
  root-equivalent on the host anyway.
- **FR-068 (rotation) covers what the platform issued, and only on
  demand.** The database password rotates for real (see **Rotating the
  database password** above). An owner-set secret can only be replaced —
  effective at the next start, and nothing here can invalidate a
  credential a third party issued. There is no scheduled rotation (the
  interval is TBD), and no overlap window: Postgres holds one password per
  role, so between the change and the restart an instance still holding
  the old password can't open new connections (ones already open keep
  working). A cold start that reads the store in the instant between the
  database change and the store update would start with the old password
  — a narrow window, stated rather than hidden.
- **FR-071 (approval for production secret operations) is not
  implemented** — it needs the approval workflow and RBAC that don't
  exist.
- **Injection is not audited.** `audit_log.actor_user_id` is required and
  a scale-to-zero cold start has no human actor, so FR-070's "injection"
  is not recorded; nor is a platform-generated database password's
  creation (the deploy that caused it is). Set and delete are.
- **No reference-by-name in `deployment.yaml`.** FR-023's contract is six
  top-level keys and none of them is `secrets`, so every secret an
  application has is injected into every one of its services, rather than
  each service declaring the ones it uses. Adding a key is a contract
  change, not an implementation detail.
- **SEC-SECRET-8 (force-rotation on detected leakage) and FR-066's
  exception flow (scanning source for committed credentials) are not
  implemented.**
- **Deliberately no MCP tool that accepts a value** (see
  `services/mcp-server/README.md`): an owner sets a secret in the Admin
  Portal's Secrets section (see `apps/admin-portal/README.md`) or through
  the Platform API directly, never through the agent.

### Verifying it

`scripts/verify_module_o.py` runs against a live stack, no mocks, and
checks what an attacker or an accident would actually see rather than
what the code claims:

```bash
docker compose up -d --build   # from the repo root, with PLATFORM_SECRET_KEY in .env
python services/platform-api/scripts/verify_module_o.py [--legacy-app NAME]
```

It sets a secret, deploys, and confirms the running application received
exactly that value (the test application reports only a hash of it),
while a full `pg_dump` of the platform database, every layer of the built
image (`docker save`), the API's responses, the audit CSV export and
platform-api's own logs contain it nowhere. It confirms another employee
gets `403` on list, set and delete; plants a ciphertext directly into
another application's row and confirms that application's restart fails
— before stopping its running container, with the reason in the audit
trail — rather than receiving the value; and restarts platform-api with a
different key to confirm applications fail closed, then recover when the
key is restored. `--legacy-app` adds the upgrade-path checks above.

## How Logging works (Module S, FR-086/087/089)

Every container the platform starts for an application is followed from
its first line. Each line it writes to stdout or stderr is stored in the
platform's own Postgres (`application_logs`), tagged with its
application, deployment, service, stream and container, and an owner
reads them back through one endpoint:

```
GET /applications/{id}/logs?service=&environment=&contains=&since=&until=&limit=&cursor=
→ {"entries": [{"timestamp", "service", "stream", "message", "deployment_id", "instance"}], "next_cursor": ...}
```

Newest first. `contains` is a case-insensitive literal match (`%` and `_`
are characters, not wildcards); `since` and `until` are RFC 3339,
inclusive and exclusive; `limit` defaults to 200, capped at 1000. A full
page carries a `next_cursor` to pass back as `cursor` for the next, older
page. The cursor is the last line's timestamp *and* id: lines written in
the same microsecond share a timestamp, and a timestamp-only cursor would
skip some of them at a page boundary.

### Collected as it's written, so removed containers keep their history

`internal/runtimeengine/logs.go` starts following a container's output
right after starting it, before the health check, so a container that
crashes on start still leaves its output behind. Lines are written in
small batches every half second. `Stop` waits (up to 10s) for a
container's last lines to be stored before removing it, because removing
a container discards Docker's copy of its output. That is what keeps the
history of every container the platform replaces: a failed deploy's, a
restart's, a scale-to-zero's.

Each container also carries its tags as Docker labels, so when
platform-api starts it resumes collection for every application container
still there, from the last line stored for each: lines written while
platform-api was down are collected, and none is stored twice. If the
store is unreachable, lines are held in memory and retried (FR-086's
exception flow), up to 20,000 per container.

All four start paths (deploy, resume, restart, cold start) tag the
container, and `database_wiring_test.go` checks every one: the same four
paths Module N's wiring has to reach, and the same way to miss one.

### Secrets are redacted before they're stored

Every value the platform injected into a container (the owner's secrets,
`DATABASE_URL`, `DATABASE_PASSWORD`) is replaced with `[REDACTED:NAME]`
in each line before it's stored, longest value first, so a connection
string goes as a whole rather than around the password inside it.
Connection details that aren't secret (`DATABASE_HOST`/`PORT`/`NAME`/
`USER`) and values shorter than six characters are left alone, since
replacing every `5432` would only garble the logs. Only injected values
are redacted. Anything else an application prints, personal data
included, is stored as written: the platform has no way to know what
else is sensitive.

### Access (FR-087/FR-089)

Owners only: anyone with an active ownership row (primary owner,
co-owner or contributor), the same check as every other per-application
endpoint. Anyone else gets exactly the `404` a nonexistent application
gets, same status and same body, so a refusal doesn't confirm the
application exists. Every successful read is recorded as
`application.read_logs`, with the filters used but never the `contains`
text, which could itself be sensitive. A refused read is deliberately
*not* recorded: the audit log shows people their own actions, so an
entry for the refusal would tell them what the 404 was careful not to.

### Also fixed here: a container that crashed on start was never removed

Verifying the crash case above found it. `StartContainer` returned an
error without removing the container whenever anything failed after
creating it, and a container that exits before its port is published
always fails there. It stayed on the host, exited, for good: every
failed deploy of a crashing application left one more. `StartContainer`
now removes the container on any failure after creating it, the way
`Stop` does (its last lines stored first), and reports that the
container "exited as it started" instead of that it "published no host
port". The deploy pipeline also now stops the services it had already
started when a later one fails to start, as it already did when a health
check failed. The verification's first version missed this, because it
looked for leftover containers among running ones only; it now checks
every state.

### Known gaps

- **Retention (FR-088) is 90 days** (`DEC-027`). platform-api deletes
  lines older than `LOG_RETENTION_DAYS` (default 90) at startup and then
  hourly, in batches, using an index on `logged_at` (migration 0019).
  When collection resumes after a restart, it reaches back no further
  than the retention period. A container quiet for longer than that
  doesn't get its already-purged output collected again. A deleted
  application's lines age out the same way. Verified by planting a line
  100 days old and one 10 days old in the real database: a restart
  purged only the first.
- **Log levels aren't parsed.** Each line carries its stream (stdout or
  stderr) instead, so FR-087's severity filter has nothing to filter on;
  the MCP tool says so when asked for one.
- **Docker keeps its own unredacted copy** of a container's output (the
  daemon's log file on the host, readable with `docker logs`) until the
  platform removes the container. As with injected environment variables
  (see Module O's gaps), anyone with Docker daemon access is
  root-equivalent on the host anyway.
- **Redaction matches the exact injected value.** A secret shorter than
  six characters, or one an application transforms before printing it
  (encoded, split, truncated), is stored as printed.
- **No oversight access.** FR-089's Platform/Security Administrator and
  Auditor access needs the RBAC that doesn't exist, so there is no
  cross-application log query at all.
- **Collection runs inside platform-api**, not as a separate agent. Lines
  written while it's down are collected when it comes back, but only from
  containers still there. A line longer than 64 KiB is stored in pieces,
  and beyond 20,000 lines held for one container while the store is
  unreachable, the oldest are dropped (and that is logged).
- **Nothing streams.** Reading logs is a query, not a follow: the Admin
  Portal's Logs section and the MCP server's `get_application_logs` both
  fetch a page at a time, and neither tails a container's output as it is
  written.

### Verifying it

`scripts/verify_module_s.py` runs against a live stack, no mocks:

```bash
docker compose up -d --build   # from the repo root
python services/platform-api/scripts/verify_module_s.py
```

It deploys an application with a database and an owner-set `API_KEY`
that prints all three secrets on purpose, a line a second, and a last
line when it's stopped. It confirms each secret is stored as
`[REDACTED:NAME]` and appears nowhere in a full `pg_dump` of the platform
database, in platform-api's own logs, or in any log response the run
reads. It exercises every filter (including that `%` and `_` are
literal), pages through with `next_cursor` three lines at a time and
compares the result with a single query, and confirms a non-owner gets a
404 byte-identical to a random id's, with no audit entry, while every
owner read is audited. It restarts the application and finds the replaced
container's shutdown line, printed as it was being stopped, stored before
the container was removed. It stops platform-api for six seconds and
confirms, once it's back, that the running container's once-a-second
output has no gap and no repeat, the lines written during the outage
included. And it deploys an application that crashes on start, confirming
the deploy fails, the container is removed, and the reason it printed on
stderr can still be read.

## How Monitoring works (Module T, FR-090/091)

Every application gets CPU, memory, request rate, error rate and latency
without instrumenting anything, because both sources are things the
platform already sees:

- **Resource use** is read from the container runtime itself
  (`internal/runtimeengine/stats.go`), on an interval, for every container
  the platform started — found by the same labels Module S puts on them,
  so the platform's own database containers are never sampled. CPU is the
  figure `docker stats` prints: 100% means one core's worth, not one
  machine's. Memory subtracts reclaimable page cache, or every application
  would look like it leaks.
- **Traffic** is counted at the platform's own proxy
  (`internal/httpapi/handlers_proxy.go`), which every request to a
  deployed application already passes through (Module L's
  `/run/{app}/{service}` address). Counting is in memory on the request's
  own goroutine — one row per service per minute, not one per request —
  and the sampler flushes it.

```
GET /applications/{id}/metrics?service=&environment=&from=&to=
→ {"from","to","collection":{...},"instances":[...],"last_scale_event":{...},
   "resource":[...],"traffic":[...],"summary":{...}}
```

The window defaults to the last hour and is capped at seven days; both are
implementation limits that keep one call from pulling an unbounded result,
not a retention policy. `summary` is what most callers want: requests,
errors, error rate, mean and max latency, and the latest CPU and memory.

### What the numbers mean, exactly

- **An error is the application failing**: a 5xx, or no answer at all
  (the proxy's own 502). A 4xx is the caller asking for something wrong,
  so it counts as a request and not as an error.
- **Latency is the application's own response time.** Waiting for a
  scaled-to-zero container to start is a scale event (FR-056), already
  recorded as one, and is not counted against the application's speed.
- **Latency is a mean and a max per minute.** There are no percentiles:
  computing a P95 needs the distribution, and storing one per minute per
  service is a real cost nobody has asked for yet.
- **Instances are 0 or 1.** FR-054's horizontal scaling above one
  instance doesn't exist, so "instance count" is "is it running".

### A gap stays a gap

FR-090's exception flow says a failed collection must be visible rather
than interpolated. A container whose reading fails is left out of that
sweep — no row, no zero — and every answer carries a `collection` block
saying whether sampling is keeping up, when it last succeeded, and why
there may be nothing to show. An application scaled to zero has no
samples, and says so; a sampler that has fallen behind says that instead.
Without it, "no data" and "no traffic" would look identical
(docs/13_API_Requirements.md §5.5 asks for exactly this distinction).

Traffic counted but not yet stored is kept in memory and added to the next
flush, so a brief database outage costs no counts and double-counts none.

### Access (FR-091)

Owners only, and anyone else gets exactly the `404` a nonexistent
application gets — FR-091's business rule is that metrics access follows
log access (FR-089) exactly.

Reading metrics is **not** audited, where reading logs is. FR-089 asks for
an audit trail over logs, which carry whatever an application printed;
nothing asks it of CPU numbers and request counts, and auditing every
dashboard refresh would bury the entries that matter. FR-093's
cross-application administrator view would be audited — it doesn't exist.

### Known gaps

- **FR-092 (threshold alerting)** is implemented with `DEC-028`'s
  thresholds — see **How Alerts work** below.
- **FR-093 (platform-wide dashboard) is not implemented** — not a gap, a
  decision: it is explicitly an administrator and auditor view, and by
  `DEC-002` (Decided 2026-09-11) those roles don't exist, permanently, for
  this deployment.
- **NFR-032 retention is not implemented.** Its durations are TBD, so
  nothing is purged: one row per container per interval, plus one per
  service per minute of traffic, accumulate. Same standing gap as Module
  S's FR-088.
- **No custom application metrics.** FR-090's alternative flow allows an
  application to expose its own metrics endpoint for collection; only the
  platform's own baseline is collected.
- **Only traffic through the platform's own address is counted.** A
  request reaching a container some other way — a background job, or one
  service calling another over the application's private network — is
  invisible here, because the proxy never sees it. Until recently that
  included the URL the platform itself reported: `containers[].url` was
  the container's published port, which went around the proxy and broke
  whenever the container was replaced. Found while verifying this module,
  and fixed — see **How an application's URL works**.
- **Sampling is a platform-wide interval** (`METRICS_SAMPLE_INTERVAL_SECONDS`,
  default 15s), not per application or tier, and it is an engineering
  default rather than a ratified policy value.
- **No dashboard.** The Admin Portal's Metrics section summarises this
  for one application (numbers plus two sparklines); there is no
  cross-application or per-service view, and nothing auto-refreshes.

### Verifying it

`scripts/verify_module_t.py` runs against a live stack, no mocks:

```bash
docker compose up -d --build   # from the repo root, METRICS_SAMPLE_INTERVAL_SECONDS=5 in .env to watch it move
python services/platform-api/scripts/verify_module_t.py
```

It deploys an application that burns CPU, allocates memory, fails and
stalls on demand, so every number checked is one the platform measured
about real work rather than a value read back from configuration. In the
run that verified this module: a reading appeared within 25 seconds of the
deploy, attributed to the right service and container; CPU went from 0%
idle to 97.7% while the application burned a core, and memory from 1.6 MB
to 103 MB after it allocated 96 MiB; the proxy counted 11 requests and
exactly the 2 the application failed — an error rate of 0.18, with the
application's own 404 counted as a request and not an error — and the
slowest request it really served showed up as a 6,016 ms maximum against a
600 ms mean.

It then checks the query itself: filters by service and environment, a
window from before the application existed coming back empty, a malformed
time and a backwards window refused, and a non-owner getting a 404
byte-identical to a random id's. Finally it suspends the application and
confirms that the answer then contains no samples at all — not zeros —
and says why, that no audit entry is written for a metrics read, and that
deleting the application leaves no container behind.

## How signed approvals work (DEC-017)

### Approving in the Admin Portal

An approver (an account in `PLATFORM_APPROVER_KEYS`; `/auth/me` carries `is_approver`) gets an **Approvals** page with a count of what's waiting. Each deployment opens to:
- its REVIEW.md, rendered;
- every file of the build. The archive is downloaded through `GET /approvals/{id}/source` and must match the SHA-256 that will be signed, or it can't be approved from the Portal.
- the test report and scans;
- the exact text to be signed.

To sign, the approver picks their `approver_ed25519.pem` and types its passphrase. The browser decrypts the key and signs in the tab's memory: PBES2 with PBKDF2-HMAC-SHA256 and AES-CBC, then Ed25519, in pure JavaScript (`@noble`), because the Portal is served over plain http, where browsers withhold WebCrypto. Only `decision`, `reason`, `signed_at` and `signature` are sent. The key stays on the approver's machine, as with `company-deploy approve`.

Verified:
- vitest tests decrypt a key made by the CLI's own code and produce a signature identical to the Python one (Ed25519 is deterministic). They read a PAX archive with a Thai and a 150-character path.
- On the local stack with a throwaway approver key, a real browser:
  - opened a pending dev deployment and showed its REVIEW.md and its 11 verified files;
  - signed with the uploaded key file. The platform accepted the signature and the application went running, and the request carried only the four fields.

### Review before approval (2026-10-24)

The platform owner reviews every file before anything goes up:
- **The source is kept.** Every build's source archive is kept exactly as uploaded, in `SOURCE_DIR` (the `platform-sources` volume, `<application>/<build>.tar.gz`, readable only by platform-api). It is deleted with its application.
- **A REVIEW.md is required.** A deployment to an environment that waits for approval needs a `review_document`. The MCP server sends the project's `REVIEW.md`, which the skill has Claude write from `templates/REVIEW.md`: workflow, structure with every file's path, data, external calls, configuration names, what changed, tests and risks. Without one the deployment is refused before anything is built. The MCP server checks against `GET /policy`, and the API checks again.
- **Both are signed.** The signed text (now `v2`) adds `source-sha256` (of the kept archive) and `review-sha256`. Whatever the approver reviewed is what goes live: a different archive or document means a different text, so the signature doesn't verify.
- **The approver gets everything.** `GET /approvals/{id}/source` (approvers only) serves the archive. `company-deploy review <id>` downloads it, refuses it unless its SHA-256 matches, and extracts it into a folder alongside `REVIEW.md`, the test report, the scans and the signing template. Extraction refuses absolute paths, `..` and links.

Verified on the local stack, with dev gated and a throwaway approver key, using the real MCP tools and CLI:
- a dev deploy without `REVIEW.md` was refused with no build recorded;
- with one, it returned `PENDING_APPROVAL`;
- `review` laid out the 11 built files (SHA-256 verified) and the same `REVIEW.md`;
- `approve` signed, and the application answered at `/<app>/api/`;
- deleting the application left nothing in its source folder.

The platform owner decided that deployments needing approval (production)
are approved by the platform administrator, who holds the private key. The
platform only ever has the public key:

```
# on the administrator's machine, once
company-deploy approver keygen         # passphrase-encrypted Ed25519 key; prints the line below
# on the platform host's .env, then restart platform-api
PLATFORM_APPROVER_KEYS=admin@sti-th.com=ed25519:<base64>
# whenever a deployment waits
company-deploy approvals               # what's waiting
company-deploy review <deployment>     # the exact source, REVIEW.md, tests, scans, the text to sign — in one folder
company-deploy approve <deployment>    # shows images, scan results and the test report; signs
company-deploy approve <deployment> --reject --reason "run the integration tests"
```

- **What is signed.** The text is built from the platform's own records:
  the deployment, the application, the environment, the build, every image
  reference, the SHA-256 of the requester's test report, the decision, the
  reason, and the signing time (`domain.ApprovalSubject`). The platform
  rebuilds it to verify, so a signature for one deployment, for a rebuilt
  one, or with another test report does not verify. A signature dated more
  than 10 minutes from now is refused.
- **Who may sign.** Only a signed-in account whose email matches a
  configured key, and only with that key. The API key or session that
  sends the decision is not enough by itself, and an owner's
  `POST /deployments/{id}/approve` gets `403 signed_approval_required`.
- **The test report.** The requester (Claude, via
  `deploy_application(test_report=…)`, or `POST …/deploy
  {"test_report": …}`) says which tests ran and how they went. The
  approver reads it before signing, and its hash is part of what's signed.
- **Records.** The approval row keeps the signer's fingerprint, the signing
  time and the signature. The audit entry reads `approved (signed with key
  SHA256:…)`. Approvers are notified in-app with the command to run.

### Known gaps

- **Approving in the Portal** signs in the browser: the key file and
  passphrase are read there and never sent (see **Approving in the Admin
  Portal**). The CLI remains the other way.
- **Losing the key file** means nothing can be approved until a new public
  key is configured. Keep an offline copy.
- **Rollback** doesn't pass the approval gate (unchanged; see **How
  Rollback works**).

### Verifying it

```bash
services/mcp-server/.venv/Scripts/python services/platform-api/scripts/verify_signed_approval.py
```

It runs with a real key made by `company-deploy approver keygen` and real
production deployments. platform-api is restarted with that public key,
and without it at the end. Results:

- **Pausing and access:** a production deploy with a test report paused,
  and the approver was notified with the command. The owner's old-style
  approve got 403, the owner couldn't see the queue, and
  `company-deploy approvals` listed it for the approver.
- **Refusals:** a decision signed with another key was refused, and a
  wrong passphrase signed nothing (the deployment stayed waiting).
- **Approval:** `company-deploy approve` showed the test report, signed,
  and the build went live. The audit log named the key, and the record
  kept the signer, time, signature and report. Deciding again was refused
  (409).
- **Rejection and dev:** `--reject` rejected the next deployment, and a
  dev deployment still went straight through.

## How development governance works (DEC-029)

The company standard applies from the moment an application is started,
not only when it is deployed. Starting is registering, not asking
permission. Stepping outside the standard is an exception that a platform
administrator decides.

- **Profile.** `PUT /applications/{id}/profile` (owners) records what the
  application is for, who uses it, whether it handles personal data and
  which, and its stack. The MCP's `start_project` registers the
  application and records this in one step. A build refuses an application
  without a profile (`profile_required`, 409, before any build record is
  made). `GOVERNANCE_REQUIRE_PROFILE=false` turns that off, e.g. for the
  older dev-header verify scripts. Owners and administrators can read it.
- **The standard.** `GET /standards` (anyone signed in): the stack
  catalog, the rules in words (`domain.CompanyStandardRules`), and the
  dependency rules.
- **Dependency rules.** `POST /standards/dependency-rules` and `DELETE
  /standards/dependency-rules/{id}` (administrators only). Each rule names
  a library the company doesn't allow, per ecosystem (`npm`, `go`,
  `pypi`). Names are compared the way each ecosystem does: npm without
  regard to case, PyPI also treating `-`, `_` and `.` as one (PEP 503),
  and Go module paths as written. None are set by default.
- **The build's check.** After the source is kept for review and before
  anything is installed, the build reads each service's root
  `package.json` (every kind of dependency, since the build installs dev
  dependencies too), `go.mod` (`require`) and `requirements.txt`. A
  library on a rule fails the build with category `source`, naming the
  library, the manifest and the rule's reason. A manifest that doesn't
  parse fails it the same way. `POST /applications/{id}/standards-check`
  runs the same check on an uploaded archive without building (the MCP's
  `check_project`).
- **Exceptions.** `POST /applications/{id}/exceptions` (owners): kind
  `dependency` (subject `<ecosystem>:<package>`, normalized like the
  rules), `stack` or `other`, with a reason. Asking again for what is
  already pending or approved returns that request. Administrators are
  notified, see the queue at `GET /standards/exceptions?status=pending`,
  and decide with `POST /standards/exceptions/{id}/decision` (a rejection
  needs a reason). The owners are notified of the decision. Every step is
  in the audit log. An approved dependency exception lets that library
  through the build for that application only. A deleted application's
  pending requests leave the queue, and it can't ask for new ones.
- **Portal.** The **Standards** page (administrators): the exception
  queue with approve/reject, the dependency rules, the standard, and the
  decided exceptions.

### Known gaps

- **Stack and other exceptions aren't enforced.** They record the
  administrator's decision; validation still accepts only the active
  catalog, whatever an exception says.
- **Only root manifests are read**, the ones the build installs from.
  `pyproject.toml`, `package-lock.json`'s transitive dependencies and
  `go.sum` aren't checked.
- **Applications registered before this** have no profile, so their next
  build answers `profile_required` until `start_project` is called with
  their `application_id`.
- Tests run on the platform, and dev copies: see "How tests run on the
  platform" and "How dev copies work" (DEC-029 parts 2 and 3).

### Verifying it

```bash
cd services/mcp-server
.venv/Scripts/python.exe scripts/verify_governance.py
```

It runs through the real MCP server over stdio against the local stack.
platform-api is restarted with a throwaway administrator, and without it
at the end. Results (2026-10-24, all passed):

- **Start:** `start_project` refused `php` and registered nothing. It
  then registered the application with its profile, which the
  administrator read with its Thai text intact. `scaffold_project` laid
  out the React + Go templates with `REVIEW.md` and `deployment.yaml`.
- **Rules:** an employee couldn't add a rule (403). The administrator
  added one against the templates' own MySQL driver.
- **The check and the build:** `check_project` found it, and the real
  build failed with category `source`, naming the library and what to do.
- **Exceptions:** `request_exception` notified the administrator. The
  owner couldn't decide it (403), and a rejection without a reason was
  refused (400). The administrator approved it, and the owner was
  notified.
- **After approval:** `check_project` showed the library as allowed by
  the exception, and the same project built and deployed for real (38 s).
- **No profile:** an application registered with `create_application`
  alone was refused at build with a message naming `start_project`, and
  no build was recorded.

Also run against the same stack: `e2e_verify.py` (59 checks, now 31 tools,
recording a profile with `start_project` for an existing application) and
`verify_lan_stack.py` with `RUNTIMES=go`. The Standards page was driven in
Chromium: approve, reject without a reason refused, a direct visit to
`/standards`, no page errors. A pending exception left the queue once its
application was deleted, and the deleted application's new request got
404.

## How tests run on the platform (DEC-029 part 2)

An application's tests run on the platform, so an employee's machine
needs no Go, Node or Python. The report an approver signs is the
platform's own, not the requester's word.

- **Running them.** `POST /applications/{id}/test-runs` (owners) takes the
  same tar.gz a build does. Each service in the saved deployment.yaml is
  tested in its runtime's governed image:
  - `go`: `go vet ./...` and `go test ./...`;
  - `python`: requirements installed, `python -m compileall`, then
    `pytest` when there are test files (`test_*.py`, `*_test.py`);
  - Node runtimes: `npm install`, the `build` script when there is one,
    then the `test` script when there is one. npm's placeholder test
    script doesn't count.

  A service with no tests is reported as "no tests to run", never as
  passed tests. The run is `passed`, `failed`, or `error` (the platform
  couldn't run it: a pull, the time limit), and the report keeps the end
  of each service's output.
- **The sandbox.** A test run is a throwaway image build of a test recipe
  (`internal/buildengine/testrun.go`): the same sandbox a build uses, with
  no Docker socket and nothing mounted from the host, plus memory and CPU
  limits per service (`TEST_RUN_MEMORY_MB`, `TEST_RUN_CPUS`) and a time
  limit for the whole run (`TEST_RUN_TIMEOUT_SECONDS`). The image is
  removed afterwards. Dependencies install before a per-run build
  argument, so the layer cache keeps them between runs while the test
  steps always run again. One run at a time by default
  (`TEST_RUN_CONCURRENT`): one more is refused with 429, not queued.
- **Into the deployment.** A deploy request can name `test_run_id`. The
  platform attaches that run's report only if the run passed and was made
  on the same source as the build being deployed. "Same source" means the
  same files and contents (`domain.SourceTreeDigest`, compared with the
  build's kept source archive), so a project packed twice still matches.
  Otherwise the deploy is refused with 409 `test_run_not_usable`. A
  `test_report` given as well is added after it under a "from the
  requester" line. A `test_report` alone is now always marked that way, so
  no report can pass for one the platform ran. The approval page shows
  "Run by the platform" or "Requester's report only".
- **The MCP.** `run_tests`, and `deploy_application`, which runs the tests
  on exactly the bytes it then builds and builds nothing if they fail.

**Also fixed here: an application built but not deployed couldn't be
built again.** A build moves the application to `Build`, and a build was
only accepted from Validated, Running or Failed. When a deploy was refused
after a successful build, the application stayed in `Build`, and a
running one, with its previous version still serving, couldn't be
archived or deleted either. `verify_test_runs.py` hit exactly that, with a
test run of other source. A build is now accepted from `Build` too (a
build in flight is still refused), and a failed one returns to `Build`.

### Known gaps

- **No database or secrets in a test run.** Tests that need the
  application's MySQL or its secrets can't pass there.
- **The network is open** during a test run, as it is during a build:
  dependencies are downloaded from the registries.
- **Only the runtimes' standard commands run.** There is no field for a
  custom test command.
- **Interrupted runs.** A run interrupted by a restart is closed as
  `error` when the platform starts.

### Verifying it

```bash
cd services/mcp-server
.venv/Scripts/python.exe scripts/verify_test_runs.py
```

It runs through the real MCP server over stdio against the local stack.
platform-api is restarted with a throwaway approver key and production
needing approval, and without them at the end. Results (2026-10-24):

- **The scaffolded React + Go project.** `run_tests` passed in about
  100–120 s. The report showed `go vet`, `go test` (including a test that
  checks the Docker socket isn't reachable from a test run), the React
  build, and "no tests to run" for the frontend.
- **A failing test.** The run failed with the test's output, Thai text
  intact. `deploy_application` refused and showed the failure, and built
  nothing (no build record).
- **Two runs at once.** A second run during one got 429.
- **Fixed.** `deploy_application` ran the tests on what it built, built,
  and deployed to dev (about 135 s).
- **Changed source.** A passed run of the earlier files was refused for a
  build of changed source.
- **Production.** The tests ran again on what was built, and the
  deployment waited for approval. The approver's view had the platform's
  report (no terminal codes) and the requester's note after it, marked as
  theirs. The report's hash was in the signing template.
- **Two state bugs on the way, both fixed and unit-tested.**
  - An app stuck in `Build` (above).
  - A **rejected** deployment marked the application `Failed` even with
    an earlier version still serving. It could then be neither archived
    (only Running or Suspended can be) nor deleted (something is live).
    It now stays `Running` when an earlier version serves.

## How dev copies work (DEC-029 part 3)

Each application can have one long-lived **dev copy**, `<name>-dev`, to
try changes on.
- **Its own application.** Like any application: its own database, its
  own address (`/<name>-dev/`) and its own lifecycle.
- **Approval.** Deploying to it never waits for approval.
- **Data.** Its database stays from one deployment to the next, so what
  an employee entered while trying it is still there after each change.
- **Production.** The application itself is production, which waits for
  approval as before.

The platform owner first asked for a 24-hour preview. They changed this
on 2026-10-24 because an employee changing an application wants to keep
the data they entered.

**Why its own application, not the application's dev environment.** One
application has one live deployment, one database, one address and one
lifecycle status. A dev deployment of the application itself would
replace what production serves, and share its data. An ungated dev on
the same address and database would bypass approval altogether. So the
copy is a linked application (`dev_copies`). A dev deployment of the
application itself through the API still follows
`APPROVAL_REQUIRED_ENVIRONMENTS`.

- **Deploying to dev.** `POST /applications/{id}/dev` (owners; `{id}` is
  the application or its copy) takes the same tar.gz a build does.
  - **The first time,** it makes the copy: registered as the caller,
    named `<name>-dev`, or `-dev2`, `-dev3` (a deleted application keeps
    its name). It uses the application's deployment.yaml (database
    backups off) and its profile.
  - **Every time,** it follows the application's current deployment.yaml,
    then validate → build → deploy to `dev`.
  - **On failure,** a failed build leaves the copy's running version and
    data as they were.
  - **Approved exceptions.** The copy builds with the application's
    approved dependency exceptions.
- **The DEV banner.** Its pages carry a "DEV — not the live application"
  banner, added by the proxy to HTML responses.
- **Test data Claude adds.** `POST .../dev/test-data {"note"}` (MCP
  `record_test_data`) keeps Claude's record of test data it put in the
  copy's database itself, with who and when. This happens every time
  Claude does it.
- **Data into production, once.**
  - **The request.** A production deployment can ask for
    `copy_dev_data`, only while production has never run (409
    `dev_data_not_copyable` otherwise), and only when there is a dev copy
    with a database.
  - **What the approver signs.** The deployment records which copy and a
    summary that includes Claude's test-data note. The summary is added
    to the signing template as a `dev-data:` line, and shown in the
    Portal and `company-deploy approve`.
  - **The copy.** Once the deployment is live, production's database is
    backed up and then filled from the copy's (a dump and restore). The
    result is recorded on the deployment (`dev_data_copied_at` /
    `dev_data_copy_error`), and the owners are told if it failed.
- **The MCP side.**
  - Like a PaaS, the platform is where an employee sees their
    application. `scaffold_project` deploys a new project to dev straight
    away and returns the link. A dev deployment after each change skips
    the platform test run unless asked; the build still compiles
    everything.
  - `deploy_application` to dev goes to the copy, with no REVIEW.md
    needed.
  - To production, if there is a copy and production has never run, it
    stops and makes Claude **ask the employee whether the dev data is
    real**. The question names Claude's recorded test data. Claude answers
    with `dev_data="copy"` or `"leave"`.
  - After every dev deployment Claude is told to ask: production, or
    keep changing.
- **A changed deployment.yaml keeps the data.** Applications that have
  been deployed (Build, Running, Failed, Suspended, Rolled back) now
  revise their deployment.yaml in place: `PUT .../deployment-yaml` (MCP
  `validate_application` with `deployment_yaml`).
  - The new contract passes the same checks as a new one, or nothing is
    saved.
  - The application keeps its status, its database and its data. The
    next build and deployment use the new contract, for example a service
    added.
  - The only change refused is to the database: another type, or none
    (409 `database_change_loses_data`), because the data is in it.
  - Before, a deployed application's deployment.yaml couldn't change at
    all, and a dev copy would have had to be reset, losing its data. On
    each dev deployment the copy takes the application's deployment.yaml
    this way.
- **Ending.** `DELETE .../dev` (`reset_dev`) removes the copy, its code
  and its data. A sweeper (every minute) removes the copy of an archived
  or deleted application. A copy deleted on its own is replaced on the
  next dev deployment.
- **Portal.** A Dev copy card on the application's page shows the link,
  Claude's test-data note, and **Reset** (with a confirmation). The
  approval page shows where production's data comes from.

### Known gaps

- **RAM.** A copy's MySQL runs all the time (only the frontend and API
  scale to zero). Each application with a dev copy and production runs
  two databases.
- **The copy is owned only by whoever made it.** The application's other
  owners see, deploy to and reset it through the application.
- **Dev data goes to production once, whole.** There is no merge and no
  per-table choice. After production has run, its data is never replaced
  from dev.
- **Rolling back past a deployment.yaml change.** Activation reads the
  application's current deployment.yaml. A rollback to a deployment built
  before a service was added would look for that service's image in an
  older build, which doesn't have it. This gap existed before, but
  revising deployed contracts makes it reachable.
- **The approver doesn't see deployment.yaml.** A revised one takes
  effect at the next deployment, which a gated environment approves. The
  signing template covers the images, source, tests and REVIEW.md, not
  the deployment.yaml.
- **The test-data record is only as complete as Claude's reports.** The
  platform can't tell who wrote a row.

### Verifying it

```bash
cd services/mcp-server
.venv/Scripts/python.exe scripts/verify_dev_copies.py
```

It runs through the real MCP server over stdio against the local stack,
with a throwaway approver key and `APPROVAL_REQUIRED_ENVIRONMENTS=dev,production`
(as on the LAN server). Results (2026-10-24):

- **The first dev deployment.** It made `<name>-dev` and was running
  without waiting, though dev is gated (about 2 minutes, tests included).
  The application itself had no deployment. The copy's page carried the
  DEV banner.
- **Data kept.** An employee's item and Claude's item (recorded with
  `record_test_data`) were still there after the next dev deployment, at
  the same address.
- **The first production deployment.** It stopped and asked about the
  data, naming Claude's test item, and built nothing.
- **`dev_data="copy"`.** The deployment waited for approval. The approval
  request showed the copy and Claude's note, and the signing template had
  the `dev-data:` line. Signed with a throwaway key, it went live, and the
  copy was recorded as done.
  - Production served both items.
  - Production's page had no banner.
  - The dev copy kept its own data.
- **No second copy.** A second production deployment couldn't copy again,
  through the MCP or the API (409).
- **Reset.** `reset_dev` removed the copy and its database container, and
  production's data was untouched. The next dev deployment made `-dev2`,
  empty.
- **Archive.** Archiving the application removed its dev copy within the
  sweep.
- **A service added to a dev copy that holds data.** The worker answered
  with its database connected, and both items were still there. The copy
  was revised in place, not reset.
- **Running production.** Changing its database type was refused (409
  `database_change_loses_data`). Changing its scaling was revised in
  place, and it stayed Running.
- **A bug found on the way and fixed.** The second dev deployment first
  failed ("not in a state this operation can be performed from"). It
  re-saved deployment.yaml onto a running copy, which no deployed
  application accepted. Deployed applications now revise their
  deployment.yaml in place (above).

## How Alerts work (FR-092, DEC-028)

Every 60 seconds (`ALERT_EVAL_INTERVAL_SECONDS`) platform-api judges each
running service against the decided thresholds (set B). It uses its own
measurements (Module T) and the host's data disk:

| Rule | Over | Warning | Critical |
|---|---|---|---|
| 5xx rate (only from 20 requests up) | 5 min | 5% | 20% |
| Mean latency (from 5 requests) | 5 min | 1 s | 3 s |
| CPU, *every* sample over the line, readings covering ¾ of the window | 10 min | 85% | 95% |
| Memory, as a share of the limit (the host's RAM: no quotas) | 5 min | 80% | 95% |
| The host's data disk (where databases, backups and logs live) | now | 85% | 95% |

**The rhythm** is what the platform owner chose. An alert is announced:
- once when it **starts**;
- once if it **becomes critical**;
- as a **reminder at most once an hour** while it lasts;
- once when it **clears**.

A drop from critical back to warning isn't announced. A service that
stops being measured (scaled to zero, stopped) counts as cleared.

**State.** What is firing lives in `alert_states`, so a restart neither
forgets an alert nor announces it again. Every announcement is kept in
`alert_events` for 90 days (`DEC-027`).

**Recipients.** Application alerts go to the application's owners.
Platform alerts (the disk) go to `PLATFORM_ADMIN_EMAILS`. Both arrive in
the in-app inbox (category `alert`). Owners also see what's firing:
- on the application page in the Portal;
- at `GET /applications/{id}/alerts` (current state, the last 7 days, and
  the thresholds in force);
- in the MCP's `get_application_status` (`active_alerts`).

Every number is overridable through `ALERT_*` (see `.env.example`).

### Known gaps

- **In-app only.** No e-mail, Slack, Teams or LINE: no channel has been
  chosen (`DEC-018`).
- **Means, not percentiles.** Latency is a mean: Module T keeps a total
  and a maximum per minute, not a distribution.
- **No alert on a service that's down.** A service that never answers
  produces 502s at the proxy, which the error-rate rule catches. Module R
  already restarts unhealthy instances and notifies owners.

### Verifying it

```bash
python services/platform-api/scripts/verify_alerts.py
```

It deploys a real application and drives real traffic through the proxy,
with platform-api restarted on short windows so the run takes minutes.
Each check is read from the notifications the owner and the admin actually
received:
- slow responses raised a latency warning, which resolved once diluted;
- 7% 5xx raised a warning, and more failures escalated it to critical,
  announced once;
- a platform-api restart did not announce it again;
- after the reminder interval came exactly one "Still critical";
- the owner's alerts endpoint showed it with its history (another
  employee got a 404), and healthy traffic resolved it;
- a real busy loop in the container raised a CPU alert, which resolved
  when it stopped;
- the disk alert reached the admin and not the owner.

## How Continuous Health Monitoring works (Module R, FR-084/085)

FR-083's deploy-time health check (see **How Deploy works** above) only
ever ran once, at activation. Nothing re-checked an instance afterward —
a service that started healthy and later hung, deadlocked, or leaked its
way into failure just kept receiving traffic, forever, until someone
noticed and restarted it by hand. `cmd/api/main.go`'s `runHealthSweeper`
closes that: on a platform-wide interval (`HEALTH_SWEEP_INTERVAL_SECONDS`,
default 15s, same "engineering default, not a ratified value" status as
the scale-to-zero and metrics intervals), `HealthMonitorService.Sweep`
re-runs the same health check against every currently-`Running` instance
across every application — not only the ones the idle sweeper cares about;
a static frontend or a `scaling.min>=1` backend is never scale-to-zero
eligible, but it still gets checked here.

- **Detection debounces a single blip.** A miss has to happen on two
  consecutive sweeps before it counts as FR-084's "begins failing health
  checks" — a lone dropped packet or GC pause isn't a service that's down.
- **Remediation (FR-085)** stops the unhealthy container and starts a
  fresh one in its place — the same Stop/StartContainer/HealthCheck
  sequence `Restart` (FR-048) already uses, just system-triggered and
  scoped to the one instance that actually failed. The replacement is
  itself health-checked before it rejoins the pool (mirrors FR-083); if it
  also fails, the service is left with **no** running instance rather than
  quietly serving a container already known to be broken, and the owner is
  notified (Module X) that it needs manual attention.
- **A restart loop is not treated as routine remediation.** If the same
  service gets remediated 3 times within 5 minutes (platform-chosen
  numbers, not spec-given), the platform stops trying, leaves it down, and
  sends one elevated notification rather than one every sweep — FR-085's
  alternative flow calls this out explicitly ("to avoid a restart loop
  masking a systemic issue"). The pause lifts on its own once the window
  passes with no further remediation; there's no administrator role yet to
  action an explicit "resume" on.
- **A scaled-to-zero instance has nothing to poll**, per FR-084's own
  business rule — the sweep only ever looks at services with a live
  container.
- **"Remediation event is logged" (FR-085 step 4)** is Module S: the
  replacement container's output is captured exactly like any other
  (`Log` is set on every `StartContainer` call here too), so its
  history — including whatever it printed while unhealthy — isn't lost.

### Known gaps

(FR-099, automatic rollback on a post-activation health regression, used
to be listed here — it's implemented now; see **How Rollback works**
above, the "FR-099" section, for what it does and the one narrow gap it
carries over from the manual Rollback path.)

- **The failure/remediation-count bookkeeping is in-memory**, not
  persisted — a platform-api restart forgets how many times a service was
  recently remediated (the circuit breaker resets) and forgets any
  in-progress debounce count (a service that had one recent miss needs a
  fresh sustained failure to trigger remediation again). Never a false
  remediation, only a slower one, and simpler than persisting a counter no
  read path needs.
- **No dedicated "instance down" or "remediation history" API/UI.** What
  happened is visible in the platform's own log and in the owner's
  notification inbox (`GET /notifications`), not as a queryable timeline —
  no FR here asks for one, unlike FR-056's scale-event history, which the
  Admin Portal does show.
- **The numbers (debounce count, remediation timeout, circuit breaker
  limit/window) are this platform's own choices**, the same status as the
  scale-to-zero idle timeout and the metrics sampling interval: nothing in
  the requirements specifies them.

### Verifying it

`scripts/verify_module_r.py` runs against a live stack and a real Docker
daemon, no mocks:

```bash
docker compose up -d --build   # from the repo root, HEALTH_SWEEP_INTERVAL_SECONDS=3 in .env to watch it move
python services/platform-api/scripts/verify_module_r.py
```

It deploys an application that answers normally until told to fail, waits
through several sweeps to confirm a healthy instance is left alone, then
tells it to start failing. In the run that verified this module: the
platform's own background sweeper — not this script — detected the
failure and replaced the container within one sweep interval of the
debounce, proven at the Docker level (the container id changed) and at the
application level (a different instance answered, by its own hostname);
the application kept serving throughout at its stable `/run/{app}/{service}`
URL; and the owner had a `health_remediation` notification explaining what
happened. The exception flow (a replacement that's also unhealthy) and the
circuit-breaker alternative flow are covered by
`internal/service/health_service_test.go`'s unit tests instead, for speed
and precision — those tests can script an exact sequence of failures in
milliseconds that would take real sweep intervals to reproduce live.

A live container *can* be scripted into staying broken across a restart
on demand, though — `scripts/verify_fr099_rollback.py` (see the FR-099
section under **How Rollback works** above) does exactly that, by making
the application answer healthy for its first request ever and unhealthy
from the second request onward, deterministically, regardless of which
container instance is asked. That end-to-end run is what verified FR-099
itself: the platform failed to fix it via restart-in-place a few times,
tripped its own circuit breaker, and automatically rolled back — with the
application still reachable at its stable URL throughout, serving the
prior version again once the rollback landed.

### After the host restarts

Found by the first reboot of a real LAN host (192.168.10.25,
2026-10-23):
- **The platform stayed down.** None of the compose services had a restart
  policy. They now have `restart: unless-stopped`.
- **Applications came back without their database.** Once the platform was
  started by hand, the health sweeper replaced every application's API and
  frontend. Nothing restarted their MySQL containers, though. Each
  application answered as healthy with its database disconnected, and its
  backup failed. Database containers are now created with
  `unless-stopped`. A database is only ever stopped by being removed, so
  this never revives one on purpose. Application containers still get no
  restart policy: restarted, they'd come up on a host port the platform
  doesn't route to. The sweeper replaces them instead, as before.
- **A request could make remediation fail an application that was
  running.** Remediation clears the container before starting its
  replacement. A request arriving in that moment sees the service as
  scaled to zero and cold-starts it under the same container name.
  Remediation's own start then failed on the name conflict. It took that
  to mean the service was down, triggered FR-099's rollback (the
  deployment was minutes old) and left the application `failed`, while
  the cold-started instance was serving. Now, when its start fails,
  remediation waits up to 20s for another instance to be recorded, and
  keeps it if one is. `TestHealthSweep_ColdStartedWhileBeingReplaced_KeepsThatInstance`
  reproduces the race. The live re-run below didn't hit it: the timing
  has to line up.

`scripts/verify_host_restart.py` deploys React + Go + MySQL, writes an
item, and restarts the Docker daemon (`sudo systemctl restart docker`),
which stops every container the way a reboot does. It refuses to run if
`live-restore` is enabled. Run on that host after the fix, it checked:
- platform-api answered again 18s after the restart, with postgres,
  platform-api and the gateway all up;
- the database container was running without anyone starting it;
- the application answered with its database connected 63s after the
  restart;
- the item written before the restart was still there.

## What's deliberately NOT here yet

Each will land as its own feature branch/PR, per the Application Lifecycle:

- Registry push — not a gap, a decision: `DEC-005` (Decided 2026-09-11) is
  that no separate registry is needed. Built images live in the local
  Docker daemon on the single on-prem host both the Build Engine and
  Deployment step talk to — that's the intended permanent shape, per
  `DEC-004`'s revision, not a stand-in for a multi-host setup that isn't
  planned.
- Enterprise SSO/IdP integration — not a gap, a decision (`DEC-001`).
  The platform authenticates employees itself: see **How authentication
  works** below.
- Resource quota enforcement (FR-032) — not a gap, a decision: `DEC-014`
  (2026-10-23) is no quotas, so Module M is not built.
- Full RBAC / Role / Permission tables (Module A/B) — not a gap, a
  decision: `DEC-002` is that there is no role beyond application
  ownership, with one addition from `DEC-017`. A platform administrator
  holding an approver key approves deployments, and receives platform
  alerts (`PLATFORM_ADMIN_EMAILS`).
- Stack version governance (FR-022, deprecated/blocked versions) — the
  catalog only tracks active/deprecated/blocked per whole runtime name, not
  per version range yet.
- Retry path out of `failed` — see **Known gap** above (Build section).
- Build/deploy status streaming/notifications (FR-036/FR-043 alternative
  flows) — status is poll-only for now; both the build and the deploy
  pipeline also run synchronously within the triggering HTTP request rather
  than being queued asynchronously, which is fine for small internal-tool
  builds/deploys but won't scale to slow ones without a background
  job/worker model.
- Image-scan severity threshold is hardcoded to "any CRITICAL blocks" —
  FR-041 says this should be Security Administrator policy; no such policy
  exists yet to read from (worth a `DEC-xxx` entry).
- Dual base-image governance (a BUILD-stage image and a separate
  RUNTIME-stage image per runtime, both IT-governed) — today only the
  build-stage image is catalog-driven; the minimal runtime-stage image for
  `go`/`react`/`vue` is a fixed platform constant (see **How Build works**).
- True horizontal scaling above 1 instance (FR-054's `scaling.max` ceiling)
  — see **How Scale-to-Zero works** above.
- Graceful in-flight-request draining before a scale-to-zero shutdown
  (FR-052's alternative flow) — the sweeper stops a container based on
  idle time only; a request that arrives in the same instant as a sweep
  decision isn't specially drained, just subject to the same narrow race
  window documented in **How Scale-to-Zero works**.
- Per-application/per-tier idle timeout tuning (FR-052's business rule
  mentions "potentially tunable per resource tier") — today it's one
  platform-wide constant; real tuning needs Module M (Resource Manager),
  not built yet.
- Security Administrator force-suspend, bypassing owner-initiated Suspend
  — not a gap, a decision: `DEC-002` means there is no Security
  Administrator role to perform it. Only owner-initiated Suspend exists,
  and that's the permanent model.
- Real deprovisioning on Archive/Delete for domains (Module P) — not a
  gap, a decision: `DEC-007` (Deferred 2026-09-11) is that Module P isn't
  needed for this LAN-only deployment at all (see **Self-declared
  identity** below), so there is nothing for Archive/Delete to deprovision
  there. Databases (Module N) and secrets (Module O) *are* removed for
  real. Every Application Lifecycle state this deployment needs (`Draft` →
  `Validated` → `Build` → `Deploying`/`Running`, `Suspended`, `Rolled Back`
  as a transient step back to `Running`, `Archived`, `Deleted`) is now
  implemented.
- Un-archive / reactivation of an `Archived` application — see **How
  Archive/Delete work**'s known gap.
- Production de-registration approval gate (FR-050, "mirrors FR-014") —
  not a gap, a decision: same `DEC-002` basis as the production-deploy
  approver-independence point above — there is no distinct approver role
  to gate on.

## Departments

There is no fixed list of departments. A department is added:
- when someone registers under a new one;
- by `POST /departments {"name": ...}` (the Portal's Departments page);
- by the MCP `create_application` tool, given a department that doesn't exist yet.

Names are normalized (trimmed, inner whitespace collapsed, at most 80 characters, any script) and matched without regard to case, so "innovation" finds "Innovation" instead of creating a second one. A name that isn't valid UTF-8 is refused with 400. That case was found when a Thai name sent from a Windows console arrived in its code page and would have been stored as replacement characters.

`create_application` with no department uses the signed-in employee's own. `GET /departments/summary` lists each department with its people and (not deleted) applications.

Verified on the local stack:
- the API added a department, matched it again in lowercase without a duplicate, refused an empty name and a non-UTF-8 one, and refused anyone not signed in;
- a real browser added `ฝ่ายบุคคล` and registered an application under a department that didn't exist yet;
- the real MCP tool created "Logistics …", matched "ENGINEERING" to "Engineering", and defaulted an empty department to the caller's own.

## How the administrators' dashboard works

`GET /admin/overview` answers only accounts in `PLATFORM_ADMIN_EMAILS` (case-insensitive); anyone else gets 403. `GET /auth/me` carries `is_admin`, which the Portal uses to offer the **Dashboard** page. The API checks again on every request.

- **The host.** CPU, memory, load and uptime come from `/proc/stat`, `/proc/meminfo`, `/proc/loadavg` and `/proc/uptime`. Docker doesn't namespace these, so inside platform-api's container they describe the whole host. They're read every 5s and the last 15 minutes are kept in memory for the charts. CPU is the non-idle share between two readings, with iowait counted as idle. Memory used is MemTotal minus MemAvailable, the figure `free` shows. Without `/proc` (a developer's Windows or macOS) the host is reported unavailable, not invented. Disk is the same reading the disk alert uses: the volume holding the backups.
- **Applications.** Every application that isn't deleted, with its department, primary owner, the environments it runs in and how many containers serve it now. CPU and memory are each serving container's latest reading from the metrics sampler (Module T), summed. Requests, errors and mean latency cover the last 5 minutes of proxy traffic.
- **Traffic.** Requests and errors per minute across every application, for the last 15 minutes.

### Verifying it

`internal/hostinfo` and `admin_overview_service_test.go` test the parsing, the CPU arithmetic, the history window, the admin check and how readings are combined.

On the local stack, with a test account as the administrator, a real browser:
- signed in and saw the Dashboard menu entry;
- read the host (12 cores, 3.1 of 7.6 GB);
- saw the 43 requests just sent to an application, with every application listed under its owner;
- found no sideways scroll at 390px.

With no administrator configured, `/admin/overview` without a session is 401.

## How authentication works (DEC-001/DEC-003, revised 2026-10-23)

Every endpoint except `/healthz` and the application proxy (`/run/...`)
needs `Authorization: Bearer <token>`. A token is one of two kinds of
credential (`internal/domain/auth.go`):

- **Portal session** (`cps_…`, 12h by default). An employee gets one by
  registering or signing in with email and password.
- **API key** (`cpk_…`, 30 days by default). Each Claude Code machine
  fetches its own through a **device authorization** before it can deploy.

Only a SHA-256 of each token is stored, and passwords are bcrypt. The
pg_dump check in `scripts/verify_auth.py` confirms neither appears in the
database.

```
POST /auth/register   {email, password, full_name, department, enrollment_code}  -> session
POST /auth/login      {email, password}                                         -> session
POST /auth/device     {client_name}         -> device_code, user_code, verification_uri(_complete)
POST /auth/device/token {device_code}       -> authorization_pending | access_denied | expired_token | api_key
GET  /auth/me                               -> who this credential is
POST /auth/logout                           -> revoke the credential used
# session only — never an API key:
GET/POST /auth/api-keys, DELETE /auth/api-keys/{id}
GET /auth/device/{userCode}, POST /auth/device/{userCode}/approve|deny
```

**The device flow.**
1. The machine asks `/auth/device` and shows the employee a code such as
   `BCDF-GHJK` and a link to the Admin Portal's `/device` page.
2. The employee signs in there with their own password and approves the
   code.
3. The machine's next poll of `/auth/device/token` receives a key named
   after the machine.

The key is generated at that moment, so it exists in plaintext only on its
way to the machine. A device code yields exactly one key and expires after
10 minutes. Device codes follow RFC 8628's error vocabulary.

**What a key cannot do.** It can do everything its employee can, except
credential management:
- listing, issuing or revoking keys;
- approving or denying a device.

Those take a Portal session. So an AI agent holding a key can never mint
itself another key or approve its own request.

**Registration** needs `PLATFORM_ENROLLMENT_CODE`, the shared code IT sets
and hands to employees. It is compared in constant time, and the platform
refuses to start in token mode without one. `PLATFORM_ALLOWED_EMAIL_DOMAINS`
can additionally limit which emails may register. An account the old
self-declared identity created has applications but no password; the
first registration with that email **claims** it, keeping its id and
applications, and only once.

**Sign-in** gives the same answer for an unknown email, a wrong password
and an account with no password yet. After 5 failures in 15 minutes an
email is locked out for the rest of that window, even with the right
password. The failure count is in memory, so a restart forgets it.

**Audit.** Registering, signing in (and failing to), issuing and revoking
keys, and deciding devices are recorded as `auth.*` actions against the
employee they concern (`resource_type = credential`). They appear in that
employee's own audit view.

**Authorization is unchanged** (DEC-002): there is still no role beyond
application ownership. Authentication only makes the "who" real.

### The dev-header mode

`PLATFORM_AUTH_MODE=dev-header` brings back the old self-declared identity
(`X-Dev-User-Email` and friends, `internal/httpapi/devauth.go`) for local
testing only. `config.Load` refuses it unless `PLATFORM_ENV=dev`. The
older verification scripts (`verify_module_n.py` and the others before
this change) still authenticate that way, so they need this mode.
`verify_auth.py`, `verify_lan_stack.py` and the MCP end-to-end script sign
in for real (`scripts/platform_auth.py`).

### Known gaps

- **No password reset and no MFA.** An employee who forgets their
  password needs IT to clear `users.password_hash`, after which
  registering with the enrollment code sets a new one.
- **The enrollment code is shared.** Anyone who has it and an
  unregistered email (in an allowed domain, if set) can create an account.
  Rotate it by changing `PLATFORM_ENROLLMENT_CODE` and restarting;
  existing accounts are unaffected.
- **Lockout is per email, in memory, and not per source address.**
- **Starting a device authorization needs no credential**, by design (the
  machine has none yet), and isn't rate-limited. Unapproved requests grant
  nothing and expire after 10 minutes, but they are not purged.
- **The Portal keeps its session in `localStorage`**, readable by any
  script running on the Portal's origin.

### Verifying it

```bash
docker compose up -d --build      # from the repo root, with .env present
python services/platform-api/scripts/verify_auth.py
```

It checks the following, over real HTTP against the live stack:
- nothing answers without a credential, and the old header is refused;
- registration needs the code;
- sign-in doesn't reveal which emails exist, and locks out;
- the device flow, end to end: pending, approved, collected once, denied;
- the key registers and validates an application owned by the employee
  who approved it, which another employee can't act on;
- the key can't touch credentials;
- revoked keys and signed-out sessions stop at once;
- every event is in the audit log;
- a full `pg_dump` contains none of the key, session, password or device
  code.

## Running locally

From the repo root, first time only:

```
cp .env.example .env   # then edit POSTGRES_PASSWORD if you want a non-default value
openssl rand -base64 32   # required: paste the output into .env as PLATFORM_SECRET_KEY
python -c "import secrets; print(secrets.token_urlsafe(12))"   # required: .env's PLATFORM_ENROLLMENT_CODE
```

`.env` is git-ignored — `docker-compose.yml` reads all credentials from it
and refuses to start with a clear error if it's missing (see
`.env.example` for what's needed). If you're also running
[`../../apps/admin-portal`](../../apps/admin-portal) (a browser client),
check `CORS_ALLOWED_ORIGINS` in the same file matches whatever origin its
dev server actually printed — see that app's README for why this isn't
always `http://localhost:5173`. Then:

```
docker compose up --build
```

Then:

```
curl -X POST localhost:8090/auth/register -H "Content-Type: application/json" \
  -d '{"email":"alice@example.com","password":"at-least-10-chars","department":"Engineering","enrollment_code":"<from .env>"}'
# -> {"token":"cps_...", ...}; send it as a Bearer credential from here on
curl localhost:8090/departments -H "Authorization: Bearer cps_..."
curl -X POST localhost:8090/applications -H "Authorization: Bearer cps_..." \
  -H "Content-Type: application/json" \
  -d '{"name":"overtime","description":"HR overtime tracker","owning_department_id":"<department id>"}'
```

(Registering creates the department you name if it doesn't exist yet.
A Claude Code machine uses an API key instead of a session — see **How
authentication works**.)

To exercise a real build, after registering + saving a valid
`deployment.yaml` + validating: `POST` a `tar.gz` archive as the raw request
body to `/applications/{id}/build` (a top-level directory per service name —
see **How Build works** above). The Build Engine needs the Docker socket
mounted into the container — `docker-compose.yml` already does this and
runs `platform-api` as `root` locally so it can reach `/var/run/docker.sock`
(see the comments there for why).

Then, to exercise a real deploy: `POST /applications/{id}/deploy` with
`{"environment":"dev"}` (or omit the body entirely for the same default).
The first scan pulls and caches Trivy's vulnerability DB (~30–60s); after
that it's fast, since the DB is cached in the `platform-trivy-cache` Docker
volume across scans. Expect the scan to occasionally block a build on a real
CRITICAL finding in the current base image — that's the gate doing its job,
not a bug; see `internal/db/migrations/0003_build_engine.sql`'s comments for
what was actually found and fixed while exercising this for real.

Once `running`, hit the app at `GET /run/{appName}/{serviceName}/` (no
platform auth needed — see **How Scale-to-Zero works**). To actually watch
scale-to-zero happen without waiting the default 5 minutes, lower
`SCALE_TO_ZERO_IDLE_SECONDS`/`SCALE_SWEEP_INTERVAL_SECONDS` in `.env` (e.g.
`20`/`5`) before starting the stack, stop sending requests, and watch
`docker logs` for `scale sweeper: scaled 1 service(s) to zero` — then hit
the `/run/...` URL again and watch it cold-start.

`POST /applications/{id}/suspend`, `.../resume`, and `.../restart` need no
special setup beyond a `Running` application — try hitting `/run/...`
between suspend and resume to see it correctly 404 instead of cold-starting.

To exercise a real rollback: deploy, then deploy again so there are two
successful deployments (see the note above on how to trigger a *second*
build — today that needs a fresh application, or a directly-inserted
`builds` row, since there's no API path back into Build once `running`).
`GET /applications/{id}/deployments` to find the earlier one's id, then
`POST /applications/{id}/rollback` with
`{"target_deployment_id": "<that id>"}`. Hitting `/run/...` before and after
should show the response flip back to the earlier version's output.

`POST /applications/{id}/archive` needs no special setup beyond a `Running`
or `Suspended` application — try `/run/...` afterward to see it 404 the
same way a suspended app does. `POST /applications/{id}/delete` needs
`Archived` or `Suspended` first (calling it directly from `Running` is a
`409`, on purpose) and a `{"confirm": true}` body — omitting `confirm` is a
`400`.

## Running tests

```
cd services/platform-api
go test ./...
```

Service-layer tests (`internal/service`) use in-memory fakes (including a
fake `BuildEngine`/`ImageScanner`/`RuntimeEngine` — no Docker needed) and
need no database. `internal/buildengine` has real unit tests for the pure
logic (tar extraction/injection, Dockerfile templates, build-output-stream
parsing, error classification) that also don't need Docker. There are no
repository-layer (Postgres) tests yet — a future increment should add them
against a real Postgres instance (e.g. via `docker compose` in CI), since
the partial-unique-index and CHECK constraints in the migrations are part
of the actual correctness guarantees.

All eight states have also been manually verified end-to-end against a real
Postgres instance (and, for Build/Deploy/Scale, a real Docker daemon and a
real Trivy scanner — not mocked) via `docker compose up --build` — see the
PR descriptions for the exact `curl` sessions exercised. Worth calling out
specifically, because each surfaced a real bug that got fixed as a direct
result of testing against the real thing instead of only fakes:

- **Build**: a real minimal Go HTTP server was built through the full
  pipeline; the image scan then correctly flagged the Go toolchain left in
  the runtime image (22 CRITICAL findings on the compiler binaries
  themselves) — the Dockerfile template was genuinely broken (bloated,
  unnecessarily exposed), not just imperfect, and is now multi-stage.
- **Build**: a base-image registry pull timeout was originally categorized
  as `source` (blaming the employee) — fixed to correctly classify as
  `platform` based on Docker's own error phrasing.
- **Deploy**: health checks initially used the deployment's own
  `http://localhost:<port>` URL and would have hung/failed forever from
  inside the `platform-api` container — fixed to use
  `http://host.docker.internal:<port>` for the internal check specifically,
  while still returning the `localhost` URL to callers.
- **Deploy**: after those fixes, a full real deploy was exercised to
  completion — Trivy scan passed clean, a container started, the health
  check passed, the reported URL was hit from the *host* machine and
  returned the actual application's response, application `lifecycle_status`
  correctly showed `running`. A production redeploy was then exercised
  through the full approval gate (paused at `pending_approval`, an invalid
  decision value rejected with 400, approval resumed the pipeline), and the
  successful redeploy correctly stopped and superseded the previous `dev`
  deployment's container. A rejection of a further redeploy attempt was also
  verified to leave the still-`running` application untouched.
- **Scale-to-Zero**: with a short idle timeout for testing (`20s`, 5s sweep
  interval), a real deployed Go service was left idle and the sweeper
  correctly stopped its container within one sweep cycle (confirmed both in
  `service_runtime_state` and via `docker ps` — the container was actually
  gone, not just marked). Hitting the stable `/run/{app}/{service}/` URL
  while scaled to zero correctly cold-started a *new* container on a *new*
  host port (0.6s) and served the real response. Firing 5 concurrent
  requests during a second cold start produced exactly one `cold_start`
  scale event (confirmed by counting `scale_events` rows), not five —
  `singleflight` coalescing verified for real, not just asserted in a
  unit test. The full event sequence (`initial_activation` →
  `scaled_to_zero`/`idle_timeout` → `scaled_up`/`cold_start`) was confirmed
  accurate and in order via `GET /applications/{id}/scale-events`.
- **Suspend/Resume/Restart**: deployed a real Go service with
  `scaling.min: 1` (deliberately not scale-to-zero eligible, so the test
  wasn't confounded by the sweeper), confirmed it reachable via the stable
  proxy URL, suspended it, confirmed via `docker ps` its container was
  genuinely gone (not just marked), and confirmed the proxy correctly
  returned a 404 instead of cold-starting it. Resumed it and confirmed
  traffic was restored — this run is what surfaced both real bugs listed
  in **How Suspend/Resume/Restart works** (the migration's constraint
  lookup, and the stale `containers` snapshot; both fixed and re-verified
  with a second full suspend→resume→restart cycle afterward). Restarted
  the running app and confirmed a genuinely new container id/port while
  traffic kept working throughout, and confirmed via `docker ps` that
  exactly one `platform-run-*` container existed at the end of the whole
  cycle — no orphaned containers accumulated across suspend, resume, and
  restart.
- **Rollback**: built and deployed a real "version 1" Go service, confirmed
  it reachable via the stable proxy URL, then deployed a real "version 2"
  onto the same application (this is where the Build-from-`running` gap
  above was found and worked around). Confirmed version 2 was live via the
  proxy and version 1's deployment record correctly `superseded`. Called
  `POST .../rollback` with version 1's deployment id as the target and
  confirmed: the response's `build_id` matched version 1's build, a *new*
  deployment record was created (not a reactivation of the old one), the
  proxy immediately started serving version 1's response again, `docker ps`
  showed exactly one running container (version 2's was genuinely stopped,
  not just marked), and version 2's deployment record flipped to
  `superseded`. Also verified the negative paths for real over HTTP: an
  unknown `target_deployment_id` returns 404, a non-owner caller gets 403,
  and an empty request body gets 400 — not just asserted in unit tests. This
  run is what surfaced the hardcoded-`transientAppStatus` bug documented in
  **How Rollback works**, caught by the new unit tests before this manual
  pass even started.
- **Archive/Delete**: deployed a real Go service, confirmed it reachable via
  the stable proxy URL, archived it, and confirmed via `docker ps` that its
  container was genuinely gone (not just marked) and via `/run/...` that the
  proxy correctly 404s post-archive, same as a suspended app. Confirmed
  `POST .../delete` without a `confirm` field returns `400` and leaves the
  application untouched, then confirmed `{"confirm": true}` transitions it
  to `deleted`. Confirmed `Deleted` is genuinely terminal: a second delete
  attempt and a `suspend` attempt on the same now-deleted application both
  correctly reject with `409`. On a second application, confirmed
  `POST .../delete` called directly from `Running` is rejected with `409`
  (must Suspend or Archive first), then confirmed Delete works directly from
  `Suspended` too, not only from `Archived` — both of FR-050's documented
  valid preconditions exercised for real, not just one. `docker ps` showed
  zero leftover containers for either application at the end.
- **Database Management (Module N)**: automated rather than described, as
  `scripts/verify_module_n.py` — see **How Database Management works**
  above for what it covers and how to run it. Its first run is what found
  the FR-062 isolation defect documented in that section: the database
  container was on `["bridge", "platform-net-..."]`, not on its private
  network alone, so every other application's container could reach it.
  Worth stating plainly, because it is the argument for writing the
  verification before believing the code: the isolation was asserted in a
  doc comment, passed every unit test, and was not real. After the fix the
  same script proves it by connecting from off the network by raw IP and
  timing out.
- **Secret Management (Module O)**: automated as
  `scripts/verify_module_o.py` — see **How Secret Management works**
  above. Setting up its upgrade-path test is what found the Module N
  readiness defect documented there: a freshly deployed application's
  first connection to its own database was refused, because the platform
  reported the deployment running before Postgres had finished
  initialising.
- **Deleting an application that never went live**: checked against the
  live stack. The two genuinely stuck applications from this repo's earlier
  verification runs (`validated` and `build`) were deleted through the API;
  a draft was deleted directly, with the secret set on it purged; a first
  deploy that crashed after its database was provisioned was deleted, with
  that database's container, network, record and password all gone; and an
  application mid-rebuild with its previous version still serving was
  refused with `409` and kept serving. The Admin Portal's Delete button was
  checked the same way in a real browser.
