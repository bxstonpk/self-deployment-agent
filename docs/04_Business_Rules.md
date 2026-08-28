# 04 — Business Rules

**Document ID:** BR-DOC-04
**Project:** Company AI Application Deployment Platform
**Version:** 0.1 (Draft Baseline)
**Status:** For Review
**Owner:** Senior Solution Architect
**Date:** 2026-08-28
**Related Documents:** `01_BRD.md`, `02_Functional_Requirements.md`, `03_Non_Functional_Requirements.md`, `06_System_Requirements.md`, `11_Security_Requirements.md`, `17_Decision_Log.md`

---

## 1. Purpose and How to Read This Document

Business Rules are the invariant policy constraints that govern the Company AI Application Deployment Platform's behavior **regardless of how any given feature is implemented**. Where `02_Functional_Requirements.md` describes *what the system does* (a flow, an input/output, an actor interaction) and `03_Non_Functional_Requirements.md` describes *how well it does it* (a measurable quality target), this document describes *what is always true or never allowed*, independent of any specific feature or screen.

Every rule has an ID (`BR-001`, `BR-002`, …), assigned **sequentially across the whole document**, grouped by category. Category groupings do not restart the numbering.

Each rule is documented with:

- **Statement** — the rule itself, stated as an invariant.
- **Rationale** — why the rule exists.
- **Applies To** — the actor(s) and/or system module(s) the rule governs.
- **Enforcement Point** — where in the architecture the rule is actually checked/enforced (never left as "policy on paper" — every rule below is tied to a concrete enforcement point).
- **Exceptions** — any legitimate, explicitly governed exception, or "None."

Where a rule depends on an exact numeric value or a specific governance decision this document is not positioned to invent, the rule states the invariant and marks the missing input as **TBD**, consistent with `03_Non_Functional_Requirements.md`. TBD items are collected in Section 3 and will be tracked in `17_Decision_Log.md`.

Actors, system modules (`MOD-01`…`MOD-19`), and FR module references (`FR Module A`…`AB`) used below follow the naming fixed in `01_BRD.md`, `02_Functional_Requirements.md`, and `06_System_Requirements.md` respectively — this document does not invent new IDs for those.

---

## 2. Business Rules

### 2.1 Application Ownership & Governance

#### BR-001 — Every Application Has Exactly One Owner and One Department

| Field | Detail |
|---|---|
| Statement | Every application registered on the platform must have exactly one accountable Application Owner (an individual) and exactly one Department at all times. An application must never exist in an unowned or unattributed state. |
| Rationale | A single point of accountability is required for cost attribution, approval decisions, incident response, and lifecycle decisions; shared or absent ownership diffuses responsibility and slows every subsequent governance action. |
| Applies To | Application Owner; Application Registry (MOD-02); FR Module E (Application Ownership) |
| Enforcement Point | Application Registry, at `create_application` registration and re-validated before any subsequent deployment-affecting action. |
| Exceptions | None. |

#### BR-002 — Ownership Transfer Must Never Create an Ownership Gap

| Field | Detail |
|---|---|
| Statement | Application ownership may be transferred between employees, but the outgoing Application Owner remains accountable until the incoming owner explicitly accepts ownership; there is never a period where an application has zero owners. |
| Rationale | Protects against accidental orphaning of applications during transfers, role changes, or reorganizations, which would otherwise leave production systems without an accountable party. |
| Applies To | Application Owner; Application Registry (MOD-02); IT Administrator |
| Enforcement Point | Application Registry ownership-transfer workflow (atomic transfer, not a two-step remove-then-add). |
| Exceptions | IT Administrator may force-reassign ownership when the current owner has left the company; the action is fully audited (see BR-030). |

#### BR-003 — Only the Owner (or a Delegated Administrator) Governs Application Lifecycle

