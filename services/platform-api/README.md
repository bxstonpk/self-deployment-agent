# Platform API — Draft through Archive/Delete (State 1–8) + Audit Log, Notifications, Ownership, Reporting

Go implementation of the Business API, built one Application Lifecycle state
at a time. Currently covers **Draft**, **Validated**, **Build**,
**Deploying**, **Scale-to-Zero** (the ongoing behavior of a `Running`
application), **Suspend/Resume/Restart**, **Rollback**, **Archive/Delete**
— every Application Lifecycle state reachable without Modules N/O/P
(Database/Secret/Domain Management), which don't exist yet — plus
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
Suspend/Resume/Restart and Archive/Delete); `FR-095`, `FR-098`, `FR-100`,
`FR-101` (Module V — Rollback — see below); `FR-103`, `FR-104`,
`FR-105`, `FR-106` (Module W — Audit Log — see below); `FR-107`,
`FR-108` (Module X — Notification — see below); `FR-016`, `FR-017`
(Module E — Co-Owner/Contributor Management and Transfer Ownership — see
below); and `FR-127`, `FR-128` (Module AB — Reporting — see below). See
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
| `POST /applications/{id}/delete` | FR-050, FR-065 | `archived`/`suspended` → `deleted` (terminal); requires `{"confirm": true}`. Deprovisions the application's database, if it has one, and deletes its secrets, before the status moves. Owner-only |
| `GET /applications/{id}/secrets` | FR-066, FR-070 | Names, versions and who last set each secret — never a value. Includes platform-managed ones (the database password). Owner-only — see **How Secret Management works** |
| `PUT /applications/{id}/secrets/{name}` | FR-066 | Sets or replaces a secret from `{"value": "..."}`. Write-only: the response is metadata, and no endpoint ever returns a value. Takes effect at the application's next container start. Owner-only |
| `DELETE /applications/{id}/secrets/{name}` | FR-066 | Removes an owner-set secret; a platform-managed one is refused with `409`. Owner-only |
| `GET /audit-log` | FR-104 | Filter by `actor_user_id`/`resource_type`/`resource_id`/`action`/`from`/`to`/`limit`; scoped to entries the caller performed or that concern an application they own |
| `GET /audit-log/export` | FR-105 | Same filters, CSV response; the export itself is recorded as a new audit entry |
| `GET /audit-log/integrity` | FR-106 | Recomputes the hash chain end-to-end; reports the first `seq` where it breaks, if any |
| `GET /notifications` | FR-107, FR-108 | The caller's own notifications, newest first; `?unread_only=true` filters to unread |
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
| `nodejs` | `package.json` with a `start` script | No — node/npm are needed at runtime, not just to build |
| `python` | `requirements.txt` (optional) + `app.py` | No — same reasoning as nodejs |
| `react` | `package.json` with a `build` script, output in `build/` (Create React App convention) | Yes — final stage drops the project's `node_modules`/devDependencies, keeps only the static output + a fresh `serve` install |
| `vue` | `package.json` with a `build` script, output in `dist/` (Vite convention) | Yes — same as react |
| `nextjs` | `package.json` with a `build` script, served via `next start` | No — a properly optimized build needs Next's "standalone output" mode, not implemented here (documented gap, not silent) |

**Why `go` needed fixing to multi-stage — found for real, not assumed:**
a single-stage `golang:*-alpine` build leaves the entire Go SDK/toolchain in
the final runtime image. When the Image Scan gate (below) was exercised for
real, Trivy correctly flagged 22 CRITICAL findings on the toolchain binaries
themselves (`usr/local/go/bin/go`, `.../pkg/tool/...`) — real CVEs, just
irrelevant ones, since nothing in the runtime image should ever execute the
Go compiler. Multi-stage (copy only the compiled binary into a minimal final
image) fixed this and dropped image size from 338MB to ~24MB. The governed
base_images catalog (FR-037) still only names the BUILD-stage image per
runtime; the fixed minimal runtime-stage image for `go`/`react`/`vue` is a
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
   `deployment_approvals` row; `POST /deployments/{id}/approve` with
   `{"decision": "approve"|"reject", "reason": "..."}` resumes or rejects it.
   **Known gap, documented not hidden:** this does not require the approver
   to be a different person than the requester — genuinely guaranteeing an
   independent approver needs the RBAC/Platform Administrator role modeling
   Module A/B doesn't have yet (blocked on `DEC-001`/`DEC-002`). Requiring a
   *different* owner today would make single-owner applications undeployable
   to production, a worse outcome than an honest gap.
3. **Deploy** — `internal/runtimeengine` (real Docker Engine API, a stand-in
   for the eventual K3s+Knative Runtime Platform per `DEC-004` — same shape,
   swappable later per `NFR-046`) starts one container per service, port
   published dynamically.
