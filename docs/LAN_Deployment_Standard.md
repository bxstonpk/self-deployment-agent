# LAN Deployment Standard — Claude Code machines deploying to the platform

How the platform is set up on the company LAN, and how every employee's
machine is set up so Claude Code can build and deploy development
applications through it. Unlike documents 01–17, this one describes what
is built and running, not requirements. Each claim names the script that
verified it.

## 1. The shape of it

```
 Employee's machine (Windows / macOS / Linux)            Platform host (Linux + Docker)
 ┌──────────────────────────────────────────┐   LAN    ┌─────────────────────────────────────────────┐
 │ Claude Code                               │          │ gateway :80  (nginx)                         │
 │   ├─ company-deployment skill             │  HTTP    │   /            Admin Portal                  │
 │   └─ Company Deployment MCP server ───────┼─────────▶│   /api/        Platform API ──▶ Postgres     │
 │        reads the machine's API key from   │          │   /<app>/      deployed applications         │
 │        ~/.company-deploy/credentials.json │          │   /downloads/  installer, MCP server, skill  │
 │ company-deploy (sign-in CLI)              │          │ Platform API ──▶ Docker: app containers,     │
 └──────────────────────────────────────────┘          │                  one MySQL per application   │
                                                        └─────────────────────────────────────────────┘
```

- One address for everything: the gateway. In examples below it is
  `http://platform-host:8080`.
- Nothing on an employee's machine talks to Docker, a database or the host
  directly — only to `/api/` through the MCP server, with the machine's
  own key.

## 2. The application standard

Every new application uses this stack. Starter code for each part is in
`company-deployment-skill/templates/`, which the installer puts on every
machine with the skill:

| Part | Standard | Runtime name in `deployment.yaml` |
|---|---|---|
| Frontend | React (Vite) | `react` |
| API | Python, JavaScript, TypeScript or Go | `python`, `nodejs`, `typescript`, `go` |
| Database | MySQL 8.4, one private instance per application | `database: type: mysql` |

Project layout and conventions (the templates already follow them):

```
my-app/
  deployment.yaml   # services: frontend (react) + api (<runtime>, port 8080); database: mysql
  frontend/         # npm run build -> build/, relative asset paths (Vite base: './')
  api/              # listens on $PORT, reads DATABASE_HOST/PORT/NAME/USER/PASSWORD
```

- **Addresses.** Deployed, an application is at `/<app>/` on the
  platform's address: the frontend at `/<app>/`, the API at
  `/<app>/api/`. Share that link; anyone on the LAN can open it. The bare
  `/<app>/` serves the service named `frontend` (or the only service);
  `/<app>/<service>/` reaches any other. The long form,
  `/run/<app>/<service>/`, still works. The Portal's pages and the
  gateway's own paths (`api`, `run`, `downloads`, `assets`,
  `applications`, `device`, `sign-in`, …) are reserved and can't be
  application names. The template frontend works out its API address from
  its own.
- **Credentials.** The platform generates the database password and
  injects it. Nobody writes a credential into source or
  `deployment.yaml`. Other secrets are set by the employee in the Admin
  Portal's **Secrets** section.
- **Images.** Runtime images carry no package manager, and every image
  must pass the CRITICAL-severity scan before it runs.

Verified: `services/platform-api/scripts/verify_lan_stack.py` deploys all
four combinations from the templates and checks each end to end (67
checks, including a real browser).

## 3. Setting up the platform host (IT, once)

**Requirements:**
- a Linux host with Docker Engine and the compose plugin;
- reachable from employees' machines on one TCP port (the gateway);
- outbound access to Docker Hub for base images, and to PyPI for
  machines installing the MCP server (or a mirror of each).

```bash
git clone <this repository> && cd Self-Deployment_Agent
cp .env.example .env
```

Set in `.env`:

