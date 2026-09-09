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

// AddOwner mirrors application_owner_repo.go's real upsert semantics: a
// second grant of the same (application, user, role) re-activates rather
// than erroring or duplicating.
func (f *fakeOwnerRepo) AddOwner(ctx context.Context, applicationID, userID string, role domain.OwnershipRole, assignedBy string) (domain.ApplicationOwner, error) {
	for i, o := range f.owners[applicationID] {
		if o.UserID == userID && o.OwnershipRole == role {
			f.owners[applicationID][i].Status = "active"
			f.owners[applicationID][i].AssignedBy = assignedBy
			f.owners[applicationID][i].AssignedAt = time.Now()
			return f.owners[applicationID][i], nil
		}
	}
	o := domain.ApplicationOwner{
		ID: "owner-" + userID + "-" + string(role), ApplicationID: applicationID, UserID: userID,
		OwnershipRole: role, AssignedBy: assignedBy, AssignedAt: time.Now(), Status: "active",
	}
	f.owners[applicationID] = append(f.owners[applicationID], o)
	return o, nil
}

// Revoke mirrors application_owner_repo.go's real WHERE clause: never
// touches a 'primary' role row.
func (f *fakeOwnerRepo) Revoke(ctx context.Context, applicationID, userID string) (int64, error) {
	var revoked int64
	for i, o := range f.owners[applicationID] {
		if o.UserID == userID && o.Status == "active" && o.OwnershipRole != domain.OwnerRolePrimary {
			f.owners[applicationID][i].Status = "revoked"
			revoked++
		}
	}
	return revoked, nil
}

type fakeDepartmentRepo struct{ known map[string]bool }

func (f *fakeDepartmentRepo) Exists(ctx context.Context, id string) (bool, error) {
	return f.known[id], nil
}

// fakeUserRepo mirrors user_repo.go's GetByEmail: returns ErrTargetUserUnknown
// for any email it wasn't explicitly seeded with, matching the real
// "never silently provision an account for someone else" behavior.
type fakeUserRepo struct {
	byEmail map[string]domain.User
}

func newFakeUserRepo() *fakeUserRepo {
	return &fakeUserRepo{byEmail: map[string]domain.User{}}
}

func (f *fakeUserRepo) GetByEmail(ctx context.Context, email string) (domain.User, error) {
	u, ok := f.byEmail[email]
	if !ok {
		return domain.User{}, domain.ErrTargetUserUnknown
	}
	return u, nil
}

func newService() (*service.ApplicationService, *fakeApplicationRepo, *fakeOwnerRepo, *fakeUserRepo) {
	apps := newFakeApplicationRepo()
	owners := newFakeOwnerRepo()
	depts := &fakeDepartmentRepo{known: map[string]bool{"dept-1": true}}
	users := newFakeUserRepo()
	return service.NewApplicationService(apps, owners, depts, users, newFakeAuditRecorder()), apps, owners, users
}

func TestRegister_Success_AssignsPrimaryOwnerAndDraftStatus(t *testing.T) {
	svc, _, owners, _ := newService()
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
	svc, _, _, _ := newService()
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
	svc, _, _, _ := newService()
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
	svc, _, _, _ := newService()
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
	svc, _, _, _ := newService()
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
	svc, _, _, _ := newService()
	caller := domain.User{ID: "user-1"}

	_, err := svc.Register(context.Background(), service.RegisterApplicationInput{
		Name: "overtime", OwningDepartmentID: "dept-does-not-exist", RegisteredBy: caller,
	})
	if !errors.Is(err, domain.ErrDepartmentUnknown) {
		t.Errorf("expected ErrDepartmentUnknown, got %v", err)
	}
}

