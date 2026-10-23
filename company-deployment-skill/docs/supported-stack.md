# Supported Stack — cached snapshot

**This is a cache, not a source of truth.** Always call `get_supported_stacks`
for the live answer before generating or validating a `deployment.yaml` — see
`SKILL.md`, Step 3. This file exists for fast offline reference only.

Snapshot taken from `services/platform-api`'s seed catalog
(`internal/db/migrations/0002_stack_catalog_and_validation.sql`) as of this
package's last update. If the live `get_supported_stacks` result disagrees
with anything below, the live result is correct and this file is stale.

## Frontend runtimes

| Runtime name (exact, case-sensitive) | Notes |
|---|---|
| `react` | `npm run build` must produce `build/` (Create React App's default; with Vite set `build.outDir: 'build'`). The app is served under `/run/{app}/{service}/`, so assets must use relative paths (Vite `base: './'`) — see `templates/frontend-react/` |
| `nextjs` | Served via its `start` script (`next start`, run without npm); standalone-output optimization not implemented yet (larger runtime image than ideal) |
| `vue` | Vite convention assumed (`npm run build`, output in `dist/`), served by nginx; use relative asset paths like `react` |

## Backend runtimes

| Runtime name (exact, case-sensitive) | Notes |
|---|---|
| `go` | **Not** `golang` — the catalog and Build Engine both use `go`. Requires `go.mod` at the service root. |
| `nodejs` | JavaScript. Requires `package.json` with a `start` script. The script runs without npm (runtime images carry no package manager), so it must not call `npm` itself |
| `typescript` | Requires `package.json` with a `build` script (e.g. `tsc`) and a `start` script that runs the compiled output (e.g. `node dist/server.js`); devDependencies are pruned after the build, and `start` runs without npm like `nodejs` |
| `python` | Requires `app.py`; `requirements.txt` optional |

Backend-kind services **must** declare `port` in `deployment.yaml` — the
platform rejects a backend service definition with no port at validation
time. Frontend-kind services never need one. Every service is started with
`PORT` set to the port the platform routes to.

## Database

| Type | Notes |
|---|---|
| `mysql` | MySQL 8.4. The company LAN standard (see `templates/`) |
| `postgres` | PostgreSQL 16 |

Either one gets the application its own container on its own private
network, a platform-generated password, and these environment variables:
`DATABASE_URL` (`mysql://…` or `postgres://…`), `DATABASE_ENGINE`,
`DATABASE_HOST`, `DATABASE_PORT`, `DATABASE_NAME`, `DATABASE_USER`,
`DATABASE_PASSWORD`.

## Cache

| Type | Notes |
|---|---|
| `redis` | Declared but not yet wired into `deployment.yaml`'s schema/validation as of this writing — treat as reserved, not yet usable; confirm against the live `get_supported_stacks(category="cache")` result before relying on it |

## What determines "supported"

A runtime/database name is supported if and only if it's `active` in the
live catalog — `deprecated` and `blocked` entries exist in the data model
but are never returned as usable by `get_supported_stacks` or accepted by
`validate_application`. There is no separate "documentation-only" list this
platform maintains — what `get_supported_stacks` returns **is** what
`validate_application` enforces, by construction (they both read the same
catalog table).

## Version ranges

`get_supported_stacks`' response includes a `version_range` field per
entry — as of this writing it is always `null`. The catalog tracks
supported runtimes by name only, not by version range (e.g. it can't yet
express "Node 18–20 supported, Node 16 deprecated"). Don't infer a version
constraint that isn't actually enforced.
