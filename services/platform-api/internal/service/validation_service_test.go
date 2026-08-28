package service_test

import (
	"context"
	"testing"
	"time"

	"platform-api/internal/domain"
	"platform-api/internal/service"
)

// --- fakes ---

type fakeLifecycleRepo struct {
	apps map[string]domain.Application
}

func newFakeLifecycleRepo(apps ...domain.Application) *fakeLifecycleRepo {
	m := map[string]domain.Application{}
	for _, a := range apps {
		m[a.ID] = a
	}
	return &fakeLifecycleRepo{apps: m}
}

func (f *fakeLifecycleRepo) GetByID(ctx context.Context, id string) (domain.Application, error) {
	a, ok := f.apps[id]
	if !ok {
		return domain.Application{}, domain.ErrNotFound
	}
	return a, nil
}

func (f *fakeLifecycleRepo) UpdateLifecycleStatus(ctx context.Context, id string, from, to domain.LifecycleStatus, markValidated bool) (domain.Application, error) {
	a, ok := f.apps[id]
	if !ok {
		return domain.Application{}, domain.ErrNotFound
	}
	if a.LifecycleStatus != from {
		return domain.Application{}, domain.ErrInvalidLifecycleTransition
	}
	a.LifecycleStatus = to
	if markValidated {
		now := time.Now()
		a.ValidatedAt = &now
	}
	f.apps[id] = a
	return a, nil
}

func (f *fakeLifecycleRepo) UpdateDeploymentYAML(ctx context.Context, id, yamlContent string) (domain.Application, error) {
	a, ok := f.apps[id]
	if !ok {
		return domain.Application{}, domain.ErrNotFound
	}
	if a.LifecycleStatus != domain.StatusDraft && a.LifecycleStatus != domain.StatusValidated {
		return domain.Application{}, domain.ErrInvalidLifecycleTransition
	}
	a.DeploymentYAMLDraft = yamlContent
	a.LifecycleStatus = domain.StatusDraft
	a.ValidatedAt = nil
	f.apps[id] = a
	return a, nil
}

type fakeStackRepo struct {
	// kind -> set of active names
	active map[domain.StackKind]map[string]bool
}

func newFakeStackRepo() *fakeStackRepo {
	return &fakeStackRepo{active: map[domain.StackKind]map[string]bool{
		domain.StackKindFrontend: {"react": true, "nextjs": true, "vue": true},
		domain.StackKindBackend:  {"go": true, "nodejs": true, "python": true},
		domain.StackKindDatabase: {"postgres": true},
		domain.StackKindCache:    {"redis": true},
	}}
}

func (f *fakeStackRepo) FindKind(ctx context.Context, name string) (domain.StackKind, bool, error) {
	for _, k := range []domain.StackKind{domain.StackKindFrontend, domain.StackKindBackend} {
		if f.active[k][name] {
			return k, true, nil
		}
	}
	return "", false, nil
}

func (f *fakeStackRepo) IsAllowed(ctx context.Context, kind domain.StackKind, name string) (bool, error) {
	return f.active[kind][name], nil
}

func (f *fakeStackRepo) List(ctx context.Context) ([]domain.SupportedStack, error) { return nil, nil }

func newValidationService(app domain.Application, ownerID string) (*service.ValidationService, *fakeLifecycleRepo) {
	apps := newFakeLifecycleRepo(app)
	owners := newFakeOwnerRepo()
	owners.owners[app.ID] = []domain.ApplicationOwner{{
		ApplicationID: app.ID, UserID: ownerID, OwnershipRole: domain.OwnerRolePrimary, Status: "active",
	}}
	stacks := newFakeStackRepo()
	return service.NewValidationService(apps, owners, stacks), apps
}

const validYAML = `
app:
  name: overtime
  owner: HR
services:
  frontend:
    runtime: react
  api:
    runtime: go
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
`

func draftApp(id, name, yaml string) domain.Application {
	return domain.Application{
		ID: id, Name: name, LifecycleStatus: domain.StatusDraft, DeploymentYAMLDraft: yaml,
	}
}

func TestValidate_ValidConfig_TransitionsToValidated(t *testing.T) {
	app := draftApp("app-1", "overtime", validYAML)
	svc, repo := newValidationService(app, "owner-1")

	report, updated, err := svc.Validate(context.Background(), "app-1", "owner-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !report.Valid {
		t.Fatalf("expected valid report, got %+v", report)
	}
	if updated.LifecycleStatus != domain.StatusValidated {
		t.Errorf("expected Validated status, got %q", updated.LifecycleStatus)
	}
	if updated.ValidatedAt == nil {
		t.Error("expected validated_at to be set")
	}
	if repo.apps["app-1"].LifecycleStatus != domain.StatusValidated {
		t.Error("expected persisted status to be Validated")
	}
}

