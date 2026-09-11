package service_test

import (
	"context"
	"strings"
	"testing"

	"platform-api/internal/domain"
)

// Four separate code paths start an application container: deploy, resume,
// restart, and scale-to-zero cold start. Each one has to hand the
// container its database environment and attach it to the application's
// private network — and a path that forgets fails *silently*, with an
// application that starts fine and then can't reach its own data.
//
// These tests exist because that class of bug is invisible to every other
// test in this package: they all use an application with no database, so
// missing wiring looks exactly like correct wiring.

func assertWired(t *testing.T, spec domain.ContainerSpec, where string) {
	t.Helper()
	if spec.NetworkID != "net-test" {
		t.Errorf("%s: container %q was not attached to the application's private network (FR-062), got NetworkID=%q", where, spec.Name, spec.NetworkID)
	}
	env := strings.Join(spec.Env, "\n")
	if !strings.Contains(env, "DATABASE_URL=postgres://") {
		t.Errorf("%s: container %q did not receive its database connection details (FR-063), got env=%v", where, spec.Name, spec.Env)
	}
}

// Module S tags every container a start path creates, so each line it
// writes is attributed to its application, deployment and service
// (FR-086) — the same four paths, and the same way to miss one.
func assertTagged(t *testing.T, spec domain.ContainerSpec, where string) {
	t.Helper()
	if spec.Log.ApplicationID != "app-1" || spec.Log.DeploymentID == "" || spec.Log.Service == "" {
		t.Errorf("%s: container %q is not tagged for log collection: %+v", where, spec.Name, spec.Log)
	}
}

func TestDeploy_WiresDatabaseIntoTheContainer(t *testing.T) {
	app, build := builtApp("app-1", "overtime")
	app.DeploymentYAMLDraft = "app:\n  name: overtime\n  owner: HR\nservices:\n  api:\n    runtime: go\n    port: 8080\ndatabase:\n  type: postgres\n"
	databases := newFakeDatabaseService().withDatabase()
	svc, _, _, runtime, _, _ := newDeployServiceWithDatabase(app, build, "owner-1", databases)

	if _, err := svc.InitiateDeploy(context.Background(), "app-1", "owner-1", domain.EnvironmentDev); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// FR-061: declaring a database in deployment.yaml is what provisions one.
	if len(databases.provisioned) != 1 || databases.provisioned[0] != "app-1" {
		t.Fatalf("expected the declared database to be provisioned, got %v", databases.provisioned)
	}
	if len(runtime.startedSpecs) != 1 {
		t.Fatalf("expected one container started, got %d", len(runtime.startedSpecs))
	}
	assertWired(t, runtime.startedSpecs[0], "deploy")
	assertTagged(t, runtime.startedSpecs[0], "deploy")
}

// An application that declares no database must be unaffected by Module N
// — same container, no network, no injected environment.
func TestDeploy_WithoutADatabaseProvisionsNothing(t *testing.T) {
	app, build := builtApp("app-1", "overtime") // the default YAML declares no database
	databases := newFakeDatabaseService()
	svc, _, _, runtime, _, _ := newDeployServiceWithDatabase(app, build, "owner-1", databases)

	if _, err := svc.InitiateDeploy(context.Background(), "app-1", "owner-1", domain.EnvironmentDev); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(databases.provisioned) != 0 {
		t.Fatalf("an application declaring no database had one provisioned: %v", databases.provisioned)
	}
	if len(runtime.startedSpecs) != 1 {
		t.Fatalf("expected one container started, got %d", len(runtime.startedSpecs))
	}
	if spec := runtime.startedSpecs[0]; spec.NetworkID != "" || len(spec.Env) != 0 {
		t.Fatalf("expected an unmodified container spec, got NetworkID=%q env=%v", spec.NetworkID, spec.Env)
	}
}

