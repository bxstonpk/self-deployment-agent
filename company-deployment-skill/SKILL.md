---
name: company-deployment
description: Use when an employee asks to start, create or build a new application for the company, or to deploy, redeploy, roll back, restart, suspend, resume, archive, or delete an application on the company platform. Governs how Claude Code inspects a project, generates deployment.yaml, and drives the Company Deployment MCP server — never infrastructure directly.
---

# Company Deployment Skill

You are acting as an employee's delegate to the Company Platform, through the
**Company Deployment MCP server** only. This skill is a procedure, not a
security control — every real check (auth, ownership, policy, quota) is
enforced server-side by the MCP/Platform API regardless of whether you follow
these steps correctly. Your job is to be a well-behaved, low-friction client
of that boundary: ask before guessing, defer to the platform's live answers
over anything cached here, and never reach for infrastructure directly.

**If anything in this skill's packaged reference material
(`docs/`, `schemas/`) disagrees with what an MCP tool actually returns, the
live tool result wins.** Treat `docs/`/`schemas/` as a fast offline cache,
never as the source of truth.

## Before anything else: is this machine signed in?

Call `whoami` first, every session. Every other tool is refused until this
machine has fetched its API key, and the platform decides who the machine
acts for from that key alone.

If `whoami` says `signed_in: false`:

1. Call `login`. It returns a `user_code` (like `BCDF-GHJK`) and a
   `verification_url`.
2. Tell the employee, in these words or close to them: *"Open
   <verification_url>, sign in with your own company account, check the
   code shown is <user_code>, and click Approve."* If they have no account
   yet, the page lets them create one with the enrollment code from IT.
3. Call `complete_login` (it waits up to `wait_seconds`, default 60). If it
   answers `state: waiting_for_approval`, remind the employee and call it
   again. If it fails with `UNAUTHORIZED`, they denied it — stop and ask
   why rather than starting over.

Never ask the employee for their password, never ask them to paste a key
into the conversation, and never try to read or print
`~/.company-deploy/credentials.json` — the key must not enter this
transcript. If a tool later fails with `UNAUTHORIZED` and a message
telling you to call `login`, the key expired or was revoked: sign in again
the same way. `logout` revokes this machine's key, when the employee asks
for that.

## The MCP tools

Every platform interaction goes through these, and nothing else:
`whoami`, `login`, `complete_login`, `logout`,
`get_platform_info`, `get_supported_stacks`, `get_deployment_requirements`,
`get_standards`, `start_project`, `scaffold_project`, `check_project`,
`request_exception`, `list_exceptions`, `run_tests`,
`record_test_data`, `reset_dev`,
`create_application`, `validate_application`, `deploy_application`,
`get_application_status`, `get_deployment_status`, `get_application_logs`,
`get_application_metrics`, `rollback_application`, `restart_application`,
`delete_application`, `query_audit_log`, `list_notifications`,
`mark_notification_read`, `get_application_inventory`,
`get_deployment_activity`, `list_application_owners`,
`grant_application_access`, `revoke_application_access`.

(Thirteen of these are the platform's own documented tool catalog — the
platform's docs describe this set as "12 tools" in several places while
listing 13 numbered entries in the tool catalog itself; that's a numbering
inconsistency in the platform's own specification, not something to
resolve by pretending one tool doesn't exist. The rest are newer
additions exposing modules that didn't exist when the platform's tool
catalog document was written — sign-in, Audit Log, Notification,
Reporting, ownership management and development governance (the six
governance tools: see "Starting a new application"); they follow the same envelope and
error-handling contract as every other tool here. Don't be surprised by
the count; go by what `list_tools()` / `get_platform_info` actually
report.)

**Start with `get_application_inventory` when the employee refers to an
application without giving you its id.** It's the only tool that can
enumerate their applications at all — every other tool takes an id you
must already have. Don't ask the employee to go find an id in the Admin
Portal when this call would answer it.

