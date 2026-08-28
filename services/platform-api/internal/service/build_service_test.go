package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"platform-api/internal/domain"
	"platform-api/internal/service"
)

type fakeBuildRepo struct {
	byID  map[string]domain.Build
	byApp map[string]string // applicationID -> latest build ID
	next  int
}

func newFakeBuildRepo() *fakeBuildRepo {
	return &fakeBuildRepo{byID: map[string]domain.Build{}, byApp: map[string]string{}}
}

func (f *fakeBuildRepo) Create(ctx context.Context, applicationID, triggeredBy string) (domain.Build, error) {
	f.next++
	id := "build-" + time.Now().Format("150405.000000") + "-" + string(rune('a'+f.next))
	b := domain.Build{ID: id, ApplicationID: applicationID, TriggeredBy: triggeredBy, Status: domain.BuildQueued, StartedAt: time.Now()}
	f.byID[id] = b
	f.byApp[applicationID] = id
	return b, nil
}

func (f *fakeBuildRepo) MarkInProgress(ctx context.Context, buildID string) (domain.Build, error) {
	b := f.byID[buildID]
	b.Status = domain.BuildInProgress
	f.byID[buildID] = b
	return b, nil
}

func (f *fakeBuildRepo) MarkSucceeded(ctx context.Context, buildID string, imageRefs map[string]string) (domain.Build, error) {
	b := f.byID[buildID]
	b.Status = domain.BuildSucceeded
	b.ImageRefs = imageRefs
	now := time.Now()
	b.CompletedAt = &now
	f.byID[buildID] = b
	return b, nil
}

func (f *fakeBuildRepo) MarkFailed(ctx context.Context, buildID string, category domain.ErrorCategory, detail string) (domain.Build, error) {
	b := f.byID[buildID]
	b.Status = domain.BuildFailed
	b.ErrorCategory = &category
	b.ErrorDetail = &detail
	now := time.Now()
	b.CompletedAt = &now
	f.byID[buildID] = b
	return b, nil
}

func (f *fakeBuildRepo) GetByID(ctx context.Context, buildID string) (domain.Build, error) {
	b, ok := f.byID[buildID]
	if !ok {
		return domain.Build{}, domain.ErrNotFound
	}
	return b, nil
}

func (f *fakeBuildRepo) LatestForApplication(ctx context.Context, applicationID string) (domain.Build, error) {
	id, ok := f.byApp[applicationID]
	if !ok {
		return domain.Build{}, domain.ErrNotFound
	}
	return f.byID[id], nil
}

type fakeBaseImageRepo struct {
	byRuntime map[string]domain.BaseImage
}

func newFakeBaseImageRepo() *fakeBaseImageRepo {
	return &fakeBaseImageRepo{byRuntime: map[string]domain.BaseImage{
		"go":    {ID: "bi-go", Runtime: "go", ImageReference: "golang:1.23-alpine", Status: "active"},
		"react": {ID: "bi-react", Runtime: "react", ImageReference: "node:20-alpine", Status: "active"},
		"php":   {ID: "bi-php", Runtime: "php", ImageReference: "php:8-alpine", Status: "blocked"},
	}}
}

func (f *fakeBaseImageRepo) GetForRuntime(ctx context.Context, runtime string) (domain.BaseImage, error) {
	bi, ok := f.byRuntime[runtime]
	if !ok {
		return domain.BaseImage{}, domain.ErrNotFound
	}
	return bi, nil
}

type fakeBuildEngine struct {
	result  map[string]string
	err     error
	called  bool
	lastReq service.BuildEngineRequest
}

func (f *fakeBuildEngine) Build(ctx context.Context, req service.BuildEngineRequest) (map[string]string, error) {
	f.called = true
	f.lastReq = req
	return f.result, f.err
}

func newBuildService(app domain.Application, ownerID string) (*service.BuildService, *fakeLifecycleRepo, *fakeBuildRepo, *fakeBuildEngine) {
	apps := newFakeLifecycleRepo(app)
	owners := newFakeOwnerRepo()
	owners.owners[app.ID] = []domain.ApplicationOwner{{
		ApplicationID: app.ID, UserID: ownerID, OwnershipRole: domain.OwnerRolePrimary, Status: "active",
	}}
	builds := newFakeBuildRepo()
	baseImages := newFakeBaseImageRepo()
	engine := &fakeBuildEngine{result: map[string]string{"frontend": "platform-build/app-frontend:abc123"}}
	return service.NewBuildService(apps, owners, builds, baseImages, engine), apps, builds, engine
}

const validatedYAML = `
app:
  name: overtime
  owner: HR
services:
  frontend:
    runtime: react
`

func validatedApp(id, name string) domain.Application {
	return domain.Application{
		ID: id, Name: name, LifecycleStatus: domain.StatusValidated, DeploymentYAMLDraft: validatedYAML,
	}
}

