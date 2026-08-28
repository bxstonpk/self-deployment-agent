# 15 — Requirements Traceability Matrix (RTM)

## Company AI Application Deployment Platform

---

## 1. Purpose and How to Read This Matrix

### 1.1 Purpose

This document is the single cross-reference that ties every Business Objective in **01_BRD.md** (Section 6) to the downstream requirements, design components, and tests that realize it, and back again. Its job is to answer four questions on demand:

1. **Forward traceability** — "Why does FR-XXX exist?" → trace up to the Business Objective(s) and Business Rule(s) it serves.
2. **Backward traceability** — "What implements Business Objective #N?" → trace down to Functional Requirements, Non-Functional Requirements, System Modules, and Test Types.
3. **Coverage verification** — "Is every objective actually built and tested? Is every built module actually needed?" → the reverse-check table (Section 3) and gap analysis (Section 4).
4. **Change-impact analysis** — "If FR-045 changes, what else might need to change?" → read the row(s)/column(s) that contain FR-045 across every table in this document.

### 1.2 The Traceability Chain

```
Business Objective (01_BRD.md §6, 20 objectives)
        │
        ▼
Business Requirement / Business Rule (01_BRD.md §14 themes → 04_Business_Rules.md, BR-001–BR-035)
        │
        ▼
Functional Requirement (02_Functional_Requirements.md, FR-001–FR-129, Modules A–AB)
        │
        ▼
Non-Functional Requirement (03_Non_Functional_Requirements.md, NFR-001–NFR-050, 13 categories)
        │
        ▼
System Module (06_System_Requirements.md, MOD-01–MOD-19)
        │
        ▼
Test Case / Test Type (14_Test_Strategy.md, 14 test types)
        │
        ▼
Acceptance Criteria (01_BRD.md §45 platform-level AC, or the owning FR's own Acceptance Criteria field)
```

### 1.3 How to Read the Tables

- **Section 2 (Master Matrix)** reads **top-down**: pick a Business Objective row, read across to see everything that implements and verifies it.
- **Section 3 (Reverse-Check)** reads **bottom-up**: pick an FR Module row, read across to see which objective(s) justify its existence. This is the check against orphaned scope (a module nobody asked for) and unsupported ambition (an objective nothing was built for).
- **Section 4 (Gap Analysis)** is the delta between what Sections 2 and 3 *should* show in a fully consistent baseline and what they *actually* show, based on a real read of all source documents — not a hypothetical checklist.
- **IDs are load-bearing.** Every BO/BR/FR/NFR/MOD/Test-Type reference in this document was read directly from its source file. Where a mapping is inferred (i.e., the source documents do not state the link verbatim), it is marked *(inferred)* rather than presented as an explicit cross-reference.
- **Objective numbering.** 01_BRD.md §6 lists 20 objectives in a numbered table but does not assign them formal IDs. This matrix assigns `BO-01`…`BO-20` following that table's row order, for reference purposes only.

---

## 2. Master Traceability Matrix — Business Objectives → Everything

