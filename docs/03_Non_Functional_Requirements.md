# 03 — Non-Functional Requirements

**Document ID:** NFR-DOC-03
**Project:** Company AI Application Deployment Platform
**Version:** 0.1 (Draft Baseline)
**Status:** For Review
**Owner:** Senior Solution Architect
**Date:** 2026-08-28
**Related Documents:** `01_BRD.md`, `02_Functional_Requirements.md`, `04_Business_Rules.md`, `06_System_Requirements.md`, `17_Decision_Log.md`

---

## 1. Purpose and How to Read This Document

This document defines measurable Non-Functional Requirements (NFRs) for the Company AI Application Deployment Platform: the quality attributes the platform must exhibit regardless of which specific feature is being exercised — performance, scalability, availability, reliability, security, maintainability, observability, auditability, recoverability, deployability, portability, usability, and accessibility.

Every NFR has an ID (`NFR-001`, `NFR-002`, …), assigned **sequentially across the whole document**, grouped by category. Category groupings do not restart the numbering.

**On numeric targets:** Where this document states a concrete number (a latency, a percentage, a duration) and the project context does not hand us that number directly, the target is explicitly labeled **"Proposed / for confirmation."** These are defensible engineering estimates for an internal platform serving a small-to-mid-size company, not committed SLAs. They exist so downstream design (`06_System_Requirements.md`, `10_System_Architecture.md`) has something concrete to build against, and so `14_Test_Strategy.md` has something concrete to test against. Every proposed number should be ratified (or revised) by Platform/IT/Security leadership and recorded in `17_Decision_Log.md` before it is treated as a contractual SLA.

Where a value is genuinely a business/compliance decision this document cannot responsibly estimate (e.g., audit retention driven by a regulation not yet identified, an org-wide financial SLA commitment), it is marked **TBD** with a one-line note on what input is required. TBD items are collected in Section 4.

**Priority** uses the standard scale: **MUST** (mandatory for the MVP baseline), **SHOULD** (important, may slip to a later phase under resource constraint), **COULD** (desirable enhancement), **WON'T** (explicitly out of scope for this baseline). Priority reflects whether the *requirement* is mandatory, not whether the *specific number* is final — a MUST requirement can still carry a proposed, not-yet-ratified target.

Functional behavior referenced here (e.g., "the `validate_application` MCP tool") is owned and detailed in `02_Functional_Requirements.md` (FR Modules A–AB) and `07_MCP_Requirements.md` — this document only attaches quality-attribute targets to that behavior, it does not redefine it. System module names (`MOD-01`…`MOD-19`) are owned in detail by `06_System_Requirements.md` and are referenced here only to indicate where a target is likely to be measured or enforced.

---

## 2. NFR Index

| ID | Title | Category | Priority |
|---|---|---|---|
| NFR-001 | `validate_application` response time | Performance | MUST |
| NFR-002 | `deploy_application` end-to-end time (dev, small tier) | Performance | MUST |
| NFR-003 | Build latency | Performance | SHOULD |
| NFR-004 | Scale-from-zero cold start latency | Performance | MUST |
| NFR-005 | MCP tool call latency and async pattern | Performance | MUST |
| NFR-006 | Platform API read-endpoint latency | Performance | SHOULD |
| NFR-007 | Concurrent deployment throughput | Performance | SHOULD |
| NFR-008 | Idle-to-zero timeout configurability | Scalability | MUST |
| NFR-009 | Horizontal scale bounds and org-wide ceiling | Scalability | MUST |
| NFR-010 | Registered-application capacity | Scalability | SHOULD |
| NFR-011 | Concurrent active session capacity | Scalability | SHOULD |
| NFR-012 | Persistent-service scaling independence | Scalability | MUST |
| NFR-013 | Application Catalog query performance at scale | Scalability | COULD |
| NFR-014 | Control-plane availability | Availability | MUST |
| NFR-015 | Hosted-application availability | Availability | SHOULD |
| NFR-016 | Scheduled maintenance windows | Availability | SHOULD |
| NFR-017 | MCP Server availability and graceful degradation | Availability | MUST |
| NFR-018 | Deployment success rate | Reliability | MUST |
| NFR-019 | Automatic retry for transient failures | Reliability | SHOULD |
| NFR-020 | MCP tool call idempotency | Reliability | MUST |
| NFR-021 | Health-check gating before traffic activation | Reliability | MUST |
| NFR-022 | Failure isolation / blast radius | Reliability | MUST |
| NFR-023 | Tenant / application isolation | Security | MUST |
| NFR-024 | Secret rotation | Security | MUST |
| NFR-025 | Image scan turnaround | Security | MUST |
| NFR-026 | Default-deny network isolation | Security | MUST |
| NFR-027 | Container/workload hardening compliance | Security | MUST |
| NFR-028 | Server-side policy re-validation | Security | MUST |
| NFR-029 | Supported-stack extensibility | Maintainability | MUST |
| NFR-030 | Modular, independently upgradable architecture | Maintainability | SHOULD |
| NFR-031 | Log ingestion-to-queryable latency | Observability | SHOULD |
| NFR-032 | Metrics retention | Observability | SHOULD |
| NFR-033 | Health-check interval/threshold configurability | Observability | MUST |
| NFR-034 | End-to-end correlation ID coverage | Observability | MUST |
| NFR-035 | Dashboard status freshness | Observability | SHOULD |
| NFR-036 | Audit log immutability | Auditability | MUST |
| NFR-037 | Audit log retention period | Auditability | MUST |
| NFR-038 | Audit query performance | Auditability | SHOULD |
| NFR-039 | Control-plane Recovery Time Objective (RTO) | Recoverability | MUST |
| NFR-040 | Application database Recovery Point Objective (RPO) | Recoverability | MUST |
| NFR-041 | Rollback Time Objective (RTO-rollback) | Recoverability | MUST |
| NFR-042 | Deployment history reconstructability | Recoverability | SHOULD |
| NFR-043 | Platform self-upgrade with minimal downtime | Deployability | SHOULD |
| NFR-044 | `deployment.yaml` schema backward compatibility | Deployability | MUST |
| NFR-045 | Infrastructure-agnostic deployment contract | Portability | MUST |
| NFR-046 | Replaceable container-platform implementation | Portability | SHOULD |
| NFR-047 | Self-service completion rate | Usability | SHOULD |
| NFR-048 | Actionable validation error messages | Usability | MUST |
| NFR-049 | Administration Portal / Catalog WCAG conformance | Accessibility | SHOULD |
| NFR-050 | Status never conveyed by color alone | Accessibility | MUST |
| NFR-051 | Frontend/backend implementation independence (addendum) | Portability | MUST |