| Variable | Value |
|---|---|
| `POSTGRES_PASSWORD` | a strong random value |
| `PLATFORM_SECRET_KEY` | `openssl rand -base64 32` — encrypts every stored secret; back it up, losing it makes every secret unreadable |
| `PLATFORM_ENROLLMENT_CODE` | the code you give employees so they can create an account (8+ characters) |
| `PLATFORM_ALLOWED_EMAIL_DOMAINS` | optional, e.g. `sti-th.com` |
| `GATEWAY_HOST_PORT` | the LAN port, e.g. `8080` |
| `PLATFORM_PUBLIC_BASE_URL` | the gateway as others reach it, e.g. `http://platform-host:8080` (application URLs are built from it) |
| `PLATFORM_PORTAL_URL` | the same address (machines' sign-in links point here) |
| `PLATFORM_AUTH_MODE` | leave as `token` |

```bash
docker compose up -d --build
curl http://platform-host:8080/api/healthz     # {"status":"ok"}
```

**Firewall.** Open only the gateway port to the LAN. Postgres is bound to
loopback. The Platform API's own port only needs to be reachable from the
host itself. Bind it to loopback with `PLATFORM_API_HOST_PORT=127.0.0.1:8090`.
A host firewall such as ufw won't stop it: Docker-published ports bypass it.

**Reboots.** The platform's services, and every application's database,
start again by themselves when the host reboots. Applications' own
containers are replaced by the health sweeper within about a minute.
`services/platform-api/scripts/verify_host_restart.py` checks this on the
host.

**Base images.** Builds use the images already on the host; nothing pulls
newer ones automatically. Refresh them on a schedule (FR-022):

```bash
docker pull node:22-alpine golang:1.25-alpine python:3.12-alpine alpine:3.21 nginx:1.30-alpine mysql:8.4 aquasec/trivy:latest
```

**Seal the encryption key with the host's TPM** (`DEC-006`). After the
first start, follow `deploy/host/README.md`:
1. Seal `PLATFORM_SECRET_KEY` with `tpm-seal-key.sh` and keep the escrow
   copy offline.
2. Enable the unseal-at-boot unit.
3. Remove the key from `.env`.
4. Always start with `-f docker-compose.yml -f
   deploy/host/docker-compose.tpm.yml`.

**Approving deployments.** To require approval for `dev` as well, set
`APPROVAL_REQUIRED_ENVIRONMENTS=dev,production`; the company host does.
Claude learns the rule from `get_platform_info`. Production deployments wait for
the administrator's decision, signed with a private key that never leaves
their machine (`DEC-017`).
1. Once, on the administrator's machine, after the installer: run
   `company-deploy approver keygen`, choose a passphrase, and keep an
   offline copy of the key file.
2. Put the line it prints in the host's `.env` as
   `PLATFORM_APPROVER_KEYS=…`, add the same email to
   `PLATFORM_ADMIN_EMAILS`, and run `docker compose up -d platform-api`.
3. From then on, `company-deploy approvals` lists what's waiting.
   `company-deploy review <id>` puts everything to read into one folder:
   - the exact source that was built, checked against the SHA-256 you
     sign;
   - the requester's `REVIEW.md` (workflow, structure and every file's
     path, data, what changed);
   - the test report, the scans and the text you'll sign.

   `company-deploy approve <id>` then signs (or `--reject --reason`).

   Or do all of it in the Admin Portal's **Approvals** page: read
   REVIEW.md and every file there, then pick the key file and type its
   passphrase to sign. The key is used in the browser tab only and never
   uploaded.
   A deployment that waits for approval without a `REVIEW.md` is refused
   before anything is built.

**Backups.** Every application database is backed up automatically
(`DEC-025`): a full every Sunday and a differential every other day, from
01:00 Asia/Bangkok. The previous week's set is deleted once a new full
succeeds. They are sealed with `PLATFORM_SECRET_KEY` and stored in the
`platform-backups` Docker volume. Copy that volume off the host on your
own schedule, and keep a copy of the key somewhere else: without it the
backups can't be opened. Owners restore from the Admin Portal
(**Backups** section).

**Rotating the enrollment code.** Change `PLATFORM_ENROLLMENT_CODE` and
run `docker compose up -d platform-api`. Existing accounts and keys are
unaffected.

## 4. Setting up an employee's machine (each employee, once)

Prerequisites:
- Claude Code installed and signed in;
- Python 3.11+;
- an account on the platform — create one in the Admin Portal with the
  enrollment code, or during step 2 below.

**Windows (PowerShell):**

```powershell
irm http://platform-host:8080/downloads/install.ps1 | iex
```

**macOS / Linux:**

```bash
curl -fsSL http://platform-host:8080/downloads/install.sh | sh
```

The installer:
1. checks it can reach the platform;
2. installs the MCP server and the `company-deploy` command in
   `~/.company-deploy/venv`;
3. installs the skill, with the templates, in
   `~/.claude/skills/company-deployment`;
4. registers the MCP server with Claude Code (user scope);
5. starts the sign-in. It prints a code and a link. Open the link, sign in
   with your own password, check the code matches and click
   **Approve**. The machine stores its own API key, which is never shown.

Running it again upgrades in place and keeps the machine signed in.

Verified:
- **Windows:** `install.ps1` was run for real in a throwaway user profile:
  installed, signed in, and `claude mcp list` reports the server
  connected. A re-run kept the same key.
- **Linux:** `install.sh` was run in a clean Linux container reaching the
  gateway as another LAN machine would. The credentials file is `600` in
  a `700` directory.
- **macOS:** not run on a Mac; the script uses only POSIX `sh`, `curl` and
  Python.

## 5. Deploying with Claude Code

Open the project in Claude Code and ask it to deploy, e.g. *"deploy this
to the dev environment as leave-tracker"*. The skill makes Claude:

1. **`whoami`** — if the machine isn't signed in, **`login`**, ask you to
   approve the code, then **`complete_login`**;
2. start from the templates for a new application; write
   `deployment.yaml`;
3. **`create_application`**, then **`validate_application`** against the
   live catalog;
