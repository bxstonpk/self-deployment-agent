package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"platform-api/internal/domain"
	"platform-api/internal/service"
)

type fakePreviewRepo struct {
	byApp map[string]domain.Preview
}

func (f *fakePreviewRepo) Live(ctx context.Context, parentID string) (domain.Preview, error) {
	for _, p := range f.byApp {
		if p.ParentID == parentID && p.EndedAt == nil {
			return p, nil
		}
	}
	return domain.Preview{}, domain.ErrNotFound
}

func (f *fakePreviewRepo) Of(ctx context.Context, applicationID string) (domain.Preview, error) {
	p, ok := f.byApp[applicationID]
	if !ok {
		return domain.Preview{}, domain.ErrNotFound
	}
	return p, nil
}

func (f *fakePreviewRepo) Create(ctx context.Context, p domain.Preview) (domain.Preview, error) {
	p.CreatedAt = time.Now()
	f.byApp[p.ApplicationID] = p
	return p, nil
}

func (f *fakePreviewRepo) End(ctx context.Context, applicationID string) error {
	p := f.byApp[applicationID]
	now := time.Now()
	p.EndedAt = &now
	f.byApp[applicationID] = p
	return nil
}

func (f *fakePreviewRepo) Due(ctx context.Context, now time.Time) ([]domain.Preview, error) {
	var out []domain.Preview
	for _, p := range f.byApp {
		if p.EndedAt == nil && !now.Before(p.ExpiresAt) {
			out = append(out, p)
		}
	}
	return out, nil
}

const parentYAML = `
app:
  name: leave
  owner: HR
services:
  frontend:
    runtime: react
  api:
    runtime: go
    port: 8080
database:
  type: mysql
`

type previewFixture struct {
	svc      *service.PreviewService
	apps     *fakeLifecycleRepo
	repo     *fakePreviewRepo
	calls    []string
	profiles map[string]domain.ApplicationProfile
	build    domain.BuildStatus
	now      time.Time
}

func newPreviews(t *testing.T) *previewFixture {
	t.Helper()
	f := &previewFixture{build: domain.BuildSucceeded, now: time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC),
		profiles: map[string]domain.ApplicationProfile{"app-1": {ApplicationID: "app-1", Purpose: "leave requests"}}}
	f.apps = newFakeLifecycleRepo(domain.Application{ID: "app-1", Name: "leave", LifecycleStatus: domain.StatusRunning,
		OwningDepartmentID: "hr", DeploymentYAMLDraft: parentYAML})
	owners := newFakeOwnerRepo()
	owners.owners["app-1"] = []domain.ApplicationOwner{{ApplicationID: "app-1", UserID: "owner-1", Status: "active"}}
	f.repo = &fakePreviewRepo{byApp: map[string]domain.Preview{}}
	n := 0
	steps := service.PreviewSteps{
		Register: func(ctx context.Context, in service.RegisterApplicationInput) (domain.Application, error) {
			f.calls = append(f.calls, "register "+in.Name)
			n++
			app := domain.Application{ID: "pv-" + string(rune('0'+n)), Name: in.Name, LifecycleStatus: domain.StatusDraft,
				OwningDepartmentID: in.OwningDepartmentID, DeploymentYAMLDraft: in.DeploymentYAMLDraft}
			f.apps.apps[app.ID] = app
			return app, nil
		},
		Validate: func(ctx context.Context, id, who string) (domain.ValidationReport, domain.Application, error) {
			f.calls = append(f.calls, "validate "+id)
			return domain.ValidationReport{Valid: true}, f.apps.apps[id], nil
		},
		Build: func(ctx context.Context, id, who string, archive []byte) (domain.Build, error) {
			f.calls = append(f.calls, "build "+id)
			a := f.apps.apps[id]
			a.LifecycleStatus = domain.StatusFailed
			if f.build == domain.BuildSucceeded {
				a.LifecycleStatus = domain.StatusBuild
			}
			f.apps.apps[id] = a
			return domain.Build{ID: "b-" + id, Status: f.build}, nil
		},
		Deploy: func(ctx context.Context, id, who string, env domain.Environment) (domain.Deployment, error) {
			f.calls = append(f.calls, "deploy "+id+" "+string(env))
			a := f.apps.apps[id]
			a.LifecycleStatus = domain.StatusRunning
			f.apps.apps[id] = a
			return domain.Deployment{ID: "d-" + id, Status: domain.DeploymentRunning, Environment: env}, nil
		},
		Archive: func(ctx context.Context, id, who string) (domain.Application, error) {
			f.calls = append(f.calls, "archive "+id+" as "+who)
			a := f.apps.apps[id]
			a.LifecycleStatus = domain.StatusArchived
			f.apps.apps[id] = a
			return a, nil
		},
		Delete: func(ctx context.Context, id, who string, confirm bool) (domain.Application, error) {
			f.calls = append(f.calls, "delete "+id+" as "+who)
			a := f.apps.apps[id]
			a.LifecycleStatus = domain.StatusDeleted
			f.apps.apps[id] = a
			return a, nil
		},
		GetProfile: func(ctx context.Context, id string) (domain.ApplicationProfile, error) {
			p, ok := f.profiles[id]
			if !ok {
				return domain.ApplicationProfile{}, domain.ErrNotFound
			}
			return p, nil
		},
		PutProfile: func(ctx context.Context, p domain.ApplicationProfile) (domain.ApplicationProfile, error) {
			f.profiles[p.ApplicationID] = p
			return p, nil
		},
	}
	f.svc = service.NewPreviewService(f.apps, owners, f.repo, steps, newFakeAuditRecorder(), 24*time.Hour)
	service.SetPreviewClockForTest(f.svc, func() time.Time { return f.now }, func() string { return "a1b2" })
	return f
}

