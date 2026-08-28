---
name: company-deployment
description: Use when an employee asks to deploy, redeploy, roll back, restart, suspend, resume, archive, or delete an application on the company platform. Governs how Claude Code inspects a project, generates deployment.yaml, and drives the Company Deployment MCP server — never infrastructure directly.
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

## The MCP tools

Every platform interaction goes through these, and nothing else:
`get_platform_info`, `get_supported_stacks`, `get_deployment_requirements`,
`create_application`, `validate_application`, `deploy_application`,
`get_application_status`, `get_deployment_status`, `get_application_logs`,
`get_application_metrics`, `rollback_application`, `restart_application`,
`delete_application`.

(Thirteen tools are actually registered on the MCP server as of this
writing — the platform's own docs describe this set as "12 tools" in
several places while listing 13 numbered entries in the tool catalog
itself. That's a numbering inconsistency in the platform's own
specification, not something to resolve by pretending one tool doesn't
exist. Don't be surprised by the count; go by what `list_tools()` /
`get_platform_info` actually report.)

Every tool response is the structured envelope: `{status, data, error,
request_id, server_time}`. Always read `status` and, on failure,
`error.code` — never infer success/failure from response shape or prose.
`error.code` is one of: `VALIDATION_ERROR`, `POLICY_VIOLATION`,
`UNSUPPORTED_STACK`, `QUOTA_EXCEEDED`, `UNAUTHORIZED`, `NOT_FOUND`,
`CONFLICT`, `RATE_LIMITED`, `PENDING_APPROVAL` (not a failure — see Step 8),
`INTERNAL_ERROR`. See `docs/troubleshooting.md` for what each one means in
practice and what to do about it.

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
  backend-kind services; omit for static frontends).
- `database.type`, if the app has one.
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

If the application isn't registered yet, call `create_application(name,
description, department, deployment_yaml, idempotency_key)` — `department`
is a **name** (e.g. `"Engineering"`), not an internal ID; the tool
resolves it server-side, and returns a clear `VALIDATION_ERROR` listing
known department names if it doesn't recognize the one given. Passing
`deployment_yaml` here saves it in the same call. If the application
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

### 7. Run the project's own tests

Run whatever test tooling the project already has (`npm test`,
`go test ./...`, `pytest`). The platform validates infrastructure/policy
compliance, not application correctness — that's still your and the
employee's responsibility. If tests fail or none exist, say so clearly and
ask the employee how to proceed rather than deploying untested code
silently, or silently skipping this step.

### 8. Deploy

Once validated, call:

```
deploy_application(
  application_id,
  target_environment,       # "dev" or "production"
  source_archive_base64,    # required for a first deploy, or to ship new code
  idempotency_key,
)
```

`source_archive_base64` is the project's source as a base64-encoded
`tar.gz`, with a **top-level directory per service name** matching
`deployment.yaml`'s `services` keys (e.g. `api/main.go`, `api/go.mod` for
a service named `api`). This tool builds from that source **and** deploys
in one call — you don't need, and there is no, separate "build" tool.
Omit `source_archive_base64` only when redeploying an application's
*already-built* latest version unchanged (rare — usually you have new or
first-time source to ship).

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

If `target_environment` is `"production"`, expect `data.status` (or
`data.mcp_status`) to be `pending_approval`/`PENDING_APPROVAL` instead of
`running`. That is **not a failure** — see "Handling production approval"
below.

### 9. Confirm deployment health

Call `get_deployment_status(deployment_id)`. Confirm `data.phase` is
`COMPLETED` before saying anything succeeded. Then call
`get_application_status(application_id)` and confirm
`data.current_lifecycle_state` is `running` with a non-null `data.url` —
don't declare success on the deploy call alone.

### 10. Report the URL — verbatim, never guessed

Relay the exact `data.url` from `get_application_status`'s response to the
employee, along with the environment. Never construct, pattern-match, or
guess a "likely" URL before this confirmation exists.

### 11. Never touch infrastructure directly

At every step, your only path to the platform is these 13 MCP tools. You
never write, suggest, or execute `kubectl`, `docker`, `docker-compose`
(beyond reading an employee's own pre-existing local-dev file — never
executing it on the platform's behalf), `helm`, `ssh`, or any other
infrastructure-level command, even if explicitly asked, even "just to
check something."

## Handling production approval

`deploy_application(target_environment="production")` doesn't proceed
straight to Build — it returns with a pending-approval status once
validation/policy checks pass. There is no tool that grants or forces
approval; approval happens through a channel outside the MCP (the Platform
API console/Admin Portal), by a human. Tell the employee clearly that
production requires human approval and is not immediate. Poll
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

1. **Get the facts first**: `get_deployment_status` (and
   `get_application_logs` if it would help — see note below) before
   speculating about the cause.
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

**`get_application_logs` and `get_application_metrics` are not available
yet** — they always return `INTERNAL_ERROR` explaining that log/metrics
storage doesn't exist on the platform yet. Don't retry them; use
`get_deployment_status`'s failure detail as your primary diagnostic
signal instead, and tell the employee honestly that deeper log/metric
inspection isn't available through the platform today.

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
- Never store production secrets in source, `deployment.yaml`, or the
  chat transcript. (Note: this platform does not yet have a Secret
  Manager at all — if an employee's app needs a secret, say so honestly
  rather than inventing a place to put it.)
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