| Field | Detail |
|---|---|
| Statement | Only the current Application Owner, or a Platform/IT Administrator acting explicitly on the owner's behalf, may request production promotion, suspension, or decommissioning of an application. |
| Rationale | Prevents an unauthorized employee — including one with unrelated deployment permissions on other applications — from affecting an application's production status. |
| Applies To | Application Owner; Platform Administrator; IT Administrator; Deployment Manager (MOD-03) |
| Enforcement Point | Platform API authorization layer, on all lifecycle-changing endpoints (promote, suspend, decommission). |
| Exceptions | Security Administrator may force-suspend any application platform-wide in response to an active security incident, independent of owner consent; the action is fully audited. |

### 2.2 Stack & Technology Policy

#### BR-004 — Unsupported Technology Fails Validation Before Any Deployment Attempt

| Field | Detail |
|---|---|
| Statement | Only technologies present on the current, IT-governed Supported Stack list may pass `validate_application`. An application declaring an unsupported runtime, database, or cache in its `deployment.yaml` fails validation and is blocked from proceeding to build or deploy. |
| Rationale | Prevents uncontrolled technology sprawl and unsupportable production incidents; validating *before* build/deploy avoids wasting build resources on an application that could never be deployed anyway. |
| Applies To | Employee / AI Coding Agent; Validation Engine (MOD-04); FR Module F (Stack Management), FR Module H (Deployment Validation) |
| Enforcement Point | Validation Engine, at `validate_application` and again at `deploy_application` (re-checked, not solely relied upon from an earlier validation), prior to any Build Engine invocation. |
| Exceptions | None — no deployment path, including a claimed production hotfix, bypasses stack validation. |

#### BR-005 — Supported Stack Changes Are Config-Driven IT Governance, Not a Platform Release

| Field | Detail |
|---|---|
| Statement | Additions or removals from the Supported Stack list are an IT Administrator governance action, effective platform-wide upon publication, and must never require a change to Platform API, MCP Server, or Validation Engine source code. |
| Rationale | Keeps stack governance agile and decoupled from the platform's own release cycle, per the project's requirement that IT be able to add/remove supported technologies "without changing the entire platform." |
| Applies To | IT Administrator; FR Module F (Stack Management) |
| Enforcement Point | Stack Management configuration store, read by the Validation Engine at every validation/deploy check. |
| Exceptions | None. |

#### BR-006 — Deprecated-Stack Applications May Keep Running but Not Redeploy

| Field | Detail |
|---|---|
| Statement | An application already running on a stack entry that is later removed from the Supported Stack list may continue running as-is, but cannot be redeployed, updated, or promoted to a new environment until migrated to a currently supported stack. |
| Rationale | Avoids abruptly breaking a running application when governance tightens the supported list, while still preventing further investment (new deployments) in technology IT no longer supports. |
| Applies To | Application Owner; Validation Engine (MOD-04); Deployment Manager (MOD-03) |
| Enforcement Point | Validation Engine, re-evaluated on every deployment attempt against the *current* supported list (not the list at original registration time). |
| Exceptions | Platform Administrator may grant a time-boxed migration exception with a defined end date, after which the deprecated-stack application is blocked from redeploy per the base rule. |

### 2.3 Deployment Approval Policy

#### BR-007 — Production Deployments Require Explicit Approval

| Field | Detail |
|---|---|
| Statement | A production-environment deployment must not proceed to the Deployment Controller until it has received explicit approval from an authorized approver. |
| Rationale | Production changes carry direct business risk (outage, data exposure, cost); the project context explicitly requires that production deployments "may require explicit approval," and this document treats that as a firm baseline rule rather than an optional feature. |
| Applies To | Application Owner; Platform Administrator; Security Administrator; Deployment Manager (MOD-03) |
| Enforcement Point | Platform API authorization layer, gating `deploy_application` whenever the target environment is `production`. |
| Exceptions | None — Platform and IT Administrators cannot bypass the approval gate for their own applications; they may only serve as the approver of record where policy designates them as such. Exact designation of who is authorized to approve production for a given application/department is **TBD** (see Section 3). |

