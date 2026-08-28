# 16 — Risk Register

## Document Control

| Field | Value |
|---|---|
| Document ID | DOC-16 |
| Document Name | Risk Register |
| Version | 0.1 (Draft) |
| Status | Draft — Pending Review |
| Date | 2026-08-28 |
| Prepared By | Business Analysis / Risk Management (admin@sti-th.com) |
| Project | Company AI Application Deployment Platform |
| Related Documents | 01_BRD.md (§42 Risks / §43 Risk Mitigation), 09_SDLC.md, 10_System_Architecture.md, 11_Security_Requirements.md, 17_Decision_Log.md |

---

## 1. Purpose & Scope

This document is the **business, project, operational, adoption, and organizational** risk register for the Company AI Application Deployment Platform initiative. It answers the question: *"What could cause this initiative, or the platform once live, to fail, stall, cost more than expected, or create unmanaged operational burden?"*

This register is **not** a security attack/abuse threat model. Attack-scenario risks such as compromised AI agent sessions, container escape, cross-application secret/database access, supply-chain attacks, and privilege escalation are enumerated and rated as `THREAT-xxx` items in **11_Security_Requirements.md**. Where a business risk in this register is directly caused by an unmitigated security threat, it is cross-referenced rather than re-derived (see RISK-013).

Risk IDs (`RISK-001`, `RISK-002`, …) are numbered sequentially and are owned exclusively by this document. No other sibling document should mint a `RISK-xxx` ID.

## 2. Risk Rating Methodology

Each risk is rated for **Impact** (severity if it occurs) and **Likelihood** (probability of occurring in the current, largely unmitigated state of the initiative — i.e. this is an *inherent* rating, not a residual rating after full mitigation). The combination determines the **Overall Risk Rating** per the matrix below. Ratings should be re-assessed at each SDLC phase gate as mitigations are implemented.

**Impact scale**

| Level | Definition |
|---|---|
| Low | Minor inconvenience; absorbed within normal team capacity; no schedule/budget/reputation effect. |
| Medium | Noticeable schedule slip, rework, or cost; workaround exists; limited to one team/department. |
| High | Material delay, cost, or capability gap; affects the core business objective (reducing IT workload / enabling self-service); may require executive attention. |
| Critical | Threatens the viability of the initiative, causes a business-impacting production incident, or creates irreversible cost/rework. |

**Likelihood scale**

| Level | Definition |
|---|---|
| Low | Unlikely under current plans; would require multiple independent failures. |
| Medium | Plausible; has occurred in comparable initiatives or is a known weak point in the current plan. |
| High | Expected to occur absent active mitigation; already observed in the current AS-IS state. |

**Rating matrix (Impact × Likelihood → Overall Risk Rating)**

| Impact \ Likelihood | Low | Medium | High |
|---|---|---|---|
| **Critical** | Medium | High | Critical |
| **High** | Medium | High | High |
| **Medium** | Low | Medium | High |
| **Low** | Low | Low | Medium |

## 3. Top 10 Risks Requiring Near-Term Attention

Ranked by Overall Risk Rating and by how early in the SDLC each risk must be addressed to avoid becoming locked-in.

| Rank | Risk ID | Risk | Rating | Why It's Urgent Now |
|---|---|---|---|---|
| 1 | RISK-012 | Production incident from inadequate self-service deploy gates | **Critical** | The production-approval workflow (see 17_Decision_Log.md DEC-017) must be designed before any production go-live; this is the single highest-impact unmitigated risk. |
| 2 | RISK-006 | Infrastructure technology choice proves a poor operational fit | High | The infrastructure decision (DEC-004) is close to being locked in via 10_System_Architecture.md; a wrong choice is expensive to reverse later. |
| 3 | RISK-016 | Platform API / MCP is a single point of failure | High | Every deployment for every application depends on this component; its availability target (DEC-024) must be set before Technical Design. |
| 4 | RISK-001 | Shadow IT persists during and after rollout | High | Already true today per the AS-IS process; grows more entrenched the longer MVP launch is delayed. |
| 5 | RISK-003 | IT/Platform team becomes a new approval bottleneck | High | If not designed out now, it directly undermines the core business objective of reducing IT workload. |
| 6 | RISK-008 | Insufficient staffing/skills to operate Kubernetes-family infrastructure | High | Feeds directly into the infrastructure decision (DEC-004); must be assessed before that decision, not after. |
| 7 | RISK-002 | Low employee adoption of the standardized deployment flow | High | Adoption behavior is easiest (and cheapest) to shape during MVP pilot design, before habits form. |
| 8 | RISK-005 | Underestimated effort to build the MCP / Platform API layer | High | Directly affects MVP timeline and budget commitments being made now. |
| 9 | RISK-023 | AI-generated deployment.yaml misconfiguration causes outage or exposure | High | Directly tied to how the Company Deployment Skill and Platform API validation are specified during Technical Design. |
| 10 | RISK-011 | Unclear application ownership when an employee departs | High | Requires an HR-integrated process decision (DEC-020) that should be settled before Production Go-Live, not discovered during an incident. |

