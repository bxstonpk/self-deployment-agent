# Company AI Application Deployment Platform

An internal, self-service platform for deploying the applications employees
build with Claude Code. An employee asks Claude to deploy. Claude follows the
Company Deployment Skill and calls the Company Deployment MCP server. The
platform validates, builds, scans, deploys and runs the application, with no
one-off IT work per deployment.

```
Employee → Claude Code → Company Deployment Skill → Company Deployment MCP
         → Platform API → Build / Image scan / Deploy → Docker → Application
```

The business case and requirements are in [`docs/01_BRD.md`](docs/01_BRD.md).
This README is the map: what exists, where it lives, and how to run it.
Each component's own README has the implementation detail, its gaps, and
exactly how it was verified.

## Deployment context

Internal-only, for one company's employees. The platform runs on **a single
on-prem Linux host with Docker**, reachable only from the company LAN: no
cloud, no Kubernetes. Each decision behind that is recorded in
[`docs/17_Decision_Log.md`](docs/17_Decision_Log.md). The ones that shape
the system most:

| Decision | What it means here |
|---|---|
| `DEC-004` | One Docker host, not a cluster |
| `DEC-001` / `DEC-003` (revised 2026-10-23) | The platform authenticates people itself. Employees register with a password and IT's enrollment code. **Each Claude Code machine fetches its own API key**, approved by its employee, **before it can deploy**. No corporate SSO. |
| `DEC-002` + `DEC-017` | Permissions are application ownership, plus one role: the **platform administrator**, who approves production deployments by **signing with a private key only they hold** |
| `DEC-014` | No resource quotas |
| `DEC-025` | Database backups: a full every Sunday, differentials Monday–Saturday, the previous week deleted after the next full |
| `DEC-027` | Application logs kept 90 days |
| `DEC-028` | Alert thresholds, and the rhythm: on start, on escalation, at most hourly while ongoing, on clear |
| `DEC-006` | The platform's encryption key is sealed with the host's TPM |
| `DEC-007` | No per-application DNS: applications live at `/<app>/` on the platform's address (`/<app>/api/` for the API) |
| `DEC-029` | The company standard applies from the start of an application, not only at deployment: a profile, allowed libraries, exceptions decided by an administrator, tests run by the platform, and a dev copy per application that deploys without approval |
| `DEC-030` | A platform administrator can suspend any application, with a reason, and only an administrator can resume it |
| `DEC-032` | Applications' APIs are RESTful, pages are paths (the build refuses `/#/` routing), and searches and filters live in the URL's query |

## The application standard

New applications use:
- a **React** frontend;
- one API in **Python, JavaScript (Node.js), TypeScript or Go**;
- a **MySQL** database, which the platform provisions per application.

PostgreSQL, Vue and Next.js are also supported. Starter code for every part
is in [`company-deployment-skill/templates/`](company-deployment-skill/templates/),
and the installer puts it on every Claude Code machine with the skill.

## What is built