var previewOwner = domain.User{ID: "owner-1", Email: "somchai@sti-th.com"}

func TestPreview_IsItsOwnApplicationWithTheOriginalsYAMLProfileAndNoBackups(t *testing.T) {
	f := newPreviews(t)
	res, err := f.svc.Start(context.Background(), previewOwner, "app-1", []byte("archive"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"register leave-preview-a1b2", "validate pv-1", "build pv-1", "deploy pv-1 preview"}
	if strings.Join(f.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("steps\n got %v\nwant %v", f.calls, want)
	}
	if res.Preview.Name != "leave-preview-a1b2" || !res.Preview.ExpiresAt.Equal(f.now.Add(24*time.Hour)) {
		t.Fatalf("preview: %+v", res.Preview)
	}
	var doc struct {
		App      struct{ Name string } `yaml:"app"`
		Services map[string]any        `yaml:"services"`
		Database struct{ Type, Backup string }
	}
	if err := yaml.Unmarshal([]byte(f.apps.apps["pv-1"].DeploymentYAMLDraft), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.App.Name != "leave-preview-a1b2" || len(doc.Services) != 2 || doc.Database.Type != "mysql" || doc.Database.Backup != "none" {
		t.Fatalf("the preview's deployment.yaml: %+v", doc)
	}
	if f.profiles["pv-1"].Purpose != "leave requests" {
		t.Fatal("the profile is copied, so the build accepts it")
	}
	if f.apps.apps["app-1"].LifecycleStatus != domain.StatusRunning {
		t.Fatal("the application it previews is untouched")
	}
}

func TestPreview_ANewOneReplacesTheLiveOne(t *testing.T) {
	f := newPreviews(t)
	ctx := context.Background()
	if _, err := f.svc.Start(ctx, previewOwner, "app-1", []byte("v1")); err != nil {
		t.Fatal(err)
	}
	f.calls = nil
	res, err := f.svc.Start(ctx, previewOwner, "app-1", []byte("v2"))
	if err != nil {
		t.Fatal(err)
	}
	if f.calls[0] != "archive pv-1 as owner-1" || f.calls[1] != "delete pv-1 as owner-1" || res.Preview.ApplicationID != "pv-2" {
		t.Fatalf("the old preview goes first: %v", f.calls)
	}
	if live, _ := f.repo.Live(ctx, "app-1"); live.ApplicationID != "pv-2" {
		t.Fatalf("one live preview: %+v", live)
	}
}

func TestPreview_AFailedBuildLeavesNothing(t *testing.T) {
	f := newPreviews(t)
	f.build = domain.BuildFailed
	res, err := f.svc.Start(context.Background(), previewOwner, "app-1", []byte("broken"))
	if err != nil || res.Build.Status != domain.BuildFailed {
		t.Fatalf("a failed build is reported, not an error: %v %v", res.Build.Status, err)
	}
	if f.apps.apps["pv-1"].LifecycleStatus != domain.StatusDeleted {
		t.Fatal("torn down at once")
	}
	if _, err := f.repo.Live(context.Background(), "app-1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("and no live preview remains")
	}
}

func TestPreview_ExpiresOnTheSweep(t *testing.T) {
	f := newPreviews(t)
	ctx := context.Background()
	if _, err := f.svc.Start(ctx, previewOwner, "app-1", []byte("v1")); err != nil {
		t.Fatal(err)
	}
	if n, _ := f.svc.Sweep(ctx); n != 0 {
		t.Fatal("not yet")
	}
	f.now = f.now.Add(24 * time.Hour)
	if n, err := f.svc.Sweep(ctx); n != 1 || err != nil {
		t.Fatalf("expired: %d %v", n, err)
	}
	if f.apps.apps["pv-1"].LifecycleStatus != domain.StatusDeleted {
		t.Fatal("its application is deleted (its database with it)")
	}
	if _, err := f.svc.Get(ctx, previewOwner, "app-1"); !errors.Is(err, domain.ErrNoPreview) {
		t.Fatalf("gone: %v", err)
	}
}

func TestPreview_Refusals(t *testing.T) {
	f := newPreviews(t)
	ctx := context.Background()
	if _, err := f.svc.Start(ctx, domain.User{ID: "someone"}, "app-1", []byte("x")); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("not an owner: %v", err)
	}
	if _, err := f.svc.Start(ctx, previewOwner, "app-1", []byte("v1")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Start(ctx, previewOwner, "pv-1", []byte("x")); err == nil {
		t.Fatal("a preview of a preview is refused")
	}
	a := f.apps.apps["app-1"]
	a.Name = strings.Repeat("x", 51)
	f.apps.apps["app-1"] = a
	if _, err := f.svc.Start(ctx, previewOwner, "app-1", []byte("x")); !errors.Is(err, domain.ErrPreviewNameTooLong) {
		t.Fatalf("too long: %v", err)
	}
}