#### BR-008 — Development Deployments May Auto-Deploy

| Field | Detail |
|---|---|
| Statement | Development-environment deployments may proceed automatically without manual human approval, provided automated validation, stack, and security policy checks all pass. |
| Rationale | Preserves the platform's core self-service value proposition for low-risk, non-production work; requiring manual approval for every dev iteration would recreate the IT-ticket bottleneck the platform exists to remove. |
| Applies To | Employee / AI Coding Agent; Deployment Manager (MOD-03); Validation Engine (MOD-04) |
| Enforcement Point | Deployment Controller, on the `environment = development` deployment path. |
| Exceptions | A Department or Application Owner may opt into requiring manual approval even for development deployments, at IT Administrator's discretion (e.g., for a sensitive internal tool). |

#### BR-009 — A Production Approval Does Not Carry Over to a Materially Changed Deployment

| Field | Detail |
|---|---|
| Statement | An approval granted for a specific application version and configuration does not automatically apply to a materially different version. Changes to resource tier, scaling bounds, domain visibility, secrets configuration, or database configuration invalidate a prior production approval and require re-approval before the changed deployment may proceed. |
| Rationale | Prevents "approval drift," where an approval granted for a low-risk change is used as implicit cover for a subsequently altered, higher-risk configuration that was never actually reviewed. |
| Applies To | Application Owner; Deployment Manager (MOD-03); DeploymentApproval (data entity) |
| Enforcement Point | Deployment Manager, at deploy-time, comparing the requested manifest against the manifest hash/version that was actually approved. |
| Exceptions | None. |

### 2.4 Environment Policy (Dev / Staging / Production)

#### BR-010 — Every Deployment Belongs to Exactly One Defined Environment

| Field | Detail |
|---|---|
| Statement | Every deployment must be associated with exactly one defined environment (development, staging, or production). Platform policy — approval requirements, resource quotas, monitoring rigor, data isolation — is applied per-environment, and no policy is ever applied globally across environments. |
| Rationale | Differentiated risk controls (per BR-007/BR-008) are only meaningful if environment is an unambiguous, always-present property of every deployment, not an optional or inferred one. |
| Applies To | All actors; Deployment Manager (MOD-03); Environment (data entity) |
| Enforcement Point | Platform API request schema validation (environment is a required field) and Deployment Controller routing logic. |
| Exceptions | None. |

#### BR-011 — Production and Non-Production Are Fully Isolated

| Field | Detail |
|---|---|
| Statement | Production and non-production environments must be fully isolated from one another at the network, credential, and database level. A development or staging deployment must never connect to a production database or reuse a production secret, and vice versa. |
| Rationale | A compromised, buggy, or experimental non-production deployment must never be able to reach or affect production data — this is a hard boundary, not a best-effort convention. |
| Applies To | Database Manager (MOD-09); Secret Manager (MOD-08); Resource Manager (MOD-07) |
| Enforcement Point | Secret Manager and Database Manager provisioning logic, which issue secrets/credentials scoped strictly to (application, environment); verified by the Validation Engine. |
| Exceptions | None. |

#### BR-012 — Staging Gate Policy Is Pending Decision

| Field | Detail |
|---|---|
| Statement | Whether staging is a mandatory gate between development and production, and the exact automated/manual checks required at each promotion step, is not yet decided. Until decided, the platform's default policy is that the production approval gate (BR-007) is the only mandatory promotion gate; staging, where used, is available but not enforced as compulsory. |
| Rationale | The project context does not specify a mandatory staging gate. This document intentionally states the default explicitly rather than silently assuming a stricter policy that governance has not actually approved, consistent with the instruction not to invent unstated business decisions. |
| Applies To | Platform Administrator; Deployment Manager (MOD-03) |
| Enforcement Point | Deployment Manager promotion workflow (pending final policy definition — see Section 3). |
| Exceptions | N/A — TBD. |

