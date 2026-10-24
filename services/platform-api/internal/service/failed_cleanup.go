package service

import (
	"context"
	"errors"
	"fmt"
	"log"

	"platform-api/internal/domain"
)

// Failed applications that never ran are removed at once (the platform
// owner, 2026-10-24): a first build or deployment that failed leaves
// nothing worth keeping — no deployment ever went live, so there is no
// data — only containers, a database and memory. The application is
// deleted (its database with it), its name is freed, and its owners are
// told to fix the problem and deploy again under the same name.
//
// An application that has run is never removed this way, whatever state it
// is in: its data is real. Nor is one with a dev copy, whose database may
// hold what an employee entered while trying it.

const AuditActionFailedCleanup domain.AuditAction = "application.failed_cleanup"

type FailedApplications interface {
	ListFailed(ctx context.Context) ([]domain.Application, error)
}

type FailedCleanupLookups struct {
	Deployments interface {
		ListForApplication(ctx context.Context, applicationID string) ([]domain.Deployment, error)
	}
	Builds interface {
		LatestForApplication(ctx context.Context, applicationID string) (domain.Build, error)
	}
	Owners ApplicationOwnerRepository
	// DevCopy is the application's live dev copy (domain.ErrNotFound if none).
	DevCopy func(ctx context.Context, parentID string) (domain.DevCopy, error)
	Apps    ApplicationLifecycleRepository
}

type FailedCleanupService struct {
	failed   FailedApplications
	lookups  FailedCleanupLookups
	delete   func(ctx context.Context, applicationID, requesterID string, confirm bool) (domain.Application, error)
	notifier GovernanceNotifier
	audit    AuditRecorder
}

func NewFailedCleanupService(failed FailedApplications, lookups FailedCleanupLookups,
	deleteApp func(ctx context.Context, applicationID, requesterID string, confirm bool) (domain.Application, error),
	notifier GovernanceNotifier, audit AuditRecorder) *FailedCleanupService {
	return &FailedCleanupService{failed: failed, lookups: lookups, delete: deleteApp, notifier: notifier, audit: audit}
}

// everRan reports whether any deployment of the application went live.
func everRan(deployments []domain.Deployment) bool {
	for _, d := range deployments {
		switch d.Status {
		case domain.DeploymentRunning, domain.DeploymentSuperseded, domain.DeploymentSuspended, domain.DeploymentArchived:
			return true
		}
	}
	return false
}

func (s *FailedCleanupService) reason(ctx context.Context, app domain.Application, deployments []domain.Deployment) string {
	if b, err := s.lookups.Builds.LatestForApplication(ctx, app.ID); err == nil && b.Status == domain.BuildFailed && b.ErrorDetail != nil {
		return *b.ErrorDetail
	}
	if len(deployments) > 0 && deployments[0].FailureReason != nil {
		return *deployments[0].FailureReason
	}
	return "its build or deployment failed"
}

// Sweep removes every failed application that never ran. Returns how many.
func (s *FailedCleanupService) Sweep(ctx context.Context) (int, error) {
	apps, err := s.failed.ListFailed(ctx)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, app := range apps {
		deployments, err := s.lookups.Deployments.ListForApplication(ctx, app.ID)
		if err != nil {
			log.Printf("failed cleanup: %s: %v", app.Name, err)
			continue
		}
		if everRan(deployments) {
			continue
		}
		if s.lookups.DevCopy != nil {
			if c, err := s.lookups.DevCopy(ctx, app.ID); err == nil {
				if copyApp, aerr := s.lookups.Apps.GetByID(ctx, c.ApplicationID); aerr == nil && copyApp.LifecycleStatus != domain.StatusDeleted {
					continue // the dev copy's data is the employee's
				}
			} else if !errors.Is(err, domain.ErrNotFound) {
				log.Printf("failed cleanup: %s: %v", app.Name, err)
				continue
			}
		}
		owners, err := s.lookups.Owners.ListForApplication(ctx, app.ID)
		if err != nil {
			log.Printf("failed cleanup: %s: %v", app.Name, err)
			continue
		}
		primary := ""
		for _, o := range owners {
			if o.Status == "active" && (primary == "" || o.OwnershipRole == domain.OwnerRolePrimary) {
				primary = o.UserID
			}
		}
		if primary == "" {
			continue
		}
		why := s.reason(ctx, app, deployments)
		s.notifier.NotifyOwners(ctx, app.ID, domain.NotificationDeploymentStatus,
			fmt.Sprintf("%s was removed: it never ran successfully", app.Name),
			fmt.Sprintf("Its first build or deployment failed: %s. Nothing of it ever went live, so the platform removed "+
				"it and freed its name. Fix the problem and deploy it again: with Claude, start the application again "+
				"under the same name (start_project) and deploy.", why),
			"application", app.ID)
		if _, err := s.delete(ctx, app.ID, primary, true); err != nil {
			log.Printf("failed cleanup: remove %s: %v", app.Name, err)
			continue
		}
		removed++
		if err := s.audit.Record(ctx, domain.AuditEntry{
			ActorUserID: primary, Action: AuditActionFailedCleanup, ResourceType: "application", ResourceID: app.ID,
			Outcome: domain.AuditSuccess, Detail: fmt.Sprintf("%s never ran (%s): removed, name freed", app.Name, why),
		}); err != nil {
			log.Printf("failed cleanup: %v", err)
		}
		log.Printf("failed cleanup: removed %s, which never ran (%s)", app.Name, why)
	}
	return removed, nil
}
