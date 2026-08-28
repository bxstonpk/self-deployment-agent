# Company AI Application Deployment Platform — Documentation Baseline

## What this is

This folder is the complete **Business Analysis + Requirements + SDLC + System Design** baseline for the *Company AI Application Deployment Platform* — an internal, self-service platform that lets employees deploy applications (built with the help of Claude Code, an AI coding agent) through a standardized, IT-governed pipeline, instead of every application requiring hands-on IT deployment:

```
Employee → Claude Code → Company Deployment Skill → Company Deployment MCP
         → Company Platform API → Deployment Engine → Container Platform → Application
```

This is **not** an implementation. No source code, no Kubernetes manifests, no Docker Compose files, and no MCP server exist yet. These 17 documents are the foundation the next phase (Technical Design → Development) will build from. Every place a real decision was needed but couldn't be responsibly invented is marked **TBD** and captured as an open question in [17_Decision_Log.md](17_Decision_Log.md) — nothing was silently assumed.

## Document set

| # | Document | Contains |
|---|---|---|
| 01 | [01_BRD.md](01_BRD.md) | The master Business Requirements Document — all 46 sections: executive summary, objectives, scope, stakeholders, the 7 user personas, AS-IS/TO-BE process, and roll-ups of every other document's content. **Start here.** |
| 02 | [02_Functional_Requirements.md](02_Functional_Requirements.md) | 129 functional requirements (FR-001–FR-129) across 28 modules (Authentication → Reporting), each with full flows, business rules, and acceptance criteria |
| 03 | [03_Non_Functional_Requirements.md](03_Non_Functional_Requirements.md) | 50 measurable NFRs (NFR-001–NFR-050) across 13 categories (Performance, Scalability, Security, Observability, etc.) |
| 04 | [04_Business_Rules.md](04_Business_Rules.md) | 35 invariant policy rules (BR-001–BR-035) — ownership, stack policy, approval policy, isolation rules |
| 05 | [05_Process_Flows.md](05_Process_Flows.md) | AS-IS vs TO-BE flow diagrams, the Application Lifecycle state diagram, the Deployment Lifecycle sequence (with failure/rollback branches), and the production approval gate flow |
| 06 | [06_System_Requirements.md](06_System_Requirements.md) | The 19 system modules (MOD-01–MOD-19) — Identity & Access through Application Catalog — with responsibilities, interactions, and observability requirements |
| 07 | [07_MCP_Requirements.md](07_MCP_Requirements.md) | The Company Deployment MCP as a business-capability interface: auth, authorization, audit, error handling, and full spec for all 12 MCP tools |
| 08 | [08_Company_Deployment_Skill.md](08_Company_Deployment_Skill.md) | The Company Deployment Skill Claude Code reads — structure, behavior contract, guardrails, worked example |
| 09 | [09_SDLC.md](09_SDLC.md) | The 14-phase SDLC plan for building *this platform*, with roles, entry/exit criteria, and quality gates per phase |
| 10 | [10_System_Architecture.md](10_System_Architecture.md) | Logical architecture (Control Plane / Data Plane / AI Interface / Application Runtime), deploy sequence diagram, and the infrastructure evaluation (Docker Compose vs K3s+Kubernetes vs K3s+Knative vs Managed Container Platform) with a recommendation |
| 11 | [11_Security_Requirements.md](11_Security_Requirements.md) | Authn/authz/RBAC/secrets/network isolation/container security requirements, plus a full threat model (THREAT-001–THREAT-018) |
| 12 | [12_Data_Requirements.md](12_Data_Requirements.md) | The 20 core entities (ENT-01–ENT-20) with attributes, relationships, lifecycle, and data classification |
| 13 | [13_API_Requirements.md](13_API_Requirements.md) | Business API vs MCP Interface vs Internal Infrastructure API separation, and illustrative endpoint groups |
| 14 | [14_Test_Strategy.md](14_Test_Strategy.md) | Strategy for all 14 test types, with platform-specific depth on MCP testing, scale-to-zero/cold-start, and rollback/failure recovery |
| 15 | [15_Traceability_Matrix.md](15_Traceability_Matrix.md) | The Requirements Traceability Matrix: Business Objective → FR → NFR → System Module → Test Type → Acceptance Criteria, plus a gap analysis |
| 16 | [16_Risk_Register.md](16_Risk_Register.md) | 30 business/project/operational risks (RISK-001–RISK-030), ranked, with mitigation and ownership |
| 17 | [17_Decision_Log.md](17_Decision_Log.md) | 26 open decisions (DEC-001–DEC-026) that Management/IT/Security must answer before later phases can proceed |