### 2.5 Security & Isolation Rules

#### BR-013 — No Application May Access Another Application's Secrets, Database, or Network

| Field | Detail |
|---|---|
| Statement | An application must never be granted access to another application's secrets, database, or internal network endpoints — regardless of shared owner, shared department, or any other relationship — unless routed through an explicit, approved, platform-mediated integration mechanism. |
| Rationale | Directly required by the project's security context. Cross-application trust must never be implicit or ambient; "same team owns both apps" is not, by itself, a basis for access. |
| Applies To | Secret Manager (MOD-08); Database Manager (MOD-09); Resource Manager (MOD-07); all applications |
| Enforcement Point | Network policy / namespace isolation applied by the Resource Manager and Deployment Controller at deploy-time; verified by an automated cross-tenant isolation test suite on every platform release (see NFR-023). |
| Exceptions | A documented, explicitly approved cross-application integration (e.g., a shared internal API between two owned services) may be provisioned — this is a deliberate, audited, addressed integration, never ambient access. |

#### BR-014 — Containers Never Run Privileged or Mount Host Resources

| Field | Detail |
|---|---|
| Statement | Application containers must never run in privileged mode, mount the Docker socket, or mount the host filesystem, under any environment, tier, or approval level. |
| Rationale | These are the highest-severity container-escape and host-compromise vectors; the project context lists this as an absolute prohibition, not a configurable policy. |
| Applies To | Build Engine (MOD-05); Deployment Controller (MOD-06); Validation Engine (MOD-04) |
| Enforcement Point | Policy-as-code admission gate in the Deployment Controller, evaluated for every deployment regardless of approval status or requester role. |
| Exceptions | None — there is no self-service or administrative override for this rule at the application layer. |

#### BR-015 — Internal Databases Are Never Directly Exposed

| Field | Detail |
|---|---|
| Statement | An application must not directly expose its backend database on a public or internal-wide-reachable network address. Database access is only reachable from that application's own service layer. |
| Rationale | Directly required by the project's security context ("prevent apps from exposing internal databases"); a direct database exposure bypasses the application's own access-control logic entirely. |
| Applies To | Database Manager (MOD-09); Domain Manager (MOD-10); FR Module Q (Networking) |
| Enforcement Point | Domain Manager / networking layer, which never issues a public or cross-application-reachable route to a database service, regardless of what an application's `deployment.yaml` requests. |
| Exceptions | None. |

#### BR-016 — The AI Agent and MCP Are Never the Security Boundary

| Field | Detail |
|---|---|
| Statement | The AI Coding Agent (Claude Code) and the Company Deployment MCP are never treated as a security boundary. Every action they request that affects deployment, resources, or data must be independently re-authorized and policy-checked by the Platform API before execution — even when the agent or MCP has already performed its own client-side validation. |
| Rationale | Directly required by the project's architectural principle: "never trust the AI agent as a security boundary." An AI agent can be misled by prompt injection in project content, compromised, or simply produce an incorrect request; client-side checks improve UX but must never be the actual enforcement point. |
| Applies To | MCP Server (MOD-16); Platform API (MOD-17); AI Coding Agent |
| Enforcement Point | Platform API authorization layer, applied to every request regardless of origin (MCP Server, Administration Portal, or direct API caller). |
| Exceptions | None. |

#### BR-017 — Only High-Level Business Capabilities Are Exposed to the AI Agent

| Field | Detail |
|---|---|
| Statement | The MCP Server must only expose high-level, business-capability tools (e.g., `validate_application`, `deploy_application`, `get_application_status`). Raw infrastructure operations — `kubectl`-equivalent commands, Docker daemon control, arbitrary Kubernetes/container-platform resource creation, host filesystem access, or arbitrary container execution — must never be reachable through the MCP interface, directly or indirectly. |
| Rationale | Directly required by the project's architecture; this is the mechanism by which infrastructure detail stays abstracted from the AI agent and by which a compromised or manipulated agent session is structurally prevented from taking infrastructure-level action, independent of BR-016's authorization check. |
| Applies To | MCP Server (MOD-16); FR Module Y (MCP Integration) |
| Enforcement Point | MCP Server tool registry design — the tool surface itself is the control; no low-level tool is ever registered, so there is nothing for authorization to need to block at that layer. |
| Exceptions | None. |