func TestValidate_UnsupportedRuntime_FailsAndStaysDraft(t *testing.T) {
	badYAML := `
app:
  name: overtime
  owner: HR
services:
  frontend:
    runtime: php
`
	app := draftApp("app-1", "overtime", badYAML)
	svc, repo := newValidationService(app, "owner-1")

	report, updated, err := svc.Validate(context.Background(), "app-1", "owner-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Valid {
		t.Fatalf("expected invalid report for unsupported runtime, got %+v", report)
	}
	if updated.LifecycleStatus != domain.StatusDraft {
		t.Errorf("expected status to remain Draft, got %q", updated.LifecycleStatus)
	}
	if repo.apps["app-1"].LifecycleStatus != domain.StatusDraft {
		t.Error("expected persisted status to remain Draft on failure")
	}
}

func TestValidate_UnknownTopLevelField_FailsAsSecurityPrecheck(t *testing.T) {
	sneaky := `
app:
  name: overtime
  owner: HR
services:
  frontend:
    runtime: react
kubernetes:
  privileged: true
`
	app := draftApp("app-1", "overtime", sneaky)
	svc, _ := newValidationService(app, "owner-1")

	report, _, err := svc.Validate(context.Background(), "app-1", "owner-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Valid {
		t.Fatal("expected an unrecognized top-level field to fail validation")
	}
	found := false
	for _, c := range report.Checks {
		if c.Name == "security_precheck" && c.Status == domain.CheckFailed {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a failed security_precheck check, got %+v", report.Checks)
	}
}

func TestValidate_BackendServiceMissingPort_Fails(t *testing.T) {
	yaml := `
app:
  name: overtime
  owner: HR
services:
  api:
    runtime: go
`
	app := draftApp("app-1", "overtime", yaml)
	svc, _ := newValidationService(app, "owner-1")

	report, _, err := svc.Validate(context.Background(), "app-1", "owner-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Valid {
		t.Fatal("expected missing port on a backend service to fail validation")
	}
}

func TestValidate_AppNameMismatch_Fails(t *testing.T) {
	yaml := `
app:
  name: not-the-registered-name
  owner: HR
services:
  frontend:
    runtime: react
`
	app := draftApp("app-1", "overtime", yaml)
	svc, _ := newValidationService(app, "owner-1")

	report, _, err := svc.Validate(context.Background(), "app-1", "owner-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Valid {
		t.Fatal("expected app.name mismatch to fail validation")
	}
}

func TestValidate_NonOwner_Rejected(t *testing.T) {
	app := draftApp("app-1", "overtime", validYAML)
	svc, _ := newValidationService(app, "owner-1")

	_, _, err := svc.Validate(context.Background(), "app-1", "stranger")
	if err != domain.ErrUnauthorized {
		t.Errorf("expected ErrUnauthorized, got %v", err)
	}
}

func TestValidate_NotInDraft_Rejected(t *testing.T) {
	app := draftApp("app-1", "overtime", validYAML)
	app.LifecycleStatus = domain.StatusRunning
	svc, _ := newValidationService(app, "owner-1")

	_, _, err := svc.Validate(context.Background(), "app-1", "owner-1")
	if err != domain.ErrInvalidLifecycleTransition {
		t.Errorf("expected ErrInvalidLifecycleTransition, got %v", err)
	}
}

func TestValidate_EmptyDraft_Rejected(t *testing.T) {
	app := draftApp("app-1", "overtime", "")
	svc, _ := newValidationService(app, "owner-1")

	_, _, err := svc.Validate(context.Background(), "app-1", "owner-1")
	if err != domain.ErrNoDeploymentYAML {
		t.Errorf("expected ErrNoDeploymentYAML, got %v", err)
	}
}

func TestSaveDeploymentYAML_RevertsValidatedToDraft(t *testing.T) {
	app := draftApp("app-1", "overtime", validYAML)
	app.LifecycleStatus = domain.StatusValidated
	svc, repo := newValidationService(app, "owner-1")

	updated, err := svc.SaveDeploymentYAML(context.Background(), "app-1", "owner-1", validYAML)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updated.LifecycleStatus != domain.StatusDraft {
		t.Errorf("expected save to revert status to Draft, got %q", updated.LifecycleStatus)
	}
	if repo.apps["app-1"].ValidatedAt != nil {
		t.Error("expected validated_at to be cleared on save")
	}
}

func TestSaveDeploymentYAML_InvalidYAMLSyntax_Rejected(t *testing.T) {
	app := draftApp("app-1", "overtime", "")
	svc, _ := newValidationService(app, "owner-1")

	_, err := svc.SaveDeploymentYAML(context.Background(), "app-1", "owner-1", "not: [valid: yaml")
	if err == nil {
		t.Fatal("expected an error for malformed YAML")
	}
}
