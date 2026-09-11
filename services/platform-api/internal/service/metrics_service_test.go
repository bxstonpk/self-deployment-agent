package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"platform-api/internal/domain"
	"platform-api/internal/service"
)

type fakeMetricStore struct {
	samples    []domain.ResourceSample
	buckets    []domain.RequestBucket
	lastSample *time.Time
	bucketErr  error
	lastQuery  domain.MetricsQuery
}

func (f *fakeMetricStore) AppendResourceSamples(ctx context.Context, s []domain.ResourceSample) error {
	f.samples = append(f.samples, s...)
	return nil
}

func (f *fakeMetricStore) AddRequestBuckets(ctx context.Context, b []domain.RequestBucket) error {
	if f.bucketErr != nil {
		return f.bucketErr
	}
	f.buckets = append(f.buckets, b...)
	return nil
}

func (f *fakeMetricStore) QueryResource(ctx context.Context, q domain.MetricsQuery) ([]domain.ResourceSample, error) {
	f.lastQuery = q
	return f.samples, nil
}

func (f *fakeMetricStore) QueryTraffic(ctx context.Context, q domain.MetricsQuery) ([]domain.RequestBucket, error) {
	return f.buckets, nil
}

func (f *fakeMetricStore) LastResourceSampleAt(ctx context.Context, applicationID string) (time.Time, bool, error) {
	if f.lastSample == nil {
		return time.Time{}, false, nil
	}
	return *f.lastSample, true, nil
}

type fakeUsageSampler struct {
	usage []domain.ContainerUsage
	err   error
}

func (f *fakeUsageSampler) SampleUsage(ctx context.Context) ([]domain.ContainerUsage, error) {
	return f.usage, f.err
}

type fakeCurrentDeployment struct {
	deployment domain.Deployment
	err        error
}

func (f *fakeCurrentDeployment) CurrentRunning(ctx context.Context, applicationID string) (domain.Deployment, error) {
	return f.deployment, f.err
}

type fakeMetricStates struct{ states []domain.ServiceRuntimeState }

func (f *fakeMetricStates) ListForDeployment(ctx context.Context, deploymentID string) ([]domain.ServiceRuntimeState, error) {
	return f.states, nil
}

type fakeMetricEvents struct{ events []domain.ScaleEvent }

func (f *fakeMetricEvents) ListForApplication(ctx context.Context, deploymentIDs []string, limit int) ([]domain.ScaleEvent, error) {
	return f.events, nil
}

type metricsFixture struct {
	svc     *service.MetricsService
	store   *fakeMetricStore
	sampler *fakeUsageSampler
	traffic *service.TrafficRecorder
	states  *fakeMetricStates
}

func newMetricsFixture(running bool) metricsFixture {
	apps := newFakeLifecycleRepo(
		domain.Application{ID: "app-1", Name: "overtime", LifecycleStatus: domain.StatusRunning},
		domain.Application{ID: "app-2", Name: "leave-tracker", LifecycleStatus: domain.StatusRunning},
	)
	owners := newFakeOwnerRepo()
	owners.owners["app-1"] = []domain.ApplicationOwner{{ApplicationID: "app-1", UserID: "owner-1", OwnershipRole: domain.OwnerRolePrimary, Status: "active"}}
	owners.owners["app-2"] = []domain.ApplicationOwner{{ApplicationID: "app-2", UserID: "owner-2", OwnershipRole: domain.OwnerRolePrimary, Status: "active"}}

	container := "c-api"
	state := domain.ServiceRuntimeState{DeploymentID: "dep-1", ServiceName: "api", Eligible: true}
	if running {
		state.ContainerID = &container
	}
	store := &fakeMetricStore{}
	sampler := &fakeUsageSampler{}
	traffic := service.NewTrafficRecorder()
	states := &fakeMetricStates{states: []domain.ServiceRuntimeState{state}}
	svc := service.NewMetricsService(apps, owners, store, sampler, traffic,
		&fakeCurrentDeployment{deployment: domain.Deployment{ID: "dep-1", ApplicationID: "app-1", Environment: domain.EnvironmentDev}},
		states, &fakeMetricEvents{}, 15*time.Second)
	return metricsFixture{svc: svc, store: store, sampler: sampler, traffic: traffic, states: states}
}

func TestTraffic_CountsRequestsErrorsAndLatency(t *testing.T) {
	rec := service.NewTrafficRecorder()
	rec.Record("app-1", "api", domain.EnvironmentDev, 200, 10*time.Millisecond)
	rec.Record("app-1", "api", domain.EnvironmentDev, 500, 20*time.Millisecond)
	// A 404 is the caller asking for something that isn't there, not the
	// application failing: counted as a request, not as an error.
	rec.Record("app-1", "api", domain.EnvironmentDev, 404, 5*time.Millisecond)

	buckets := rec.Drain()
	if len(buckets) != 1 {
		t.Fatalf("expected one bucket, got %+v", buckets)
	}
	b := buckets[0]
	if b.Requests != 3 || b.Errors != 1 || b.LatencyMsMax != 20 || b.LatencyMsTotal != 35 {
		t.Fatalf("unexpected bucket %+v", b)
	}
	if len(rec.Drain()) != 0 {
		t.Fatal("draining twice returned the same counts twice")
	}
}