Everything below has been verified against a running system: real Docker
builds, real HTTP, real databases, a real headless browser, real MCP over
stdio, and a real Claude Code session. Each claim is backed by a script
(see [Verification](#verification)).

| Area | What it does | Detail |
|---|---|---|
| Application lifecycle | Draft → Validated → Build → Deploying → Running → Suspended / Rolled back → Archived → Deleted, enforced server-side | `services/platform-api` |
| Build and deploy | One governed build recipe per runtime, with no package manager in runtime images. Every image must pass a CRITICAL-severity Trivy scan. Health checks run before traffic, and redeploys never take down a working version. | platform-api: How Build / Deploy work |
| Scale-to-zero | Idle services stop; the first request cold-starts them at the same stable address | Module L |
| Health and rollback | Running instances are re-checked continuously and replaced when unhealthy. A version that keeps failing right after release is rolled back automatically. | Modules R, V |
| Databases | A dedicated MySQL or Postgres per application, on its own private network. The generated password is never in source, and can be rotated. | Module N |
| Backups | The weekly schedule above, sealed with the platform key, restorable by an owner (who confirms the name) after a safety backup | platform-api: How Database Backups work |
| Secrets | Write-only values, AES-256-GCM encrypted, injected at container start, never returned by any API or MCP tool | Module O |
| Logs and metrics | Every line an application prints, with secrets redacted, kept 90 days. CPU, memory, requests, errors and latency are measured by the platform itself. | Modules S, T |
| Alerts | Error rate, latency, CPU, memory and host disk against `DEC-028`'s thresholds. In-app delivery: owners get application alerts, administrators get platform alerts. | platform-api: How Alerts work |
| Authentication | Passwords, Portal sessions, and per-machine API keys from a device approval. A key can't mint keys or approve devices. Only hashes are stored. | platform-api: How authentication works |
| Signed approval | Production deployments wait for the administrator's Ed25519 signature over the exact deployment, images, source archive, test report and the requester's REVIEW.md. `company-deploy review` downloads every file first. | platform-api: How signed approvals work |
| Development governance | A new application is registered with a profile (purpose, users, personal data, stack) before any code, and starts from the templates. Libraries the company forbids are refused at build time. Anything outside the standard needs an exception, which an administrator decides in the Portal. | platform-api: How development governance works |
| Tests on the platform | The platform runs an application's tests (Go, Python, Node) in the build sandbox, so a machine needs no toolchain. A production deployment carries the report only if the run passed on the same source. | platform-api: How tests run on the platform |
| Dev copies | Each application has one long-lived dev copy, `<app>-dev`, with its own database and address. A new application is deployed there at once with a link, and every change goes there without approval, keeping its data. A changed `deployment.yaml` (a service added, say) is applied in place, keeping the data. The first production deployment can take the dev data if the employee says it is real, as part of what the approver signs. Claude records test data it adds itself. | platform-api: How dev copies work |
| Database memory | MySQL runs with a small configuration and sleeps with its application, waking on the next request. A failed application that never ran is removed at once, its name freed, and its owners told to deploy again. | platform-api: How databases save memory, and failed applications are removed |
| Administrator's suspension | An administrator suspends any application from the Dashboard, with a reason the owners are told. The owners can't resume, archive, delete or redeploy it until an administrator resumes it. | platform-api: How an administrator's suspension works |
| Client updates | Each gateway build publishes one release of the MCP server and skill. A machine that is older is refused until it updates: Claude runs the installer's update and asks for a restart of Claude Code. | mcp-server: Keeping a machine up to date |
| Audit, notifications, reports | A hash-chained, append-only audit log; an in-app inbox; inventory and deployment-activity reports | Modules W, X, AB |
| Ownership | Co-owners and contributors; a primary-ownership transfer the new owner must accept | Module E |
| MCP server | 34 tools: sign-in, discovery, the standard (start, scaffold, check, exceptions), tests, dev copies, lifecycle, deploy from a project folder (`.env` files and keys never uploaded), status with per-service URLs and active alerts, logs, metrics, audit, ownership | `services/mcp-server` |
| Skill | How Claude must use the MCP server: update and sign in first, start from the company standard, show every change on dev, test before production, never handle secrets or infrastructure | `company-deployment-skill` |
| Administrators' dashboard | A task manager for the platform, for `PLATFORM_ADMIN_EMAILS` only: the host's CPU, memory, load and disk with 15-minute charts, traffic across every application, and every application with its owner, instances, CPU, memory, requests, errors and latency, refreshed every 5s | platform-api: How the administrators' dashboard works |
| Admin Portal | Every owner action above for a person. Also: machine approval, access keys, backups and restore, alerts, departments. For administrators: the Dashboard (with suspend and resume) and the Standards page (dependency rules and exceptions). | `apps/admin-portal` |
| LAN gateway and installers | One port serves the Portal, API, applications and downloads. A one-line installer per machine (Windows, macOS/Linux) sets up the MCP server, the skill and sign-in. | `deploy/gateway`, `docs/LAN_Deployment_Standard.md` |
| TPM-sealed key | The encryption key is sealed with the host's TPM, unsealed into RAM at boot, with an offline escrow copy | `deploy/host` |

## Getting started

**IT, on the platform host (once)**: follow
[`docs/LAN_Deployment_Standard.md`](docs/LAN_Deployment_Standard.md), section 3:
1. Fill in `.env` (secret key, enrollment code, the gateway's address,
   administrators).
2. Run `docker compose up -d --build`.
3. Open only the gateway port to the LAN.
4. Seal the key with the TPM ([`deploy/host/README.md`](deploy/host/README.md)).
5. Set up the administrator's approver key (`company-deploy approver keygen`).

**Each employee (once per machine):**

```powershell
irm http://<platform-host>:8080/downloads/install.ps1 | iex           # Windows
```
```bash
curl -fsSL http://<platform-host>:8080/downloads/install.sh | sh     # macOS / Linux
```

Then approve the code it shows in the Admin Portal. From then on, ask Claude
Code to build or deploy an application. A new one starts from the company
standard and is up on its dev copy (`/<app>-dev/`) with a link straight
away; every change goes there. Production is deployed when the employee
asks, after the administrator signs. When the platform is updated, Claude
updates the machine first (machines installed before 2026-10-26 need the
installer run once by hand).

**Developers, on a workstation:** Docker, Go 1.25+, Python 3.11+, Node 20+.

```bash
cp .env.example .env
# set PLATFORM_SECRET_KEY (openssl rand -base64 32) and PLATFORM_ENROLLMENT_CODE (8+ characters)
docker compose up --build          # postgres, platform-api (:8090) and the gateway (:8080)
```

- Open `http://localhost:8080` to create an account.
- The Portal can also run from source (`apps/admin-portal`, `npm run dev`),
  as can the MCP server (`services/mcp-server`, `pip install -e ".[dev]"`,
  then `company-deploy login --url http://localhost:8090`). See their
  READMEs.
- Deployed applications run as `platform-run-*` / `platform-db-*`
  containers on the same Docker daemon. `docker compose down` doesn't
  remove them; deleting the application does.

## Repository map

```
docs/                       The original BRD, requirements and design (01–17), the
                            Decision Log, and the LAN Deployment Standard.
services/platform-api/      Go. The only component that touches Postgres and Docker.
                            Everything else is a client of its API.
services/mcp-server/        Python. The MCP server Claude Code uses, and the
                            `company-deploy` CLI (sign-in, approvals).
company-deployment-skill/   Markdown. The skill Claude Code follows, the deployment.yaml
                            schema, and the starter templates.
apps/admin-portal/          React + TypeScript. The web UI for people.
deploy/gateway/             nginx: the one LAN address; builds the Portal and the
                            per-machine installers.
deploy/host/                The platform host: TPM sealing of the encryption key.
```

## Verification

Each script runs against the live compose stack and cleans up after itself.
Some restart `platform-api` with temporary settings and restore them
afterwards.

| Script | Proves |
|---|---|
| `services/platform-api/scripts/verify_lan_stack.py` | React + each of Python/JS/TS/Go + MySQL, built from the templates, deployed and used end to end, including in a real browser |
| `services/platform-api/scripts/verify_auth.py` | Registration, sign-in, device approval, key limits, revocation, audit; and a `pg_dump` containing no token or password |
| `services/platform-api/scripts/verify_backups.py` | Scheduled full, differential, exact restore, weekly retention, opt-out, deletion, on MySQL and Postgres |
| `services/platform-api/scripts/verify_alerts.py` | The alert rhythm on real traffic and real CPU load; platform alerts to administrators |
| `services/platform-api/scripts/verify_signed_approval.py` | Production waits for a signature from a real key; forged or replayed signatures are refused; approve and reject both work |
| `services/platform-api/scripts/verify_host_restart.py` | After the Docker daemon restarts (as on a reboot), the platform and a deployed application come back by themselves, with the application's database and its data intact. Run on the platform host; it needs passwordless sudo. |
| `services/platform-api/scripts/verify_admin_hold.py` | An administrator's suspension: only administrators, a reason, owners refused until an administrator resumes, a held dev copy |
| `services/mcp-server/scripts/e2e_verify.py` | All 34 MCP tools over real stdio, including sign-in, deploy from a folder, rebuild and rollback |
| `services/mcp-server/scripts/verify_governance.py` | Starting an application, scaffolding, a forbidden library refused, an exception, the real build |
| `services/mcp-server/scripts/verify_test_runs.py` | Tests run by the platform: pass, fail, busy, the tested deploy, the approver's report (about 10 minutes) |
| `services/mcp-server/scripts/verify_dev_copies.py` | Dev copies: own database and address without approval, data kept, a service added in place, dev data into the first production as signed, reset, archive |
| `services/mcp-server/scripts/verify_db_memory.py` | MySQL sized small, asleep with its application and woken on demand; failed applications that never ran removed with their names freed |
| `services/mcp-server/scripts/verify_client_update.py` | An outdated machine is refused, updates itself with the command it is given, and works after a restart (needs the gateway) |
| `deploy/host/verify-tpm.sh` | Seal and unseal against a software TPM 2.0: bound to the TPM, escrow recovery, PCR binding |
| `verify_module_n/o/r/s/t.py`, `verify_stable_url.py`, `verify_fr099_rollback.py` | Databases, secrets, health remediation, logs, metrics, stable URLs, automatic rollback. These older scripts use the self-declared identity, so they need `PLATFORM_AUTH_MODE=dev-header`. |

Unit tests: `go test ./...` (platform-api), `pytest` (mcp-server), and
`npx vitest run` (admin-portal).

## Known gaps

**Decided against for this deployment** (not pending work):
- corporate SSO and MFA;
- roles beyond ownership plus the approver;
- per-application DNS/TLS (`DEC-007`);
- a container registry (`DEC-005`);
- Kubernetes (`DEC-004`);
- resource quotas (`DEC-014`).

**Open**:
- **No external alert channel.** Alerts and notifications are in-app only:
  no e-mail, Slack, Teams or LINE until one is chosen (`DEC-018`).
- **Metrics are kept forever:** their retention (`NFR-032`) is undecided.
- **No rotation of the encryption key.** There is no re-encryption tool,
  so changing the key makes stored secrets and backups unreadable. Root on
  the running host can read the unsealed key.
- **The TPM scripts have only run against a software TPM.** Run them once
  on the real host's chip.
- **Everything is on one host.** Backups live on the same host as the
  databases, so copy the `platform-backups` volume and the escrowed key
  elsewhere. There is no high availability.
- **Deploys run synchronously.** That's fine at this scale, but there is
  no job queue.
- **No password reset.** IT clears the account's password and the
  employee registers again.
- **Application containers also publish a random host port.** They can be
  reached around the platform's proxy, which bypasses its request metrics.
- **Machines need PyPI or a mirror** to install the MCP server's
  dependencies.
- **Updating a machine needs a restart of Claude Code.** The MCP server
  can't reload itself. Machines installed before the version check
  (2026-10-26) need the installer run once by hand.
- **Nothing reconciles orphaned database containers.** One left by a
  platform restart in the middle of provisioning was found on the company
  host and removed by hand.
- **An administrator's suspension covers one application.** Its dev copy
  is suspended separately.
- **Smaller gaps:**
  - cross-application connection attempts are blocked but not logged as
    policy violations (Module Q);
  - log levels aren't parsed;
  - latency has means, not percentiles;
  - the Portal is the application catalog (MOD-19), not a full
    administration portal.

Each component README lists its own gaps in more detail.

## Contributing

Every change goes through a feature branch and a pull request into `main`.
Verify against the running system, not only with unit tests, and update the
READMEs to say what was verified and what wasn't. Commit messages record the
real bugs that verification found.

When PRs are stacked (each based on the one before), merging one into its
base branch does not bring it to `main`. Check `git log origin/main` after
merging a stack.
