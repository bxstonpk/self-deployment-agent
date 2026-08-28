# 02 — Functional Requirements

**Project:** Company AI Application Deployment Platform
**Document:** 02_Functional_Requirements.md
**Status:** Draft baseline for review
**Last Updated:** 2026-08-28

## 1. Purpose

This document defines the complete set of functional requirements (FR) for the Company AI Application Deployment Platform — the internal, self-service pipeline through which an Employee, assisted by Claude Code (the AI coding agent), the Company Deployment Skill, and the Company Deployment MCP server, deploys applications through the Company Platform API, Deployment Engine, and Container Platform, without ever touching raw infrastructure directly.

It translates the business vision and problem statement in **01_BRD.md** into discrete, testable, actor-driven functional behaviors that the platform MUST, SHOULD, COULD, or explicitly WON'T deliver. Each requirement is independently identifiable, traceable, and testable, and is intended to be consumed by architecture, engineering, QA, and security teams as the functional baseline for design and acceptance testing.

This document does **not** define non-functional requirements (performance, availability, capacity — see the Non-Functional Requirements doc), detailed MCP tool I/O schemas (see **07_MCP_Requirements.md**), Company Deployment Skill prompt/behavior design (see **08_Company_Deployment_Skill.md**), or open business decisions pending sign-off (see **17_Decision_Log.md**). Where a concrete numeric value (quota, retention period, SSO vendor, etc.) would normally be required, this document uses a placeholder tier reference or flags the value **TBD — see Decision Log**, rather than inventing one.

## 2. How to Read This Document

Requirements are grouped into 28 functional modules (A through AB), ordered roughly by the lifecycle of an application on the platform: identity and organizational setup first, then application registration and configuration, then the deployment pipeline itself, then runtime concerns (scaling, data, secrets, networking, health), then operational concerns (observability, versioning, rollback, audit), then integration (MCP, AI agent), and finally platform administration and reporting.

Each functional requirement (FR) is presented as a level-4 heading (`#### FR-xxx — Name`) followed by a fixed set of sub-bullets, applied uniformly across all requirements in this document:

- **Description** — what the platform must do, in business terms.
- **Actor(s)** — one or more of the 7 defined actors who initiate or participate in the requirement.
- **Trigger** — the event that starts the behavior.
- **Preconditions** — state that must hold before the behavior can execute.
- **Main Flow** — the numbered happy-path steps.
- **Alternative Flow** — valid variations on the happy path.
- **Exception Flow** — error/failure handling.
- **Business Rules** — constraints, policies, and invariants that govern the requirement.
- **Input** — data/artifacts consumed.
- **Output** — data/artifacts/state produced.
- **Acceptance Criteria** — condition(s) under which the requirement is considered satisfied, written to be verifiable by QA.
- **Priority** — MoSCoW: MUST, SHOULD, COULD, or WON'T (this release).

### Actors referenced throughout this document

| Short name | Full actor |
|---|---|
| Employee | Employee / Application Developer |
| Agent | AI Coding Agent / Claude Code |
| IT Admin | IT Administrator |
| Platform Admin | Platform Administrator |
| App Owner | Application Owner |
| Security Admin | Security Administrator |
| Auditor | Management / Auditor |

### Application Lifecycle states (referenced throughout, defined fully in Module K)

`Draft → Validated → Build → Deploying → Running → Suspended → Failed → Rolled Back → Archived → Deleted`

### Deployment Lifecycle steps (referenced throughout, defined fully in Module J)

`Request → Authentication → Authorization → Validation → Security Check → Build → Image Scan → Registry → Deployment → Health Check → Traffic Activation → Monitoring → Completed` (plus Failure/Rollback branches at any gated step)

## 3. Numbering Scheme

Requirements are numbered **FR-001 through FR-129**, sequentially and continuously across all 28 modules in the order listed in the Table of Contents below — numbering does **not** restart per module. This numbering is stable for the life of the baseline; superseded or removed requirements are marked **Deprecated** in place rather than renumbered, so that cross-document references (test cases, traceability matrices, design docs) never break.

## 4. Related Documents

| Document | Relationship |
|---|---|
| **01_BRD.md** | Upstream business requirements, problem statement, vision, and scope this document elaborates into functional detail. |
| **07_MCP_Requirements.md** | Owns full technical detail (tool schemas, request/response contracts, error codes) for every MCP tool referenced at the business level in Module Y. |
| **08_Company_Deployment_Skill.md** | Owns the detailed behavior, prompts, and guardrails of the Company Deployment Skill referenced at the business level in Module Z. |
| **17_Decision_Log.md** | Tracks open business decisions flagged **TBD** in this document (vendor choices, exact quotas, exact retention periods). |
| Non-Functional / Resource Requirements doc | Owns concrete resource-tier definitions (CPU/memory/storage per tier) referenced by Modules G and M. |

## Table of Contents

| # | Module | FR Range |
|---|---|---|
| A | Authentication | FR-001–FR-004 |
| B | User Management | FR-005–FR-007 |
| C | Organization / Department | FR-008–FR-010 |
| D | Application Registration | FR-011–FR-014 |
| E | Application Ownership | FR-015–FR-018 |
| F | Stack Management | FR-019–FR-022 |
| G | Deployment Configuration | FR-023–FR-028 |
| H | Deployment Validation | FR-029–FR-034 |
| I | Build Management | FR-035–FR-038 |
| J | Deployment Management | FR-039–FR-044 |
| K | Application Lifecycle | FR-045–FR-050 |
| L | Scale-to-Zero | FR-051–FR-056 |
| M | Resource Management | FR-057–FR-060 |
| N | Database Management | FR-061–FR-065 |
| O | Secret Management | FR-066–FR-071 |
| P | Domain Management | FR-072–FR-075 |
| Q | Networking | FR-076–FR-081 |
| R | Health Check | FR-082–FR-085 |
| S | Logging | FR-086–FR-089 |
| T | Monitoring | FR-090–FR-093 |
| U | Version Management | FR-094–FR-097 |
| V | Rollback | FR-098–FR-102 |
| W | Audit Log | FR-103–FR-106 |
| X | Notification | FR-107–FR-109 |
| Y | MCP Integration | FR-110–FR-115 |
| Z | Claude Code / AI Agent Integration | FR-116–FR-121 |
| AA | Administration | FR-122–FR-126 |
| AB | Reporting | FR-127–FR-129 |

---

## Module A — Authentication

#### FR-001 — Employee Platform Authentication