---

## 3. Requirements Detail

### 3.1 Performance

#### NFR-001 — `validate_application` Response Time

| Field | Detail |
|---|---|
| Category | Performance |
| Statement | The platform must return `validate_application` results (stack compliance, `deployment.yaml` schema check, security policy pre-check) within a bounded time for a project of typical size. |
| Rationale | Employees and Claude Code iterate on validation repeatedly during development; slow feedback breaks the tight self-service loop the platform exists to enable. |
| Target / Metric | **Proposed:** P95 ≤ 5 seconds; P99 ≤ 10 seconds, for a project with ≤300 source files and one `deployment.yaml`. Larger projects scale linearly with a documented per-100-file allowance (TBD once real project size distribution is known). |
| Measurement Method | Synthetic timing harness run against representative sample apps per supported stack; timed at the MCP tool boundary (`validate_application` call → response), tracked in MOD-13 Monitoring. |
| Priority | MUST |

#### NFR-002 — `deploy_application` End-to-End Time (Dev, Small Tier)

| Field | Detail |
|---|---|
| Category | Performance |
| Statement | For a small-tier application deploying to the development environment, the platform must complete the full pipeline (validate → build → deploy → health check → traffic activation) within a bounded time, assuming a warm build cache. |
| Rationale | Deployment speed is a primary driver of the platform's value proposition (self-service replacing IT ticket turnaround measured in days). |
| Target / Metric | **Proposed:** P95 ≤ 3 minutes end-to-end for a single-service, small-tier app with warm dependency/layer cache; P95 ≤ 6 minutes on a cold cache. Production deployments exclude approval wait time from this measurement (approval latency is a governance metric, not a platform-performance metric). |
| Measurement Method | End-to-end timer from `deploy_application` acceptance to health-check pass, recorded per deployment in MOD-03 Deployment Manager and surfaced via MOD-13 Monitoring. |
| Priority | MUST |

#### NFR-003 — Build Latency

| Field | Detail |
|---|---|
| Category | Performance |
| Statement | The Build Engine must produce a deployable container image for a small-tier, single-service application within a bounded time. |
| Rationale | Build time is the largest controllable component of NFR-002; isolating it lets Build Engine performance be tuned/tested independently of deployment orchestration. |
| Target / Metric | **Proposed:** P95 ≤ 90 seconds with a warm dependency cache; P95 ≤ 4 minutes on a cold cache, for a small-tier service in any v1 supported runtime. |
| Measurement Method | Build Engine (MOD-05) internal timer, start-of-build to image-pushed-to-registry, sampled per build and reported to MOD-13 Monitoring. |
| Priority | SHOULD |

#### NFR-004 — Scale-from-Zero Cold Start Latency

| Field | Detail |
|---|---|
| Category | Performance |
| Statement | When a scale-to-zero-eligible service receives traffic while at zero instances, the platform must route the first request to a running instance within a bounded time. |
| Rationale | Cold start is the primary end-user-visible cost of scale-to-zero; an unbounded or excessive cold start undermines adoption of the feature and, if too slow, user trust in the platform generally. |
| Target / Metric | **Proposed:** P95 ≤ 10 seconds for a small-tier stateless service with a pre-pulled/cached image; P99 ≤ 20 seconds. Medium/large tiers and multi-container services may carry a higher, separately confirmed target (TBD). |
| Measurement Method | Synthetic idle-then-request probe scheduled by MOD-13 Monitoring against representative scale-to-zero apps; timer from first inbound request to first successful response. |
| Priority | MUST |

#### NFR-005 — MCP Tool Call Latency and Async Pattern

| Field | Detail |
|---|---|
| Category | Performance |
| Statement | Synchronous, read-oriented MCP tools (`get_platform_info`, `get_supported_stacks`, `get_application_status`, `get_deployment_status`, `get_application_logs`, `get_application_metrics`) must respond within a bounded time. Long-running tools (`deploy_application`, `rollback_application`, `create_application` with build) must acknowledge the request with a job/deployment handle quickly and report progress via polling or status tools, never block the AI agent's connection for the full pipeline duration. |
| Rationale | Claude Code's usefulness to the employee depends on the MCP not stalling the agent session; long synchronous calls also risk client-side timeouts unrelated to actual deployment health. |
| Target / Metric | **Proposed:** Synchronous read-tool P95 ≤ 2 seconds. Long-running tool acknowledgment (handle returned) P95 ≤ 1 second, independent of pipeline duration (see NFR-002/NFR-003 for the pipeline itself). |
| Measurement Method | MCP Server (MOD-16) request-level timing, tagged by tool name, aggregated in MOD-13 Monitoring. |
| Priority | MUST |

#### NFR-006 — Platform API Read-Endpoint Latency

| Field | Detail |
|---|---|
| Category | Performance |
| Statement | Platform API (MOD-17) read endpoints (status, list, get) must respond within a bounded time under nominal load. |
| Rationale | The Platform API underlies both the MCP Server and the Administration Portal; its latency is a lower bound on everything built above it. |
| Target / Metric | **Proposed:** P95 ≤ 500 ms under nominal load, defined as ≤50 requests/second aggregate against the Platform API. Exact nominal-load figure is an estimate pending real usage data. |
| Measurement Method | API gateway / Platform API instrumentation, aggregated per-endpoint in MOD-13 Monitoring; validated under load test in `14_Test_Strategy.md`. |
| Priority | SHOULD |

#### NFR-007 — Concurrent Deployment Throughput

| Field | Detail |
|---|---|
| Category | Performance |
| Statement | The platform must support a defined number of concurrent build-and-deploy pipelines company-wide without deployment requests queueing beyond a bounded wait time. |
| Rationale | As adoption grows, simultaneous deployments (e.g., many teams deploying near a release window) must not create disproportionate wait times that push employees back toward manual IT requests. |
| Target / Metric | **Proposed:** ≥10 concurrent build+deploy pipelines supported platform-wide at MVP scale, with queue wait P95 ≤ 60 seconds before a pipeline starts executing when at that concurrency ceiling. Exact ceiling should scale with confirmed org headcount/app count (TBD refinement). |
| Measurement Method | Build Engine / Deployment Controller queue-depth and wait-time metrics, load-tested per `14_Test_Strategy.md` and monitored in production via MOD-13. |
| Priority | SHOULD |