### 2.6 Resource & Quota Rules

#### BR-018 — Resource Requests Must Match a Published Tier

| Field | Detail |
|---|---|
| Statement | Every service within an application must declare a resource tier (e.g., small / medium / large) from the platform's published catalog. A resource request outside the published tiers is rejected at validation. |
| Rationale | Keeps capacity planning, cost, and platform behavior predictable; prevents arbitrary or unbounded resource requests originating from either an employee or an AI agent. |
| Applies To | Resource Manager (MOD-07); Validation Engine (MOD-04) |
| Enforcement Point | Validation Engine, at `validate_application`, cross-checked against the Resource Manager's current tier catalog. |
| Exceptions | None at v1 — custom/negotiated resource tiers are a candidate future-phase capability, not available at MVP. |

#### BR-019 — Departments and Applications Are Subject to Enforced Quotas

| Field | Detail |
|---|---|
| Statement | Each Department and each individual application is subject to a resource quota — maximum concurrent instances, aggregate CPU/memory, and maximum number of registered applications — set and adjustable only by a Platform or IT Administrator. |
| Rationale | Bounds the "blast radius" of self-service resource consumption and protects shared platform capacity from being exhausted by any single team, intentionally or accidentally. |
| Applies To | Resource Manager (MOD-07); Platform Administrator; IT Administrator |
| Enforcement Point | Resource Manager, evaluated before scale-out and before accepting a new application registration. |
| Exceptions | Platform Administrator may grant a temporary quota increase; the grant is itself time-boxed and audited. Exact default quota values are **TBD** (see Section 3). |

#### BR-020 — Quota Breach Blocks Growth, Not Running Traffic

| Field | Detail |
|---|---|
| Statement | An application that would exceed its allocated quota is prevented from further scale-out (denied additional instances); it is never forcibly terminated or had already-running instances removed as the enforcement mechanism. |
| Rationale | Fails safe: protects shared capacity from further consumption without itself causing an unplanned outage of traffic the application is already serving. |
| Applies To | Resource Manager (MOD-07); Notification (MOD-15) |
| Enforcement Point | Resource Manager, at scale-out decision time (the scaling controller declines to add instances beyond quota). |
| Exceptions | None. |

### 2.7 Scale-to-Zero Rules

#### BR-021 — Only Stateless Services May Scale to Zero

| Field | Detail |
|---|---|
| Statement | Only stateless web, API, and worker services may be configured for scale-to-zero (`scaling.min = 0` in `deployment.yaml`). The Validation Engine determines eligibility from the declared service type. |
| Rationale | Scale-to-zero is only safe and meaningful for workloads that hold no required in-memory state — traffic can be routed to a freshly started cold instance with no loss of correctness. |
| Applies To | Validation Engine (MOD-04); Deployment Controller (MOD-06); FR Module L (Scale-to-Zero) |
| Enforcement Point | Validation Engine, at `validate_application` and `deploy_application`. |
| Exceptions | None. |

#### BR-022 — Databases and Persistent Services Never Scale to Zero With Their Application

| Field | Detail |
|---|---|
| Statement | An application's database and other persistent/stateful services must never scale to zero alongside its stateless workload. Persistent services remain continuously available regardless of the stateless workload's current instance count, including while it is at zero. |
| Rationale | Direct project requirement — "keep databases and persistent services separate from stateless application workloads." A database that scaled to zero would break any always-on consumer (scheduled jobs, direct DB tooling, monitoring) and risk data-layer instability. |
| Applies To | Database Manager (MOD-09); Resource Manager (MOD-07) |
| Enforcement Point | Deployment Controller, which never applies the scale-to-zero lifecycle policy to a workload classified as a database or persistent service, regardless of any `deployment.yaml` request to the contrary. |
| Exceptions | None. |

