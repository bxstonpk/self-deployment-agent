# Admin Portal

React + TypeScript frontend for the Company Deployment Platform, calling
[`../../services/platform-api`](../../services/platform-api) directly (not
through the MCP server — this is the human-operated path, per
[`../../docs/06_System_Requirements.md`](../../docs/06_System_Requirements.md)'s
architecture: MOD-16 (MCP) and this app are the platform's only two entry
points, and both route exclusively through MOD-17 (Platform API), so no
business logic is duplicated between the AI-agent path and the human path).

## Scope: Application Catalog (MOD-19), not the full Administration Portal (MOD-18)

The confirmed tech stack names this deliverable "Frontend Admin Portal
(MOD-18)." This app deliberately does **not** build MOD-18 as specified —
MOD-18 is the privileged surface for IT/Platform/Security Administrators to
manage roles, policy, quotas, and approval workflow configuration, and none
of that has a real backend yet (no distinct administrator roles exist
anywhere in `platform-api` — every authorization check today is "are you an
owner of this application," full stop; see `platform-api`'s own README).
Building MOD-18's actual screens now would mean shipping buttons that call
nothing real.

Instead, this app is shaped like **MOD-19 (Application Catalog)** — the
self-service, human-readable directory of applications and their status —
extended with the real, working lifecycle actions the Platform API already
supports (validate, build, deploy, suspend, resume, restart, roll back,
archive, delete). Every screen and every button in this app calls a real,
already-verified Platform API endpoint. Nothing here is a placeholder for a
capability that doesn't exist — see `platform-api`'s and `mcp-server`'s own
READMEs for the honest gap lists of what those services don't do yet; this
app doesn't pretend to offer anything they can't actually deliver.

## What's implemented

- **Sign in** (`src/pages/SignIn.tsx`) — dev-mode identity (email, optional
  name/department), stored in `localStorage`, sent as
  `X-Dev-User-Email`/`X-Dev-User-Name`/`X-Dev-Department` on every request —
  the exact same headers `platform-api`'s own `DevHeaderAuthenticator`
  trusts (`internal/httpapi/devauth.go`). Not a security boundary; see
  **Known gaps**.
- **Application list** (`ApplicationList.tsx`) — every registered
  application, with client-side search-by-name and filter-by-lifecycle-status.
- **Register application** (`RegisterApplication.tsx`) — name/description/
  department form; department is a live dropdown from `GET /departments`
  (added to `platform-api` specifically for `mcp-server`'s
  `create_application` tool — this app is a second real consumer of that
  same endpoint, not a one-off).
- **Application detail** (`ApplicationDetail.tsx`) — the main working
  surface:
  - `deployment.yaml` editor (a plain textarea — see **Known gaps** for
    why not a real YAML/schema editor yet) with **Save draft** and
    **Validate**, showing the real `validate_application` findings.
  - **Build**: a native file picker uploading the source archive as raw
    bytes to `POST .../build` — the browser's own file-upload UI is a
    genuinely better fit for this than `mcp-server`'s base64-JSON
    workaround (a human already has the file; an AI agent doesn't have
    hands).
  - **Deploy**: environment selector + button, calling the real
    (synchronous, per `platform-api`'s own documented behavior)
    `POST .../deploy`.
  - **Lifecycle actions**: Suspend / Resume / Restart / Archive / Delete,
    each only enabled when the application's current `lifecycle_status`
    actually permits it (mirrors the Go service layer's own preconditions
    — see below) rather than showing a button that would just 409.
  - **Deployment history** with a **Roll back to this** action per prior
    successful entry — calls the real `rollback_application` endpoint with
    that row's own deployment id, not a guess.
  - **Scale events** table (`GET .../scale-events`).

## How button-enablement mirrors the real service preconditions

Rather than showing every action always and letting the server reject most
of them, this app derives which buttons are enabled directly from
`lifecycle_status`, matching the exact preconditions each Go service method
enforces (checked against source, not assumed):

| Action | Enabled when `lifecycle_status` is | Matches |
|---|---|---|
| Validate | `draft` | `validation_service.go`'s `Validate` |
| Build | `validated`, `running`, or `failed` | `build_service.go`'s `TriggerBuild` (widened in a prior PR to allow a rebuild of an already-live application) |
| Deploy | `running`, `build`, or `failed` | `deploy_service.go`'s `InitiateDeploy` (a build must exist; the button itself doesn't re-derive that — a deploy attempt with no build still 409s with a clear message, surfaced via the error banner) |
| Suspend | `running` | `lifecycle_service.go`'s `Suspend` |
| Resume | `suspended` | `lifecycle_service.go`'s `Resume` |
| Restart | `running` | `lifecycle_service.go`'s `Restart` |
| Archive | `running` or `suspended` | `lifecycle_service.go`'s `Archive` |
| Delete | `archived` or `suspended` | `lifecycle_service.go`'s `Delete` |

This is a convenience, not a security boundary — the Platform API
re-validates every precondition itself regardless of what this app shows;
a disabled button here is about not inviting a confusing 409, not about
enforcing anything.

## Known gaps (documented, not hidden)

- **Dev-mode identity only, not a real session.** Mirrors `platform-api`'s
  own `DEC-001`-blocked auth stub — any email works, nothing is actually
  authenticated. This app adds no security of its own; every real check
  still happens server-side.
- **No real RBAC-aware UI.** Every signed-in identity sees the exact same
  screens and the exact same enabled/disabled buttons — there's no
  Administrator-only view because there's no Administrator role to check
  server-side yet (see **Scope** above).
- **`deployment.yaml` is a plain textarea**, not a real YAML editor with
  syntax highlighting, inline schema validation, or autocomplete from
  `company-deployment-skill/schemas/deployment.schema.json`. The server-side
  `validate_application` result is still fully surfaced (findings list),
  just not pre-checked client-side the way the Skill package's local
  schema check does for an AI agent.
- **Delete confirmation is a native `window.confirm`**, not a
  type-the-application-name-to-confirm pattern like `mcp-server`'s
  `delete_application` tool implements. `platform-api`'s own
  `confirm: true` boolean is still what actually gates the irreversible
  action server-side; this is a materially weaker confirmation UX than the
  MCP path has, worth tightening in a follow-up.
- **No log/metric views** — `platform-api` doesn't have Logging or
  Monitoring modules to show anything from (same gap `mcp-server`'s
  `get_application_logs`/`get_application_metrics` document).
- **No pagination** on the application list — `GET /applications` supports
  `limit`/`offset` server-side (defaults to 20 with no cap requested), but
  this app always requests the default page and doesn't yet expose paging
  controls. Fine at today's data volumes, a real gap at scale.
- **`Restart`'s precondition is looser here than actual Go behavior in one
  edge case**: the button enables on any `running` application, but the
  service itself may still reject via `ErrApplicationNotRunning` if the
  underlying deployment record isn't `DeploymentRunning` (a narrow,
  transient window) — surfaced correctly via the error banner if hit, just
  not pre-filtered by this app's simpler `lifecycle_status`-only check.