### 3.2 Scalability

#### NFR-008 — Idle-to-Zero Timeout Configurability

| Field | Detail |
|---|---|
| Category | Scalability |
| Statement | The idle period after which a scale-to-zero-eligible service scales from N back to 0 instances must be configurable per application, within IT-defined minimum and maximum bounds. |
| Rationale | A single fixed idle timeout cannot suit both a rarely-used internal tool (aggressive scale-down desired) and a moderately-used one where cold starts would be disruptive. |
| Target / Metric | **Proposed:** Configurable range 5–60 minutes, default 15 minutes if unspecified. Exact bounds are a policy decision for IT Administrator input (see `17_Decision_Log.md`). |
| Measurement Method | Configuration validated by Validation Engine (MOD-04) at deploy-time; actual scale-down timing verified against configured value by MOD-13 Monitoring scale-event logs. |
| Priority | MUST |

#### NFR-009 — Horizontal Scale Bounds and Org-Wide Ceiling

| Field | Detail |
|---|---|
| Category | Scalability |
| Statement | Each service scales horizontally between the `scaling.min` and `scaling.max` values declared in its `deployment.yaml`, bounded by a platform-enforced, tier-dependent maximum instance ceiling that no single application may exceed regardless of its own declared `max`. |
| Rationale | Per-app scaling limits protect shared platform capacity from a single misbehaving or unexpectedly popular application. |
| Target / Metric | TBD — exact per-tier ceiling values require capacity-planning input (available compute budget, expected concurrent app count) not available at requirements time. |
| Measurement Method | Resource Manager (MOD-07) enforces the ceiling at scale-out decision time; violations logged and alertable via MOD-13/MOD-15. |
| Priority | MUST |

#### NFR-010 — Registered-Application Capacity

| Field | Detail |
|---|---|
| Category | Scalability |
| Statement | The platform's control plane (Application Registry, Platform API, MCP Server) must sustain its stated performance targets (NFR-001, NFR-006) as the number of registered applications grows to a defined scale. |
| Rationale | An internal platform for a small-to-mid-size company should be sized realistically rather than assumed infinitely scalable; this bounds acceptable degradation. |
| Target / Metric | **Proposed:** No measurable degradation of NFR-001/NFR-006 targets up to 500 registered applications platform-wide. |
| Measurement Method | Load/scale testing against a synthetically populated Application Registry (MOD-02), per `14_Test_Strategy.md`. |
| Priority | SHOULD |

#### NFR-011 — Concurrent Active Session Capacity

| Field | Detail |
|---|---|
| Category | Scalability |
| Statement | The platform must support a defined number of concurrent active employee/AI-agent sessions (Administration Portal + MCP) without MCP tool latency (NFR-005) degrading beyond its stated targets. |
| Rationale | Adoption success means many employees using Claude Code against the platform simultaneously during business hours; the control plane must not become the bottleneck. |
| Target / Metric | **Proposed:** ≥100 concurrent active sessions at MVP scale without breaching NFR-005 targets. |
| Measurement Method | Load test simulating concurrent MCP and Portal sessions; production monitoring via MOD-13. |
| Priority | SHOULD |

#### NFR-012 — Persistent-Service Scaling Independence

| Field | Detail |
|---|---|
| Category | Scalability |
| Statement | Managed database and cache instances scale (or remain available) independently of their owning application's stateless workload instance count; they are never subject to the same scale-to-zero lifecycle. |
| Rationale | Direct architectural requirement — persistent services must remain available for administration, backup, and any always-on consumer regardless of application traffic. |
| Target / Metric | Database/cache availability is unaffected by the stateless workload's instance count at any value from 0 to `scaling.max`, measured as continuous connectivity during scale-to-zero test cycles. |
| Measurement Method | Scale-to-zero test suite (per `14_Test_Strategy.md`) asserts DB/cache reachability throughout a full 0→N→0 cycle. |
| Priority | MUST |

#### NFR-013 — Application Catalog Query Performance at Scale

| Field | Detail |
|---|---|
| Category | Scalability |
| Statement | The Application Catalog (MOD-19) must return search/browse results within a bounded time as the catalog grows. |
| Rationale | Catalog usability degrades adoption if browsing/discovering existing internal apps becomes slow as the org's app inventory grows. |
| Target / Metric | **Proposed:** P95 ≤ 1 second for catalog search/list queries up to 2,000 cataloged entries. |
| Measurement Method | Catalog query timing instrumented in MOD-19, load-tested against a synthetically populated catalog. |
| Priority | COULD |

### 3.3 Availability

#### NFR-014 — Control-Plane Availability

| Field | Detail |
|---|---|
| Category | Availability |
| Statement | The platform control plane (Platform API, MCP Server, Identity & Access Management) must be available to accept and process requests for a defined percentage of time, excluding scheduled maintenance (NFR-016). |
| Rationale | Control-plane downtime blocks *all* deployment activity platform-wide, unlike a single hosted application being down; this is the platform's most business-critical availability surface. |
| Target / Metric | **Proposed:** 99.5% monthly uptime (≈3h40m allowed downtime/month), excluding scheduled maintenance windows. This is a proposed internal target, not a contractual org-wide SLA — the exact committed SLA % is a business decision requiring executive/IT sign-off (see `17_Decision_Log.md`). |
| Measurement Method | Uptime monitoring (external + internal synthetic probes) against Platform API and MCP Server health endpoints, aggregated monthly via MOD-13 Monitoring. |
| Priority | MUST |

#### NFR-015 — Hosted-Application Availability

| Field | Detail |
|---|---|
| Category | Availability |
| Statement | The platform must provide the underlying infrastructure availability needed to run a hosted application reliably, distinct from and independent of the application's own code quality; hosted-application availability targets are differentiated by resource tier and environment, and a scale-to-zero service's cold-start delay (NFR-004) is not counted as unavailability. |
| Rationale | The platform is responsible for infrastructure uptime, not for bugs in employee-written application code; this distinction must be explicit so responsibility for an outage can be correctly attributed. |
| Target / Metric | **Proposed:** Infrastructure-attributable availability ≥99% for production-tier, always-on (`scaling.min` ≥ 1) applications; no formal availability target for development/staging or for scale-to-zero-configured applications (intermittent availability is by design). |
| Measurement Method | Synthetic health-check probes per application (MOD-11 Health Check Manager), with outage attribution (infrastructure vs. application-code) reviewed during incident postmortem. |
| Priority | SHOULD |

