# Troubleshooting — error codes to plain language

Every MCP tool failure carries `error.code` (one of a fixed set) and
`error.message` (a specific, human-readable explanation — always read the
actual message; it's written to be specific, not a template to work around).

| `error.code` | What it means | What to do |
|---|---|---|
| `VALIDATION_ERROR` | The request is malformed, or fails a structural/business rule (bad `deployment.yaml` field, unknown department name, wrong lifecycle state for this operation, a build's source failed to compile) | Surface the specific reason from `error.message`/`error.details` to the employee in plain language. Don't retry with guessed values — only resubmit once the employee has confirmed a correction |
| `UNSUPPORTED_STACK` | A declared runtime isn't on the current supported list | Re-check `get_supported_stacks` (live, not cached). Ask the employee whether to change the stack or request an exception through IT — never silently substitute a "close enough" runtime |
| `QUOTA_EXCEEDED` | A resource limit was hit (today: mainly source-archive size — 50MiB) | Tell the employee; for an oversized archive, check for accidentally-included `node_modules`/`vendor`/build artifacts that shouldn't be in the upload |
| `UNAUTHORIZED` | Either this machine isn't signed in (no key, or its key expired or was revoked — the message says to call `login`), or the employee the key belongs to isn't an owner of the target application | Not signed in: follow `SKILL.md`'s "Before anything else" — `login`, the employee approves the code, `complete_login`. Not an owner: don't look for another way in; tell the employee they (or an existing owner) need to grant them access |
| `NOT_FOUND` | The referenced application, deployment, or department doesn't exist | Confirm the identifier with the employee — never fabricate or guess one. For `rollback_application`, this can mean there's no prior successful version to roll back to |
| `CONFLICT` | An `idempotency_key` was reused with different input, or a build/deploy is already in flight for this application | Check current state via `get_application_status`/`get_deployment_status` before retrying; surface the conflict to the employee if it doesn't self-resolve |
| `RATE_LIMITED` | Too many calls in a time window (not currently enforced by this platform, but handle it in case that changes) | Back off; never tight-loop retry |
| `PENDING_APPROVAL` | **Not a failure.** A deploy to an environment that needs approval (any in `get_platform_info`'s `approval_required_environments`, which may include dev) is waiting for a human | Tell the employee it's with the approver; poll `get_deployment_status` rather than treating this as an error — see `SKILL.md`'s "Handling approval" |
| `POLICY_VIOLATION` | A platform rule refuses this. Today: a platform administrator suspended the application (the message gives the reason and who), and only an administrator can resume it | Tell the employee the reason and to contact the administrator named. Don't retry, and don't look for another way to get it running — see `SKILL.md`'s "Guardrails" |
| `UPDATE_REQUIRED` | This machine's MCP server or skill is older than the platform's current release | Run the update command in the message, ask the employee to restart Claude Code (`claude --continue`), then carry on — see `SKILL.md`'s "Before anything else" |
| `INTERNAL_ERROR` | Either an unexpected platform-side failure, or a deliberate, honest "this capability doesn't exist yet" (see below) | For a genuine unexpected failure: tell the employee plainly, suggest retrying once. For the deliberate cases below: don't retry, they won't start working |

## Diagnosing beyond `get_deployment_status`

Two tools show what the platform itself observed, and neither needs the
application to be instrumented:

- **`get_application_logs`** — what the application printed, including a
  container from a failed deploy the platform has already removed.
- **`get_application_metrics`** — CPU and memory read from the container,
  and requests, errors and latency counted at the platform's proxy.

Neither invents anything to fill a gap. An error is a 5xx (never a 4xx),
latency is a mean and a max rather than percentiles, and an application
scaled to zero simply has no resource samples — the answer's `collecting`
flag and `note` say which of those you are looking at.

## Common `VALIDATION_ERROR` messages you'll actually see

These are real, specific messages the platform returns — recognize them
rather than treating every `VALIDATION_ERROR` as generic:

- `"unknown department '<name>'"` — the department name given to
  `create_application` doesn't match any registered department;
  `error.details` lists the known ones.
- `"app.name (\"X\") must match the registered application name (\"Y\")"`
  — the `app.name` inside `deployment.yaml` has to exactly equal the name
  the application was registered under; these can drift if the employee
  renamed something.
- `"services.<name>: backend/API services must declare a port"` —
  add `port` under that service.
- `"services.<name> declares unsupported runtime \"X\""` — re-check
  `get_supported_stacks`; the exact runtime name matters (`go`, not
  `golang`).
- `"no successful build exists for this application, and no source was
  given to build one..."` — pass `project_path` on the
  `deploy_application` call (see `SKILL.md`, Step 8) rather than assuming
  a build already happened.
- `"each service in deployment.yaml needs a folder of the same name in the
  project; missing: ..."` — the project's folders must be named like
  `deployment.yaml`'s services (`frontend/`, `api/`). Rename the folder or
  the service, re-validate, and deploy again.
- `"the packed source is N MiB, over the platform's 50 MiB limit"` —
  something large that shouldn't ship is in a service folder (data,
  media, generated files). Find it with the employee; don't delete files
  on your own.
- `"deletion is irreversible and requires explicit confirmation
  (\"confirm\": true)"` / a `delete_application` confirmation mismatch —
  ask the employee to state the application's exact name before calling
  `delete_application` again.
- A build failure's message includes the actual compiler/dependency
  error and a tail of the build log — relay the specific error, not just
  "the build failed."

## If something in this file looks wrong

`error.message` from the live tool call is always correct; this file is a
cache that can drift. Trust what the platform actually told you over what's
written here.