## Real port-collision note (found while verifying, not hypothetical)

`CORS_ALLOWED_ORIGINS` on the Platform API defaults to
`http://localhost:5173` (Vite's default port), and this app's own
`.env.example` assumes the same. On a machine already running other
projects' dev servers on `5173`/`5174`, Vite silently falls back to the
next free port (`5175`, `5176`, …) — confirmed for real during this app's
own verification. If your browser can't reach the API (CORS errors in the
console) after `npm run dev`, check which port Vite actually printed and
add it to the Platform API's `CORS_ALLOWED_ORIGINS` (comma-separated) or
free up `5173`.

## Running locally

From the repo root, start the Platform API first (see
[`../../services/platform-api/README.md`](../../services/platform-api/README.md)):

```
cp .env.example .env
docker compose up --build
```

Then, in this directory:

```
npm install
cp .env.example .env.local
npm run dev
```

Sign in with any email at the prompt — the Platform API creates the
user/department on first use, exactly like every other dev-mode client in
this project.

## Running tests

```
npm run build   # tsc -b && vite build — type-checks and bundles
npm run lint    # oxlint
npm run test    # vitest run — component tests, jsdom, mocked fetch
```

**A real environment issue found while setting this up, not a code bug**:
Vitest's default `forks` worker pool fails to spawn at all in this sandbox
(`Failed to start forks worker`, `Timeout waiting for worker to respond`) —
the run reports `exit code 0` with **zero tests actually executed**, which
looks like a pass at a glance if you only check the exit code. Fixed by
setting `pool: 'threads'` in `vite.config.ts`'s `test` block (uses
`worker_threads` instead of `child_process`) — all 15 tests then run and
pass. Worth knowing if you hit the same silent-zero-tests result elsewhere.