- **Description:** The platform must authenticate every Employee before granting access to any self-service deployment capability, whether via the web console, CLI, or through Claude Code / the Company Deployment MCP acting on the employee's behalf.
- **Actor(s):** Employee, Platform Administrator
- **Trigger:** Employee (or Claude Code on the employee's behalf) attempts to access a platform capability.
- **Preconditions:** Employee has a valid, active Company identity account.
- **Main Flow:**
  1. Employee initiates access to the platform (console, CLI, or MCP-mediated request).
  2. Platform redirects to / validates against the Company Identity Provider (IdP — vendor **TBD**, see Decision Log).
  3. IdP confirms identity and returns an authenticated session assertion.
  4. Platform API issues a platform session/token bound to the employee's identity and roles.
  5. Employee (or the agent acting for them) proceeds with an authenticated session.
- **Alternative Flow:** Employee already holds a valid unexpired session token; steps 2–3 are skipped and the existing token is validated directly.
- **Exception Flow:** IdP authentication fails or account is inactive/locked → access denied, no session issued, attempt recorded per Module W (Audit Log).
- **Business Rules:** No platform capability is reachable without a successfully authenticated identity; the AI agent never authenticates as itself in place of the employee — every agent-initiated action carries the employee's authenticated identity (see FR-117).
- **Input:** Employee credentials / IdP session assertion.
- **Output:** Authenticated platform session/token scoped to the employee's identity and roles.
- **Acceptance Criteria:** An unauthenticated request to any platform capability is rejected (401/equivalent); a successfully authenticated employee receives a session usable for subsequent authorized calls.
- **Priority:** MUST

#### FR-002 — Single Sign-On (SSO) Integration

- **Description:** The platform must integrate with the Company's single sign-on identity provider so employees use one corporate identity across the platform, Claude Code, and other internal systems, rather than a platform-specific credential.
- **Actor(s):** Employee, IT Administrator
- **Trigger:** Employee accesses the platform for the first time or after session expiry.
- **Preconditions:** Company IdP/SSO is provisioned and reachable; IT Administrator has federated the platform as a relying application.
- **Main Flow:**
  1. Employee is redirected to the corporate SSO login.
  2. Employee authenticates with corporate credentials (and MFA, if enforced by the IdP).
  3. SSO issues an identity token/assertion to the platform.
  4. Platform maps the SSO identity to a platform user record (creating one on first login per FR-005).
- **Alternative Flow:** Employee already has an active corporate SSO session (single sign-on across other apps) → platform accepts the existing session without re-prompting for credentials.
- **Exception Flow:** SSO assertion invalid, expired, or from an untrusted issuer → login rejected; IT Administrator alerted if failures spike (possible misconfiguration or attack).
- **Business Rules:** The specific IdP/SSO vendor and protocol (e.g., SAML vs. OIDC) is **TBD — see Decision Log**; this requirement is vendor-agnostic. MFA enforcement policy is inherited from the corporate IdP, not re-implemented by the platform.
- **Input:** Corporate SSO credentials / assertion.
- **Output:** Federated platform identity session.
- **Acceptance Criteria:** Employees can log in using only their corporate SSO identity; no platform-only password exists for standard employee accounts.
- **Priority:** MUST

#### FR-003 — Session and Token Lifecycle Management

- **Description:** The platform must issue, expire, refresh, and revoke authentication sessions/tokens for employees and administrators to bound the window of access from any single authentication event.
- **Actor(s):** Employee, Platform Administrator, IT Administrator
- **Trigger:** Session creation (login), token expiry, explicit logout, or administrative revocation.
- **Preconditions:** An authenticated session exists or is being created.
- **Main Flow:**
  1. Platform issues a session/token with a defined expiry per the platform's session policy.
  2. Employee's client presents the token on each subsequent request.
  3. On expiry, the client is prompted to re-authenticate or silently refreshes via a refresh token (if enabled).
  4. Employee explicitly logs out, or a Platform Administrator revokes the session.
- **Alternative Flow:** Refresh token flow renews an active session without full re-authentication, subject to a maximum session lifetime.
- **Exception Flow:** Expired, malformed, or revoked token presented → request rejected (401/equivalent); repeated invalid attempts trigger throttling/lockout per security policy.
- **Business Rules:** Session and refresh-token lifetimes follow the platform security baseline (exact durations **TBD — see Decision Log**); a revoked session must be rejected on the very next request, not merely at next refresh.
- **Input:** Session/refresh token.
- **Output:** Valid/invalid session determination; renewed token where applicable.
- **Acceptance Criteria:** A revoked or expired token is rejected on the next call; an administrator-initiated revocation takes effect without requiring the token to expire naturally.
- **Priority:** MUST

#### FR-004 — Service-to-Service Authentication (MCP / API / Engine)

- **Description:** The platform must authenticate machine-to-machine calls between the Company Deployment MCP server, the Company Platform API, the Deployment Engine, and the Container Platform using service identities distinct from — and never a substitute for — the originating employee's identity.
- **Actor(s):** Platform Administrator, Security Administrator, AI Coding Agent / Claude Code (as an indirect caller through MCP)
- **Trigger:** Any internal service-to-service call in the deployment pipeline (e.g., MCP server calling the Platform API on behalf of a request).
- **Preconditions:** Each internal service has been issued its own service credential/identity by Platform/Security Administration.
- **Main Flow:**
  1. Calling service (e.g., MCP server) authenticates to the receiving service (e.g., Platform API) using its service credential.
  2. Calling service forwards the originating employee's identity/claims alongside the service credential (never in place of it).
  3. Receiving service validates both the service credential and the forwarded employee identity before processing.
- **Alternative Flow:** None — service-to-service calls are always authenticated; there is no unauthenticated internal path.
- **Exception Flow:** Invalid/expired service credential → call rejected and logged as a potential platform integrity issue, alerted to Security Administrator.
- **Business Rules:** A valid service credential alone never grants authorization to act — the receiving service must still authorize against the forwarded employee identity and role (see Module Y/Z); service credentials are rotated per the platform's credential rotation policy (see Module O).
- **Input:** Service credential; forwarded employee identity/claims.
- **Output:** Authenticated inter-service call context.
- **Acceptance Criteria:** A service call without a valid service credential is rejected; a service call with a valid service credential but no forwarded employee identity is rejected for any employee-scoped operation.
- **Priority:** MUST

---

## Module B — User Management

#### FR-005 — User Account Provisioning

- **Description:** The platform must create a platform user record for each employee, either automatically on first successful SSO login (just-in-time provisioning) or via explicit administrative creation.
- **Actor(s):** Employee, IT Administrator, Platform Administrator
- **Trigger:** Employee's first successful SSO login, or an administrator manually provisions an account ahead of first login.
- **Preconditions:** Employee has a valid corporate identity (Module A).
- **Main Flow:**
  1. Employee authenticates via SSO for the first time.
  2. Platform detects no existing user record for that identity.
  3. Platform creates a new user record with a default minimal role (e.g., Employee/Application Developer) and no department association.
  4. IT/Platform Administrator subsequently assigns department and any elevated roles.
- **Alternative Flow:** Administrator pre-provisions the account (e.g., bulk import ahead of onboarding); first SSO login then binds to the existing record instead of creating a new one.
- **Exception Flow:** Identity cannot be mapped to a known employee record (e.g., contractor without HR record) → provisioning held pending administrative review.
- **Business Rules:** Every platform action must be traceable to exactly one provisioned user record; duplicate accounts for the same corporate identity are not permitted.
- **Input:** SSO identity assertion, or administrator-submitted user data.
- **Output:** Platform user record.
- **Acceptance Criteria:** A first-time SSO login for a valid corporate identity results in exactly one new user record with a default role and no privileged access.
- **Priority:** MUST

#### FR-006 — Role Assignment (RBAC)

- **Description:** The platform must support assigning one or more roles (mapped to the 7 defined actor types) to a user, governing which platform capabilities that user — and any agent acting on their behalf — can invoke.
- **Actor(s):** Platform Administrator, IT Administrator, Security Administrator
- **Trigger:** New user provisioned, role change request, or periodic access review.
- **Preconditions:** Requesting administrator holds a role authorized to grant roles.
- **Main Flow:**
  1. Administrator selects a target user.
  2. Administrator assigns/removes one or more roles from the supported role set.
  3. Platform persists the role change and invalidates any cached authorization decisions for that user.
  4. Change is recorded in the audit log (Module W).
- **Alternative Flow:** Role assigned automatically based on department/group mapping from the IdP (if configured), rather than manually per user.
- **Exception Flow:** Administrator attempts to grant a role beyond their own delegated authority → request rejected.
- **Business Rules:** A user may hold multiple roles concurrently (e.g., Application Developer and Application Owner); role changes take effect on the next authorization check, not retroactively; no self-elevation — an administrator cannot grant themselves a higher-privilege role without a second approver (control detail owned by Security/Non-Functional docs).
- **Input:** Target user, role(s) to assign/revoke.
- **Output:** Updated user role set.
- **Acceptance Criteria:** After a role change, the user's next request is authorized (or denied) according to the new role set; the change appears in the audit log.
- **Priority:** MUST

#### FR-007 — User Deactivation / Offboarding

- **Description:** The platform must be able to deactivate a user's access immediately upon employee offboarding or role removal, without requiring deletion of their historical audit trail or authored applications.
- **Actor(s):** IT Administrator, Platform Administrator
- **Trigger:** HR/IT offboarding event, or administrative deactivation request.
- **Preconditions:** Target user record exists.
- **Main Flow:**
  1. Administrator (or automated IdP deprovisioning feed) marks the user inactive.
  2. Platform immediately revokes all active sessions/tokens for that user (per FR-003).
  3. Platform flags any applications solely owned by the deactivated user for ownership review (Module E).
- **Alternative Flow:** Deactivation is scheduled for a future date (e.g., last day of employment) rather than immediate.
- **Exception Flow:** Deactivated user is the sole owner of production applications → deactivation proceeds but an ownership-transfer task is raised to the user's manager/Platform Administrator.
- **Business Rules:** Deactivation revokes access but preserves the user's historical audit records (Module W) and authorship attribution; a deactivated user's applications continue running until ownership is reassigned or the app is otherwise retired.
- **Input:** Target user, deactivation directive.
- **Output:** Deactivated user record; revoked sessions; ownership-review flag where applicable.
- **Acceptance Criteria:** A deactivated user's next request is rejected regardless of any previously valid token; applications they owned remain running pending ownership transfer.
- **Priority:** MUST

---

## Module C — Organization / Department

#### FR-008 — Department / Team Registration

- **Description:** The platform must allow administrators to register the departments/teams (e.g., HR, Finance, Engineering) that own applications and employees, establishing the organizational structure used for ownership, quotas, and reporting.
- **Actor(s):** Platform Administrator, IT Administrator
- **Trigger:** New department needs representation on the platform.
- **Preconditions:** Requesting administrator holds department-management privileges.
- **Main Flow:**
  1. Administrator submits a new department record (name, identifier).
  2. Platform validates the department identifier is unique.
  3. Platform creates the department record, available for user and application association.
- **Alternative Flow:** Departments are synchronized automatically from an authoritative HR/organizational system rather than entered manually (integration detail **TBD**).
- **Exception Flow:** Duplicate department identifier submitted → rejected with a conflict error.
- **Business Rules:** Every application must belong to exactly one department at any given time (Module D/E); department identifiers are unique platform-wide.
- **Input:** Department name/identifier.
- **Output:** Department record.
- **Acceptance Criteria:** A department, once registered, is selectable when registering applications and assigning users.
- **Priority:** MUST

#### FR-009 — Department–User Association

- **Description:** The platform must associate each user with a home department, used to attribute applications, quotas, and reporting to the correct organizational unit.
- **Actor(s):** Platform Administrator, IT Administrator, Employee
- **Trigger:** User provisioning, department transfer, or organizational restructuring.
- **Preconditions:** Target department exists (FR-008).
- **Main Flow:**
  1. Administrator (or automated IdP/HR sync) assigns a department to the user record.
  2. Platform updates the user's department association.
  3. Reporting and quota calculations (Modules M, AB) reflect the new association going forward.
- **Alternative Flow:** User moves between departments; historical applications/audit records retain their original department attribution rather than being retroactively reassigned.
- **Exception Flow:** Department does not exist or is inactive → assignment rejected.
- **Business Rules:** A user has exactly one home department at a time; department changes are effective from the change date forward only.
- **Input:** User, target department.
- **Output:** Updated user-department association.
- **Acceptance Criteria:** Applications newly registered by a user are attributed to that user's current department.
- **Priority:** SHOULD

#### FR-010 — Department Policy and Quota Assignment

- **Description:** The platform must allow administrators to assign resource and policy tiers to a department, which flow down as defaults/ceilings for that department's applications.
- **Actor(s):** Platform Administrator, IT Administrator
- **Trigger:** Department onboarding, or periodic policy/quota review.
- **Preconditions:** Department exists (FR-008); resource tiers are defined (Module M).
- **Main Flow:**
  1. Administrator selects a department and a policy/quota tier (per resource tiers defined in the Non-Functional/Resource Requirements doc).
  2. Platform stores the department's assigned tier and effective policy set.
  3. Subsequent application registrations/deployments in that department are evaluated against the assigned tier (Module H/M).
- **Alternative Flow:** Department-level overrides are layered on top of a global default tier for exceptional cases, subject to Platform Administrator approval.
- **Exception Flow:** Requested tier/quota exceeds platform-wide ceilings → rejected.
- **Business Rules:** Exact quota numbers are **TBD — see Decision Log**; this requirement defines the mechanism, not the values. Department quotas are enforced independently of, and in addition to, per-application quotas (Module M).
- **Input:** Department, target policy/quota tier.
- **Output:** Department policy/quota assignment.
- **Acceptance Criteria:** An application registration/deployment that would exceed its department's assigned quota is blocked with a clear quota-exceeded error.
- **Priority:** SHOULD

---

## Module D — Application Registration

#### FR-011 — Register New Application

- **Description:** The platform must allow an Employee (typically working through Claude Code and the Company Deployment Skill) to register a new application, creating the canonical application record that all subsequent configuration, builds, and deployments attach to.
- **Actor(s):** Employee, AI Coding Agent / Claude Code
- **Trigger:** Employee (via Claude Code) initiates registration of a new application, or calls the MCP `create_application` capability (Module Y).
- **Preconditions:** Employee is authenticated (Module A) and belongs to a department (Module C).
- **Main Flow:**
  1. Employee/Agent submits application registration data (name, owning department, initial `deployment.yaml` draft).
  2. Platform validates the name is unique and well-formed (FR-012).
  3. Platform creates the application record in **Draft** state (Module K).
  4. Platform assigns the requesting employee as the initial Application Owner (Module E), pending confirmation.
- **Alternative Flow:** Application is registered with only a name and owner first; full `deployment.yaml` is authored in a subsequent step (Module G).
- **Exception Flow:** Name collision, missing department, or malformed submission → registration rejected with a specific error returned to the employee/agent.
- **Business Rules:** Every application belongs to exactly one department and has at least one designated owner at all times after registration completes; registration alone does not authorize any deployment — validation and approval gates still apply (Modules H, J).
- **Input:** Application name, department, initial configuration draft.
- **Output:** Application record in Draft state.
- **Acceptance Criteria:** A successful registration produces a queryable application record in Draft state with an assigned owner and department; a duplicate name is rejected.
- **Priority:** MUST

#### FR-012 — Application Naming and Uniqueness Validation

- **Description:** The platform must enforce a consistent naming convention for applications and guarantee name uniqueness platform-wide, since the application name drives derived artifacts such as domains (Module P) and container/image names.
- **Actor(s):** Employee, AI Coding Agent / Claude Code
- **Trigger:** Application registration (FR-011) or rename request.
- **Preconditions:** None beyond an authenticated requester.
- **Main Flow:**
  1. Requester submits a candidate application name.
  2. Platform checks the name against naming-convention rules (character set, length, reserved words).
  3. Platform checks the name against existing application names platform-wide.
  4. If both checks pass, the name is accepted and reserved for the application.
- **Alternative Flow:** None.
- **Exception Flow:** Name violates convention or collides with an existing application → rejected with the specific violated rule returned to the employee/agent for correction.
- **Business Rules:** Application names are unique platform-wide (not just per department), immutable after first successful deployment (renaming a live application is out of scope for this release), and restricted to characters valid in DNS labels since names feed domain generation (Module P).
- **Input:** Candidate application name.
- **Output:** Accepted/rejected determination.
- **Acceptance Criteria:** Two applications cannot exist with the same name; a name containing invalid characters is rejected before registration completes.
- **Priority:** MUST

#### FR-013 — Application Metadata Management

- **Description:** The platform must allow the Application Owner to view and edit descriptive application metadata (description, tags, department, contact info) independently of the deployment-affecting `deployment.yaml` contract.
- **Actor(s):** Application Owner, Employee
- **Trigger:** Owner initiates a metadata update.
- **Preconditions:** Application is registered (FR-011); requester is the Application Owner or a co-owner (Module E).
- **Main Flow:**
  1. Owner opens the application record.
  2. Owner edits metadata fields.
  3. Platform validates and persists the change.
  4. Change is recorded in the audit log (Module W).
- **Alternative Flow:** Department reassignment is requested as part of a metadata edit; this routes through department-transfer rules (FR-009) rather than a simple field edit.
- **Exception Flow:** Non-owner attempts an edit → rejected as unauthorized.
- **Business Rules:** Metadata edits never trigger a redeploy or lifecycle state change; only `deployment.yaml` changes do (Module G).
- **Input:** Metadata field changes.
- **Output:** Updated application metadata.
- **Acceptance Criteria:** A metadata-only edit does not change the application's lifecycle state or trigger a new deployment.
- **Priority:** SHOULD

#### FR-014 — Application De-registration Request

- **Description:** The platform must allow an Application Owner to request de-registration (permanent removal) of an application they own, subject to safeguards, feeding into the Application Lifecycle's Archive/Delete states (Module K).
- **Actor(s):** Application Owner, Platform Administrator
- **Trigger:** Owner submits a de-registration request.
- **Preconditions:** Requester is the Application Owner; application is not in an active Deploying state.
- **Main Flow:**
  1. Owner submits a de-registration request with a reason.
  2. Platform checks for blocking conditions (e.g., running production workload with active traffic, dependent applications).
  3. If clear, the application transitions toward Archived/Deleted per Module K.
  4. Underlying resources (database, secrets, domain) are deprovisioned per Modules N, O, P.
- **Alternative Flow:** Owner requests Archive only (retain data/config, stop serving traffic) rather than full Delete (Module K).
- **Exception Flow:** Application is a designated dependency of another running application, or is a production workload requiring additional approval → request is held pending resolution/approval.
- **Business Rules:** De-registration of a Running production application requires explicit approval (mirrors the production deploy approval gate, Module J); de-registration is irreversible once the Deleted state is reached (Module K).
- **Input:** De-registration request, reason.
- **Output:** Application transitioned to Archived or Deleted state; deprovisioning of dependent resources initiated.
- **Acceptance Criteria:** A de-registration request for a Running production application without approval is held, not executed; an approved request results in the application reaching Archived/Deleted and no longer serving traffic.
- **Priority:** MUST

---

## Module E — Application Ownership

#### FR-015 — Assign Application Owner

- **Description:** The platform must record exactly one accountable Application Owner per application at any given time, distinct from the (possibly many) employees who develop or deploy to it.
- **Actor(s):** Employee, Application Owner, Platform Administrator
- **Trigger:** Application registration (default: registering employee becomes owner) or explicit owner assignment.
- **Preconditions:** Target application exists; target owner is an active platform user.
- **Main Flow:**
  1. Requester designates an owner for the application (defaults to the registering employee at creation, FR-011).
  2. Platform validates the target user is active and, if required by policy, in the same department.
  3. Platform records the owner assignment.
- **Alternative Flow:** Platform Administrator force-assigns an owner administratively (e.g., during offboarding cleanup, FR-007).
- **Exception Flow:** Target user is inactive or the assignment would leave the application without any owner → rejected.
- **Business Rules:** An application always has exactly one primary Application Owner; ownership is a prerequisite for production deployment approval (Module J) and for destructive lifecycle operations (Module K).
- **Input:** Application, target owner.
- **Output:** Recorded application ownership.
- **Acceptance Criteria:** Every application in a non-Draft state has exactly one active Application Owner queryable at all times.
- **Priority:** MUST

#### FR-016 — Transfer Application Ownership

- **Description:** The platform must support transferring an application's ownership from one employee to another, e.g., on team changes or offboarding.
- **Actor(s):** Application Owner, Platform Administrator
- **Trigger:** Current owner (or an administrator, e.g., during offboarding) initiates an ownership transfer.
- **Preconditions:** Requester is the current owner or a Platform Administrator; target new owner is an active user.
- **Main Flow:**
  1. Current owner (or administrator) nominates a new owner.
  2. Platform notifies the nominated new owner (Module X).
  3. New owner accepts the transfer.
  4. Platform updates the ownership record; prior owner is retained in history for audit purposes.
- **Alternative Flow:** Administrator performs a forced transfer without new-owner acceptance during offboarding of an already-deactivated user (FR-007).
- **Exception Flow:** Nominated new owner does not accept within the policy window → transfer request expires and prior owner remains accountable.
- **Business Rules:** Ownership transfer does not change the application's lifecycle state, configuration, or running deployment; it is a pure accountability change.
- **Input:** Application, nominated new owner.
- **Output:** Updated ownership record; transfer event in audit log.
- **Acceptance Criteria:** After a completed transfer, the new owner — not the prior owner — is required for owner-gated actions (e.g., FR-014, production approvals).
- **Priority:** MUST

#### FR-017 — Co-Owner / Contributor Management

- **Description:** The platform must allow the Application Owner to grant co-owner or contributor access to other employees, so a team — not only a single individual — can develop and deploy an application, while accountability remains with the primary owner.
- **Actor(s):** Application Owner, Employee
- **Trigger:** Owner adds/removes a co-owner or contributor.
- **Preconditions:** Requester is the primary Application Owner.
- **Main Flow:**
  1. Owner selects an employee and a contributor-level role (co-owner or contributor) for the application.
  2. Platform records the association, scoped to that application only.
  3. The added employee gains access to the application's configuration and deployment actions consistent with their granted level.
- **Alternative Flow:** Owner removes a previously granted co-owner/contributor, immediately revoking their application-scoped access.
- **Exception Flow:** Owner attempts to grant access to an inactive/deactivated user → rejected.
- **Business Rules:** Co-owners may perform day-to-day configuration and deployment actions but only the primary owner (or Platform Administrator) can transfer ownership (FR-016) or de-register the application (FR-014); this access is scoped strictly to the single application, never platform-wide.
- **Input:** Application, target employee, access level.
- **Output:** Updated contributor list for the application.
- **Acceptance Criteria:** A contributor can perform actions within their granted level and is blocked from owner-only actions; removed contributors lose access immediately.
- **Priority:** SHOULD

#### FR-018 — Ownership Verification Gate

- **Description:** The platform must verify an active, valid Application Owner exists before permitting production deployment, ownership-sensitive configuration changes, or destructive lifecycle transitions, preventing orphaned applications from operating unaccountably.
- **Actor(s):** Application Owner, Platform Administrator, AI Coding Agent / Claude Code
- **Trigger:** Any owner-gated action is attempted (production deploy request, de-registration, secret access change, etc.).
- **Preconditions:** Application exists.
- **Main Flow:**
  1. Requested action is classified as owner-gated.
  2. Platform checks that the application has an active, non-deactivated owner.
  3. If present, the action proceeds to its normal authorization checks (Module J/H).
- **Alternative Flow:** None.
- **Exception Flow:** Owner is missing/deactivated → action is blocked and an ownership-remediation notification is raised to Platform Administrator (Module X).
- **Business Rules:** This gate is evaluated independently of, and in addition to, the requester's own role/permission check; it protects against orphaned production applications specifically.
- **Input:** Application, requested owner-gated action.
- **Output:** Allow/block determination.
- **Acceptance Criteria:** A production deployment request against an application with no active owner is blocked, with a specific "no active owner" error, until ownership is remediated.
- **Priority:** MUST

---

## Module F — Stack Management

#### FR-019 — Maintain Supported Stack Catalog

- **Description:** The platform must maintain an IT-governed catalog of supported technology stacks (frontend, backend, database, cache runtimes and versions) that applications may declare in their `deployment.yaml`, per the platform's Supported Stack v1 baseline.
- **Actor(s):** IT Administrator, Platform Administrator
- **Trigger:** Initial platform setup, or a stack addition/update/deprecation request.
- **Preconditions:** Requesting administrator holds stack-catalog management privileges.
- **Main Flow:**
  1. Administrator adds, updates, or deprecates a stack entry (e.g., runtime name, supported version range, default port conventions).
  2. Platform validates the entry against platform build/runtime capability (i.e., the Deployment Engine and Container Platform can actually build/run it).
  3. Platform publishes the updated catalog, immediately effective for new validations (Module H).
- **Alternative Flow:** None.
- **Exception Flow:** Administrator attempts to add a stack the Deployment Engine cannot build/run → rejected pending engine support.
- **Business Rules:** Supported Stack v1 baseline: Frontend — React, Next.js, Vue; Backend — Go, Node.js, Python; Database — PostgreSQL; Cache — Redis. The catalog is extensible by IT Administrators without requiring a platform code change; any stack not in the catalog fails validation (FR-021).
- **Input:** Stack entry (name, category, version range, build/runtime metadata).
- **Output:** Updated stack catalog.
- **Acceptance Criteria:** The current catalog is queryable (feeds MCP `get_supported_stacks`, Module Y); an application declaring a catalog stack passes stack validation, one declaring a non-catalog stack fails it.
- **Priority:** MUST

#### FR-020 — Stack Selection During Configuration

- **Description:** The platform must let an Employee/Agent select, per service defined in `deployment.yaml`, a runtime from the current supported stack catalog when configuring an application.
- **Actor(s):** Employee, AI Coding Agent / Claude Code
- **Trigger:** Employee/Agent authors or edits a service's runtime declaration in `deployment.yaml` (Module G).
- **Preconditions:** Stack catalog is populated (FR-019).
- **Main Flow:**
  1. Employee/Agent queries the supported stack catalog (directly, or via MCP `get_supported_stacks`).
  2. Employee/Agent selects a runtime for each declared service.
  3. Selection is written into the service's `runtime` field in `deployment.yaml`.
- **Alternative Flow:** Claude Code infers the appropriate runtime from the application's existing source code and pre-fills the selection for employee confirmation.
- **Exception Flow:** Selected runtime is not present in the current catalog → configuration is flagged invalid at validation time (Module H), not silently accepted.
- **Business Rules:** Runtime selection is per-service (e.g., `frontend` and `api` may use different stacks in the same application).
- **Input:** Desired runtime per service.
- **Output:** `deployment.yaml` service runtime fields populated.
- **Acceptance Criteria:** Only catalog runtimes are selectable/acceptable for a service's `runtime` field.
- **Priority:** MUST

#### FR-021 — Unsupported Stack Rejection

- **Description:** The platform must detect and reject, at validation time, any declared runtime, database, or cache technology not present in the current supported stack catalog, preventing unsupported or unvetted technology from reaching build/deploy.
- **Actor(s):** AI Coding Agent / Claude Code, Employee
- **Trigger:** Deployment Validation is run against an application's `deployment.yaml` (Module H).
- **Preconditions:** Stack catalog is populated (FR-019).
- **Main Flow:**
  1. Validation reads each service/database/cache technology declared in `deployment.yaml`.
  2. Validation checks each against the current catalog.
  3. Any unmatched technology produces a validation failure naming the offending field and the currently supported alternatives.
- **Alternative Flow:** None.
- **Exception Flow:** N/A — this requirement is itself an exception-detection mechanism for Module H.
- **Business Rules:** Rejection is deterministic and catalog-driven — the same declared stack always produces the same validation result regardless of who submits it (employee vs. agent).
- **Input:** `deployment.yaml` stack declarations.
- **Output:** Pass/fail validation result with specific unsupported-field detail.
- **Acceptance Criteria:** A `deployment.yaml` declaring a non-catalog runtime (e.g., PHP, MySQL) fails validation and never proceeds to Build (Module I).
- **Priority:** MUST

#### FR-022 — Stack Version / Runtime Governance

- **Description:** The platform must allow IT Administrators to govern which specific versions of a supported runtime are currently allowed, deprecated, or blocked (e.g., end-of-life language runtime versions), independent of adding/removing the runtime family itself.
- **Actor(s):** IT Administrator
- **Trigger:** A runtime version reaches end-of-support, a security advisory is issued, or a new version is qualified for use.
- **Preconditions:** Runtime family already exists in the catalog (FR-019).
- **Main Flow:**
  1. IT Administrator updates the allowed version range or deprecation status for a runtime.
  2. Platform applies the updated policy to future validations (FR-021) immediately.
  3. Existing running applications on a newly deprecated version are flagged for owner remediation (Module X) rather than force-stopped.
- **Alternative Flow:** IT Administrator blocks a version outright (e.g., known-vulnerable) rather than merely deprecating it, causing immediate validation failure for any new deploy on that version.
- **Exception Flow:** N/A.
- **Business Rules:** Deprecation warns; blocking prevents new deployments outright. Neither action automatically stops an already-Running application — that remains an explicit owner or Platform Administrator decision (Module K), consistent with not trusting automated force-changes to production without review.
- **Input:** Runtime, version range, status (allowed/deprecated/blocked).
- **Output:** Updated version governance policy.
- **Acceptance Criteria:** A new deployment attempt using a blocked version fails validation; a deprecated-version application continues running but its owner receives a remediation notification.
- **Priority:** SHOULD

---

## Module G — Deployment Configuration

#### FR-023 — Author `deployment.yaml` Application Contract

- **Description:** The platform must accept a single, application-level `deployment.yaml` contract as the sole declarative description of how an application is built and run — the mechanism through which the Employee, typically assisted by Claude Code and the Company Deployment Skill, expresses intent without touching raw infrastructure.
- **Actor(s):** Employee, AI Coding Agent / Claude Code
- **Trigger:** Employee/Agent authors or edits an application's configuration, in Draft state or later (Module K).
- **Preconditions:** Application is registered (FR-011).
- **Main Flow:**
  1. Employee/Agent authors/edits `deployment.yaml` fields: `app` (name, owner), `services` (per-service runtime, port), `database`, `scaling` (min/max), `resources` (tier), `domain` (visibility).
  2. Claude Code (via the Company Deployment Skill) may generate or suggest the contract from the application's source code.
  3. Draft `deployment.yaml` is saved against the application record.
- **Alternative Flow:** Employee edits `deployment.yaml` directly (e.g., via console) without Claude Code involvement.
- **Exception Flow:** Malformed YAML/unparseable submission → rejected with a parse error before it reaches schema validation (FR-024).
- **Business Rules:** `deployment.yaml` is the only accepted mechanism for describing how an application is built and run; there is no path for an employee or agent to submit raw Kubernetes manifests, Dockerfiles-as-infra-config, or equivalent low-level infrastructure definitions.
- **Input:** `deployment.yaml` content (full or partial).
- **Output:** Saved draft configuration on the application record.
- **Acceptance Criteria:** A syntactically valid `deployment.yaml` is persisted and retrievable; no lower-level infrastructure artifact is accepted in its place.
- **Priority:** MUST

#### FR-024 — Configuration Schema Validation

- **Description:** The platform must validate that a submitted `deployment.yaml` conforms to the platform's defined schema (required fields, allowed value types/ranges) before it can progress toward deployment.
- **Actor(s):** AI Coding Agent / Claude Code, Employee
- **Trigger:** `deployment.yaml` is saved or a validation is explicitly requested (Module H).
- **Preconditions:** A `deployment.yaml` draft exists (FR-023).
- **Main Flow:**
  1. Platform parses the submitted YAML.
  2. Platform checks required top-level sections (`app`, `services`) and field types against the schema.
  3. Schema-valid configuration is marked structurally valid, ready for deeper deployment validation (Module H).
- **Alternative Flow:** Partial/in-progress drafts are allowed to be saved without passing full schema validation, provided they are clearly marked incomplete and cannot proceed past Draft (Module K).
- **Exception Flow:** Missing required field or invalid type/value → rejected with field-level error detail returned to the employee/agent for correction.
- **Business Rules:** Schema validation is necessary but not sufficient for deployment — it governs structure only; business/security/quota validation is covered separately in Module H.
- **Input:** `deployment.yaml`.
- **Output:** Schema-valid/invalid determination with field-level errors.
- **Acceptance Criteria:** A `deployment.yaml` missing a required field (e.g., no `app.name`) is rejected with that field identified; a fully conformant document passes.
- **Priority:** MUST

#### FR-025 — Service Definition Configuration

- **Description:** The platform must allow one or more `services` (e.g., `frontend`, `api`) to be declared within a single application's `deployment.yaml`, each with its own runtime and, where applicable, network port.
- **Actor(s):** Employee, AI Coding Agent / Claude Code
- **Trigger:** Employee/Agent adds, edits, or removes a service entry in `deployment.yaml`.
- **Preconditions:** Application exists in Draft or an editable lifecycle state.
- **Main Flow:**
  1. Employee/Agent declares a named service with a `runtime` from the supported stack catalog (FR-020) and, for backend/API services, a `port`.
  2. Platform stores the service definition under the application's configuration.
  3. Each declared service becomes an independently buildable/deployable unit within the application (Modules I, J).
- **Alternative Flow:** A frontend-only or backend-only application declares just one service.
- **Exception Flow:** Duplicate service name within the same application, or a port conflict between services of the same application → rejected.
- **Business Rules:** Service names are unique within an application; only stateless web/API/worker services are eligible for scale-to-zero (Module L) — static frontends are not.
- **Input:** Service name, runtime, port (where applicable).
- **Output:** Service definitions within `deployment.yaml`.
- **Acceptance Criteria:** An application with two services declaring the same name is rejected at schema/validation time; a valid multi-service application (e.g., `frontend` + `api`) is accepted.
- **Priority:** MUST

#### FR-026 — Scaling Configuration (Min/Max Instances)

- **Description:** The platform must allow an application to declare `scaling.min` and `scaling.max` instance counts per eligible service, driving both baseline capacity and scale-to-zero behavior (Module L).
- **Actor(s):** Employee, AI Coding Agent / Claude Code
- **Trigger:** Employee/Agent sets or edits the `scaling` section of `deployment.yaml`.
- **Preconditions:** Application/service is defined (FR-025).
- **Main Flow:**
  1. Employee/Agent declares `scaling.min` (e.g., `0` to enable scale-to-zero) and `scaling.max`.
  2. Platform validates `min` ≤ `max` and both within department/tier ceilings (Module M).
  3. Values are stored and later enforced at runtime by the Deployment Engine (Module L).
- **Alternative Flow:** Employee omits `scaling` entirely → platform applies a documented default (e.g., `min: 0`, `max` per resource tier) rather than rejecting the configuration.
- **Exception Flow:** `min` > `max`, or `max` exceeds the department/tier ceiling → rejected with the specific constraint violated.
- **Business Rules:** `scaling.min: 0` is only meaningful for scale-to-zero-eligible service types (Module L); databases and static frontends ignore/reject a `min: 0` scale-to-zero interpretation since they are not eligible.
- **Input:** `scaling.min`, `scaling.max`.
- **Output:** Stored scaling configuration.
- **Acceptance Criteria:** A configuration with `min` greater than `max` is rejected; a valid configuration is honored by the Deployment Engine at runtime.
- **Priority:** MUST

#### FR-027 — Resource Tier Configuration

- **Description:** The platform must allow an application to declare a `resources.tier` (e.g., small/medium/large) in `deployment.yaml`, mapping to concrete CPU/memory/storage allocations maintained centrally (Module M) rather than allowing arbitrary raw resource requests.
- **Actor(s):** Employee, AI Coding Agent / Claude Code
- **Trigger:** Employee/Agent sets or edits `resources.tier`.
- **Preconditions:** Resource tier catalog exists (Module M, FR-057).
- **Main Flow:**
  1. Employee/Agent selects a tier from the current resource tier catalog.
  2. Platform validates the tier exists and is permitted for the application's department/environment.
  3. Tier selection is stored and resolved to concrete resource limits at deploy time (Module M).
- **Alternative Flow:** Employee omits `resources.tier` → platform applies the documented default tier.
- **Exception Flow:** Requested tier is not in the catalog, or exceeds department quota (FR-059) → rejected.
- **Business Rules:** Employees/agents never specify raw CPU/memory values directly in `deployment.yaml` — only an abstract tier name; concrete tier-to-resource mapping is owned by the Non-Functional/Resource Requirements doc.
- **Input:** `resources.tier`.
- **Output:** Stored resource tier selection.
- **Acceptance Criteria:** An undefined tier name is rejected at validation; a valid tier resolves to a concrete resource allocation at deploy time.
- **Priority:** MUST

#### FR-028 — Domain / Visibility Configuration

- **Description:** The platform must allow an application to declare its network `domain.visibility` (e.g., `internal` or `external`) in `deployment.yaml`, governing whether it is reachable only within the corporate network or also from outside it, subject to security policy (Module P, Q).
- **Actor(s):** Employee, AI Coding Agent / Claude Code, Security Administrator
- **Trigger:** Employee/Agent sets or edits `domain.visibility`.
- **Preconditions:** Application/service is defined.
- **Main Flow:**
  1. Employee/Agent declares `domain.visibility` (default: `internal`).
  2. Platform stores the declared visibility.
  3. At deployment time, visibility is enforced by domain/networking provisioning (Modules P, Q), with `external` subject to the additional approval gate (FR-074).
- **Alternative Flow:** No `domain` section provided → platform defaults to `internal`, the safer posture.
- **Exception Flow:** `external` visibility requested for a service that is policy-restricted to internal-only (e.g., a raw database service) → rejected at validation (Module H).
- **Business Rules:** Databases and internal-only backend services can never be declared `external`, regardless of employee/agent request — this is enforced independently by platform policy, not merely by client-side configuration (see Module Q security rules).
- **Input:** `domain.visibility`.
- **Output:** Stored visibility configuration.
- **Acceptance Criteria:** A database service configured with `external` visibility fails validation; a compliant frontend/API configured `external` proceeds to the approval gate.
- **Priority:** MUST

---

## Module H — Deployment Validation

#### FR-029 — Automated Pre-Deployment Validation Pass

- **Description:** The platform must run a comprehensive automated validation pass over an application's configuration before any build or deployment step begins, aggregating schema, stack, security, quota, and naming checks into a single pass/fail decision with itemized results.
- **Actor(s):** AI Coding Agent / Claude Code, Employee
- **Trigger:** Employee/Agent explicitly requests validation, or a deployment request implicitly triggers validation as its first gated step (Module J, "Validation" step).
- **Preconditions:** `deployment.yaml` exists and is schema-valid (FR-024).
- **Main Flow:**
  1. Platform runs schema validation (FR-024, if not already passed).
  2. Platform runs stack compliance validation (FR-030).
  3. Platform runs security policy pre-checks (FR-031).
  4. Platform runs resource quota checks (FR-032).
  5. Platform runs naming/domain conflict checks (FR-033).
  6. Platform aggregates all results and returns a single validation report (FR-034).
- **Alternative Flow:** Validation is run in a "dry-run" mode explicitly requested by the employee/agent to check readiness without attempting a deployment.
- **Exception Flow:** Any sub-check fails → overall validation fails; the pipeline does not proceed to Build (Module I) regardless of how many other sub-checks passed.
- **Business Rules:** Validation is mandatory and cannot be bypassed for any environment, including dev; only the downstream approval requirement differs between dev and production (Module J).
- **Input:** Application configuration (`deployment.yaml`).
- **Output:** Aggregated validation report (pass/fail per sub-check).
- **Acceptance Criteria:** An application failing any sub-check does not proceed to Build; an application passing all sub-checks is marked Validated (Module K) and eligible to proceed.
- **Priority:** MUST

#### FR-030 — Stack Compliance Validation

- **Description:** The platform must verify, as part of validation, that every runtime/database/cache declared in `deployment.yaml` is present and currently allowed in the supported stack catalog (Module F).
- **Actor(s):** AI Coding Agent / Claude Code
- **Trigger:** Invoked as a sub-check of FR-029.
- **Preconditions:** Stack catalog is available (FR-019).
- **Main Flow:**
  1. Validation reads all declared stack technologies from `deployment.yaml`.
  2. Each is checked against the catalog's allowed entries and version governance (FR-022).
  3. Result (pass/fail with specifics) is returned to the aggregate report.
- **Alternative Flow:** None.
- **Exception Flow:** Unsupported or blocked-version technology found → sub-check fails, naming the offending field (mirrors FR-021).
- **Business Rules:** This sub-check duplicates FR-021's detection logic but is scoped as a formal, itemized member of the aggregate validation report.
- **Input:** `deployment.yaml` stack declarations.
- **Output:** Stack compliance pass/fail with detail.
- **Acceptance Criteria:** Included as a distinctly reported line item in every validation report.
- **Priority:** MUST

#### FR-031 — Security Policy Pre-Check

- **Description:** The platform must verify, as part of validation, that the configuration does not request anything prohibited by security policy — privileged containers, host filesystem or Docker socket access, direct external exposure of a database, arbitrary infrastructure modification, or cross-application secret/database access — before any build or deploy step runs.
- **Actor(s):** AI Coding Agent / Claude Code, Security Administrator
- **Trigger:** Invoked as a sub-check of FR-029.
- **Preconditions:** None beyond a schema-valid configuration.
- **Main Flow:**
  1. Validation inspects the configuration for any field or combination implying a prohibited capability.
  2. Each prohibited pattern found is recorded as a specific violation.
  3. Presence of zero violations passes the sub-check.
- **Alternative Flow:** None — because `deployment.yaml` is a constrained declarative schema (Module G), most prohibited infrastructure actions have no expressible field in the first place; this check covers the residual policy-level combinations that are structurally expressible (e.g., `external` visibility on a database).
- **Exception Flow:** A violation is found → sub-check fails; repeated attempts to submit a policy-violating configuration are flagged to the Security Administrator (Module W/X) as a potential signal of misuse.
- **Business Rules:** This check is enforced by the Platform API/Deployment Engine independently of Claude Code's own guardrails — the AI agent is never trusted as the security boundary; even a configuration an agent "should" have refused to generate is still independently re-checked server-side.
- **Input:** `deployment.yaml`.
- **Output:** Security pre-check pass/fail with itemized violations.
- **Acceptance Criteria:** A configuration attempting to expose a database externally, or any other prohibited pattern, fails this sub-check regardless of how it was authored (console, employee, or agent).
- **Priority:** MUST

#### FR-032 — Resource Quota Validation

- **Description:** The platform must verify, as part of validation, that the requested resource tier, scaling limits, and count of applications do not exceed the requesting application's department/tier quotas.
- **Actor(s):** AI Coding Agent / Claude Code, Platform Administrator
- **Trigger:** Invoked as a sub-check of FR-029.
- **Preconditions:** Department quota/tier is assigned (FR-010); resource tier catalog exists (FR-057).
- **Main Flow:**
  1. Validation computes the requested resource footprint from `resources.tier` and `scaling.max`.
  2. Validation compares the footprint against the department's remaining quota.
  3. Within-quota requests pass; over-quota requests fail with the specific quota exceeded.
- **Alternative Flow:** None.
- **Exception Flow:** Department has no assigned quota/tier → validation fails closed (treated as zero quota) rather than silently passing.
- **Business Rules:** Exact quota numbers are **TBD — see Decision Log**; enforcement mechanism is defined here regardless of final values.
- **Input:** Requested resource footprint; department quota.
- **Output:** Quota pass/fail with detail.
- **Acceptance Criteria:** A request exceeding the department's remaining quota fails this sub-check with the specific over-quota resource named.
- **Priority:** MUST

#### FR-033 — Naming and Domain Conflict Validation

- **Description:** The platform must verify, as part of validation, that the application name and any derived domain/subdomain do not conflict with an existing application's name or domain.
- **Actor(s):** AI Coding Agent / Claude Code
- **Trigger:** Invoked as a sub-check of FR-029 (and re-checked at registration time, FR-012).
- **Preconditions:** None.
- **Main Flow:**
  1. Validation derives the expected domain(s) from the application name and visibility (Module P).
  2. Validation checks for collisions against existing applications' names/domains.
  3. No collision passes the sub-check.
- **Alternative Flow:** None.
- **Exception Flow:** Collision found → sub-check fails, naming the conflicting application/domain.
- **Business Rules:** Domain derivation follows a deterministic, platform-defined convention (owned in detail by Module P) so this check can run without provisioning an actual domain.
- **Input:** Application name, visibility.
- **Output:** Naming/domain conflict pass/fail.
- **Acceptance Criteria:** Two applications that would resolve to the same domain cannot both pass validation.
- **Priority:** MUST

#### FR-034 — Validation Result Reporting

- **Description:** The platform must return a clear, itemized validation report to the requesting Employee/Agent, identifying exactly which sub-checks passed or failed and why, so that failures can be corrected and re-submitted without guesswork.
- **Actor(s):** AI Coding Agent / Claude Code, Employee
- **Trigger:** Completion of an aggregate validation run (FR-029).
- **Preconditions:** A validation run has completed.
- **Main Flow:**
  1. Platform compiles results from all sub-checks (FR-030–FR-033 and FR-024).
  2. Platform formats an itemized report (per sub-check: pass/fail, human-readable reason, offending field where applicable).
  3. Report is returned to the caller (console, or MCP `validate_application`, Module Y) for the employee/agent to act on.
- **Alternative Flow:** Report is also persisted against the application record for later reference (e.g., audit, Module W).
- **Exception Flow:** N/A.
- **Business Rules:** The report must be specific enough that Claude Code can autonomously correct common failures (e.g., unsupported stack, missing field) and re-submit without requiring the employee to interpret a generic error.
- **Input:** Sub-check results.
- **Output:** Itemized validation report.
- **Acceptance Criteria:** A failed validation report names every failing sub-check and the specific reason for each; a passed report is unambiguous that the application is ready to proceed to Build.
- **Priority:** MUST

---

## Module I — Build Management

#### FR-035 — Trigger Build from Source

- **Description:** The platform must build a deployable container image for each declared service from the application's source code once the application has passed validation, without the employee or agent ever invoking a Docker daemon or build tool directly.
- **Actor(s):** AI Coding Agent / Claude Code, Employee
- **Trigger:** Successful validation (Module H) followed by a deployment request (Module J), or an explicit build-only request.
- **Preconditions:** Application is in Validated state (Module K); source code is accessible to the platform's build system.
- **Main Flow:**
  1. Employee/Agent submits a deployment (or build) request referencing the application's current source revision.
  2. Deployment Engine retrieves the source and the validated `deployment.yaml`.
  3. Deployment Engine builds a container image per service using the standard base image for the declared runtime (Module F).
  4. Build artifact is registered for the subsequent Image Scan step (Module J).
- **Alternative Flow:** A previously built, unchanged image is reused (build cache) rather than rebuilt from scratch, if the platform's build system supports it.
- **Exception Flow:** Source is unreachable, or build fails (compile/dependency error) → build marked Failed (Module K), detailed failure output returned (FR-038).
- **Business Rules:** All builds use IT-governed standard base images per runtime (Module F) — arbitrary custom base images are not accepted; the employee/agent never has direct Docker/daemon access at any point in this flow.
- **Input:** Source code revision, validated `deployment.yaml`.
- **Output:** Container image artifact per service.
- **Acceptance Criteria:** A validated application with buildable source produces a container image per declared service without any direct Docker/daemon interaction by the employee or agent.
- **Priority:** MUST

#### FR-036 — Build Status Tracking

- **Description:** The platform must track and expose the real-time status of an in-progress or completed build so the employee/agent can monitor progress and react to outcomes.
- **Actor(s):** Employee, AI Coding Agent / Claude Code
- **Trigger:** A build is initiated (FR-035).
- **Preconditions:** A build has been triggered.
- **Main Flow:**
  1. Deployment Engine reports build state transitions (queued, in-progress, succeeded, failed) to the Platform API.
  2. Platform API persists current build status against the application/deployment record.
  3. Employee/Agent queries status directly or via `get_deployment_status` (Module Y).
- **Alternative Flow:** Employee/Agent subscribes to status change notifications instead of polling (Module X).
- **Exception Flow:** Status reporting itself fails (engine unreachable) → status marked Unknown/Stale rather than silently showing a stale success.
- **Business Rules:** Build status is always attributable to a specific application, service, and source revision.
- **Input:** Build state events from the Deployment Engine.
- **Output:** Queryable build status.
- **Acceptance Criteria:** Querying an in-progress build returns a non-terminal status; querying after completion returns a terminal status (succeeded/failed) matching the actual outcome.
- **Priority:** MUST

#### FR-037 — Standard Base Image Governance

- **Description:** The platform must build every application's container images from a catalog of IT-governed standard base images (per supported runtime), ensuring a consistent, security-patched foundation across all applications rather than employee-supplied base images.
- **Actor(s):** IT Administrator, Security Administrator
- **Trigger:** Build execution (FR-035); base image catalog update.
- **Preconditions:** A base image is published per supported runtime (Module F).
- **Main Flow:**
  1. IT Administrator publishes/updates a standard base image for a runtime (e.g., patched Node.js base).
  2. Deployment Engine uses the current standard base image whenever building a service of that runtime.
  3. Base image updates apply to subsequent builds; already-built images are unaffected until rebuilt.
- **Alternative Flow:** Security Administrator forces base image deprecation following a vulnerability disclosure, blocking new builds on the old base until applications rebuild on the patched one.
- **Exception Flow:** No standard base image exists for a validated runtime → build fails closed rather than falling back to an arbitrary image.
- **Business Rules:** Employees/agents cannot specify a custom base image in `deployment.yaml` — base image selection is implicit from the declared runtime and fully IT-governed.
- **Input:** Runtime, standard base image reference.
- **Output:** Built image based on the current standard base.
- **Acceptance Criteria:** Two applications using the same runtime and stack version build from the identical governed base image.
- **Priority:** MUST

#### FR-038 — Build Failure Handling and Reporting

- **Description:** The platform must capture and surface actionable build failure detail (e.g., compile error, missing dependency) to the employee/agent so the failure can be diagnosed and corrected without platform administrator involvement for routine cases.
- **Actor(s):** AI Coding Agent / Claude Code, Employee
- **Trigger:** A build fails (FR-035).
- **Preconditions:** A build was in progress.
- **Main Flow:**
  1. Deployment Engine captures build failure output (logs, error summary).
  2. Platform API marks the build/application Failed (Module K) and stores the failure detail.
  3. Failure detail is returned to the employee/agent (directly or via `get_deployment_status`/`get_application_logs`, Module Y) for correction and resubmission.
- **Alternative Flow:** Claude Code parses the failure detail and autonomously attempts a source-code fix before resubmitting, subject to employee confirmation for any non-trivial change.
- **Exception Flow:** Build fails for a platform-side reason (e.g., build infrastructure outage) rather than a source issue → distinguished in the report so the employee is not misled into debugging their own code.
- **Business Rules:** Build failure never leaves an application in an ambiguous state — it is always explicitly marked Failed with a retrievable cause.
- **Input:** Build failure output.
- **Output:** Structured build failure report; application state = Failed.
- **Acceptance Criteria:** Every build failure is retrievable with a specific cause distinguishing source-code error from platform/infrastructure error.
- **Priority:** MUST

---

## Module J — Deployment Management

#### FR-039 — Initiate Deployment Request

- **Description:** The platform must accept a deployment request for a validated application and drive it through the full deployment pipeline (Request → Authentication → Authorization → Validation → Security Check → Build → Image Scan → Registry → Deployment → Health Check → Traffic Activation → Monitoring → Completed), coordinating the Company Platform API, Deployment Engine, and Container Platform.
- **Actor(s):** Employee, AI Coding Agent / Claude Code
- **Trigger:** Employee/Agent submits a deployment request (console, CLI, or MCP `deploy_application`, Module Y).
- **Preconditions:** Application has passed validation (Module H); requester is authorized to deploy this application to the target environment.
- **Main Flow:**
  1. Request received and the requester authenticated (Module A).
  2. Requester authorized for this application/environment (role, ownership per Module E).
  3. Configuration (re-)validated (Module H) and security-checked (FR-031).
  4. Build executed (Module I) and resulting image scanned (FR-041).
  5. Image pushed to the internal registry.
  6. Deployment Engine deploys to the Container Platform, health-checked (Module R), and traffic activated.
  7. Deployment marked Completed; ongoing monitoring begins (Module T).
- **Alternative Flow:** Deployment targets dev, which may auto-deploy without the explicit human approval step required for production (FR-042).
- **Exception Flow:** Failure at any gated step halts the pipeline at that step, marks the deployment Failed, and may trigger automatic rollback (FR-044, Module V) if a prior good version exists.
- **Business Rules:** Every step in the pipeline is enforced server-side by the Platform API/Deployment Engine — the AI agent's own judgment is never a substitute for any gate; no step may be skipped regardless of caller.
- **Input:** Deployment request (application, target environment/version).
- **Output:** Running deployment (on success) or a Failed deployment with cause (on failure).
- **Acceptance Criteria:** A deployment request for an application that has not passed validation is rejected before Build; a fully compliant request reaches Completed with the application in Running state.
- **Priority:** MUST

#### FR-040 — Deployment Pipeline Orchestration

- **Description:** The platform must orchestrate the ordered execution of every deployment pipeline step, guaranteeing steps execute in sequence, each gate is fully evaluated before the next step starts, and the current step is always visible.
- **Actor(s):** AI Coding Agent / Claude Code, Employee
- **Trigger:** A deployment request has passed initial acceptance (FR-039).
- **Preconditions:** Deployment request is queued.
- **Main Flow:**
  1. Orchestrator executes each pipeline step in the defined order.
  2. Each step's outcome (pass/fail) is recorded before the next step is invoked.
  3. Current step and outcome are exposed via deployment status (FR-043).
- **Alternative Flow:** Independent services within a multi-service application (e.g., `frontend` and `api`) may build/deploy in parallel where the pipeline design allows, provided ordering of gated steps is preserved per service.
- **Exception Flow:** A step fails → orchestrator halts forward progress and invokes the Failure/Rollback branch (FR-044) rather than continuing to later steps.
- **Business Rules:** No step is ever skipped or reordered based on caller identity (employee vs. agent) or environment, except the explicit dev-vs-production approval difference (FR-042).
- **Input:** Deployment request.
- **Output:** Ordered execution record of all pipeline steps.
- **Acceptance Criteria:** The recorded step sequence for any completed deployment matches the defined pipeline order exactly, with no step missing.
- **Priority:** MUST

#### FR-041 — Image Scan Gate

- **Description:** The platform must scan every built container image for known vulnerabilities and policy violations before it is pushed to the registry or deployed, blocking images that fail the scan.
- **Actor(s):** Security Administrator, AI Coding Agent / Claude Code
- **Trigger:** A build completes successfully (Module I), immediately preceding registry push.
- **Preconditions:** Image build succeeded.
- **Main Flow:**
  1. Deployment Engine submits the built image for vulnerability/policy scanning.
  2. Scan results are evaluated against the platform's severity threshold policy.
  3. Passing images proceed to registry push; failing images halt the pipeline.
- **Alternative Flow:** Security Administrator grants a time-boxed exception for a specific known/accepted finding, allowing the image through with the exception logged.
- **Exception Flow:** Scan service itself is unavailable → pipeline fails closed (deployment blocked) rather than skipping the scan.
- **Business Rules:** Severity threshold for blocking is defined by security policy (exact thresholds owned by the Security/Non-Functional docs); scan results are retained for audit (Module W) regardless of outcome.
- **Input:** Built container image.
- **Output:** Scan report; pass/fail gate decision.
- **Acceptance Criteria:** An image with a blocking-severity vulnerability does not reach the registry or deployment step; scan results are retrievable after the fact.
- **Priority:** MUST

#### FR-042 — Production Approval Gate

- **Description:** The platform must require explicit human approval before a deployment to a production environment is allowed to proceed past validation/security checks, while permitting dev deployments to proceed automatically.
- **Actor(s):** Application Owner, Platform Administrator, Employee
- **Trigger:** A deployment request targets a production environment.
- **Preconditions:** Deployment has passed validation, security check, build, and image scan.
- **Main Flow:**
  1. Pipeline reaches the production approval checkpoint and pauses.
  2. Platform notifies the designated approver(s) (Module X).
  3. Approver reviews the deployment request (version, diff, scan results) and approves or rejects.
  4. On approval, the pipeline resumes toward Deployment/Health Check/Traffic Activation.
- **Alternative Flow:** Dev-environment deployments skip this checkpoint entirely and proceed automatically once prior gates pass.
- **Exception Flow:** Approver rejects → deployment marked Failed (rejected) with reason recorded; requester notified.
- **Business Rules:** The specific approver role/policy (e.g., Application Owner only, or Application Owner + Platform Administrator) is configurable per department/application criticality; production deploys can never bypass this gate regardless of requester role, including Platform Administrators acting as requester.
- **Input:** Deployment request pending approval.
- **Output:** Approved/rejected decision.
- **Acceptance Criteria:** No production deployment reaches the Deployment step without a recorded approval decision; a dev deployment reaches it without requiring one.
- **Priority:** MUST

#### FR-043 — Deployment Status Tracking and Query

- **Description:** The platform must expose the real-time and historical status of any deployment (current pipeline step, outcome, timestamps) to authorized employees/agents/administrators.
- **Actor(s):** Employee, AI Coding Agent / Claude Code, Application Owner
- **Trigger:** A deployment is initiated; status is queried at any point during or after.
- **Preconditions:** Deployment request exists.
- **Main Flow:**
  1. Pipeline orchestration (FR-040) emits step-level status events.
  2. Platform API persists and aggregates current status per deployment.
  3. Requester queries status directly, via console, or via MCP `get_deployment_status` (Module Y).
- **Alternative Flow:** Status changes are pushed as notifications instead of polled (Module X).
- **Exception Flow:** Query for a non-existent or unauthorized deployment ID → rejected/not-found, never leaking another application's deployment detail.
- **Business Rules:** Deployment status is scoped to applications the requester is authorized to view (ownership/role, Module E).
- **Input:** Deployment ID.
- **Output:** Current/historical deployment status.
- **Acceptance Criteria:** A deployment's status accurately reflects its current pipeline step at query time; unauthorized requesters cannot retrieve another application's deployment status.
- **Priority:** MUST

#### FR-044 — Deployment Failure Handling and Auto-Rollback Trigger

- **Description:** The platform must handle a failure at any gated pipeline step by halting forward progress, marking the deployment Failed, and — where a previously good running version exists — automatically initiating rollback (Module V) to preserve application availability.
- **Actor(s):** AI Coding Agent / Claude Code, Application Owner
- **Trigger:** Any pipeline step fails (validation, security check, build, scan, health check, etc.).
- **Preconditions:** A deployment is in progress.
- **Main Flow:**
  1. Failing step reports failure with cause to the orchestrator.
  2. Orchestrator halts the pipeline and marks the deployment Failed (Module K).
  3. If a prior Running version exists and the failure occurred post-Traffic-Activation (e.g., health check regression), automatic rollback is triggered (FR-099).
  4. Failure and any rollback action are reported to the requester and Application Owner (Module X).
- **Alternative Flow:** Failure occurs pre-Traffic-Activation (e.g., build/scan failure) → no rollback is needed since the previously Running version was never affected; only the failed attempt is marked Failed.
- **Exception Flow:** Rollback itself fails → escalated immediately to Application Owner and Platform Administrator as a critical incident.
- **Business Rules:** Auto-rollback only applies once traffic has been activated to a new version and that version subsequently fails health checks; pre-activation failures never affect the currently Running version.
- **Input:** Pipeline failure event.
- **Output:** Deployment marked Failed; rollback initiated where applicable.
- **Acceptance Criteria:** A post-activation health-check failure results in traffic remaining on (or reverting to) the last known-good version, with no employee-visible downtime beyond the platform's defined recovery window.
- **Priority:** MUST

---

## Module K — Application Lifecycle

#### FR-045 — Lifecycle State Model Enforcement

- **Description:** The platform must enforce that every application occupies exactly one defined lifecycle state at a time — `Draft → Validated → Build → Deploying → Running → Suspended → Failed → Rolled Back → Archived → Deleted` — and that only platform-defined transitions between states are permitted.
- **Actor(s):** AI Coding Agent / Claude Code, Employee, Platform Administrator
- **Trigger:** Any action that would change an application's operational status.
- **Preconditions:** Application exists.
- **Main Flow:**
  1. Requested action is mapped to a target lifecycle state.
  2. Platform checks the transition is valid from the application's current state per the defined state model.
  3. Valid transitions are applied and recorded; the new state becomes authoritative for all downstream modules (scaling, health, etc.).
- **Alternative Flow:** None — invalid transitions are always rejected rather than coerced.
- **Exception Flow:** Requested transition is not defined from the current state (e.g., `Draft → Running` directly) → rejected with the valid next states listed.
- **Business Rules:** State transitions are the single source of truth for what operations are permitted elsewhere (e.g., only a `Running` application can be Suspended; only a `Validated` application can enter `Build`).
- **Input:** Current state, requested transition/action.
- **Output:** New application state, or rejection.
- **Acceptance Criteria:** An attempted invalid transition (e.g., deploying a `Draft` application directly without validation) is rejected; every valid transition is reflected in the application's queryable state.
- **Priority:** MUST

#### FR-046 — Lifecycle State Transition Triggers

- **Description:** The platform must define and enforce the specific triggering action for each lifecycle state transition, ensuring state changes only occur as a result of a legitimate, attributable platform event — never a direct, unmediated state edit.
- **Actor(s):** AI Coding Agent / Claude Code, Employee, Application Owner
- **Trigger:** Registration (→Draft), validation pass (→Validated), build start (→Build), deployment start (→Deploying), successful traffic activation (→Running), suspend request (→Suspended), any pipeline failure (→Failed), rollback completion (→Rolled Back), archive request (→Archived), deletion request (→Deleted).
- **Preconditions:** Application is in a state from which the triggering action is valid.
- **Main Flow:**
  1. A defined triggering action occurs (e.g., successful Health Check + Traffic Activation, Module J).
  2. Platform applies the corresponding state transition (FR-045).
  3. Transition and its trigger are recorded in the audit log (Module W).
- **Alternative Flow:** None.
- **Exception Flow:** An action attempts to force a state transition without its defined trigger having actually occurred (e.g., manually flipping to Running without a completed deployment) → rejected; no direct state-write capability is exposed to any actor, including administrators, outside these defined triggers.
- **Business Rules:** State is always derived from platform events, never directly settable.
- **Input:** Triggering event.
- **Output:** State transition.
- **Acceptance Criteria:** Every state change in the audit log has a corresponding, valid triggering event.
- **Priority:** MUST

#### FR-047 — Application Suspend

- **Description:** The platform must allow a Running application to be Suspended — stopping it from serving traffic and consuming compute while retaining its configuration, data, and identity for later resumption.
- **Actor(s):** Application Owner, Platform Administrator, Security Administrator
- **Trigger:** Owner-initiated suspend request, or administrative/security suspend (e.g., policy violation, cost control).
- **Preconditions:** Application is in Running state.
- **Main Flow:**
  1. Requester initiates suspend.
  2. Platform stops traffic routing to the application and scales its services to zero instances.
  3. Application transitions to Suspended; database and stored secrets/config are retained untouched.
- **Alternative Flow:** Security Administrator force-suspends an application immediately upon detecting a policy violation, bypassing the normal owner-initiated flow.
- **Exception Flow:** Suspend requested on an application already Suspended/Failed → no-op response indicating current state.
- **Business Rules:** Suspend is reversible via Resume (FR-048); it does not deprovision the database (Module N) or delete secrets (Module O).
- **Input:** Suspend request.
- **Output:** Application in Suspended state; traffic stopped.
- **Acceptance Criteria:** A Suspended application serves no traffic and consumes no compute instances, while its data and configuration remain intact and retrievable.
- **Priority:** MUST

#### FR-048 — Application Resume / Restart

- **Description:** The platform must allow a Suspended (or otherwise stopped) application to be resumed back to Running, and allow a Running application to be Restarted (recycle running instances) without a full redeploy.
- **Actor(s):** Application Owner, AI Coding Agent / Claude Code
- **Trigger:** Owner/agent-initiated resume or restart request (or MCP `restart_application`, Module Y).
- **Preconditions:** Application is Suspended (for resume) or Running (for restart).
- **Main Flow:**
  1. Requester initiates resume/restart.
  2. Platform re-establishes routing and starts service instances at the configured `scaling.min` (resume), or gracefully recycles existing instances (restart).
  3. Health checks confirm readiness (Module R) before restoring full traffic.
  4. Application state updates accordingly (Suspended → Running for resume; Running throughout for restart).
- **Alternative Flow:** Resume fails health checks → application remains in a non-serving state and the failure is reported rather than silently marked Running.
- **Exception Flow:** Restart/resume requested on a Deleted or Archived application → rejected; those require explicit un-archive/re-registration flows instead.
- **Business Rules:** Restart never changes the deployed version/configuration — it is a runtime recycling operation only, distinct from a new Deployment (Module J).
- **Input:** Resume/restart request.
- **Output:** Application returned to Running with healthy instances.
- **Acceptance Criteria:** A resumed application serves traffic again at its configured minimum scale; a restarted application shows fresh instance start times without a version change.
- **Priority:** MUST

#### FR-049 — Application Archive

- **Description:** The platform must allow an application to be Archived — retaining its configuration, metadata, and historical records for reference/compliance while releasing its active runtime footprint (compute, and optionally domain) more permanently than a Suspend.
- **Actor(s):** Application Owner, Platform Administrator
- **Trigger:** Owner-initiated archive request, typically as part of de-registration (FR-014) or long-term retirement without full deletion.
- **Preconditions:** Application is Suspended or Running; requester is the owner or a Platform Administrator.
- **Main Flow:**
  1. Requester initiates archive.
  2. Platform stops all traffic/compute (if not already Suspended) and releases the application's domain (Module P) and non-essential runtime resources.
  3. Application transitions to Archived; configuration, audit history, and version history remain retrievable.
- **Alternative Flow:** None.
- **Exception Flow:** Archive requested while a deployment is actively in progress → rejected until the in-flight deployment completes or is cancelled.
- **Business Rules:** Archived applications cannot serve traffic and cannot be directly resumed to Running — reactivation requires an explicit un-archive action treated as a new deployment cycle; archive is distinct from delete in that underlying data (e.g., database) retention follows a longer/compliance-driven policy (exact period **TBD — see Decision Log**).
- **Input:** Archive request.
- **Output:** Application in Archived state; domain released.
- **Acceptance Criteria:** An Archived application is not reachable by any traffic and consumes no active compute, while its configuration and history remain queryable.
- **Priority:** SHOULD

#### FR-050 — Application Deletion

- **Description:** The platform must allow an application to be permanently Deleted, deprovisioning all associated runtime resources, database, secrets, and domain, subject to a retention/grace policy and irreversible once complete.
- **Actor(s):** Application Owner, Platform Administrator
- **Trigger:** Owner/administrator-initiated delete request, typically following FR-014.
- **Preconditions:** Application is Archived or Suspended (deletion directly from Running requires an explicit stop-first confirmation); requester is authorized (ownership + approval per FR-014 for production).
- **Main Flow:**
  1. Requester confirms deletion, acknowledging irreversibility.
  2. Platform deprovisions the database (Module N), revokes/deletes secrets (Module O), releases the domain (Module P), and removes runtime configuration.
  3. Application transitions to Deleted; a minimal audit tombstone record is retained per the audit retention policy (Module W).
- **Alternative Flow:** A soft-delete grace period (exact duration **TBD — see Decision Log**) holds the application recoverable before hard deletion completes.
- **Exception Flow:** Deletion of a production application without required approval → rejected (mirrors FR-014).
- **Business Rules:** Deletion is the only lifecycle state from which no further transition is possible; audit records referencing the deleted application (Module W) are never deleted, only the live application and its runtime resources.
- **Input:** Delete request, confirmation.
- **Output:** Application in Deleted state; all associated resources deprovisioned.
- **Acceptance Criteria:** A Deleted application's domain, database, and secrets are confirmed deprovisioned; its historical audit trail remains queryable by Auditors.
- **Priority:** MUST

---

## Module L — Scale-to-Zero

#### FR-051 — Scale-to-Zero Eligibility Determination

- **Description:** The platform must determine, per declared service, whether it is eligible for scale-to-zero (0→N→0) behavior — limited strictly to stateless web/API/worker workloads — and must never apply scale-to-zero to static frontends or databases.
- **Actor(s):** AI Coding Agent / Claude Code, Employee
- **Trigger:** Application validation (Module H) or deployment (Module J).
- **Preconditions:** Service type is declared (Module G).
- **Main Flow:**
  1. Platform inspects each declared service's type/runtime.
  2. Stateless web/API/worker services with `scaling.min: 0` are marked scale-to-zero-eligible.
  3. Static frontend services and database instances are marked ineligible regardless of declared `scaling.min`.
- **Alternative Flow:** Employee declares `scaling.min: 0` on an ineligible service type → platform silently normalizes it to the minimum sensible value for that type (e.g., `1`) and flags this in the validation report (Module H), rather than failing outright, since it is a configuration mismatch rather than a security issue.
- **Exception Flow:** N/A.
- **Business Rules:** Eligibility is fully determined by service type, not by employee declaration — this determination cannot be overridden by configuration.
- **Input:** Service type, `scaling.min`.
- **Output:** Per-service scale-to-zero eligibility flag.
- **Acceptance Criteria:** A database service never scales to zero regardless of its `scaling` configuration; an eligible API/worker service with `min: 0` is confirmed as scale-to-zero-managed at deploy time.
- **Priority:** MUST

#### FR-052 — Idle Detection and Scale-Down to Zero

- **Description:** The platform must detect when a scale-to-zero-eligible service has received no traffic/work for its configured idle threshold and scale its running instance count down to zero.
- **Actor(s):** AI Coding Agent / Claude Code (indirectly, as a consumer of the resulting state)
- **Trigger:** Continuous idle-time monitoring on a Running, scale-to-zero-eligible service.
- **Preconditions:** Service is eligible (FR-051) and currently has zero-to-many active instances above its configured minimum of zero.
- **Main Flow:**
  1. Deployment Engine tracks request/work activity per service instance.
  2. When no activity is observed for the configured idle threshold, the Engine initiates graceful instance shutdown.
  3. Instance count reaches zero; the service remains registered and routable but not actively running.
  4. Scale event is logged (FR-056).
- **Alternative Flow:** In-flight requests are allowed to complete before the last instance is shut down (graceful drain) rather than being dropped.
- **Exception Flow:** A new request arrives during the drain/shutdown sequence → treated as a wake trigger (FR-053) rather than dropped.
- **Business Rules:** The idle threshold is a platform-defined default, potentially tunable per resource tier (exact value **TBD — see Decision Log**); scale-down never applies to database instances or static frontends (FR-051).
- **Input:** Service activity/idle signal.
- **Output:** Service instance count = 0.
- **Acceptance Criteria:** An eligible service with no traffic for the idle threshold reaches zero running instances while remaining addressable for the next request.
- **Priority:** MUST

#### FR-053 — Cold-Start Scale-Up on Incoming Traffic

- **Description:** The platform must detect an incoming request/work item to a scaled-to-zero service and automatically start at least one instance to handle it, activating traffic once the instance is healthy.
- **Actor(s):** AI Coding Agent / Claude Code (indirectly)
- **Trigger:** A request arrives for a service currently at zero running instances.
- **Preconditions:** Service is scaled to zero (FR-052) and still Running/registered (not Suspended).
- **Main Flow:**
  1. Ingress/routing layer receives a request for the zero-instance service.
  2. Deployment Engine starts one instance of the service.
  3. Health check confirms readiness (Module R).
  4. Request is routed to the now-healthy instance; the service scales further per demand up to `scaling.max`.
- **Alternative Flow:** Multiple simultaneous requests during cold-start are queued/buffered briefly rather than all separately triggering redundant start attempts.
- **Exception Flow:** Instance fails to become healthy within the platform's start timeout → request fails with a retryable error, and the scale-up attempt is logged as a failure for monitoring (Module T).
- **Business Rules:** Cold-start latency is a known trade-off of scale-to-zero and is documented as a Non-Functional concern; this requirement covers only the functional guarantee that traffic is eventually served once an instance is healthy.
- **Input:** Incoming request to a zero-instance service.
- **Output:** At least one running, healthy instance serving the request.
- **Acceptance Criteria:** A request to a scaled-to-zero eligible service results in a successful response after cold-start, without requiring any manual intervention.
- **Priority:** MUST

#### FR-054 — Min/Max Instance Enforcement

- **Description:** The platform must continuously enforce that a service's running instance count never falls below `scaling.min` or exceeds `scaling.max` as declared in `deployment.yaml` (Module G).
- **Actor(s):** AI Coding Agent / Claude Code (indirectly)
- **Trigger:** Any scaling event (scale-up, scale-down, deploy, restart).
- **Preconditions:** Service has a defined `scaling.min`/`scaling.max`.
- **Main Flow:**
  1. Deployment Engine evaluates the current instance count against configured bounds on every scaling decision.
  2. Scale-down operations never reduce the count below `scaling.min` (unless `min: 0` and the service is idle, per FR-052).
  3. Scale-up operations never exceed `scaling.max` regardless of demand; excess demand is queued or rejected per platform capacity policy.
- **Alternative Flow:** None.
- **Exception Flow:** A configuration change lowers `scaling.max` below the currently running instance count → platform gracefully scales down to the new max rather than leaving it inconsistent.
- **Business Rules:** `scaling.max` acts as a hard ceiling enforced independently of demand spikes, protecting shared platform capacity and department quotas (Module M).
- **Input:** Current instance count, `scaling.min`/`scaling.max`.
- **Output:** Instance count kept within bounds.
- **Acceptance Criteria:** Under sustained load, a service's instance count never exceeds `scaling.max`; under idle conditions, it never falls below `scaling.min` (or reaches exactly zero only when `min: 0`).
- **Priority:** MUST

#### FR-055 — Scale-to-Zero Opt-Out / Override

- **Description:** The platform must allow an Application Owner to opt an eligible service out of scale-to-zero (i.e., set `scaling.min ≥ 1`) where cold-start latency is unacceptable, subject to the resulting resource cost being within department quota.
- **Actor(s):** Application Owner, Employee
- **Trigger:** Owner edits `scaling.min` on an eligible service to a value ≥ 1.
- **Preconditions:** Service is scale-to-zero-eligible (FR-051).
- **Main Flow:**
  1. Owner updates `scaling.min` in `deployment.yaml`.
  2. Configuration re-validates against resource quota (FR-032) with the new always-on minimum footprint.
  3. On next deployment, the service maintains at least `scaling.min` running instances continuously, never scaling to zero.
- **Alternative Flow:** None.
- **Exception Flow:** New minimum footprint would exceed department quota → rejected at validation.
- **Business Rules:** Opting out increases baseline resource consumption and is therefore subject to the same quota checks as any other resource request (Module M); this is a per-service, per-application decision, not a platform-wide switch.
- **Input:** Updated `scaling.min`.
- **Output:** Service running continuously at or above the new minimum.
- **Acceptance Criteria:** A service with `scaling.min ≥ 1` is confirmed never to reach zero running instances during idle periods.
- **Priority:** SHOULD

#### FR-056 — Scale Event Logging

- **Description:** The platform must log every scale-up and scale-down event (including scale-to-zero and cold-start) per service, with timestamps, for observability, cost analysis, and troubleshooting cold-start complaints.
- **Actor(s):** AI Coding Agent / Claude Code (indirectly), Platform Administrator
- **Trigger:** Any instance count change on a service.
- **Preconditions:** Service is Running.
- **Main Flow:**
  1. Deployment Engine emits a scale event (direction, instance count before/after, trigger reason) whenever the instance count changes.
  2. Event is recorded in the platform's logging/monitoring pipeline (Modules S, T).
  3. Events are queryable per application/service for troubleshooting and reporting (Module AB).
- **Alternative Flow:** None.
- **Exception Flow:** Event emission fails → gap is itself flagged for monitoring rather than silently lost (best-effort reconciliation from instance state where possible).
- **Business Rules:** Scale events are retained per the platform's log retention policy (Module S).
- **Input:** Instance count change.
- **Output:** Recorded scale event.
- **Acceptance Criteria:** Every scale-to-zero and cold-start event for a service is retrievable with an accurate timestamp and trigger reason.
- **Priority:** SHOULD

---

## Module M — Resource Management

#### FR-057 — Resource Tier Catalog Maintenance

- **Description:** The platform must maintain a catalog of resource tiers (e.g., small/medium/large), each mapping to concrete CPU/memory/storage allocations, that applications reference abstractly via `resources.tier` (Module G) rather than requesting raw values.
- **Actor(s):** Platform Administrator, IT Administrator
- **Trigger:** Initial platform setup, or a tier definition update.
- **Preconditions:** Requesting administrator holds resource-tier management privileges.
- **Main Flow:**
  1. Administrator defines/updates a tier's name and concrete resource mapping (owned in detail by the Non-Functional/Resource Requirements doc).
  2. Platform publishes the updated catalog for use in configuration (FR-027) and validation (FR-032).
  3. Existing applications on an updated tier definition receive the new mapping on their next deployment, not retroactively mid-flight.
- **Alternative Flow:** None.
- **Exception Flow:** Administrator attempts to remove a tier currently in use by a Running application → rejected until all dependent applications migrate.
- **Business Rules:** Concrete CPU/memory/storage numbers per tier are owned by the Non-Functional/Resource Requirements doc; this requirement governs only the catalog mechanism.
- **Input:** Tier definition.
- **Output:** Updated resource tier catalog.
- **Acceptance Criteria:** The current tier catalog is queryable and consistently applied across configuration and validation.
- **Priority:** MUST

#### FR-058 — Resource Allocation per Deployment

- **Description:** The platform must allocate the concrete compute resources corresponding to an application's declared tier at deployment time, and release them when the application scales down, is suspended, or is deleted.
- **Actor(s):** AI Coding Agent / Claude Code (indirectly)
- **Trigger:** Deployment execution (Module J), scale event (Module L), suspend/delete (Module K).
- **Preconditions:** Application has a valid `resources.tier` (FR-027).
- **Main Flow:**
  1. Deployment Engine resolves `resources.tier` to concrete limits from the tier catalog (FR-057).
  2. Container Platform allocates those limits to each running instance.
  3. On scale-down/suspend/delete, allocated resources are released back to the shared pool.
- **Alternative Flow:** None.
- **Exception Flow:** Insufficient platform capacity to satisfy the allocation → deployment fails with a capacity error rather than degrading silently below the declared tier.
- **Business Rules:** An instance is never run with resources below its declared tier's allocation; over-allocation beyond the tier is never granted without a tier/config change.
- **Input:** Resolved tier limits.
- **Output:** Allocated (or released) compute resources.
- **Acceptance Criteria:** A running instance's actual resource allocation matches its declared tier; released resources are confirmed available to the shared pool after scale-down/delete.
- **Priority:** MUST

#### FR-059 — Resource Quota Enforcement per Application / Department

- **Description:** The platform must continuously enforce that an application's and its department's aggregate resource consumption stays within their assigned quotas (Module C), rejecting or throttling requests that would exceed them.
- **Actor(s):** Platform Administrator, AI Coding Agent / Claude Code
- **Trigger:** Any action that would change resource consumption (deploy, scale-up, tier change).
- **Preconditions:** Department/application quota is assigned (FR-010).
- **Main Flow:**
  1. Requested action's resulting resource footprint is computed.
  2. Platform checks the footprint against remaining application and department quota.
  3. Within-quota actions proceed; over-quota actions are rejected with the specific quota named.
- **Alternative Flow:** Platform Administrator grants a temporary quota exception for a specific application, logged and time-boxed.
- **Exception Flow:** Quota check service itself is unavailable → action fails closed rather than proceeding unchecked.
- **Business Rules:** This is enforced at both validation time (FR-032, pre-deploy) and continuously at runtime (e.g., scale-up attempts that would breach quota mid-operation are also blocked).
- **Input:** Requested resource footprint, current quota state.
- **Output:** Allow/block determination.
- **Acceptance Criteria:** A scale-up that would exceed department quota is blocked at the moment it would occur, not only at initial deploy validation.
- **Priority:** MUST

#### FR-060 — Resource Usage Visibility

- **Description:** The platform must expose current and historical resource consumption per application and department to authorized viewers, supporting cost awareness and quota planning.
- **Actor(s):** Application Owner, Platform Administrator, Management / Auditor
- **Trigger:** Viewer queries resource usage (console, or feeds Module AB reporting).
- **Preconditions:** Application/department has deployment history.
- **Main Flow:**
  1. Platform aggregates resource allocation/usage data per application and rolls it up per department.
  2. Viewer queries current usage and remaining quota.
  3. Historical usage is available for trend analysis (Module AB).
- **Alternative Flow:** None.
- **Exception Flow:** Viewer lacks authorization for the requested scope (e.g., another department's detail) → rejected/limited to authorized scope.
- **Business Rules:** Application Owners see their own applications; Platform Administrators and Auditors see cross-department aggregate and detail per their role.
- **Input:** Query scope (application/department/time range).
- **Output:** Resource usage report.
- **Acceptance Criteria:** An Application Owner can retrieve their application's current resource consumption against its tier and quota at any time.
- **Priority:** SHOULD

---

## Module N — Database Management

#### FR-061 — Managed Database Provisioning

- **Description:** The platform must automatically provision a managed PostgreSQL database instance for an application that declares a `database` section in `deployment.yaml`, without the employee or agent ever running raw database administration commands against shared infrastructure.
- **Actor(s):** AI Coding Agent / Claude Code, Employee
- **Trigger:** First deployment of an application declaring `database.type: postgres`.
- **Preconditions:** Application has passed validation (Module H); database is within the Supported Stack v1 baseline (Module F).
- **Main Flow:**
  1. Deployment Engine reads the `database` declaration.
  2. Deployment Engine provisions a dedicated managed PostgreSQL instance/schema scoped to this application.
  3. Connection credentials are generated and delivered via Secret Management (Module O), never exposed in source or plaintext config.
  4. Application's backend service(s) receive the connection details as injected runtime secrets.
- **Alternative Flow:** Application declares Redis as a `cache` rather than (or in addition to) a database — provisioned analogously as a managed cache instance.
- **Exception Flow:** Declared database type is not in the supported stack catalog → validation failure (FR-021), provisioning never attempted.
- **Business Rules:** Every application's database is logically isolated and dedicated to that application (FR-062); databases are never directly reachable by the employee/agent for raw admin commands — only through the application's own runtime connection or governed platform tooling.
- **Input:** `database` declaration.
- **Output:** Provisioned, isolated database instance; injected connection credentials.
- **Acceptance Criteria:** A newly deployed application declaring a database has a working, isolated database reachable only by its own services, with credentials delivered via Secret Management rather than manual configuration.
- **Priority:** MUST

#### FR-062 — Database Isolation Enforcement

- **Description:** The platform must guarantee that one application's database is never reachable or discoverable by another application, preventing any cross-application data access.
- **Actor(s):** Security Administrator, AI Coding Agent / Claude Code
- **Trigger:** Database provisioning (FR-061); ongoing runtime network policy enforcement.
- **Preconditions:** Multiple applications with independently provisioned databases exist on the platform.
- **Main Flow:**
  1. Each application's database is provisioned within a network/access boundary scoped only to that application's own services (Module Q).
  2. Network policy denies any connection attempt from another application's services to this database.
  3. Isolation is verified as part of the security pre-check (FR-031) and periodic security review.
- **Alternative Flow:** None — there is no supported mechanism for cross-application database sharing in this release.
- **Exception Flow:** A configuration or code attempt to connect cross-application is detected → connection denied at the network layer and logged as a potential policy violation (Module W/X).
- **Business Rules:** This isolation is enforced independently of application code correctness — even a misconfigured or malicious application cannot reach another's database, consistent with never trusting the agent/application as the security boundary.
- **Input:** Cross-application connection attempt (as a negative test case).
- **Output:** Denied connection; isolation upheld.
- **Acceptance Criteria:** A connection attempt from Application X's runtime to Application Y's database is denied at the network layer, regardless of credentials used.
- **Priority:** MUST

#### FR-063 — Database Connection Credential Issuance

- **Description:** The platform must generate and deliver database connection credentials to an application's runtime exclusively through the Secret Management subsystem (Module O), never requiring or permitting the employee/agent to author or store them in source code.
- **Actor(s):** AI Coding Agent / Claude Code (indirectly, as consumer)
- **Trigger:** Database provisioning (FR-061) or credential rotation (FR-068).
- **Preconditions:** Database instance is provisioned.
- **Main Flow:**
  1. Platform generates a unique credential for the application's database access.
  2. Credential is stored in the Secret Management subsystem, scoped to this application only.
  3. Credential is injected into the application's runtime environment at deploy/start time (FR-067).
- **Alternative Flow:** Credential rotation (FR-068) reissues a new credential and invalidates the old one, re-injecting without requiring a code change.
- **Exception Flow:** Credential injection fails → deployment/start is blocked rather than starting the application without valid database access.
- **Business Rules:** Database credentials never appear in `deployment.yaml`, source control, or build logs.
- **Input:** Provisioned database instance.
- **Output:** Injected runtime credential.
- **Acceptance Criteria:** No database credential is discoverable in the application's source repository, `deployment.yaml`, or build artifacts at any point.
- **Priority:** MUST

#### FR-064 — Database Backup Scheduling

- **Description:** The platform must automatically schedule and execute backups of each application's managed database according to a platform-defined policy, without requiring the employee/agent to configure or trigger backups manually.
- **Actor(s):** Platform Administrator, AI Coding Agent / Claude Code (indirectly, as beneficiary)
- **Trigger:** Database provisioning (initial backup schedule setup); recurring schedule thereafter.
- **Preconditions:** Database instance is provisioned and Running.
- **Main Flow:**
  1. Platform applies the default backup policy (frequency, retention — exact values **TBD, per resource tier defined in the Non-Functional/Resource Requirements doc**) at provisioning time.
  2. Backups execute automatically on schedule without employee/agent action.
  3. Backup completion/failure is logged and monitored (Modules S, T).
- **Alternative Flow:** Higher resource tiers or production-classified databases may receive a more frequent backup schedule than dev/lower tiers.
- **Exception Flow:** A scheduled backup fails → alert raised to Platform Administrator (Module X); retried per policy.
- **Business Rules:** Backup frequency/retention is tier- and environment-dependent, exact values **TBD — see Decision Log**; this requirement defines the automated mechanism only.
- **Input:** Backup policy, database instance.
- **Output:** Recorded backup artifacts.
- **Acceptance Criteria:** A provisioned database has at least one successful backup recorded within its policy's defined window at all times during Running state.
- **Priority:** SHOULD

#### FR-065 — Database Deprovisioning on Application Deletion

- **Description:** The platform must deprovision (per policy: purge or retain-then-purge) an application's managed database when the application reaches the Deleted lifecycle state, ensuring no orphaned data persists indefinitely outside policy.
- **Actor(s):** Application Owner, Platform Administrator
- **Trigger:** Application deletion (FR-050).
- **Preconditions:** Application is transitioning to Deleted.
- **Main Flow:**
  1. Deletion workflow triggers database deprovisioning.
  2. Platform takes a final backup (per retention policy) before removing the live instance, where policy requires retention.
  3. Live database instance and its network/access scope are removed.
  4. Deprovisioning outcome is recorded in the audit log (Module W).
- **Alternative Flow:** A grace-period retention holds the final backup for a defined window (exact period **TBD — see Decision Log**) to support accidental-deletion recovery, before permanent purge.
- **Exception Flow:** Deprovisioning fails (e.g., engine error) → deletion is not marked fully complete until confirmed, and Platform Administrator is alerted.
- **Business Rules:** No live database instance exists for a Deleted application; final-backup retention (if any) follows the platform's data retention policy, distinct from the live database's own backup policy (FR-064).
- **Input:** Application deletion event.
- **Output:** Deprovisioned database; optional retained final backup.
- **Acceptance Criteria:** After an application reaches Deleted, no live, reachable database instance remains for it.
- **Priority:** MUST

---

## Module O — Secret Management

#### FR-066 — Centralized Secret Storage

- **Description:** The platform must store all application secrets (database credentials, API keys, tokens) in a centralized, encrypted secret store, never accepting or persisting secrets in plaintext within source code, `deployment.yaml`, or build artifacts.
- **Actor(s):** Employee, AI Coding Agent / Claude Code, Security Administrator
- **Trigger:** Secret creation (e.g., database provisioning FR-061, or an employee/agent registers an application-specific secret such as a third-party API key).
- **Preconditions:** Application is registered.
- **Main Flow:**
  1. Employee/Agent submits a secret value (or the platform generates one) through a dedicated secret-registration path — never as a `deployment.yaml` field.
  2. Platform encrypts and stores the secret in the centralized secret store, scoped to the owning application.
  3. Secret is referenced elsewhere (e.g., in `deployment.yaml`) only by name/reference, never by value.
- **Alternative Flow:** None.
- **Exception Flow:** A build/validation scan detects what appears to be a plaintext credential committed in source or configuration → flagged as a security violation (FR-031) and blocked/reported rather than silently deployed.
- **Business Rules:** No plaintext production credential is ever permitted in source control or `deployment.yaml`; secret values are write-only from the employee/agent's perspective after initial submission (never re-displayed in plaintext, see FR-070).
- **Input:** Secret value (on creation).
- **Output:** Stored, encrypted secret with a reference name.
- **Acceptance Criteria:** A `deployment.yaml` or source scan finds zero plaintext secrets for any application passing validation.
- **Priority:** MUST

#### FR-067 — Secret Injection into Runtime

- **Description:** The platform must inject secrets into an application's running instances at start time (e.g., as environment variables or mounted runtime values) without ever writing them to the built container image or persistent application storage outside the running process.
- **Actor(s):** AI Coding Agent / Claude Code (indirectly, as consumer)
- **Trigger:** Application instance start (deployment, restart, scale-up, Module J/L).
- **Preconditions:** Required secrets exist in the secret store for this application (FR-066).
- **Main Flow:**
  1. Deployment Engine resolves the set of secrets referenced by the application's configuration.
  2. Secrets are injected into the instance's runtime at start time.
  3. Instance uses the injected values to connect to its database/external services.
- **Alternative Flow:** None.
- **Exception Flow:** A referenced secret is missing/inaccessible → instance start fails closed rather than starting without required credentials.
- **Business Rules:** Secrets are never baked into the built container image (FR-035–37); they exist only in the running process's runtime environment.
- **Input:** Secret references from configuration.
- **Output:** Running instance with injected secret values.
- **Acceptance Criteria:** Inspecting a built container image (at rest, not running) reveals no secret values; a running instance has successfully authenticated using injected secrets.
- **Priority:** MUST

#### FR-068 — Secret Rotation

- **Description:** The platform must support rotating a secret (issuing a new value and invalidating the old one) without requiring an application code change, and re-inject the rotated value into running instances.
- **Actor(s):** Application Owner, Security Administrator, Platform Administrator
- **Trigger:** Scheduled rotation policy, manual rotation request, or suspected credential compromise.
- **Preconditions:** Secret exists in the store (FR-066).
- **Main Flow:**
  1. Rotation is triggered (scheduled or manual).
  2. Platform generates/accepts a new secret value and updates the store.
  3. Running instances are re-injected with the new value (e.g., via a rolling restart) while the old value is invalidated per the overlap policy.
  4. Rotation event is recorded in the audit log (Module W).
- **Alternative Flow:** Security Administrator forces immediate rotation and revocation (no overlap window) in a suspected-compromise scenario.
- **Exception Flow:** Re-injection fails on some instances → those instances are flagged unhealthy/restarted rather than left silently running on a revoked credential.
- **Business Rules:** Rotation frequency for routine (non-incident) rotation follows platform security policy (exact interval **TBD — see Decision Log**); database credential rotation (FR-063) uses this same mechanism.
- **Input:** Rotation trigger.
- **Output:** New active secret value; old value invalidated.
- **Acceptance Criteria:** After rotation completes, the old secret value no longer authenticates successfully, and all running instances operate on the new value.
- **Priority:** MUST

#### FR-069 — Secret Access Isolation per Application

- **Description:** The platform must guarantee that an application (and the employees/agents working on it) can only access secrets scoped to that specific application, never another application's secrets, mirroring database isolation (FR-062).
- **Actor(s):** Security Administrator, AI Coding Agent / Claude Code
- **Trigger:** Any secret read/injection request.
- **Preconditions:** Requesting application/identity is authenticated.
- **Main Flow:**
  1. Secret access request is scoped to a specific application.
  2. Platform verifies the requesting identity (service or human) is authorized for that specific application's secrets.
  3. Access is granted only within that scope; cross-application requests are denied.
- **Alternative Flow:** None.
- **Exception Flow:** A request attempts to read another application's secret (whether via misconfiguration, bug, or malicious attempt) → denied and logged as a potential policy violation.
- **Business Rules:** Secret isolation is enforced by the Platform API/secret store itself, independent of any application-level code correctness — consistent with never trusting the application or agent as the security boundary.
- **Input:** Secret access request, requesting application identity.
- **Output:** Allow/deny determination.
- **Acceptance Criteria:** A secret access request scoped to Application X but naming a secret owned by Application Y is denied.
- **Priority:** MUST

#### FR-070 — Secret Access Audit

- **Description:** The platform must log every access to a secret's value (creation, rotation, injection, any read), so that secret use is fully attributable and reviewable, and must never display a stored secret's plaintext value back to a human requester after initial submission.
- **Actor(s):** Security Administrator, Management / Auditor
- **Trigger:** Any secret store operation.
- **Preconditions:** Secret exists.
- **Main Flow:**
  1. Secret store operation occurs (create, rotate, inject, delete).
  2. Platform records the operation, actor/service identity, application scope, and timestamp — never the plaintext value itself — in the audit log (Module W).
  3. Security Administrator/Auditor can later query this trail.
- **Alternative Flow:** None.
- **Exception Flow:** A human attempts to retrieve a stored secret's plaintext value directly (e.g., via console) → denied; only re-injection into a running instance or rotation is permitted, never plaintext display.
- **Business Rules:** Plaintext secret values are never included in any audit record, log line, or UI display after initial submission.
- **Input:** Secret operation event.
- **Output:** Audit record (metadata only, no plaintext value).
- **Acceptance Criteria:** Querying the audit trail for a secret returns a complete history of operations without ever exposing the secret's actual value.
- **Priority:** MUST

#### FR-071 — Production Secret Restricted Access and Approval

- **Description:** The platform must apply stricter access and approval controls to secrets scoped to production applications than to dev/non-production applications, consistent with production deployments requiring explicit approval (Module J).
- **Actor(s):** Application Owner, Security Administrator, Platform Administrator
- **Trigger:** Creation, rotation, or access-grant request for a production-scoped secret.
- **Preconditions:** Target application/environment is classified as production.
- **Main Flow:**
  1. Requester submits a production secret operation (create/rotate/grant access).
  2. Platform routes the request through the production approval control (mirroring FR-042) before it takes effect.
  3. Approved operations proceed and are recorded (FR-070); rejected ones are not applied.
- **Alternative Flow:** Dev-scoped secret operations do not require this additional approval, consistent with dev's auto-deploy posture.
- **Exception Flow:** Approval is not granted within the policy window → request expires, no change applied.
- **Business Rules:** Exactly who must approve production secret operations is configurable per department/security policy; regardless of configuration, no production secret operation bypasses this gate.
- **Input:** Production secret operation request.
- **Output:** Approved/rejected secret operation.
- **Acceptance Criteria:** A production secret rotation/creation request cannot take effect without a recorded approval; the equivalent dev request does not require one.
- **Priority:** MUST

---

## Module P — Domain Management

#### FR-072 — Domain / Subdomain Assignment

- **Description:** The platform must automatically derive and assign a domain or subdomain to an application based on a deterministic platform naming convention, without the employee/agent manually configuring DNS.
- **Actor(s):** AI Coding Agent / Claude Code (indirectly, as consumer)
- **Trigger:** First deployment reaching Traffic Activation (Module J).
- **Preconditions:** Application name is validated and unique (FR-012, FR-033).
- **Main Flow:**
  1. Platform derives the domain/subdomain from the application name and visibility setting (`domain.visibility`, Module G) per the platform's naming convention.
  2. Platform registers the DNS record automatically.
  3. Domain is bound to the application's ingress routing (Module Q) once traffic activation succeeds.
- **Alternative Flow:** None — custom/vanity domains are out of scope for this release (COULD/future consideration, not committed here).
- **Exception Flow:** Derived domain collides with an existing one → caught earlier at validation (FR-033); assignment should never reach a live collision in normal operation.
- **Business Rules:** DNS is fully platform-managed; the employee/agent has no direct DNS configuration capability at any point.
- **Input:** Application name, visibility.
- **Output:** Assigned, resolvable domain/subdomain.
- **Acceptance Criteria:** A newly Running application is reachable at its automatically assigned domain without any manual DNS step by the employee.
- **Priority:** MUST

#### FR-073 — Automated TLS Certificate Provisioning

- **Description:** The platform must automatically provision and renew a TLS certificate for every application domain, ensuring all traffic is served over HTTPS without the employee/agent manually configuring TLS/certificates.
- **Actor(s):** AI Coding Agent / Claude Code (indirectly, as consumer), IT Administrator
- **Trigger:** Domain assignment (FR-072); certificate approaching expiry.
- **Preconditions:** Domain has been assigned.
- **Main Flow:**
  1. Platform requests/issues a TLS certificate for the assigned domain through its managed certificate authority integration.
  2. Certificate is bound to the application's ingress (Module Q).
  3. Platform monitors certificate expiry and auto-renews ahead of expiration.
- **Alternative Flow:** None.
- **Exception Flow:** Certificate issuance/renewal fails → alert raised to IT Administrator (Module X) well ahead of any expiry-driven outage; traffic is never silently downgraded to plaintext HTTP as a fallback.
- **Business Rules:** No application is ever exposed without valid TLS; the employee/agent never handles certificate files or private keys directly.
- **Input:** Assigned domain.
- **Output:** Valid, auto-renewing TLS certificate bound to the domain.
- **Acceptance Criteria:** Every Running application's domain serves valid HTTPS at all times, with certificate renewal occurring automatically before expiry.
- **Priority:** MUST

#### FR-074 — External Domain Exposure Approval Gate

- **Description:** The platform must require explicit approval before an application's domain is exposed externally (outside the corporate network), consistent with `domain.visibility: external` being a higher-risk configuration than the `internal` default.
- **Actor(s):** Application Owner, Security Administrator, Platform Administrator
- **Trigger:** An application configured with `domain.visibility: external` reaches its first deployment (or an existing internal application is reconfigured to external).
- **Preconditions:** Configuration has passed all other validation (Module H), including confirming the service is policy-eligible for external exposure (FR-028).
- **Main Flow:**
  1. Deployment pipeline detects `external` visibility and pauses at an approval checkpoint distinct from (and in addition to) the production approval gate (FR-042).
  2. Security Administrator (and/or Platform Administrator, per policy) reviews and approves or rejects external exposure.
  3. On approval, domain/TLS provisioning (FR-072–073) proceeds with external reachability enabled.
- **Alternative Flow:** Internal-only applications skip this gate entirely.
- **Exception Flow:** Rejected → application remains internal-only; requester notified with the rejection reason.
- **Business Rules:** This gate applies regardless of environment (dev or production) — external exposure is always reviewed, since it changes the platform's external attack surface.
- **Input:** External exposure request.
- **Output:** Approved/rejected external exposure decision.
- **Acceptance Criteria:** No application domain is reachable from outside the corporate network without a recorded external-exposure approval.
- **Priority:** MUST

#### FR-075 — Domain Decommissioning

- **Description:** The platform must release/deregister an application's domain and revoke its TLS certificate when the application is Archived or Deleted (Module K), preventing dangling DNS records or stale certificates.
- **Actor(s):** AI Coding Agent / Claude Code (indirectly), Platform Administrator
- **Trigger:** Application transitions to Archived or Deleted.
- **Preconditions:** Application currently has an assigned domain.
- **Main Flow:**
  1. Lifecycle transition (FR-049 or FR-050) triggers domain decommissioning.
  2. Platform removes the DNS record and revokes/allows expiry of the associated TLS certificate.
  3. The domain becomes available for potential future reassignment per platform policy.
- **Alternative Flow:** Suspend (FR-047) does not decommission the domain — only Archive/Delete do, since Suspend is intended to be reversible quickly.
- **Exception Flow:** Decommissioning fails (e.g., DNS provider error) → retried and escalated to Platform Administrator if not resolved within policy window, since a dangling record is itself a minor security exposure.
- **Business Rules:** A Deleted or Archived application is never left with a live, resolvable domain.
- **Input:** Lifecycle transition event.
- **Output:** Decommissioned domain and certificate.
- **Acceptance Criteria:** An Archived or Deleted application's former domain no longer resolves to any running service.
- **Priority:** MUST

---

## Module Q — Networking

#### FR-076 — Network Isolation Between Applications

- **Description:** The platform must isolate each application's network namespace/segment from every other application's, so that no application's services can reach another application's services, database, or secrets over the network by default.
- **Actor(s):** Security Administrator, AI Coding Agent / Claude Code
- **Trigger:** Application deployment (Module J).
- **Preconditions:** Application is being deployed to the Container Platform.
- **Main Flow:**
  1. Deployment Engine provisions each application within its own isolated network boundary on the Container Platform.
  2. Default-deny network policy is applied between application boundaries.
  3. Only explicitly permitted paths (e.g., an application's own frontend-to-API traffic, FR-080) are allowed.
- **Alternative Flow:** None — there is no supported mechanism for two applications to share a network namespace in this release.
- **Exception Flow:** An attempted cross-application connection is detected → denied at the network layer and logged (mirrors FR-062, FR-069).
- **Business Rules:** Default-deny is the platform-wide baseline; nothing is reachable across application boundaries unless explicitly modeled by the platform (e.g., a documented shared-service pattern, out of scope for v1).
- **Input:** Application deployment.
- **Output:** Isolated network boundary per application.
- **Acceptance Criteria:** A network path from one application's service to another's is confirmed blocked by default in a security test.
- **Priority:** MUST

#### FR-077 — Managed Ingress Routing Configuration

- **Description:** The platform must automatically configure ingress routing (load balancing, path/host routing, TLS termination) for each application based on its `deployment.yaml`, without the employee/agent ever writing or editing raw nginx/ingress-controller configuration.
- **Actor(s):** AI Coding Agent / Claude Code (indirectly, as consumer)
- **Trigger:** Deployment reaching Traffic Activation (Module J).
- **Preconditions:** Application's services and domain are defined (Modules G, P).
- **Main Flow:**
  1. Deployment Engine derives the required ingress rules from the application's service definitions and domain.
  2. Ingress is configured automatically on the Container Platform's managed ingress layer.
  3. Traffic to the assigned domain is routed to the correct service/instance.
- **Alternative Flow:** Multi-service applications (e.g., `frontend` + `api`) receive path- or host-based routing rules automatically derived from the service definitions, without manual specification.
- **Exception Flow:** Ingress configuration fails → deployment halts at Traffic Activation (Module J) rather than serving with broken routing.
- **Business Rules:** There is no employee/agent-facing capability to directly edit ingress-controller configuration, nginx config files, or equivalent — this is entirely platform-managed.
- **Input:** Service definitions, domain.
- **Output:** Configured ingress routing.
- **Acceptance Criteria:** Requests to an application's domain are correctly routed to the intended service without any manual ingress configuration step.
- **Priority:** MUST

#### FR-078 — Internal-Only Service Enforcement

- **Description:** The platform must prevent any database, cache, or other internal-only-by-policy service from being directly reachable from outside the application's own service boundary, regardless of the application's overall `domain.visibility` setting.
- **Actor(s):** Security Administrator, AI Coding Agent / Claude Code
- **Trigger:** Deployment of any service classified as internal-only by platform policy (e.g., database, cache).
- **Preconditions:** Application declares a database/cache (Module N, F).
- **Main Flow:**
  1. Platform classifies database/cache services as internal-only regardless of the application's declared visibility.
  2. Ingress routing (FR-077) is never configured for these service types.
  3. Only the application's own backend services can reach them, per network isolation (FR-076, FR-062).
- **Alternative Flow:** None.
- **Exception Flow:** A configuration attempts to expose a database/cache externally → rejected at validation (FR-028) well before this enforcement point is even reached; this requirement is the runtime backstop for that policy.
- **Business Rules:** This is a hard platform invariant, not a configurable option — no `deployment.yaml` field can expose a database or cache externally.
- **Input:** Service classification.
- **Output:** No external route to internal-only services.
- **Acceptance Criteria:** An external network scan of the platform finds zero directly reachable database/cache endpoints for any application.
- **Priority:** MUST

#### FR-079 — Egress Policy Enforcement

- **Description:** The platform must control and, by default, restrict an application's outbound (egress) network access to prevent data exfiltration or unauthorized external communication, while allowing legitimate outbound calls (e.g., to approved third-party APIs) where declared.
- **Actor(s):** Security Administrator, Application Owner
- **Trigger:** Application deployment; egress policy review.
- **Preconditions:** Application is deployed.
- **Main Flow:**
  1. Platform applies a default egress policy (e.g., restricted to approved destinations or a documented default-allow with monitoring, per platform security baseline).
  2. Application Owner may request additional egress destinations where the application's function requires them.
  3. Security Administrator reviews/approves non-default egress requests.
- **Alternative Flow:** None.
- **Exception Flow:** Unapproved/anomalous egress traffic is detected → alerted to Security Administrator (Module X) as a potential exfiltration or compromise signal.
- **Business Rules:** The exact default egress posture (allow-with-monitoring vs. default-deny-with-allowlist) is **TBD — see Decision Log**; this requirement commits to the enforcement mechanism and review process regardless of the chosen default.
- **Input:** Egress request/policy.
- **Output:** Enforced egress policy per application.
- **Acceptance Criteria:** An application's outbound traffic to a non-approved destination (under a default-deny posture) is blocked and logged.
- **Priority:** SHOULD

#### FR-080 — Intra-Application Service-to-Service Communication

- **Description:** The platform must allow an application's own services (e.g., its `frontend` calling its `api`) to communicate with each other automatically, without manual network configuration by the employee/agent.
- **Actor(s):** AI Coding Agent / Claude Code (indirectly, as consumer)
- **Trigger:** Multi-service application deployment (Module G/J).
- **Preconditions:** Application declares more than one service.
- **Main Flow:**
  1. Deployment Engine provisions internal service discovery/networking within the application's own boundary.
  2. Services can address each other (e.g., by service name) without additional configuration.
  3. This intra-application path is exempt from the cross-application default-deny (FR-076) since it is within a single application's own boundary.
- **Alternative Flow:** None.
- **Exception Flow:** N/A.
- **Business Rules:** Intra-application communication is scoped strictly to services of the same application — it is not a mechanism for any broader network access.
- **Input:** Multi-service application deployment.
- **Output:** Working intra-application service connectivity.
- **Acceptance Criteria:** A deployed application's frontend service can successfully call its own backend API service by service name without manual network setup.
- **Priority:** MUST

#### FR-081 — Network Policy Violation Alerting

- **Description:** The platform must detect and alert on any attempted network policy violation (cross-application access attempt, unauthorized egress, attempted access to an internal-only service) in near real time.
- **Actor(s):** Security Administrator, Platform Administrator
- **Trigger:** A denied network event occurs (FR-076, FR-078, FR-079).
- **Preconditions:** Network policy enforcement is active.
- **Main Flow:**
  1. Network layer denies a policy-violating connection attempt.
  2. Event is captured with source/target application, timestamp, and policy rule violated.
  3. Alert is raised to Security Administrator (Module X); event is recorded in the audit log (Module W).
- **Alternative Flow:** Repeated violations from the same source within a short window are aggregated into a single elevated-severity alert rather than flooding notifications.
- **Exception Flow:** N/A.
- **Business Rules:** Every denied cross-boundary attempt is logged, even if it appears to be benign misconfiguration rather than malicious intent, since the pattern of attempts is itself valuable security signal.
- **Input:** Denied network event.
- **Output:** Security alert; audit record.
- **Acceptance Criteria:** A simulated cross-application access attempt results in both a denied connection and a retrievable alert/audit record within the platform's defined alerting latency.
- **Priority:** MUST

---

## Module R — Health Check

#### FR-082 — Automated Health Check Configuration

- **Description:** The platform must automatically configure a default health check (e.g., HTTP endpoint or process liveness probe appropriate to the declared runtime) for each service, without requiring the employee/agent to hand-author health check infrastructure, while allowing an application-specific health endpoint to be declared where the default does not fit.
- **Actor(s):** AI Coding Agent / Claude Code, Employee
- **Trigger:** Service definition (Module G) at validation/deploy time.
- **Preconditions:** Service runtime is declared.
- **Main Flow:**
  1. Platform applies a runtime-appropriate default health check (e.g., a standard readiness path convention) unless the application declares its own.
  2. Health check configuration is attached to the service's deployment definition.
  3. Configuration is used by subsequent deployment (FR-083) and continuous monitoring (FR-084).
- **Alternative Flow:** Employee/agent declares a custom health check path for the service where the platform default does not apply (e.g., a non-HTTP worker uses a process-liveness convention instead).
- **Exception Flow:** Declared custom health check path is malformed → validation failure (Module H) before deploy.
- **Business Rules:** Every deployable service has an active health check configuration — there is no supported "no health check" state for a Running service.
- **Input:** Service runtime, optional custom health check declaration.
- **Output:** Health check configuration per service.
- **Acceptance Criteria:** Every service in a Running application has a resolvable, active health check configuration.
- **Priority:** MUST

#### FR-083 — Health Check Gate During Deployment

- **Description:** The platform must execute the configured health check against a newly deployed instance before activating traffic to it, ensuring only healthy instances ever serve requests.
- **Actor(s):** AI Coding Agent / Claude Code (indirectly)
- **Trigger:** New instance becomes available during a deployment (Module J, "Health Check" step preceding "Traffic Activation").
- **Preconditions:** Instance has started and health check configuration exists (FR-082).
- **Main Flow:**
  1. Deployment Engine polls the instance's health check per the configured interval/timeout.
  2. Instance reports healthy within the timeout.
  3. Traffic Activation proceeds (Module J) only after a healthy report.
- **Alternative Flow:** Multiple instances in a scale-up are each individually health-checked before being added to the serving pool (rolling activation).
- **Exception Flow:** Instance fails to report healthy within the timeout → it is never added to the serving pool; the deployment is marked Failed and the failure/rollback branch is invoked (FR-044).
- **Business Rules:** No instance ever receives production traffic before passing its health check, without exception.
- **Input:** Instance health check responses.
- **Output:** Healthy/unhealthy determination gating traffic activation.
- **Acceptance Criteria:** An instance that never becomes healthy never receives traffic, and the deployment attempt is marked Failed.
- **Priority:** MUST

#### FR-084 — Continuous Health Monitoring Post-Deploy

- **Description:** The platform must continue polling each running instance's health check throughout its lifetime, not only at deployment time, to detect runtime degradation.
- **Actor(s):** AI Coding Agent / Claude Code (indirectly)
- **Trigger:** Instance is Running.
- **Preconditions:** Instance passed initial deployment health check (FR-083).
- **Main Flow:**
  1. Deployment Engine polls each running instance's health check at a regular interval.
  2. Healthy responses keep the instance in the active serving pool.
  3. Results feed into Monitoring (Module T) for trend/alerting purposes.
- **Alternative Flow:** None.
- **Exception Flow:** An instance begins failing health checks → it is removed from the serving pool and remediated (FR-085) rather than continuing to receive traffic.
- **Business Rules:** Continuous health checking applies to all Running instances, including those that are part of a scale-to-zero-eligible service while active (idle/zero instances have no health check to poll, per Module L).
- **Input:** Ongoing health check responses.
- **Output:** Current health status per instance.
- **Acceptance Criteria:** An instance that begins failing health checks is removed from the serving pool within the platform's defined detection window.
- **Priority:** MUST

#### FR-085 — Unhealthy Instance Remediation

- **Description:** The platform must automatically remediate an instance that fails continuous health checks — by restarting or replacing it — to restore healthy capacity without requiring manual intervention for routine failures.
- **Actor(s):** AI Coding Agent / Claude Code (indirectly), Application Owner
- **Trigger:** An instance is detected unhealthy (FR-084).
- **Preconditions:** Instance is part of a Running service.
- **Main Flow:**
  1. Deployment Engine removes the unhealthy instance from the serving pool.
  2. Deployment Engine restarts or replaces the instance.
  3. New/restarted instance is health-checked (mirrors FR-083) before rejoining the serving pool.
  4. Remediation event is logged (Module S) and, if remediation fails repeatedly, escalated (Module X) to the Application Owner.
- **Alternative Flow:** Repeated remediation failures on the same service within a short window trigger an elevated alert and may pause further automatic remediation attempts pending human review, to avoid a restart loop masking a systemic issue.
- **Exception Flow:** Remediation itself fails (replacement instance also unhealthy) → escalated as a service-down incident rather than looping indefinitely without visibility.
- **Business Rules:** Remediation must never reduce the service below `scaling.min` healthy instances without alerting (Module L, Module X).
- **Input:** Unhealthy instance event.
- **Output:** Restarted/replaced, healthy instance; or an escalated incident.
- **Acceptance Criteria:** A single unhealthy instance is automatically replaced and rejoins the serving pool healthy, without manual intervention, within the platform's defined remediation window.
- **Priority:** MUST

---

## Module S — Logging

#### FR-086 — Centralized Log Collection

- **Description:** The platform must automatically collect stdout/stderr and structured application logs from every running instance into a centralized logging pipeline, without requiring the employee/agent to configure log shipping.
- **Actor(s):** AI Coding Agent / Claude Code (indirectly, as consumer)
- **Trigger:** Instance start (any Running instance).
- **Preconditions:** Instance is deployed on the Container Platform.
- **Main Flow:**
  1. Container Platform captures each instance's stdout/stderr automatically.
  2. Logs are shipped to the centralized logging pipeline, tagged with application, service, instance, and timestamp metadata.
  3. Logs become queryable (FR-087) shortly after emission.
- **Alternative Flow:** None — there is no supported path for an employee/agent to configure custom log shipping infrastructure; the platform captures standard output uniformly.
- **Exception Flow:** Log shipping pipeline is temporarily unavailable → logs are buffered locally where possible and shipped once connectivity is restored, rather than silently dropped.
- **Business Rules:** Logs are always tagged with enough metadata to attribute every line to a specific application, service, and instance.
- **Input:** Instance stdout/stderr.
- **Output:** Centrally collected, tagged log stream.
- **Acceptance Criteria:** Output written by a running instance is retrievable from the centralized log store, correctly attributed to its application/service.
- **Priority:** MUST

#### FR-087 — Application Log Access

- **Description:** The platform must let an authorized Employee/Agent/Owner query an application's logs (current and recent historical) directly and via the MCP `get_application_logs` capability (Module Y), scoped strictly to applications they are authorized to view.
- **Actor(s):** Employee, AI Coding Agent / Claude Code, Application Owner
- **Trigger:** Log query request.
- **Preconditions:** Requester is authorized for the target application (ownership/role, Module E).
- **Main Flow:**
  1. Requester submits a log query (application, optional service/time range/filter).
  2. Platform authorizes the request against the requester's role and application ownership.
  3. Matching logs are returned, most recent first (or per requested ordering).
- **Alternative Flow:** Query is made through Claude Code via the MCP `get_application_logs` tool as part of debugging a failed deployment.
- **Exception Flow:** Requester is not authorized for the target application → rejected without leaking whether the application even exists (to avoid information disclosure).
- **Business Rules:** Log access is always scoped to applications the requester is authorized to view — there is no cross-application log query capability, mirroring database/secret isolation.
- **Input:** Application, filters (service, time range, text).
- **Output:** Matching log entries.
- **Acceptance Criteria:** An authorized owner can retrieve their application's recent logs; an unauthorized requester cannot retrieve any other application's logs.
- **Priority:** MUST

#### FR-088 — Log Retention Policy

- **Description:** The platform must retain application logs for a defined period appropriate to operational troubleshooting and compliance needs, after which they are purged per policy.
- **Actor(s):** Platform Administrator, IT Administrator
- **Trigger:** Ongoing log lifecycle management.
- **Preconditions:** Logs exist in the centralized store (FR-086).
- **Main Flow:**
  1. Platform applies the defined retention window per log category (application logs vs. audit-relevant logs, which may retain longer per Module W).
  2. Logs older than the retention window are purged automatically.
  3. Retention policy is consistently applied across all applications, potentially varying by resource/environment tier.
- **Alternative Flow:** Production application logs may retain longer than dev, per policy.
- **Exception Flow:** N/A.
- **Business Rules:** Exact retention periods are **TBD — see Decision Log**; this requirement commits to an enforced, policy-driven retention/purge mechanism regardless of final values. Log retention is distinct from, and shorter than or equal to, audit log retention (Module W).
- **Input:** Retention policy.
- **Output:** Purged logs beyond the retention window.
- **Acceptance Criteria:** Logs older than the configured retention window are confirmed no longer retrievable via the query interface (FR-087).
- **Priority:** SHOULD

#### FR-089 — Log Access Control

- **Description:** The platform must enforce that log access is available only to the Application Owner/contributors of that specific application and to Platform/Security Administrators and Auditors in their oversight capacity — never to unrelated employees.
- **Actor(s):** Security Administrator, Platform Administrator, Management / Auditor
- **Trigger:** Any log access attempt (mirrors FR-087, stated here as the explicit access-control requirement).
- **Preconditions:** Requester is authenticated.
- **Main Flow:**
  1. Log query request is evaluated against the requester's role and application relationship.
  2. Application Owner/contributors: access to their own application's logs.
  3. Platform/Security Administrator, Auditor: access per their oversight role, potentially cross-application, but itself logged (Module W) to prevent unchecked snooping.
- **Alternative Flow:** None.
- **Exception Flow:** An employee with no relationship to the application (not owner/contributor, not an administrator role) attempts access → denied.
- **Business Rules:** Even privileged administrative log access is itself audited (Module W) — no role has silent, unlogged access to application logs.
- **Input:** Log access request, requester role.
- **Output:** Allow/deny determination.
- **Acceptance Criteria:** An employee unrelated to an application cannot retrieve its logs; any administrator access to another team's application logs is itself recorded in the audit trail.
- **Priority:** MUST

---

## Module T — Monitoring

#### FR-090 — Application Metrics Collection

- **Description:** The platform must automatically collect runtime metrics (CPU, memory, request count/rate, error rate, latency) from every running instance without requiring employee/agent instrumentation configuration.
- **Actor(s):** AI Coding Agent / Claude Code (indirectly, as consumer)
- **Trigger:** Instance is Running.
- **Preconditions:** Instance is deployed on the Container Platform.
- **Main Flow:**
  1. Container Platform/Deployment Engine collects standard resource and traffic metrics from each running instance automatically.
  2. Metrics are aggregated per service and per application.
  3. Metrics feed both the query interface (FR-091) and alerting (FR-092).
- **Alternative Flow:** Applications may optionally expose additional custom metrics via a standard convention (e.g., a metrics endpoint), collected the same way as platform-default metrics.
- **Exception Flow:** Metrics collection temporarily fails for an instance → gap is recorded rather than silently interpolated, so dashboards/alerts do not present misleading data.
- **Business Rules:** Baseline metrics (CPU, memory, request rate, error rate, latency) are collected for every application uniformly, regardless of stack, since they derive from the Container Platform layer rather than application code.
- **Input:** Running instance runtime signals.
- **Output:** Collected, aggregated metrics.
- **Acceptance Criteria:** A Running application has queryable CPU, memory, and request/error rate metrics without any employee-authored instrumentation.
- **Priority:** MUST

#### FR-091 — Application Metrics Access

- **Description:** The platform must let an authorized Employee/Agent/Owner query an application's current and historical metrics directly and via the MCP `get_application_metrics` capability (Module Y).
- **Actor(s):** Employee, AI Coding Agent / Claude Code, Application Owner
- **Trigger:** Metrics query request.
- **Preconditions:** Requester is authorized for the target application.
- **Main Flow:**
  1. Requester submits a metrics query (application, metric type, time range).
  2. Platform authorizes the request per the application's ownership/role scope (mirrors FR-087).
  3. Matching metric data/time series is returned.
- **Alternative Flow:** Query made via Claude Code as part of diagnosing a performance issue or verifying a deployment's health after rollout.
- **Exception Flow:** Requester unauthorized for the target application → rejected.
- **Business Rules:** Metrics access follows the same authorization scoping as log access (FR-089).
- **Input:** Application, metric type, time range.
- **Output:** Metric time series/values.
- **Acceptance Criteria:** An authorized owner can retrieve their application's current CPU/memory/error-rate metrics on demand.
- **Priority:** MUST

#### FR-092 — Threshold-Based Alerting

- **Description:** The platform must alert relevant actors when an application's metrics or health cross defined thresholds (e.g., elevated error rate, resource saturation, repeated health check failures), enabling proactive response.
- **Actor(s):** Application Owner, Platform Administrator, Security Administrator
- **Trigger:** A monitored metric crosses its configured threshold.
- **Preconditions:** Application is Running and being monitored (FR-090).
- **Main Flow:**
  1. Monitoring pipeline continuously evaluates collected metrics against defined thresholds.
  2. A threshold breach generates an alert.
  3. Alert is routed to the Application Owner (and Platform/Security Administrator, where the threshold implies a platform- or security-level concern) via Module X.
- **Alternative Flow:** Some thresholds are platform-default (e.g., sustained high error rate); others may be configurable per application/tier where the platform supports it.
- **Exception Flow:** Alert delivery itself fails → retried per notification policy (Module X); persistent delivery failure is escalated.
- **Business Rules:** Exact threshold values are implementation/tier detail (owned by Non-Functional docs); this requirement commits to the alerting mechanism and routing, not the specific numbers.
- **Input:** Metric stream, configured thresholds.
- **Output:** Generated alert.
- **Acceptance Criteria:** A simulated threshold breach (e.g., sustained elevated error rate) results in an alert delivered to the Application Owner within the platform's defined alerting latency.
- **Priority:** MUST

#### FR-093 — Platform-Level Monitoring Dashboard

- **Description:** The platform must provide Platform Administrators with an aggregate, cross-application monitoring view (platform health, capacity, error trends) to operate the platform as a whole, distinct from any single application's owner-facing metrics view.
- **Actor(s):** Platform Administrator, Management / Auditor
- **Trigger:** Administrator/auditor accesses the platform monitoring view.
- **Preconditions:** Requester holds a platform-level administrative or oversight role.
- **Main Flow:**
  1. Platform aggregates metrics across all applications/departments.
  2. Administrator views platform-wide health, capacity utilization, and trend data.
  3. Drill-down to a specific application's detail is available, itself subject to the same audit logging as any administrative access (FR-089).
- **Alternative Flow:** Auditor accesses a read-only summary view appropriate to Management/Auditor oversight rather than the full operational dashboard.
- **Exception Flow:** A non-privileged employee attempts to access the platform-wide dashboard → denied.
- **Business Rules:** This is distinct from per-application reporting (Module AB) in that it is operational/real-time rather than periodic/summarized.
- **Input:** Platform-wide metrics.
- **Output:** Aggregate monitoring dashboard view.
- **Acceptance Criteria:** A Platform Administrator can view aggregate platform health and capacity trends across all applications from a single view.
- **Priority:** SHOULD

---

## Module U — Version Management

#### FR-094 — Version / Build Tagging per Deployment

- **Description:** The platform must assign a unique, traceable version identifier to every successful build/deployment of an application, linking it to the exact source revision, configuration, and build artifact used.
- **Actor(s):** AI Coding Agent / Claude Code (indirectly)
- **Trigger:** A build completes successfully (Module I) as part of a deployment (Module J).
- **Preconditions:** Build has succeeded.
- **Main Flow:**
  1. Platform generates a unique version identifier for the build (e.g., incrementing per application, or derived from source revision).
  2. Version identifier is linked to the source revision, `deployment.yaml` snapshot, and built image.
  3. Version record becomes part of the application's version history (FR-095).
- **Alternative Flow:** None.
- **Exception Flow:** N/A.
- **Business Rules:** Every Running application always has exactly one currently active version; version identifiers are immutable once assigned and never reused.
- **Input:** Successful build.
- **Output:** Unique version identifier linked to source, config, and image.
- **Acceptance Criteria:** Every successful deployment produces a distinct, queryable version identifier traceable back to its exact source revision and configuration.
- **Priority:** MUST

#### FR-095 — Version History Tracking

- **Description:** The platform must maintain a complete, ordered history of all versions ever deployed for an application, including which were currently active, superseded, or rolled back, to support rollback (Module V) and audit.
- **Actor(s):** Application Owner, AI Coding Agent / Claude Code
- **Trigger:** Any new version deployment (FR-094).
- **Preconditions:** Application has at least one prior deployed version, or this is its first.
- **Main Flow:**
  1. New version deployment is appended to the application's version history.
  2. Previously active version is marked superseded (not deleted).
  3. History is queryable in order, showing state transitions per version.
- **Alternative Flow:** A rollback (Module V) reactivates a prior version, which is recorded as a new history entry pointing back to that prior version's build artifact rather than rebuilding it.
- **Exception Flow:** N/A.
- **Business Rules:** Version history is never deleted for a Running or Archived application; it is only removed as part of full application Deletion (FR-050), subject to audit retention (Module W).
- **Input:** Version deployment/rollback events.
- **Output:** Ordered version history.
- **Acceptance Criteria:** An application's full deployment history, in order, is retrievable, including which version is currently active.
- **Priority:** MUST

#### FR-096 — Version Comparison Metadata

- **Description:** The platform must expose enough metadata per version (source revision, configuration diff summary, deploy timestamp, deployer identity) to let an Application Owner understand what changed between two versions before deciding to roll back or approve a promotion.
- **Actor(s):** Application Owner, Employee
- **Trigger:** Owner requests a comparison between two versions (e.g., before approving a production deploy, FR-042, or before a rollback decision, Module V).
- **Preconditions:** Both versions exist in history (FR-095).
- **Main Flow:**
  1. Owner selects two versions to compare.
  2. Platform surfaces each version's source revision, configuration snapshot, and deploy metadata.
  3. Owner reviews the comparison to inform their decision.
- **Alternative Flow:** Comparison metadata is surfaced automatically as part of the production approval gate (FR-042) rather than requiring a separate explicit request.
- **Exception Flow:** One of the requested versions no longer exists (purged per retention, FR-097) → comparison unavailable for that version, reported clearly rather than failing silently.
- **Business Rules:** This requirement covers metadata-level comparison only (what changed, at a summary level) — full source-code diffing is a developer-tooling concern outside the platform's scope.
- **Input:** Two version identifiers.
- **Output:** Comparison metadata.
- **Acceptance Criteria:** An owner can retrieve source revision, config snapshot, and deploy metadata for any two retained versions of their application.
- **Priority:** COULD

#### FR-097 — Version Retention Policy

- **Description:** The platform must retain a bounded number/period of prior versions and their build artifacts per application to support rollback, purging older versions per policy to manage storage.
- **Actor(s):** Platform Administrator
- **Trigger:** Ongoing version lifecycle management.
- **Preconditions:** Application has more versions than the retention policy allows.
- **Main Flow:**
  1. Platform applies the defined version retention policy (count and/or age-based).
  2. Versions beyond the retention window are purged (build artifact removed; history metadata may be retained longer for audit per Module W).
  3. The currently active version and, at minimum, the immediately prior version are always retained to guarantee rollback capability.
- **Alternative Flow:** Production applications may retain a longer version history than dev, per policy.
- **Exception Flow:** N/A.
- **Business Rules:** Exact retention count/period is **TBD — see Decision Log**; regardless of the final value, the immediately prior version is always retained so rollback (FR-098) is never unavailable due to retention purge alone.
- **Input:** Version retention policy.
- **Output:** Purged old versions; retained recent versions.
- **Acceptance Criteria:** At any point, the currently active version and at least the immediately prior version are retained and rollback-eligible.
- **Priority:** SHOULD

---

## Module V — Rollback

#### FR-098 — Manual Rollback Request

- **Description:** The platform must allow an Application Owner (or Claude Code on their behalf via MCP `rollback_application`, Module Y) to explicitly request rolling an application back to a previously deployed version.
- **Actor(s):** Application Owner, AI Coding Agent / Claude Code
- **Trigger:** Owner/agent submits a rollback request, typically after observing an issue with the current version.
- **Preconditions:** A prior version exists in the application's version history (FR-095) and is still retained (FR-097).
- **Main Flow:**
  1. Requester selects a target prior version to roll back to.
  2. Platform validates the target version is retained and rollback-eligible (FR-100).
  3. Rollback executes (FR-101), redeploying the target version's build artifact and configuration.
  4. Traffic is switched to the rolled-back version once it passes health checks (mirrors FR-083).
- **Alternative Flow:** Production rollback may require the same approval gate as a forward production deployment (FR-042), depending on platform policy severity classification — see Business Rules.
- **Exception Flow:** Target version is no longer retained → rejected with a clear "version no longer available" error, listing currently retained versions.
- **Business Rules:** Whether production rollback requires the same explicit approval as a forward deploy, or is expedited given its risk-reducing nature, is a policy decision — default posture in this baseline is that a manual rollback to a previously *approved* production version does **not** require re-approval, since it does not introduce new, unreviewed code (exact policy confirmable in Decision Log if this needs revisiting).
- **Input:** Rollback request, target version.
- **Output:** Application redeployed at the target version.
- **Acceptance Criteria:** A rollback request to a valid, retained prior version results in the application serving traffic from that version, confirmed by its version identifier.
- **Priority:** MUST

#### FR-099 — Automated Rollback on Failed Health Check

- **Description:** The platform must automatically trigger rollback to the last known-good version when a newly activated deployment begins failing health checks post-activation, minimizing downtime without requiring manual detection and response.
- **Actor(s):** AI Coding Agent / Claude Code (indirectly)
- **Trigger:** A newly activated version fails continuous health checks (FR-084) shortly after Traffic Activation.
- **Preconditions:** A previously Running, healthy version exists to roll back to.
- **Main Flow:**
  1. Continuous health monitoring detects the new version's instances failing health checks post-activation.
  2. Platform automatically initiates rollback to the last known-good version (mirrors FR-044).
  3. Traffic is switched back to the known-good version.
  4. Incident is logged and the Application Owner is notified (Module X) that an automatic rollback occurred and why.
- **Alternative Flow:** If no prior known-good version exists (e.g., this is the application's first deployment), automatic rollback is not possible — the application is instead marked Failed and the owner is alerted to fix and redeploy.
- **Exception Flow:** The automatic rollback itself fails to restore a healthy state → escalated immediately as a critical incident to the Application Owner and Platform Administrator.
- **Business Rules:** Automatic rollback triggers only after Traffic Activation has occurred and observed health has regressed — it does not apply to pre-activation build/scan/validation failures, which simply fail forward (FR-044).
- **Input:** Post-activation health check failure signal.
- **Output:** Application restored to the last known-good version; incident logged.
- **Acceptance Criteria:** A simulated post-activation health regression results in automatic reversion to the prior version with no manual action required, within the platform's defined recovery window.
- **Priority:** MUST

#### FR-100 — Rollback Target Version Validation

- **Description:** The platform must validate that a requested rollback target version is legitimate and safely deployable — retained, previously successfully deployed, and compatible with current infrastructure — before executing the rollback.
- **Actor(s):** AI Coding Agent / Claude Code (indirectly)
- **Trigger:** A rollback is requested (FR-098 or FR-099).
- **Preconditions:** A target version is specified.
- **Main Flow:**
  1. Platform confirms the target version exists in retained history (FR-097) and was itself a successfully completed deployment (never a Failed build).
  2. Platform confirms the target version's build artifact is still available (not purged).
  3. Validated target proceeds to rollback execution (FR-101).
- **Alternative Flow:** None.
- **Exception Flow:** Target version fails any check → rollback is rejected/blocked with the specific reason, rather than attempting a rollback to a broken or unavailable state.
- **Business Rules:** Rollback never targets a version that itself never successfully completed a deployment — only previously Running versions are valid rollback targets.
- **Input:** Target version identifier.
- **Output:** Valid/invalid rollback target determination.
- **Acceptance Criteria:** A rollback request targeting a version that never successfully deployed (e.g., a Failed build) is rejected before any rollback action is attempted.
- **Priority:** MUST

#### FR-101 — Rollback Execution and Status Tracking

- **Description:** The platform must execute a validated rollback as a tracked, observable operation — redeploying the target version's artifact, health-checking it, and switching traffic — with status queryable throughout, mirroring forward deployment tracking (FR-043).
- **Actor(s):** AI Coding Agent / Claude Code, Application Owner
- **Trigger:** Rollback target is validated (FR-100).
- **Preconditions:** Target version validated.
- **Main Flow:**
  1. Platform redeploys the target version's build artifact and configuration snapshot.
  2. New instances are health-checked (mirrors FR-083) before traffic is switched.
  3. Traffic switches to the rolled-back version; current version pointer updates.
  4. Rollback is marked Completed; status is queryable throughout via the same mechanism as FR-043.
- **Alternative Flow:** Rollback fails health checks on the target version too (rare — e.g., an environmental dependency changed) → rollback itself fails, and the incident is escalated (mirrors FR-099 exception flow) rather than looping.
- **Exception Flow:** Rollback execution fails mid-flight → application state is left in a clearly reported Failed or degraded status, never silently ambiguous.
- **Business Rules:** A completed rollback updates the application's lifecycle state per Module K (`Rolled Back`, then typically returning to `Running` once traffic is confirmed stable) and its active version pointer to the target version.
- **Input:** Validated rollback target.
- **Output:** Application running on the rolled-back version; rollback status.
- **Acceptance Criteria:** A completed rollback results in the application's active version matching the requested target, with a queryable Completed status.
- **Priority:** MUST

#### FR-102 — Rollback Notification

- **Description:** The platform must notify the Application Owner (and requester, if different) whenever a rollback — manual or automatic — occurs, including the reason and the resulting active version.
- **Actor(s):** Application Owner, AI Coding Agent / Claude Code
- **Trigger:** Rollback reaches Completed or Failed status (FR-101).
- **Preconditions:** A rollback has occurred.
- **Main Flow:**
  1. Rollback completes (successfully or not).
  2. Platform generates a notification summarizing the rollback (trigger reason, prior version, new active version, outcome).
  3. Notification is delivered per Module X to the Application Owner and original requester.
- **Alternative Flow:** Automatic rollback (FR-099) generates a higher-urgency notification than a manual, owner-requested rollback, since it signals an unplanned incident.
- **Exception Flow:** N/A.
- **Business Rules:** Rollback notification is always sent regardless of whether the rollback was manual or automatic, successful or failed.
- **Input:** Rollback outcome.
- **Output:** Delivered notification.
- **Acceptance Criteria:** Every rollback event, automatic or manual, results in a notification to the Application Owner containing the reason and resulting version.
- **Priority:** MUST

---

## Module W — Audit Log

#### FR-103 — Immutable Audit Trail of Platform Actions

- **Description:** The platform must record an immutable audit entry for every significant action taken on the platform — by employees, agents (on employees' behalf), and administrators alike — covering authentication, configuration changes, deployments, lifecycle transitions, secret operations, and administrative changes.
- **Actor(s):** Management / Auditor, Security Administrator, Platform Administrator
- **Trigger:** Any significant platform action (spanning Modules A through AB).
- **Preconditions:** None — auditing is a cross-cutting requirement applied platform-wide.
- **Main Flow:**
  1. A significant action occurs (e.g., login, deployment, role change, secret rotation, lifecycle transition).
  2. Platform records an audit entry: actor identity (including, for agent-initiated actions, both the agent and the employee it acted for), action, target resource, timestamp, and outcome.
  3. Entry is written to an append-only audit store.
- **Alternative Flow:** None.
- **Exception Flow:** Audit write itself fails → the triggering action is not considered complete until the audit entry is durably recorded, for any action classified as security- or lifecycle-critical (i.e., no critical action succeeds silently without an audit trail).
- **Business Rules:** Audit entries are never edited or deleted by any actor, including Platform Administrators, through normal platform operation; agent-initiated actions are always attributed to both the AI agent and the human employee it acted on behalf of (see FR-117).
- **Input:** Platform action event.
- **Output:** Immutable audit entry.
- **Acceptance Criteria:** Every deployment, lifecycle transition, secret operation, and administrative change has a corresponding, complete audit entry.
- **Priority:** MUST

#### FR-104 — Audit Log Query and Search

- **Description:** The platform must let authorized Auditors, Security Administrators, and Platform Administrators search and filter the audit log by actor, application, action type, and time range.
- **Actor(s):** Management / Auditor, Security Administrator, Platform Administrator
- **Trigger:** Audit query request.
- **Preconditions:** Requester holds an audit-access role.
- **Main Flow:**
  1. Requester submits a query (actor, application, action type, time range, or combination).
  2. Platform authorizes the requester's audit-access scope.
  3. Matching audit entries are returned, ordered chronologically.
- **Alternative Flow:** Auditor performs a broad, cross-department query as part of a compliance review, distinct from a Security Administrator's narrower incident-investigation query.
- **Exception Flow:** Requester lacks audit-access privileges → rejected.
- **Business Rules:** Audit query access is itself a privileged capability, distinct from an Application Owner's own scoped log/metrics access (Modules S, T); every audit query is itself logged (self-referential auditing) to prevent unchecked surveillance of employee activity.
- **Input:** Query filters.
- **Output:** Matching audit entries.
- **Acceptance Criteria:** An Auditor can retrieve a complete, chronologically ordered record of all actions taken against a specific application or by a specific actor within a given time range.
- **Priority:** MUST

#### FR-105 — Audit Log Export

- **Description:** The platform must allow authorized Auditors/administrators to export audit log query results in a structured format for offline compliance review, external reporting, or legal hold.
- **Actor(s):** Management / Auditor, Security Administrator
- **Trigger:** Export request following an audit query (FR-104).
- **Preconditions:** Requester is authorized and has performed a valid audit query.
- **Main Flow:**
  1. Auditor performs an audit query (FR-104).
  2. Auditor requests export of the result set.
  3. Platform generates a structured export (e.g., CSV/JSON) and delivers it to the requester.
  4. The export action itself is recorded in the audit log.
- **Alternative Flow:** None.
- **Exception Flow:** Export of an extremely large result set is paginated/batched rather than failing outright.
- **Business Rules:** Exported audit data never includes plaintext secret values (consistent with FR-070), even if the underlying entries reference secret operations.
- **Input:** Audit query result set.
- **Output:** Structured export file.
- **Acceptance Criteria:** An authorized auditor can export a query result set to a structured file suitable for offline review, and the export itself appears in the audit trail.
- **Priority:** SHOULD

#### FR-106 — Audit Log Tamper Protection

- **Description:** The platform must protect the audit log against tampering, deletion, or unauthorized modification, including protection against a compromised administrative account attempting to cover its tracks.
- **Actor(s):** Security Administrator, Platform Administrator
- **Trigger:** Any attempt to modify or delete an existing audit entry.
- **Preconditions:** Audit entries exist.
- **Main Flow:**
  1. Platform stores audit entries in an append-only store with no standard platform capability to edit or delete individual entries.
  2. Any attempted direct modification (e.g., via underlying data store access outside platform-mediated paths) is detectable through integrity verification (e.g., hashing/chaining), owned in technical depth by the Non-Functional/Security docs.
  3. Detected tampering is treated as a critical security incident.
- **Alternative Flow:** Bulk audit log purge only ever occurs through the defined retention policy process (Module W's own retention rules, exact period **TBD — see Decision Log**), never through an ad hoc administrative delete action.
- **Exception Flow:** A tampering attempt is detected → immediately escalated to Security Administrator and Management/Auditor as a critical incident, independent of who attempted it.
- **Business Rules:** No role, including Platform Administrator, has a standard platform capability to delete or edit an individual audit entry.
- **Input:** N/A (protective/detective control).
- **Output:** Tamper-resistant audit store; incident alert on detected tampering.
- **Acceptance Criteria:** No platform UI, API, or MCP capability exists to edit or delete an individual audit entry outside the defined retention-purge process.
- **Priority:** MUST

---

## Module X — Notification

#### FR-107 — Deployment Status Notifications

- **Description:** The platform must notify the relevant Employee/Agent and Application Owner of key deployment status changes (started, succeeded, failed, awaiting approval) so they do not need to continuously poll status.
- **Actor(s):** Employee, AI Coding Agent / Claude Code, Application Owner
- **Trigger:** A deployment reaches a key status milestone (Module J).
- **Preconditions:** A deployment is in progress or has concluded.
- **Main Flow:**
  1. Deployment pipeline reaches a milestone (e.g., Completed, Failed, awaiting production approval).
  2. Platform generates a notification summarizing the milestone.
  3. Notification is delivered to the requester and Application Owner via their configured channel(s).
- **Alternative Flow:** Claude Code, acting on the employee's behalf, receives the equivalent status via the MCP status-query tools (Module Y) as part of its own feedback loop, in addition to any human-facing notification.
- **Exception Flow:** Notification delivery fails → retried per policy; failure to deliver does not block the underlying deployment pipeline itself.
- **Business Rules:** Failure and approval-required notifications are always sent; success notifications may be configurable (e.g., digest vs. immediate) per employee preference where the platform supports it.
- **Input:** Deployment milestone event.
- **Output:** Delivered notification.
- **Acceptance Criteria:** A failed deployment results in a notification to the requester within the platform's defined notification latency.
- **Priority:** MUST

#### FR-108 — Approval Request Notifications

- **Description:** The platform must notify the designated approver(s) promptly when a production deployment, external domain exposure, or production secret operation is awaiting their approval (Modules J, O, P).
- **Actor(s):** Application Owner, Platform Administrator, Security Administrator
- **Trigger:** A pipeline step reaches an approval checkpoint (FR-042, FR-071, FR-074).
- **Preconditions:** An approval gate is pending.
- **Main Flow:**
  1. Pipeline pauses at an approval checkpoint.
  2. Platform identifies the designated approver(s) for this application/action.
  3. Notification is delivered to the approver(s), including enough context (application, version/change summary, requester) to make an informed decision.
- **Alternative Flow:** Reminder notifications are sent if the approval remains pending beyond a defined window, until the approval expires (per the relevant module's expiry rule) or is actioned.
- **Exception Flow:** No approver is currently reachable/active for the application (e.g., owner deactivated) → escalated to Platform Administrator as a stalled-approval condition (mirrors FR-018).
- **Business Rules:** Approval-request notifications are always delivered promptly — they gate a pipeline step, unlike routine status notifications (FR-107) which are informational only.
- **Input:** Pending approval event.
- **Output:** Delivered approval-request notification.
- **Acceptance Criteria:** A production deployment reaching the approval gate results in a notification to the designated approver(s) within the platform's defined notification latency.
- **Priority:** MUST

#### FR-109 — Security and Policy Violation Notifications

- **Description:** The platform must notify the Security Administrator (and, where appropriate, the Application Owner) promptly when a security policy violation, network policy violation, or repeated failed validation of a prohibited pattern is detected (Modules H, Q, W).
- **Actor(s):** Security Administrator, Application Owner
- **Trigger:** A security-relevant event is detected (FR-031 violation, FR-081 network violation, FR-106 tamper detection, etc.).
- **Preconditions:** A security-relevant detection has occurred.
- **Main Flow:**
  1. Detecting subsystem raises a security event with severity and detail.
  2. Platform routes the notification to the Security Administrator, and to the Application Owner where the violation originates from their application.
  3. Notification includes enough detail to begin investigation without requiring a separate audit query first.
- **Alternative Flow:** Critical-severity events (e.g., audit tamper detection) are escalated through a higher-urgency channel than routine policy-violation notices.
- **Exception Flow:** N/A.
- **Business Rules:** Security notifications are never suppressed or batched into a digest the way routine status notifications may be — they are delivered promptly given their sensitivity.
- **Input:** Security event.
- **Output:** Delivered security notification.
- **Acceptance Criteria:** A simulated security policy violation (e.g., attempted cross-application access) results in a Security Administrator notification within the platform's defined alerting latency.
- **Priority:** MUST

---

## Module Y — MCP Integration

> The requirements below define the **business-level** obligation to expose each capability of the Company Deployment MCP server to Claude Code, consistent with the MCP server exposing only high-level business-capability tools — never low-level infrastructure operations. Full tool I/O schemas, error codes, and protocol-level detail are owned by **07_MCP_Requirements.md**; this module states what must be exposed and why, not the wire contract.

#### FR-110 — Platform and Stack Discovery Tools

- **Description:** The platform must expose read-only MCP tools (`get_platform_info`, `get_supported_stacks`, `get_deployment_requirements`) so Claude Code can discover platform capabilities, the current supported stack catalog (Module F), and what a valid `deployment.yaml` requires (Module G) before attempting to configure an application.
- **Actor(s):** AI Coding Agent / Claude Code, Employee
- **Trigger:** Claude Code begins assisting an employee with a new or existing application deployment.
- **Preconditions:** MCP session is authenticated (FR-004) on behalf of an authenticated employee (FR-001).
- **Main Flow:**
  1. Claude Code calls `get_platform_info` to learn general platform capabilities/constraints.
  2. Claude Code calls `get_supported_stacks` to retrieve the current catalog (FR-019).
  3. Claude Code calls `get_deployment_requirements` to learn the required `deployment.yaml` shape/fields.
  4. Claude Code uses this information to guide the employee and generate a compliant configuration (FR-119).
- **Alternative Flow:** None — these are read-only, side-effect-free discovery calls usable at any point in the conversation.
- **Exception Flow:** MCP session unauthenticated → all three tools reject the call.
- **Business Rules:** These tools return only business-level, current-state information — never infrastructure credentials, internal engine endpoints, or other applications' data.
- **Input:** None (or minimal query parameters).
- **Output:** Platform info, supported stack catalog, deployment requirement schema.
- **Acceptance Criteria:** Claude Code can retrieve the current supported stack catalog and required configuration shape at any time without prior application context.
- **Priority:** MUST

#### FR-111 — Application Creation via MCP

- **Description:** The platform must expose an MCP tool (`create_application`) that lets Claude Code register a new application on the employee's behalf, invoking the same registration business logic and constraints as direct console use (Module D).
- **Actor(s):** AI Coding Agent / Claude Code, Employee
- **Trigger:** Employee, through Claude Code, decides to register a new application.
- **Preconditions:** Employee is authenticated; Claude Code's MCP session carries the employee's identity (FR-004, FR-117).
- **Main Flow:**
  1. Claude Code calls `create_application` with the employee-approved application details.
  2. Company Platform API applies the identical validation and business rules as FR-011/FR-012, regardless of caller.
  3. On success, the new application record (Draft state) is returned to Claude Code, which relays the outcome to the employee.
- **Alternative Flow:** None.
- **Exception Flow:** Name collision, missing department, or unauthorized identity → tool call fails with the same structured error a direct console call would produce (Module D exception flows apply identically).
- **Business Rules:** `create_application` never bypasses any Module D business rule — the MCP layer is a caller, not a privilege escalation path.
- **Input:** Application name, department, initial configuration draft.
- **Output:** Created application record (Draft state) or structured error.
- **Acceptance Criteria:** An application created via `create_application` is indistinguishable, in resulting state and applied validation, from one created via direct console registration.
- **Priority:** MUST

#### FR-112 — Application Validation via MCP

- **Description:** The platform must expose an MCP tool (`validate_application`) that runs the full deployment validation pass (Module H) against an application's current configuration and returns the itemized report, letting Claude Code iterate on a configuration before attempting a real deployment.
- **Actor(s):** AI Coding Agent / Claude Code, Employee
- **Trigger:** Claude Code has authored or edited an application's `deployment.yaml` and wants to check readiness.
- **Preconditions:** Application exists; caller is authorized for it.
- **Main Flow:**
  1. Claude Code calls `validate_application` for the target application.
  2. Platform runs the full validation pass (FR-029) and returns the itemized report (FR-034).
  3. On failure, Claude Code parses the specific failures and can autonomously correct common issues (e.g., unsupported stack, missing field) before re-validating.
- **Alternative Flow:** Claude Code calls this repeatedly in a tight loop while iterating on a configuration, entirely without deploying anything, since validation has no deployment side effects.
- **Exception Flow:** Application not found or caller unauthorized → structured error, no validation attempted.
- **Business Rules:** `validate_application` never triggers a build or deployment as a side effect — it is purely evaluative, safe to call repeatedly.
- **Input:** Application identifier.
- **Output:** Itemized validation report.
- **Acceptance Criteria:** Repeated calls to `validate_application` against an unchanged configuration return identical results and produce no deployment side effects.
- **Priority:** MUST

#### FR-113 — Deployment Initiation via MCP

- **Description:** The platform must expose an MCP tool (`deploy_application`) that initiates the full deployment pipeline (Module J) for a validated application, subject to the identical authentication, authorization, validation, security, and approval gates as any other deployment trigger.
- **Actor(s):** AI Coding Agent / Claude Code, Employee
- **Trigger:** Employee, through Claude Code, requests deployment of a validated application.
- **Preconditions:** Application has passed validation (FR-029); Claude Code's MCP session carries the employee's authenticated identity.
- **Main Flow:**
  1. Claude Code calls `deploy_application`, specifying the target application and environment.
  2. Company Platform API runs the identical deployment pipeline as FR-039, with all gates (authorization, validation, security check, image scan, approval-if-production) applied exactly as they would be for any other caller.
  3. Result (accepted/queued, or a specific gate rejection) is returned to Claude Code.
- **Alternative Flow:** Target environment is production → the pipeline pauses at the approval gate (FR-042) exactly as it would for a console-initiated deployment; Claude Code reports the pending-approval status back to the employee rather than the deployment proceeding unattended.
- **Exception Flow:** Any gate rejects the request → structured failure returned, with enough detail for Claude Code to explain the rejection to the employee and, where correctable, attempt a fix and retry.
- **Business Rules:** `deploy_application` grants Claude Code no authority the calling employee does not already have — every gate defined in Module J is re-evaluated server-side regardless of the caller being an AI agent, consistent with never trusting the agent as a security boundary.
- **Input:** Application identifier, target environment.
- **Output:** Deployment request accepted (with tracking ID) or rejected with reason.
- **Acceptance Criteria:** A `deploy_application` call targeting production for an employee without production-approval authority results in the identical gate behavior as a direct console attempt by that same employee.
- **Priority:** MUST

#### FR-114 — Status and Observability via MCP

- **Description:** The platform must expose MCP tools (`get_application_status`, `get_deployment_status`, `get_application_logs`, `get_application_metrics`) so Claude Code can observe the outcome of its own actions and help the employee diagnose issues, scoped by the same authorization rules as their direct-access equivalents (Modules J, S, T).
- **Actor(s):** AI Coding Agent / Claude Code, Employee, Application Owner
- **Trigger:** Claude Code needs to check on a deployment it initiated, or the employee asks about an application's current state.
- **Preconditions:** Caller is authorized for the target application.
- **Main Flow:**
  1. Claude Code calls the relevant status/observability tool with the application (and, where applicable, deployment) identifier.
  2. Platform authorizes the request against the employee's application ownership/role (FR-089, FR-091 authorization logic applies identically).
  3. Current status/logs/metrics are returned to Claude Code for interpretation and relay to the employee.
- **Alternative Flow:** Claude Code polls `get_deployment_status` in a loop immediately after calling `deploy_application` to report progress conversationally to the employee.
- **Exception Flow:** Caller unauthorized for the target application → rejected, identical to the direct-access exception flows in Modules J/S/T.
- **Business Rules:** These tools are read-only and carry the identical authorization scoping as their non-MCP equivalents — no broader visibility is granted through the MCP path.
- **Input:** Application/deployment identifier, optional filters (log/metric queries).
- **Output:** Status, logs, or metrics data.
- **Acceptance Criteria:** A `get_application_logs` call via MCP for an application the employee does not own/have access to is rejected identically to a direct console attempt.
- **Priority:** MUST

#### FR-115 — Lifecycle Operations via MCP

- **Description:** The platform must expose MCP tools (`rollback_application`, `restart_application`, `delete_application`) so Claude Code can perform lifecycle and rollback operations (Modules K, V) on the employee's behalf, subject to the identical business rules, approval gates, and ownership checks as their direct-access equivalents.
- **Actor(s):** AI Coding Agent / Claude Code, Employee, Application Owner
- **Trigger:** Employee, through Claude Code, requests a rollback, restart, or deletion of an application.
- **Preconditions:** Caller is authorized (ownership per Module E); target application is in a state from which the requested operation is valid (Module K).
- **Main Flow:**
  1. Claude Code calls the relevant lifecycle tool with the target application (and, for rollback, target version).
  2. Platform applies the identical validation, ownership, and approval-gate logic as the corresponding direct requirement (FR-098, FR-048, FR-050).
  3. Result is returned to Claude Code and relayed to the employee.
- **Alternative Flow:** `delete_application` on a production application without the required approval (FR-014) is held pending approval rather than executed, exactly as a direct console request would be.
- **Exception Flow:** Requested operation is invalid from the application's current lifecycle state (e.g., restarting a Deleted application) → rejected with the same state-model error as FR-045.
- **Business Rules:** None of these three tools grants Claude Code any capability beyond what the employee already holds directly; ownership verification (FR-018) applies identically to MCP-initiated calls.
- **Input:** Application identifier, operation-specific parameters (e.g., rollback target version).
- **Output:** Operation result (success, pending-approval, or structured rejection).
- **Acceptance Criteria:** A `delete_application` call via MCP against a production application without approval produces the same held/pending outcome as a direct console request.
- **Priority:** MUST

---

## Module Z — Claude Code / AI Agent Integration

#### FR-116 — Company Deployment Skill Invocation

- **Description:** The platform's intended integration path requires that Claude Code invoke the Company Deployment Skill (a packaged, company-authored set of instructions — detailed fully in **08_Company_Deployment_Skill.md**) whenever an employee's request involves deploying, configuring, or managing an application on this platform, ensuring consistent, company-approved guidance rather than ad hoc agent behavior.
- **Actor(s):** AI Coding Agent / Claude Code, Employee
- **Trigger:** Employee asks Claude Code to deploy, configure, check on, or manage a company application.
- **Preconditions:** Company Deployment Skill is installed/available to Claude Code in the employee's environment.
- **Main Flow:**
  1. Employee expresses a deployment-related intent to Claude Code.
  2. Claude Code recognizes the intent matches the Company Deployment Skill's trigger conditions and invokes it.
  3. The Skill's instructions guide Claude Code's subsequent use of the Company Deployment MCP tools (Module Y) for the remainder of the task.
- **Alternative Flow:** Employee explicitly invokes the skill by name/command rather than relying on Claude Code's automatic recognition.
- **Exception Flow:** Skill is unavailable/not installed → Claude Code has no MCP-mediated path to deployment capability and must inform the employee, rather than attempting an unmediated, unsupported action.
- **Business Rules:** The Skill is the sole sanctioned entry point connecting Claude Code's general coding assistance to this platform's deployment capabilities; detailed skill behavior/prompt design is owned by 08_Company_Deployment_Skill.md.
- **Input:** Employee's natural-language deployment-related request.
- **Output:** Company Deployment Skill invoked, MCP tool usage follows.
- **Acceptance Criteria:** A deployment-related employee request results in Claude Code operating through the Company Deployment Skill and MCP tools, not through unrelated general-purpose coding actions (e.g., shelling out to a local Docker daemon).
- **Priority:** MUST

#### FR-117 — Agent Identity and Attribution

- **Description:** Every action Claude Code performs against the platform must be attributable to both the AI agent and the specific human employee it is acting on behalf of — the platform must never treat an agent-initiated action as though it were authored by an anonymous or platform-level identity.
- **Actor(s):** AI Coding Agent / Claude Code, Employee
- **Trigger:** Any MCP tool call from Claude Code to the Company Deployment MCP server.
- **Preconditions:** Employee has an authenticated session (Module A); Claude Code is operating within that session's context.
- **Main Flow:**
  1. Claude Code makes an MCP tool call, carrying the employee's authenticated identity/claims alongside the agent's own service identity (mirrors FR-004).
  2. Company Platform API records both identities against the resulting action (FR-103).
  3. All downstream authorization decisions (Module Y flows) are evaluated against the employee's identity/role, not the agent's.
- **Alternative Flow:** None.
- **Exception Flow:** A tool call arrives without a valid forwarded employee identity → rejected outright, regardless of the agent's own service credential validity (mirrors FR-004 business rule).
- **Business Rules:** The AI agent never possesses independent authority to act on the platform — it is always a proxy for a specific, currently-authenticated employee; this dual attribution is permanent and appears in every relevant audit entry (Module W).
- **Input:** Employee session context, agent service identity.
- **Output:** Dual-attributed action record.
- **Acceptance Criteria:** Every audit entry for an agent-initiated action names both the employee and the fact that it was agent-mediated; no action exists in the audit trail attributed to "Claude Code" alone without an associated employee.
- **Priority:** MUST

#### FR-118 — Agent Authorization Boundary Enforcement

- **Description:** The platform must independently enforce every authorization, validation, security, and approval check on every agent-initiated request exactly as it would for a direct human request — the AI agent's own judgment, guardrails, or refusal behavior are never treated as a substitute for server-side policy enforcement.
- **Actor(s):** AI Coding Agent / Claude Code, Security Administrator, Platform Administrator
- **Trigger:** Any MCP tool call from Claude Code that maps to a business action (Module Y).
- **Preconditions:** None — this is a universal, cross-cutting enforcement requirement.
- **Main Flow:**
  1. Claude Code submits a tool call.
  2. Company Platform API evaluates the identical authorization/validation/security/approval logic it would apply to a directly-submitted request (Modules A, H, J, O, P), using the forwarded employee identity (FR-117).
  3. Outcome (allow/deny/gate) is identical to what a direct request from that same employee would have produced.
- **Alternative Flow:** None.
- **Exception Flow:** A request that an ideally-behaving Skill/agent "should" have refused to even attempt is nonetheless submitted (e.g., due to a prompt injection, bug, or model error) → the platform's own server-side checks still catch and reject it, since the agent is never the security boundary.
- **Business Rules:** This requirement is the platform-level restatement of the project's core security principle: the platform enforces authorization/policy independently of the AI agent. It is deliberately redundant with per-module business rules (e.g., FR-031, FR-062, FR-113) to make the invariant explicit at the integration layer.
- **Input:** Agent-initiated request.
- **Output:** Server-side-enforced allow/deny/gate decision, independent of agent behavior.
- **Acceptance Criteria:** A penetration-style test that attempts a prohibited action (e.g., cross-application secret access) via a crafted MCP call — bypassing the Skill's own guidance — is still rejected by the Platform API.
- **Priority:** MUST

#### FR-119 — Guided `deployment.yaml` Generation

- **Description:** Claude Code, guided by the Company Deployment Skill, should be able to inspect an employee's application source code and generate a draft, schema-conformant `deployment.yaml` (Module G) — inferring services, runtimes, and reasonable defaults — for the employee to review and confirm before submission.
- **Actor(s):** AI Coding Agent / Claude Code, Employee
- **Trigger:** Employee asks Claude Code to prepare an application for deployment, without having authored `deployment.yaml` manually.
- **Preconditions:** Application source code is accessible to Claude Code; Company Deployment Skill is invoked (FR-116).
- **Main Flow:**
  1. Claude Code inspects the source code structure (e.g., detects a React frontend and a Go backend).
  2. Claude Code queries supported stacks/requirements via MCP (FR-110) to ground its inference in current platform capability.
  3. Claude Code drafts a `deployment.yaml` and presents it to the employee for review/edit.
  4. Employee confirms (with or without edits); Claude Code submits the confirmed configuration (FR-111/via direct save).
- **Alternative Flow:** Claude Code's inference is ambiguous (e.g., cannot determine a runtime) → it asks the employee to clarify rather than guessing and submitting an unreviewed configuration.
- **Exception Flow:** Inferred configuration fails validation (Module H) on submission → Claude Code relays the specific failure to the employee and offers a corrected draft.
- **Business Rules:** A generated `deployment.yaml` is never submitted for actual deployment without employee review/confirmation of at least the first version, even though Claude Code may auto-correct subsequent validation failures.
- **Input:** Application source code.
- **Output:** Draft `deployment.yaml`, confirmed by the employee.
- **Acceptance Criteria:** Given a recognizable application source layout, Claude Code produces a draft configuration that passes schema validation (FR-024) on first attempt in the common case.
- **Priority:** SHOULD

#### FR-120 — Agent-Initiated Deployment Request Flow

- **Description:** The platform must support a complete, coherent conversational flow in which Claude Code takes an employee from a natural-language deployment intent through validation, deployment initiation, and status reporting, using only the exposed MCP tools (Module Y) at each step.
- **Actor(s):** AI Coding Agent / Claude Code, Employee
- **Trigger:** Employee expresses intent to deploy an application through a Claude Code conversation.
- **Preconditions:** Company Deployment Skill invoked (FR-116); employee authenticated.
- **Main Flow:**
  1. Claude Code ensures the application is registered (FR-111, if new) and configured (FR-119).
  2. Claude Code validates the configuration (FR-112), iterating on any failures autonomously where possible.
  3. Claude Code initiates deployment (FR-113).
  4. Claude Code monitors status (FR-114) and reports progress/outcome to the employee conversationally, including any pending-approval state.
- **Alternative Flow:** Deployment targets production and pauses at the approval gate (FR-042/FR-113) — Claude Code informs the employee that human approval is required and who the approver is, rather than implying the deployment is complete.
- **Exception Flow:** Any step fails (validation, security, quota) → Claude Code surfaces the specific failure and, where it is a correctable configuration issue, proposes a fix for employee confirmation rather than silently retrying indefinitely.
- **Business Rules:** This end-to-end flow is composed entirely of the individually gated MCP tools in Module Y — there is no "fast path" tool that bypasses validation or approval for agent-initiated requests.
- **Input:** Employee's natural-language deployment request.
- **Output:** Deployed (or pending-approval, or rejected-with-reason) application, reported conversationally.
- **Acceptance Criteria:** A complete "deploy my app" conversational request results in either a Running application, a clearly reported pending-approval state, or a clearly reported and specific failure — never an ambiguous or silent outcome.
- **Priority:** MUST

#### FR-121 — Agent Feedback Loop for Iterative Correction

- **Description:** The platform must return structured, machine-actionable error/validation detail (not just human-readable prose) from every MCP tool so Claude Code can programmatically identify the specific failing field/rule and attempt an autonomous correction before involving the employee again.
- **Actor(s):** AI Coding Agent / Claude Code
- **Trigger:** Any MCP tool call returns a failure/validation-error result (e.g., FR-112, FR-113 rejections).
- **Preconditions:** A tool call has failed with a correctable, structured reason.
- **Main Flow:**
  1. MCP tool call fails; platform returns a structured error (error code/category, offending field, human-readable explanation).
  2. Claude Code parses the structured error and determines whether it is autonomously correctable (e.g., unsupported stack version, missing required field) or requires employee input (e.g., ambiguous business decision, policy rejection).
  3. For correctable errors, Claude Code adjusts the configuration and retries the relevant tool call.
  4. For non-correctable errors (e.g., quota exceeded, security rejection, missing approval), Claude Code surfaces the issue to the employee rather than retrying blindly.
- **Alternative Flow:** Repeated retries without resolution (e.g., a persistent platform-side issue) are capped, after which Claude Code stops and reports the unresolved failure rather than looping indefinitely.
- **Exception Flow:** N/A — this requirement is itself the platform's contribution to a well-behaved retry loop; runaway-agent protection (e.g., rate limiting repeated calls) is a Non-Functional/security concern.
- **Business Rules:** Structured error responses never include information that would let Claude Code bypass a check (e.g., no error message reveals another application's internal configuration); auto-correction is scoped strictly to configuration-shape issues, never to silently reinterpreting a security or approval rejection as something to retry around.
- **Input:** MCP tool failure result.
- **Output:** Structured, machine-parseable error detail.
- **Acceptance Criteria:** Given a `deployment.yaml` with a single unsupported-stack field, Claude Code can identify the exact offending field from the structured error without requiring the employee to interpret a generic failure message.
- **Priority:** MUST

---

## Module AA — Administration

#### FR-122 — Platform Configuration Management

- **Description:** The platform must allow Platform Administrators to manage global platform configuration (e.g., default policies, environment definitions, approval-workflow defaults) distinct from any single application's configuration.
- **Actor(s):** Platform Administrator
- **Trigger:** Administrator updates a global platform setting.
- **Preconditions:** Requester holds Platform Administrator privileges.
- **Main Flow:**
  1. Administrator opens platform configuration management.
  2. Administrator updates a global setting (e.g., default resource tier, default egress posture).
  3. Platform persists the change and applies it to subsequent evaluations; it is recorded in the audit log (Module W).
- **Alternative Flow:** A staged/preview mode lets an administrator review the impact of a proposed global change before applying it platform-wide, where the platform supports it.
- **Exception Flow:** Change would leave the platform in an inconsistent state (e.g., removing a stack still in active use) → blocked or requires an explicit acknowledged override.
- **Business Rules:** Global configuration changes are always effective going forward only, never retroactively altering already-Running applications' behavior without a separate, explicit migration action.
- **Input:** Global configuration change.
- **Output:** Updated platform configuration.
- **Acceptance Criteria:** A global configuration change is applied to new requests going forward and is fully traceable in the audit log.
- **Priority:** MUST

#### FR-123 — Stack Catalog Administration

- **Description:** The platform must provide Platform/IT Administrators an administrative interface for maintaining the supported stack catalog and version governance (Module F) as a first-class administrative function, distinct from application-level configuration.
- **Actor(s):** IT Administrator, Platform Administrator
- **Trigger:** Administrator manages the stack catalog (this requirement is the administrative-surface restatement of FR-019/FR-022).
- **Preconditions:** Requester holds catalog-management privileges.
- **Main Flow:**
  1. Administrator views the current catalog with usage statistics (how many applications use each stack/version).
  2. Administrator adds, deprecates, or blocks a stack/version.
  3. Change is published and immediately effective for future validations (Module H).
- **Alternative Flow:** None.
- **Exception Flow:** Attempted removal of a stack with active dependent applications → blocked or requires an acknowledged override with a remediation plan for affected owners.
- **Business Rules:** Mirrors FR-019/FR-022's business rules; this requirement specifically ensures an administrative surface exists for that governance.
- **Input:** Stack catalog change.
- **Output:** Updated catalog, visible via administrative interface.
- **Acceptance Criteria:** An administrator can view which applications are affected by a proposed stack deprecation before finalizing it.
- **Priority:** SHOULD

#### FR-124 — Quota and Policy Administration

- **Description:** The platform must provide Platform Administrators an administrative interface for managing department/application quota tiers and policy assignments (Module C, M) at scale, including bulk review and adjustment.
- **Actor(s):** Platform Administrator, IT Administrator
- **Trigger:** Periodic quota/policy review, or a specific department's quota adjustment request.
- **Preconditions:** Requester holds quota-management privileges.
- **Main Flow:**
  1. Administrator views current quota/policy assignments across departments.
  2. Administrator adjusts a department's or application's tier/quota.
  3. Change takes effect for subsequent validation checks (FR-032, FR-059).
- **Alternative Flow:** Administrator grants a time-boxed quota exception for a specific application (mirrors FR-059 alternative flow) rather than a permanent department-wide change.
- **Exception Flow:** Requested change would exceed platform-wide capacity ceilings → blocked.
- **Business Rules:** Exact quota numbers remain **TBD — see Decision Log**; this is the administrative mechanism regardless of final values.
- **Input:** Quota/policy adjustment.
- **Output:** Updated quota/policy assignment.
- **Acceptance Criteria:** A quota change made through this administrative interface is reflected in the very next validation check against the affected department/application.
- **Priority:** SHOULD

#### FR-125 — User and Role Administration

- **Description:** The platform must provide an administrative interface for Platform/IT Administrators to manage users, roles, and department associations at scale (Modules B, C), including bulk operations for onboarding/offboarding events.
- **Actor(s):** Platform Administrator, IT Administrator
- **Trigger:** Administrator performs user/role management (this requirement is the administrative-surface restatement of FR-005–FR-009).
- **Preconditions:** Requester holds user-administration privileges.
- **Main Flow:**
  1. Administrator searches/filters the user directory.
  2. Administrator views/edits a user's roles and department association.
  3. Change is applied per FR-006/FR-009's business rules and recorded in the audit log.
- **Alternative Flow:** Bulk role/department reassignment is performed as part of an organizational restructuring event.
- **Exception Flow:** Administrator attempts an action beyond their own delegated authority (mirrors FR-006 exception flow) → rejected.
- **Business Rules:** Mirrors Module B/C business rules; this requirement ensures the administrative surface for them exists and scales to bulk operations.
- **Input:** User/role/department administrative change.
- **Output:** Updated user records.
- **Acceptance Criteria:** An administrator can locate any platform user and view/adjust their roles and department from a single administrative view.
- **Priority:** SHOULD

#### FR-126 — Approval Workflow Administration

- **Description:** The platform must let Platform/Security Administrators configure which roles serve as approvers for each type of approval gate (production deployment FR-042, external domain exposure FR-074, production secret operations FR-071), per department or platform-wide default.
- **Actor(s):** Platform Administrator, Security Administrator
- **Trigger:** Approval workflow policy is defined or updated.
- **Preconditions:** Requester holds approval-workflow administration privileges.
- **Main Flow:**
  1. Administrator selects an approval gate type and defines the required approver role(s) (e.g., Application Owner only, or Application Owner + Security Administrator for external exposure).
  2. Platform stores the configuration, applied per department where department-level overrides exist, else platform-wide default.
  3. Subsequent approval-gated actions (Modules J, O, P) route to the configured approver(s).
- **Alternative Flow:** A department requests a stricter-than-default approval policy (e.g., two-approver requirement) for its own applications.
- **Exception Flow:** Configuration would leave an approval gate with no valid approver role assigned → rejected, since every gate must always have a resolvable approver.
- **Business Rules:** No approval gate can ever be configured to require zero approvers — that would be equivalent to silently disabling the control, which is out of scope for any role including Platform Administrator to do unilaterally.
- **Input:** Approval gate type, approver role configuration.
- **Output:** Updated approval workflow configuration.
- **Acceptance Criteria:** A production deployment request routes to exactly the approver role(s) currently configured for that application's department.
- **Priority:** MUST

---

## Module AB — Reporting

#### FR-127 — Application Inventory Report

- **Description:** The platform must produce a queryable inventory report of all applications — their department, owner, lifecycle state, stack, and environment — supporting organizational visibility into what is running on the platform.
- **Actor(s):** Management / Auditor, Platform Administrator, Application Owner
- **Trigger:** Report request (ad hoc query, or scheduled periodic generation where the platform supports it).
- **Preconditions:** Requester holds a reporting-access role.
- **Main Flow:**
  1. Requester specifies report scope (all applications, a department, a lifecycle state filter, etc.).
  2. Platform aggregates current application records matching the scope.
  3. Report is generated and returned/exported.
- **Alternative Flow:** Application Owner views a scoped inventory limited to applications they own, rather than the platform-wide view available to administrators/auditors.
- **Exception Flow:** Requester's scope exceeds their authorization (e.g., a non-administrator requesting platform-wide detail) → scope is limited to what they are authorized to see, not rejected outright, so a useful (if narrower) report is still returned.
- **Business Rules:** Inventory reflects current state at generation time; it is not itself a historical/audit record (that is Module W's role).
- **Input:** Report scope/filters.
- **Output:** Application inventory report.
- **Acceptance Criteria:** A Management/Auditor can generate a complete, accurate inventory of all applications, their owners, departments, and current lifecycle states at any time.
- **Priority:** MUST

#### FR-128 — Deployment Activity Report

- **Description:** The platform must produce a report summarizing deployment activity over a given period — counts by outcome (succeeded/failed/rolled back), by department, and by environment — to support operational and management visibility into platform usage and reliability trends.
- **Actor(s):** Management / Auditor, Platform Administrator
- **Trigger:** Report request for a specified time range.
- **Preconditions:** Deployment history exists (Module J, W).
- **Main Flow:**
  1. Requester specifies a time range and optional department/environment filter.
  2. Platform aggregates deployment outcomes from the audit/version history over that range.
  3. Report is generated summarizing counts and trends (e.g., deployment frequency, failure rate, rollback rate).
- **Alternative Flow:** Report is broken down per department to support department-level accountability discussions.
- **Exception Flow:** Requested range predates available retained history (Module W/U retention) → report clearly indicates the actual available range rather than silently under-reporting.
- **Business Rules:** This report is derived from, but distinct from, the raw audit log (Module W) — it is a summarized, management-facing view rather than a line-by-line record.
- **Input:** Time range, optional filters.
- **Output:** Deployment activity summary report.
- **Acceptance Criteria:** A generated report's totals (deployments, failures, rollbacks) reconcile with the underlying audit log for the same period and scope.
- **Priority:** SHOULD

#### FR-129 — Resource Utilization Report

- **Description:** The platform must produce a report summarizing resource consumption (against quota) by department and application over a given period, supporting cost awareness and capacity planning conversations, building on the real-time usage visibility of Module M.
- **Actor(s):** Management / Auditor, Platform Administrator, Application Owner
- **Trigger:** Report request for a specified time range/scope.
- **Preconditions:** Resource usage data exists (FR-060).
- **Main Flow:**
  1. Requester specifies scope (department, application, time range).
  2. Platform aggregates resource allocation/consumption data across the scope and period.
  3. Report is generated showing utilization against assigned quota/tier.
- **Alternative Flow:** Report highlights departments/applications approaching or exceeding their quota, to support proactive quota-planning conversations.
- **Exception Flow:** Requester's scope exceeds their authorization → limited to authorized scope, mirroring FR-127.
- **Business Rules:** This report is a periodic, summarized companion to the real-time usage visibility of FR-060; exact retention of historical utilization data for reporting purposes is **TBD — see Decision Log**.
- **Input:** Report scope, time range.
- **Output:** Resource utilization report.
- **Acceptance Criteria:** A Platform Administrator can generate a report showing each department's resource utilization against its assigned quota for a specified period.
- **Priority:** SHOULD

---

## 5. Summary

| Module | FR Range | Count |
|---|---|---|
| A. Authentication | FR-001–FR-004 | 4 |
| B. User Management | FR-005–FR-007 | 3 |
| C. Organization / Department | FR-008–FR-010 | 3 |
| D. Application Registration | FR-011–FR-014 | 4 |
| E. Application Ownership | FR-015–FR-018 | 4 |
| F. Stack Management | FR-019–FR-022 | 4 |
| G. Deployment Configuration | FR-023–FR-028 | 6 |
| H. Deployment Validation | FR-029–FR-034 | 6 |
| I. Build Management | FR-035–FR-038 | 4 |
| J. Deployment Management | FR-039–FR-044 | 6 |
| K. Application Lifecycle | FR-045–FR-050 | 6 |
| L. Scale-to-Zero | FR-051–FR-056 | 6 |
| M. Resource Management | FR-057–FR-060 | 4 |
| N. Database Management | FR-061–FR-065 | 5 |
| O. Secret Management | FR-066–FR-071 | 6 |
| P. Domain Management | FR-072–FR-075 | 4 |
| Q. Networking | FR-076–FR-081 | 6 |
| R. Health Check | FR-082–FR-085 | 4 |
| S. Logging | FR-086–FR-089 | 4 |
| T. Monitoring | FR-090–FR-093 | 4 |
| U. Version Management | FR-094–FR-097 | 4 |
| V. Rollback | FR-098–FR-102 | 5 |
| W. Audit Log | FR-103–FR-106 | 4 |
| X. Notification | FR-107–FR-109 | 3 |
| Y. MCP Integration | FR-110–FR-115 | 6 |
| Z. Claude Code / AI Agent Integration | FR-116–FR-121 | 6 |
| AA. Administration | FR-122–FR-126 | 5 |
| AB. Reporting | FR-127–FR-129 | 3 |
| **Total** | **FR-001–FR-129** | **129** |