#### NFR-016 — Scheduled Maintenance Windows

| Field | Detail |
|---|---|
| Category | Availability |
| Statement | The platform must support planned maintenance with advance notice to affected Application Owners, distinct from unplanned outages. |
| Rationale | Predictable maintenance windows let teams avoid scheduling critical activity during platform work, and keep maintenance from counting against availability targets unfairly. |
| Target / Metric | **Proposed:** ≥5 business days advance notice for control-plane maintenance impacting deployments; maintenance scheduled outside core business hours where feasible. |
| Measurement Method | Notification module (MOD-15) delivery timestamp vs. maintenance start timestamp, audited. |
| Priority | SHOULD |

#### NFR-017 — MCP Server Availability and Graceful Degradation

| Field | Detail |
|---|---|
| Category | Availability |
| Statement | The MCP Server must meet the same availability target as the control plane (NFR-014) for write/deploy operations, and must degrade gracefully — keeping read-only status/info tools functional — during a partial platform outage rather than failing all tool calls uniformly. |
| Rationale | The MCP Server sits directly on Claude Code's critical path; graceful degradation (e.g., "you can still check status, but not deploy") is materially better for employee trust than a hard outage of every tool. |
| Target / Metric | Same as NFR-014 (99.5% proposed) for the MCP Server process itself; read-only tools (`get_platform_info`, `get_application_status`, `get_deployment_status`) must remain functional whenever their underlying data store is reachable, independent of Deployment Controller health. |
| Measurement Method | MCP Server synthetic probes per tool category (read vs. write), correlated with Deployment Controller health in MOD-13. |
| Priority | MUST |

### 3.4 Reliability

#### NFR-018 — Deployment Success Rate

| Field | Detail |
|---|---|
| Category | Reliability |
| Statement | Deployment attempts that pass validation must complete successfully (reach `Running` with passing health checks) at a defined minimum rate, excluding failures caused by the application's own code. |
| Rationale | A high platform-attributable failure rate would erode trust in self-service deployment and drive employees back to requesting manual IT help. |
| Target / Metric | **Proposed:** ≥98% platform-attributable deployment success rate, measured monthly, excluding application-code-caused failures (e.g., app crash on startup). |
| Measurement Method | Deployment Manager (MOD-03) outcome logging, classified success / platform-failure / app-failure, aggregated in MOD-13 Monitoring; this is also a KPI tracked in `01_BRD.md`. |
| Priority | MUST |

#### NFR-019 — Automatic Retry for Transient Failures

| Field | Detail |
|---|---|
| Category | Reliability |
| Statement | The platform must automatically retry transient infrastructure failures (e.g., a momentary registry push failure, a transient scheduling failure) a bounded number of times before surfacing a failure to the employee/AI agent. |
| Rationale | Many pipeline failures are transient infrastructure blips, not real problems; surfacing them immediately without retry produces unnecessary noise and manual re-triggering. |
| Target / Metric | **Proposed:** Up to 2 automatic retries with exponential backoff for classified-transient failures, before the deployment is marked `Failed` and reported. |
| Measurement Method | Deployment Controller (MOD-06) retry-attempt logging, reviewed against failure classification accuracy periodically. |
| Priority | SHOULD |

#### NFR-020 — MCP Tool Call Idempotency

| Field | Detail |
|---|---|
| Category | Reliability |
| Statement | State-changing MCP tools (`create_application`, `deploy_application`, `rollback_application`, `restart_application`, `delete_application`) must be idempotent with respect to client-side retries, using a request/idempotency key, so a network-level retry from the AI agent cannot trigger a duplicate deployment or duplicate side effect. |
| Rationale | AI agents and network clients retry on ambiguous failures (e.g., a timeout where the request may have actually succeeded); without idempotency this could trigger duplicate builds, duplicate deployments, or double-billing of resources. |
| Target / Metric | 100% of state-changing MCP tools support an idempotency key; a repeated call with the same key returns the original result rather than re-executing the action. |
| Measurement Method | Contract/integration tests in `14_Test_Strategy.md` (MCP Testing) asserting idempotent behavior for each state-changing tool. |
| Priority | MUST |

#### NFR-021 — Health-Check Gating Before Traffic Activation

| Field | Detail |
|---|---|
| Category | Reliability |
| Statement | A newly deployed version must pass a configurable health-check threshold before it receives production or shared traffic. |
| Rationale | Prevents routing live traffic to a broken deployment, reducing user-visible failures and unnecessary rollback churn. |
| Target / Metric | **Proposed default:** 3 consecutive successful health checks required before traffic activation; threshold configurable per application within IT-defined bounds. |
| Measurement Method | Health Check Manager (MOD-11) gate logic, verified in deployment pipeline tests. |
| Priority | MUST |

#### NFR-022 — Failure Isolation / Blast Radius

