package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"platform-api/internal/domain"
)

// Delete's second route: straight from a state that never went live — but
// only when nothing is actually serving or in progress. See
// LifecycleService.Delete for why the guard reads what's running rather
// than trusting the state's name.

func abandonedApp() domain.Application {
	return domain.Application{ID: "app-1", Name: "overtime", LifecycleStatus: domain.StatusDraft}
}

func buildRepoWith(status domain.BuildStatus) *fakeBuildRepo {
	builds := newFakeBuildRepo()
	builds.byID["b-1"] = domain.Build{ID: "b-1", ApplicationID: "app-1", Status: status}
	builds.byApp["app-1"] = "b-1"
	return builds
}

func TestDelete_FromDraft_NothingToStop_Succeeds(t *testing.T) {
	databases := newFakeDatabaseService()
	svc, apps, _, _, runtime := newLifecycleServiceWithBuilds(abandonedApp(), domain.Deployment{}, "owner-1", databases, newFakeBuildRepo())

	result, err := svc.Delete(context.Background(), "app-1", "owner-1", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.LifecycleStatus != domain.StatusDeleted || apps.apps["app-1"].LifecycleStatus != domain.StatusDeleted {
		t.Fatalf("expected the draft deleted, got %q", apps.apps["app-1"].LifecycleStatus)
	}
	if len(runtime.stopped) != 0 {
		t.Fatalf("nothing was running, yet something was stopped: %v", runtime.stopped)
	}
	// Even a draft can hold secrets (they can be set before a first
	// deploy), so its resources are still released.
	if len(databases.deprovisioned) != 1 {
		t.Fatalf("the draft's resources were not released: %v", databases.deprovisioned)
	}
}

func TestDelete_FromValidated_Succeeds(t *testing.T) {
	app := abandonedApp()
	app.LifecycleStatus = domain.StatusValidated
	svc, apps, _, _, _ := newLifecycleServiceWithBuilds(app, domain.Deployment{}, "owner-1", newFakeDatabaseService(), newFakeBuildRepo())

	if _, err := svc.Delete(context.Background(), "app-1", "owner-1", true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if apps.apps["app-1"].LifecycleStatus != domain.StatusDeleted {
		t.Fatalf("expected Deleted, got %q", apps.apps["app-1"].LifecycleStatus)
	}
}

// Built, never deployed — where an abandoned application most often ends up.
func TestDelete_FromBuild_BuiltButNeverDeployed_Succeeds(t *testing.T) {
	app := abandonedApp()
	app.LifecycleStatus = domain.StatusBuild
	svc, apps, _, _, _ := newLifecycleServiceWithBuilds(app, domain.Deployment{}, "owner-1", newFakeDatabaseService(), buildRepoWith(domain.BuildSucceeded))

	if _, err := svc.Delete(context.Background(), "app-1", "owner-1", true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if apps.apps["app-1"].LifecycleStatus != domain.StatusDeleted {
		t.Fatalf("expected Deleted, got %q", apps.apps["app-1"].LifecycleStatus)
	}
}

// A first deploy that provisioned a database and then failed leaves the
// application in Failed with that database still running — which, before
// this route existed, nothing could ever remove.
func TestDelete_FromFailed_FirstDeployFailed_DeprovisionsItsDatabase(t *testing.T) {
	app := abandonedApp()
	app.LifecycleStatus = domain.StatusFailed
	failed := domain.Deployment{ID: "dep-1", ApplicationID: "app-1", Status: domain.DeploymentFailed, CreatedAt: time.Now()}
	databases := newFakeDatabaseService().withDatabase()
	svc, apps, _, _, _ := newLifecycleServiceWithBuilds(app, failed, "owner-1", databases, buildRepoWith(domain.BuildSucceeded))

	if _, err := svc.Delete(context.Background(), "app-1", "owner-1", true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if apps.apps["app-1"].LifecycleStatus != domain.StatusDeleted {
		t.Fatalf("expected Deleted, got %q", apps.apps["app-1"].LifecycleStatus)
	}
	if len(databases.deprovisioned) != 1 || databases.deprovisioned[0] != "app-1" {
		t.Fatalf("the failed application's database was not deprovisioned: %v", databases.deprovisioned)
	}
}

// A rebuild of a running application leaves it in Build while the previous
// version keeps serving. Deleting that would be deleting something live.
func TestDelete_FromBuild_PreviousVersionStillServing_Refused(t *testing.T) {
	app := abandonedApp()
	app.LifecycleStatus = domain.StatusBuild
	live := domain.Deployment{ID: "dep-1", ApplicationID: "app-1", Status: domain.DeploymentRunning, CreatedAt: time.Now()}
	databases := newFakeDatabaseService().withDatabase()
	svc, apps, _, _, runtime := newLifecycleServiceWithBuilds(app, live, "owner-1", databases, buildRepoWith(domain.BuildSucceeded))

	_, err := svc.Delete(context.Background(), "app-1", "owner-1", true)
	if !errors.Is(err, domain.ErrApplicationStillLive) {
		t.Fatalf("expected ErrApplicationStillLive, got %v", err)
	}
	if apps.apps["app-1"].LifecycleStatus == domain.StatusDeleted {
		t.Fatal("a live application was deleted")
	}
	if len(runtime.stopped) != 0 || len(databases.deprovisioned) != 0 {
		t.Fatalf("a refused deletion still tore something down: stopped=%v deprovisioned=%v", runtime.stopped, databases.deprovisioned)
	}
}

// After a failed redeploy, the LATEST deployment record is the failed one
// while an older one still serves. A guard that read only the latest would
// wave this through.
func TestDelete_FromFailed_OlderDeploymentStillServing_Refused(t *testing.T) {
	app := abandonedApp()
	app.LifecycleStatus = domain.StatusFailed
	newer := domain.Deployment{ID: "dep-new", ApplicationID: "app-1", Status: domain.DeploymentFailed, CreatedAt: time.Now()}
	older := domain.Deployment{ID: "dep-old", ApplicationID: "app-1", Status: domain.DeploymentRunning, CreatedAt: time.Now().Add(-time.Hour)}
	svc, apps, deployments, _, _ := newLifecycleServiceWithBuilds(app, newer, "owner-1", newFakeDatabaseService(), buildRepoWith(domain.BuildSucceeded))
	deployments.byID[older.ID] = older

	if _, err := svc.Delete(context.Background(), "app-1", "owner-1", true); !errors.Is(err, domain.ErrApplicationStillLive) {
		t.Fatalf("expected ErrApplicationStillLive, got %v", err)
	}
	if apps.apps["app-1"].LifecycleStatus == domain.StatusDeleted {
		t.Fatal("deleted while an older deployment was still serving")
	}
}

// The spec's guard is literal: "no active deployment attempt exists".
func TestDelete_BuildInProgress_Refused(t *testing.T) {
	app := abandonedApp()
	app.LifecycleStatus = domain.StatusBuild
	svc, _, _, _, _ := newLifecycleServiceWithBuilds(app, domain.Deployment{}, "owner-1", newFakeDatabaseService(), buildRepoWith(domain.BuildInProgress))

	if _, err := svc.Delete(context.Background(), "app-1", "owner-1", true); !errors.Is(err, domain.ErrApplicationStillLive) {
		t.Fatalf("expected ErrApplicationStillLive, got %v", err)
	}
}

func TestDelete_DeploymentInFlight_Refused(t *testing.T) {
	app := abandonedApp()
	app.LifecycleStatus = domain.StatusBuild
	inFlight := domain.Deployment{ID: "dep-1", ApplicationID: "app-1", Status: domain.DeploymentDeploying, CreatedAt: time.Now()}
	svc, _, _, _, _ := newLifecycleServiceWithBuilds(app, inFlight, "owner-1", newFakeDatabaseService(), buildRepoWith(domain.BuildSucceeded))

	if _, err := svc.Delete(context.Background(), "app-1", "owner-1", true); !errors.Is(err, domain.ErrApplicationStillLive) {
		t.Fatalf("expected ErrApplicationStillLive, got %v", err)
	}
}

// The new route doesn't loosen anything else: still owner-only, still
// needs explicit confirmation.
func TestDelete_FromDraft_StillNeedsOwnerAndConfirmation(t *testing.T) {
	svc, apps, _, _, _ := newLifecycleServiceWithBuilds(abandonedApp(), domain.Deployment{}, "owner-1", newFakeDatabaseService(), newFakeBuildRepo())

	if _, err := svc.Delete(context.Background(), "app-1", "someone-else", true); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
	if _, err := svc.Delete(context.Background(), "app-1", "owner-1", false); !errors.Is(err, domain.ErrDeleteNotConfirmed) {
		t.Fatalf("expected ErrDeleteNotConfirmed, got %v", err)
	}
	if apps.apps["app-1"].LifecycleStatus == domain.StatusDeleted {
		t.Fatal("deleted without an owner's confirmation")
	}
}
