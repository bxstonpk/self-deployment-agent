# 17 — Decision Log ("Decision Required" Register)

## Document Control

| Field | Value |
|---|---|
| Document ID | DOC-17 |
| Document Name | Decision Log |
| Version | 0.1 (Draft) |
| Status | Draft — Pending Review |
| Date | 2026-08-28 |
| Prepared By | Business Analysis / Risk Management (admin@sti-th.com) |
| Project | Company AI Application Deployment Platform |
| Related Documents | 01_BRD.md, 09_SDLC.md, 10_System_Architecture.md, 11_Security_Requirements.md, 12_Data_Requirements.md, 16_Risk_Register.md |

---

## 1. Purpose & Scope

This document lists every point in the documentation baseline where a **real business decision** is required from Management, IT, Security, or another accountable role — decisions this documentation set deliberately did **not** invent, per the project's explicit instruction not to silently assume unresolved business decisions.

Each item is a **question**, not a specification. Where a defensible recommendation exists, it is offered — but the item remains **Open** until a named Decision Owner formally accepts, modifies, or rejects it. Downstream documents (Technical Design, System Architecture implementation, Security controls, Data model) that currently mark a value as `TBD` should trace back to the corresponding `DEC-xxx` here.

Decision IDs (`DEC-001`, `DEC-002`, …) are numbered sequentially and are owned exclusively by this document.

## 2. How to Use This Log

- **Status** starts at `Open` for every item and should be updated to `Decided`, `Deferred`, or `Superseded` once resolved, with the resolution recorded (a future revision of this document, or a linked decision record, should capture the final answer — this baseline captures the *question*, not a fabricated answer).
- **Target Phase** indicates the latest SDLC phase (per 09_SDLC.md) by which the decision must be closed to avoid blocking or being retrofitted at extra cost.
- Items are grouped by domain for readability; grouping does not imply priority — see the summary index below for a flat, sequential view.

## 3. Decision Index

| DEC ID | Group | Topic | Decision Owner | Target Phase | Status |
|---|---|---|---|---|---|
| DEC-001 | Identity & Access | Identity Provider / SSO integration | IT Administrator + Security Administrator | Before Technical Design | Open |
| DEC-002 | Identity & Access | RBAC/permission model source of truth | Platform Administrator + Security Administrator | Before Technical Design | Open |
| DEC-003 | Identity & Access | MCP/AI-agent authentication mechanism | Security Administrator + Platform Administrator | Before Technical Design | Open |
| DEC-004 | Infrastructure & Hosting | Final infrastructure implementation choice | Platform Administrator + IT Administrator (sign-off: Management) | Before Solution Architecture sign-off | **Decided** |
| DEC-005 | Infrastructure & Hosting | Container registry & image vulnerability scanning tooling | IT Administrator + Security Administrator | Before Technical Design | Open |
| DEC-006 | Infrastructure & Hosting | Secret management backend | Security Administrator + IT Administrator | Before Technical Design | Open |
| DEC-007 | Infrastructure & Hosting | Domain naming, DNS zone ownership, TLS certificate authority | IT Administrator | Before Technical Design | Open |
| DEC-008 | Infrastructure & Hosting | Environment topology beyond dev/staging/production | Platform Administrator | Before Solution Architecture sign-off | Open |
| DEC-009 | Infrastructure & Hosting | Multi-region / cross-datacenter high availability requirement | IT Administrator + Management / Auditor | Before Solution Architecture sign-off | Open |
| DEC-010 | Compliance & Data | Applicable regulatory/compliance framework(s) and audit-log retention | Management / Auditor + Security Administrator | Before Solution Architecture sign-off | Open |
| DEC-011 | Compliance & Data | Data residency / hosting location constraints | IT Administrator + Management / Auditor | Before Solution Architecture sign-off | Open |
| DEC-012 | Compliance & Data | Data classification policy for platform-hosted apps | Security Administrator + Management / Auditor | Before Production Go-Live | Open |
| DEC-013 | Compliance & Data | Whether public-internet ("external") visibility is permitted in v1 | Security Administrator + Platform Administrator | Before Solution Architecture sign-off | Open |
| DEC-014 | Financial / Quota | Resource quota tiers and numeric limits (small/medium/large) | Platform Administrator + IT Administrator | Before Technical Design | Open |
| DEC-015 | Financial / Quota | Budget/cost ceiling for control-plane infra and tooling/licensing | Management / Auditor | Before Solution Architecture sign-off | Open |
| DEC-016 | Financial / Quota | Department/cost-center chargeback or showback model | Management / Auditor | Before Production Go-Live | Open |
| DEC-017 | Process & Governance | Production deployment approval workflow and tooling | Platform Administrator + Security Administrator | Before Technical Design | Open |
| DEC-018 | Process & Governance | Notification channels to integrate | IT Administrator + Platform Administrator | Before Development | Open |
| DEC-019 | Process & Governance | Governance process for supported-stack changes | Platform Administrator + IT Administrator | Before Production Go-Live | Open |
| DEC-020 | Process & Governance | Application ownership transfer process on employee departure | Application Owner + Platform Administrator | Before Production Go-Live | Open |
| DEC-021 | Process & Governance | Human-in-the-loop confirmation policy for AI-initiated deployments | Platform Administrator + Security Administrator | Before UX/DX Design | Open |
| DEC-022 | Process & Governance | MVP pilot scope (participating departments/app types) | Platform Administrator + Management / Auditor | Before UAT | Open |
| DEC-023 | Operational | Scale-to-zero idle timeout default and configurability | Platform Administrator | Before Technical Design | Open |
| DEC-024 | Operational | Availability/SLA targets — control plane vs. hosted apps | Platform Administrator + Management / Auditor | Before Production Go-Live | Open |
| DEC-025 | Operational | RPO/RTO targets and backup retention period for DR | IT Administrator + Application Owner | Before Production Go-Live | Open |
| DEC-026 | Operational | Platform support model / on-call ownership | IT Administrator + Platform Administrator | Before Production Go-Live | Open |