## Suggested reading order

1. **[01_BRD.md](01_BRD.md)** — read fully first; it's the single source of business truth and links out to everything else.
2. **[10_System_Architecture.md](10_System_Architecture.md)** then **[07_MCP_Requirements.md](07_MCP_Requirements.md)** — the architectural core: how the AI agent, MCP, and platform relate, and why the MCP is never a security boundary.
3. **[11_Security_Requirements.md](11_Security_Requirements.md)** — the threat model and controls that everything else is designed around.
4. **[02](02_Functional_Requirements.md) / [03](03_Non_Functional_Requirements.md) / [04](04_Business_Rules.md)** — the detailed requirement sets, as needed.
5. **[05](05_Process_Flows.md) / [06](06_System_Requirements.md) / [08](08_Company_Deployment_Skill.md) / [12](12_Data_Requirements.md) / [13](13_API_Requirements.md)** — supporting design detail.
6. **[09_SDLC.md](09_SDLC.md)** and **[14_Test_Strategy.md](14_Test_Strategy.md)** — how this gets built and verified.
7. **[15_Traceability_Matrix.md](15_Traceability_Matrix.md)**, **[16_Risk_Register.md](16_Risk_Register.md)**, **[17_Decision_Log.md](17_Decision_Log.md)** — governance: coverage proof, risk, and open questions.

## How the documents relate

`01_BRD.md` is the hub — every other document either supplies detail the BRD summarizes, or consumes an ID scheme the BRD references. IDs are owned by exactly one document each, so no two files invent conflicting numbers for the same thing:

- **FR-001…FR-129** → owned by `02_Functional_Requirements.md`
- **NFR-001…NFR-050** → owned by `03_Non_Functional_Requirements.md`
- **BR-001…BR-035** → owned by `04_Business_Rules.md`
- **MOD-01…MOD-19** (system modules) → owned by `06_System_Requirements.md`
- **ENT-01…ENT-20** (data entities) → owned by `12_Data_Requirements.md`
- **THREAT-001…THREAT-018** (security threats) → owned by `11_Security_Requirements.md`
- **RISK-001…RISK-030** (business/project risk — distinct from THREAT-xxx) → owned by `16_Risk_Register.md`
- **DEC-001…DEC-026** (open decisions) → owned by `17_Decision_Log.md`

`15_Traceability_Matrix.md` is the only document that cross-references all of the above at once, to prove every Business Objective in the BRD has real, traceable requirement, module, and test coverage — and to name the few places it doesn't (see its Gap Analysis section).

## What's still open (TBD)

Nothing in this baseline invents a business decision it wasn't given. **26 open decisions** are logged in [17_Decision_Log.md](17_Decision_Log.md), grouped as:

- **Identity & Access (3)** — SSO/IdP choice, RBAC source of truth, MCP authentication mechanism
- **Infrastructure & Hosting (6)** — final infra choice (Docker Compose / K3s+Kubernetes / K3s+Knative / Managed), registry & image scanning tooling, secret backend, DNS/TLS, environment topology, multi-region HA
- **Compliance & Data (4)** — applicable regulatory framework, data residency, data classification policy, whether external/public visibility is allowed in v1
- **Financial / Quota (3)** — resource quota tiers/numbers, budget ceiling, chargeback model
- **Process & Governance (6)** — production approval workflow, notification channels, stack-governance process, ownership transfer on employee departure, human-in-the-loop policy for AI-initiated deploys, MVP pilot scope
- **Operational (4)** — scale-to-zero idle timeout, availability/SLA targets, RPO/RTO, on-call/support model

The [15_Traceability_Matrix.md](15_Traceability_Matrix.md) gap analysis additionally flags: the "automatic domain/URL assignment" business objective currently has no dedicated NFR, and five lower-priority FR modules (User Management, Organization/Department, Application Ownership, Administration, Reporting) aren't yet named in explicit test scenarios in `14_Test_Strategy.md` (they're covered only by the general unit-testing statement). Both are good candidates for a follow-up pass before Technical Design.

## Document status

All 17 documents are Draft, dated 2026-08-28, produced in the Discovery / Requirements Analysis / Solution Architecture phases of `09_SDLC.md`. None have been reviewed or approved by Management, IT, Platform Administration, or Security. Treat every TBD and every "Recommendation — pending sign-off" as exactly that until a named Decision Owner from `17_Decision_Log.md` accepts it.