## 4. Risk Heat Map (Impact × Likelihood — Count of Risks)

| Impact \ Likelihood | Low | Medium | High | Row Total |
|---|---|---|---|---|
| **Critical** | 0 | 2 | 1 | 3 |
| **High** | 2 | 6 | 1 | 9 |
| **Medium** | 3 | 9 | 3 | 15 |
| **Low** | 0 | 3 | 0 | 3 |
| **Column Total** | 5 | 20 | 5 | **30** |

**Overall Rating distribution:** Critical = 1 · High = 12 · Medium = 11 · Low = 6 (total 30 risks).

## 5. Risk Register Summary Index

| Risk ID | Category | Risk | Impact | Likelihood | Rating | Owner | Status |
|---|---|---|---|---|---|---|---|
| RISK-001 | Adoption | Shadow IT persists during and after platform rollout | High | High | High | IT Administrator | Open |
| RISK-002 | Adoption | Low employee adoption of the standardized deployment flow | High | Medium | High | Platform Administrator | Open |
| RISK-003 | Operational | IT/Platform team becomes a new approval bottleneck | High | Medium | High | Platform Administrator | Open |
| RISK-004 | Project | Scope creep in the supported technology stack | Medium | Medium | Medium | Platform Administrator | Open |
| RISK-005 | Project | Underestimated effort to build the MCP / Platform API layer | High | Medium | High | Platform Administrator | Open |
| RISK-006 | Technical | Infrastructure technology choice proves a poor operational fit | Critical | Medium | High | IT Administrator | Open |
| RISK-007 | Business | Cost overrun on always-on control-plane infrastructure | Medium | Medium | Medium | Management / Auditor | Open |
| RISK-008 | Organizational | Insufficient staffing/skills to operate Kubernetes-family infrastructure | High | Medium | High | IT Administrator | Open |
| RISK-009 | Technical | Claude Code / AI agent behavior change breaks Skill assumptions | Medium | Low | Low | Platform Administrator | Open |
| RISK-010 | Business | Vendor / tooling lock-in | Medium | Medium | Medium | IT Administrator | Open |
| RISK-011 | Organizational | Unclear application ownership when an employee departs | Medium | High | High | Platform Administrator | Open |
| RISK-012 | Operational | Production incident from inadequate self-service deploy gates | Critical | High | **Critical** | Platform Administrator | Open |
| RISK-013 | Technical | Security control gaps / policy bypass (see 11_Security_Requirements.md) | High | Low | Medium | Security Administrator | Open |
| RISK-014 | Operational | Resource quota misconfiguration causes noisy-neighbor exhaustion | Medium | Medium | Medium | Platform Administrator | Open |
| RISK-015 | Technical | Uncontrolled per-application database sprawl | Medium | Medium | Medium | IT Administrator | Open |
| RISK-016 | Technical | Platform API / MCP is a single point of failure | Critical | Medium | High | Platform Administrator | Open |
| RISK-017 | Business | Incomplete audit trail undermines compliance/investigation capability | Medium | Low | Low | Security Administrator | Open |
| RISK-018 | Adoption | Cold-start latency drives teams to opt out of scale-to-zero | Medium | High | High | Platform Administrator | Open |
| RISK-019 | Organizational | Production approval workflow becomes a rubber stamp | Medium | Medium | Medium | Management / Auditor | Open |
| RISK-020 | Project | deployment.yaml schema drift versus the Company Deployment Skill | Low | Medium | Low | Platform Administrator | Open |
| RISK-021 | Organizational | Key-person / bus-factor risk on a small platform team | Medium | Medium | Medium | Platform Administrator | Open |
| RISK-022 | Business | Budget not secured beyond MVP/pilot phase | High | Low | Medium | Management / Auditor | Open |
| RISK-023 | Operational | AI-generated deployment.yaml misconfiguration causes outage or exposure | High | Medium | High | Application Owner | Open |
| RISK-024 | Organizational | Competing department priorities stall the platform roadmap | Low | Medium | Low | Management / Auditor | Open |
| RISK-025 | Business | Regulatory/compliance scope left undefined | High | Medium | High | Security Administrator | Open |
| RISK-026 | Operational | Container registry / image storage growth unmanaged | Low | Medium | Low | IT Administrator | Open |
| RISK-027 | Technical | Legacy shadow-IT applications incompatible with the supported stack | Medium | High | High | Platform Administrator | Open |
| RISK-028 | Organizational | IT staff resistance to automation of their manual role | Medium | Low | Low | Management / Auditor | Open |
| RISK-029 | Project | MVP pilot uses only simple apps, hiding complex-app gaps | Medium | Medium | Medium | Platform Administrator | Open |
| RISK-030 | Operational | Monitoring/alerting gaps cause silent failures in scaled-to-zero apps | Medium | Medium | Medium | Platform Administrator | Open |

## 6. Detailed Risk Register

#### RISK-001 — Shadow IT Persists During and After Platform Rollout

