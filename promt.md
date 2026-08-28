You are a Senior Business Analyst, Solution Architect, Product Owner,
DevOps Architect, and SDLC Specialist.

Your task is to analyze and document a new internal company platform called:

"Company AI Application Deployment Platform"

The platform is an internal self-service application deployment platform
designed for employees who use AI coding agents such as Claude Code.

==================================================
1. BUSINESS CONTEXT
==================================================

The company currently allows employees to use Claude Code to analyze
requirements, write code, modify applications, test applications,
and build small internal tools for their own work.

This approach is already working.

However, there is a major operational problem:

Employees can create applications using different technologies,
frameworks, databases, infrastructure patterns, and deployment methods.

Some technologies may not be supported by the company's infrastructure.

If every employee asks IT to manually deploy, configure, maintain,
monitor, secure, and troubleshoot each application, the IT workload
will increase significantly.

Therefore, the company wants to create an internal Application Platform
that allows employees and AI coding agents to deploy applications
through a standardized and controlled deployment process.

The target experience is:

Employee
    ↓
Claude Code
    ↓
Company Deployment Skill
    ↓
Company Deployment MCP
    ↓
Company Platform API
    ↓
Deployment Engine
    ↓
Container Platform
    ↓
Application

The employee should not need to understand infrastructure details
such as Docker, Kubernetes, K3s, Knative, Nginx, reverse proxy,
network configuration, TLS certificates, DNS, container networking,
or infrastructure security.

These infrastructure concerns must be abstracted by the platform.

==================================================
2. CORE BUSINESS OBJECTIVE
==================================================

The primary objective is:

"Allow employees to create and deploy approved internal applications
using AI coding agents with minimal IT intervention."

The platform must:

1. Standardize application deployment.
2. Reduce IT deployment workload.
3. Allow employees to self-service deploy applications.
4. Support AI-assisted development through Claude Code.
5. Provide a Company Deployment Skill that instructs AI agents.
6. Provide a Company Deployment MCP Server that exposes controlled
   deployment capabilities to AI agents.
7. Enforce supported technology stacks.
8. Enforce security and infrastructure policies.
9. Automatically provision required resources.
10. Automatically configure application networking.
11. Automatically configure application URLs/domains.
12. Support application health checks.
13. Support logging and monitoring.
14. Support deployment versioning.
15. Support rollback.
16. Support resource limits and quotas.
17. Support scale-to-zero for suitable applications.
18. Keep databases and persistent services separate from stateless
    application workloads.
19. Prevent AI agents from directly manipulating infrastructure.
20. Keep infrastructure implementation details hidden from employees
    and AI agents.

==================================================
3. IMPORTANT ARCHITECTURAL PRINCIPLE
==================================================

The platform must use the following abstraction:

AI Agent
    ↓
MCP
    ↓
Platform API
    ↓
Deployment Controller
    ↓
Infrastructure

The MCP Server must NOT directly expose low-level infrastructure
operations such as:

- kubectl
- Docker daemon control
- host filesystem access
- arbitrary container execution
- arbitrary network configuration
- arbitrary Kubernetes resource creation

Instead, the MCP should expose high-level business capabilities such as:

- get_platform_info
- get_supported_stacks
- validate_application
- create_application
- deploy_application
- get_deployment_status
- get_application_status
- get_application_logs
- get_application_metrics
- rollback_application
- restart_application
- delete_application

The platform must enforce authorization and policy independently
from the AI agent.

Never trust the AI agent as a security boundary.

==================================================
4. SCALE-TO-ZERO REQUIREMENT
==================================================

Scale-to-zero is a major platform requirement.

Stateless web/API/worker applications should be able to scale from:

0 → N instances

when traffic exists.

When there is no traffic for a configurable idle period,
the application should be able to return to:

N → 0 instances

The architecture should consider a cloud-native container platform
such as:

- K3s
- Kubernetes
- Knative

However, do not assume these technologies are mandatory.

Treat infrastructure technology as an implementation detail.

The BRD must describe the business requirement rather than locking
the business into one infrastructure implementation.

The SDLC / technical architecture section may evaluate K3s + Knative
as a candidate implementation.

