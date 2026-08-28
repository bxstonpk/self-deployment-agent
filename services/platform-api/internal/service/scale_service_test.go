package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"platform-api/internal/domain"
	"platform-api/internal/service"
)

type fakeServiceRuntimeStateRepo struct {
	byKey map[string]domain.ServiceRuntimeState
}

func newFakeServiceRuntimeStateRepo() *fakeServiceRuntimeStateRepo {
	return &fakeServiceRuntimeStateRepo{byKey: map[string]domain.ServiceRuntimeState{}}
}

func stateKey(deploymentID, serviceName string) string { return deploymentID + ":" + serviceName }

func (f *fakeServiceRuntimeStateRepo) Upsert(ctx context.Context, s domain.ServiceRuntimeState) error {
	s.LastActiveAt = time.Now()
	f.byKey[stateKey(s.DeploymentID, s.ServiceName)] = s
	return nil
}

func (f *fakeServiceRuntimeStateRepo) Get(ctx context.Context, deploymentID, serviceName string) (domain.ServiceRuntimeState, error) {
	s, ok := f.byKey[stateKey(deploymentID, serviceName)]
	if !ok {
		return domain.ServiceRuntimeState{}, domain.ErrServiceStateNotFound
	}
	return s, nil
}

func (f *fakeServiceRuntimeStateRepo) SetContainer(ctx context.Context, deploymentID, serviceName, containerID string, hostPort int) error {
	s := f.byKey[stateKey(deploymentID, serviceName)]
	s.ContainerID = &containerID
	s.HostPort = &hostPort
	s.LastActiveAt = time.Now()
	f.byKey[stateKey(deploymentID, serviceName)] = s
	return nil
}

func (f *fakeServiceRuntimeStateRepo) ClearContainer(ctx context.Context, deploymentID, serviceName, expectedContainerID string) (bool, error) {
	key := stateKey(deploymentID, serviceName)
	s, ok := f.byKey[key]
	if !ok || s.ContainerID == nil || *s.ContainerID != expectedContainerID {
		return false, nil // mirrors the real WHERE container_id = expected CAS
	}
	s.ContainerID = nil
	s.HostPort = nil
	f.byKey[key] = s
	return true, nil
}

func (f *fakeServiceRuntimeStateRepo) TouchActive(ctx context.Context, deploymentID, serviceName string) error {
	key := stateKey(deploymentID, serviceName)
	s := f.byKey[key]
	s.LastActiveAt = time.Now()
	f.byKey[key] = s
	return nil
}

