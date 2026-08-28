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
| `react` | Create React App convention assumed by the Build Engine (`npm run build`, output in `build/`) |
| `nextjs` | Served via `next start`; standalone-output optimization not implemented yet (larger runtime image than ideal) |
| `vue` | Vite convention assumed (`npm run build`, output in `dist/`) |

## Backend runtimes

| Runtime name (exact, case-sensitive) | Notes |
|---|---|
| `go` | **Not** `golang` — the catalog and Build Engine both use `go`. Requires `go.mod` at the service root. |
| `nodejs` | Requires `package.json` with a `start` script |
| `python` | Requires `app.py`; `requirements.txt` optional |

Backend-kind services **must** declare `port` in `deployment.yaml` — the
platform rejects a backend service definition with no port at validation
time. Frontend-kind services never need one.

## Database

| Type | Notes |
|---|---|
| `postgres` | The only supported `database.type` value currently |

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