---

## 4. Identity & Access

#### DEC-001 — Identity Provider / SSO Integration

| Field | Detail |
|---|---|
| Topic | Identity Provider / SSO integration |
| Question | Which enterprise Identity Provider / SSO system should the platform integrate with for employee, IT, and service authentication — the company's existing corporate IdP (e.g., Azure AD/Entra ID, Okta, Google Workspace, on-prem AD/LDAP), or a new/dedicated instance? |
| Why It Matters | Authentication is the foundation of every other control (RBAC, application ownership, audit trail). Building against the wrong or no IdP delays IAM module design and risks significant rework; it also determines whether SSO/MFA can be inherited from existing corporate identity rather than rebuilt from scratch. |
| Options Considered | (a) Integrate with the company's existing corporate IdP via SAML/OIDC; (b) stand up a dedicated IdP instance for the platform; (c) federate multiple IdPs (corporate AD for employees + a separate issuer for machine/MCP service accounts). |
| Recommendation | (a) — integrate with the existing corporate IdP via OIDC/SAML to avoid duplicating identity and inherit existing MFA/lifecycle management. Pending confirmation of which IdP the company currently operates. |
| Decision Owner | IT Administrator + Security Administrator |
| Status | Open |
| Target Phase | Before Technical Design |

#### DEC-002 — RBAC/Permission Model Source of Truth

| Field | Detail |
|---|---|
| Topic | RBAC/permission model source of truth |
| Question | Should role/permission assignments (department, application ownership, environment permissions) be sourced from IdP groups/claims, or maintained natively within the platform's own IAM/Application Registry module? |
| Why It Matters | Determines the data model for the User/Department/Role/Permission entities and whether the platform must build its own role-administration UI or can delegate to existing corporate group management. |
| Options Considered | (a) IdP-group-driven (platform maps IdP groups to platform roles); (b) platform-native RBAC with periodic IdP sync for identity only; (c) hybrid — coarse roles from IdP, fine-grained app-level permissions native to the platform. |
| Recommendation | (c) hybrid approach, pending confirmation of the chosen IdP's group/claims capabilities under DEC-001. |
| Decision Owner | Platform Administrator + Security Administrator |
| Status | Open |
| Target Phase | Before Technical Design |

#### DEC-003 — MCP/AI-Agent Authentication Mechanism

| Field | Detail |
|---|---|
| Topic | MCP/AI-agent authentication mechanism |
| Question | How does Claude Code, acting on behalf of a logged-in employee, authenticate to the Company Deployment MCP / Platform API — a short-lived delegated OAuth token tied to the employee's identity, a personal API key issued per employee, or a broker/service-account pattern? |
| Why It Matters | This is the actual security boundary between "the employee's intent" and "the AI agent's action," directly implementing the architectural principle that the platform must never trust the AI agent as a security boundary. The mechanism chosen determines how strongly actions can be attributed to a specific human for audit and how quickly access can be revoked. |
| Options Considered | (a) Delegated OAuth2 token scoped to the employee's session; (b) long-lived personal API key stored locally by Claude Code; (c) broker service issuing short-lived, just-in-time credentials per MCP call. |
| Recommendation | (a) or (c) — short-lived, employee-attributable, revocable credentials. Long-lived personal API keys (b) are discouraged as higher risk. Final mechanism to be finalized jointly with 11_Security_Requirements.md. |
| Decision Owner | Security Administrator + Platform Administrator |
| Status | Open |
| Target Phase | Before Technical Design |