| Field | Detail |
|---|---|
| Category | Adoption |
| Description | Employees continue building and running applications outside the platform — on personal cloud accounts, unmanaged VMs, or ad hoc servers — using technologies the company cannot support, secure, or monitor, because the platform is not yet available or does not yet cover their use case. |
| Trigger / Cause | Platform launches with limited supported-stack coverage or slow onboarding; employees under delivery pressure bypass the new process; no enforcement mechanism exists to detect ungoverned deployments. |
| Impact | High |
| Likelihood | High |
| Overall Risk Rating | **High** |
| Mitigation | Launch MVP with the most commonly requested stack combinations to minimize the incentive to go around it; provide a clear, fast path for employees to request new stack support (see DEC-019 in 17_Decision_Log.md); communicate the platform's existence and benefits proactively. |
| Contingency | Periodic IT discovery scan / amnesty program to identify existing shadow-IT applications and migrate them or formally exception-approve them. |
| Owner | IT Administrator |
| Status | Open |

#### RISK-002 — Low Employee Adoption of the Standardized Deployment Flow

| Field | Detail |
|---|---|
| Category | Adoption |
| Description | Employees may continue requesting manual IT deployment or avoid the Claude Code + Skill + MCP flow if it is perceived as slower, more restrictive, or less flexible than what they are used to, undermining the core objective of reducing IT workload. |
| Trigger / Cause | Poor developer experience (confusing errors, slow validation, unclear documentation); perceived loss of flexibility versus ungoverned self-hosting; insufficient training/communication at launch. |
| Impact | High |
| Likelihood | Medium |
| Overall Risk Rating | **High** |
| Mitigation | Prioritize developer experience in the dedicated UX/DX Design SDLC phase; provide clear, fast validation feedback from MCP tools; run a well-supported pilot with responsive feedback channels before wider rollout. |
| Contingency | Time-box a mandatory transition period (with Management/IT backing) requiring new applications to use the platform, if voluntary adoption fails to reach targets after an initial window. |
| Owner | Platform Administrator |
| Status | Open |

#### RISK-003 — IT/Platform Team Becomes a New Approval Bottleneck

| Field | Detail |
|---|---|
| Category | Operational |
| Description | If production approval gates, stack-addition requests, or exception handling all funnel through a small IT/Platform team without efficient tooling, the platform could recreate the same bottleneck it was built to eliminate, just at a different point in the flow. |
| Trigger / Cause | Approval workflow requires manual review for every deployment rather than only production/high-risk cases; insufficient staffing relative to deployment volume; no self-service exception path for common requests. |
| Impact | High |
| Likelihood | Medium |
| Overall Risk Rating | **High** |
| Mitigation | Keep dev-environment deployment fully automated with policy-based validation (no human in the loop), per the stated business rules; reserve human approval strictly for production and policy exceptions; track approval queue time as an operational KPI. |
| Contingency | Add approval delegation (e.g., department-level approvers) if the central Platform Administrator team becomes a queue bottleneck. |
| Owner | Platform Administrator |
| Status | Open |

#### RISK-004 — Scope Creep in the Supported Technology Stack

| Field | Detail |
|---|---|
| Category | Project |
| Description | Without a disciplined governance process, the list of supported frontend/backend/database/cache technologies could expand rapidly as individual teams request their preferred tools, increasing the validation, security-review, and maintenance burden across every layer of the platform. |
| Trigger / Cause | No formal stack-governance process defined (see DEC-019); ad hoc approvals granted under delivery pressure without assessing the full lifecycle cost of supporting a new technology. |
| Impact | Medium |
| Likelihood | Medium |
| Overall Risk Rating | **Medium** |
| Mitigation | Establish the stack-governance process (DEC-019) before launch; require a documented cost/benefit case (security review, operational support cost, expected usage) for any proposed stack addition. |
| Contingency | Periodically review actual usage of each supported technology and deprecate low-usage entries to control long-term maintenance surface. |
| Owner | Platform Administrator |
| Status | Open |

#### RISK-005 — Underestimated Effort to Build the MCP / Platform API Layer

| Field | Detail |
|---|---|
| Category | Project |
| Description | The MCP server and Platform API are the most novel, highest-risk components of this project — translating a simple deployment.yaml contract into safe, policy-enforced infrastructure actions. The engineering effort to build this robustly, including validation, policy enforcement, idempotency, error handling, and audit logging, may be significantly underestimated. |
| Trigger / Cause | Limited prior organizational experience building MCP servers or platform abstraction layers; estimation based on tool count rather than the full non-functional requirements each tool requires. |
| Impact | High |
| Likelihood | Medium |
| Overall Risk Rating | **High** |
| Mitigation | Scope MVP tightly to the minimum tool set needed for the pilot (e.g., validate_application, deploy_application, get_deployment_status, get_application_status); allocate explicit Technical Design time before development; consider a technical spike for the MCP-to-Platform-API boundary. |
| Contingency | If the build slips, protect security-critical requirements (policy enforcement, audit logging) rather than descoping them to hit a date — slip the timeline instead. |
| Owner | Platform Administrator |
| Status | Open |

#### RISK-006 — Infrastructure Technology Choice Proves a Poor Operational Fit