#### BR-023 — Static Frontends Are Not Necessarily Subject to Scale-to-Zero Container Lifecycle

| Field | Detail |
|---|---|
| Statement | A purely static frontend application is not necessarily subject to scale-to-zero container lifecycle rules; it may instead be served through a persistently-available static hosting mechanism rather than a scalable container runtime. |
| Rationale | Direct project requirement. Static assets have no meaningful "cold start" and gain no operational or cost benefit from being modeled as a scale-to-zero container workload. |
| Applies To | Deployment Controller (MOD-06); Resource Manager (MOD-07) |
| Enforcement Point | Deployment Controller, based on the declared service runtime type (`static` vs. containerized) in `deployment.yaml`. |
| Exceptions | None. |

#### BR-024 — Idle-to-Zero Timeout Is Configurable Within IT-Defined Bounds

| Field | Detail |
|---|---|
| Statement | The idle timeout after which a scale-to-zero-eligible service scales down to zero is configurable by the Application Owner, but only within minimum/maximum bounds defined by IT Administrator policy; a request outside those bounds is rejected at validation, and the platform default applies when unspecified. |
| Rationale | Balances an application's actual usage pattern (cost/resource efficiency from aggressive scale-down) against acceptable cold-start latency (see NFR-004, NFR-008), without letting either extreme be set unilaterally by a single Application Owner. |
| Applies To | Application Owner; Resource Manager (MOD-07); Validation Engine (MOD-04) |
| Enforcement Point | Validation Engine, at `validate_application`. |
| Exceptions | None — exact bound values are proposed in `03_Non_Functional_Requirements.md` (NFR-008) and subject to confirmation. |

### 2.8 Secret & Credential Rules

#### BR-025 — Production Credentials Are Never Stored in Source Code

| Field | Detail |
|---|---|
| Statement | Production credentials, API keys, tokens, and connection strings must never be committed to application source code or written directly into `deployment.yaml`. They must be provisioned exclusively through the Secret Manager and injected into the application at runtime. |
| Rationale | Direct project requirement, preventing secret leakage through source control history, code review exposure, or the AI agent's own working context/output. |
| Applies To | Employee / AI Coding Agent; Secret Manager (MOD-08); Validation Engine (MOD-04) |
| Enforcement Point | Validation Engine performs static secret-pattern scanning at `validate_application`; the Secret Manager is the only sanctioned runtime secret-delivery path, enforced by the absence of any alternative secret-injection mechanism in the platform. |
| Exceptions | None. |

#### BR-026 — Secrets Are Scoped to a Single Application and Environment

| Field | Detail |
|---|---|
| Statement | A secret provisioned for one application, or for one environment of an application, is never visible to or injectable into another application, or into a different environment of the same application (e.g., a development secret is never usable in production, and vice versa). |
| Rationale | Prevents lateral secret exposure — without this, a lower-trust environment (dev) compromise could reach production credentials, and one application's compromise could reach a sibling application's secrets. |
| Applies To | Secret Manager (MOD-08) |
| Enforcement Point | Secret Manager access-control layer, keyed strictly by (application, environment). |
| Exceptions | None. |

#### BR-027 — Secret Rotation Must Not Require a Code Change

| Field | Detail |
|---|---|
| Statement | Secret rotation and revocation must be achievable through the Secret Manager without requiring an application source-code change or rebuild — a runtime reload/restart is sufficient. |
| Rationale | Rotation that requires a code change or rebuild is operationally expensive enough that, in practice, it will not happen on schedule; decoupling rotation from application code is what makes routine rotation actually feasible. |
| Applies To | Secret Manager (MOD-08); Security Administrator |
| Enforcement Point | Secret Manager rotation workflow (runtime secret reload, not a redeploy pipeline). |
| Exceptions | None. Exact rotation cadence is **TBD** (see Section 3; proposed interim default in `03_Non_Functional_Requirements.md`, NFR-024). |