Important:

Frontend applications that are purely static should not necessarily
be treated as scale-to-zero container workloads.

Databases and persistent infrastructure should not automatically
scale to zero with application containers.

==================================================
5. DEPLOYMENT CONTRACT
==================================================

Every application must have a standardized deployment definition,
for example:

deployment.yaml

The deployment definition should describe only application-level
requirements.

Example:

app:
  name: overtime
  owner: HR

services:
  frontend:
    runtime: react

  api:
    runtime: golang
    port: 8080

database:
  type: postgres

scaling:
  min: 0
  max: 3

resources:
  tier: small

domain:
  visibility: internal

The platform should translate this application-level contract into
actual infrastructure configuration.

The employee and Claude Code should NOT have to manually write:

- Kubernetes manifests
- Knative manifests
- Nginx configuration
- Docker network configuration
- TLS configuration
- DNS configuration
- infrastructure secrets
- infrastructure credentials

==================================================
6. SUPPORTED STACK
==================================================

The platform must define a controlled list of supported technologies.

Initial candidate stack:

Frontend:
- React
- Next.js
- Vue

Backend:
- Go
- Node.js
- Python

Database:
- PostgreSQL

Cache:
- Redis

The document must define how supported stacks are managed.

The architecture must allow IT to add or remove supported technologies
without changing the entire platform.

Unsupported technologies must fail validation before deployment.

==================================================
7. SECURITY REQUIREMENTS
==================================================

The platform must enforce:

- Authentication
- Authorization
- RBAC
- Application ownership
- Department ownership
- Environment permissions
- Resource quotas
- Secret management
- Network isolation
- Database isolation
- Container security
- Image security scanning
- Audit logging
- Deployment audit trail

Applications must not be allowed to:

- expose internal databases directly
- use privileged containers
- access the host filesystem
- access the Docker socket
- modify infrastructure
- access another application's secrets
- access another application's database
- store production credentials in source code
- bypass platform policies

Production deployments may require explicit approval.

Development environments may support automatic deployment.

==================================================
8. TARGET USERS
==================================================

Define and analyze at least these actors:

1. Employee / Application Developer
2. AI Coding Agent / Claude Code
3. IT Administrator
4. Platform Administrator
5. Application Owner
6. Security Administrator
7. Management / Auditor

For each actor define:

- Responsibilities
- Goals
- Permissions
- Limitations
- Main workflows
- Risks

==================================================
9. BUSINESS PROCESSES
==================================================

Document the current process (AS-IS):

Employee
→ Creates application with Claude Code
→ Application works locally
→ Employee requests IT deployment
→ IT reviews application
→ IT configures server
→ IT configures database
→ IT configures networking
→ IT configures domain
→ IT configures SSL
→ IT deploys
→ IT monitors
→ IT troubleshoots

Then define the target process (TO-BE):

Employee
→ Claude Code develops application
→ Claude reads Company Deployment Skill
→ Claude generates deployment definition
→ Claude validates application
→ Claude requests deployment through MCP
→ Platform validates policy
→ Platform builds application
→ Platform deploys application
→ Platform performs health check
→ Platform registers application
→ Platform provides URL
→ Platform automatically manages runtime lifecycle

Document the differences between AS-IS and TO-BE.

==================================================
10. BRD DOCUMENT
==================================================

Create a complete professional BRD.

The BRD must contain at minimum:

1. Document Control
2. Executive Summary
3. Background
4. Problem Statement
5. Business Opportunity
6. Business Objectives
7. Project Goals
8. Project Scope
9. Out of Scope
10. Stakeholders
11. User Personas
12. AS-IS Process
13. TO-BE Process
14. Business Requirements
15. Functional Requirements
16. Non-Functional Requirements
17. Business Rules
18. Security Requirements
19. Compliance Requirements
20. Data Requirements
21. Integration Requirements
22. Deployment Requirements
23. Scale-to-Zero Requirements
24. Monitoring Requirements
25. Logging Requirements
26. Backup and Recovery Requirements
27. Availability Requirements
28. Disaster Recovery Requirements
29. Audit Requirements
30. Permission and RBAC Requirements
31. Resource Quota Requirements
32. Application Lifecycle
33. Deployment Lifecycle
34. Environment Management
35. Error Handling
36. Notification Requirements
37. Reporting Requirements
38. Administration Requirements
39. Assumptions
40. Constraints
41. Dependencies
42. Risks
43. Risk Mitigation
44. Success Metrics / KPIs
45. Acceptance Criteria
46. Future Enhancements

