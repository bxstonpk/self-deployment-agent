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
  - **Logs** (Module S) — what this application's containers printed,
    newest first, with a text filter, a service filter and **Load older
    lines** that follows the cursor the Platform API hands back. Each line
    says which stream it came from in words (`out`/`err`), never colour
    alone. Secret values the platform injected arrive already redacted
    (`[REDACTED:NAME]`): this app never has one to show. A non-owner is
    told the logs are owners-only — see **Logs UI** below for why that is
    what a `404` means on this page.
  - **Audit log** — this application's own entries (`GET /audit-log?resource_type=application&resource_id=...`),
    fetched unconditionally (unlike Build/Deploy/Scale events above, a
    `draft`/`validated` application already has real audit entries —
    registering and validating it are themselves audited actions).
  - **Secrets** (Module O) — each secret's name, whether an owner or the
    platform manages it, its version and when it was last set; never a
    value, because no `platform-api` endpoint returns one. **Save secret**
    sets or replaces one (write-only: the field clears the moment the save
    succeeds); **Delete** asks first, and appears only for owner-set
    secrets; **Rotate** appears only on the platform-managed database
    password (FR-068). Someone who isn't an owner is told the section is
    owners-only, rather than shown an empty list. See **Secrets UI
    (Module O), verified for real** below for the choices made about the
    value field.
- **Audit Log** (`AuditLog.tsx`, reachable from the header nav on every
  page) — the platform-wide view: filter by resource type/action, **Export
  CSV** (a real file download carrying the same identity headers as every
  other request — plain `<a href>` can't do that, so it fetches a `Blob`
  and triggers the save client-side), and **Verify chain integrity**
  (`GET /audit-log/integrity`), surfacing whether the hash chain is intact
  or, if not, the first broken `seq`.
- **Notifications** (`Notifications.tsx`, reachable from the header nav on
  every page, with an unread-count badge) — the caller's own deployment
  status / production-approval inbox, with an **Unread only** toggle and a
  **Mark read** action per row. The header badge is polled every 20s
  (`GET /notifications?unread_only=true`) — there's no websocket/SSE
  channel anywhere in this platform to push it live, and a 20s staleness
  window is a reasonable trade-off for an internal tool's header badge over
  building one just for this.
  - **Owners** (a new section on the application detail page) — grants
    co-owner/contributor access by email, and revokes it. This page can't
    tell client-side whether the signed-in identity is the application's
    *primary* owner (no endpoint resolves that without an extra
    round-trip), so the form is never hidden — a non-primary owner
    attempting it sees the real `403`/`not_primary_owner` via the same
    error banner every other action here uses. Granting access to an
    email the platform has never seen surfaces the real
    `target_user_unknown` error rather than silently doing nothing.
  - **Transfer primary ownership** (below the Owners table, same card) —
    nominate a new primary owner by email; if a transfer is already
    pending, the nominate form is replaced by an **Accept transfer**
    button instead (shown to everyone viewing the page, not just the
    nominee — same "don't hide, let the real error surface" convention;
    `platform-api`'s own `not_transfer_nominee` rejects anyone else).
- **Reports** (`Reports.tsx`, reachable from the header nav) — Module AB's
  two reports: deployment activity over a selectable date range (a
  three-tile KPI row of succeeded / failed / rolled-back totals, plus
  per-environment and per-department breakdown tables) and the application
  inventory (name, department, lifecycle status, stack, environment, owner
  count), each name linking through to its detail page.

  **Deliberately not charts.** Three headline numbers is a stat-tile row,
  not a three-bar chart, and two-to-three-row breakdowns are tables — at
  this size a charting dependency would be more machinery than the data
  justifies. Each tile carries its own text label, so the status color is
  never the only thing distinguishing them, and the values use the
  existing `--success`/`--danger`/`--accent` tokens (which already have
  validated dark-mode variants) rather than introducing a new palette.
  Rolled-back takes the informational accent, not danger red: it's a
  distinct outcome, not a failure.

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
| Delete | `archived` or `suspended` — or `draft`, `validated`, `build` or `failed` while nothing is running or in progress (no running or in-flight deployment in the history, no queued or running build) | `lifecycle_service.go`'s `Delete` and its `requireNothingLive` guard |
| Save secret | anything but `deleted` | `secret_service.go`'s `Set` (a deleted application's secrets were purged with it) |

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
- **No metric views** — metrics don't exist anywhere: there is no
  Monitoring module (the same gap `mcp-server`'s
  `get_application_metrics` documents). Logs do have a view now (see
  **Logs UI** below), with the limits that view carries: no live
  tail/follow, no download, and a text search only — the endpoint's
  `since`/`until` and `environment` filters aren't exposed here.
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
- **Audit Log's "Actor" column shows a raw user id**, not an email/name —
  `platform-api`'s audit entries only carry `actor_user_id` (a UUID), and
  no endpoint exists anywhere to resolve a user id back to a human-readable
  identity for display. Matches every other place in this app that shows a
  raw id today (e.g. `requested_by` on a deployment); worth a real user
  lookup once one exists, not something this page invents on its own.