| Field | Detail |
|---|---|
| Category | Technical |
| Description | The candidate infrastructure implementation ultimately selected (e.g., K3s+Knative) may prove operationally difficult for the company's actual team size, skill level, or workload patterns after real-world use, requiring a costly migration to a different implementation later. |
| Trigger / Cause | Selection made primarily on technical merit or industry popularity rather than validated against the company's actual operational capacity; MVP pilot too small or too simple to reveal operational issues before wider commitment. |
| Impact | Critical |
| Likelihood | Medium |
| Overall Risk Rating | **High** |
| Mitigation | Validate the chosen implementation against a realistic pilot workload (at least one multi-service, database-backed app) before broad rollout; explicitly weight "IT operational workload" and "maintainability" in the architecture decision, per the stated business objective. |
| Contingency | The architecture separates deployment.yaml (application contract) from the Deployment Controller implementation, so a future migration should be contained to the controller/infrastructure layers without changing the employee-facing contract; budget and timeline for this contingency should still be pre-acknowledged. |
| Owner | IT Administrator |
| Status | Open |

#### RISK-007 — Cost Overrun on Always-On Control-Plane Infrastructure

| Field | Detail |
|---|---|
| Category | Business |
| Description | The always-on components required for the platform to function (Platform API, MCP server, Application Registry database, monitoring/logging stack) accrue continuous infrastructure cost regardless of how many hosted applications are actually running, and this cost may exceed budget. |
| Trigger / Cause | No budget ceiling defined before architecture finalization (see DEC-015); control plane sized for a larger scale than initially needed; managed-service pricing assumptions prove inaccurate. |
| Impact | Medium |
| Likelihood | Medium |
| Overall Risk Rating | **Medium** |
| Mitigation | Secure an explicit MVP-phase budget ceiling before infrastructure sign-off (DEC-015); right-size control-plane components for pilot scale; track actual spend against budget monthly during the pilot. |
| Contingency | If costs exceed ceiling, downscale control-plane redundancy for the pilot phase or pause onboarding of new departments until budget is reforecast. |
| Owner | Management / Auditor |
| Status | Open |

#### RISK-008 — Insufficient Staffing/Skills to Operate Kubernetes-Family Infrastructure

| Field | Detail |
|---|---|
| Category | Organizational |
| Description | If the final infrastructure choice involves K3s, Kubernetes, or Knative, operating it reliably requires specialized skills (cluster administration, networking, troubleshooting) that the current IT organization may not yet have in sufficient depth. |
| Trigger / Cause | Infrastructure choice made without a matching staffing/training plan; reliance on one or two individuals with the relevant expertise (overlaps with RISK-021). |
| Impact | High |
| Likelihood | Medium |
| Overall Risk Rating | **High** |
| Mitigation | Factor current team skill level explicitly into the infrastructure decision (DEC-004) rather than treating it as a purely technical choice; budget for training or hiring before committing to a Kubernetes-family implementation; evaluate the Managed Container Platform option as a way to offload this skill requirement. |
| Contingency | Engage a short-term specialized contractor or managed-service support contract to bridge the skills gap during initial rollout. |
| Owner | IT Administrator |
| Status | Open |

#### RISK-009 — Claude Code / AI Agent Behavior Change Breaks Skill Assumptions

| Field | Detail |
|---|---|
| Category | Technical |
| Description | The Company Deployment Skill is written against the current behavior, tool-calling conventions, and capabilities of Claude Code. A future change in how the AI agent interprets instructions, calls tools, or handles files could silently break the Skill's assumptions, causing failed or malformed deployment requests. |
| Trigger / Cause | An update to Claude Code's tool-use behavior, file-handling conventions, or Skill-loading mechanism that is not backward compatible with the Skill's current structure. |
| Impact | Medium |
| Likelihood | Low |
| Overall Risk Rating | **Low** |
| Mitigation | Version the Company Deployment Skill explicitly; maintain an automated regression test suite exercising the Skill against a fixed set of sample projects; monitor Anthropic release notes relevant to Claude Code and Skills. |
| Contingency | If a breaking change is detected, temporarily require manual review of AI-generated deployment.yaml files until the Skill is patched and revalidated. |
| Owner | Platform Administrator |
| Status | Open |

#### RISK-010 — Vendor / Tooling Lock-In

| Field | Detail |
|---|---|
| Category | Business |
| Description | Committing to a Managed Container Platform, a specific cloud provider, or proprietary tooling for the control plane may create switching costs that constrain future negotiating leverage or migration options. |
| Trigger / Cause | Infrastructure choice (DEC-004) favors convenience/speed over portability; deployment.yaml translation logic becomes tightly coupled to one vendor's APIs. |
| Impact | Medium |
| Likelihood | Medium |
| Overall Risk Rating | **Medium** |
| Mitigation | Keep the deployment.yaml contract and Platform API vendor-neutral even if the underlying Deployment Controller targets one implementation initially; document the abstraction boundary so the Deployment Controller can be swapped without changing the employee-facing contract. |
| Contingency | Budget a defined migration project if lock-in becomes commercially unfavorable; the layered architecture (MCP → Platform API → Deployment Controller → Infrastructure) is specifically designed to make this contained. |
| Owner | IT Administrator |
| Status | Open |

