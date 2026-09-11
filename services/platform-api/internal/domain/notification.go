// Notification implements a scoped slice of Module X
// (docs/02_Functional_Requirements.md): FR-107 (Deployment Status
// Notifications) and FR-108 (Approval Request Notifications). FR-109
// (Security and Policy Violation Notifications) is a documented gap — it
// needs a Security Administrator role this platform doesn't have
// (blocked on DEC-002) and a proactive detection sweep (e.g. periodically
// re-running Module W's VerifyChain) this platform doesn't run in the
// background, unlike the scale-to-zero sweeper.
//
// Delivery is in-app only: a queryable, per-recipient notification list,
// not email/Slack/webhook — there is no outbound delivery channel
// configured anywhere in this platform, and FR-107's "configured
// channel(s)" is squarely a Module X follow-up, not invented here.
package domain

import "time"

type NotificationCategory string

const (
	// NotificationDeploymentStatus implements FR-107: a deployment/rollback
	// attempt reached a terminal outcome (succeeded or failed).
	NotificationDeploymentStatus NotificationCategory = "deployment_status"
	// NotificationApprovalRequest implements FR-108: a production deployment
	// is paused at the approval gate. Recipients are every active owner of
	// the application, not a distinct "approver" role — this platform has
	// no Platform/Security Administrator role, the same gap DecideApproval's
	// own doc comment already names.
	NotificationApprovalRequest NotificationCategory = "approval_request"
	// NotificationOwnershipTransfer implements FR-016 main flow step 2: the
	// nominated new owner is notified an ownership transfer is awaiting
	// their acceptance. Unlike the two categories above, this goes to a
	// single specific recipient (the nominee), not every active owner —
	// see NotificationService.NotifyUser.
	NotificationOwnershipTransfer NotificationCategory = "ownership_transfer"
	// NotificationHealthRemediation implements FR-085 step 4 and FR-085's
	// alternative/exception flows: an instance was automatically restarted
	// after failing continuous health checks (Module R, FR-084), or
	// automatic remediation itself failed or was paused and needs human
	// review. See service.HealthMonitorService.
	NotificationHealthRemediation NotificationCategory = "health_remediation"
)

type Notification struct {
	ID              string
	RecipientUserID string
	Category        NotificationCategory
	Title           string
	Detail          string
	ResourceType    string
	ResourceID      string
	ReadAt          *time.Time
	CreatedAt       time.Time
}