Component tests (`src/**/*.test.tsx`) use `@testing-library/react` +
`vitest` + `jsdom`, with `fetch` mocked to return response bodies shaped
exactly like `platform-api`'s real JSON (including its two PascalCase
endpoints, `/departments` and `/supported-stacks` — see `api/types.ts`'s
module doc for why those two are inconsistent with the rest of the API,
and `api/client.ts`'s normalizers). No network or Docker involved — the
HTTP contract itself was verified separately, for real, against a running
Platform API (see below).

### What was verified for real (not just against mocks or by reading code)

- `npm run build` — real `tsc` type-checking + a real Vite production
  build, zero errors.
- **CORS wiring** — brought up the real Platform API (`docker compose`)
  and used `curl` with an `Origin: http://localhost:5173` header to
  confirm: a preflight `OPTIONS` request gets the correct
  `Access-Control-Allow-*` headers back; an actual `GET` from an allowed
  origin gets `Access-Control-Allow-Origin` in its response; the same
  request with a disallowed origin (`http://evil.example.com`) gets no
  CORS header at all (what actually makes a real browser refuse to expose
  the response to this app's JS).
- **The dev server itself** — ran `npm run dev` for real, confirmed it
  serves this app's actual `index.html` (checked the page `<title>` and
  that `/src/main.tsx` is served as `text/javascript`) rather than
  silently failing or serving a stale/wrong build — this is also what
  surfaced the port-collision note above.
- **A real headless-Chromium click-through**, driven with Playwright
  (this environment doesn't have a `chromium-cli` install, so the `run`
  skill's documented fallback — drive `playwright`'s `chromium` module
  directly, `args: ['--no-sandbox']` — was used instead) against the real
  dev server and a real running Platform API: signed in, landed on
  `/applications`, registered a real application (department dropdown
  populated live from `GET /departments`), landed on its detail page,
  typed a real `deployment.yaml` into the editor, saved it, clicked
  **Validate**, and confirmed the real `validate_application` findings
  rendered (`schema`/`stack_compliance` passed, `resource_quota` skipped
  with its real explanatory text) with the status badge flipping from
  `draft` to `validated`. Screenshots taken at every step and actually
  looked at, not just captured — the sign-in screen, the populated
  register form, and the validated detail page all render cleanly with
  no layout breakage. `console --errors`-equivalent checked throughout
  (every `console.error`/`pageerror` collected across the whole run).

This is what found two real bugs no other layer of testing caught:

1. **The application-name `pattern` attribute threw a real browser
   exception.** `RegisterApplication.tsx`'s `<input pattern="[a-z]([a-z0-9-]{0,61}[a-z0-9])?">`
   compiled to an invalid regular expression under Chrome's newer
   Unicode-mode (`v`-flag) character-class parsing —
   `Uncaught SyntaxError: Invalid regular expression: ... Invalid character
   class` — which `jsdom` (what the component tests run under) doesn't
   reproduce at all, so 15 passing component tests gave no signal on this
   whatsoever. Fixed by escaping the hyphen (`[a-z0-9\-]`).
2. **A registered application's detail page fired three-to-four API calls
   guaranteed to 404** (`GET .../deployments/latest`, `.../builds/latest`,
   `.../scale-events`) immediately after registration and again after
   validation — because a `draft`/`validated` application can never have a
   deployment, build, or scale event yet (verified against every Go
   service's actual `UpdateLifecycleStatus` call site: `validated` is
   *only* ever reached from `draft`, nowhere else — grepped for real, not
   assumed). The app already handled the resulting `NOT_FOUND` gracefully
   (correct empty states), but a real browser's console logs every failed
   network request regardless of whether the rejection is caught — a
   `jsdom`+mocked-`fetch` test can't surface this kind of console noise
   either, since there's no real network layer generating it. Fixed by
   skipping those four calls entirely while `lifecycle_status` is `draft`
   or `validated`.

### What's still owed — not everything was re-verified after this pass

The click-through above covers **register → save → validate** end to end
for real. It does *not* yet cover build (needs a real source archive
upload through the file picker), deploy, or any of the lifecycle actions
(suspend/resume/restart/archive/delete/rollback) driven through the actual
UI — those were verified at the Platform API layer directly in prior PRs
(`curl`, the MCP path) and are exercised by this app's component tests
with mocked responses, but not yet clicked through in a real browser
against a real backend the way the register/validate flow now has been.