func TestUpdateMetadata_NonOwner_Rejected(t *testing.T) {
	svc, _, _, _ := newService()
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
	svc, _, _, _ := newService()
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

func TestRegister_Success_RecordsAuditEntry(t *testing.T) {
	apps := newFakeApplicationRepo()
	owners := newFakeOwnerRepo()
	depts := &fakeDepartmentRepo{known: map[string]bool{"dept-1": true}}
	audit := newFakeAuditRecorder()
	svc := service.NewApplicationService(apps, owners, depts, newFakeUserRepo(), audit)
	caller := domain.User{ID: "user-1"}

	app, err := svc.Register(context.Background(), service.RegisterApplicationInput{
		Name: "overtime", OwningDepartmentID: "dept-1", RegisteredBy: caller,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entries := audit.all()
	if len(entries) != 1 {
		t.Fatalf("expected exactly one audit entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Action != domain.AuditActionRegisterApplication || e.Outcome != domain.AuditSuccess || e.ResourceID != app.ID || e.ActorUserID != caller.ID {
		t.Fatalf("unexpected audit entry: %+v", e)
	}
}

func TestRegister_AuditWriteFailure_SurfacesAsError(t *testing.T) {
	apps := newFakeApplicationRepo()
	owners := newFakeOwnerRepo()
	depts := &fakeDepartmentRepo{known: map[string]bool{"dept-1": true}}
	audit := newFakeAuditRecorder()
	audit.failNext = true
	svc := service.NewApplicationService(apps, owners, depts, newFakeUserRepo(), audit)

	// FR-103: a critical action must not report a bare, unaudited success —
	// see audit_service.go's Record doc comment. The application row is
	// still created (the state change already happened); the caller just
	// finds out the audit trail failed to record it.
	_, err := svc.Register(context.Background(), service.RegisterApplicationInput{
		Name: "overtime", OwningDepartmentID: "dept-1", RegisteredBy: domain.User{ID: "user-1"},
	})
	if err == nil {
		t.Fatal("expected an error when the audit trail write fails")
	}
}

// --- FR-017: Co-Owner/Contributor Management ---

func registerApp(t *testing.T, svc *service.ApplicationService, ownerID string) domain.Application {
	t.Helper()
	app, err := svc.Register(context.Background(), service.RegisterApplicationInput{
		Name: "overtime", OwningDepartmentID: "dept-1", RegisteredBy: domain.User{ID: ownerID},
	})
	if err != nil {
		t.Fatalf("setup: register application: %v", err)
	}
	return app
}

func TestGrantCoOwner_ByPrimaryOwner_Succeeds(t *testing.T) {
	svc, _, owners, users := newService()
	app := registerApp(t, svc, "primary-1")
	users.byEmail["bob@example.com"] = domain.User{ID: "bob-1", Email: "bob@example.com", Status: "active"}

	granted, err := svc.GrantCoOwner(context.Background(), app.ID, "primary-1", "bob@example.com", domain.OwnerRoleSecondary)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if granted.UserID != "bob-1" || granted.OwnershipRole != domain.OwnerRoleSecondary || granted.Status != "active" {
		t.Fatalf("unexpected grant result: %+v", granted)
	}

	all, _ := owners.ListForApplication(context.Background(), app.ID)
	if len(all) != 2 {
		t.Fatalf("expected primary + new co-owner, got %+v", all)
	}
}

func TestGrantCoOwner_ByNonPrimaryOwner_Rejected(t *testing.T) {
	svc, _, _, users := newService()
	app := registerApp(t, svc, "primary-1")
	users.byEmail["bob@example.com"] = domain.User{ID: "bob-1", Status: "active"}
	users.byEmail["carol@example.com"] = domain.User{ID: "carol-1", Status: "active"}

	if _, err := svc.GrantCoOwner(context.Background(), app.ID, "primary-1", "bob@example.com", domain.OwnerRoleSecondary); err != nil {
		t.Fatalf("setup: grant bob: %v", err)
	}

	// Bob is now an active co-owner, but not the primary owner - FR-017's
	// precondition requires the primary specifically.
	_, err := svc.GrantCoOwner(context.Background(), app.ID, "bob-1", "carol@example.com", domain.OwnerRoleTechnical)
	if !errors.Is(err, domain.ErrNotPrimaryOwner) {
		t.Fatalf("expected ErrNotPrimaryOwner, got %v", err)
	}
}

func TestGrantCoOwner_UnknownTargetEmail_Rejected(t *testing.T) {
	svc, _, _, _ := newService()
	app := registerApp(t, svc, "primary-1")

	_, err := svc.GrantCoOwner(context.Background(), app.ID, "primary-1", "nobody@example.com", domain.OwnerRoleSecondary)
	if !errors.Is(err, domain.ErrTargetUserUnknown) {
		t.Fatalf("expected ErrTargetUserUnknown, got %v", err)
	}
}

func TestGrantCoOwner_InactiveTargetUser_Rejected(t *testing.T) {
	svc, _, _, users := newService()
	app := registerApp(t, svc, "primary-1")
	users.byEmail["bob@example.com"] = domain.User{ID: "bob-1", Status: "offboarded"}

	_, err := svc.GrantCoOwner(context.Background(), app.ID, "primary-1", "bob@example.com", domain.OwnerRoleSecondary)
	if !errors.Is(err, domain.ErrTargetUserInactive) {
		t.Fatalf("expected ErrTargetUserInactive, got %v", err)
	}
}

func TestGrantCoOwner_InvalidRole_Rejected(t *testing.T) {
	svc, _, _, users := newService()
	app := registerApp(t, svc, "primary-1")
	users.byEmail["bob@example.com"] = domain.User{ID: "bob-1", Status: "active"}

	_, err := svc.GrantCoOwner(context.Background(), app.ID, "primary-1", "bob@example.com", domain.OwnerRolePrimary)
	if !errors.Is(err, domain.ErrInvalidOwnershipRole) {
		t.Fatalf("expected ErrInvalidOwnershipRole for role=primary, got %v", err)
	}
}

func TestGrantCoOwner_RegrantingAfterRevoke_IsIdempotentNotAnError(t *testing.T) {
	svc, _, owners, users := newService()
	app := registerApp(t, svc, "primary-1")
	users.byEmail["bob@example.com"] = domain.User{ID: "bob-1", Status: "active"}

	if _, err := svc.GrantCoOwner(context.Background(), app.ID, "primary-1", "bob@example.com", domain.OwnerRoleSecondary); err != nil {
		t.Fatalf("first grant: %v", err)
	}
	if err := svc.RevokeCoOwner(context.Background(), app.ID, "primary-1", "bob-1"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := svc.GrantCoOwner(context.Background(), app.ID, "primary-1", "bob@example.com", domain.OwnerRoleSecondary); err != nil {
		t.Fatalf("re-grant after revoke should succeed, got: %v", err)
	}

	all, _ := owners.ListForApplication(context.Background(), app.ID)
	var bobRows int
	for _, o := range all {
		if o.UserID == "bob-1" {
			bobRows++
			if o.Status != "active" {
				t.Errorf("expected bob's re-granted row to be active, got %+v", o)
			}
		}
	}
	if bobRows != 1 {
		t.Fatalf("expected exactly one row for bob (re-activated, not duplicated), got %d", bobRows)
	}
}

func TestRevokeCoOwner_ByPrimaryOwner_Succeeds(t *testing.T) {
	svc, _, owners, users := newService()
	app := registerApp(t, svc, "primary-1")
	users.byEmail["bob@example.com"] = domain.User{ID: "bob-1", Status: "active"}
	if _, err := svc.GrantCoOwner(context.Background(), app.ID, "primary-1", "bob@example.com", domain.OwnerRoleSecondary); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := svc.RevokeCoOwner(context.Background(), app.ID, "primary-1", "bob-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	all, _ := owners.ListForApplication(context.Background(), app.ID)
	for _, o := range all {
		if o.UserID == "bob-1" && o.Status != "revoked" {
			t.Errorf("expected bob's grant to be revoked, got %+v", o)
		}
	}
}

func TestRevokeCoOwner_CannotRevokeThePrimaryOwner(t *testing.T) {
	svc, _, owners, _ := newService()
	app := registerApp(t, svc, "primary-1")

	// No co-owner exists at all - attempting to revoke the primary owner's
	// own id must be a no-op ErrCoOwnerGrantNotFound, not an accidental
	// removal of the only owner (Revoke's WHERE clause excludes
	// ownership_role='primary').
	err := svc.RevokeCoOwner(context.Background(), app.ID, "primary-1", "primary-1")
	if !errors.Is(err, domain.ErrCoOwnerGrantNotFound) {
		t.Fatalf("expected ErrCoOwnerGrantNotFound, got %v", err)
	}

	all, _ := owners.ListForApplication(context.Background(), app.ID)
	if len(all) != 1 || all[0].Status != "active" {
		t.Fatalf("expected the primary owner to remain untouched, got %+v", all)
	}
}

func TestRevokeCoOwner_ByNonPrimaryOwner_Rejected(t *testing.T) {
	svc, _, _, users := newService()
	app := registerApp(t, svc, "primary-1")
	users.byEmail["bob@example.com"] = domain.User{ID: "bob-1", Status: "active"}
	users.byEmail["carol@example.com"] = domain.User{ID: "carol-1", Status: "active"}
	if _, err := svc.GrantCoOwner(context.Background(), app.ID, "primary-1", "bob@example.com", domain.OwnerRoleSecondary); err != nil {
		t.Fatalf("setup grant bob: %v", err)
	}
	if _, err := svc.GrantCoOwner(context.Background(), app.ID, "primary-1", "carol@example.com", domain.OwnerRoleTechnical); err != nil {
		t.Fatalf("setup grant carol: %v", err)
	}

	err := svc.RevokeCoOwner(context.Background(), app.ID, "bob-1", "carol-1")
	if !errors.Is(err, domain.ErrNotPrimaryOwner) {
		t.Fatalf("expected ErrNotPrimaryOwner, got %v", err)
	}
}

func TestRevokeCoOwner_UnknownTarget_ReturnsCoOwnerGrantNotFound(t *testing.T) {
	svc, _, _, _ := newService()
	app := registerApp(t, svc, "primary-1")

	err := svc.RevokeCoOwner(context.Background(), app.ID, "primary-1", "no-such-user")
	if !errors.Is(err, domain.ErrCoOwnerGrantNotFound) {
		t.Fatalf("expected ErrCoOwnerGrantNotFound, got %v", err)
	}
}

func TestGrantCoOwner_RecordsAuditEntry(t *testing.T) {
	apps := newFakeApplicationRepo()
	owners := newFakeOwnerRepo()
	depts := &fakeDepartmentRepo{known: map[string]bool{"dept-1": true}}
	users := newFakeUserRepo()
	audit := newFakeAuditRecorder()
	svc := service.NewApplicationService(apps, owners, depts, users, audit)
	app := registerApp(t, svc, "primary-1")
	users.byEmail["bob@example.com"] = domain.User{ID: "bob-1", Status: "active"}

	if _, err := svc.GrantCoOwner(context.Background(), app.ID, "primary-1", "bob@example.com", domain.OwnerRoleSecondary); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entries := audit.all()
	var grantEntry *domain.AuditEntry
	for i := range entries {
		if entries[i].Action == domain.AuditActionGrantOwner {
			grantEntry = &entries[i]
		}
	}
	if grantEntry == nil {
		t.Fatalf("expected a grant_owner audit entry among %+v", entries)
	}
	if grantEntry.Outcome != domain.AuditSuccess || grantEntry.ResourceID != app.ID || grantEntry.ActorUserID != "primary-1" {
		t.Fatalf("unexpected grant audit entry: %+v", *grantEntry)
	}
}