#### RISK-011 — Unclear Application Ownership When an Employee Departs

| Field | Detail |
|---|---|
| Category | Organizational |
| Description | The deployment.yaml contract assigns an owner (e.g., department, implicitly tied to the employee who created it). When that employee leaves the company or changes roles without a defined handoff process, the application may become orphaned — no one able to approve changes, respond to incidents, or authorize decommissioning. |
| Trigger / Cause | No ownership-transfer process defined (see DEC-020); ownership modeled at the individual level without a designated backup/department-level approver. |
| Impact | Medium |
| Likelihood | High |
| Overall Risk Rating | **High** |
| Mitigation | Require every application to have both an individual owner and a department/team-level co-owner from creation; integrate ownership-transfer triggers with the company's HR offboarding process where feasible. |
| Contingency | Applications with no responsive owner after a defined grace period are automatically flagged, then auto-suspended, pending department manager review. |
| Owner | Platform Administrator |
| Status | Open |

#### RISK-012 — Production Incident from Inadequate Self-Service Deploy Gates

| Field | Detail |
|---|---|
| Category | Operational |
| Description | A poorly configured, insufficiently tested, or accidentally misconfigured application is deployed to production through the self-service flow and causes a business-impacting outage, data issue, or security exposure, because the approval and testing gates in place were not sufficient to catch it. |
| Trigger / Cause | Production-approval workflow not yet defined or under-resourced (see DEC-017); AI-generated deployment.yaml accepted without adequate human review; insufficient automated testing/validation before production promotion. |
| Impact | Critical |
| Likelihood | High |
| Overall Risk Rating | **Critical** |
| Mitigation | Enforce mandatory production approval with a defined approver and clear review information (see DEC-017); require automated validation and test execution as a hard gate before any production deploy request can be submitted; support instant rollback as a standard capability. |
| Contingency | Documented incident-response runbook including immediate rollback via the platform's rollback capability, plus a post-incident review that feeds back into validation/approval rules. |
| Owner | Platform Administrator |
| Status | Open |

#### RISK-013 — Security Control Gaps / Policy Bypass

| Field | Detail |
|---|---|
| Category | Technical |
| Description | Gaps in authentication, authorization, network isolation, or policy enforcement could allow an application, employee, or compromised AI agent session to bypass platform security controls. This entry is a business-impact summary only; the full attack-scenario threat model (compromised agent, container escape, cross-app access, supply-chain risk, credential/secret leakage, etc.) is maintained separately with its own `THREAT-xxx` register. |
| Trigger / Cause | See **11_Security_Requirements.md** for the complete threat model and root causes. |
| Impact | High |
| Likelihood | Low |
| Overall Risk Rating | **Medium** |
| Mitigation | Implement and continuously test the security controls defined in 11_Security_Requirements.md; treat security testing as a mandatory SDLC gate before any production rollout. |
| Contingency | See 11_Security_Requirements.md for threat-specific residual risk and incident-response guidance. |
| Owner | Security Administrator |
| Status | Open |

#### RISK-014 — Resource Quota Misconfiguration Causes Noisy-Neighbor Exhaustion

| Field | Detail |
|---|---|
| Category | Operational |
| Description | If quota tiers (small/medium/large) are set too generously, or enforcement is incomplete, a single misbehaving or unexpectedly popular application could consume shared infrastructure resources and degrade other applications on the same platform. |
| Trigger / Cause | Quota numbers not yet defined (see DEC-014); enforcement gap between Platform API validation and actual Deployment Controller enforcement; unexpected traffic spike on a scale-to-zero app. |
| Impact | Medium |
| Likelihood | Medium |
| Overall Risk Rating | **Medium** |
| Mitigation | Define and enforce hard resource ceilings per tier at the infrastructure level (not just at validation time); implement per-application resource monitoring with alerting. |
| Contingency | Automatic throttling or restart of the offending application; documented incident-response runbook for resource-exhaustion events. |
| Owner | Platform Administrator |
| Status | Open |

#### RISK-015 — Uncontrolled Per-Application Database Sprawl

| Field | Detail |
|---|---|
| Category | Technical |
| Description | Because every application can declare its own database in deployment.yaml, the platform may accumulate a large number of small, individually-managed PostgreSQL instances over time, increasing operational overhead, patching burden, and backup complexity. |
| Trigger / Cause | No consolidation strategy defined for how per-app database declarations map to actual database infrastructure (e.g., shared multi-tenant clusters vs. one instance per app). |
| Impact | Medium |
| Likelihood | Medium |
| Overall Risk Rating | **Medium** |
| Mitigation | Define a database provisioning strategy in Technical Design (e.g., shared PostgreSQL clusters with per-app schemas/databases and strict isolation) before the Database Manager module is built. |
| Contingency | Periodic database consolidation/rightsizing exercise; decommission databases for archived/deleted applications promptly. |
| Owner | IT Administrator |
| Status | Open |

