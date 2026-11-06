package service_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"platform-api/internal/domain"
	"platform-api/internal/service"
	"platform-api/internal/sourcestore"
)

type fakeSourceBuilds struct{ builds []domain.Build }

func (f *fakeSourceBuilds) ListForApplication(_ context.Context, applicationID string) ([]domain.Build, error) {
	out := []domain.Build{}
	for _, b := range f.builds {
		if b.ApplicationID == applicationID {
			out = append(out, b)
		}
	}
	return out, nil
}

func (f *fakeSourceBuilds) GetByID(_ context.Context, id string) (domain.Build, error) {
	for _, b := range f.builds {
		if b.ID == id {
			return b, nil
		}
	}
	return domain.Build{}, domain.ErrNotFound
}

type fakeSourceDeployments struct{ deployments []domain.Deployment }

func (f *fakeSourceDeployments) ListForApplication(_ context.Context, applicationID string) ([]domain.Deployment, error) {
	out := []domain.Deployment{}
	for _, d := range f.deployments {
		if d.ApplicationID == applicationID {
			out = append(out, d)
		}
	}
	return out, nil
}

type fakeSourceUsers map[string]domain.User

func (f fakeSourceUsers) GetByID(_ context.Context, id string) (domain.User, error) {
	u, ok := f[id]
	if !ok {
		return domain.User{}, domain.ErrNotFound
	}
	return u, nil
}

type fakeSourceAdmins struct{}

func (fakeSourceAdmins) IsAdmin(email string) bool { return email == "admin@sti-th.com" }

type sourceFixture struct {
	svc                           *service.SourceHistoryService
	owner, copyOwner, stranger    domain.User
	admin, approver               domain.User
	parentID, copyID, otherID     string
	prodBuild, devBuild, oldBuild string
	otherBuild                    string
}

// leave (production) and leave-dev (its live dev copy), plus an unrelated
// application. Builds: leave's approved one and an old one from before
// sources were kept; leave-dev's newest.
func newSourceFixture(t *testing.T) sourceFixture {
	t.Helper()
	f := sourceFixture{
		owner:     domain.User{ID: "u-owner", Email: "owner@sti-th.com"},
		copyOwner: domain.User{ID: "u-copy", Email: "copy@sti-th.com"},
		stranger:  domain.User{ID: "u-stranger", Email: "stranger@sti-th.com"},
		admin:     domain.User{ID: "u-admin", Email: "admin@sti-th.com"},
		approver:  domain.User{ID: "u-approver", Email: "approver@sti-th.com"},
		parentID:  "app-leave", copyID: "app-leave-dev", otherID: "app-other",
		prodBuild: "b-prod", devBuild: "b-dev", oldBuild: "b-old", otherBuild: "b-other",
	}
	apps := newFakeApplicationRepo()
	apps.byID[f.parentID] = domain.Application{ID: f.parentID, Name: "leave"}
	apps.byID[f.copyID] = domain.Application{ID: f.copyID, Name: "leave-dev"}
	apps.byID[f.otherID] = domain.Application{ID: f.otherID, Name: "other"}
	owners := newFakeOwnerRepo()
	owners.owners[f.parentID] = []domain.ApplicationOwner{{ApplicationID: f.parentID, UserID: f.owner.ID, Status: "active"},
		{ApplicationID: f.parentID, UserID: "u-gone", Status: "revoked"}}
	owners.owners[f.copyID] = []domain.ApplicationOwner{{ApplicationID: f.copyID, UserID: f.copyOwner.ID, Status: "active"}}
	copies := &fakeDevCopyRepo{byApp: map[string]domain.DevCopy{
		f.copyID: {ApplicationID: f.copyID, ParentID: f.parentID},
	}, names: map[string]string{f.copyID: "leave-dev", f.parentID: "leave"}}

	now := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	builds := &fakeSourceBuilds{builds: []domain.Build{
		{ID: f.oldBuild, ApplicationID: f.parentID, TriggeredBy: f.owner.ID, Status: domain.BuildSucceeded, StartedAt: now.Add(-72 * time.Hour)},
		{ID: f.prodBuild, ApplicationID: f.parentID, TriggeredBy: f.owner.ID, Status: domain.BuildSucceeded, StartedAt: now.Add(-time.Hour)},
		{ID: f.devBuild, ApplicationID: f.copyID, TriggeredBy: "u-deleted", Status: domain.BuildFailed, StartedAt: now},
		{ID: f.otherBuild, ApplicationID: f.otherID, TriggeredBy: f.stranger.ID, Status: domain.BuildSucceeded, StartedAt: now},
	}}
	deployments := &fakeSourceDeployments{deployments: []domain.Deployment{
		{ID: "d-prod", ApplicationID: f.parentID, BuildID: f.prodBuild, Environment: domain.EnvironmentProduction, Status: domain.DeploymentRunning},
	}}
	store := sourcestore.New(t.TempDir())
	for app, build := range map[string]string{f.parentID: f.prodBuild, f.copyID: f.devBuild, f.otherID: f.otherBuild} {
		if err := store.Save(app, build, []byte("archive of "+build)); err != nil {
			t.Fatal(err)
		}
	}
	users := fakeSourceUsers{f.owner.ID: f.owner}
	f.svc = service.NewSourceHistoryService(apps, owners, builds, deployments, store, copies, users, fakeSourceAdmins{},
		func(email string) bool { return email == "approver@sti-th.com" })
	return f
}