func (f *fakeServiceRuntimeStateRepo) ListEligibleActive(ctx context.Context) ([]domain.ServiceRuntimeState, error) {
	var out []domain.ServiceRuntimeState
	for _, s := range f.byKey {
		if s.Eligible && s.ContainerID != nil {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakeServiceRuntimeStateRepo) ListForDeployment(ctx context.Context, deploymentID string) ([]domain.ServiceRuntimeState, error) {
	var out []domain.ServiceRuntimeState
	for _, s := range f.byKey {
		if s.DeploymentID == deploymentID {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakeServiceRuntimeStateRepo) DeleteForDeployment(ctx context.Context, deploymentID string) error {
	for k, s := range f.byKey {
		if s.DeploymentID == deploymentID {
			delete(f.byKey, k)
		}
	}
	return nil
}

type fakeScaleEventRepo struct {
	events []domain.ScaleEvent
}

func (f *fakeScaleEventRepo) Record(ctx context.Context, deploymentID, serviceName string, direction domain.ScaleDirection, triggerReason string) error {
	f.events = append(f.events, domain.ScaleEvent{
		DeploymentID: deploymentID, ServiceName: serviceName, Direction: direction,
		TriggerReason: triggerReason, OccurredAt: time.Now(),
	})
	return nil
}

func (f *fakeScaleEventRepo) ListForApplication(ctx context.Context, deploymentIDs []string, limit int) ([]domain.ScaleEvent, error) {
	return f.events, nil
}

type fakeAppByNameRepo struct {
	byName map[string]domain.Application
}

func (f *fakeAppByNameRepo) GetByName(ctx context.Context, name string) (domain.Application, error) {
	app, ok := f.byName[name]
	if !ok {
		return domain.Application{}, domain.ErrApplicationNotFound
	}
	return app, nil
}

type fakeRunningDeploymentLookup struct {
	byApp map[string]domain.Deployment
}

func (f *fakeRunningDeploymentLookup) CurrentRunning(ctx context.Context, applicationID string) (domain.Deployment, error) {
	d, ok := f.byApp[applicationID]
	if !ok {
		return domain.Deployment{}, domain.ErrNoRunningDeployment
	}
	return d, nil
}

func newScaleService() (*service.ScaleService, *fakeServiceRuntimeStateRepo, *fakeScaleEventRepo, *fakeRuntime) {
	states := newFakeServiceRuntimeStateRepo()
	events := &fakeScaleEventRepo{}
	stacks := newFakeStackRepo()
	runtime := newHealthyRuntime()
	apps := &fakeAppByNameRepo{byName: map[string]domain.Application{}}
	deployments := &fakeRunningDeploymentLookup{byApp: map[string]domain.Deployment{}}
	svc := service.NewScaleService(apps, deployments, states, events, stacks, runtime)
	return svc, states, events, runtime
}

const multiServiceYAML = `
app:
  name: overtime
  owner: HR
services:
  frontend:
    runtime: react
  api:
    runtime: go
    port: 8080
`

func TestInitializeForDeployment_BackendEligible_FrontendNever(t *testing.T) {
	svc, states, events, _ := newScaleService()

	containers := map[string]domain.RunningContainer{
		"frontend": {ContainerID: "c-front", HostPort: 10001},
		"api":      {ContainerID: "c-api", HostPort: 10002},
	}
	imageRefs := map[string]string{"frontend": "img-front", "api": "img-api"}

	err := svc.InitializeForDeployment(context.Background(), "dep-1", multiServiceYAML, imageRefs, containers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	frontend, err := states.Get(context.Background(), "dep-1", "frontend")
	if err != nil {
		t.Fatalf("frontend state: %v", err)
	}
	if frontend.Eligible {
		t.Error("FR-051: static frontend must never be scale-to-zero eligible")
	}

	api, err := states.Get(context.Background(), "dep-1", "api")
	if err != nil {
		t.Fatalf("api state: %v", err)
	}
	if !api.Eligible {
		t.Error("expected backend/API service to be scale-to-zero eligible with scaling.min unset")
	}

	if len(events.events) != 2 {
		t.Errorf("expected 2 initial_activation events, got %d", len(events.events))
	}
}

func TestInitializeForDeployment_ScalingMinOptOut_MakesBackendIneligible(t *testing.T) {
	svc, states, _, _ := newScaleService()
	yamlWithMin := `
app:
  name: overtime
  owner: HR
services:
  api:
    runtime: go
    port: 8080
scaling:
  min: 1
`
	containers := map[string]domain.RunningContainer{"api": {ContainerID: "c-api", HostPort: 10002}}
	err := svc.InitializeForDeployment(context.Background(), "dep-1", yamlWithMin, map[string]string{"api": "img-api"}, containers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	api, _ := states.Get(context.Background(), "dep-1", "api")
	if api.Eligible {
		t.Error("FR-055: scaling.min >= 1 must opt the service out of scale-to-zero")
	}
}

func TestEnsureRunning_AlreadyActive_TouchesActiveWithoutColdStart(t *testing.T) {
	svc, states, _, runtime := newScaleService()
	containerID := "c-api"
	hostPort := 12345
	_ = states.Upsert(context.Background(), domain.ServiceRuntimeState{
		DeploymentID: "dep-1", ServiceName: "api", ImageRef: "img-api", ContainerPort: 8080,
		Eligible: true, ContainerID: &containerID, HostPort: &hostPort,
	})

	port, err := svc.EnsureRunning(context.Background(), "dep-1", "api")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if port != hostPort {
		t.Errorf("expected existing host port %d, got %d", hostPort, port)
	}
	if len(runtime.started) != 0 {
		t.Error("expected no cold start when the service is already active")
	}
}

func TestEnsureRunning_ScaledToZero_ColdStartsAndRecordsEvent(t *testing.T) {
	svc, states, events, runtime := newScaleService()
	_ = states.Upsert(context.Background(), domain.ServiceRuntimeState{
		DeploymentID: "dep-1", ServiceName: "api", ImageRef: "img-api", ContainerPort: 8080,
		Eligible: true, ContainerID: nil, HostPort: nil,
	})

	port, err := svc.EnsureRunning(context.Background(), "dep-1", "api")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if port == 0 {
		t.Error("expected a nonzero host port after cold start")
	}
	if len(runtime.started) != 1 {
		t.Errorf("expected exactly one cold-start container, got %v", runtime.started)
	}

	state, _ := states.Get(context.Background(), "dep-1", "api")
	if state.ScaledToZero() {
		t.Error("expected state to show a live container after cold start")
	}

	found := false
	for _, e := range events.events {
		if e.Direction == domain.ScaledUp && e.TriggerReason == domain.TriggerColdStart {
			found = true
		}
	}
	if !found {
		t.Error("expected a scaled_up/cold_start event to be recorded")
	}
}

func TestEnsureRunning_ColdStartHealthCheckFails_StopsContainerAndReturnsError(t *testing.T) {
	svc, states, _, runtime := newScaleService()
	runtime.healthCheckErr = errors.New("connection refused")
	_ = states.Upsert(context.Background(), domain.ServiceRuntimeState{
		DeploymentID: "dep-1", ServiceName: "api", ImageRef: "img-api", ContainerPort: 8080,
		Eligible: true, ContainerID: nil, HostPort: nil,
	})

	_, err := svc.EnsureRunning(context.Background(), "dep-1", "api")
	if err == nil {
		t.Fatal("expected an error when the cold-started container fails its health check")
	}
	if len(runtime.stopped) != 1 {
		t.Errorf("expected the failed cold-start container to be stopped, got %v", runtime.stopped)
	}
	state, _ := states.Get(context.Background(), "dep-1", "api")
	if !state.ScaledToZero() {
		t.Error("expected state to remain scaled-to-zero after a failed cold start")
	}
}

func TestSweepIdle_IdleEligibleService_ScalesDownAndRecordsEvent(t *testing.T) {
	svc, states, events, runtime := newScaleService()
	containerID := "c-api"
	hostPort := 12345
	old := time.Now().Add(-time.Hour)
	states.byKey[stateKey("dep-1", "api")] = domain.ServiceRuntimeState{
		DeploymentID: "dep-1", ServiceName: "api", ImageRef: "img-api", ContainerPort: 8080,
		Eligible: true, ContainerID: &containerID, HostPort: &hostPort, LastActiveAt: old,
	}

	scaledDown, err := svc.SweepIdle(context.Background(), 5*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if scaledDown != 1 {
		t.Errorf("expected 1 service scaled down, got %d", scaledDown)
	}
	if len(runtime.stopped) != 1 || runtime.stopped[0] != containerID {
		t.Errorf("expected the idle container to be stopped, got %v", runtime.stopped)
	}
	state, _ := states.Get(context.Background(), "dep-1", "api")
	if !state.ScaledToZero() {
		t.Error("expected state cleared to scaled-to-zero")
	}
	found := false
	for _, e := range events.events {
		if e.Direction == domain.ScaledToZero && e.TriggerReason == domain.TriggerIdleTimeout {
			found = true
		}
	}
	if !found {
		t.Error("expected a scaled_to_zero/idle_timeout event to be recorded")
	}
}

func TestSweepIdle_NotYetIdle_LeftRunning(t *testing.T) {
	svc, states, _, runtime := newScaleService()
	containerID := "c-api"
	hostPort := 12345
	states.byKey[stateKey("dep-1", "api")] = domain.ServiceRuntimeState{
		DeploymentID: "dep-1", ServiceName: "api", Eligible: true,
		ContainerID: &containerID, HostPort: &hostPort, LastActiveAt: time.Now(),
	}

	scaledDown, err := svc.SweepIdle(context.Background(), 5*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if scaledDown != 0 {
		t.Errorf("expected nothing scaled down for a recently active service, got %d", scaledDown)
	}
	if len(runtime.stopped) != 0 {
		t.Error("expected no container stopped")
	}
}

func TestSweepIdle_IneligibleService_NeverConsidered(t *testing.T) {
	svc, states, _, runtime := newScaleService()
	containerID := "c-front"
	hostPort := 12345
	old := time.Now().Add(-time.Hour)
	states.byKey[stateKey("dep-1", "frontend")] = domain.ServiceRuntimeState{
		DeploymentID: "dep-1", ServiceName: "frontend", Eligible: false,
		ContainerID: &containerID, HostPort: &hostPort, LastActiveAt: old,
	}

	scaledDown, err := svc.SweepIdle(context.Background(), 5*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if scaledDown != 0 {
		t.Errorf("expected an ineligible service never to be scaled down, got %d", scaledDown)
	}
	if len(runtime.stopped) != 0 {
		t.Error("expected no container stopped for an ineligible service")
	}
}

// TestClearContainer_CompareAndSwap_PreventsRaceDataLoss exercises the
// fake's CAS semantics directly (mirroring the real SQL WHERE
// container_id = expected) — this is the actual data-safety guard
// documented in ScaleService.SweepIdle against a concurrent cold-start
// replacing a container between the sweeper's read and its write.
func TestClearContainer_CompareAndSwap_PreventsRaceDataLoss(t *testing.T) {
	states := newFakeServiceRuntimeStateRepo()
	oldContainerID := "c-old"
	hostPort := 111
	_ = states.Upsert(context.Background(), domain.ServiceRuntimeState{
		DeploymentID: "dep-1", ServiceName: "api", ContainerID: &oldContainerID, HostPort: &hostPort,
	})

	// Simulate a concurrent cold-start swapping in a new container between
	// the sweeper's read and its clear.
	newContainerID := "c-new"
	_ = states.SetContainer(context.Background(), "dep-1", "api", newContainerID, 222)

	cleared, err := states.ClearContainer(context.Background(), "dep-1", "api", oldContainerID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cleared {
		t.Fatal("expected ClearContainer to refuse clearing when container_id no longer matches (CAS)")
	}
	state, _ := states.Get(context.Background(), "dep-1", "api")
	if state.ScaledToZero() {
		t.Error("expected the concurrently cold-started container to survive the stale clear attempt")
	}
	if *state.ContainerID != newContainerID {
		t.Errorf("expected state to still reference the new container, got %v", state.ContainerID)
	}
}