- **A secret's value is visible on screen while it's being typed.** The
  field is a plain textarea (see **Secrets UI** below for why), which
  nothing masks; it's cleared the moment the save succeeds and never shown
  again. A show/hide toggle would be a reasonable follow-up.

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

That first pass found two real bugs no other layer of testing caught:

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

**A second, full-lifecycle click-through** then extended the same
Playwright driver past register/validate: uploaded a real `tar.gz` through
the actual file-picker input (a real `docker build` ran server-side),
clicked **Deploy**, confirmed the rendered live URL genuinely served the
deployed application's real HTTP response (fetched it directly, not just
checked that a link appeared), then clicked through **Restart** ->
**Suspend** (confirmed the live URL became genuinely unreachable —
connection-refused, the container was actually stopped, not just marked)
-> **Resume** (confirmed `running` again) -> **Archive** -> **Delete**
(confirmed the terminal `deleted` state, confirmed via `docker ps`/`docker
images` afterward that no container or leftover test image remained).
Every functional step passed — the whole lifecycle genuinely works through
the real UI, not just via `curl`/the MCP path.

That run surfaced one more instance of the same class of issue as bug #2
above: right after a **first-ever** build completes (a `draft`/`validated`
application transitioning through the transient `build` status on its way
to its first deploy), the detail page still fires the deployment/scale-event
fetches, which still 404 for the same "genuinely doesn't exist yet" reason
— two more benign console log lines. This wasn't fixed the same way,
deliberately: unlike `draft`/`validated`, the `build` status does **not**
universally mean "no deployment exists yet" — a *rebuild* of an
already-`running` application also passes through `build`, and that case
genuinely does have a prior deployment/scale-events worth fetching.
Skipping fetches whenever `lifecycle_status === "build"` would silently
break that case instead. Distinguishing "first build" from "rebuild"
would need the frontend to track more state than `lifecycle_status` alone
currently carries. Given the actual application behavior is already
correct in both cases (the right empty/populated state renders either
way) and the only remaining cost is two harmless devtools console lines
in one specific transient window, this was judged not worth the added
state-tracking complexity — noted here as a deliberate, considered
trade-off, not an unnoticed gap.

### What's still owed

Register -> validate -> build -> deploy -> restart -> suspend -> resume ->
archive -> delete have now all been driven through the real UI against a
real backend. **`rollback_application` has not** — exercising it through
this UI needs two real successful deployments to roll back between (a
"deploy a second version while the first is still running" scenario),
which the click-through above didn't set up. Rollback itself is already
verified at the Platform API and MCP layers in prior PRs; only the
UI-specific "click Roll back to this on a history row and watch traffic
actually flip" path remains unverified here.

### Audit Log UI (Module W), verified for real