==================================================
11. FUNCTIONAL REQUIREMENTS
==================================================

Create detailed functional requirements.

Use a consistent numbering scheme such as:

FR-001
FR-002
FR-003

Group requirements into modules:

A. Authentication
B. User Management
C. Organization / Department
D. Application Registration
E. Application Ownership
F. Stack Management
G. Deployment Configuration
H. Deployment Validation
I. Build Management
J. Deployment Management
K. Application Lifecycle
L. Scale-to-Zero
M. Resource Management
N. Database Management
O. Secret Management
P. Domain Management
Q. Networking
R. Health Check
S. Logging
T. Monitoring
U. Version Management
V. Rollback
W. Audit Log
X. Notification
Y. MCP Integration
Z. Claude Code / AI Agent Integration
AA. Administration
AB. Reporting

Each functional requirement must contain:

- Requirement ID
- Requirement Name
- Description
- Actor
- Trigger
- Preconditions
- Main Flow
- Alternative Flow
- Exception Flow
- Business Rules
- Input
- Output
- Acceptance Criteria
- Priority

Priority must use:

MUST
SHOULD
COULD
WON'T

==================================================
12. NON-FUNCTIONAL REQUIREMENTS
==================================================

Define measurable NFRs.

Include:

Performance
Scalability
Availability
Reliability
Security
Maintainability
Observability
Auditability
Recoverability
Deployability
Portability
Usability
Accessibility

Avoid vague statements such as:

"System must be fast."

Instead define measurable criteria where possible.

Example:

"The platform should return deployment validation results
within X seconds for an application under defined conditions."

Where a value cannot reasonably be determined, mark it as:

TBD

and explain what must be decided later.

==================================================
13. APPLICATION LIFECYCLE
==================================================

Define:

Draft
→ Validated
→ Build
→ Deploying
→ Running
→ Suspended
→ Failed
→ Rolled Back
→ Archived
→ Deleted

Define allowed transitions.

==================================================
14. DEPLOYMENT LIFECYCLE
==================================================

Define:

Request
→ Authentication
→ Authorization
→ Validation
→ Security Check
→ Build
→ Image Scan
→ Registry
→ Deployment
→ Health Check
→ Traffic Activation
→ Monitoring
→ Completed

Also define failure and rollback flows.

==================================================
15. MCP REQUIREMENTS
==================================================

Define the MCP as a business capability interface.

Document:

- MCP purpose
- MCP architecture
- MCP authentication
- MCP authorization
- MCP tool discovery
- MCP tool permissions
- MCP audit logging
- MCP error handling
- MCP timeout handling
- MCP idempotency
- MCP deployment confirmation
- MCP production approval

Define candidate tools:

get_platform_info
get_supported_stacks
get_deployment_requirements
create_application
validate_application
deploy_application
get_application_status
get_deployment_status
get_application_logs
get_application_metrics
rollback_application
restart_application
delete_application

For every MCP tool define:

- Purpose
- Input
- Output
- Permission
- Validation
- Error conditions
- Security considerations
- Audit requirements

==================================================
16. COMPANY DEPLOYMENT SKILL
==================================================

Define requirements for a Company Deployment Skill that Claude Code
can read.

The skill should instruct the AI agent to:

1. Inspect the project.
2. Identify the application architecture.
3. Check supported technologies.
4. Generate deployment.yaml.
5. Validate the deployment definition.
6. Run application tests.
7. Call the Company MCP.
8. Deploy only through the approved platform.
9. Verify deployment health.
10. Report the application URL.
11. Never manipulate infrastructure directly.

Define the Skill structure.

Example:

company-deployment-skill/

SKILL.md
docs/
schemas/
examples/