## 5. Infrastructure & Hosting

#### DEC-004 — Final Infrastructure Implementation Choice

| Field | Detail |
|---|---|
| Topic | Final infrastructure implementation choice |
| Question | Which of the four candidate implementations — Docker + Docker Compose, K3s + Kubernetes, K3s + Knative, or a Managed Container Platform — will the platform standardize on for v1? |
| Why It Matters | This choice cascades into virtually every downstream technical decision: the scale-to-zero mechanism, deployment lifecycle automation, staffing/skills needs (see RISK-008), cost model, and disaster-recovery approach. Changing it later is expensive (see RISK-006). |
| Options Considered | Docker + Docker Compose; K3s + Kubernetes; K3s + Knative; Managed Container Platform. A full weighted comparison (scale-to-zero support, operational complexity, cost, security, self-hosting, maintainability, developer experience, AI deployment compatibility, future scalability, IT workload) is provided in 10_System_Architecture.md. |
| Recommendation | Defer to the reasoned recommendation in 10_System_Architecture.md. This entry exists precisely because final sign-off is a Platform Administrator/IT/Management decision, not something the documentation baseline can unilaterally finalize. |
| Decision Owner | Platform Administrator + IT Administrator (final sign-off: Management) |
| Status | **Decided (2026-08-28)** |
| Resolution | **Option C — K3s + Knative.** Self-hosted rather than the primary Managed Container Platform recommendation in 10_System_Architecture.md; chosen to keep the platform fully self-hosted while still natively satisfying the scale-to-zero requirement (Knative) on a lighter-weight Kubernetes distribution (K3s) than full upstream Kubernetes. This narrows DEC-005 (registry/scanning), DEC-006 (secret backend), DEC-007 (DNS/TLS), DEC-008 (environment topology), and DEC-009 (multi-region HA) to Kubernetes-native tooling options — those entries should be revisited with this constraint in mind. |
| Target Phase | Before Solution Architecture sign-off / before Technical Design |

#### DEC-005 — Container Registry & Image Vulnerability Scanning Tooling

| Field | Detail |
|---|---|
| Topic | Container registry & image vulnerability scanning tooling |
| Question | Which container registry (e.g., internal Harbor, cloud-provider registry, GitHub Container Registry) and image-scanning tool will the platform standardize on to satisfy the mandatory image-security-scanning requirement? |
| Why It Matters | Required before the Build Engine and Deployment Controller can be technically designed; affects cost, self-hosting posture, and how quickly vulnerable images can be blocked from deployment. |
| Options Considered | (a) Self-hosted registry + open-source scanner (e.g., Trivy); (b) managed cloud registry with built-in scanning; (c) an existing company-standard registry, if one already exists. |
| Recommendation | Confirm whether the company already operates a container registry/scanner before evaluating new tooling; reuse existing investment if so. |
| Decision Owner | IT Administrator + Security Administrator |
| Status | Open |
| Target Phase | Before Technical Design |

#### DEC-006 — Secret Management Backend

| Field | Detail |
|---|---|
| Topic | Secret management backend |
| Question | What secret-management system will store and broker application secrets, database credentials, and platform service credentials — e.g., HashiCorp Vault, a cloud KMS/Secrets Manager, or Kubernetes-native sealed secrets? |
| Why It Matters | Directly implements the "no production credentials in source code" and "no cross-app secret access" requirements; determines how the Secret Manager module integrates with the Deployment Controller. |
| Options Considered | (a) HashiCorp Vault (self-hosted); (b) cloud-native secrets manager; (c) platform-native secret store scoped to the chosen container platform. |
| Recommendation | No default recommended without knowing whether the company already standardizes on a secrets tool; flag as a joint decision with the Security Administrator once DEC-004 narrows the infrastructure options. |
| Decision Owner | Security Administrator + IT Administrator |
| Status | Open |
| Target Phase | Before Technical Design |

#### DEC-007 — Domain Naming, DNS Zone Ownership, TLS Certificate Authority

