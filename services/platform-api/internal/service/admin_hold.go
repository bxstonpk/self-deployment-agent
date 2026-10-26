package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"platform-api/internal/domain"
)

// An administrator's suspension (the platform owner, 2026-10-26): a
// platform administrator (PLATFORM_ADMIN_EMAILS) can suspend any running
// application, with a reason, for a security or technical problem. The
// owners are told why. While the hold is on, only an administrator can
// resume the application: its owners can't resume, archive or delete it,
// and a dev copy can't be deployed while it or its application is held.
// Everything else about a suspension is the ordinary one: the containers
// stop, the database and its data stay.

const (
	AuditActionAdminSuspend domain.AuditAction = "application.admin_suspend"
	AuditActionAdminResume  domain.AuditAction = "application.admin_resume"
)

type AdminHoldRepository interface {
	Place(ctx context.Context, applicationID, heldBy, reason string) (domain.AdminHold, error)
	Get(ctx context.Context, applicationID string) (domain.AdminHold, error)
	Release(ctx context.Context, applicationID string) error
}

// SetAdminHolds enables administrators' suspensions.
func (s *LifecycleService) SetAdminHolds(holds AdminHoldRepository, admins AdminChecker, notifier GovernanceNotifier) {
	s.holds, s.admins, s.notifier = holds, admins, notifier
}

// HoldOf is the application's hold, or domain.ErrNotFound if it has none.
func (s *LifecycleService) HoldOf(ctx context.Context, applicationID string) (domain.AdminHold, error) {
	if s.holds == nil {
		return domain.AdminHold{}, domain.ErrNotFound
	}
	return s.holds.Get(ctx, applicationID)
}

// RequireNoHold refuses an owner's action on an application an
// administrator suspended, saying why it was suspended.
func (s *LifecycleService) RequireNoHold(ctx context.Context, applicationID string) error {
	h, err := s.HoldOf(ctx, applicationID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("%w (by %s on %s: %s)", domain.ErrAdminHold, h.HeldByEmail, h.HeldAt.UTC().Format("2006-01-02 15:04 UTC"), h.Reason)
}

func (s *LifecycleService) requireAdmin(admin domain.User) error {
	if s.admins == nil || !s.admins.IsAdmin(admin.Email) {
		return domain.ErrUnauthorized
	}
	return nil
}

// AdminSuspend suspends a running application, or holds one its owners
// already suspended, so that only an administrator can resume it.
func (s *LifecycleService) AdminSuspend(ctx context.Context, applicationID string, admin domain.User, reason string) (hold domain.AdminHold, err error) {
	if err := s.requireAdmin(admin); err != nil {
		return domain.AdminHold{}, err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return domain.AdminHold{}, domain.ErrHoldReasonRequired
	}
	app, err := s.apps.GetByID(ctx, applicationID)
	if err != nil {
		return domain.AdminHold{}, err
	}
	if app.LifecycleStatus != domain.StatusRunning && app.LifecycleStatus != domain.StatusSuspended {
		return domain.AdminHold{}, domain.ErrApplicationNotRunning
	}
	deployment, err := s.deployments.LatestForApplication(ctx, applicationID)
	if err != nil {
		return domain.AdminHold{}, err
	}
	running := app.LifecycleStatus == domain.StatusRunning
	if running && deployment.Status != domain.DeploymentRunning {
		return domain.AdminHold{}, domain.ErrApplicationNotRunning
	}

	defer s.auditAction(ctx, AuditActionAdminSuspend, applicationID, admin.ID, &err)

	// The hold first, so no owner resumes it in between.
	hold, err = s.holds.Place(ctx, applicationID, admin.ID, reason)
	if err != nil {
		return domain.AdminHold{}, err
	}
	if running {
		if _, err := s.suspend(ctx, app, deployment); err != nil {
			if rerr := s.holds.Release(ctx, applicationID); rerr != nil {
				log.Printf("admin suspend: %s didn't stop and its hold wasn't removed: %v", app.Name, rerr)
			}
			return domain.AdminHold{}, err
		}
	}
	if s.notifier != nil {
		s.notifier.NotifyOwners(ctx, applicationID, domain.NotificationDeploymentStatus,
			fmt.Sprintf("%s was suspended by a platform administrator", app.Name),
			fmt.Sprintf("Reason: %s. Its database and data are kept. Only a platform administrator can resume it: "+
				"contact %s. Until then it can't be resumed, archived or deleted, and its dev copy can't be deployed.",
				reason, admin.Email),
			"application", applicationID)
	}
	return hold, nil
}

// AdminResume lifts an administrator's hold and starts the application
// again. If it fails to start, the hold stays and it can be tried again.
func (s *LifecycleService) AdminResume(ctx context.Context, applicationID string, admin domain.User) (app domain.Application, err error) {
	if err := s.requireAdmin(admin); err != nil {
		return domain.Application{}, err
	}
	app, err = s.apps.GetByID(ctx, applicationID)
	if err != nil {
		return domain.Application{}, err
	}
	hold, err := s.HoldOf(ctx, applicationID)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Application{}, domain.ErrNotAdminHeld
	}
	if err != nil {
		return domain.Application{}, err
	}

	defer s.auditAction(ctx, AuditActionAdminResume, applicationID, admin.ID, &err)

	if app.LifecycleStatus == domain.StatusSuspended {
		deployment, err := s.deployments.LatestForApplication(ctx, applicationID)
		if err != nil {
			return domain.Application{}, err
		}
		if deployment.Status != domain.DeploymentSuspended {
			return domain.Application{}, domain.ErrApplicationNotSuspended
		}
		if _, err := s.resume(ctx, app, deployment); err != nil {
			return domain.Application{}, err
		}
	}
	if err := s.holds.Release(ctx, applicationID); err != nil {
		return domain.Application{}, err
	}
	if app, err = s.apps.GetByID(ctx, applicationID); err != nil {
		return domain.Application{}, err
	}
	if s.notifier != nil {
		s.notifier.NotifyOwners(ctx, applicationID, domain.NotificationDeploymentStatus,
			fmt.Sprintf("%s was resumed by a platform administrator", app.Name),
			fmt.Sprintf("The suspension (%s) is lifted; the application is %s again.", hold.Reason, app.LifecycleStatus),
			"application", applicationID)
	}
	return app, nil
}