Explain the responsibilities of each component.

==================================================
17. SDLC
==================================================

Create a complete SDLC plan for this project.

Use:

1. Discovery
2. Requirements Analysis
3. Solution Architecture
4. UX / DX Design
5. Technical Design
6. Development
7. Testing
8. Security Testing
9. Integration Testing
10. UAT
11. Deployment
12. Monitoring
13. Maintenance
14. Continuous Improvement

For each SDLC phase define:

- Objective
- Activities
- Deliverables
- Responsible roles
- Entry criteria
- Exit criteria
- Risks
- Quality gates

==================================================
18. SYSTEM MODULES
==================================================

Define the required modules:

1. Identity & Access Management
2. Application Registry
3. Deployment Manager
4. Validation Engine
5. Build Engine
6. Deployment Controller
7. Resource Manager
8. Secret Manager
9. Database Manager
10. Domain Manager
11. Health Check Manager
12. Logging
13. Monitoring
14. Audit
15. Notification
16. MCP Server
17. Platform API
18. Administration Portal
19. Application Catalog

Explain responsibilities and interactions.

==================================================
19. DATA MODEL
==================================================

Identify major entities.

At minimum consider:

User
Department
Role
Permission
Application
ApplicationOwner
ApplicationVersion
Deployment
DeploymentHistory
Environment
Service
ResourceProfile
Database
Secret
Domain
AuditLog
DeploymentApproval
MCPClient
APIKey / Credential
Notification

For each entity describe:

- Purpose
- Key attributes
- Relationships
- Lifecycle

Do NOT jump directly into SQL unless necessary.
This is a requirements and system analysis document.

==================================================
20. API REQUIREMENTS
==================================================

Define high-level API capabilities.

Example:

POST /applications
GET /applications
GET /applications/{id}
POST /applications/{id}/validate
POST /applications/{id}/deploy
GET /applications/{id}/status
GET /applications/{id}/logs
POST /applications/{id}/rollback

Do not prematurely lock the project to REST if another interface
would be more appropriate.

Clearly separate:

- Business API
- MCP Interface
- Internal infrastructure APIs

==================================================
21. ARCHITECTURE
==================================================

Provide a logical architecture.

At minimum include:

Employee
Claude Code
Company Skill
Company MCP
Platform API
Deployment Controller
Application Registry
Build System
Container Registry
Runtime Platform
Database
Secret Store
Gateway
Monitoring
Logging
Audit

Clearly distinguish:

Control Plane
Data Plane
AI Interface
Application Runtime

==================================================
22. INFRASTRUCTURE EVALUATION
==================================================

Evaluate candidate implementations:

Option A:
Docker + Docker Compose

Option B:
K3s + Kubernetes

Option C:
K3s + Knative

Option D:
Managed Container Platform

Compare them using:

- Scale-to-zero
- Operational complexity
- Cost
- Security
- Self-hosting
- Maintainability
- Developer experience
- AI deployment compatibility
- Future scalability
- IT workload

Provide a recommendation.

Do not select a technology simply because it is popular.

The primary business objective is minimizing IT operational workload
while maintaining security and control.

==================================================
23. OBSERVABILITY
==================================================

Define:

Logs
Metrics
Health Checks
Events
Deployment status
Application status
Resource usage
Scale events
Failure events
Audit events

Define what employees can see and what only IT can see.

==================================================
24. BACKUP / DISASTER RECOVERY
==================================================

Define:

Application backup
Database backup
Configuration backup
Deployment history
Rollback
Recovery
RPO
RTO

If values cannot yet be determined, mark them TBD.

==================================================
25. SECURITY THREAT MODEL
==================================================

Analyze threats including:

- Malicious employee
- Compromised AI agent
- Malicious application code
- Container escape
- Secret leakage
- Privilege escalation
- Cross-application access
- Supply-chain attack
- Malicious dependency
- Image vulnerability
- Unauthorized deployment
- Production deployment abuse
- Resource exhaustion
- Data exfiltration

For each threat define:

Threat
Impact
Likelihood
Risk
Mitigation
Residual Risk