### 2.9 Data & Database Rules

#### BR-028 — Each Application's Database Is Exclusively Owned by That Application

| Field | Detail |
|---|---|
| Statement | Each application's database instance or schema is provisioned for, and owned exclusively by, that application. No application may be provisioned direct access to another application's database. |
| Rationale | Reinforces BR-013 specifically for the data layer — data isolation must be a hard architectural guarantee, not merely a naming or documentation convention. |
| Applies To | Database Manager (MOD-09) |
| Enforcement Point | Database Manager provisioning and credential-issuance logic, which never issues a second application's database credential to a requesting application. |
| Exceptions | None. |

#### BR-029 — Database Infrastructure Is Never Directly Managed by the Owner or the AI Agent

| Field | Detail |
|---|---|
| Statement | Database provisioning, credential issuance, backup scheduling, and the database's exclusion from the stateless-workload scaling lifecycle (BR-022) are managed exclusively by the Database Manager module. Neither the Application Owner nor the AI Coding Agent may directly configure or access the underlying database infrastructure. |
| Rationale | Keeps infrastructure detail abstracted from employees and the AI agent, consistent with the platform's core architectural principle, and keeps a single accountable module responsible for data-layer correctness and security. |
| Applies To | Database Manager (MOD-09); Employee / AI Coding Agent |
| Enforcement Point | MCP tool surface (no raw database-administration tool is ever exposed, per BR-017) and the Platform API authorization layer. |
| Exceptions | None. |

#### BR-030 — Database Deletion Requires a Distinct, Explicit Confirmation

| Field | Detail |
|---|---|
| Statement | Deletion of an application's database requires a distinct, explicit confirmation step, separate from and in addition to general application deletion or decommissioning. `delete_application` must never implicitly cascade to database deletion. |
| Rationale | Database deletion is irreversible and high-impact; a routine application cleanup action (including one initiated autonomously by an AI agent following a plausible-sounding instruction) must not be able to destroy data as a side effect. |
| Applies To | Database Manager (MOD-09); Application Owner; Platform Administrator |
| Enforcement Point | Platform API — `delete_application` does not cascade to database deletion without a separate, explicitly confirmed request naming the database resource. |
| Exceptions | None. |

### 2.10 Audit & Compliance Rules

#### BR-031 — Every Deployment-Affecting Action Is Attributable and Audited

| Field | Detail |
|---|---|
| Statement | Every deployment-affecting action — create, validate, deploy, rollback, restart, delete, approval decisions, and any policy override — must be attributable to a specific identity (a human user, or an AI agent acting explicitly on behalf of an identified human) and recorded in the audit log. |
| Rationale | Direct project requirement, and the foundation for incident investigation, security review, and Management/Auditor oversight — an action with no attributable identity cannot be meaningfully audited. |
| Applies To | Audit (MOD-14); all actors |
| Enforcement Point | Platform API request-handling layer; the audit event is emitted synchronously as part of authorizing the action, not as a best-effort, asynchronous afterthought. |
| Exceptions | None. |

#### BR-032 — Audit Records Are Immutable

| Field | Detail |
|---|---|
| Statement | Audit records are immutable once written. No role — including Platform Administrator — may edit or delete an audit entry through normal platform operation. Export or redaction for a legal hold is a distinct, separately authorized and separately audited procedure, not a standard operation. |
| Rationale | An editable audit trail cannot be trusted for incident investigation, security review, or compliance purposes; immutability is what makes the audit log evidentiary rather than merely informational. |
| Applies To | Audit (MOD-14); Security Administrator; Management / Auditor |
| Enforcement Point | Audit module storage layer (append-only / tamper-evident design); no update or delete API is exposed to any standard role. |
| Exceptions | A legal-hold export/redaction procedure, itself logged and requiring dual authorization. The exact procedure is **TBD**, pending Legal/Compliance input (see Section 3). |