4. **Health Check** — polls the container until it responds or a 15s
   timeout elapses (simplified stand-in for Module R, not yet its own
   module). Failure here stops the containers just started for *this*
   attempt only.
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
Full continuous post-activation health monitoring triggering an *automatic*
rollback of an already-live version (FR-099, Module V) is out of scope — it
needs ongoing background monitoring infrastructure this
synchronous-per-request pipeline doesn't have. The deliberate,
requester-initiated rollback path (FR-098) is implemented — see **How
Rollback works** below.

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

**Known gap, documented not hidden:** FR-047 names a Security Administrator
force-suspend path (bypassing the owner-initiated flow, e.g. on a policy
violation) as an alternative flow. There's no distinct Security
Administrator role to check yet — same recurring RBAC gap as the
production approval gate, blocked on `DEC-001`/`DEC-002`. Only
owner-initiated suspend is implemented.

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
  needs policy-tier modeling this platform doesn't have (same RBAC/policy
  gap as the approval gate's approver-independence gap, blocked on
  `DEC-001`/`DEC-002`).
- **FR-099** (fully *automatic* rollback triggered by a post-activation
  health regression) isn't wired to any trigger — that needs continuous
  runtime health monitoring (Module T), not built. What's already true
  without any Module V code at all: FR-044's existing pre-activation failure
  handling means a failed forward-deploy attempt never touches an
  already-`running` previous version in the first place (see **How Deploy
  works**). FR-098 (this feature) covers the deliberate,
  requester-initiated path; FR-099's fully-automatic trigger remains a
  documented gap.
- FR-102 (rollback notification) is now partially real: since `Rollback`
  shares `deployAndActivate`/`markDeploymentFailedFrom` with a forward
  deploy (see above), it automatically triggers the same Module X
  notification on both success and failure — an owner genuinely gets
  notified. What's missing is FR-102's specific content requirements: the
  notification text is the generic "deployment succeeded/failed" wording,
  not rollback-specific ("rolled back to version X", trigger reason, prior
  vs. new active version) — see **How Notifications work** below.

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
- **Delete (FR-050)** is callable from `Archived` or `Suspended` only —
  FR-050's precondition explicitly excludes deleting directly from `Running`
  ("requires an explicit stop-first confirmation"), implemented by requiring
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

