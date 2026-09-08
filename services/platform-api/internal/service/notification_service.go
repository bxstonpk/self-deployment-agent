// Notification implements Module X's service layer (docs/02_Functional_Requirements.md
// FR-107, FR-108). See internal/domain/notification.go's package comment
// for the scope this slice covers.
package service

import (
	"context"
	"log"

	"platform-api/internal/domain"
)

type NotificationRepository interface {
	Create(ctx context.Context, n domain.Notification) (domain.Notification, error)
	ListForRecipient(ctx context.Context, recipientUserID string, unreadOnly bool, limit int) ([]domain.Notification, error)
	MarkRead(ctx context.Context, id, recipientUserID string) (domain.Notification, error)
}

// NotificationRecorder is the narrow seam DeploymentService depends on —
// FR-107's exception flow is explicit that "failure to deliver does not
// block the underlying deployment pipeline itself", so unlike
// AuditRecorder this reports nothing back to its caller at all: there is
// nothing for a caller to react to. Mirrors scale_event_repo.go's Record,
// the other place this codebase already treats a side-effect write as
// genuinely best-effort by design, not by omission.
type NotificationRecorder interface {
	NotifyOwners(ctx context.Context, applicationID string, category domain.NotificationCategory, title, detail, resourceType, resourceID string)
}

type NotificationService struct {
	repo   NotificationRepository
	owners ApplicationOwnerRepository
}

func NewNotificationService(repo NotificationRepository, owners ApplicationOwnerRepository) *NotificationService {
	return &NotificationService{repo: repo, owners: owners}
}

// NotifyOwners implements FR-107/FR-108's recipient rule as this platform
// can actually satisfy it: every active owner of the application, standing
// in for both "the requester" (who is, by construction, always an active
// owner — every deployment-pipeline action requireOwner-gates on exactly
// that) and "the designated approver(s)" (FR-108 — there is no distinct
// approver role, the same gap DecideApproval's own doc comment names).
//
// Known gap: FR-107's exception flow calls for delivery retry "per
// policy" on failure — there is no retry queue here, a failed write is
// only logged. Consistent with the same flow's actual requirement (must
// not block the pipeline), just not the literal retry mechanic.
func (s *NotificationService) NotifyOwners(ctx context.Context, applicationID string, category domain.NotificationCategory, title, detail, resourceType, resourceID string) {
	owners, err := s.owners.ListForApplication(ctx, applicationID)
	if err != nil {
		log.Printf("notification: failed to list owners for application %s: %v", applicationID, err)
		return
	}
	for _, o := range owners {
		if o.Status != "active" {
			continue
		}
		if _, err := s.repo.Create(ctx, domain.Notification{
			RecipientUserID: o.UserID, Category: category, Title: title, Detail: detail,
			ResourceType: resourceType, ResourceID: resourceID,
		}); err != nil {
			log.Printf("notification: failed to notify %s about application %s: %v", o.UserID, applicationID, err)
		}
	}
}

// List implements the read side for a signed-in caller's own notifications
// — always scoped to the caller as recipient; there is no "list someone
// else's notifications" capability, so no additional authorization check
// is needed beyond what ListForRecipient's WHERE clause already enforces.
func (s *NotificationService) List(ctx context.Context, recipientUserID string, unreadOnly bool) ([]domain.Notification, error) {
	return s.repo.ListForRecipient(ctx, recipientUserID, unreadOnly, 100)
}

func (s *NotificationService) MarkRead(ctx context.Context, id, recipientUserID string) (domain.Notification, error) {
	return s.repo.MarkRead(ctx, id, recipientUserID)
}