==================================================
26. TEST STRATEGY
==================================================

Define testing strategy for:

Unit Testing
Integration Testing
API Testing
MCP Testing
Security Testing
Deployment Testing
Infrastructure Testing
Performance Testing
Scale-to-Zero Testing
Cold Start Testing
Failure Recovery Testing
Rollback Testing
UAT
End-to-End Testing

==================================================
27. ACCEPTANCE CRITERIA
==================================================

Create measurable acceptance criteria for the whole platform.

Examples:

- Employee can register an application without IT intervention.
- Claude Code can discover deployment capabilities.
- Claude Code can validate an application.
- Supported stack violations are rejected.
- Unauthorized deployments are rejected.
- Application can be deployed through MCP.
- Application receives an internal URL.
- Health checks are automatically executed.
- Stateless application can scale from zero to one or more instances.
- Application can return to zero instances after inactivity.
- Employee cannot access another application's secrets.
- IT can view deployment history.
- IT can rollback a deployment.
- All deployment operations are audited.

==================================================
28. KPI
==================================================

Define measurable success metrics.

Examples:

IT deployment tickets per month
Average deployment time
Percentage of self-service deployments
Deployment failure rate
Mean time to recovery
Number of supported applications
IT hours spent per application
Percentage of deployments completed without IT intervention
Security policy violation rate
Platform availability

==================================================
29. DOCUMENT QUALITY RULES
==================================================

The document must be:

- Professional
- Enterprise-grade
- Implementation-aware
- Traceable
- Measurable
- Consistent
- Suitable for management review
- Suitable for developer handoff
- Suitable for security review
- Suitable for future system architecture

Do not invent unknown business decisions.

If information is missing, explicitly mark:

TBD

Then create a:

"Decision Required"

section listing questions that management / IT must answer.

Do not silently make assumptions.

==================================================
30. TRACEABILITY
==================================================

Create a Requirements Traceability Matrix:

Business Objective
→ Business Requirement
→ Functional Requirement
→ Non-Functional Requirement
→ System Module
→ Test Case
→ Acceptance Criteria

Ensure every major business objective has corresponding requirements
and acceptance criteria.

==================================================
31. FINAL OUTPUT
==================================================

Create the following documents:

01_BRD.md
02_Functional_Requirements.md
03_Non_Functional_Requirements.md
04_Business_Rules.md
05_Process_Flows.md
06_System_Requirements.md
07_MCP_Requirements.md
08_Company_Deployment_Skill.md
09_SDLC.md
10_System_Architecture.md
11_Security_Requirements.md
12_Data_Requirements.md
13_API_Requirements.md
14_Test_Strategy.md
15_Traceability_Matrix.md
16_Risk_Register.md
17_Decision_Log.md

Also create:

README.md

README.md must explain:

- What this documentation set is
- What each document contains
- Which documents should be read first
- How the documents relate to each other
- Which decisions are still TBD

==================================================
32. IMPORTANT
==================================================

Do NOT start coding the actual platform.

The current phase is:

BUSINESS ANALYSIS + REQUIREMENTS + SDLC + SYSTEM DESIGN.

Do not create production source code.

Do not create Kubernetes manifests.

Do not create Docker deployment scripts.

Do not implement the MCP server yet.

The objective of this task is to create a high-quality,
implementation-ready documentation baseline that can be used
as the foundation for the next development phase.

Before finalizing the documents:

1. Check for missing requirements.
2. Check for conflicting requirements.
3. Check for duplicated requirements.
4. Check that every major business objective has requirements.
5. Check that every major requirement has acceptance criteria.
6. Check security implications.
7. Check scale-to-zero implications.
8. Check IT operational workload.
9. Check AI/MCP security boundaries.
10. Check that infrastructure implementation details are not
    unnecessarily exposed to employees.
11. Produce a list of unresolved decisions.
12. Produce recommendations for the MVP scope.

Finally, provide:

A. Executive Summary
B. Recommended MVP
C. Recommended Phase 2
D. Recommended Phase 3
E. Major Risks
F. Critical Decisions Required
G. Recommended Technology Direction
H. Recommended SDLC approach