| Field | Detail |
|---|---|
| Category | Reliability |
| Statement | A single application's failure (crash loop, resource exhaustion, misbehaving traffic pattern) must not degrade the platform control plane's availability (NFR-014) or the availability of other, unrelated applications. |
| Rationale | Multi-tenant self-service platforms fail badly if one team's bug can take down everyone else; isolation is foundational to the platform's viability. |
| Target / Metric | Zero cross-application availability impact measured during fault-injection testing (e.g., an intentionally crash-looping test app must not affect sibling apps' health-check pass rate or control-plane latency beyond baseline variance). |
| Measurement Method | Chaos/fault-injection test suite (`14_Test_Strategy.md`), run pre-release and periodically in a non-production environment. |
| Priority | MUST |

### 3.5 Security

#### NFR-023 — Tenant / Application Isolation

| Field | Detail |
|---|---|
| Category | Security |
| Statement | No application may access another application's secrets, database, or internal network endpoints under normal operation. |
| Rationale | Directly required by the project's security context; multi-tenant isolation is a precondition for trusting the platform with any real internal application. |
| Target / Metric | 0 cross-application access events permitted (100% block rate) in automated isolation testing; any observed violation is treated as a Sev-1 security incident. |
| Measurement Method | Automated cross-tenant isolation test suite run on every platform release (network policy probes, secret-scope probes, DB-credential-scope probes). |
| Priority | MUST |

#### NFR-024 — Secret Rotation

| Field | Detail |
|---|---|
| Category | Security |
| Statement | Platform-managed secrets (database credentials, internal API keys/tokens) must be rotatable on a defined cadence, and immediately rotatable on suspected compromise, without requiring an application code change. |
| Rationale | Long-lived, never-rotated credentials are a standard finding in security reviews and a primary target once any single credential leaks. |
| Target / Metric | **Proposed:** default rotation interval 90 days for standard application secrets; immediate (≤1 hour) forced rotation capability on compromise, initiated by Security Administrator. Exact default cadence should be confirmed against company security policy. |
| Measurement Method | Secret Manager (MOD-08) rotation-event logging; rotation compliance reviewed by Security Administrator, reported to Management/Auditor. |
| Priority | MUST |

#### NFR-025 — Image Scan Turnaround

| Field | Detail |
|---|---|
| Category | Security |
| Statement | Every container image built by the platform must be scanned for known vulnerabilities as part of the deployment pipeline, within a bounded time, and deployment must be blocked on critical/high-severity findings. |
| Rationale | Supply-chain and dependency vulnerabilities are a named threat in the platform's threat model; scanning must be automatic and non-bypassable, not a manual, optional step. |
| Target / Metric | **Proposed:** P95 scan turnaround ≤ 60 seconds for a small-tier image; deployment blocked automatically on any unresolved critical or high-severity CVE, with Security Administrator-managed exception process for false positives. |
| Measurement Method | Build Engine (MOD-05) pipeline stage timing and pass/fail logging; scan results retained and auditable. |
| Priority | MUST |

#### NFR-026 — Default-Deny Network Isolation

| Field | Detail |
|---|---|
| Category | Security |
| Statement | Applications must be network-isolated from one another and from production/non-production counterparts by default (default-deny), with only explicitly required paths (e.g., an app to its own database) allowed. |
| Rationale | Reinforces NFR-023 at the network layer; default-deny is materially safer than default-allow-with-exceptions for a platform hosting many independently-owned applications. |
| Target / Metric | 100% of inter-application network paths denied by default; only declared, validated paths (own database, own cache, approved external egress) are permitted. |
| Measurement Method | Network policy audit / automated port-scan style isolation test, run per release and periodically in production. |
| Priority | MUST |

#### NFR-027 — Container/Workload Hardening Compliance

| Field | Detail |
|---|---|
| Category | Security |
| Statement | 100% of deployed application workloads must run as non-privileged containers, without host filesystem mounts and without Docker socket access, with no self-service override available. |
| Rationale | Directly required by the project's security context; this is treated as an absolute, not a best-effort, control. |
| Target / Metric | 0 privileged containers, 0 host-fs mounts, 0 Docker-socket mounts in production at any time — enforced as a hard admission-control gate, verified continuously (not only at deploy time). |
| Measurement Method | Policy-as-code admission gate in Deployment Controller (MOD-06); continuous compliance scan reported to Security Administrator and Audit (MOD-14). |
| Priority | MUST |

#### NFR-028 — Server-Side Policy Re-Validation

| Field | Detail |
|---|---|
| Category | Security |
| Statement | 100% of deployment-affecting requests must be independently authorized and policy-checked by the Platform API, regardless of any validation already performed client-side by the AI agent, the Company Deployment Skill, or the MCP Server. |
| Rationale | Direct architectural principle: never trust the AI agent as a security boundary. Client-side checks improve UX (fast feedback) but must never be the actual enforcement point. |
| Target / Metric | 100% of `deploy_application` (and other state-changing) calls pass through Platform API authorization/policy evaluation before reaching the Deployment Controller, with no code path that skips this step. |
| Measurement Method | Architecture/code review gate plus automated test asserting that a request bypassing MCP-side validation (simulating a compromised or buggy agent) is still correctly rejected server-side. |
| Priority | MUST |

### 3.6 Maintainability

#### NFR-029 — Supported-Stack Extensibility

| Field | Detail |
|---|---|
| Category | Maintainability |
| Statement | IT Administrators must be able to add or remove a supported technology (frontend/backend runtime, database, cache) from the Supported Stack list via configuration, without requiring a Platform API, MCP Server, or Validation Engine code change or redeploy. |
| Rationale | Directly required by the project context; stack governance must be operationally agile and decoupled from the platform's own release cycle. |
| Target / Metric | **Proposed:** A stack list change is effective platform-wide within 1 business day of approval, via configuration change plus IT Administrator approval workflow — no core-platform code deployment involved. |
| Measurement Method | Change-management audit trail (time from approval to effective) tracked via Stack Management configuration store and Audit (MOD-14). |
| Priority | MUST |

#### NFR-030 — Modular, Independently Upgradable Architecture

| Field | Detail |
|---|---|
| Category | Maintainability |
| Statement | System modules (MOD-01…MOD-19) must expose documented interfaces and be independently deployable/upgradable without requiring a simultaneous redeploy of unrelated modules. |
| Rationale | Reduces coupling-driven release risk and lets the platform team iterate on, e.g., the Build Engine without redeploying the Secret Manager. |
| Target / Metric | Qualitative architecture goal at v1, verified via dependency audit rather than a runtime metric; target for a future phase: measurable independent-deploy frequency per module (TBD, needs engineering velocity baseline once the platform team is staffed). |
| Measurement Method | Architecture/dependency review at each major design milestone (`10_System_Architecture.md`); module coupling reviewed at each release. |
| Priority | SHOULD |

### 3.7 Observability

#### NFR-031 — Log Ingestion-to-Queryable Latency

| Field | Detail |
|---|---|
| Category | Observability |
| Statement | Application and platform logs must become queryable within a bounded time after being emitted. |
| Rationale | Troubleshooting a live incident depends on logs being near-real-time; stale logs materially slow down both employee self-diagnosis and IT/Platform Administrator incident response. |
| Target / Metric | **Proposed:** P95 ≤ 30 seconds from log emission to queryable availability. |
| Measurement Method | Logging module (MOD-12) pipeline instrumentation, timestamp-diffed at ingestion vs. query availability, sampled continuously. |
| Priority | SHOULD |

#### NFR-032 — Metrics Retention

| Field | Detail |
|---|---|
| Category | Observability |
| Statement | Application and platform metrics must be retained at defined resolutions for defined durations to support both real-time troubleshooting and longer-term capacity/trend analysis. |
| Rationale | Short retention prevents trend analysis (capacity planning, KPI tracking per `01_BRD.md`); unbounded high-resolution retention is a cost the platform must not silently absorb. |
| Target / Metric | **Proposed:** 30 days at native/high resolution, downsampled and retained for 13 months for trend/KPI reporting. Exact retention durations and associated storage cost are subject to IT/Finance confirmation (TBD). |
| Measurement Method | Monitoring module (MOD-13) retention policy configuration, audited quarterly against actual storage usage and cost. |
| Priority | SHOULD |

#### NFR-033 — Health-Check Interval/Threshold Configurability

| Field | Detail |
|---|---|
| Category | Observability |
| Statement | Health-check interval and failure threshold must be configurable per application within platform-defined bounds, with a sane default applied when unspecified. |
| Rationale | Different applications have different acceptable detection latency vs. check-overhead tradeoffs; a single hardcoded interval either wastes resources or detects failures too slowly. |
| Target / Metric | **Proposed default:** 15-second interval, 3-consecutive-failure threshold before an instance is marked unhealthy and removed from rotation. |
| Measurement Method | Health Check Manager (MOD-11) configuration validation at deploy-time; actual check cadence verified in monitoring data. |
| Priority | MUST |

#### NFR-034 — End-to-End Correlation ID Coverage

| Field | Detail |
|---|---|
| Category | Observability |
| Statement | Every deployment/scale/failure event must carry a correlation/trace identifier that is propagated across the full request path (Employee → Claude Code → Company Deployment Skill → MCP → Platform API → Deployment Engine → Container Platform), so a single action can be traced end-to-end across module logs. |
| Rationale | Without a shared correlation ID, diagnosing a failure across seven architectural hops requires manual log correlation by timestamp, which is slow and error-prone. |
| Target / Metric | 100% of deployment-affecting requests carry a correlation ID present in every module's log/event output for that request. |
| Measurement Method | Log schema conformance check (correlation ID field presence) enforced across MOD-12/MOD-13/MOD-14 log/event schemas; verified in integration tests. |
| Priority | MUST |

#### NFR-035 — Dashboard Status Freshness

| Field | Detail |
|---|---|
| Category | Observability |
| Statement | Application and deployment status shown in the Administration Portal and Application Catalog must reflect the underlying state within a bounded time of a state change, without requiring the viewer to manually refresh for correctness. |
| Rationale | Stale dashboards erode trust in the platform and cause unnecessary support inquiries ("is my deploy actually done?"). |
| Target / Metric | **Proposed:** P95 ≤ 15 seconds from state change to dashboard reflection (near-real-time; true push/streaming is not required at v1). |
| Measurement Method | Synthetic state-change-to-dashboard-update timing test, run periodically against the Administration Portal (MOD-18). |
| Priority | SHOULD |

### 3.8 Auditability

#### NFR-036 — Audit Log Immutability

| Field | Detail |
|---|---|
| Category | Auditability |
| Statement | Audit records must be write-once and tamper-evident; no standard role, including Platform Administrator, may modify or delete an audit entry through normal platform operation. |
| Rationale | An editable audit trail cannot support incident investigation, compliance review, or management oversight with any confidence. |
| Target / Metric | 100% of audit records immutable post-write; no update/delete API exposed to any standard role; any exceptional legal-hold export/redaction procedure is itself separately logged. |
| Measurement Method | Audit module (MOD-14) storage design review (append-only / hash-chained or equivalent tamper-evidence); periodic integrity verification job. |
| Priority | MUST |

#### NFR-037 — Audit Log Retention Period

| Field | Detail |
|---|---|
| Category | Auditability |
| Statement | Audit records must be retained for a defined minimum period sufficient to support incident investigation and compliance review. |
| Rationale | Retention period is typically driven by regulatory/compliance requirements specific to the company's industry, which this document is not positioned to determine. |
| Target / Metric | **TBD** — exact retention period requires Legal/Compliance input on applicable regulatory requirements. **Interim proposed default: 1 year**, to be confirmed and recorded in `17_Decision_Log.md`. |
| Measurement Method | Audit module retention policy configuration; retention compliance reviewed periodically by Security Administrator / Management/Auditor. |
| Priority | MUST |

#### NFR-038 — Audit Query Performance

| Field | Detail |
|---|---|
| Category | Auditability |
| Statement | Authorized users (Security Administrator, Management/Auditor) must be able to query the audit trail for a given application, user, or time range and receive results within a bounded time, across the full retention window. |
| Rationale | An audit trail that is too slow to query under investigation pressure (e.g., during an active incident) fails its purpose regardless of how complete it is. |
| Target / Metric | **Proposed:** P95 ≤ 5 seconds for a scoped query (single application or single user, bounded time range) across the full retention window. |
| Measurement Method | Audit module (MOD-14) query timing instrumentation, tested against a representative-scale synthetic audit dataset. |
| Priority | SHOULD |

### 3.9 Recoverability

#### NFR-039 — Control-Plane Recovery Time Objective (RTO)

| Field | Detail |
|---|---|
| Category | Recoverability |
| Statement | Following a control-plane outage (Platform API, MCP Server, IAM, or their underlying data stores), the platform must be restorable to normal operation within a defined time. |
| Rationale | Control-plane RTO determines how long the *entire platform* is unable to accept new deployments or serve status queries — the single highest-impact recovery scenario. |
| Target / Metric | **TBD** — exact RTO is a business-continuity decision requiring input on acceptable organizational risk. **Proposed interim target: ≤4 hours**, pending confirmation. |
| Measurement Method | Disaster-recovery drill (`14_Test_Strategy.md` — Failure Recovery Testing), timed from declared outage to restored service. |
| Priority | MUST |

#### NFR-040 — Application Database Recovery Point Objective (RPO)

| Field | Detail |
|---|---|
| Category | Recoverability |
| Statement | Managed application databases must be backed up such that data loss in a recovery scenario is bounded to a defined maximum window. |
| Rationale | RPO directly determines acceptable backup frequency; the right value depends on each application's actual data criticality, which the platform does not yet formally classify. |
| Target / Metric | **TBD** — exact RPO requires a data-criticality classification scheme per application/department (not yet defined). **Proposed interim default: ≤24 hours (daily backup) for standard-tier applications**; critical-tier applications should carry a tighter, separately confirmed RPO. |
| Measurement Method | Database Manager (MOD-09) backup-job scheduling and completion logging; restore drills verify actual achievable RPO periodically. |
| Priority | MUST |

#### NFR-041 — Rollback Time Objective

| Field | Detail |
|---|---|
| Category | Recoverability |
| Statement | The `rollback_application` operation must restore an application to its previous known-good version within a bounded time. |
| Rationale | Rollback is the platform's primary defense against a bad deployment; if it is too slow, teams will be tempted to hand-patch production instead, undermining the standardized pipeline. |
| Target / Metric | **Proposed:** P95 ≤ 2 minutes for a small-tier application, using a previously-built image (no rebuild required). |
| Measurement Method | Deployment Manager (MOD-03) timer from `rollback_application` acceptance to health-check pass on the restored version. |
| Priority | MUST |

#### NFR-042 — Deployment History Reconstructability

| Field | Detail |
|---|---|
| Category | Recoverability |
| Statement | The platform must retain sufficient deployment manifest and configuration history to fully reconstruct the state of any of an application's recent deployed versions. |
| Rationale | Reconstructability underpins both rollback (NFR-041) and audit/compliance investigation into "what was actually running at time X." |
| Target / Metric | **Proposed:** Minimum of the last 10 deployed versions per application fully reconstructable (manifest + build artifact reference + configuration), independent of the general audit retention period (NFR-037). |
| Measurement Method | Deployment History data entity completeness check; periodic reconstruction test against retained history. |
| Priority | SHOULD |

### 3.10 Deployability

#### NFR-043 — Platform Self-Upgrade with Minimal Downtime

| Field | Detail |
|---|---|
| Category | Deployability |
| Statement | The platform's own control-plane components must be upgradable with minimal disruption to in-flight and new deployment requests, and platform-component rollback must be possible independently of any tenant application's rollback. |
| Rationale | The platform is itself a piece of software that must evolve; its own release process should not force company-wide deployment freezes, and a bad platform release must be recoverable without affecting tenant application state. |
| Target / Metric | **Proposed:** ≤5 minutes of control-plane downtime per platform release, performed outside core business hours where feasible; platform component rollback achievable independently of tenant `rollback_application` operations. |
| Measurement Method | Release runbook timing record; platform release rollback drill performed pre-production-release. |
| Priority | SHOULD |

#### NFR-044 — `deployment.yaml` Schema Backward Compatibility

| Field | Detail |
|---|---|
| Category | Deployability |
| Statement | Changes to the `deployment.yaml` schema must remain backward-compatible with previously accepted schema versions for a defined support window, so existing applications and the Company Deployment Skill are not broken by a platform update. |
| Rationale | The deployment contract is the single most important interface between employees/AI agents and the platform; breaking it silently would be a severe, hard-to-diagnose failure mode across many applications at once. |
| Target / Metric | A given `deployment.yaml` schema version must remain accepted (validate + deploy successfully) for at least 2 prior major schema versions after a new version is introduced, with deprecation notices issued before removal. |
| Measurement Method | Schema version compatibility test suite run against archived example manifests from each supported schema version, per platform release. |
| Priority | MUST |

### 3.11 Portability

#### NFR-045 — Infrastructure-Agnostic Deployment Contract

| Field | Detail |
|---|---|
| Category | Portability |
| Statement | The `deployment.yaml` application-level contract must describe only application requirements and must not reference Kubernetes, Knative, Docker, Nginx, or any other specific infrastructure technology by name or field shape. |
| Rationale | Directly required by the project's core abstraction principle; keeping the contract infra-neutral is what allows the underlying container platform to change without breaking every application's deployment definition. |
| Target / Metric | 100% of supported `deployment.yaml` fields are infrastructure-neutral (describe application intent: runtime, scaling bounds, resource tier, visibility — never a raw infra construct), verified by schema design review. |
| Measurement Method | Schema design review at each schema revision (`06_System_Requirements.md`, `13_API_Requirements.md`); automated schema linter rejecting infra-specific field additions. |
| Priority | MUST |

#### NFR-046 — Replaceable Container-Platform Implementation

| Field | Detail |
|---|---|
| Category | Portability |
| Statement | The underlying container/runtime platform implementation (e.g., a K3s + Knative candidate evaluated in `10_System_Architecture.md`) must be replaceable without requiring changes to the application-level `deployment.yaml` contract or to employee/AI-agent-facing workflows. |
| Rationale | Avoids irreversible lock-in to a single infrastructure choice made early in the platform's life, consistent with the project's instruction to treat infrastructure technology as an implementation detail. |
| Target / Metric | Architecture design goal, validated at design review rather than measured at runtime for v1; a future phase may validate this empirically via a second reference implementation (COULD, out of v1 scope). |
| Measurement Method | Architecture review confirming the Deployment Controller (MOD-06) is the sole component with infrastructure-specific knowledge, isolated behind its module interface. |
| Priority | SHOULD |

### 3.12 Usability

#### NFR-047 — Self-Service Completion Rate

| Field | Detail |
|---|---|
| Category | Usability |
| Statement | An employee/AI agent must be able to go from a validated, supported-stack application to a running development deployment without filing an IT ticket, for a defined proportion of standard-stack applications. |
| Rationale | This is the platform's core value proposition; usability is measured here as "does self-service actually work end-to-end," not subjective ease-of-use. |
| Target / Metric | **Proposed:** ≥95% of standard-stack, dev-environment deployment attempts complete without any IT ticket or manual intervention. This aligns with the KPI "percentage of deployments completed without IT intervention" in `01_BRD.md`. |
| Measurement Method | Cross-referenced deployment logs (MOD-03) against IT ticketing system records; tracked as a platform KPI. |
| Priority | SHOULD |

#### NFR-048 — Actionable Validation Error Messages

| Field | Detail |
|---|---|
| Category | Usability |
| Statement | When `validate_application` or `deploy_application` fails, the returned error must identify the specific failing field, rule, or policy, not a generic failure message, so the employee or AI agent can self-correct without escalation. |
| Rationale | Vague errors are a leading cause of employees abandoning self-service and filing an IT ticket instead; this directly supports NFR-047. |
| Target / Metric | **Proposed:** ≥90% of validation failures include a specific field/rule reference and a remediation hint, measured via error-message content review and support-ticket root-cause analysis. |
| Measurement Method | Validation Engine (MOD-04) error schema conformance check; periodic sampling review of real validation failure messages. |
| Priority | MUST |

### 3.13 Accessibility

#### NFR-049 — Administration Portal / Catalog WCAG Conformance

| Field | Detail |
|---|---|
| Category | Accessibility |
| Statement | The Administration Portal (MOD-18) and Application Catalog (MOD-19) web interfaces must conform to WCAG 2.1 Level AA for keyboard navigation, color contrast, and screen-reader compatibility. |
| Rationale | These are the platform's primary human-facing web UIs (used by Platform/IT/Security Administrators, Application Owners, and Management/Auditor); accessibility is both good practice and, in many jurisdictions, a compliance expectation for internal enterprise tools. |
| Target / Metric | WCAG 2.1 AA conformance, verified against automated accessibility scanning plus a manual audit pass before general availability. |
| Measurement Method | Automated accessibility scan (e.g., axe-core class tooling) integrated into the UI build/test pipeline; manual audit prior to major releases. |
| Priority | SHOULD |

#### NFR-050 — Status Never Conveyed by Color Alone

| Field | Detail |
|---|---|
| Category | Accessibility |
| Statement | Status indicators across the platform — Administration Portal, Application Catalog, and MCP tool/CLI-style output surfaced to the AI agent — must convey status (e.g., healthy/degraded/failed) through explicit text or iconography, never through color alone. |
| Rationale | Color-only status is inaccessible to color-blind users and is also meaningless to a text-based AI agent consumer of MCP responses — this requirement serves both human accessibility and machine-readability. |
| Target / Metric | 100% of status-bearing UI elements and MCP tool response fields include an explicit status string, not color/icon alone. |
| Measurement Method | UI/UX design review checklist item; MCP response schema review confirming explicit status enum fields. |
| Priority | MUST |

### 3.13 Portability Addendum (Post-Baseline)

#### NFR-051 — Frontend/Backend Implementation Independence

| Field | Detail |
|---|---|
| Category | Portability |
| Statement | The Frontend Admin Portal (and any other Business API consumer) must interact with the platform exclusively through the versioned Business API contract (`13_API_Requirements.md`). The backend implementation behind that contract — Platform API, MCP Server, Deployment Controller, or their internal language/framework/service boundaries — must be changeable, replaced, or re-architected without requiring a change to the Frontend Admin Portal, as long as the API contract version served is unchanged. |
| Rationale | Confirmed as an explicit architecture constraint (2026-08-28): the backend runs self-hosted on Docker/K3s+Knative (see resolved `DEC-004` in `17_Decision_Log.md`), not on a managed cloud platform, and the platform must be free to evolve backend services independently of the Admin frontend. This extends NFR-030 (module independence) and NFR-046 (replaceable container platform) to explicitly cover the frontend/backend boundary, which those two do not name directly. |
| Target / Metric | 100% of Frontend Admin Portal functionality is implemented against Business API endpoints only — zero direct calls from the frontend to the Deployment Controller, container platform, or any internal-only API. Verified by API-boundary review at each release. |
| Measurement Method | Architecture/dependency review (`10_System_Architecture.md`) confirming the Frontend Admin Portal has no import/network dependency on internal infrastructure APIs; API contract versioning policy (`13_API_Requirements.md`) enforced via consumer-driven contract checks in `14_Test_Strategy.md`. |
| Priority | MUST |

Added after the original baseline in response to an explicit architecture directive; numbered NFR-051 to preserve the existing NFR-001–NFR-050 sequence rather than renumbering.

---

## 4. TBD Items Requiring a Decision

The following targets in this document are marked TBD because they depend on a business, compliance, or capacity-planning decision this document cannot responsibly make on its own. Each will be tracked as an open item in `17_Decision_Log.md`.

| NFR | Open Question | Input Needed From |
|---|---|---|
| NFR-009 | Exact per-tier org-wide instance ceiling | Platform Administrator + capacity planning |
| NFR-014 | Contractual (not proposed) control-plane availability SLA % | Executive / IT leadership |
| NFR-032 | Final metrics retention durations and associated cost | IT Administrator + Finance |
| NFR-037 | Audit log retention period | Legal / Compliance |
| NFR-039 | Control-plane RTO | Business continuity / executive risk tolerance |
| NFR-040 | Application database RPO, and the underlying data-criticality classification scheme | Application Owners + Security Administrator |
| NFR-030 | Per-module independent-release velocity target | Engineering leadership, once platform team is staffed |

All other numeric targets in this document are **proposed engineering estimates** appropriate for an internal, small-to-mid-size company platform, offered so downstream design and test planning has a concrete baseline — they should be explicitly ratified (or revised) rather than silently treated as final.

---

## 5. Glossary of Measurement Terms

| Term | Meaning |
|---|---|
| **P50 / P95 / P99** | Percentile latency: P95 means 95% of measured requests/operations complete at or below the stated value (the remaining 5% may be slower). P99 is the stricter, tail-latency version. Percentiles are used instead of averages because averages hide bad tail experiences. |
| **RPO (Recovery Point Objective)** | The maximum acceptable amount of data loss, measured as time — e.g., an RPO of 24 hours means at most the last 24 hours of data may be lost in a recovery scenario. |
| **RTO (Recovery Time Objective)** | The maximum acceptable time to restore a system to operation after an outage. |
| **MTTR (Mean Time To Recovery)** | The average elapsed time from an incident's detection to its resolution/service restoration; a reliability/operations KPI distinct from RTO (which is a target, while MTTR is an observed average). |
| **Cold Start** | The added latency incurred when a scale-to-zero service must start a new instance to serve a request, versus serving from an already-running (warm) instance. |
| **Idempotency Key** | A client-supplied unique identifier attached to a state-changing request so that repeating the same request (e.g., after a network retry) does not repeat its side effects. |
| **Blast Radius** | The scope of systems/tenants/users affected by a single failure; "failure isolation" aims to keep blast radius limited to the failing component only. |
| **Tamper-Evident** | A data store design (e.g., append-only logs, hash-chaining) where any unauthorized modification of past records is detectable, even if not technically impossible to attempt. |
| **WCAG** | Web Content Accessibility Guidelines — the W3C standard used to assess web interface accessibility; "Level AA" is the commonly required conformance tier for enterprise software. |
| **SLA / SLO** | Service Level Agreement (a committed, often contractual target) vs. Service Level Objective (an internal target used to manage toward an SLA). Most numeric targets in this document are proposed SLOs pending ratification as SLAs. |

---

*End of document. See `04_Business_Rules.md` for the invariant policy constraints that govern platform behavior, and `06_System_Requirements.md` for the system modules referenced throughout this document.*
