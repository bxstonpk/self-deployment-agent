# Troubleshooting — error codes to plain language

Every MCP tool failure carries `error.code` (one of a fixed set) and
`error.message` (a specific, human-readable explanation — always read the
actual message; it's written to be specific, not a template to work around).

| `error.code` | What it means | What to do |
|---|---|---|
| `VALIDATION_ERROR` | The request is malformed, or fails a structural/business rule (bad `deployment.yaml` field, unknown department name, wrong lifecycle state for this operation, a build's source failed to compile) | Surface the specific reason from `error.message`/`error.details` to the employee in plain language. Don't retry with guessed values — only resubmit once the employee has confirmed a correction |
| `UNSUPPORTED_STACK` | A declared runtime isn't on the current supported list | Re-check `get_supported_stacks` (live, not cached). Ask the employee whether to change the stack or request an exception through IT — never silently substitute a "close enough" runtime |
| `QUOTA_EXCEEDED` | A resource limit was hit (today: mainly source-archive size — 50MiB) | Tell the employee; for an oversized archive, check for accidentally-included `node_modules`/`vendor`/build artifacts that shouldn't be in the upload |
| `UNAUTHORIZED` | Missing/invalid session identity, or the identity isn't an owner of the target application | Don't retry under a different claimed identity. Tell the employee they (or an existing owner) need to grant them ownership, or that re-authentication is needed |
| `NOT_FOUND` | The referenced application, deployment, or department doesn't exist | Confirm the identifier with the employee — never fabricate or guess one. For `rollback_application`, this can mean there's no prior successful version to roll back to |
| `CONFLICT` | An `idempotency_key` was reused with different input, or a build/deploy is already in flight for this application | Check current state via `get_application_status`/`get_deployment_status` before retrying; surface the conflict to the employee if it doesn't self-resolve |
| `RATE_LIMITED` | Too many calls in a time window (not currently enforced by this platform, but handle it in case that changes) | Back off; never tight-loop retry |
| `PENDING_APPROVAL` | **Not a failure.** A production-affecting action is queued for human approval | Tell the employee it's queued and by what process; poll `get_deployment_status` rather than treating this as an error — see `SKILL.md`'s "Handling production approval" |
| `INTERNAL_ERROR` | Either an unexpected platform-side failure, or a deliberate, honest "this capability doesn't exist yet" (see below) | For a genuine unexpected failure: tell the employee plainly, suggest retrying once. For the deliberate cases below: don't retry, they won't start working |

## `INTERNAL_ERROR`s that mean "not built yet," not "broken"

Two specific tools always return `INTERNAL_ERROR` with a message
explaining exactly this — don't retry them, and don't report them to the
employee as if something is wrong with their request:

- **`get_application_logs`** — application log storage doesn't exist on
  this platform yet (no Logging module).
- **`get_application_metrics`** — application metrics storage doesn't
  exist on this platform yet (no Monitoring module).

If an employee needs deeper diagnosis than `get_deployment_status`'s
failure detail provides, say so honestly: this platform doesn't have log
or metric inspection through the MCP (or at all) yet.

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
- `"no successful build exists for this application, and no
  source_archive_base64 was given to build one..."` — pass
  `source_archive_base64` on the `deploy_application` call (see
  `SKILL.md`, Step 8) rather than assuming a build already happened.
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
