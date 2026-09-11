package service_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"platform-api/internal/domain"
	"platform-api/internal/service"
)

// fakeHealthRuntime is deliberately not fakeRuntime (deploy_service_test.go):
// these tests need per-instance, per-sweep-call control over which health
// check succeeds or fails, keyed by the host port StartContainer itself
// assigns — fakeRuntime only offers one static result for every call.
type fakeHealthRuntime struct {
	mu        sync.Mutex
	failPorts map[int]bool
	startErr  error
	nextPort  int
	started   []domain.ContainerSpec
	stopped   []string
}

func newFakeHealthRuntime() *fakeHealthRuntime {
	return &fakeHealthRuntime{failPorts: map[int]bool{}, nextPort: 30000}
}

func portFromHealthURL(url string) int {
	port, _ := strconv.Atoi(url[strings.LastIndex(url, ":")+1:])
	return port
}

func (f *fakeHealthRuntime) HealthCheck(ctx context.Context, url string, timeout time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failPorts[portFromHealthURL(url)] {
		return fmt.Errorf("instance on %s is unhealthy", url)
	}
	return nil
}

func (f *fakeHealthRuntime) StartContainer(ctx context.Context, spec domain.ContainerSpec) (domain.RunningContainer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = append(f.started, spec)
	if f.startErr != nil {
		return domain.RunningContainer{}, f.startErr
	}
	f.nextPort++
	port := f.nextPort
	return domain.RunningContainer{ContainerID: fmt.Sprintf("container-%d", port), HostPort: port, URL: "http://localhost:0"}, nil
}

func (f *fakeHealthRuntime) Stop(ctx context.Context, containerID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, containerID)
	return nil
}

func (f *fakeHealthRuntime) fail(port int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failPorts[port] = true
}

func (f *fakeHealthRuntime) stoppedContainers() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.stopped))
	copy(out, f.stopped)
	return out
}

func (f *fakeHealthRuntime) startedSpecs() []domain.ContainerSpec {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.ContainerSpec, len(f.started))
	copy(out, f.started)
	return out
}

func newHealthMonitorService() (
	*service.HealthMonitorService, *fakeServiceRuntimeStateRepo, *fakeHealthRuntime, *fakeNotificationRecorder,
) {
	states := newFakeServiceRuntimeStateRepo()
	deployments := &fakeRunningDeploymentLookup{
		byApp: map[string]domain.Deployment{},
		byID:  map[string]domain.Deployment{"dep-1": {ID: "dep-1", ApplicationID: "app-1"}},
	}
	runtime := newFakeHealthRuntime()
	notify := newFakeNotificationRecorder()
	apps := &fakeApplicationRepo{byID: map[string]domain.Application{"app-1": {ID: "app-1", Name: "overtime"}}}
	resources := newFakeDatabaseService()
	svc := service.NewHealthMonitorService(apps, deployments, states, resources, runtime, notify)
	return svc, states, runtime, notify
}

func seedActiveState(states *fakeServiceRuntimeStateRepo, containerID string, hostPort int) {
	_ = states.Upsert(context.Background(), domain.ServiceRuntimeState{
		DeploymentID: "dep-1", ServiceName: "api", ImageRef: "img-api", ContainerPort: 8080,
		Eligible: true, ContainerID: &containerID, HostPort: &hostPort,
	})
}