A third Playwright pass exercised the new `AuditLog.tsx` page and
`ApplicationDetail.tsx`'s audit section against a real Platform API,
running through everything the new client functions
(`queryAuditLog`/`exportAuditLogCsv`/`verifyAuditLogIntegrity`) do: signed
in, registered and validated a real application, confirmed its own detail
page's **Audit log** section showed both the real `application.register`
and `application.validate` entries, followed the header's **Audit Log**
link to the platform-wide page and confirmed the same entries appeared
there, applied the action filter and confirmed it correctly narrowed to
just the matching entries, clicked **Verify chain integrity** and
confirmed it reported the real hash chain intact, and clicked **Export
CSV** — a genuine file download (`page.waitForEvent("download")`, not a
mocked click), saved it to disk, and confirmed the file actually contains
the real entries and their `entry_hash` column. Confirmed the export
itself then shows up as a new `audit_log.export` entry after refetching,
per FR-105's own main flow. Every check passed on the first real run —
screenshots taken and looked at, no console errors, no layout breakage. No
bugs found in the UI this time (the Module W backend itself did have one,
caught during its own PR's verification — see
`services/platform-api/README.md`'s "How Audit Logging works" section).

### Notifications UI (Module X), verified for real

A fourth Playwright pass exercised the new `Notifications.tsx` page and the
header's unread badge against a real Platform API: signed in, registered
and validated a real application, confirmed the header showed **no**
unread badge yet (register/validate aren't deployment-pipeline
milestones — see `services/platform-api/README.md`'s "How Notifications
work"), uploaded a real source archive (a real `docker build` ran
server-side) and deployed to `dev`, then confirmed the header's unread
badge genuinely reached `1` via its real 20s poll cycle (not a mocked
timer), followed it to the Notifications page and confirmed the real
`deployment_status` notification appeared, toggled **Unread only**,
clicked **Mark read**, and confirmed the notification correctly dropped
out of the unread-only view, then confirmed it still appeared once the
filter was cleared — with no **Mark read** button on an already-read row.
Confirmed the header badge itself cleared back to `0` within a poll cycle.
Finally, rebuilt and redeployed the same application to `production` and
confirmed a real `approval_request` notification appeared on the
Notifications page.

Every functional check passed. Two `console.error`-level 404s were
observed during the run (`GET .../deployments/latest`,
`GET .../scale-events`, both immediately after the *first-ever* build
completes) — confirmed via `page.on("response")` logging, not guessed.
This is **not a new bug**: it's the exact, already-documented,
deliberately-not-fixed gap from the first full-lifecycle verification pass
(see "That run surfaced one more instance..." above) — `lifecycle_status
=== "build"` isn't added to `ApplicationDetail.tsx`'s fetch-skip condition
because a *rebuild* of an already-`running` application also passes
through `build` and genuinely does have prior deployment/scale-event data
worth fetching; only a first-ever build doesn't. Re-confirming this gap is
still exactly where it was, not somewhere new, was itself part of what
this pass verified.

Two real bugs were found and fixed **in the Playwright driver script
itself**, not the application, while writing this verification — worth
noting since they'd cost real debugging time if hit again: (1) Playwright's
`waitForURL(/\/applications\/[^/]+$/)` resolves immediately if the
*current* URL already satisfies the pattern — `/applications/new` matches
that regex too, so capturing `page.url()` right after can read a stale
URL; fixed by waiting for a detail-page-only element (`.yaml-editor`)
instead. (2) A case-sensitive `innerText.includes("pending_approval")`
check missed the real status text, because `StatusBadge`'s CSS
`text-transform: capitalize` renders it as `Pending_approval` in
`innerText` — fixed with a case-insensitive check.

### Owners UI (Module E, FR-017), verified for real

A fifth Playwright pass exercised the new **Owners** section against a
real Platform API — the first of these passes needing **two** genuinely
independent signed-in identities at once, done with two separate
Playwright browser *contexts* (each with its own `localStorage`, exactly
like two different employees in two different browsers), not two tabs
sharing one session: signed in as Alice and, separately, as Bob (Bob's
sign-in alone is what provisions him server-side — a real employee has to
exist before anyone can grant them access). Alice registered a real
application and confirmed the Owners table showed her alone, as
`primary`, with no **Revoke** button next to her own row. Attempted to
grant access to an email that had never signed in and confirmed the real
`target_user_unknown` error surfaced in the UI, not a silent no-op.
Granted Bob co-owner (`secondary`) access, confirmed the table updated
with a real second row and a **Revoke** button next to it, and confirmed
the email field cleared — but only because the grant genuinely succeeded;
an earlier draft of this feature (before I noticed `runAction` swallows
every error to always resolve) would have cleared the field even on a
failed grant, so a dedicated `handleGrantOwner` was written instead of
reusing `runAction`. Then, in Bob's own browser context, navigated
directly to the same application's detail page and saved a real
`deployment.yaml` draft himself — genuine day-to-day access, granted
moments earlier, with zero code changes anywhere outside this one grant
endpoint (every other service's `requireOwner` already accepted any
active owner role). Back in Alice's context, clicked **Revoke**, confirmed
the button disappeared from Bob's now-revoked row, then — the important
check — reloaded Bob's page and had him attempt the same save again,
confirming a real `403`/`forbidden` came back immediately, not just that
the UI *looked* revoked.

Every functional check passed on the first real run. Two expected
`console.error`-level network failures were logged by Chrome during the
run (a `404` and a `403`) — both are the direct, correctly-handled
consequence of the two negative-path scenarios this same run deliberately
exercised (granting an unknown email; Bob acting after revocation), the
same "Chrome logs every failed request to the console regardless of
whether the app handles it" behavior noted in this file's very first
verification pass above, not a new finding.

### Ownership Transfer UI (Module E, FR-016), verified for real

A sixth Playwright pass — three genuinely independent signed-in
identities at once this time (Alice the primary owner, Bob the nominee,
Carol a genuinely uninvolved third party, each their own browser context)
— exercised the new **Transfer primary ownership** section. Registered an
application as Alice, confirmed the nominate form (not the accept button)
shows when nothing is pending, nominated Bob, confirmed the form was
immediately replaced by the pending-transfer view (a second nomination
isn't even reachable from this UI while one is outstanding — matching
`platform-api`'s own `transfer_already_pending` rejection), followed Bob
to his own Notifications page and confirmed a real notification arrived,
had Carol — on the same application's page, in her own browser context —
click **Accept transfer** and confirmed the real `not_transfer_nominee`
error surfaced, then had Bob actually accept it for real. Confirmed the
nominate form reappeared afterward (no more pending transfer), confirmed
the Owners table showed the real ownership change (two `primary` rows —
the prior owner `revoked`, the new one `active`), and confirmed Alice —
the now-former primary owner — attempting to nominate again got a real
`not_primary_owner` rejection, not a UI that merely looked locked out.

**A real bug found and fixed, in `platform-api` itself, not this app:**
the first full run of this driver logged **four** `404`s from Chrome, not
the deliberately-triggered kind — one on every single page load that
checked for a pending transfer, because `GET
/applications/{id}/ownership-transfer` originally 404'd whenever nothing
was pending. That's the *ordinary* state for most applications most of
the time, not an exceptional one — a poor fit for `404`, and a real
design mistake caught here by exercising the realistic, everyday path
(every page load), not just the edge cases. Fixed in `platform-api` to
always return `200` with `{"transfer": ...}` or `{"transfer": null}` (see
its own README's "How Ownership Transfer works" section for the full
reasoning), updated this app's `getPendingOwnershipTransfer` to match,
rebuilt the backend image, and re-ran the same driver end to end: the
`404`s were completely gone, leaving only the two expected `403`s from
Carol's and Alice's deliberately-triggered rejection scenarios above —
the same benign pattern this file's very first verification pass already
established.

### Reports UI (Module AB), verified for real

A seventh Playwright pass, this one **in both colour schemes** (two
browser contexts, `colorScheme: "light"` and `"dark"`), against a stack
seeded with genuinely real activity: a real application taken through
build → deploy → rebuild → redeploy → rollback, plus a second one left in
`draft`. Confirmed the stat tiles showed the real totals from the live
API (2 succeeded / 0 failed / 1 rolled back — the same numbers
`GET /reports/deployment-activity` returns), the environment and
department breakdowns rendered real rows (with the department's *name*,
not a raw UUID), the inventory read the stack (`go`) out of the real
`deployment.yaml` and marked the never-deployed application as such
rather than leaving a blank cell, and an inventory name linked through to
its detail page. Changing the date range genuinely re-queried the API (a
2020 window correctly reported zeros and surfaced the `available_from`
note). Zero console errors in either scheme.

**Two real bugs found, both by driving the page rather than by any
test:**

1. **Clearing a date input crashed the whole page.** An empty `<input
   type="date">` gives `""`, `new Date("T00:00:00")` is an Invalid Date,
   and `.toISOString()` on it *throws* — so clearing a date to retype it
   white-screened the component. The unit test written for the
   backwards-range guard below is what surfaced it, because clearing the
   field is how you type a new one.
2. **A half-edited range flashed a red error banner.** Editing the two
   dates in sequence necessarily passes through `from` being later than
   `to`, and that fired a real request that came back `400
   invalid_range` — an error banner shown to someone who was simply
   mid-edit. Both are now guarded client-side before the request is ever
   made, with a plain hint instead of an error; the API still validates
   the range itself regardless.

**And one layout defect only visible by looking at the render**, not
catchable by any assertion: the environment and department breakdown
tables sat directly on top of each other with no separation, reading as
one table with a stray repeated header row. Fixed with spacing between
them, then re-rendered and re-checked.

### Secrets UI (Module O), verified for real

An eighth Playwright pass, against a real application built and deployed
for it, from two browser contexts: the application's owner, and an
employee who isn't one. What it checked is what matters for a secret —
where the value goes, not just what the page shows:

- As the owner: a lowercase name is stopped by the browser's own
  `pattern` check with nothing sent; a reserved name (`DATABASE_URL`)
  shows the server's real `reserved_secret_name` error and keeps what was
  typed; a real save reports the name and version — not the value — and
  clears the form. Recording every request and response for the whole
  run, the value left the browser **exactly once**, in that one `PUT`, and
  appears in no API response, the page's DOM, `localStorage` or
  `sessionStorage` afterwards.
- **Restart** from Lifecycle actions, then a check *outside* the browser,
  through the platform's own proxy: the running container received
  exactly that value (compared by hash — the test application never
  echoes it). After **Delete** (confirm dialog accepted) and another
  restart, the next container no longer had it.
- As the other employee: the section says it's owners-only and shows not
  even the secret's name; their attempt to overwrite it gets the real
  `403 forbidden`, and the owner's value is untouched.
- Console: only the deliberate `400` and the non-owner's expected `403`s.
  Screenshots looked at, not just captured: name, value and Save on one
  row at 1280px, one column at 420px; the audit log beneath shows
  `set secret API_KEY (version 1)` and `deleted secret API_KEY` — names,
  never values.

**One real bug, found while writing the non-owner step, before the pass
even ran:** `listSecrets` returns `403` to someone who isn't an owner,
and the page's catch-all for failed fetches turned that into an empty
list — so the section told them **"No secrets yet."** That's false; there
may well be secrets, they just can't see them. A `403` is now told apart
from an empty list, with its own unit test.

**The value field, deliberately:** a `<textarea>`, not a password input —
plenty of credentials are multi-line (PEM keys, JSON service-account
files), and a password field invites the browser's password manager to
save the value. Spell-check is off (enhanced spell-checking can send what's
typed to a third-party service), as is autocomplete. The cost is that the
value is visible while it's typed — listed under **Known gaps**.

### Deleting an application that never went live, verified for real

Delete used to be enabled only for `archived`/`suspended` applications,
matching the Platform API — where an application that had never been
deployed could not be deleted by anyone, since Archive needs it running
first. Both now follow `docs/05_Process_Flows.md`'s `Draft → Deleted`:
Delete is also offered for `draft`, `validated`, `build` and `failed`, but
only while nothing in the deployment history is running or in progress and
no build is queued or running — because a rebuild leaves an application in
`build` while its previous version still serves. A Playwright pass against
a real stack confirmed both sides: on an application mid-rebuild with its
previous version live, Delete is disabled and its tooltip says why; on a
draft, Delete is enabled, and confirming it really deletes the
application. No page errors.

### Rotating the database password, verified for real

The platform-managed `DATABASE_PASSWORD` row has a **Rotate** button;
owner-set secrets don't, since the platform can't invalidate a credential
a third party issued. It confirms first, then shows the server's own
account of what happened, and an incomplete rotation
(`rotation_incomplete`) shows as an error rather than a success. A
Playwright pass against a real application with a real database: the row
offered Rotate and not Delete, the owner-set secret offered no Rotate, the
confirmation said the old password stops working at once, and after
confirming the result read "version 2 … restarted" with the table
updated. Then, checked outside the browser from the application's own
network: the old password was refused, the new one worked, the restarted
application still reached its database, and the audit trail recorded the
rotation by version. No page errors.

### Logs UI (Module S), verified for real

The application detail page has a **Logs** section: the lines this
application's containers printed, newest first, 50 at a time, with a text
filter, a service filter and **Load older lines** that follows the cursor
the Platform API hands back rather than counting offsets in the browser.
A `draft` or `validated` application asks the platform for nothing at all
— it has never had a container — and says "Nothing has run yet" instead.

A non-owner is told "Only this application's owners can read its logs."
The Platform API answers `404` for "no such application" and "not yours"
alike, so it never confirms to a stranger that an application exists
(`FR-087`). On this page the application itself has already loaded, so a
`404` from the logs endpoint can only mean the second — and saying so is
clearer than repeating "application not found" on a page showing that
very application.

A Playwright pass against a real stack, driving a deployed application
that prints a line a second, prints its own `API_KEY` on purpose, and
writes one line to stderr, in two independent browser contexts (its owner
and an uninvolved employee). Confirmed for real: the owner sees a line
printed seconds earlier; the 50 lines shown run newest first by their own
timestamps; the secret reads `[REDACTED:API_KEY]` and its value appears
nowhere in the page's HTML; the stderr line is marked `err` in text; a
filter matching nothing says so rather than showing an empty box; Clear
restores the unfiltered lines; **Load older lines** appended a second page
(50, then 100) without moving or repeating a single line already on
screen, and the two pages together still ran newest first; no console
errors. In the second context, the uninvolved employee got the
owners-only message, no lines at all, and nothing the application had
printed anywhere in their page.

One check in the first version of that driver script was wrong — not the
page: it asserted the line just printed was at the top. This application
prints a line a second, so a newer one legitimately overtakes it. The
check now asserts the ordering itself.
