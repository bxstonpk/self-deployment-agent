# Deployment Lifecycle — what `get_deployment_status` phases mean

**This is a cache, not a source of truth.** The live `data.phase` value
from `get_deployment_status` is authoritative; this file just explains what
each value means in plain language.

## Deployment phases

| `data.phase` | Meaning |
|---|---|
| `IMAGE_SCAN` | Built image(s) are being scanned for vulnerabilities |
| `PENDING_APPROVAL` | A production deploy is waiting on human approval — not a failure, see `SKILL.md`'s "Handling production approval" |
| `DEPLOYING` | Container(s) starting |
| `HEALTH_CHECK` | Waiting for the new container(s) to respond healthily |
| `COMPLETED` | Terminal success — traffic is live on this version. `data.result.url` carries the reachable address when applicable |
| `FAILED` | Terminal failure — `data.result.reason` carries why |
| `UNKNOWN` | The underlying deployment record is in a state this skill package's phase mapping doesn't recognize; treat as "ask `get_application_status` for the ground truth" rather than guessing |

**Important, real-world detail as of this writing**: `deploy_application`
and `rollback_application` run their entire pipeline synchronously within
the call itself and return the terminal phase directly — there is
currently no meaningful in-between polling window where you'd observe
`IMAGE_SCAN`/`DEPLOYING`/`HEALTH_CHECK` from a separate `get_deployment_status`
call for the *same* deploy that's still in flight, because by the time the
tool call returns, it's already done. Still call `get_deployment_status`
afterward as your confirmation step (`SKILL.md` Step 9) — it's cheap,
correct, and is what keeps this procedure working unchanged if the
platform becomes genuinely asynchronous later. The one phase you'll
actually observe mid-flight in practice today is `PENDING_APPROVAL`, since
that's a real pause waiting on a human, not internal pipeline work.

## Application lifecycle states

`get_application_status`'s `data.current_lifecycle_state` reflects the
application's overall state, independent of any one deployment:

| State | Meaning |
|---|---|
| `draft` | Registered, `deployment.yaml` not yet validated |
| `validated` | Passed validation, not yet built/deployed |
| `build` | A build is in progress, or has completed and is ready to deploy — **if this application was already `running` before this build started, its previous version is still live and serving traffic**; `build` here doesn't mean downtime |
| `deploying` | A deploy is actively in progress (transient) |
| `running` | Live, serving traffic |
| `suspended` | Traffic/compute stopped, config retained — reversible via `restart_application`'s sibling tools (not in this tool set directly; see the platform's own lifecycle tools if exposed in a future MCP revision) |
| `rolled_back` | Transient — a rollback is landing the application back on `running` |
| `failed` | The most recent deploy or build attempt failed and there was no prior good version to fall back to. **If a *previous* version was already running, a failed rebuild or redeploy attempt leaves the application at `running`, not `failed`** — `failed` only appears when there was nothing good to protect |
| `archived` | Compute released more permanently than `suspended`; not directly resumable |
| `deleted` | Terminal — no further action possible |

## What a failed rebuild/redeploy does and doesn't do

A build or deploy attempt that fails **never** tears down an
already-`running` previous version. This holds whether the failure is a
compile error, a failed health check, or anything else in the pipeline —
the currently-live version is only ever replaced by a *successful*
new one. If `get_application_status` still reports `running` after a
failed `deploy_application` call, that's correct and expected, not a bug —
it means the previous version is still live and the failed attempt simply
never went anywhere.
