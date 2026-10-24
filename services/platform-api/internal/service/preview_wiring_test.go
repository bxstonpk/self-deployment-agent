package service_test

import (
	"context"
	"errors"
	"testing"

	"platform-api/internal/domain"
)

type fakePreviewParents map[string]string

func (f fakePreviewParents) ParentOf(ctx context.Context, applicationID string) (string, error) {
	return f[applicationID], nil
}

func TestDeploy_OnlyAPreviewDeploysToPreview_AndItDoesntWaitForApproval(t *testing.T) {
	app, build := builtApp("app-1", "leave")
	svc, _, _, _, _, _ := newDeployService(app, build, "owner-1")
	svc.SetApprovalEnvironments([]domain.Environment{domain.EnvironmentDev, domain.EnvironmentProduction})

	svc.SetPreviews(fakePreviewParents{})
	if _, err := svc.InitiateDeploy(reviewed(), "app-1", "owner-1", domain.EnvironmentPreview); !errors.Is(err, domain.ErrPreviewOnlyEnvironment) {
		t.Fatalf("an ordinary application can't deploy to preview: %v", err)
	}

	svc.SetPreviews(fakePreviewParents{"app-1": "parent-1"})
	if _, err := svc.InitiateDeploy(reviewed(), "app-1", "owner-1", domain.EnvironmentDev); !errors.Is(err, domain.ErrPreviewOnlyEnvironment) {
		t.Fatalf("a preview deploys only to preview: %v", err)
	}
	d, err := svc.InitiateDeploy(context.Background(), "app-1", "owner-1", domain.EnvironmentPreview)
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != domain.DeploymentRunning || d.Environment != domain.EnvironmentPreview {
		t.Fatalf("not gated (dev and production are): %s %s", d.Status, d.Environment)
	}
}

func TestDeploy_PreviewCanBeGatedToo(t *testing.T) {
	app, build := builtApp("app-1", "leave")
	svc, _, _, _, _, _ := newDeployService(app, build, "owner-1")
	svc.SetApprovalEnvironments([]domain.Environment{domain.EnvironmentPreview})
	svc.SetPreviews(fakePreviewParents{"app-1": "parent-1"})
	if _, err := svc.InitiateDeploy(context.Background(), "app-1", "owner-1", domain.EnvironmentPreview); !errors.Is(err, domain.ErrReviewDocumentRequired) {
		t.Fatalf("named in APPROVAL_REQUIRED_ENVIRONMENTS, a preview waits like any other: %v", err)
	}
}

func TestGovernance_APreviewBuildsWithItsApplicationsExceptions(t *testing.T) {
	g := newGovernance(t, false)
	ctx := context.Background()
	addRule(t, g, "npm", "left-pad")
	e, _, err := g.svc.RequestException(ctx, govOwner, "app-1", "dependency", "npm:left-pad", "vendor SDK")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.svc.DecideException(ctx, govAdmin, e.ID, true, ""); err != nil {
		t.Fatal(err)
	}
	g.svc.SetPreviews(fakePreviewParents{"pv-1": "app-1"})
	if err := g.svc.SourceFailure(ctx, "pv-1", tarGz(t, forbiddenSource)); err != nil {
		t.Fatalf("the preview of app-1 may use what app-1 may: %v", err)
	}
	if err := g.svc.SourceFailure(ctx, "other", tarGz(t, forbiddenSource)); err == nil {
		t.Fatal("another application may not")
	}
}