#### RISK-016 — Platform API / MCP Is a Single Point of Failure

| Field | Detail |
|---|---|
| Category | Technical |
| Description | Because every deployment action for every application in the company must flow through the Company Deployment MCP and Platform API, an outage or severe degradation of these central components blocks deployments, rollbacks, and status checks company-wide, even though already-running applications may be unaffected. |
| Trigger / Cause | Control-plane components deployed without redundancy/HA design; no defined availability target for the control plane (see DEC-024); single Application Registry database instance. |
| Impact | Critical |
| Likelihood | Medium |
| Overall Risk Rating | **High** |
| Mitigation | Design the Platform API and MCP server for redundancy appropriate to their criticality (informed by DEC-024's availability target); ensure already-deployed applications continue running normally even if the control plane is temporarily unavailable. |
| Contingency | Documented incident-response runbook for control-plane outages; status page/notification so employees know self-service actions are temporarily degraded rather than assuming their specific application failed. |
| Owner | Platform Administrator |
| Status | Open |

#### RISK-017 — Incomplete Audit Trail Undermines Compliance/Investigation Capability

| Field | Detail |
|---|---|
| Category | Business |
| Description | If the Audit module does not capture a complete, tamper-evident record of who did what (employee, AI agent action, approval, deployment outcome), the company may be unable to answer compliance or incident-investigation questions after the fact. |
| Trigger / Cause | Audit logging treated as a lower-priority feature during MVP build; retention period not yet defined (see DEC-010). |
| Impact | Medium |
| Likelihood | Low |
| Overall Risk Rating | **Low** |
| Mitigation | Treat audit logging as a MUST-priority requirement from the first deployment-lifecycle implementation, not an add-on; define retention/immutability requirements early via DEC-010. |
| Contingency | If gaps are found post-launch, prioritize an audit-logging remediation sprint before onboarding additional departments. |
| Owner | Security Administrator |
| Status | Open |

#### RISK-018 — Cold-Start Latency Drives Teams to Opt Out of Scale-to-Zero

| Field | Detail |
|---|---|
| Category | Adoption |
| Description | If the latency to scale an application back up from zero instances is noticeable to end users, employees may set scaling.min to 1 or higher for every application to avoid it, defeating the cost and resource-efficiency purpose of the scale-to-zero requirement. |
| Trigger / Cause | Chosen infrastructure implementation has slow cold-start characteristics; idle timeout (DEC-023) set too aggressively for the actual traffic pattern of internal tools. |
| Impact | Medium |
| Likelihood | High |
| Overall Risk Rating | **High** |
| Mitigation | Benchmark cold-start latency during infrastructure evaluation (10_System_Architecture.md) as an explicit comparison criterion; set a sensible default idle timeout (DEC-023) rather than an aggressive one. |
| Contingency | If cold-start latency proves broadly unacceptable, revisit the infrastructure choice's scale-to-zero mechanism as a scoped architecture review rather than letting every app silently opt out. |
| Owner | Platform Administrator |
| Status | Open |

#### RISK-019 — Production Approval Workflow Becomes a Rubber Stamp

| Field | Detail |
|---|---|
| Category | Organizational |
| Description | If the production-approval step is procedurally required but not meaningfully enforced (approvers click "approve" without review due to time pressure or unclear responsibility), the control provides a false sense of safety while not actually preventing unsafe production deploys. |
| Trigger / Cause | Approval workflow/tooling not clearly defined (see DEC-017); approver role assigned without adequate context or time; approval volume outpaces reviewer capacity. |
| Impact | Medium |
| Likelihood | Medium |
| Overall Risk Rating | **Medium** |
| Mitigation | Design the approval interface to surface the specific information an approver needs (validation results, diff from prior version, resource/cost impact) rather than a blind approve button; track and periodically audit approval turnaround time and rejection rate. |
| Contingency | If audit data shows rubber-stamping (near-100% approval with near-zero review time), escalate to Management and revise the workflow or approver assignment. |
| Owner | Management / Auditor |
| Status | Open |

#### RISK-020 — deployment.yaml Schema Drift Versus the Company Deployment Skill

| Field | Detail |
|---|---|
| Category | Project |
| Description | If the Platform API's accepted deployment.yaml schema evolves (new fields, changed validation rules) without a corresponding update to the Company Deployment Skill that Claude Code reads, AI-generated deployment definitions will fail validation or silently omit new capabilities. |
| Trigger / Cause | Schema versioning and Skill versioning managed independently without a coordinated release process. |
| Impact | Low |
| Likelihood | Medium |
| Overall Risk Rating | **Low** |
| Mitigation | Version the deployment.yaml schema explicitly; gate the Company Deployment Skill release process on schema-compatibility tests; publish schema changes to the Skill's docs/ and schemas/ folders as part of the same change. |
| Contingency | Platform API rejects deployment.yaml files with unrecognized schema versions with a clear error directing the employee to update the Skill/tooling, rather than failing silently. |
| Owner | Platform Administrator |
| Status | Open |

#### RISK-021 — Key-Person / Bus-Factor Risk on a Small Platform Team

| Field | Detail |
|---|---|
| Category | Organizational |
| Description | In the early phases, the platform (MCP server, Platform API, Deployment Controller, Skill) will likely be built and deeply understood by a very small number of people; losing one of them could stall development or leave production operations without adequate expertise. |
| Trigger / Cause | Small dedicated platform team by design/budget constraint (overlaps with RISK-008); knowledge not documented outside individuals' heads. |
| Impact | Medium |
| Likelihood | Medium |
| Overall Risk Rating | **Medium** |
| Mitigation | Require documentation of architecture decisions, runbooks, and configuration as they are built; pair critical build/operational work across at least two people. |
| Contingency | Cross-train an IT Administrator or secondary Platform Administrator early enough to provide backup coverage before go-live. |
| Owner | Platform Administrator |
| Status | Open |

#### RISK-022 — Budget Not Secured Beyond MVP/Pilot Phase

| Field | Detail |
|---|---|
| Category | Business |
| Description | If funding is approved only for the initial MVP/pilot and not for ongoing operation, hardening, and Phase 2 expansion, the project risks stalling after the pilot with partially-built capabilities and disillusioned early-adopter departments. |
| Trigger / Cause | Budget approval process treats this as a single project rather than an ongoing platform with operating costs; MVP success criteria not tied to a funded Phase 2 decision gate. |
| Impact | High |
| Likelihood | Low |
| Overall Risk Rating | **Medium** |
| Mitigation | Present budget requests in phases with clear MVP exit criteria that trigger a funded Phase 2 decision (see DEC-015); track KPIs during the pilot to build the business case for continued investment. |
| Contingency | If Phase 2 funding is delayed, maintain the MVP in a stable, supportable state rather than expanding scope without corresponding budget/staffing. |
| Owner | Management / Auditor |
| Status | Open |

#### RISK-023 — AI-Generated deployment.yaml Misconfiguration Causes Outage or Accidental Exposure

| Field | Detail |
|---|---|
| Category | Operational |
| Description | Claude Code, acting on incomplete or ambiguous instructions from an employee, could generate a deployment.yaml with an incorrect setting — for example, domain.visibility: external when internal was intended, or an undersized resources.tier — that platform validation does not catch, resulting in accidental exposure or a resource-starved application. |
| Trigger / Cause | Company Deployment Skill instructions ambiguous, or the employee under-specifies requirements; Platform API validation does not flag high-risk field combinations for extra scrutiny. |
| Impact | High |
| Likelihood | Medium |
| Overall Risk Rating | **High** |
| Mitigation | Require the Company Deployment Skill to always surface a human-readable summary of security-relevant fields (visibility, database exposure, resource tier) for explicit employee confirmation before submission (see DEC-021); require Platform API validation to flag and require additional approval for any visibility: external declaration (see DEC-013). |
| Contingency | Automated post-deployment configuration audit that flags any running application whose declared visibility/resource settings deviate from the department's typical pattern, for review. |
| Owner | Application Owner |
| Status | Open |

#### RISK-024 — Competing Department Priorities Stall the Platform Roadmap

| Field | Detail |
|---|---|
| Category | Organizational |
| Description | Different departments (e.g., HR, Finance, Engineering) may request conflicting features, supported technologies, or priority treatment for their applications, diluting focus and slowing delivery of a coherent MVP. |
| Trigger / Cause | No formal intake/prioritization process for feature requests or new department onboarding. |
| Impact | Low |
| Likelihood | Medium |
| Overall Risk Rating | **Low** |
| Mitigation | Establish a lightweight platform roadmap governance process with a single prioritization owner; communicate MVP scope boundaries clearly up front. |
| Contingency | Maintain a visible backlog so deferred requests are acknowledged rather than silently dropped, reducing stakeholder frustration. |
| Owner | Management / Auditor |
| Status | Open |

#### RISK-025 — Regulatory/Compliance Scope Left Undefined

| Field | Detail |
|---|---|
| Category | Business |
| Description | If it remains unclear which regulatory or compliance frameworks (if any) apply to the company and this platform, requirements for audit-log retention, data handling, and access control may be under-built now and require costly rework once the applicable framework is finally identified. |
| Trigger / Cause | No confirmed answer to DEC-010 (applicable regulatory/compliance framework) at the time Technical Design begins. |
| Impact | High |
| Likelihood | Medium |
| Overall Risk Rating | **High** |
| Mitigation | Escalate DEC-010 for resolution before Technical Design begins, not after; where genuinely unresolved, design the Audit and Data modules to the more conservative plausible requirement so that a stricter framework confirmed later does not require rearchitecting. |
| Contingency | If a compliance framework is identified late, conduct a gap assessment against 11_Security_Requirements.md and 12_Data_Requirements.md and remediate before any affected application goes to production. |
| Owner | Security Administrator |
| Status | Open |

#### RISK-026 — Container Registry / Image Storage Growth Unmanaged

| Field | Detail |
|---|---|
| Category | Operational |
| Description | Every build produces a new container image; without a retention/cleanup policy, registry storage grows indefinitely, increasing cost and making it harder to identify which images are actually in use. |
| Trigger / Cause | No image-retention policy defined alongside the Build Engine and Container Registry design. |
| Impact | Low |
| Likelihood | Medium |
| Overall Risk Rating | **Low** |
| Mitigation | Define an image retention policy (e.g., keep last N versions per application plus any currently-deployed version) as part of Build Engine technical design; automate cleanup. |
| Contingency | Periodic manual registry audit and cleanup if automated retention is not ready by launch. |
| Owner | IT Administrator |
| Status | Open |

#### RISK-027 — Legacy Shadow-IT Applications Incompatible with the Supported Stack

| Field | Detail |
|---|---|
| Category | Technical |
| Description | Applications employees already built and are running outside the platform (see RISK-001) may use technologies, architectures, or data patterns incompatible with the v1 supported stack, making migration onto the platform difficult or impossible without a rewrite. |
| Trigger / Cause | Existing shadow-IT applications built before the supported-stack list existed, using unsupported frameworks, databases, or architectural patterns (e.g., monolith with embedded database). |
| Impact | Medium |
| Likelihood | High |
| Overall Risk Rating | **High** |
| Mitigation | Inventory existing shadow-IT applications during Discovery/Requirements Analysis to understand the realistic technology gap before finalizing the v1 supported stack; where feasible, prioritize supported-stack choices that cover the most common existing patterns. |
| Contingency | Define a documented exception/legacy-support process (time-boxed, with a required migration plan) for applications that cannot immediately conform. |
| Owner | Platform Administrator |
| Status | Open |

#### RISK-028 — IT Staff Resistance to Automation of Their Manual Role

| Field | Detail |
|---|---|
| Category | Organizational |
| Description | IT staff who currently perform manual deployment, configuration, and troubleshooting work may perceive the platform as automating away their role, leading to passive resistance, slow cooperation on integration work, or reluctance to hand off institutional knowledge. |
| Trigger / Cause | Platform introduced without clear communication about how IT roles evolve (e.g., toward platform engineering, policy design, and incident response rather than manual per-app deployment). |
| Impact | Medium |
| Likelihood | Low |
| Overall Risk Rating | **Low** |
| Mitigation | Involve IT staff early as co-designers of the platform, not just downstream operators; communicate the role shift explicitly; recognize and reward contributions to the platform build. |
| Contingency | If resistance persists, escalate to Management for a clear organizational mandate and role redefinition. |
| Owner | Management / Auditor |
| Status | Open |

#### RISK-029 — MVP Pilot Uses Only Simple Apps, Hiding Complex-App Gaps

| Field | Detail |
|---|---|
| Category | Project |
| Description | If the initial pilot only includes simple, low-risk applications, the platform may appear successful while gaps in handling multi-service apps, databases, higher traffic, or production-tier requirements remain undiscovered until a more demanding application attempts to onboard later. |
| Trigger / Cause | Pilot participant selection driven by convenience/availability rather than deliberate coverage of representative complexity (see DEC-022). |
| Impact | Medium |
| Likelihood | Medium |
| Overall Risk Rating | **Medium** |
| Mitigation | Deliberately include at least one moderately complex application (multi-service, database-backed, per the example `overtime` app) in the MVP pilot cohort. |
| Contingency | If a complex app fails onboarding post-pilot, treat it as a fast-follow hardening sprint before broader rollout rather than a platform failure. |
| Owner | Platform Administrator |
| Status | Open |

#### RISK-030 — Monitoring/Alerting Gaps Cause Silent Failures in Scaled-to-Zero Apps

| Field | Detail |
|---|---|
| Category | Operational |
| Description | An application that is idle and scaled to zero is difficult to distinguish from an application that is broken and failing to scale back up; without proper monitoring, an outage could go undetected until an employee notices the app is unreachable. |
| Trigger / Cause | Monitoring/alerting for the Health Check Manager and scale-to-zero lifecycle not fully designed before launch; alert fatigue from treating normal scale-to-zero state as an anomaly. |
| Impact | Medium |
| Likelihood | Medium |
| Overall Risk Rating | **Medium** |
| Mitigation | Design monitoring to distinguish "intentionally scaled to zero" from "failed to scale up on demand" (alert only on failed cold-start attempts, not zero-replica state itself); include synthetic health checks that periodically wake and verify low-traffic apps. |
| Contingency | Provide employees a self-service "check application health" capability (via the MCP `get_application_status` tool) so they are not solely dependent on platform-side alerting. |
| Owner | Platform Administrator |
| Status | Open |

---

## 7. Review Cadence

This register reflects **inherent risk** as of the current documentation baseline (2026-08-28), before most mitigations have been implemented. It should be formally re-reviewed:

- At the exit of each SDLC phase gate defined in 09_SDLC.md.
- Monthly during the MVP pilot.
- Quarterly after general availability.
- Immediately following any production incident, security finding, or infrastructure decision (17_Decision_Log.md) that materially changes a risk's likelihood or impact.

New risks identified during later phases should be appended as `RISK-031`, `RISK-032`, … — existing IDs must never be renumbered or reused.