**Known gaps, documented not hidden — this is the module where "not built
yet" is most visible, because FR-050 in particular is *mostly* about
deprovisioning resources that don't exist yet:**
- Domain release (Module P) is still a no-op on both Archive and Delete —
  that module isn't built. Databases and secrets are no longer in that
  list: Delete really does tear down the application's database (see
  **How Database Management works**) and purge its secrets (see **How
  Secret Management works**). Archive does neither, by design — it
  retains configuration (FR-049). What Delete DOES do for real:
  guarantees no container, database instance or stored secret is left
  for the application.
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
doesn't exist (same `DEC-001`/`DEC-002` RBAC gap as everywhere else in this
platform) — `AuditService.Query` substitutes the same owner-based scoping
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
gap — it needs a Security Administrator role this platform doesn't have
(`DEC-002`) and a proactive detection sweep (e.g. periodically re-running
Module W's `VerifyChain`) this platform doesn't run in the background,
unlike the scale-to-zero sweeper.

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
Each check is `passed`, `failed`, or **`skipped`** — `resource_quota` is
always `skipped` because Module M (Resource Manager) doesn't exist yet and
exact quota numbers are TBD (`DEC-014`). This mirrors the docs' rule of
never inventing a business decision: an honest "not implemented yet" beats a
fake pass.

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
the Platform Administrator role this platform doesn't have — blocked on
`DEC-002`, the same gap every other admin-only flow in this codebase
already documents.

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
employee declares `database: type: postgres` in `deployment.yaml` and
gets a database; there is no API to poke at it with.

What actually happens, on deploy:

1. **`EnsureProvisioned` (FR-061).** A private Docker network is created
   for the application, then a dedicated `postgres:16-alpine` container is
   started on it with a platform-generated password (`crypto/rand`,
   base64url so it can't corrupt a DSN). Idempotent: an application that
   already has a live database keeps it, so a redeploy never silently
   replaces the database and loses its data. A declared type other than
   `postgres` is rejected before anything touches the runtime.
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
- **`FR-064` (Backup Scheduling) is not implemented.** It needs a
  scheduler this platform doesn't have and a frequency/retention policy
  the requirement itself marks TBD. Inventing one would be inventing a
  business decision.
- **`FR-065`'s "purge or retain-then-purge, per policy" is purge, full
  stop.** Retention would need the data-retention policy FR-065 defers to,
  which doesn't exist; a final backup would need FR-064, which isn't built.
- **`FR-062`'s detection half** ("a cross-application connection attempt is
  detected and logged as a policy violation") is not implemented — that
  needs Module Q's network policy layer. Only the *prevention* half is
  real, which is the half that stops the attempt succeeding.
- **Postgres only.** `supported_stacks` also lists `redis` under
  `cache`, and nothing provisions one.
- **The database container's data lives in the container**, not a named
  volume — it survives restarts and cold starts (verified), but not a
  `docker rm` of the database container itself.

### Verifying it

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

- **The key is not managed.** One static key from an environment
  variable: no rotation, no re-encryption tool, and anyone who can read
  platform-api's environment (`docker inspect` on its container) can read
  it. Changing it makes every stored secret unreadable — which fails
  closed, as above, but is still an outage for every application with a
  secret. Choosing a real backend is `DEC-006`, still Open;
  `internal/secretbox` is the seam it would replace.
- **Anyone with access to the Docker daemon can read injected values**
  from a running container's environment. That is inherent to FR-067's
  environment-variable delivery, and Docker daemon access is
  root-equivalent on the host anyway.
- **FR-068 (rotation) is partial.** Replacing a value bumps its version
  and reaches the application at its next container start — a Restart
  applies it immediately (verified) — but there is no overlap window, no
  scheduled rotation, and nothing invalidates the old value. Rotating a
  database password (`ALTER ROLE` plus re-injection) is not implemented.
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

## What's deliberately NOT here yet

Each will land as its own feature branch/PR, per the Application Lifecycle:

- Registry push — there's no real container registry yet (`DEC-005` is
  still Open); built images live in the local Docker daemon that both the
  Build Engine and Deployment step talk to. Fine for one-daemon local dev;
  won't work once the platform runs across more than one host.
- Real authentication — see **Dev-mode auth** below.
- Resource quota enforcement (FR-032) — depends on Module M, not built yet.
- Full RBAC / Role / Permission tables (Module A/B) — blocked on `DEC-001`.
  This is also why the production approval gate can't yet require an
  approver distinct from the requester — see **How Deploy works** above.
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
- Fully-automatic rollback triggered by a post-activation health regression
  (FR-099) — needs continuous background health monitoring (Module T) this
  request-scoped pipeline doesn't have. The deliberate, requester-initiated
  rollback path (FR-098) is implemented — see **How Rollback works** above.
- Image-scan severity threshold is hardcoded to "any CRITICAL blocks" —
  FR-041 says this should be Security Administrator policy; no such policy
  exists yet to read from (worth a `DEC-xxx` entry).
- Dual base-image governance (a BUILD-stage image and a separate
  RUNTIME-stage image per runtime, both IT-governed) — today only the
  build-stage image is catalog-driven; the minimal runtime-stage image for
  `go`/`react`/`vue` is a fixed platform constant (see **How Build works**).
- True horizontal scaling above 1 instance (FR-054's `scaling.max` ceiling)
  — see **How Scale-to-Zero works** above.
- Continuous post-activation health monitoring feeding scale decisions —
  the idle sweeper only ever *scales down*; nothing currently restarts a
  service that crashes on its own after activation (that's FR-099, already
  noted above, not duplicated here).
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
  — see **How Suspend/Resume/Restart works**'s known gap.
- Real deprovisioning on Archive/Delete for domains (Module P) — see
  **How Archive/Delete work**. Databases (Module N) and secrets (Module O)
  *are* removed for real. Every Application
  Lifecycle state reachable without those modules existing (`Draft` →
  `Validated` → `Build` → `Deploying`/`Running`, `Suspended`, `Rolled Back`
  as a transient step back to `Running`, `Archived`, `Deleted`) is now
  implemented.
- Un-archive / reactivation of an `Archived` application — see **How
  Archive/Delete work**'s known gap.
- Production de-registration approval gate (FR-050, "mirrors FR-014") —
  needs Module C, not built; same category of gap as the production-deploy
  approver-independence limitation.

## Dev-mode auth (temporary — see DEC-001)

There is no Identity Provider integration yet (`DEC-001` in
[`17_Decision_Log.md`](../../docs/17_Decision_Log.md) is still **Open**). All
`/applications` routes require these headers, and the service refuses to
start this path at all unless `PLATFORM_ENV=dev`:

```
X-Dev-User-Email: alice@example.com
X-Dev-User-Name:  Alice Employee      # optional
X-Dev-Department: Engineering         # optional, defaults to "Unassigned"
```

The user/department are upserted on first use. This entire mechanism
(`internal/httpapi/devauth.go`) sits behind the `Authenticator` interface so
it can be swapped for real SSO without touching any handler — see `NFR-051`.

## Running locally

From the repo root, first time only:

```
cp .env.example .env   # then edit POSTGRES_PASSWORD if you want a non-default value
openssl rand -base64 32   # required: paste the output into .env as PLATFORM_SECRET_KEY
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
curl -X POST localhost:8080/applications \
  -H "X-Dev-User-Email: alice@example.com" \
  -H "Content-Type: application/json" \
  -d '{"name":"overtime","description":"HR overtime tracker","owning_department_id":"<department-uuid>"}'
```

(`owning_department_id` must be a real department UUID — dev-mode auth
auto-creates one from `X-Dev-Department` on first request; fetch it from the
`departments` table, or extend this flow with a `GET /departments` endpoint
in a future state.)

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
