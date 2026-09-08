// Audit implements the core types for Module W
// (docs/02_Functional_Requirements.md FR-103 Immutable Audit Trail, FR-104
// Query/Search, FR-105 Export, FR-106 Tamper Protection) and
// docs/12_Data_Requirements.md's ENT-16 AuditLog entity.
//
// Scope adaptation, documented not hidden: ENT-16 names an actor_type
// (user / AI-agent / system) distinct from the acting user, per FR-117's
// "attribute agent-initiated actions to both the agent and the employee it
// acted for". No such distinct actor identity exists anywhere in this
// platform yet — an MCP-initiated call authenticates as the same employee
// dev-auth identity a direct API/Admin Portal call would use (see
// services/mcp-server's platform_client.py) — so every entry's actor is
// simply "this user", with nothing yet to distinguish an agent from a
// direct human action. Worth revisiting once Module Y grows a genuine
// service-account/agent identity of its own.
package domain

import "time"

type AuditOutcome string

const (
	AuditSuccess AuditOutcome = "success"
	AuditFailure AuditOutcome = "failure"
)

// AuditAction enumerates the significant, state-changing actions this
// implementation actually instruments. Deliberately not exhaustive of every
// FR-103 example ("authentication, ... secret operations, ...") — there is
// no login flow or Secret Management module (O) to audit yet. See each
// instrumented service method's doc comment for the exact scope boundary.
type AuditAction string

const (
	AuditActionRegisterApplication AuditAction = "application.register"
	AuditActionValidateApplication AuditAction = "application.validate"
	AuditActionTriggerBuild        AuditAction = "build.trigger"
	AuditActionInitiateDeploy      AuditAction = "deployment.deploy"
	AuditActionDecideApproval      AuditAction = "deployment.approval_decision"
	AuditActionRollback            AuditAction = "deployment.rollback"
	AuditActionSuspend             AuditAction = "application.suspend"
	AuditActionResume              AuditAction = "application.resume"
	AuditActionRestart             AuditAction = "application.restart"
	AuditActionArchive             AuditAction = "application.archive"
	AuditActionDelete              AuditAction = "application.delete"
	AuditActionExport              AuditAction = "audit_log.export"
)

// AuditEntry is one append-only row. PrevHash/EntryHash implement FR-106:
// each entry's hash covers the previous entry's hash plus its own content,
// so any out-of-band tampering (a direct database edit bypassing every
// platform-mediated write path) breaks the chain from that point forward
// and is detectable — see AuditRepository.VerifyChain.
type AuditEntry struct {
	ID           string
	Seq          int64
	OccurredAt   time.Time
	ActorUserID  string
	Action       AuditAction
	ResourceType string // "application", "deployment", "build", "audit_log"
	ResourceID   string
	Outcome      AuditOutcome
	Detail       string // never a secret value (FR-105) — there are no secrets to reference yet anyway
	PrevHash     string
	EntryHash    string
}

// AuditQuery implements FR-104's filter set. A zero value matches
// everything (subject to the caller's own visibility scope — see
// AuditService.Query).
type AuditQuery struct {
	ActorUserID  string
	ResourceType string
	ResourceID   string
	Action       AuditAction
	From         *time.Time
	To           *time.Time
	Limit        int
}
