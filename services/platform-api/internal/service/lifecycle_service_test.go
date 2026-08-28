package service_test

import (
	"context"
	"errors"
	"testing"

	"platform-api/internal/domain"
	"platform-api/internal/service"
)

func newLifecycleService(app domain.Application, deployment domain.Deployment, ownerID string) (
	*service.LifecycleService, *fakeLifecycleRepo, *fakeDeploymentRepo, *fakeServiceRuntimeStateRepo, *fakeRuntime,
) {
	apps := newFakeLifecycleRepo(app)
	owners := newFakeOwnerRepo()
	owners.owners[app.ID] = []domain.ApplicationOwner{{
		ApplicationID: app.ID, UserID: ownerID, OwnershipRole: domain.OwnerRolePrimary, Status: "active",
	}}
	deployments := newFakeDeploymentRepo()
	deployments.byID[deployment.ID] = deployment
	states := newFakeServiceRuntimeStateRepo()
	runtime := newHealthyRuntime()

	svc := service.NewLifecycleService(apps, owners, deployments, states, runtime)
	return svc, apps, deployments, states, runtime
}

func runningAppAndDeployment(appID, deploymentID string) (domain.Application, domain.Deployment) {
	app := domain.Application{ID: appID, Name: "overtime", LifecycleStatus: domain.StatusRunning}
	deployment := domain.Deployment{ID: deploymentID, ApplicationID: appID, Status: domain.DeploymentRunning}
	return app, deployment
}

func TestSuspend_StopsAllContainersRegardlessOfEligibility(t *testing.T) {
	app, deployment := runningAppAndDeployment("app-1", "dep-1")
	svc, apps, deployments, states, runtime := newLifecycleService(app, deployment, "owner-1")

	frontContainer, apiContainer := "c-front", "c-api"
	frontPort, apiPort := 111, 222
	states.byKey[stateKey("dep-1", "frontend")] = domain.ServiceRuntimeState{
		DeploymentID: "dep-1", ServiceName: "frontend", Eligible: false, ContainerID: &frontContainer, HostPort: &frontPort,
	}
	states.byKey[stateKey("dep-1", "api")] = domain.ServiceRuntimeState{
		DeploymentID: "dep-1", ServiceName: "api", Eligible: true, ContainerID: &apiContainer, HostPort: &apiPort,
	}

	result, err := svc.Suspend(context.Background(), "app-1", "owner-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != domain.DeploymentSuspended {
		t.Errorf("expected deployment Suspended, got %q", result.Status)
	}
	if apps.apps["app-1"].LifecycleStatus != domain.StatusSuspended {
		t.Errorf("expected application Suspended, got %q", apps.apps["app-1"].LifecycleStatus)
	}
	if len(runtime.stopped) != 2 {
		t.Errorf("expected both frontend and api (ineligible + eligible) stopped, got %v", runtime.stopped)
	}
	for _, name := range []string{"frontend", "api"} {
		st, _ := states.Get(context.Background(), "dep-1", name)
		if !st.ScaledToZero() {
			t.Errorf("expected %s to have no container after suspend", name)
		}
	}
	if deployments.byID["dep-1"].Status != domain.DeploymentSuspended {
		t.Error("expected persisted deployment status to be Suspended")
	}
}

func TestSuspend_NotRunning_Rejected(t *testing.T) {
	app, deployment := runningAppAndDeployment("app-1", "dep-1")
	app.LifecycleStatus = domain.StatusBuild
	svc, _, _, _, _ := newLifecycleService(app, deployment, "owner-1")

	_, err := svc.Suspend(context.Background(), "app-1", "owner-1")
	if !errors.Is(err, domain.ErrApplicationNotRunning) {
		t.Errorf("expected ErrApplicationNotRunning, got %v", err)
	}
}

func TestSuspend_NonOwner_Rejected(t *testing.T) {
	app, deployment := runningAppAndDeployment("app-1", "dep-1")
	svc, _, _, _, _ := newLifecycleService(app, deployment, "owner-1")

	_, err := svc.Suspend(context.Background(), "app-1", "stranger")
	if !errors.Is(err, domain.ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized, got %v", err)
	}
}

func TestResume_StartsIneligibleServices_LeavesEligibleAtZero(t *testing.T) {
	app, deployment := runningAppAndDeployment("app-1", "dep-1")
	app.LifecycleStatus = domain.StatusSuspended
	deployment.Status = domain.DeploymentSuspended
	svc, apps, deployments, states, runtime := newLifecycleService(app, deployment, "owner-1")

	states.byKey[stateKey("dep-1", "frontend")] = domain.ServiceRuntimeState{
		DeploymentID: "dep-1", ServiceName: "frontend", ImageRef: "img-front", ContainerPort: 3000, Eligible: false,
	}
	states.byKey[stateKey("dep-1", "api")] = domain.ServiceRuntimeState{
		DeploymentID: "dep-1", ServiceName: "api", ImageRef: "img-api", ContainerPort: 8080, Eligible: true,
	}

	result, err := svc.Resume(context.Background(), "app-1", "owner-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != domain.DeploymentRunning {
		t.Errorf("expected deployment Running, got %q", result.Status)
	}
	if apps.apps["app-1"].LifecycleStatus != domain.StatusRunning {
		t.Errorf("expected application Running, got %q", apps.apps["app-1"].LifecycleStatus)
	}
	if len(runtime.started) != 1 {
		t.Errorf("expected exactly one container started (the ineligible frontend), got %v", runtime.started)
	}

	front, _ := states.Get(context.Background(), "dep-1", "frontend")
	if front.ScaledToZero() {
		t.Error("expected the ineligible frontend service to be started on resume")
	}
	api, _ := states.Get(context.Background(), "dep-1", "api")
	if !api.ScaledToZero() {
		t.Error("expected the eligible api service to remain scaled to zero on resume (cold-starts on demand)")
	}
	if deployments.byID["dep-1"].Status != domain.DeploymentRunning {
		t.Error("expected persisted deployment status to be Running")
	}
}