| Field | Detail |
|---|---|
| Topic | Internal domain naming convention, DNS zone ownership, and TLS certificate authority |
| Question | What internal domain/subdomain convention will platform-hosted apps receive (e.g., `app.internal.company.com`), who owns the DNS zone, and will TLS certificates come from a public CA, an internal CA, or a managed certificate service? |
| Why It Matters | Needed to design the Domain Manager module and to deliver on the automatic URL provisioning promised to employees in the TO-BE process. |
| Options Considered | (a) Internal-only DNS zone + internal CA; (b) public DNS subdomain + public CA (e.g., Let's Encrypt) even for internal-visibility apps; (c) split — internal CA for internal-visibility apps, public CA for external-visibility apps. |
| Recommendation | (c), the common enterprise pattern; final DNS zone ownership must be confirmed with IT regardless of which option is chosen. |
| Decision Owner | IT Administrator |
| Status | Open |
| Target Phase | Before Technical Design |

#### DEC-008 — Environment Topology Beyond Dev/Staging/Production

| Field | Detail |
|---|---|
| Topic | Whether additional environments are needed beyond dev/staging/production |
| Question | Does the platform need additional environments (e.g., QA, per-feature ephemeral preview environments, a sandbox/training environment) beyond dev/staging/production, and how do they map to infrastructure (shared cluster with namespace isolation vs. fully separate clusters)? |
| Why It Matters | Affects the Environment Management module's design, cost, and the deployment lifecycle's environment-promotion rules. |
| Options Considered | (a) Dev/staging/production only (minimum viable); (b) add ephemeral per-branch preview environments; (c) add a dedicated sandbox/training environment. |
| Recommendation | (a) for MVP; revisit (b)/(c) in Phase 2 once real adoption data exists. |
| Decision Owner | Platform Administrator |
| Status | Open |
| Target Phase | Before Solution Architecture sign-off |

#### DEC-009 — Multi-Region / Cross-Datacenter High Availability

| Field | Detail |
|---|---|
| Topic | Multi-region / cross-datacenter high availability requirement |
| Question | Must the platform (control plane and/or hosted applications) tolerate the loss of a single data center or region, or is single-site hosting acceptable for v1? |
| Why It Matters | A major cost and complexity driver; directly informs the RPO/RTO targets (DEC-025) and the infrastructure choice (DEC-004). |
| Options Considered | (a) Single-site for v1 with a documented risk acceptance; (b) active/passive DR site; (c) active/active multi-site. |
| Recommendation | (a) for MVP, consistent with the stated objective of minimizing operational complexity, with (b) evaluated for Phase 2 once critical/production-tier applications are identified. |
| Decision Owner | IT Administrator + Management / Auditor |
| Status | Open |
| Target Phase | Before Solution Architecture sign-off |

## 6. Compliance & Data

#### DEC-010 — Applicable Regulatory/Compliance Framework(s) and Audit-Log Retention

| Field | Detail |
|---|---|
| Topic | Applicable regulatory/compliance framework(s) and resulting audit-log retention period |
| Question | Does the company operate under any regulatory or compliance obligations relevant to this platform (e.g., SOC 2, ISO 27001, GDPR or local data-protection law, an industry-specific regulation), and if so, what audit-log retention period do they require? |
| Why It Matters | Drives the Audit module's retention/immutability design, the Data Requirements document, and potentially the infrastructure hosting-location decision (DEC-011). Leaving this unresolved risks costly rework (see RISK-025). |
| Options Considered | (a) No formal external framework applies — internal policy only; (b) one or more named frameworks apply; (c) not yet determined pending legal/compliance review. |
| Recommendation | Treat as genuinely unresolved (c) until Legal/Compliance confirms — do not assume a specific framework. Adopt a conservative placeholder minimum retention (e.g., 1 year) only until confirmed. |
| Decision Owner | Management / Auditor + Security Administrator |
| Status | Open |
| Target Phase | Before Solution Architecture sign-off (blocking for Compliance Requirements finalization) |

#### DEC-011 — Data Residency / Hosting Location Constraints

| Field | Detail |
|---|---|
| Topic | Data residency / hosting location constraints |
| Question | Are there constraints on where platform infrastructure and application data may be physically or geographically hosted — on-premises only, a specific country/region, or a specific cloud provider? |
| Why It Matters | Directly constrains the infrastructure evaluation (particularly the Managed Container Platform option) and the cost model; can eliminate candidate infrastructure options outright. |
| Options Considered | (a) On-premises only; (b) any location within a specified country/jurisdiction; (c) no constraint / cloud-agnostic. |
| Recommendation | Confirm with IT/Legal before finalizing DEC-004, since the answer can materially narrow the infrastructure evaluation. |
| Decision Owner | IT Administrator + Management / Auditor |
| Status | Open |
| Target Phase | Before Solution Architecture sign-off |

#### DEC-012 — Data Classification Policy for Platform-Hosted Apps

| Field | Detail |
|---|---|
| Topic | Data classification policy for platform-hosted apps |
| Question | What classes of data (public, internal, confidential, PII, financial, regulated) are employees permitted to store in applications built and deployed through this self-service platform in v1? |
| Why It Matters | A self-service platform lowers the barrier to standing up a database; without an explicit policy, employees may store sensitive data in apps that lack the corresponding controls (encryption, access review, retention). |
| Options Considered | (a) Internal/non-sensitive data only for v1, expand later under governance; (b) allow confidential data with mandatory classification tagging in deployment.yaml; (c) no restriction (not recommended). |
| Recommendation | (a) for MVP — restrict to internal/non-sensitive data until data-classification tagging and corresponding controls exist. |
| Decision Owner | Security Administrator + Management / Auditor |
| Status | Open |
| Target Phase | Before Production Go-Live |

#### DEC-013 — Public-Internet ("External") Visibility Permitted in v1?

| Field | Detail |
|---|---|
| Topic | Whether `domain.visibility: external` is supported in v1 |
| Question | The deployment.yaml contract includes a `domain.visibility` field (e.g., `internal`); should the platform support `visibility: external` (public internet exposure) at all in v1, or should MVP be restricted to internal-only visibility? |
| Why It Matters | Public exposure multiplies the attack surface and the security controls required (WAF, DDoS protection, public TLS, stricter review); deferring it meaningfully narrows MVP scope and risk. |
| Options Considered | (a) Internal-only for v1, external deferred to Phase 2; (b) support external visibility from v1 with mandatory Security Administrator approval; (c) support external visibility only for specific pre-approved app types. |
| Recommendation | (a) — internal-only for MVP, consistent with the objective of minimizing initial risk and operational complexity. |
| Decision Owner | Security Administrator + Platform Administrator |
| Status | Open |
| Target Phase | Before Solution Architecture sign-off (scopes MVP) |

## 7. Financial / Quota

#### DEC-014 — Resource Quota Tiers and Numeric Limits

| Field | Detail |
|---|---|
| Topic | Exact resource quota tiers/numbers per tier (small/medium/large) |
| Question | What are the exact CPU, memory, storage, and replica-count limits for each `resources.tier` value (`small`/`medium`/`large`) referenced in the deployment.yaml contract? |
| Why It Matters | Required for the Resource Manager module and quota-enforcement logic; without exact numbers, validation and capacity planning cannot be implemented, and cost cannot be forecast (see DEC-015). |
| Options Considered | (a) Adopt provisional numbers benchmarked from typical internal-tool workloads; (b) mirror an existing internal infrastructure standard, if one exists; (c) start conservative and revise after MVP usage data. |
| Recommendation | (c) — publish provisional numbers for MVP, explicitly labeled as subject to revision after the pilot generates real usage data. |
| Decision Owner | Platform Administrator + IT Administrator |
| Status | Open |
| Target Phase | Before Technical Design |

#### DEC-015 — Budget/Cost Ceiling for Control-Plane Infrastructure and Tooling

| Field | Detail |
|---|---|
| Topic | Budget/cost ceiling for control-plane infra and tooling/licensing |
| Question | What is the approved budget ceiling for always-on control-plane infrastructure (API servers, MCP server, database, monitoring stack) and associated tooling/licensing costs, including Claude Code seats for participating employees? |
| Why It Matters | Directly shapes the infrastructure recommendation (a Managed Container Platform has materially different cost dynamics than self-hosted K3s) and determines whether scale-to-zero for the control plane itself is in scope. |
| Options Considered | (a) A fixed annual ceiling set by Finance before architecture is finalized; (b) cost approved incrementally per phase (MVP budget only, Phase 2 reassessed); (c) no ceiling defined yet. |
| Recommendation | (b) — approve MVP-phase budget now, reassess for Phase 2 once real cost data exists, to avoid over-committing before the infrastructure choice (DEC-004) is validated. |
| Decision Owner | Management / Auditor |
| Status | Open |
| Target Phase | Before Solution Architecture sign-off |

#### DEC-016 — Department/Cost-Center Chargeback or Showback Model

| Field | Detail |
|---|---|
| Topic | Department/cost-center chargeback (if any) |
| Question | Should the cost of hosting a department's applications be charged back to that department's budget (chargeback), merely reported for visibility (showback), or absorbed centrally by IT with no cross-charging? |
| Why It Matters | Affects the Reporting module's design and whether resource-tier selection needs cost-approval workflows tied to a department's budget owner. |
| Options Considered | (a) No chargeback — IT absorbs cost centrally (simplest for MVP); (b) showback only (visibility, no billing); (c) full chargeback by cost center. |
| Recommendation | (a) or (b) for MVP to avoid blocking adoption with a billing dependency; revisit (c) once the platform has adoption data to model costs accurately. |
| Decision Owner | Management / Auditor |
| Status | Open |
| Target Phase | Before Production Go-Live |

## 8. Process & Governance

#### DEC-017 — Production Deployment Approval Workflow and Tooling

| Field | Detail |
|---|---|
| Topic | Production-approval workflow/tooling |
| Question | For applications requiring production approval, who has authority to approve (Application Owner, department manager, Platform Administrator, Security Administrator), and through what interface (chat approval, ticketing system, dedicated Administration Portal)? |
| Why It Matters | This is the primary control preventing RISK-012 (production incidents from inadequate gates). An undefined or overly heavy process either recreates the IT bottleneck the platform is meant to eliminate (RISK-003) or fails to prevent unsafe deploys. |
| Options Considered | (a) Application Owner self-approves with Platform Administrator spot-audits; (b) mandatory two-party approval (Application Owner + Platform/Security Administrator) via the Administration Portal; (c) ticket-based approval integrated with an existing ITSM tool. |
| Recommendation | (b) for production; auto-approval (a-style) remains acceptable for dev, per the stated business rule. Final workflow to be confirmed jointly with IT/Security. |
| Decision Owner | Platform Administrator + Security Administrator |
| Status | Open |
| Target Phase | Before Technical Design |

#### DEC-018 — Notification Channels to Integrate

| Field | Detail |
|---|---|
| Topic | Notification channels (email/Slack/Teams/etc.) |
| Question | Which notification channels should the platform integrate for deployment status, health alerts, and approval requests — email, Slack, Microsoft Teams, an internal ticketing system, or a combination? |
| Why It Matters | Needed to scope the Notification module and any third-party integration work/credentials required. |
| Options Considered | (a) Email only for MVP (lowest integration cost); (b) email plus whichever chat tool the company already standardizes on (Slack or Teams); (c) full multi-channel with user-configurable preferences. |
| Recommendation | (b) — reuse the company's existing chat platform if one is already standard, to maximize adoption with minimal new integration work. |
| Decision Owner | IT Administrator + Platform Administrator |
| Status | Open |
| Target Phase | Before Development |

#### DEC-019 — Governance Process for Supported-Stack Changes

| Field | Detail |
|---|---|
| Topic | Governance process for adding/removing supported technologies |
| Question | What is the formal process and approval authority for adding, deprecating, or removing a technology from the supported stack list (e.g., adding Django, deprecating an old Node.js version)? |
| Why It Matters | Directly mitigates RISK-004 (scope creep). Without a defined process, stack growth is ad hoc and undermines the "controlled list" principle central to the platform's value proposition. |
| Options Considered | (a) A standing Platform Governance board (Platform Administrator + IT Administrator + Security Administrator) reviews requests on a fixed cadence; (b) any Platform Administrator can add a stack unilaterally (not recommended); (c) stack changes require full change-advisory-board sign-off. |
| Recommendation | (a) — lightweight standing governance review, balancing agility with control. |
| Decision Owner | Platform Administrator + IT Administrator |
| Status | Open |
| Target Phase | Before Production Go-Live |

#### DEC-020 — Application Ownership Transfer Process on Employee Departure

| Field | Detail |
|---|---|
| Topic | What happens to an application when its owning employee leaves the company |
| Question | What process governs reassignment (or archival/deletion) of an application when its owning employee leaves the company or changes roles — automatic reassignment to a department manager, a mandatory offboarding checklist step, or a grace-period auto-suspend? |
| Why It Matters | Without a defined process, departed employees' applications become orphaned — no one able to approve changes, respond to incidents, or authorize decommissioning (see RISK-011). |
| Options Considered | (a) HR offboarding process triggers automatic ownership reassignment to the employee's manager/department, with a review deadline; (b) application auto-suspends after N days with no reassignment; (c) manual case-by-case handling by the Platform Administrator (not scalable). |
| Recommendation | (a) combined with (b) as a safety net — integrate with the company's existing HR offboarding trigger if technically feasible. |
| Decision Owner | Application Owner (department) + Platform Administrator |
| Status | Open |
| Target Phase | Before Production Go-Live |

#### DEC-021 — Human-in-the-Loop Confirmation Policy for AI-Initiated Deployments

| Field | Detail |
|---|---|
| Topic | Human-in-the-loop confirmation for AI-agent-initiated deployments |
| Question | Does every AI-agent-initiated deployment action (including validated dev-environment deploys) require an explicit, visible employee confirmation step before execution, or can fully validated dev deploys proceed autonomously within policy? |
| Why It Matters | Balances the self-service productivity goal against the principle that the AI agent must never be the security boundary; sets user-experience expectations for the Company Deployment Skill. |
| Options Considered | (a) Explicit confirmation required for every deploy regardless of environment; (b) dev deploys autonomous once validated, production always requires explicit confirmation plus approval; (c) fully autonomous for both (not recommended, given the "never trust the AI agent as a security boundary" principle). |
| Recommendation | (b) — matches the stated business rule that dev may auto-deploy while production requires approval. |
| Decision Owner | Platform Administrator + Security Administrator |
| Status | Open |
| Target Phase | Before UX/DX Design |

#### DEC-022 — MVP Pilot Scope

| Field | Detail |
|---|---|
| Topic | MVP pilot scope (participating departments/app types) |
| Question | Which department(s) and application types will pilot the platform first, and what criteria determine readiness to expand to additional departments? |
| Why It Matters | Mitigates RISK-029 (pilot hiding complex-app gaps) by deliberately choosing pilot participants and exit criteria rather than defaulting to whichever team asks first. |
| Options Considered | (a) A small, deliberately mixed pilot cohort — e.g., the example `overtime` HR app plus one moderately complex app (multi-service, database-backed); (b) open pilot to any interested department (higher variance, faster signal); (c) IT-led internal tools only for the first pilot cohort. |
| Recommendation | (a) — a small, deliberately mixed pilot cohort to surface real gaps early rather than only easy cases. |
| Decision Owner | Platform Administrator + Management / Auditor |
| Status | Open |
| Target Phase | Before UAT |

## 9. Operational

#### DEC-023 — Scale-to-Zero Idle Timeout Default and Configurability

| Field | Detail |
|---|---|
| Topic | Exact scale-to-zero idle timeout default and whether it's configurable per app or platform-wide |
| Question | What is the default idle-timeout period before a stateless workload scales to zero, and can individual applications override the platform default via deployment.yaml, or is it fixed platform-wide? |
| Why It Matters | Directly affects cold-start user experience (see RISK-018) and infrastructure cost savings; too short a timeout causes constant cold starts, too long undermines the cost benefit that motivates scale-to-zero in the first place. |
| Options Considered | (a) Fixed platform-wide default (e.g., 15 minutes) with no per-app override; (b) platform-wide default with a per-app override within a bounded range; (c) fully per-app configurable with no platform default. |
| Recommendation | (b) — a sensible default with a bounded override range gives flexibility without letting every app opt out and defeat the purpose. |
| Decision Owner | Platform Administrator |
| Status | Open |
| Target Phase | Before Technical Design |

#### DEC-024 — Availability/SLA Targets: Control Plane vs. Hosted Apps

| Field | Detail |
|---|---|
| Topic | Exact availability/SLA targets for the platform control plane vs. individual hosted apps |
| Question | What are the target availability/SLA percentages for (a) the platform control plane (MCP, Platform API, Deployment Controller) and (b) individual hosted applications, and do they differ by resource tier or environment? |
| Why It Matters | Needed for the Non-Functional Requirements document and to size redundancy/HA investment appropriately (ties to DEC-009 and RISK-016, the control-plane single-point-of-failure risk). |
| Options Considered | (a) A single company-wide target for both control plane and apps (simplest); (b) tiered targets — higher for the control plane than for individual dev/small-tier apps; (c) no formal SLA for MVP, targets set after baseline monitoring data exists. |
| Recommendation | (c) for MVP — publish monitoring dashboards first, commit to formal SLA targets once baseline data exists, to avoid committing to unrealistic numbers. |
| Decision Owner | Platform Administrator + Management / Auditor |
| Status | Open |
| Target Phase | Before Production Go-Live |

#### DEC-025 — RPO/RTO Targets and Backup Retention for Disaster Recovery

| Field | Detail |
|---|---|
| Topic | RPO/RTO targets for backup/disaster recovery, and backup retention period |
| Question | What Recovery Point Objective and Recovery Time Objective apply to platform-hosted application databases and to the platform's own control-plane state, and how long are backups retained? |
| Why It Matters | Drives the Backup and Recovery Requirements and the Database Manager module's backup automation design; also affects the multi-region decision (DEC-009). |
| Options Considered | (a) Uniform RPO/RTO and retention for all apps regardless of tier; (b) tiered targets by `resources.tier` or by a criticality flag; (c) no formal target for MVP — best-effort daily backup only. |
| Recommendation | (c) for MVP with a documented best-effort daily backup and 30-day retention as a provisional placeholder, moving to (b) once application criticality classification exists. |
| Decision Owner | IT Administrator + Application Owner |
| Status | Open |
| Target Phase | Before Production Go-Live |

#### DEC-026 — Platform Support Model / On-Call Ownership

| Field | Detail |
|---|---|
| Topic | Platform support model and on-call ownership |
| Question | Who provides support for the platform itself (not individual hosted apps) outside business hours, and what are the committed response times for a control-plane outage? |
| Why It Matters | A platform that automates deployment still needs an accountable operator when the automation itself breaks; without this, RISK-016 (control-plane single point of failure) has no defined incident-response path. |
| Options Considered | (a) Business-hours-only support for MVP, with documented risk acceptance for after-hours outages; (b) formal on-call rotation within the Platform Administrator team; (c) outsourced/managed support (most relevant if a Managed Container Platform is chosen in DEC-004). |
| Recommendation | (a) for MVP given team size and cost constraints, with (b) revisited once the platform hosts production-tier applications. |
| Decision Owner | IT Administrator + Platform Administrator |
| Status | Open |
| Target Phase | Before Production Go-Live |

---

## 10. Summary: Decisions That Block Each Major Phase Gate

| Target Phase | Blocking Decisions |
|---|---|
| Before UX/DX Design | DEC-021 |
| Before Solution Architecture sign-off | DEC-004, DEC-008, DEC-009, DEC-010, DEC-011, DEC-013, DEC-015 |
| Before Technical Design | DEC-001, DEC-002, DEC-003, DEC-005, DEC-006, DEC-007, DEC-014, DEC-017, DEC-023 |
| Before Development | DEC-018 |
| Before UAT | DEC-022 |
| Before Production Go-Live | DEC-012, DEC-016, DEC-019, DEC-020, DEC-024, DEC-025, DEC-026 |

This log should be treated as a **standing agenda** for Management/IT/Security review meetings during Discovery and Requirements Analysis. No item should still be `Open` when its Target Phase's gate review occurs; an item still open at its gate should either block the gate or be explicitly risk-accepted by its Decision Owner with the acceptance recorded in this document.

---

## 11. Confirmed Technology Stack (Resolved 2026-08-28)

Following the resolution of **DEC-004** (Option C — K3s + Knative, self-hosted), the following implementation-language decisions were confirmed. These are Technical Design–phase decisions (`09_SDLC.md`), recorded here for traceability rather than as new `DEC-xxx` entries since they follow directly from DEC-004 and do not require further Management/IT sign-off.

| Component | Technology | Rationale |
|---|---|---|
| Company Deployment Skill | Markdown | Instruction set read directly by Claude Code — not executable code (`08_Company_Deployment_Skill.md`) |
| Company Deployment MCP (MOD-16) | Python | Fast to build; strong fit for AI/tool-orchestration workloads and the MCP ecosystem's reference tooling |
| Platform API (MOD-17) | Go | Infrastructure-facing control-plane service; concurrency and reliability under many simultaneous deployment requests |
| Deployment Controller (MOD-06) | Go | Cloud-native; aligns with the Kubernetes/Knative controller ecosystem (client-go, controller-runtime patterns) selected in DEC-004 |
| Frontend Admin Portal (MOD-18) | React + TypeScript | Matches the platform's own supported frontend stack; existing team proficiency |
| Database | PostgreSQL | Already the platform's designated supported database (`12_Data_Requirements.md`) — reused for the platform's own control-plane data, not only hosted applications |
| Container format | Docker | Standard image format; compatible with K3s |
| Orchestration | K3s | Lighter-weight than full upstream Kubernetes; consistent with the "minimize IT operational workload" objective (`01_BRD.md` §6) |
| Scale-to-zero | Knative | Purpose-built serverless/scale-to-zero layer for Kubernetes; directly satisfies `NFR-004`/`NFR-008` (`03_Non_Functional_Requirements.md`) |

**Explicitly confirmed constraints this stack must satisfy** (per direct instruction):
- **Self-hosted on Docker/K3s, not a public cloud–managed platform.** This is stricter than 10_System_Architecture.md's primary recommendation (Option D, Managed Container Platform) — the platform will run on the self-hosted fallback (Option C) instead. `10_System_Architecture.md` §5 should be read with this override in mind.
- **Backend replaceable without impacting the frontend.** The Platform API (Go), MCP Server (Python), and Deployment Controller (Go) may change implementation independently of the React/TS Admin Portal, as long as the versioned Business API contract (`13_API_Requirements.md`) is preserved. Formalized as `NFR-051` in `03_Non_Functional_Requirements.md` §3.13.

Because Platform API and Deployment Controller (Go) sit behind the same Business API contract consumed by an MCP Server (Python) and a Frontend Admin Portal (React/TS), the Business API's language-agnostic contract (REST+JSON, per the recommendation in `13_API_Requirements.md`) becomes load-bearing across three different language runtimes — its versioning discipline (already required by `13_API_Requirements.md` §7) is now a harder constraint, not just good practice.

New decisions identified during later phases should be appended as `DEC-027`, `DEC-028`, … — existing IDs must never be renumbered or reused.