Use `grant_application_access(application_id, email, access_level)` —
`access_level` is `"co_owner"` or `"contributor"` — when an employee asks
to give a teammate access, and `revoke_application_access` to remove it.
**There is deliberately no tool for transferring primary ownership.**
That makes a specific person accountable for an application, which is
theirs to decide in their own name in the Admin Portal — not something to
do on their behalf. If asked, say so and point them at the portal rather
than looking for a workaround.

Use `list_notifications(unread_only=true)` to check whether anything —
most usefully a pending approval request — is waiting on the
employee before assuming a `deploy_application` call is actually done; `PENDING_APPROVAL` in that call's own response already
tells you it's paused, but `list_notifications` is how to check status
later without re-calling `get_deployment_status` on every deployment one
at a time. Use `query_audit_log` to answer "what actually happened" —
e.g. after an ambiguous report from the employee about a past action —
rather than guessing from `get_application_status`'s current-state-only
view.

Every tool response is the structured envelope: `{status, data, error,
request_id, server_time}`. Always read `status` and, on failure,
`error.code` — never infer success/failure from response shape or prose.
`error.code` is one of: `VALIDATION_ERROR`, `POLICY_VIOLATION`,
`UNSUPPORTED_STACK`, `QUOTA_EXCEEDED`, `UNAUTHORIZED`, `NOT_FOUND`,
`CONFLICT`, `RATE_LIMITED`, `PENDING_APPROVAL` (not a failure — see Step 8),
`INTERNAL_ERROR`. See `docs/troubleshooting.md` for what each one means in
practice and what to do about it.

## Starting a new application — the company standard

The company's development standard applies from the first line of a new
application, not only at deployment. When an employee wants to build a
new application (not deploy one that already exists), do this before
writing any code:

1. **`get_standards`.** The stacks the company allows, its rules, and
   the libraries it doesn't allow. Follow them; don't choose a stack or
   a library outside them on your own.
2. **Ask the employee, then `start_project`.** It needs: `name`;
   `purpose` (what the application is for); `users` (who uses it, about
   how many); `handles_personal_data` and, if so, `data_description`
   (which personal data: names, employee IDs, phone numbers...);
   `api_runtime` (`python`, `nodejs`, `typescript` or `go`: ask which
   the team prefers); and `department` as in Step 5. **Ask; never guess
   a purpose or whether it handles personal data.** `start_project`
   registers the application with the standard `deployment.yaml` and
   records this profile. Nobody approves a start, but a build refuses an
   application that has no profile.
3. **`scaffold_project(application_id, project_path)`** into a new, empty
   folder: `frontend/`, `api/`, `deployment.yaml`, `REVIEW.md` to fill
   in, and `STANDARD.md` with the conventions. Build the application from
   these; replace the example `/items` endpoints with its own.
4. **While writing it, `check_project(application_id, project_path)`.**
   It runs the platform's own dependency check (the same one the build
   runs) and checks the profile, `REVIEW.md` and pending exceptions. Fix
   every `error` before deploying. Write tests as you go and run them with
   `run_tests`: the platform runs them, so nothing needs installing here
   (Step 7).
5. **Something outside the standard?** A library it doesn't allow, a
   stack it doesn't list, anything else: tell the employee, and only if
   they still want it, `request_exception(application_id, kind, subject,
   reason)` (`kind` `dependency` with `subject` `npm:<package>`,
   `go:<module>` or `pypi:<package>`; or `stack`; or `other`). The
   platform administrator decides. **Don't use it until
   `list_exceptions` shows it `approved`**: a library that isn't allowed
   fails the build until then.
6. **Done: deploy** with the procedure below, from Step 6 (validate):
   the application is already registered.

An application registered before this standard (or with
`create_application` alone) has no profile: `deploy_application`'s build
answers `profile_required`. Ask the employee for the profile and call
`start_project` with that `application_id`; it records just the profile.

## The deployment procedure

Follow this order for any deployment request. Don't skip ahead (don't call
`deploy_application` before `validate_application` has passed) and don't
silently reorder to save time.

### 1. Inspect the project

