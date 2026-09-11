// Rotation implements FR-068 (Module O's secret rotation) for the secrets
// the platform can genuinely rotate: ones it generated itself. Today that
// is one secret — Module N's database password — and rotating it means
// three things, in this order: the database stops accepting the old
// password, the secret store holds the new one (both in
// DatabaseService.RotatePassword), and every running instance is restarted
// onto it.
//
// An owner-set secret is not rotatable here, and says so: the platform did
// not issue it and cannot invalidate it — only the third party that issued
// an API key can. Replacing its value, effective at the next start, is as
// far as the platform can go, and Set already does that.
package service

import (
	"context"
	"fmt"

	"platform-api/internal/domain"
)

type PasswordRotator interface {
	RotatePassword(ctx context.Context, applicationID string) error
}

type InstanceRestarter interface {
	Restart(ctx context.Context, applicationID, requesterID string) (domain.Deployment, error)
}

type SecretLister interface {
	List(ctx context.Context, applicationID, requesterID string) ([]domain.SecretMetadata, error)
}

type RotationResult struct {
	Secret domain.SecretMetadata
	// Restarted reports whether running instances were restarted onto the
	// new value; false when nothing was running to restart.
	Restarted bool
	Note      string
}

type RotationService struct {
	apps      ApplicationGetter
	owners    ApplicationOwnerRepository
	secrets   SecretLister
	databases PasswordRotator
	instances InstanceRestarter
	audit     AuditRecorder
}

func NewRotationService(
	apps ApplicationGetter, owners ApplicationOwnerRepository, secrets SecretLister,
	databases PasswordRotator, instances InstanceRestarter, audit AuditRecorder,
) *RotationService {
	return &RotationService{apps: apps, owners: owners, secrets: secrets, databases: databases, instances: instances, audit: audit}
}

func (s *RotationService) requireOwner(ctx context.Context, applicationID, userID string) error {
	owners, err := s.owners.ListForApplication(ctx, applicationID)
	if err != nil {
		return err
	}
	for _, o := range owners {
		if o.UserID == userID && o.Status == "active" {
			return nil
		}
	}
	return domain.ErrUnauthorized
}

func findSecret(list []domain.SecretMetadata, name string) (domain.SecretMetadata, bool) {
	for _, s := range list {
		if s.Name == name {
			return s, true
		}
	}
	return domain.SecretMetadata{}, false
}

// Rotate implements FR-068's manual trigger. Scheduled rotation needs an
// interval the requirement marks TBD, and the production approval FR-071
// asks for needs RBAC — neither is here (see domain/secret.go).
func (s *RotationService) Rotate(ctx context.Context, applicationID, requesterID, name string) (result RotationResult, err error) {
	app, err := s.apps.GetByID(ctx, applicationID)
	if err != nil {
		return RotationResult{}, err
	}
	if err := s.requireOwner(ctx, applicationID, requesterID); err != nil {
		return RotationResult{}, err
	}
	before, err := s.secrets.List(ctx, applicationID, requesterID)
	if err != nil {
		return RotationResult{}, err
	}
	current, ok := findSecret(before, name)
	if !ok {
		return RotationResult{}, domain.ErrSecretNotFound
	}
	if current.ManagedBy != domain.SecretManagedByPlatform || name != domain.DatabasePasswordSecret {
		return RotationResult{}, domain.ErrSecretNotRotatable
	}

	// FR-068's audit step: the secret's name and new version, never a value.
	defer func() {
		outcome, detail := domain.AuditSuccess, fmt.Sprintf("rotated secret %s (version %d)", name, result.Secret.Version)
		if err != nil {
			outcome, detail = domain.AuditFailure, fmt.Sprintf("rotate secret %s: %v", name, err)
		}
		if auditErr := s.audit.Record(ctx, domain.AuditEntry{
			ActorUserID: requesterID, Action: domain.AuditActionRotateSecret,
			ResourceType: "application", ResourceID: applicationID, Outcome: outcome, Detail: detail,
		}); auditErr != nil && err == nil {
			err = fmt.Errorf("secret rotated but audit trail failed to record: %w", auditErr)
		}
	}()

	if err := s.databases.RotatePassword(ctx, applicationID); err != nil {
		return RotationResult{}, err
	}
	after, err := s.secrets.List(ctx, applicationID, requesterID)
	if err != nil {
		return RotationResult{}, err
	}
	result.Secret, _ = findSecret(after, name)

	// Re-injection. From the moment the database changed, an instance still
	// holding the old password can't open a new connection, so this runs
	// straight away. A service scaled to zero gets the new value at its next
	// cold start; a suspended or archived application at its next start.
	if app.LifecycleStatus != domain.StatusRunning {
		result.Note = "Nothing is running; the application gets the new password at its next start."
		return result, nil
	}
	if _, err := s.instances.Restart(ctx, applicationID, requesterID); err != nil {
		// Surfaced, not swallowed — FR-068's exception flow. The database and
		// the store already agree on the new password, so a Restart retried
		// by the owner completes the rotation.
		return result, fmt.Errorf("%w: %v", domain.ErrRotationIncomplete, err)
	}
	result.Restarted = true
	result.Note = "Running instances were restarted onto the new password; a service scaled to zero gets it at its next cold start."
	return result, nil
}