func TestHealthSweep_HealthyInstance_NoAction(t *testing.T) {
	svc, states, runtime, notify := newHealthMonitorService()
	seedActiveState(states, "container-1", 9001)

	checked, remediated, err := svc.Sweep(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if checked != 1 || remediated != 0 {
		t.Fatalf("got checked=%d remediated=%d, want 1, 0", checked, remediated)
	}
	if len(runtime.stoppedContainers()) != 0 || len(runtime.startedSpecs()) != 0 {
		t.Error("a healthy instance must not be touched")
	}
	if len(notify.all()) != 0 {
		t.Error("a healthy instance must not generate a notification")
	}
}

func TestHealthSweep_ScaledToZero_NotPolled(t *testing.T) {
	svc, states, runtime, _ := newHealthMonitorService()
	// No live container — mirrors an idle, scaled-to-zero service.
	_ = states.Upsert(context.Background(), domain.ServiceRuntimeState{
		DeploymentID: "dep-1", ServiceName: "api", ImageRef: "img-api", ContainerPort: 8080, Eligible: true,
	})

	checked, remediated, err := svc.Sweep(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if checked != 0 || remediated != 0 {
		t.Fatalf("got checked=%d remediated=%d, want 0, 0 — FR-084: idle/zero instances have no health check to poll", checked, remediated)
	}
	if len(runtime.startedSpecs()) != 0 {
		t.Error("must never start a container for a service sitting at zero")
	}
}

func TestHealthSweep_SingleFailure_DoesNotRemediate(t *testing.T) {
	svc, states, runtime, notify := newHealthMonitorService()
	seedActiveState(states, "container-1", 9001)
	runtime.fail(9001)

	if _, remediated, err := svc.Sweep(context.Background()); err != nil || remediated != 0 {
		t.Fatalf("first miss must not remediate: remediated=%d err=%v", remediated, err)
	}
	if len(runtime.startedSpecs()) != 0 {
		t.Error("a single blip must not trigger a restart — only a sustained failure should (FR-084's debounce)")
	}
	if len(notify.all()) != 0 {
		t.Error("no notification before remediation is actually attempted")
	}
}

func TestHealthSweep_SustainedFailure_RemediatesAndNotifies(t *testing.T) {
	svc, states, runtime, notify := newHealthMonitorService()
	seedActiveState(states, "container-1", 9001)
	runtime.fail(9001)

	// Two consecutive sweeps see the same failure — this is what FR-084
	// means by "begins failing health checks".
	if _, remediated, _ := svc.Sweep(context.Background()); remediated != 0 {
		t.Fatalf("first sweep must not yet remediate")
	}
	_, remediated, err := svc.Sweep(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if remediated != 1 {
		t.Fatalf("second consecutive failure must trigger remediation, got remediated=%d", remediated)
	}

	if got := runtime.stoppedContainers(); len(got) != 1 || got[0] != "container-1" {
		t.Fatalf("expected the unhealthy container-1 to be stopped, got %v", got)
	}
	if len(runtime.startedSpecs()) != 1 {
		t.Fatalf("expected one replacement instance to be started")
	}

	st, err := states.Get(context.Background(), "dep-1", "api")
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if st.ContainerID == nil || *st.ContainerID == "container-1" {
		t.Fatalf("expected the state to now point at a NEW container, got %v", st.ContainerID)
	}

	calls := notify.all()
	if len(calls) != 1 {
		t.Fatalf("expected exactly one owner notification, got %d", len(calls))
	}
	if calls[0].Category != domain.NotificationHealthRemediation {
		t.Errorf("got category %q, want %q", calls[0].Category, domain.NotificationHealthRemediation)
	}
	if calls[0].ApplicationID != "app-1" {
		t.Errorf("notified about application %q, want app-1", calls[0].ApplicationID)
	}

	// A third sweep against the now-healthy replacement must be a no-op.
	if _, remediated, _ := svc.Sweep(context.Background()); remediated != 0 {
		t.Error("the replacement instance is healthy — must not be touched again")
	}
}

func TestHealthSweep_ReplacementAlsoUnhealthy_LeavesServiceDownAndEscalates(t *testing.T) {
	svc, states, runtime, notify := newHealthMonitorService()
	seedActiveState(states, "container-1", 9001)
	runtime.fail(9001)
	runtime.fail(30001) // the replacement StartContainer will assign this port next

	svc.Sweep(context.Background())
	_, remediated, err := svc.Sweep(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if remediated != 0 {
		t.Fatalf("a replacement that also fails its health check must not count as a successful remediation, got %d", remediated)
	}

	st, err := states.Get(context.Background(), "dep-1", "api")
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if st.ContainerID != nil {
		t.Fatalf("FR-085 exception flow: a service whose replacement also failed must be left with no running instance, got %v", st.ContainerID)
	}

	if got := runtime.stoppedContainers(); len(got) != 2 {
		t.Fatalf("expected both the original and the failed replacement to be stopped, got %v", got)
	}

	calls := notify.all()
	if len(calls) != 1 || calls[0].Category != domain.NotificationHealthRemediation {
		t.Fatalf("expected one escalation notification, got %+v", calls)
	}
}

func TestHealthSweep_CircuitBreaker_PausesAfterRepeatedRemediation(t *testing.T) {
	svc, states, runtime, notify := newHealthMonitorService()
	seedActiveState(states, "container-1", 9001)
	runtime.fail(9001)

	// Three successful remediation cycles: each time, mark the just-started
	// replacement's port as failing too, so the next sweep pair remediates
	// it again — simulating a service that keeps degrading right after
	// each restart.
	for i := 0; i < 3; i++ {
		svc.Sweep(context.Background())
		if _, remediated, err := svc.Sweep(context.Background()); err != nil || remediated != 1 {
			t.Fatalf("cycle %d: expected a successful remediation, got remediated=%d err=%v", i, remediated, err)
		}
		st, err := states.Get(context.Background(), "dep-1", "api")
		if err != nil {
			t.Fatalf("state: %v", err)
		}
		runtime.fail(*st.HostPort)
	}

	startedBefore := len(runtime.startedSpecs())
	notifiedBefore := len(notify.all())

	// A 4th sustained failure trips the circuit breaker: no further
	// replacement is started, but the broken instance IS still stopped —
	// a known-broken instance is never left silently serving traffic.
	svc.Sweep(context.Background())
	_, remediated, err := svc.Sweep(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if remediated != 0 {
		t.Fatalf("the circuit breaker should have paused remediation, got remediated=%d", remediated)
	}
	if len(runtime.startedSpecs()) != startedBefore {
		t.Error("no new replacement should be started once the circuit breaker is open")
	}

	st, err := states.Get(context.Background(), "dep-1", "api")
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if st.ContainerID != nil {
		t.Error("a paused service must be left with no running instance, not silently serving a known-broken one")
	}

	if got := len(notify.all()) - notifiedBefore; got != 1 {
		t.Fatalf("expected exactly one escalation notification for the pause, got %d", got)
	}

	// Repeated sweeps while still paused must not spam another notification.
	svc.Sweep(context.Background())
	svc.Sweep(context.Background())
	if got := len(notify.all()) - notifiedBefore; got != 1 {
		t.Errorf("expected the pause notification to be sent once, got %d total", got)
	}
}

// A container being replaced out from under a pending failure count (a
// redeploy or a manual restart racing the sweeper) must not carry that
// count over to the new, unrelated instance and wrongly remediate it.
func TestHealthSweep_InstanceReplacedMidCount_DoesNotCarryFailureOver(t *testing.T) {
	svc, states, runtime, notify := newHealthMonitorService()
	seedActiveState(states, "container-1", 9001)
	runtime.fail(9001)
	if _, remediated, _ := svc.Sweep(context.Background()); remediated != 0 {
		t.Fatalf("first miss must not remediate")
	}

	// Something else (a redeploy, a manual restart) replaces the container
	// before a second consecutive failure would have triggered remediation.
	_ = states.SetContainer(context.Background(), "dep-1", "api", "container-2", 30099)

	if _, remediated, err := svc.Sweep(context.Background()); err != nil || remediated != 0 {
		t.Fatalf("the new, healthy instance must not be remediated on the old instance's failure count, got remediated=%d err=%v", remediated, err)
	}
	if len(runtime.stoppedContainers()) != 0 {
		t.Error("must never stop a container it didn't itself observe as unhealthy")
	}
	if len(notify.all()) != 0 {
		t.Error("no notification when nothing was actually remediated")
	}
}