func TestSourceHistoryListsTheApplicationAndItsDevCopyNewestFirst(t *testing.T) {
	f := newSourceFixture(t)
	for _, from := range []string{f.parentID, f.copyID} {
		entries, err := f.svc.List(context.Background(), f.owner, from)
		if err != nil {
			t.Fatalf("from %s: %v", from, err)
		}
		if len(entries) != 3 {
			t.Fatalf("from %s: want 3 builds (no other application's), got %d", from, len(entries))
		}
		dev, prod, old := entries[0], entries[1], entries[2]
		if dev.Build.ID != f.devBuild || !dev.DevCopy || dev.ApplicationName != "leave-dev" {
			t.Errorf("from %s: newest should be the dev copy's build, got %+v", from, dev)
		}
		if prod.Build.ID != f.prodBuild || prod.DevCopy || prod.ApplicationName != "leave" {
			t.Errorf("from %s: second should be leave's build, got %+v", from, prod)
		}
		if !prod.Kept || prod.SourceBytes != int64(len("archive of "+f.prodBuild)) {
			t.Errorf("from %s: leave's build should be kept with its size, got kept=%v bytes=%d", from, prod.Kept, prod.SourceBytes)
		}
		if len(prod.Deployments) != 1 || prod.Deployments[0].ID != "d-prod" || prod.Deployments[0].Environment != domain.EnvironmentProduction {
			t.Errorf("from %s: leave's build should list its production deployment, got %+v", from, prod.Deployments)
		}
		if prod.TriggeredBy != "owner@sti-th.com" {
			t.Errorf("from %s: triggered_by should be the email, got %q", from, prod.TriggeredBy)
		}
		if dev.TriggeredBy != "u-deleted" {
			t.Errorf("from %s: an account that can't be read falls back to its id, got %q", from, dev.TriggeredBy)
		}
		if old.Build.ID != f.oldBuild || old.Kept {
			t.Errorf("from %s: a build from before sources were kept is listed, not kept: %+v", from, old)
		}
	}
}

func TestSourceHistoryIsForOwnersAdministratorsAndApprovers(t *testing.T) {
	f := newSourceFixture(t)
	for _, who := range []domain.User{f.owner, f.copyOwner, f.admin, f.approver} {
		if _, err := f.svc.List(context.Background(), who, f.parentID); err != nil {
			t.Errorf("%s should read leave's sources: %v", who.Email, err)
		}
	}
	for _, who := range []domain.User{f.stranger, {ID: "u-gone", Email: "gone@sti-th.com"}} {
		if _, err := f.svc.List(context.Background(), who, f.parentID); !errors.Is(err, domain.ErrUnauthorized) {
			t.Errorf("%s: want ErrUnauthorized, got %v", who.Email, err)
		}
		if _, _, _, err := f.svc.Source(context.Background(), who, f.parentID, f.prodBuild); !errors.Is(err, domain.ErrUnauthorized) {
			t.Errorf("%s download: want ErrUnauthorized, got %v", who.Email, err)
		}
	}
	if _, err := f.svc.List(context.Background(), f.owner, "app-missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("missing application: want ErrNotFound, got %v", err)
	}
}

func TestSourceHistoryDownloadsABuildOfTheApplicationOrItsDevCopyOnly(t *testing.T) {
	f := newSourceFixture(t)
	f2, size, name, err := f.svc.Source(context.Background(), f.owner, f.parentID, f.devBuild)
	if err != nil {
		t.Fatalf("the dev copy's build from the application: %v", err)
	}
	body, _ := io.ReadAll(f2)
	f2.Close()
	if string(body) != "archive of "+f.devBuild || size != int64(len(body)) || name != "leave-dev-b-dev.tar.gz" {
		t.Errorf("got %q (%d bytes) as %q", body, size, name)
	}
	if _, _, _, err := f.svc.Source(context.Background(), f.owner, f.parentID, f.otherBuild); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("another application's build through leave: want ErrNotFound, got %v", err)
	}
	if _, _, _, err := f.svc.Source(context.Background(), f.admin, f.parentID, f.oldBuild); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("a build with no kept source: want ErrNotFound, got %v", err)
	}
	if _, _, _, err := f.svc.Source(context.Background(), f.owner, f.parentID, "../../etc/passwd"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("a made-up build id: want ErrNotFound, got %v", err)
	}
}