Source for the Objective column: **01_BRD.md, Section 6** (20 objectives). Source for Business Rule(s): **01_BRD.md §14** themes, elaborated as **04_Business_Rules.md** BR-001–BR-035. Source for FR: **02_Functional_Requirements.md** (Table of Contents / §5 Summary). Source for NFR: **03_Non_Functional_Requirements.md** §2 NFR Index. Source for System Module: **06_System_Requirements.md**. Source for Test Type: **14_Test_Strategy.md** §§2–15. Source for Acceptance Criteria: **01_BRD.md §45** (platform-level AC, numbered AC-1…AC-12 per that section's list order) or the relevant FR's own **Acceptance Criteria** field.

| # | Business Objective (01_BRD.md §6) | Related Business Rule(s) | Related FR Module(s) / ID Range | Related NFR Categories / ID Range | Related System Module(s) | Related Test Type(s) | Related Acceptance Criteria |
|---|---|---|---|---|---|---|---|
| BO-01 | Standardize how internal applications are deployed across the company, regardless of author or team. | BR-004, BR-005, BR-034 (Stack & Technology Policy; Naming & Domain Rules) | F. Stack Management (FR-019–022); G. Deployment Configuration (FR-023–028); D. Application Registration (FR-011–014) | Maintainability (NFR-029–030); Deployability (NFR-043–044) | MOD-02 Application Registry; MOD-04 Validation Engine | Deployment Testing; Integration Testing; Unit Testing | AC-1 ("An employee can register an approved application without requiring IT to manually intervene") |
| BO-02 | Reduce IT operational workload per deployed application to near-zero for routine, approved cases. | BR-007, BR-008 (Deployment Approval Policy) | J. Deployment Management (FR-039–044); K. Application Lifecycle (FR-045–050); AA. Administration (FR-122–126) | Usability (NFR-047) | MOD-03 Deployment Manager; MOD-17 Platform API | UAT; Integration Testing | AC-1; supported by KPIs "IT deployment tickets/month" and "IT hours per application" (BRD §44) |
| BO-03 | Provide employees a self-service path from "working application" to "running, reachable application." | BR-007, BR-008, BR-009 (Deployment Approval Policy) | D. Application Registration (FR-011–014); G. Deployment Configuration (FR-023–028); H. Deployment Validation (FR-029–034); I. Build Management (FR-035–038); J. Deployment Management (FR-039–044) | Usability (NFR-047–048) | MOD-02 Application Registry; MOD-03 Deployment Manager; MOD-04 Validation Engine; MOD-05 Build Engine; MOD-06 Deployment Controller | End-to-End Testing; UAT; Deployment Testing; Performance Testing | AC-1; AC-6 ("An approved application can be deployed end-to-end via the MCP, reaching a healthy 'Running' state") |
| BO-04 | Support and formalize the existing employee practice of using AI coding agents (Claude Code) for application development. | BR-016, BR-017 (AI Agent Safety Boundary) | Z. Claude Code / AI Agent Integration (FR-116–121) | *(none dedicated — see Section 4a)* | MOD-16 MCP Server | MCP Testing; UAT | AC-2 ("Claude Code can discover platform capabilities... via the MCP without prior out-of-band knowledge") |
| BO-05 | Provide a Company Deployment Skill that teaches Claude Code how to package and request deployment of an application correctly. | BR-016, BR-017 | Z. Claude Code / AI Agent Integration (FR-116–121) | Usability (NFR-048 actionable validation error messages) | MOD-16 MCP Server | MCP Testing; UAT | AC-2; AC-3 ("Claude Code can validate an application via the MCP and receive accurate, actionable pass/fail feedback") |
| BO-06 | Provide a Company Deployment MCP that exposes only safe, high-level, business-capability deployment operations to AI agents. | BR-016, BR-017 | Y. MCP Integration (FR-110–115) | Performance (NFR-005 MCP tool call latency); Reliability (NFR-020 MCP tool call idempotency); Availability (NFR-017 MCP Server availability) | MOD-16 MCP Server | MCP Testing; Security Testing; Performance Testing | AC-2; AC-3; AC-5 ("An unauthorized deployment request... is rejected by the Platform API, independent of what the AI agent requested") |
| BO-07 | Enforce a governed, extensible set of supported technology stacks; reject unsupported stacks before deployment. | BR-004, BR-005, BR-006 (Stack & Technology Policy) | F. Stack Management (FR-019–022); H. Deployment Validation (FR-029–034) | Maintainability (NFR-029 supported-stack extensibility) | MOD-04 Validation Engine | Unit Testing; API Testing; Deployment Testing | AC-4 ("An application using an unsupported stack is rejected at validation, before any build/deploy work occurs") |
| BO-08 | Enforce security and infrastructure policy centrally, independent of and not trusting the AI agent or the employee's local environment. | BR-013, BR-014, BR-016 (Security & Isolation Rules; AI Agent Safety Boundary) | H. Deployment Validation (FR-029–034); O. Secret Management (FR-066–071); Y. MCP Integration (FR-110–115); Z. Claude Code / AI Agent Integration (FR-116–121) | Security (NFR-023–028, full category) | MOD-04 Validation Engine; MOD-17 Platform API; MOD-08 Secret Manager; MOD-01 IAM | Security Testing; MCP Testing | AC-5; AC-10 ("An employee/application cannot access another application's secrets or database...") |
| BO-09 | Automatically provision the compute, storage, and database resources an approved application needs, with no manual IT provisioning step. | BR-018, BR-028, BR-029 (Resource & Quota Rules; Data & Database Rules) | M. Resource Management (FR-057–060); N. Database Management (FR-061–065) | Scalability (NFR-009 horizontal scale bounds; NFR-012 persistent-service scaling independence) | MOD-07 Resource Manager; MOD-09 Database Manager | Deployment Testing; Infrastructure Testing | AC-6 |
| BO-10 | Automatically configure networking (service wiring, reverse proxying, internal routing) without employee or agent involvement. | BR-013 (Security & Isolation Rules) | Q. Networking (FR-076–081) | Security (NFR-026 default-deny network isolation) | MOD-06 Deployment Controller | Integration Testing; Deployment Testing | AC-6; module AC (FR-077: "Requests to an application's domain are correctly routed to the intended service without any manual ingress configuration step") |
| BO-11 | Automatically assign and configure a reachable URL/domain per application without manual DNS/TLS work by the employee or agent. | BR-034, BR-035 (Naming & Domain Rules) | P. Domain Management (FR-072–075) | *(none dedicated — see Section 4a; genuine gap)* | MOD-10 Domain Manager | Deployment Testing; Integration Testing | AC-7 ("A deployed internal-visibility application automatically receives a working URL, with no manual DNS/TLS/networking steps...") |
| BO-12 | Automatically run health checks on every deployment before it is considered live. | *(no directly-numbered BR; enforced procedurally via Deployment Approval/Environment rules)* | R. Health Check (FR-082–085) | Reliability (NFR-021 health-check gating before traffic activation); Observability (NFR-033 health-check interval/threshold configurability) | MOD-11 Health Check Manager | Deployment Testing; Failure Recovery Testing | AC-8 ("Health checks run automatically before any new deployment's traffic is activated") |
| BO-13 | Provide built-in logging and monitoring for every deployed application by default, with no separate setup. | *(no directly-numbered BR — see Section 4a)* | S. Logging (FR-086–089); T. Monitoring (FR-090–093) | Observability (NFR-031–035, full category) | MOD-12 Logging; MOD-13 Monitoring | Integration Testing; Infrastructure Testing | *(no direct BRD §45 AC — see Section 4c; covered via each FR's own Acceptance Criteria field, Modules S/T)* |
| BO-14 | Support application versioning, so each deployment is traceable to a specific build/version. | *(no directly-numbered BR)* | U. Version Management (FR-094–097) | Recoverability (NFR-042 deployment history reconstructability) | MOD-03 Deployment Manager; MOD-19 Application Catalog | Deployment Testing; Rollback Testing | AC-11 ("IT/Platform Administrators can view an application's deployment history and perform a rollback to a prior version") |
| BO-15 | Support rollback to a previously known-good version when a deployment or release proves faulty. | *(no directly-numbered BR)* | V. Rollback (FR-098–102) | Recoverability (NFR-041 Rollback Time Objective) | MOD-03 Deployment Manager; MOD-06 Deployment Controller | Rollback Testing | AC-11 |
| BO-16 | Enforce resource limits and quotas per application/department to prevent runaway consumption and control cost. | BR-018, BR-019, BR-020 (Resource & Quota Rules) | M. Resource Management (FR-057–060); C. Organization / Department (FR-008–010) | Scalability (NFR-009 org-wide ceiling) | MOD-07 Resource Manager | Integration Testing; Infrastructure Testing | *(no direct BRD §45 AC — see Section 4c; covered via FR-057–060 own Acceptance Criteria fields, Module M)* |
| BO-17 | Automatically scale stateless application workloads to zero when idle, and back up on demand, to reduce infrastructure cost. | BR-021, BR-023, BR-024 (Scale-to-Zero Rules) | L. Scale-to-Zero (FR-051–056) | Performance (NFR-004 scale-from-zero cold start latency); Scalability (NFR-008 idle-to-zero timeout configurability) | MOD-06 Deployment Controller; MOD-07 Resource Manager | Scale-to-Zero Testing; Cold Start Testing | AC-9 ("A stateless application scales from zero to N replicas on incoming traffic and back to zero after its configured idle period...") |
| BO-18 | Keep databases and other persistent/stateful infrastructure isolated from the scale-to-zero lifecycle of stateless application containers. | BR-022 (Databases and Persistent Services Never Scale to Zero With Their Application) | N. Database Management (FR-061–065); L. Scale-to-Zero (FR-051–056) | Scalability (NFR-012 persistent-service scaling independence) | MOD-09 Database Manager | Scale-to-Zero Testing; Failure Recovery Testing | AC-9 ("...while its database (if any) remains available throughout") |
| BO-19 | Prevent AI coding agents from directly manipulating infrastructure (no kubectl, Docker socket, host filesystem, or arbitrary resource creation) under any circumstance. | BR-014, BR-016, BR-017 (Containers Never Run Privileged; AI Agent Safety Boundary; Only High-Level Capabilities Exposed) | Y. MCP Integration (FR-110–115); Z. Claude Code / AI Agent Integration (FR-116–121) | Security (NFR-023 tenant/application isolation; NFR-028 server-side policy re-validation) | MOD-16 MCP Server; MOD-17 Platform API | Security Testing; MCP Testing | AC-5; AC-10 |
| BO-20 | Hide all low-level infrastructure detail (containers, orchestration, networking, TLS, DNS, container security) from employees and from the AI agent, so neither needs infrastructure expertise to ship an approved application. | BR-016, BR-017, BR-029 | G. Deployment Configuration (FR-023–028); Y. MCP Integration (FR-110–115); Z. Claude Code / AI Agent Integration (FR-116–121) | Portability (NFR-045 infrastructure-agnostic deployment contract); Usability (NFR-047–048) | MOD-16 MCP Server; MOD-17 Platform API; MOD-06 Deployment Controller | MCP Testing; UAT | AC-2; AC-7 |

---

## 3. Reverse-Check Matrix — FR Modules → Business Objectives Served

Purpose: confirm no FR module (A–AB) is orphaned (built but serving no stated objective) and, by construction of Section 2, no objective is left unsupported. Source: **02_Functional_Requirements.md** Table of Contents / §5 Summary for module names and FR ranges.

| Module | FR Range | Business Objective(s) Served | Basis |
|---|---|---|---|
| A. Authentication | FR-001–004 | BO-08, BO-19 (foundational to both); underpins self-service access for BO-03 | *(inferred — identity is a precondition for centralized enforcement and agent-boundary objectives, not named verbatim against a single BO)* |
| B. User Management | FR-005–007 | BO-01 (author/team identification) | *(inferred — see Section 4)* |
| C. Organization / Department | FR-008–010 | BO-16 (department-level quota) | Explicit (deptattribution feeds quota enforcement) |
| D. Application Registration | FR-011–014 | BO-01, BO-03 | Explicit |
| E. Application Ownership | FR-015–018 | BO-01 (accountable owner needed for "regardless of author or team" standardization) | *(inferred — see Section 4)* |
| F. Stack Management | FR-019–022 | BO-01, BO-07 | Explicit |
| G. Deployment Configuration | FR-023–028 | BO-01, BO-03, BO-20 | Explicit |
| H. Deployment Validation | FR-029–034 | BO-03, BO-07, BO-08 | Explicit |
| I. Build Management | FR-035–038 | BO-03 | Explicit |
| J. Deployment Management | FR-039–044 | BO-02, BO-03 | Explicit |
| K. Application Lifecycle | FR-045–050 | BO-02 | Explicit |
| L. Scale-to-Zero | FR-051–056 | BO-17, BO-18 | Explicit |
| M. Resource Management | FR-057–060 | BO-09, BO-16 | Explicit |
| N. Database Management | FR-061–065 | BO-09, BO-18 | Explicit |
| O. Secret Management | FR-066–071 | BO-08 | Explicit |
| P. Domain Management | FR-072–075 | BO-11 | Explicit |
| Q. Networking | FR-076–081 | BO-10 | Explicit |
| R. Health Check | FR-082–085 | BO-12 | Explicit |
| S. Logging | FR-086–089 | BO-13 | Explicit |
| T. Monitoring | FR-090–093 | BO-13 | Explicit |
| U. Version Management | FR-094–097 | BO-14 | Explicit |
| V. Rollback | FR-098–102 | BO-15 | Explicit |
| W. Audit Log | FR-103–106 | BO-08 (policy-enforcement evidence); BRD AC-12 | *(inferred — see Section 4)* |
| X. Notification | FR-107–109 | BO-13 (operability bundle), BO-02 | *(inferred — see Section 4)* |
| Y. MCP Integration | FR-110–115 | BO-06, BO-19, BO-20 | Explicit |
| Z. Claude Code / AI Agent Integration | FR-116–121 | BO-04, BO-05, BO-19, BO-20 | Explicit |
| AA. Administration | FR-122–126 | BO-02 (self-service reduces IT admin burden), BO-16 (quota administration) | *(inferred — see Section 4)* |
| AB. Reporting | FR-127–129 | BO-02 (KPI visibility per BRD §44) | *(inferred — see Section 4)* |

**Result of the check:** No FR module is fully orphaned — every one of the 28 modules traces to at least one Business Objective. However, **7 of the 28 modules (B, C partially, E, W, X, AA, AB) do not trace to an objective by an explicit textual match** in 01_BRD.md §6; the link is inferred from the module's evident purpose and from other BRD sections (§14 Business Requirements, §29 Audit, §36 Notification, §37 Reporting, §38 Administration, §44 KPIs). This is a real finding, detailed in Section 4.

Every Business Objective (BO-01…BO-20) has at least one explicitly-matched FR module in Section 2 — **no objective is unsupported.**

---

## 4. Coverage Gap Analysis

This section reports what was actually found by cross-checking Sections 2 and 3 against the source documents — not a hypothetical or templated gap list.

### 4a. Business Objectives with thin or no FR/NFR coverage

- **All 20 objectives have adequate-to-strong FR module coverage.** No objective lacks a Functional Requirement module.
- **NFR coverage is genuinely thin or absent for four objectives:**
  - **BO-11** (automatic URL/domain assignment, no manual DNS/TLS) has **no matching NFR at all.** A full read of the NFR Index (03_Non_Functional_Requirements.md §2, all 50 entries) found zero NFRs referencing "domain," "DNS," "TLS," or "URL" in any title. Domain/TLS behavior is functionally specified (Module P, FR-072–075) but has no measurable non-functional target (e.g., time-to-provision-a-domain, TLS cert renewal SLA). **Recommendation:** add a Deployability or Performance NFR for domain/TLS provisioning latency, or an explicit note that domain NFRs are folded into NFR-002 (deploy_application end-to-end time).
  - **BO-04 and BO-05** (formalizing the AI-agent/Claude Code practice and the Company Deployment Skill) have no NFR written specifically against them; the closest available NFRs (NFR-005 MCP latency, NFR-048 actionable validation errors) are attached to BO-06 and BO-03 instead. This is a minor/acceptable gap since BO-04/05 are largely policy/process objectives rather than measurable-system objectives, but it is worth an explicit decision-log note if a Skill-specific SLA is ever wanted.
  - **BO-16** (resource quotas) has only one loosely-related NFR (NFR-009, an org-wide scaling ceiling) and no NFR for, e.g., quota-check latency or quota-breach detection time.
- **BO-13 and BO-14/BO-15** (logging/monitoring, versioning, rollback) are adequately covered by NFR categories (Observability; Recoverability) — no gap.

### 4b. FR modules with no obvious test-type coverage in 14_Test_Strategy.md

14_Test_Strategy.md §2 (Unit Testing) makes one blanket statement that Unit Testing covers "code-level logic inside MOD-01…MOD-19 implementations," which technically gives every module *some* nominal coverage. But when checking each test type's actual **Example Scenarios** (the concrete, named test cases in §§2–15), the following modules are **never named in any scenario, in any of the 14 test types**:

- **Module B — User Management** (FR-005–007): no scenario tests account creation, role assignment, or profile/department linkage.
- **Module C — Organization / Department** (FR-008–010): no scenario tests department modeling or attribution.
- **Module E — Application Ownership** (FR-015–018): no scenario tests ownership assignment, transfer, or the "no ownership gap" rule (BR-002).
- **Module AA — Administration** (FR-122–126) / **MOD-18 Administration Portal**: named once, only as a caller of the Platform API (§4, API Testing), never as a subject under test.
- **Module AB — Reporting** (FR-127–129) / **MOD-19 Application Catalog**: the words "reporting," "dashboard," and "catalog" (as a tested capability) do not appear anywhere in 14_Test_Strategy.md's 14 test-type sections.

By contrast, **Module W (Audit Log) and Module X (Notification) are explicitly, repeatedly named** with concrete scenarios (audit-entry-per-MCP-call in MCP Testing §5; audit-entry-per-rollback in Rollback Testing §13; notification-on-failure in Failure Recovery Testing §12) — so despite lacking a direct BO match in Section 4a's sibling finding, their test coverage is solid.

**Recommendation:** 14_Test_Strategy.md should add explicit example scenarios for Modules B, C, E, AA, and AB — most naturally under Integration Testing (§3) and UAT (§14), which are the two test types whose scope statements already claim to cover "the full employee-facing journey" / "cross-module interactions" broadly enough to include them.

### 4c. Platform-level Acceptance Criteria (01_BRD.md §45) that don't map to at least one FR

All 12 platform-level acceptance criteria in BRD §45 were checked individually against the FR catalog:

| AC # | Statement (paraphrased) | Maps to |
|---|---|---|
| AC-1 | Employee registers app without IT | Module D (FR-011–014) |
| AC-2 | Claude Code discovers capabilities via MCP | Module Y (FR-110–115) |
| AC-3 | Claude Code validates app via MCP, gets actionable feedback | Module H (FR-029–034), Module Y |
| AC-4 | Unsupported stack rejected at validation | Module H, Module F (FR-019–022) |
| AC-5 | Unauthorized request rejected by Platform API, independent of agent | Module H, Module A (FR-001–004), Module Z (FR-116–121) |
| AC-6 | Approved app deploys end-to-end via MCP to healthy Running state | Module J (FR-039–044), Module R (FR-082–085) |
| AC-7 | Internal app gets working URL automatically | Module P (FR-072–075) |
| AC-8 | Health checks run automatically before traffic activation | Module R (FR-082–085) |
| AC-9 | Stateless app scales 0→N→0, database stays available | Module L (FR-051–056), Module N (FR-061–065) |
| AC-10 | Cross-application access to secrets/DB is denied | Module O (FR-066–071), Module N, Module Q (FR-076–081) |
| AC-11 | Admins view deployment history and roll back | Module U (FR-094–097), Module V (FR-098–102) |
| AC-12 | All Section 29 audit-covered actions are recorded and retrievable | Module W (FR-103–106) |

**Result: zero gaps found.** Every one of the 12 platform-level acceptance criteria maps to at least one explicit FR module. This was verified by reading BRD §45 in full and cross-referencing each statement against the FR Table of Contents/module descriptions — this is not assumed.

### 4d. Summary

Two categories of real, evidence-based gaps were found (4a: 3–4 objectives thin on NFR anchoring, most notably BO-11 with zero NFR coverage; 4b: 5 FR modules with no named test scenario). No fabricated or template gaps are reported. **No zero-coverage gap exists at the Objective→FR level or the AC→FR level** — coverage is complete at those two layers; the real gaps sit one layer down, at Objective→NFR and FR-module→named-test-scenario.

---

## 5. Traceability Maintenance

This matrix is a **derived artifact**, not a source of truth — it must never be edited to "correct" a requirement; instead the source document (BRD, FR, NFR, System Requirements, or Test Strategy) is corrected and this matrix is regenerated from it. To keep it in sync as the platform moves through later phases (09_SDLC.md):

1. **Trigger for update.** Any of the following changes must trigger a review of this matrix within the same change/PR, not as separate follow-up work: a new or renumbered FR/NFR/BR/MOD is added; an FR module's ID range shifts; a Business Objective in BRD §6 is added, split, or reworded; a test type is added/removed in 14_Test_Strategy.md; a platform-level Acceptance Criterion in BRD §45 is added or changed.
2. **Ownership.** The Business Analyst (or whoever owns 01_BRD.md and 02_Functional_Requirements.md in a given phase) is accountable for Sections 2 and 3 of this matrix. The QA/Test Engineer lead (per 14_Test_Strategy.md §18) is accountable for keeping the Test Type columns and Section 4b current whenever test scenarios are added.
3. **No invented IDs, ever.** Every ID added to this matrix in future revisions must be copied from its authoritative source document, the same discipline used to build this version. If an ID is needed but doesn't exist yet, that is itself a finding for Section 4, not a reason to invent one.
4. **Re-run the reverse-check, not just the forward view.** It is easy to add a new FR under an existing objective and forget to ask "does this new FR module serve an objective at all?" Section 3 exists specifically to catch scope creep (modules with no objective) as well as unfunded ambition (objectives with no modules) — both directions should be re-verified on every non-trivial change, not just the direction being actively edited.
5. **Gap analysis is a living check, not a one-time report.** Section 4 should be re-run (re-grepped against the current state of the NFR index and Test Strategy example scenarios) at each major baseline, e.g., end of each SDLC phase in 09_SDLC.md, not only when this document is first authored. A gap closed in a later revision should be recorded as closed with the change that closed it, not silently deleted from the history of this document.
6. **Version this document like the others.** Any future revision should note, at minimum, the date, what changed, and which source document(s) drove the change — consistent with the Document Control convention used in 01_BRD.md §1.

---
