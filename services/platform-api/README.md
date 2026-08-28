# Platform API — Draft, Validated & Build States (State 1–3)

Go implementation of the Business API, built one Application Lifecycle state
at a time. Currently covers **Draft**, **Validated**, and **Build**.

Implements from
[`../../docs/02_Functional_Requirements.md`](../../docs/02_Functional_Requirements.md):
`FR-011`, `FR-012`, `FR-013`, `FR-015` (Modules D/E — Draft state);
`FR-019`, `FR-021`, `FR-023`, `FR-024`, `FR-029`, `FR-030`, `FR-031`(partial),
`FR-033`, `FR-034` (Modules F/G/H — Validated state; `FR-032` resource quota
is honestly reported as **skipped**, not faked — see below); and
`FR-035`, `FR-036`, `FR-037`, `FR-038` (Module I — Build state, real Docker
builds, not mocked). See
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

| Runtime | Convention |
|---|---|
| `go` | `go.mod` at the service root, buildable via `go build .` |
| `nodejs` | `package.json` with a `start` script |
| `python` | `requirements.txt` (optional) + `app.py` |
| `react` | `package.json` with a `build` script, output in `build/` (Create React App convention) |
| `vue` | `package.json` with a `build` script, output in `dist/` (Vite convention) |
| `nextjs` | `package.json` with a `build` script, served via `next start` |

A failed build is a normal, fully-reported outcome (FR-038), not an HTTP
error — the response's `error_category` is `source` (compiler/dependency
error — the employee/agent's problem to fix) or `platform` (build
infrastructure problem — not their fault), and `error_detail` includes the
actual tail of the build log, not just Docker's generic "non-zero exit
code" summary.

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

- Image Scan gate, Deployment Controller integration, Registry push
  (`Deploying`/`Running` states) — built images currently stay local to the
  Docker daemon the platform-api container talks to.
- Real authentication — see **Dev-mode auth** below.
- Resource quota enforcement (FR-032) — depends on Module M, not built yet.
- Audit logging (Module W) — several FRs call for audit entries; not
  implemented until the Audit module exists.
- Full RBAC / Role / Permission tables (Module A/B) — blocked on `DEC-001`.
- Stack version governance (FR-022, deprecated/blocked versions) — the
  catalog only tracks active/deprecated/blocked per whole runtime name, not
  per version range yet.
- Retry path out of `failed` — see **Known gap** above.
- Build status streaming/notifications (FR-036 alternative flow) — status is
  poll-only (`GET .../builds/latest`) for now; the build itself also runs
  synchronously within the triggering request rather than being queued
  asynchronously, which is fine for small internal-tool builds but won't
  scale to slow builds without a background job/worker model.

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

## Running tests

```
cd services/platform-api
go test ./...
```

Service-layer tests (`internal/service`) use in-memory fakes (including a
fake `BuildEngine` for the build tests — no Docker needed) and need no
database. `internal/buildengine` has real unit tests for the pure logic
(tar extraction/injection, Dockerfile templates, build-output-stream
parsing) that also don't need Docker. There are no repository-layer
(Postgres) tests yet — a future increment should add them against a real
Postgres instance (e.g. via `docker compose` in CI), since the
partial-unique-index and CHECK constraints in the migration are part of the
actual correctness guarantees.

All three states have also been manually verified end-to-end against a real
Postgres instance (and, for Build, a real Docker daemon — not mocked) via
`docker compose up --build` — see the PR descriptions for the exact `curl`
sessions exercised. The Build state's manual verification is worth calling
out specifically: a real minimal Go HTTP server was built through the full
pipeline, the resulting image was run standalone and returned the expected
response, and both failure categories were exercised for real (a Docker
socket permission error → `platform`; a genuine Go syntax error, with the
actual compiler output surfaced → `source`).
