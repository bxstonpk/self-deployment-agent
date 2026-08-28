# Platform API — Draft State (State 1)

Go implementation of the Business API's Application Registration & Ownership
slice, covering the **Draft** state of the Application Lifecycle only.

Implements: `FR-011`, `FR-012`, `FR-013`, `FR-015` from
[`../../docs/02_Functional_Requirements.md`](../../docs/02_Functional_Requirements.md)
(Modules D and E). See
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

## What's deliberately NOT here yet

Each will land as its own feature branch/PR, per the Application Lifecycle:

- Validation Engine (`Validated` state), Build Engine (`Build` state),
  Deployment Controller integration (`Deploying`/`Running` states).
- Real authentication — see **Dev-mode auth** below.
- Audit logging (Module W) — `FR-013` calls for audit entries on metadata
  edits; not implemented until the Audit module exists.
- Full RBAC / Role / Permission tables (Module A/B) — blocked on `DEC-001`.

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

From the repo root:

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

## Running tests

```
cd services/platform-api
go test ./...
```

Service-layer tests (`internal/service`) use in-memory fakes and need no
database. There are no repository-layer (Postgres) tests yet — a future
increment should add them against a real Postgres instance (e.g. via
`docker compose` in CI), since the partial-unique-index and CHECK constraints
in the migration are part of the actual correctness guarantees.