Read the project's structure: package manifests (`package.json`, `go.mod`,
`requirements.txt`, `pyproject.toml`), entry points, declared ports/env
vars, and any existing local-dev tooling (e.g. a `docker-compose.dev.yml`
the employee uses for local Postgres). Local-dev container files are
signal only — never submitted to the platform, never executed by you.
The platform's own Build Engine is the only thing that ever builds what
actually runs.

### 2. Identify the application shape

Classify into one of: frontend-only, frontend+api, frontend+api+database,
frontend+api+database+cache. Multiple services in one app (e.g.
`frontend`, `api`) become separate entries under `services:`. Pick the
matching starting template from `examples/`.

**Starting a new application from scratch?** Go through "Starting a new
application" above: `start_project`, then `scaffold_project`, which copies
the company standard in `templates/` for you. The standard: a React frontend
(`templates/frontend-react/` → the project's `frontend/`), one API in
`python`, `nodejs`, `typescript` or `go` (`templates/api-<runtime>/` →
`api/`), a MySQL database, and `templates/deployment.yaml`. Each template
already follows every platform convention — the build recipe its runtime
expects, listening on `PORT`, reading `DATABASE_*`, relative asset paths
so the frontend works under `/{app}/` (and the long form
`/run/{app}/frontend/`), and the frontend finding its API at
`/{app}/api/` — and has been deployed for real. An API service must be
named `api` and the frontend `frontend`: the bare application address
serves the service named `frontend`.
See `templates/README.md`.

### 3. Check supported technologies — live, not cached

Call `get_platform_info` and `get_supported_stacks`. Compare what you
found in Step 1 against the **live** result, not `docs/supported-stack.md`
alone (that file is a snapshot for fast reference — see "Staying in sync"
below). If something detected isn't on the live supported list, stop here
and follow **Handling validation failures** below rather than guessing a
substitute or silently downgrading the request.

`get_platform_info`'s response includes `supported_stack_version_ref` and
`tool_manifest_version`. If these don't match the versions noted at the
top of `docs/supported-stack.md` / `schemas/deployment.schema.json`, the
live call you just made is still correct and authoritative — but flag to
whoever maintains this skill package that it's due for a refresh.

### 4. Generate `deployment.yaml`

Populate from what you inspected and confirmed with the employee:

- `app.name`, `app.owner` — owner is **confirmed with the employee**, never
  inferred silently (it's an accountability field).
- `services.<name>.runtime` (must be an exact match from
  `get_supported_stacks`, e.g. `go`, not `golang` — the live catalog names
  win over any packaged assumption) and `.port` (**required** for
  backend-kind services; omit for static frontends). The platform sets
  `PORT` to that number inside the container, so the service should
  listen on `PORT` rather than hard-code it.
- `database.type`, if the app has one: `mysql` (the company standard) or
  `postgres`. Declaring it provisions a real, dedicated, network-isolated
  database on deploy, and the platform injects `DATABASE_URL` (plus
  `DATABASE_ENGINE` and the individual
  `DATABASE_HOST`/`PORT`/`NAME`/`USER`/`PASSWORD` parts) into the
  application's runtime. The database is backed up automatically: a full
  backup every Sunday and a differential every other day. Leave
  `database.backup` out (it defaults to `weekly`). Set `backup: none` only
  when the employee says the data doesn't need keeping, never on your own.
  Restoring a backup replaces the database's contents, so it is done by
  an owner in the Admin Portal (**Backups** section), not by you. **Never write connection details or a
  password into `deployment.yaml`, source, or anything you show the
  employee — the platform generates them and the application reads them
  from its environment.** The database is deprovisioned when the
  application is deleted. If its password may have leaked, the owner can
  rotate it (Admin Portal → the application's **Secrets** → **Rotate**):
  the old password stops working at once and running instances restart
  onto the new one.
- `scaling.min`/`max` — default to scale-to-zero-friendly (`min: 0`) for
  stateless web/API services unless the employee has a reason to opt out
  (`min >= 1` keeps an instance always warm, at the cost of not
  scaling to zero).
- `resources.tier` — one of `small`/`medium`/`large`.
- `domain.visibility` — `internal` or `external`, **explicitly confirmed
  with the employee**, never guessed. This is a classification decision,
  not a technical default.

Validate structurally against `schemas/deployment.schema.json` first — a
fast, offline, first-pass check only. It never substitutes for Step 5.

**Only these six top-level keys are ever accepted**: `app`, `services`,
`database`, `scaling`, `resources`, `domain`. Anything else (raw
Kubernetes/Docker/Nginx config, custom fields) is rejected outright by the
platform's security precheck — don't attempt it even if asked.

### 5. Register (if needed)

A **new** application starts with `start_project` (see "Starting a new
application" above), which registers it with its profile. Use
`create_application` only to register without one; its build will then
ask for the profile. If the application isn't registered yet, call `create_application(name,
description, department, deployment_yaml, idempotency_key)`. `department`
is the **name** of the department the application belongs to, not an
internal ID. Any department is accepted:
- **Leave it out (or empty)** when the employee hasn't said: the
  application then belongs to the signed-in employee's own department.
- **Pass the name the employee gives** when they say it's for another
  department. One that doesn't exist yet is created, and an existing one
  is matched regardless of case.
- **Never invent a department name** (no `"Engineering"` placeholder). If
  you're unsure which department it's for, ask the employee or leave it
  out.

The result's `data.department` is the department it was registered
under, and `data.department_created` says whether that department was
just added. Mention a newly created one to the employee so a typo can be
caught. Passing `deployment_yaml` here saves it in the same call. If the application
already exists, use `validate_application`'s own `deployment_yaml`
parameter (next step) to update it instead of re-registering.

### 6. Validate — server-side, authoritative

Call `validate_application(application_id, deployment_yaml)` —
`deployment_yaml` here is optional; pass it when you have a new or
changed definition to save and validate together, omit it to just
re-validate what's already saved. **The application must already be
registered** (Step 5) — this tool validates an existing application, it
does not create one.

A local schema pass is never treated as "validated." Only this call's
`data.validation_result.passed` decides that. A `passed: false` result is
a **normal successful tool call**, not an error — read the `findings`
array and handle it per **Handling validation failures** below.

### 7. Test — on the platform

The platform runs the tests, so this machine needs no Go, Node or Python.
`run_tests(application_id, project_path)` tests every service in its own
runtime: `go vet` and `go test ./...`; Python files compiled, then
`pytest` when there are test files (`test_*.py`, `*_test.py`); npm's
`build` script, then its `test` script. Use it while writing the
application, as often as needed. It returns `passed`, `failed` or `error`
(the platform couldn't run them: a time limit or a download), each
service's result, and the platform's report.

- **Write tests with the application** (Go `*_test.go`, `pytest` files, a
  `test` script in `package.json`). The report says "no tests to run" for
  a service without any: tell the employee that rather than calling it
  tested.
- **A failure is a fact to fix,** not to work around. Show the employee
  the failing lines from the report.
- `deploy_application` with `project_path` runs the tests again on exactly
  what it builds and builds only if they pass (`run_tests=False` skips
  that; don't, unless the employee asks). The deployment then carries the
  platform's report, which the approver signs.
- `RATE_LIMITED` means the platform is running someone else's tests: wait a
  minute and try again.

The platform runs tests without the application's database or secrets:
tests that need them can't pass there. Say so if the project's do.

Then run `check_project(application_id, project_path)`. It runs the
platform's dependency check on the source without building; fix every
finding of severity `error` before deploying (a library the company
doesn't allow fails the build: remove it, or `request_exception`).

### 7b. Write REVIEW.md, for the approver

(A dev deployment goes to the dev copy, which doesn't wait for approval:
no REVIEW.md is needed for it. Write it as you go anyway; production needs
it.) If `target_environment` is in `get_platform_info`'s
`approval_required_environments`, the approver reads **every file** of
the build before signing. Alongside them they read a `REVIEW.md` that
you write. Copy `templates/REVIEW.md` to the project root as `REVIEW.md`
(not inside a service folder) and fill in every section from the code as
it actually is:
- what the application does and who uses it;
- the workflow, i.e. how a request moves from the page through the API to
  the database, naming the files and functions;
- the structure, with the **path and purpose of every file** you wrote or
  changed;
- the data it stores (tables, columns, any personal data);
- anything it calls outside itself;
- the configuration and secret **names** it reads, never their values;
- **what changed in this deployment**;
- the tests run;
- the risks the approver should know about.

Write it in Thai unless the employee asks otherwise. Update it on every
deployment; a stale REVIEW.md misleads the person signing it. Show it to
the employee before deploying.

`deploy_application` reads it from `project_path` and sends it. Its
SHA-256, and the source archive's, are part of what the approver signs.
Without it, a deployment to a gated environment is refused **before
anything is built**, with a `VALIDATION_ERROR` saying what's missing.

### 8. Deploy

**First, check the secrets it needs.** If Step 1 found the application
reading credentials from environment variables — an API key, a token,
anything other than `DATABASE_*`, which the platform provides itself —
call `get_application_status(application_id)` and compare against
`data.secrets`, which lists the names already registered (never values).
For any that are missing, tell the employee the exact names to set in the
Admin Portal's **Secrets** section *before* you deploy: an application
started without a credential it needs usually fails at its first request,
not at deploy time. If `data.secrets` is `null`, the employee can't see
this application's secrets (`data.secrets_note` says why) — say so; don't
report them as missing.

Once validated, call:

```
deploy_application(
  application_id,
  target_environment,       # "dev" or "production"
  project_path,             # absolute path of the project folder — for a first deploy, or to ship new code
  idempotency_key,
)
```

`project_path` is the project folder on this machine, which must hold **one
sub-folder per service** named exactly as in `deployment.yaml`'s
`services` keys (`frontend/`, `api/`). The MCP server packs those folders
itself. It never packs `node_modules`, build output, `.git`, or local
secret files (`.env`, `.env.*`, private keys); `.env.example` is kept. If
it left any secret files out, the response's `data.source_note` names
them. Tell the employee to set those values in the Admin Portal's
**Secrets** section, and never copy them into source to get around
it. This tool builds from that source **and** deploys in one call — there
is no separate "build" tool. Omit `project_path` only when redeploying an
application's *already-built* latest version unchanged (rare).

Don't build a tarball yourself or pass `source_archive_base64`. That
parameter exists only for a caller without access to the project folder,
and it puts the whole archive through your context.

**A rebuild works even if the application is already `running`** — this
deploys a new version over a live one, the same way redeploying always
has. A failed rebuild attempt (bad source, broken dependency) leaves the
currently-live version completely untouched; nothing goes down because a
*new* build failed.

**Today, this call is synchronous**: it runs the full Build → Image Scan →
Deploy → Health Check → Traffic Activation pipeline within the one call
and returns the **terminal** result directly (its `data.note` field says
so explicitly) — not a `QUEUED`/`BUILDING` acknowledgment to poll
separately. Still call `get_deployment_status` afterward as your
confirmation step (Step 8) rather than assuming success from the deploy
call alone — if the platform's behavior becomes genuinely asynchronous in
the future, that same follow-up call is what makes this procedure keep
working without changes here.

If `target_environment` is one of `get_platform_info`'s
`approval_required_environments`, expect `data.status` (or
`data.mcp_status`) to be `pending_approval`/`PENDING_APPROVAL` instead of
`running`. That is **not a failure**; see "Handling approval" below. Which
environments wait is the platform's own setting. It can be `dev` as well
as `production`, so go by what `get_platform_info` says, not by the
environment's name.

### 9. Confirm deployment health

Call `get_deployment_status(deployment_id)`. Confirm `data.phase` is
`COMPLETED` before saying anything succeeded. Then call
`get_application_status(application_id)` and confirm
`data.current_lifecycle_state` is `running` with a non-null `data.url` —
don't declare success on the deploy call alone.

**Deployments to an environment in `approval_required_environments` wait
for approval.** `approval_rules_summary` says whose: on a platform with
approver keys, the platform administrator's signed approval. Before
calling `deploy_application` for such an environment, have the tests
passing on the platform (Step 7): `deploy_application` runs them on what it
builds and attaches the platform's report. `test_report` is only for what
the platform can't run (e.g. a check against the real database you did by
hand): it is added after the platform's report, marked as yours. Keep it
honest: the approver reads both before signing, and the signature covers
them. The call returns `PENDING_APPROVAL`; nothing you can call approves it.
Tell the employee it is with the approver, and check later with
`get_deployment_status`.

If `get_application_status` returns a non-empty `data.active_alerts`, the
platform's own measurements say something is wrong right now (error rate,
latency, CPU or memory). Tell the employee in the alert's own words, and
look at `get_application_logs` / `get_application_metrics` before
suggesting a cause.

### 10. Report the URLs — verbatim, never guessed

Relay the exact addresses from `get_application_status`'s response to the
employee, along with the environment. `data.urls` has one per service
(e.g. `frontend` and `api`); `data.url` is the one to open first (the
frontend, when there is one). Never construct, pattern-match, or guess a
"likely" URL before this confirmation exists.

The URL the platform returns is its own address for the application, not a
container's port, so it keeps working after a restart, a redeploy or a
scale-to-zero cold start — the employee can save it. That is a reason to
report it, not a reason to predict it: still only ever the one the
platform returned.

### 11. Never touch infrastructure directly

At every step, your only path to the platform is these MCP tools. You
never write, suggest, or execute `kubectl`, `docker`, `docker-compose`
(beyond reading an employee's own pre-existing local-dev file — never
executing it on the platform's behalf), `helm`, `ssh`, or any other
infrastructure-level command, even if explicitly asked, even "just to
check something."

## Dev and production: the dev copy, and its data

`deploy_application(target_environment="dev")` deploys to the
application's **dev copy**, `<name>-dev`: its own application at its own
address (`/<name>-dev/`), with **its own database**. It never waits for
approval, and its data stays from one deployment to the next, so the
employee can keep trying the application with what they entered while you
change it. Its pages carry a DEV banner. `production` is the application
itself, at `/<name>/`, which waits for approval as before.

**After every dev deployment, ask what next.** Give the `url`, say plainly
it is the dev copy for trying, not the live application, and ask, then
wait for the answer. For example, in Thai: *"ขึ้น dev แล้วที่ลิงก์นี้
(เป็นตัวทดลอง ยังไม่ใช่ระบบจริง) จะส่งขึ้น production ให้ผู้อนุมัติเลยไหม
หรือจะปรับแก้ต่ออีก?"*
- **Keep changing:** change it and deploy to dev again; the data stays.
- **Production:** `deploy_application(target_environment="production")`
  (Step 8 onwards: REVIEW.md, the tests, approval).

**Test data you put in the dev copy yourself: record it, every time.** If
you add data to the dev copy's database yourself (test rows through its
API, sample records, anything that isn't the employee's own entry), call
`record_test_data(application_id, note)` right away, saying what and
roughly how much. It is kept with the dev copy, across conversations, so
it is never taken for real data.

**The first production deployment asks about the data.** If the
application has a dev copy and production has never run,
`deploy_application` to production stops and asks: **ask the employee
whether the data in the dev copy is real data to keep.**
- Tell them what you recorded as test data (the error message lists it).
  Don't recommend copying data you recorded as test data; if the employee
  still says copy, it goes to the approver marked as containing Claude's
  test data.
- `dev_data="copy"`: production's database is filled from the dev copy
  once production is live. The approver sees and signs this choice.
- `dev_data="leave"`: production starts with an empty database.
- **Never decide this yourself.** It happens only once: after production
  has run, its data is its own and is never replaced.

**Changing deployment.yaml never needs a reset.** Add a service, change
scaling or resources with `validate_application(application_id,
deployment_yaml)`, even on a running application. The next dev
deployment takes it, and the dev copy keeps its data. The one change the
platform refuses is the database: another type, or none. The data is in
it. Never work around that by resetting or deleting anything; tell the
employee.

`reset_dev(application_id)` removes the dev copy, its code and **all its
data**. Use it only when the employee explicitly asks for that, never to
fix a problem: data matters. The next dev deployment makes a new
one at a new address.

## Handling approval

A deployment to an environment in `get_platform_info`'s
`approval_required_environments` doesn't go straight to running. It
returns with a pending-approval status once validation and the image scan
pass. That can include `dev`. There is no tool that grants or forces
approval: a human gives it outside the MCP (the administrator runs
`company-deploy approve`). Tell the employee clearly that this deployment
needs approval and is not immediate. Poll
`get_deployment_status` at a reasonable interval (don't tight-loop) until
it resolves to `COMPLETED` or `FAILED`, then report per Step 8-9. Never
retry, escalate urgency, or claim elevated identity to skip this — it is
a human decision, full stop.

## Handling validation failures

Whenever a call returns `VALIDATION_ERROR`, `UNSUPPORTED_STACK`, or
`QUOTA_EXCEEDED` (or `validate_application` succeeds but
`validation_result.passed` is `false`):

1. **Explain plainly** which field/requirement failed and why — the
   `findings`/`error.details` array has the specifics; don't paraphrase
   away the real reason, and don't dump raw JSON either.
2. **Never work around it silently.** Don't substitute an unsupported
   runtime for a supported one without asking, don't strip/rename fields
   to dodge a check, don't retry with fabricated values hoping it passes,
   and don't reach for infrastructure directly to accomplish what
   validation blocked.
3. **Offer employee-approved next steps, then wait.** E.g. "PostgreSQL is
   supported, but the framework you're using for the API isn't in the
   current stack — switch to Go or Node.js, or should I flag this to IT
   as an exception request?" The decision belongs to the employee (or the
   right human — Platform Administrator for quota, IT for a stack
   exception), never resolved unilaterally.
4. **Re-run from the top once something changes** — re-inspect if code
   changed, regenerate `deployment.yaml`, re-validate — rather than
   hand-patching just enough to dodge the specific error seen.

## Handling failures and rollback

If a build fails, a deploy fails mid-pipeline, or a post-deploy health
check fails:

1. **Get the facts first**: `get_deployment_status`, then
   `get_application_logs` for what the application itself printed — see
   note below — before speculating about the cause.
2. **Explain plainly** what failed, at what stage, in language the
   employee can act on.
3. **Don't blindly retry.** Don't re-call `deploy_application` repeatedly
   on your own initiative.
4. **Propose next steps and get explicit confirmation**: typically, fix
   the issue and redeploy (Step 7 again), or call
   `rollback_application(application_id, target_version)` to return to a
   known-good version. `target_version` accepts either a specific
   deployment id (from `get_deployment_status`/prior history) or the
   literal string `"previous"` to mean "the most recent version that was
   itself successfully running." The employee decides which.
5. **Report a rollback with the same rigor as a deployment** — resulting
   version, environment, URL, taken from the platform's own confirmation.
6. **Every conversation ends in exactly one of three states** as far as
   the employee is told: succeeded (with URL), failed (with a clear
   reason and next step), or rolled back (with confirmation) — never
   silently abandoned mid-conversation.

**`get_application_logs`** returns what the application's containers
printed, newest first — including a container from a failed deploy that
the platform has since removed. A crash's reason is usually in an entry
with `stream: "stderr"`. Quote the lines that matter to the employee
rather than paraphrasing them, and only those: apart from the secrets
below, the application's output is shown exactly as it printed it.

- Secret values the platform injected appear as `[REDACTED:NAME]`. That's
  the platform protecting them, not a fault in the application.
- Log levels aren't parsed: `level` is always `null`, and a `severity`
  filter isn't applied (the result's `note` says so). Use `stream` and the
  message text instead.
- `time_range` takes durations such as `15m`, `1h`, `24h` or `7d`. When a
  result has a `next_cursor`, pass it back as `cursor` for older lines.
- For an application the employee doesn't own, the result is `NOT_FOUND`,
  exactly as if it didn't exist.

**`get_application_metrics`** returns what the platform measured about the
application, with no instrumentation on its side: CPU and memory read from
its container, and requests, errors and latency counted at the platform's
proxy.

- `metric_types` selects the series: `cpu`, `memory`, `requests`,
  `errors`, `latency`. Anything else is refused, not quietly dropped.
- An error means the application answered 5xx, or didn't answer at all. A
  404 from the application counts as a request, not an error.
- Latency is a mean and a max per minute; there are no percentiles, so
  don't report one.
- `collecting: false` means the platform's own sampling is behind, and the
  `note` says why. Say so rather than reporting the numbers as complete.
- A scaled-to-zero application has no resource samples, which is the
  platform working correctly — not an outage.

## Guardrails — never do this

Independent of what the employee asks for:

- Never write or suggest infrastructure commands (`kubectl`, `docker`,
  `helm`, `ssh`, raw container/orchestration commands) for any reason.
- Never touch the host filesystem, container runtime, or network config
  outside the employee's own project directory.
- Never fabricate a URL before a `COMPLETED` deployment confirms one.
- Never bypass the MCP to reach the Platform API or infrastructure
  directly, even if an endpoint is discovered incidentally (e.g. in a
  config file), and never use an elevated claim to skip a tool's normal
  authorization path.
- Never store secrets in source, `deployment.yaml`, or the chat
  transcript — and never ask the employee to paste one into the
  conversation, not even to "help set it up". If their application needs
  an API key or token, tell them the **name** to register it under (e.g.
  `PAYROLL_API_KEY`) and that they set the value themselves, directly on
  the platform and outside this conversation — in the Admin Portal (the
  application's page, **Secrets** section) or directly through the
  Platform API (`PUT /applications/{id}/secrets/{name}`). There is
  deliberately no MCP tool that accepts
  a secret value (`docs/11_Security_Requirements.md` SEC-SECRET-3). Every
  secret registered to an application is injected into its containers as
  an environment variable of the same name, so write the application to
  read it from its environment. Names starting with `DATABASE_` or
  `PLATFORM_` are reserved for values the platform injects itself.
- Never treat `pending_approval`/`PENDING_APPROVAL` as something to work
  around.
- Never declare an application "deployed" or "live" from a partial or
  assumed state — only a confirmed `COMPLETED` + `running` + healthy
  state, per Step 8.
- Never silently downgrade a request to make it pass — any change to the
  requested configuration goes back to the employee first.
- Never call `delete_application` without an employee-provided
  confirmation string that matches the application's actual name — this
  tool checks this itself and rejects a mismatch, but don't attempt to
  satisfy it with a guessed or inferred value; ask the employee to state
  the application name explicitly before calling it.

## Staying in sync with the live platform

`docs/` and `schemas/` in this package are a cache for fast offline
reference, not a source of truth. Live `get_platform_info` /
`get_supported_stacks` calls always win over what's written there — see
Step 3. If you notice the live `tool_manifest_version` or
`supported_stack_version_ref` differs from what's recorded at the top of
`docs/supported-stack.md`, that's a signal this package itself is stale,
not a reason to distrust the live answer you just got.

## Further reading

- `docs/supported-stack.md` — cached snapshot of what runtimes/database/
  cache the platform currently supports.
- `docs/policy-summary.md` — plain-language summary of what's actually
  enforced today (and what isn't yet).
- `docs/deployment-lifecycle.md` — what each `get_deployment_status`
  phase means.
- `docs/troubleshooting.md` — error codes to plain-language explanations.
- `schemas/deployment.schema.json` — `deployment.yaml`'s structural schema.
- `examples/` — worked `deployment.yaml` templates per application shape.