#### BR-033 — Audit Data Is Never Silently Purged

| Field | Detail |
|---|---|
| Statement | The platform must apply a documented retention policy to audit data and must never silently purge audit records without that policy being explicit and visible to the Security Administrator and Management/Auditor roles. |
| Rationale | Avoids the platform assuming a compliance-sensitive retention decision on its own; retention must be an explicit, visible policy, not an implementation detail of a storage system reaching capacity. |
| Applies To | Audit (MOD-14); Security Administrator; Management / Auditor |
| Enforcement Point | Audit module retention/lifecycle policy configuration, reviewed and confirmed before enabled in production. |
| Exceptions | None. Exact retention duration is **TBD** (see Section 3; proposed interim default in `03_Non_Functional_Requirements.md`, NFR-037). |

### 2.11 Naming & Domain Rules

#### BR-034 — Application Names Must Be Unique and Conform to Platform Conventions

| Field | Detail |
|---|---|
| Statement | Application names must be unique within the platform's registration namespace and must conform to platform naming conventions (character set, length, reserved-word restrictions), to prevent domain/URL collisions. |
| Rationale | The application name is used to derive the internal URL/domain (per the `deployment.yaml` example, `app.name: overtime`); a naming collision would cause routing ambiguity or, worse, accidental traffic exposure to the wrong application. |
| Applies To | Application Registry (MOD-02); Domain Manager (MOD-10) |
| Enforcement Point | Application Registry, at `create_application` / registration time. |
| Exceptions | None. Whether uniqueness is enforced platform-wide or only within a Department namespace is **TBD** (see Section 3). |

#### BR-035 — External-Facing Visibility Requires Elevated Approval

| Field | Detail |
|---|---|
| Statement | An application's domain visibility (internal vs. external/public) is a policy-governed setting. Requesting external-facing visibility requires elevated approval from the Security Administrator (and/or Platform Administrator), in addition to — not instead of — the standard production approval required by BR-007. |
| Rationale | Exposing an internal application to the public internet materially changes its threat model (unauthenticated internet traffic, public attack surface) and must never be a routine, unreviewed self-service choice. |
| Applies To | Domain Manager (MOD-10); Security Administrator; Deployment Manager (MOD-03) |
| Enforcement Point | Platform API authorization layer, evaluated specifically whenever `domain.visibility = external` is requested. |
| Exceptions | None. |

---

## 3. TBD Items Requiring a Decision

The following rules state a firm invariant but leave a specific parameter or governance detail open, because it depends on a decision this document cannot responsibly make on its own. Each will be tracked as an open item in `17_Decision_Log.md`.

| BR | Open Question | Input Needed From |
|---|---|---|
| BR-007 | Exact designation of who is the authorized production approver per application/department (Application Owner? Platform Administrator? Security Administrator? A combination?) | Platform Administrator + Security Administrator, aligned with company change-management policy |
| BR-012 | Whether staging is a mandatory promotion gate, and what automated/manual checks it requires | Platform Administrator + IT Administrator |
| BR-019 | Exact default resource quota values per department/tier | Platform Administrator, informed by capacity planning |
| BR-027 | Exact secret rotation cadence | Security Administrator, aligned with company security policy |
| BR-032 | Exact legal-hold export/redaction procedure | Legal / Compliance |
| BR-033 | Exact audit log retention duration | Legal / Compliance |
| BR-034 | Whether application-name uniqueness is platform-wide or department-scoped | Platform Administrator |

---

*End of document. See `03_Non_Functional_Requirements.md` for the measurable quality targets these rules are enforced against, and `11_Security_Requirements.md` for the detailed security control design that implements the Security & Isolation, Secret & Credential, and Data & Database rules in this document.*
