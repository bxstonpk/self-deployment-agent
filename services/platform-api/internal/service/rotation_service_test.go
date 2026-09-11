package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"platform-api/internal/domain"
	"platform-api/internal/service"
)

type fakePasswordRotator struct {
	rotated  []string
	err      error
	onRotate func() // what a real rotation does to the listed version
}

func (f *fakePasswordRotator) RotatePassword(ctx context.Context, applicationID string) error {
	f.rotated = append(f.rotated, applicationID)
	if f.err != nil {
		return f.err
	}
	if f.onRotate != nil {
		f.onRotate()
	}
	return nil
}

type fakeInstanceRestarter struct {
	restarted []string
	err       error
}

func (f *fakeInstanceRestarter) Restart(ctx context.Context, applicationID, requesterID string) (domain.Deployment, error) {
	f.restarted = append(f.restarted, applicationID)
	return domain.Deployment{}, f.err
}

type fakeSecretLister struct {
	secrets []domain.SecretMetadata
}

func (f *fakeSecretLister) List(ctx context.Context, applicationID, requesterID string) ([]domain.SecretMetadata, error) {
	return append([]domain.SecretMetadata(nil), f.secrets...), nil
}

type rotationFixture struct {
	svc       *service.RotationService
	apps      *fakeLifecycleRepo
	rotator   *fakePasswordRotator
	restarter *fakeInstanceRestarter
	audit     *fakeAuditRecorder
}

// A running application with a platform-generated database password and
// one owner-set secret.
func newRotationFixture() rotationFixture {
	apps := newFakeLifecycleRepo(domain.Application{ID: "app-1", Name: "leave-tracker", LifecycleStatus: domain.StatusRunning})
	owners := newFakeOwnerRepo()
	owners.owners["app-1"] = []domain.ApplicationOwner{{ApplicationID: "app-1", UserID: "owner-1", OwnershipRole: domain.OwnerRolePrimary, Status: "active"}}
	secrets := &fakeSecretLister{secrets: []domain.SecretMetadata{
		{Name: domain.DatabasePasswordSecret, ManagedBy: domain.SecretManagedByPlatform, Version: 1},
		{Name: "API_KEY", ManagedBy: domain.SecretManagedByEmployee, Version: 3},
	}}
	rotator := &fakePasswordRotator{onRotate: func() { secrets.secrets[0].Version++ }}
	restarter := &fakeInstanceRestarter{}
	audit := newFakeAuditRecorder()
	return rotationFixture{
		svc:  service.NewRotationService(apps, owners, secrets, rotator, restarter, audit),
		apps: apps, rotator: rotator, restarter: restarter, audit: audit,
	}
}

func TestRotate_RunningApplication_RotatesThenRestarts(t *testing.T) {
	f := newRotationFixture()

	result, err := f.svc.Rotate(context.Background(), "app-1", "owner-1", domain.DatabasePasswordSecret)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(f.rotator.rotated) != 1 || len(f.restarter.restarted) != 1 {
		t.Fatalf("expected one rotation then one restart, got %v / %v", f.rotator.rotated, f.restarter.restarted)
	}
	if !result.Restarted || result.Secret.Version != 2 {
		t.Fatalf("expected restarted onto version 2, got %+v", result)
	}
	entries := f.audit.all()
	if len(entries) != 1 || entries[0].Action != domain.AuditActionRotateSecret || entries[0].Outcome != domain.AuditSuccess ||
		!strings.Contains(entries[0].Detail, "version 2") {
		t.Fatalf("expected one successful secret.rotate entry naming the version, got %+v", entries)
	}
}

// Nothing running means nothing to restart; the next start reads the new
// password from the store like any other.
func TestRotate_NothingRunning_RotatesWithoutRestarting(t *testing.T) {
	f := newRotationFixture()
	app := f.apps.apps["app-1"]
	app.LifecycleStatus = domain.StatusSuspended
	f.apps.apps["app-1"] = app

	result, err := f.svc.Rotate(context.Background(), "app-1", "owner-1", domain.DatabasePasswordSecret)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(f.rotator.rotated) != 1 || len(f.restarter.restarted) != 0 || result.Restarted || result.Note == "" {
		t.Fatalf("expected a rotation, no restart and an explanation, got %+v (restarts %v)", result, f.restarter.restarted)
	}
}

// The platform didn't issue an owner-set secret and can't invalidate it.
func TestRotate_OwnerSetSecretIsNotRotatable(t *testing.T) {
	f := newRotationFixture()

	if _, err := f.svc.Rotate(context.Background(), "app-1", "owner-1", "API_KEY"); !errors.Is(err, domain.ErrSecretNotRotatable) {
		t.Fatalf("expected ErrSecretNotRotatable, got %v", err)
	}
	if len(f.rotator.rotated) != 0 || len(f.audit.all()) != 0 {
		t.Fatal("a refused rotation still did something")
	}
}

func TestRotate_UnknownSecret(t *testing.T) {
	f := newRotationFixture()
	if _, err := f.svc.Rotate(context.Background(), "app-1", "owner-1", "NO_SUCH_SECRET"); !errors.Is(err, domain.ErrSecretNotFound) {
		t.Fatalf("expected ErrSecretNotFound, got %v", err)
	}
}

func TestRotate_NonOwnerRejected(t *testing.T) {
	f := newRotationFixture()
	if _, err := f.svc.Rotate(context.Background(), "app-1", "owner-2", domain.DatabasePasswordSecret); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
	if len(f.rotator.rotated) != 0 {
		t.Fatal("a non-owner's request rotated the password")
	}
}

// FR-068's exception flow: the new password is in force, the restart
// wasn't — that must read as unfinished, not as success.
func TestRotate_RestartFailureIsReportedAsIncomplete(t *testing.T) {
	f := newRotationFixture()
	f.restarter.err = errors.New("restart: new instance of api failed health check")

	_, err := f.svc.Rotate(context.Background(), "app-1", "owner-1", domain.DatabasePasswordSecret)
	if !errors.Is(err, domain.ErrRotationIncomplete) {
		t.Fatalf("expected ErrRotationIncomplete, got %v", err)
	}
	if len(f.rotator.rotated) != 1 {
		t.Fatal("the rotation itself should have happened")
	}
	if entries := f.audit.all(); len(entries) != 1 || entries[0].Outcome != domain.AuditFailure {
		t.Fatalf("expected the incomplete rotation audited as a failure, got %+v", entries)
	}
}

func TestRotate_RotatorFailureRestartsNothing(t *testing.T) {
	f := newRotationFixture()
	f.rotator.err = errors.New("ALTER ROLE exited 1")

	if _, err := f.svc.Rotate(context.Background(), "app-1", "owner-1", domain.DatabasePasswordSecret); err == nil {
		t.Fatal("expected the rotation failure to surface")
	}
	if len(f.restarter.restarted) != 0 {
		t.Fatal("instances were restarted after a rotation that never happened")
	}
}