func TestTriggerBuild_Success_TransitionsToBuildAndRecordsImageRefs(t *testing.T) {
	app := validatedApp("app-1", "overtime")
	svc, lifecycle, _, engine := newBuildService(app, "owner-1")

	build, err := svc.TriggerBuild(context.Background(), "app-1", "owner-1", []byte("fake-archive"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if build.Status != domain.BuildSucceeded {
		t.Errorf("expected build to succeed, got %q (detail=%v)", build.Status, build.ErrorDetail)
	}
	if build.ImageRefs["frontend"] != "platform-build/app-frontend:abc123" {
		t.Errorf("expected image ref recorded, got %+v", build.ImageRefs)
	}
	if lifecycle.apps["app-1"].LifecycleStatus != domain.StatusBuild {
		t.Errorf("expected application lifecycle status Build, got %q", lifecycle.apps["app-1"].LifecycleStatus)
	}
	if !engine.called {
		t.Error("expected the build engine to be invoked")
	}
	if engine.lastReq.Services["frontend"].BaseImage != "node:20-alpine" {
		t.Errorf("expected resolved base image passed to engine, got %+v", engine.lastReq.Services)
	}
}

func TestTriggerBuild_EngineFailure_MarksBuildAndApplicationFailed(t *testing.T) {
	app := validatedApp("app-1", "overtime")
	svc, lifecycle, _, engine := newBuildService(app, "owner-1")
	engine.err = &domain.BuildFailure{Category: domain.ErrorCategorySource, Service: "frontend", Detail: "npm install failed"}

	build, err := svc.TriggerBuild(context.Background(), "app-1", "owner-1", []byte("fake-archive"))
	if err != nil {
		t.Fatalf("a failed build is a normal outcome, expected nil error, got: %v", err)
	}
	if build.Status != domain.BuildFailed {
		t.Errorf("expected build status Failed, got %q", build.Status)
	}
	if build.ErrorCategory == nil || *build.ErrorCategory != domain.ErrorCategorySource {
		t.Errorf("expected error category 'source', got %v", build.ErrorCategory)
	}
	if lifecycle.apps["app-1"].LifecycleStatus != domain.StatusFailed {
		t.Errorf("expected application lifecycle status Failed, got %q", lifecycle.apps["app-1"].LifecycleStatus)
	}
}

func TestTriggerBuild_NotValidated_Rejected(t *testing.T) {
	app := validatedApp("app-1", "overtime")
	app.LifecycleStatus = domain.StatusDraft
	svc, _, _, _ := newBuildService(app, "owner-1")

	_, err := svc.TriggerBuild(context.Background(), "app-1", "owner-1", []byte("archive"))
	if !errors.Is(err, domain.ErrNotValidated) {
		t.Errorf("expected ErrNotValidated, got %v", err)
	}
}

func TestTriggerBuild_NonOwner_Rejected(t *testing.T) {
	app := validatedApp("app-1", "overtime")
	svc, _, _, _ := newBuildService(app, "owner-1")

	_, err := svc.TriggerBuild(context.Background(), "app-1", "stranger", []byte("archive"))
	if !errors.Is(err, domain.ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized, got %v", err)
	}
}

func TestTriggerBuild_EmptyArchive_Rejected(t *testing.T) {
	app := validatedApp("app-1", "overtime")
	svc, _, _, engine := newBuildService(app, "owner-1")

	_, err := svc.TriggerBuild(context.Background(), "app-1", "owner-1", nil)
	if !errors.Is(err, domain.ErrNoSourceArchive) {
		t.Errorf("expected ErrNoSourceArchive, got %v", err)
	}
	if engine.called {
		t.Error("engine should not be invoked when there's no archive")
	}
}

func TestTriggerBuild_UnsupportedOrBlockedRuntime_Rejected(t *testing.T) {
	app := validatedApp("app-1", "overtime")
	app.DeploymentYAMLDraft = `
app:
  name: overtime
  owner: HR
services:
  api:
    runtime: php
    port: 8080
`
	svc, _, _, _ := newBuildService(app, "owner-1")

	_, err := svc.TriggerBuild(context.Background(), "app-1", "owner-1", []byte("archive"))
	if !errors.Is(err, domain.ErrNoBaseImageForRuntime) {
		t.Errorf("expected ErrNoBaseImageForRuntime for a blocked runtime, got %v", err)
	}
}

func TestTriggerBuild_AlreadyInFlight_Rejected(t *testing.T) {
	app := validatedApp("app-1", "overtime")
	svc, _, builds, engine := newBuildService(app, "owner-1")
	// Simulate a build already queued for this application.
	_, _ = builds.Create(context.Background(), "app-1", "owner-1")

	_, err := svc.TriggerBuild(context.Background(), "app-1", "owner-1", []byte("archive"))
	if !errors.Is(err, domain.ErrBuildAlreadyInFlight) {
		t.Errorf("expected ErrBuildAlreadyInFlight, got %v", err)
	}
	if engine.called {
		t.Error("engine should not be invoked when a build is already in flight")
	}
}