// A failed flush must neither lose the counts nor double them against what
// arrived while the store was unreachable.
func TestTraffic_PutBackAddsToWhatArrivedSince(t *testing.T) {
	rec := service.NewTrafficRecorder()
	rec.Record("app-1", "api", domain.EnvironmentDev, 200, 10*time.Millisecond)
	drained := rec.Drain()
	rec.Record("app-1", "api", domain.EnvironmentDev, 500, 30*time.Millisecond)

	rec.PutBack(drained)

	buckets := rec.Drain()
	if len(buckets) != 1 || buckets[0].Requests != 2 || buckets[0].Errors != 1 || buckets[0].LatencyMsMax != 30 {
		t.Fatalf("unexpected merge %+v", buckets)
	}
}

func TestMetrics_CollectStoresOneSamplePerTaggedContainer(t *testing.T) {
	f := newMetricsFixture(true)
	f.sampler.usage = []domain.ContainerUsage{
		{ContainerID: "c-api", Source: domain.LogSource{ApplicationID: "app-1", DeploymentID: "dep-1", Service: "api"}, CPUPercent: 12.5, MemoryBytes: 1024, MemoryLimit: 4096},
		{ContainerID: "c-unknown"}, // not an application container: no tags, nothing to attribute it to
	}

	n, err := f.svc.Collect(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 1 || len(f.store.samples) != 1 {
		t.Fatalf("expected one stored sample, got %d: %+v", n, f.store.samples)
	}
	s := f.store.samples[0]
	if s.ApplicationID != "app-1" || s.Service != "api" || s.CPUPercent != 12.5 || s.SampledAt.IsZero() {
		t.Fatalf("sample lost its attribution: %+v", s)
	}
}

// FR-086's sibling for metrics: a store that's briefly unavailable must
// not cost the counts already taken.
func TestMetrics_TrafficIsKeptWhenTheStoreRefusesIt(t *testing.T) {
	f := newMetricsFixture(true)
	f.store.bucketErr = errors.New("store unreachable")
	f.traffic.Record("app-1", "api", domain.EnvironmentDev, 200, 5*time.Millisecond)

	if _, err := f.svc.Collect(context.Background()); err != nil {
		t.Fatalf("a traffic flush failure should not fail the whole sweep: %v", err)
	}
	f.store.bucketErr = nil
	if _, err := f.svc.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.store.buckets) != 1 || f.store.buckets[0].Requests != 1 {
		t.Fatalf("the counts were lost: %+v", f.store.buckets)
	}
}

// FR-091's business rule: metrics access follows log access exactly.
func TestMetrics_SomeoneElsesApplicationLooksNonexistent(t *testing.T) {
	f := newMetricsFixture(true)

	_, refused := f.svc.Query(context.Background(), "owner-2", domain.MetricsQuery{ApplicationID: "app-1"})
	if !errors.Is(refused, domain.ErrApplicationNotFound) {
		t.Fatalf("expected ErrApplicationNotFound, got %v", refused)
	}
}

func TestMetrics_WindowDefaultsToTheLastHourAndIsBounded(t *testing.T) {
	f := newMetricsFixture(true)
	ctx := context.Background()

	if _, err := f.svc.Query(ctx, "owner-1", domain.MetricsQuery{ApplicationID: "app-1"}); err != nil {
		t.Fatal(err)
	}
	if window := f.store.lastQuery.To.Sub(f.store.lastQuery.From); window != domain.DefaultMetricsWindow {
		t.Fatalf("expected a one-hour default window, got %s", window)
	}

	to := time.Now().UTC()
	if _, err := f.svc.Query(ctx, "owner-1", domain.MetricsQuery{ApplicationID: "app-1", From: to.Add(-365 * 24 * time.Hour), To: to}); err != nil {
		t.Fatal(err)
	}
	if window := f.store.lastQuery.To.Sub(f.store.lastQuery.From); window != domain.MaxMetricsWindow {
		t.Fatalf("expected the window capped at %s, got %s", domain.MaxMetricsWindow, window)
	}

	_, err := f.svc.Query(ctx, "owner-1", domain.MetricsQuery{ApplicationID: "app-1", From: to, To: to.Add(-time.Hour)})
	if !errors.Is(err, domain.ErrInvalidMetricsWindow) {
		t.Fatalf("expected a backwards window to be rejected, got %v", err)
	}
}

// docs/13_API_Requirements.md §5.5: an empty answer must say whether it
// means "quiet" or "not being watched".
func TestMetrics_SaysWhyThereIsNothingToShow(t *testing.T) {
	ctx := context.Background()

	idle := newMetricsFixture(false)
	snapshot, err := idle.svc.Query(ctx, "owner-1", domain.MetricsQuery{ApplicationID: "app-1"})
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Collecting || snapshot.Note == "" {
		t.Fatalf("a scaled-to-zero application should report collection as healthy, with a reason: %+v", snapshot)
	}
	if len(snapshot.Instances) != 1 || snapshot.Instances[0].Instances != 0 || !snapshot.Instances[0].ScaledToZero {
		t.Fatalf("expected one service, scaled to zero: %+v", snapshot.Instances)
	}

	behind := newMetricsFixture(true)
	stale := time.Now().UTC().Add(-10 * time.Minute)
	behind.store.lastSample = &stale
	snapshot, err = behind.svc.Query(ctx, "owner-1", domain.MetricsQuery{ApplicationID: "app-1"})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Collecting {
		t.Fatalf("a 10-minute-old reading on a running container is not healthy collection: %+v", snapshot)
	}

	fresh := newMetricsFixture(true)
	now := time.Now().UTC()
	fresh.store.lastSample = &now
	snapshot, err = fresh.svc.Query(ctx, "owner-1", domain.MetricsQuery{ApplicationID: "app-1"})
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Collecting || snapshot.Note != "" {
		t.Fatalf("a fresh reading should report healthy collection with nothing to explain: %+v", snapshot)
	}
}