4. **`deploy_application`** with **`project_path`** — the MCP server packs
   the service folders itself, leaving out `node_modules`, build output,
   `.git` and local secret files (`.env`, keys), and reports any it left
   out;
5. report the URLs (`/<app>/`, `/<app>/api/`).

**Verified with a real Claude Code session.** It was run headless with
only the MCP server and skill available. Given a project made of the React
and Go templates, it:
- wrote `deployment.yaml`;
- registered, validated and deployed the project to dev;
- reported the URLs.

The API answered through the gateway connected to its MySQL, and the
frontend was served. See section 8.

## 6. Security, in one place

- **Who can get an account:** anyone with the enrollment code and an email
  in an allowed domain. Passwords are bcrypt. Five wrong passwords lock
  an email out for 15 minutes.
- **Who a machine acts as:** the employee who approved its code, until its
  key expires (30 days) or is revoked in **Access keys**.
- **What a key can't do:** a key can't list, issue or revoke keys, or
  approve another machine. Those need the employee's own signed-in
  session, so Claude can't extend its own access.
- **What the platform stores:** hashes of keys and sessions, never the
  tokens. `verify_auth.py` checks a full database dump.
- **What a deployment can reach:** its own MySQL, on a private network no
  other application can reach.
- **Authorization:** owners only (`DEC-002`). There are no administrator
  roles.

## 7. Known gaps

- No SSO, MFA, or password reset (IT clears `users.password_hash`; the
  employee registers again with the code).
- Application containers also publish a random host port. An application
  is reachable on the host's address directly as well as through
  `/run/...`, bypassing the platform's request metrics. Applications have
  no platform authentication either way.
- One host, no high availability. Database backups (a full every Sunday,
  differentials Monday to Saturday, the previous week's deleted once a new
  full succeeds) are kept in the `platform-backups` volume on that same
  host. Copy the volume, and separately `PLATFORM_SECRET_KEY`, off the host.
- Resource quotas (Module M) are not enforced.
- Deploys run synchronously: the call returns when the deploy finishes.
- Machines need PyPI (or a mirror) to install the MCP server's
  dependencies. The gateway serves the MCP server itself, not its
  dependencies.

## 8. Verification index

| What | How |
|---|---|
| The four stacks build, deploy and run, incl. a real browser, isolation and password rotation | `services/platform-api/scripts/verify_lan_stack.py` |
| Authentication end to end, incl. the database holding no secret | `services/platform-api/scripts/verify_auth.py` |
| The MCP server over real stdio: sign-in, deploy, rebuild, rollback, secrets, logs, metrics, delete | `services/mcp-server/scripts/e2e_verify.py` |
| Gateway routes, installers on Windows and Linux, a real Claude Code deploy | described in sections 4–5; run by hand for PR "LAN onboarding" |

## 9. สรุปภาษาไทย (Quick start)

**ฝ่าย IT (ทำครั้งเดียวบนเครื่อง server ของแพลตฟอร์ม):**
1. ตั้งค่า `.env` ตามตารางในหัวข้อ 3:
   - `PLATFORM_SECRET_KEY`
   - `PLATFORM_ENROLLMENT_CODE`
   - `PLATFORM_PUBLIC_BASE_URL` และ `PLATFORM_PORTAL_URL` = `http://<ชื่อเครื่อง>:8080`
2. รัน `docker compose up -d --build`
3. เปิด firewall เฉพาะพอร์ต gateway
4. แจ้งรหัสลงทะเบียน (enrollment code) ให้พนักงาน

**พนักงาน (ทำครั้งเดียวต่อเครื่อง):**
1. ติดตั้ง Claude Code และ Python 3.11 ขึ้นไป
2. รันคำสั่งติดตั้ง:
   - Windows: `irm http://<ชื่อเครื่อง>:8080/downloads/install.ps1 | iex`
   - macOS/Linux: `curl -fsSL http://<ชื่อเครื่อง>:8080/downloads/install.sh | sh`
3. ตัวติดตั้งจะแสดงรหัสและลิงก์ ให้เปิดลิงก์ เข้าสู่ระบบด้วยบัญชีของตัวเอง (หรือสมัครด้วย enrollment code) ตรวจว่ารหัสตรงกัน แล้วกด **Approve**
4. เครื่องจะได้ API key ของตัวเองเก็บไว้ในเครื่อง โดยไม่แสดงให้ใครเห็น

**การ deploy:** เปิดโปรเจกต์ใน Claude Code แล้วสั่ง เช่น "deploy แอปนี้ขึ้น dev ชื่อ leave-tracker"

มาตรฐานของแอป:
- **Frontend:** React
- **Backend (เลือกหนึ่ง):** Python, JavaScript, TypeScript หรือ Go
- **ฐานข้อมูล:** MySQL
- **โครงโปรเจกต์:** ใช้ templates ที่ติดมากับ skill

ถ้าเครื่องหาย หรือเลิกใช้เครื่องนั้น ให้เข้า Admin Portal ที่หน้า **Access keys** แล้วกด **Revoke** key ของเครื่องนั้น
