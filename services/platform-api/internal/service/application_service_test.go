package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"platform-api/internal/domain"
	"platform-api/internal/service"
)

// --- in-memory fakes, no database required ---

type fakeApplicationRepo struct {
	byID map[string]domain.Application
	next int
}

func newFakeApplicationRepo() *fakeApplicationRepo {
	return &fakeApplicationRepo{byID: map[string]domain.Application{}}
}

func (f *fakeApplicationRepo) Create(ctx context.Context, app domain.Application) (domain.Application, error) {
	f.next++
	app.ID = "app-" + time.Now().Format("150405.000000") + "-" + string(rune('a'+f.next))
	app.CreatedAt, app.UpdatedAt = time.Now(), time.Now()
	f.byID[app.ID] = app
	return app, nil
}

func (f *fakeApplicationRepo) GetByID(ctx context.Context, id string) (domain.Application, error) {
	app, ok := f.byID[id]
	if !ok {
		return domain.Application{}, domain.ErrNotFound
	}
	return app, nil
}

func (f *fakeApplicationRepo) NameExists(ctx context.Context, name string) (bool, error) {
	for _, a := range f.byID {
		if a.Name == name {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeApplicationRepo) List(ctx context.Context, limit, offset int) ([]domain.Application, error) {
	var out []domain.Application
	for _, a := range f.byID {
		out = append(out, a)
	}
	return out, nil
}

func (f *fakeApplicationRepo) UpdateMetadata(ctx context.Context, id, description string) (domain.Application, error) {
	app, ok := f.byID[id]
	if !ok {
		return domain.Application{}, domain.ErrNotFound
	}
	app.Description = description
	app.UpdatedAt = time.Now()
	f.byID[id] = app
	return app, nil
}

type fakeOwnerRepo struct {
	owners map[string][]domain.ApplicationOwner
}

func newFakeOwnerRepo() *fakeOwnerRepo {
	return &fakeOwnerRepo{owners: map[string][]domain.ApplicationOwner{}}
}

func (f *fakeOwnerRepo) AssignPrimaryOwner(ctx context.Context, applicationID, userID, assignedBy string) (domain.ApplicationOwner, error) {
	for _, o := range f.owners[applicationID] {
		if o.OwnershipRole == domain.OwnerRolePrimary && o.Status == "active" {
			return domain.ApplicationOwner{}, errors.New("primary owner already assigned")
		}
	}
	o := domain.ApplicationOwner{
		ID: "owner-" + userID, ApplicationID: applicationID, UserID: userID,
		OwnershipRole: domain.OwnerRolePrimary, AssignedBy: assignedBy,
		AssignedAt: time.Now(), Status: "active",
	}
	f.owners[applicationID] = append(f.owners[applicationID], o)
	return o, nil
}

func (f *fakeOwnerRepo) ListForApplication(ctx context.Context, applicationID string) ([]domain.ApplicationOwner, error) {
	return f.owners[applicationID], nil
}

type fakeDepartmentRepo struct{ known map[string]bool }

func (f *fakeDepartmentRepo) Exists(ctx context.Context, id string) (bool, error) {
	return f.known[id], nil
}

func newService() (*service.ApplicationService, *fakeApplicationRepo, *fakeOwnerRepo) {
	apps := newFakeApplicationRepo()
	owners := newFakeOwnerRepo()
	depts := &fakeDepartmentRepo{known: map[string]bool{"dept-1": true}}
	return service.NewApplicationService(apps, owners, depts), apps, owners
}

func TestRegister_Success_AssignsPrimaryOwnerAndDraftStatus(t *testing.T) {
	svc, _, owners := newService()
	caller := domain.User{ID: "user-1", Email: "alice@example.com"}

	app, err := svc.Register(context.Background(), service.RegisterApplicationInput{
		Name: "overtime", OwningDepartmentID: "dept-1", RegisteredBy: caller,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if app.LifecycleStatus != domain.StatusDraft {
		t.Errorf("expected Draft status, got %q", app.LifecycleStatus)
	}

	got, err := owners.ListForApplication(context.Background(), app.ID)
	if err != nil || len(got) != 1 {
		t.Fatalf("expected exactly one owner, got %v (err=%v)", got, err)
	}
	if got[0].UserID != caller.ID || got[0].OwnershipRole != domain.OwnerRolePrimary {
		t.Errorf("expected caller as primary owner, got %+v", got[0])
	}
}

func TestRegister_DuplicateName_Rejected(t *testing.T) {
	svc, _, _ := newService()
	caller := domain.User{ID: "user-1"}
	in := service.RegisterApplicationInput{Name: "overtime", OwningDepartmentID: "dept-1", RegisteredBy: caller}

	if _, err := svc.Register(context.Background(), in); err != nil {
		t.Fatalf("first registration should succeed: %v", err)
	}
	_, err := svc.Register(context.Background(), in)
	if !errors.Is(err, domain.ErrNameTaken) {
		t.Errorf("expected ErrNameTaken, got %v", err)
	}
}

func TestRegister_InvalidName_Rejected(t *testing.T) {
	svc, _, _ := newService()
	caller := domain.User{ID: "user-1"}

	// Genuinely invalid per FR-012 (DNS-label rules) or the reserved-name list.
	// Single/two-character names ("a", "ov") ARE valid DNS labels and are
	// covered separately below.
	cases := []string{"over_time", "-overtime", "1overtime", "admin", "api", ""}
	for _, name := range cases {
		_, err := svc.Register(context.Background(), service.RegisterApplicationInput{
			Name: name, OwningDepartmentID: "dept-1", RegisteredBy: caller,
		})
		if err == nil {
			t.Errorf("expected name %q to be rejected", name)
		}
	}
}

func TestRegister_NameIsNormalizedToLowercase(t *testing.T) {
	svc, _, _ := newService()
	caller := domain.User{ID: "user-1"}

	app, err := svc.Register(context.Background(), service.RegisterApplicationInput{
		Name: "Overtime", OwningDepartmentID: "dept-1", RegisteredBy: caller,
	})
	if err != nil {
		t.Fatalf("mixed-case name should be normalized and accepted, got error: %v", err)
	}
	if app.Name != "overtime" {
		t.Errorf("expected normalized name %q, got %q", "overtime", app.Name)
	}
}

func TestRegister_ShortValidDNSLabelNames_Accepted(t *testing.T) {
	svc, _, _ := newService()
	caller := domain.User{ID: "user-1"}

	for _, name := range []string{"a", "ov"} {
		if _, err := svc.Register(context.Background(), service.RegisterApplicationInput{
			Name: name, OwningDepartmentID: "dept-1", RegisteredBy: caller,
		}); err != nil {
			t.Errorf("expected short valid DNS label %q to be accepted, got error: %v", name, err)
		}
	}
}

func TestRegister_UnknownDepartment_Rejected(t *testing.T) {
	svc, _, _ := newService()
	caller := domain.User{ID: "user-1"}

	_, err := svc.Register(context.Background(), service.RegisterApplicationInput{
		Name: "overtime", OwningDepartmentID: "dept-does-not-exist", RegisteredBy: caller,
	})
	if !errors.Is(err, domain.ErrDepartmentUnknown) {
		t.Errorf("expected ErrDepartmentUnknown, got %v", err)
	}
}

func TestUpdateMetadata_NonOwner_Rejected(t *testing.T) {
	svc, _, _ := newService()
	owner := domain.User{ID: "user-1"}
	stranger := domain.User{ID: "user-2"}

	app, err := svc.Register(context.Background(), service.RegisterApplicationInput{
		Name: "overtime", OwningDepartmentID: "dept-1", RegisteredBy: owner,
	})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	_, err = svc.UpdateMetadata(context.Background(), app.ID, stranger.ID, "new description")
	if !errors.Is(err, domain.ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized, got %v", err)
	}
}

func TestUpdateMetadata_Owner_Succeeds_WithoutChangingLifecycleStatus(t *testing.T) {
	svc, _, _ := newService()
	owner := domain.User{ID: "user-1"}

	app, err := svc.Register(context.Background(), service.RegisterApplicationInput{
		Name: "overtime", OwningDepartmentID: "dept-1", RegisteredBy: owner,
	})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	updated, err := svc.UpdateMetadata(context.Background(), app.ID, owner.ID, "now with a description")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updated.Description != "now with a description" {
		t.Errorf("description not updated: %+v", updated)
	}
	if updated.LifecycleStatus != domain.StatusDraft {
		t.Errorf("FR-013: metadata edit must not change lifecycle status, got %q", updated.LifecycleStatus)
	}
}