func TestResume_WiresDatabaseIntoTheContainer(t *testing.T) {
	app, deployment := runningAppAndDeployment("app-1", "dep-1")
	app.LifecycleStatus = domain.StatusSuspended
	deployment.Status = domain.DeploymentSuspended
	databases := newFakeDatabaseService().withDatabase()
	svc, _, _, states, runtime := newLifecycleServiceWithDatabase(app, deployment, "owner-1", databases)

	states.byKey[stateKey("dep-1", "frontend")] = domain.ServiceRuntimeState{
		DeploymentID: "dep-1", ServiceName: "frontend", ImageRef: "img-front", ContainerPort: 3000, Eligible: false,
	}

	if _, err := svc.Resume(context.Background(), "app-1", "owner-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(runtime.startedSpecs) != 1 {
		t.Fatalf("expected one container started on resume, got %d", len(runtime.startedSpecs))
	}
	assertWired(t, runtime.startedSpecs[0], "resume")
	assertTagged(t, runtime.startedSpecs[0], "resume")
}

func TestRestart_WiresDatabaseIntoTheContainer(t *testing.T) {
	app, deployment := runningAppAndDeployment("app-1", "dep-1")
	databases := newFakeDatabaseService().withDatabase()
	svc, _, _, states, runtime := newLifecycleServiceWithDatabase(app, deployment, "owner-1", databases)

	oldContainer, oldPort := "c-old-api", 999
	states.byKey[stateKey("dep-1", "api")] = domain.ServiceRuntimeState{
		DeploymentID: "dep-1", ServiceName: "api", ImageRef: "img-api", ContainerPort: 8080,
		Eligible: false, ContainerID: &oldContainer, HostPort: &oldPort,
	}

	if _, err := svc.Restart(context.Background(), "app-1", "owner-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(runtime.startedSpecs) != 1 {
		t.Fatalf("expected one container started on restart, got %d", len(runtime.startedSpecs))
	}
	assertWired(t, runtime.startedSpecs[0], "restart")
	assertTagged(t, runtime.startedSpecs[0], "restart")
}

// The one most easily missed: a cold start carries only a deployment id,
// so it has to resolve the application before it can find the database.
func TestColdStart_WiresDatabaseIntoTheContainer(t *testing.T) {
	databases := newFakeDatabaseService().withDatabase()
	svc, states, _, runtime, deployments := newScaleServiceWithDatabase(databases)

	deployments.byID["dep-1"] = domain.Deployment{ID: "dep-1", ApplicationID: "app-1", Status: domain.DeploymentRunning}
	states.byKey[stateKey("dep-1", "api")] = domain.ServiceRuntimeState{
		DeploymentID: "dep-1", ServiceName: "api", ImageRef: "img-api", ContainerPort: 8080,
		Eligible: true, ContainerID: nil, HostPort: nil, // scaled to zero
	}

	if _, err := svc.EnsureRunning(context.Background(), "dep-1", "api"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(runtime.startedSpecs) != 1 {
		t.Fatalf("expected one container cold-started, got %d", len(runtime.startedSpecs))
	}
	assertWired(t, runtime.startedSpecs[0], "cold start")
	assertTagged(t, runtime.startedSpecs[0], "cold start")
}

// FR-065: no live database instance survives a deleted application.
func TestDelete_DeprovisionsTheDatabase(t *testing.T) {
	app, deployment := runningAppAndDeployment("app-1", "dep-1")
	app.LifecycleStatus = domain.StatusArchived
	databases := newFakeDatabaseService().withDatabase()
	svc, _, _, _, _ := newLifecycleServiceWithDatabase(app, deployment, "owner-1", databases)

	if _, err := svc.Delete(context.Background(), "app-1", "owner-1", true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(databases.deprovisioned) != 1 || databases.deprovisioned[0] != "app-1" {
		t.Fatalf("expected the application's database to be deprovisioned on delete, got %v", databases.deprovisioned)
	}
}
