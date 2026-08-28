# Platform API — Draft through Suspend/Resume/Restart (State 1–6)

Go implementation of the Business API, built one Application Lifecycle state
at a time. Currently covers **Draft**, **Validated**, **Build**,
**Deploying**, **Scale-to-Zero** (the ongoing behavior of a `Running`
application), and **Suspend/Resume/Restart**.

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
stable proxy URL, real event logging); and `FR-047`, `FR-048` (Module K —
Suspend/Resume/Restart — see below). See
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
| `PUT /applications/{id}/deployment-yaml` | FR-023 | Saves a `deployment.yaml` draft (must parse as YAML); reverts `validated` back to `draft` since the contract changed; owner-only |
| `POST /applications/{id}/validate` | FR-029–034 | Runs the aggregate validation pass; `draft` → `validated` on success. Only callable from `draft`. Owner-only |
| `GET /supported-stacks` | FR-019 | Lists the IT-governed Supported Stack catalog (seeded by migration `0002`) |
| `POST /applications/{id}/build` | FR-035–038 | Triggers a real Docker build from an uploaded source archive; `validated` → `build` (success) or `failed`. Only callable from `validated`. Owner-only |
| `GET /applications/{id}/builds/latest` | FR-036 | Queryable build status |
| `POST /applications/{id}/deploy` | FR-039–044 | Runs the deploy pipeline (Image Scan → [prod approval gate] → Deploy → Health Check → Traffic Activation). Only callable with a successful build. Owner-only |
| `GET /applications/{id}/deployments/latest` | FR-043 | Queryable deployment status |
| `POST /deployments/{deploymentId}/approve` | FR-042 | Approve/reject a `pending_approval` production deployment. Owner-only |
| `GET /applications/{id}/scale-events` | FR-056 | Every scale-up (incl. cold-start) / scale-down (incl. scale-to-zero) event for the app's current deployment |
| `ANY /run/{appName}/{serviceName}/*` | FR-051–053 | **The public, stable URL for a deployed application.** Not behind platform auth — see below |
| `POST /applications/{id}/suspend` | FR-047 | `running` → `suspended`: stops every container (not just scale-to-zero-eligible ones), retains config. Owner-only |
| `POST /applications/{id}/resume` | FR-048 | `suspended` → `running`: restarts non-eligible services immediately; eligible ones stay at zero and cold-start on demand as usual. Owner-only |
| `POST /applications/{id}/restart` | FR-048 | Recycles currently-running instances in place — same version, fresh containers, no redeploy. Owner-only |

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
rollback of an already-live version (the other half of FR-044) is out of
scope — it needs ongoing background monitoring infrastructure this
synchronous-per-request pipeline doesn't have.

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

### Known gap: no retry path out of Failed yet

A failed build moves the application to the `failed` lifecycle state. There
is currently no endpoint to get it back to `draft`/`validated` for a retry —
`PUT .../deployment-yaml` only accepts `draft`/`validated` as source states.
FR-038's alternative flow ("Claude Code parses the failure and attempts a
fix before resubmitting") implies this should exist; it's an honest gap for
a future increment (likely part of a fuller Module K lifecycle-transition
implementation), not something silently worked around here.

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

## What's deliberately NOT here yet

Each will land as its own feature branch/PR, per the Application Lifecycle:

- Registry push — there's no real container registry yet (`DEC-005` is
  still Open); built images live in the local Docker daemon that both the
  Build Engine and Deployment step talk to. Fine for one-daemon local dev;
  won't work once the platform runs across more than one host.
- Real authentication — see **Dev-mode auth** below.
- Resource quota enforcement (FR-032) — depends on Module M, not built yet.
- Audit logging (Module W) — several FRs call for audit entries; not
  implemented until the Audit module exists.
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
- Continuous post-activation health monitoring / automatic rollback of an
  already-`running` version (the second half of FR-044) — needs background
  monitoring infrastructure this request-scoped pipeline doesn't have.
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
  service that crashes on its own after activation (that's the other half
  of FR-044, already noted above, not duplicated here).
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
- `Archived` and `Deleted` lifecycle states (FR-049, FR-050) — both need
  Modules N/O/P (Database/Secret/Domain Management) to actually
  deprovision anything, none of which exist yet. Only the four states
  reachable without those modules (`Draft` → `Validated` → `Build` →
  `Deploying`/`Running`, plus `Suspended`) are implemented.
- Application Owner transfer, co-owner management (FR-016, FR-017) — the
  data model (`application_owners`, multiple rows per app) already
  supports it; no endpoints exist yet.

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
```

`.env` is git-ignored — `docker-compose.yml` reads all credentials from it
and refuses to start with a clear error if it's missing (see
`.env.example` for what's needed). Then:

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

All six states have also been manually verified end-to-end against a real
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