func TestResume_NotSuspended_Rejected(t *testing.T) {
	app, deployment := runningAppAndDeployment("app-1", "dep-1")
	svc, _, _, _, _ := newLifecycleService(app, deployment, "owner-1") // app is Running, not Suspended

	_, err := svc.Resume(context.Background(), "app-1", "owner-1")
	if !errors.Is(err, domain.ErrApplicationNotSuspended) {
		t.Errorf("expected ErrApplicationNotSuspended, got %v", err)
	}
}

func TestResume_HealthCheckFails_ReportsFailureNotSilentRunning(t *testing.T) {
	app, deployment := runningAppAndDeployment("app-1", "dep-1")
	app.LifecycleStatus = domain.StatusSuspended
	deployment.Status = domain.DeploymentSuspended
	svc, apps, _, states, runtime := newLifecycleService(app, deployment, "owner-1")
	runtime.healthCheckErr = errors.New("connection refused")

	states.byKey[stateKey("dep-1", "frontend")] = domain.ServiceRuntimeState{
		DeploymentID: "dep-1", ServiceName: "frontend", ImageRef: "img-front", ContainerPort: 3000, Eligible: false,
	}

	_, err := svc.Resume(context.Background(), "app-1", "owner-1")
	if err == nil {
		t.Fatal("expected an error when resume's health check fails")
	}
	// FR-048 alt flow: must not be silently marked Running.
	if apps.apps["app-1"].LifecycleStatus == domain.StatusRunning {
		t.Error("expected application to remain Suspended, not silently Running, after a failed resume health check")
	}
}

func TestRestart_RecyclesRunningInstances_SkipsScaledToZero(t *testing.T) {
	app, deployment := runningAppAndDeployment("app-1", "dep-1")
	svc, _, deployments, states, runtime := newLifecycleService(app, deployment, "owner-1")

	oldContainer := "c-old-api"
	oldPort := 999
	states.byKey[stateKey("dep-1", "api")] = domain.ServiceRuntimeState{
		DeploymentID: "dep-1", ServiceName: "api", ImageRef: "img-api", ContainerPort: 8080,
		Eligible: false, ContainerID: &oldContainer, HostPort: &oldPort,
	}
	states.byKey[stateKey("dep-1", "worker")] = domain.ServiceRuntimeState{
		DeploymentID: "dep-1", ServiceName: "worker", ImageRef: "img-worker", ContainerPort: 9090,
		Eligible: true, ContainerID: nil, HostPort: nil, // currently scaled to zero
	}

	result, err := svc.Restart(context.Background(), "app-1", "owner-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != domain.DeploymentRunning {
		t.Errorf("expected deployment to remain Running, got %q", result.Status)
	}
	if len(runtime.stopped) != 1 || runtime.stopped[0] != oldContainer {
		t.Errorf("expected exactly the old api container stopped, got %v", runtime.stopped)
	}
	if len(runtime.started) != 1 {
		t.Errorf("expected exactly one new instance started (api only, worker is idle), got %v", runtime.started)
	}
	api, _ := states.Get(context.Background(), "dep-1", "api")
	if api.ScaledToZero() {
		t.Error("expected api to have a fresh container after restart")
	}
	if *api.ContainerID == oldContainer {
		t.Error("expected a genuinely new container id after restart, not the old one")
	}
	worker, _ := states.Get(context.Background(), "dep-1", "worker")
	if !worker.ScaledToZero() {
		t.Error("expected the idle worker service to remain untouched by restart")
	}
	if deployments.byID["dep-1"].Status != domain.DeploymentRunning {
		t.Error("expected persisted deployment status to remain Running")
	}
}

func TestRestart_NotRunning_Rejected(t *testing.T) {
	app, deployment := runningAppAndDeployment("app-1", "dep-1")
	app.LifecycleStatus = domain.StatusSuspended
	svc, _, _, _, _ := newLifecycleService(app, deployment, "owner-1")

	_, err := svc.Restart(context.Background(), "app-1", "owner-1")
	if !errors.Is(err, domain.ErrApplicationNotRunning) {
		t.Errorf("expected ErrApplicationNotRunning, got %v", err)
	}
}
