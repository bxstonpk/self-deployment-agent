// Metrics implements Module T: the sweep that collects (FR-090) and the
// query that serves (FR-091). See internal/domain/metrics.go for what is
// collected, what isn't, and why.
package service

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"platform-api/internal/domain"
)

type MetricsStore interface {
	AppendResourceSamples(ctx context.Context, samples []domain.ResourceSample) error
	AddRequestBuckets(ctx context.Context, buckets []domain.RequestBucket) error
	QueryResource(ctx context.Context, q domain.MetricsQuery) ([]domain.ResourceSample, error)
	QueryTraffic(ctx context.Context, q domain.MetricsQuery) ([]domain.RequestBucket, error)
	LastResourceSampleAt(ctx context.Context, applicationID string) (time.Time, bool, error)
}

type UsageSampler interface {
	SampleUsage(ctx context.Context) ([]domain.ContainerUsage, error)
}

type MetricsDeploymentLookup interface {
	CurrentRunning(ctx context.Context, applicationID string) (domain.Deployment, error)
}

type MetricsStateLister interface {
	ListForDeployment(ctx context.Context, deploymentID string) ([]domain.ServiceRuntimeState, error)
}

type MetricsScaleEventLister interface {
	ListForApplication(ctx context.Context, deploymentIDs []string, limit int) ([]domain.ScaleEvent, error)
}

type trafficKey struct {
	applicationID string
	service       string
	environment   string
	minute        time.Time
}

// TrafficRecorder accumulates what the proxy sees, in memory, and hands it
// over a minute at a time. A request is cheap to record and expensive to
// store: one row per service per minute, not one per request.
type TrafficRecorder struct {
	mu      sync.Mutex
	buckets map[trafficKey]*domain.RequestBucket
}

func NewTrafficRecorder() *TrafficRecorder {
	return &TrafficRecorder{buckets: map[trafficKey]*domain.RequestBucket{}}
}

// Record is called once per proxied request, on the request's own
// goroutine, so it does no I/O.
func (t *TrafficRecorder) Record(applicationID, service string, environment domain.Environment, status int, latency time.Duration) {
	if applicationID == "" {
		return
	}
	key := trafficKey{applicationID, service, string(environment), time.Now().UTC().Truncate(time.Minute)}
	ms := float64(latency.Microseconds()) / 1000

	t.mu.Lock()
	defer t.mu.Unlock()
	b := t.buckets[key]
	if b == nil {
		b = &domain.RequestBucket{ApplicationID: applicationID, Service: service, Environment: environment, Minute: key.minute}
		t.buckets[key] = b
	}
	b.Requests++
	if domain.IsRequestError(status) {
		b.Errors++
	}
	b.LatencyMsTotal += ms
	if ms > b.LatencyMsMax {
		b.LatencyMsMax = ms
	}
}

func (t *TrafficRecorder) Drain() []domain.RequestBucket {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]domain.RequestBucket, 0, len(t.buckets))
	for _, b := range t.buckets {
		out = append(out, *b)
	}
	t.buckets = map[trafficKey]*domain.RequestBucket{}
	return out
}

// PutBack returns drained buckets after a failed flush. Counts are added
// into whatever has accumulated since, so nothing is lost and nothing is
// counted twice.
func (t *TrafficRecorder) PutBack(buckets []domain.RequestBucket) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, b := range buckets {
		key := trafficKey{b.ApplicationID, b.Service, string(b.Environment), b.Minute}
		existing := t.buckets[key]
		if existing == nil {
			copied := b
			t.buckets[key] = &copied
			continue
		}
		existing.Requests += b.Requests
		existing.Errors += b.Errors
		existing.LatencyMsTotal += b.LatencyMsTotal
		if b.LatencyMsMax > existing.LatencyMsMax {
			existing.LatencyMsMax = b.LatencyMsMax
		}
	}
}

type MetricsService struct {
	apps        ApplicationGetter
	owners      ApplicationOwnerRepository
	store       MetricsStore
	sampler     UsageSampler
	traffic     *TrafficRecorder
	deployments MetricsDeploymentLookup
	states      MetricsStateLister
	events      MetricsScaleEventLister
	interval    time.Duration
}

func NewMetricsService(apps ApplicationGetter, owners ApplicationOwnerRepository, store MetricsStore,
	sampler UsageSampler, traffic *TrafficRecorder, deployments MetricsDeploymentLookup,
	states MetricsStateLister, events MetricsScaleEventLister, interval time.Duration) *MetricsService {
	return &MetricsService{apps: apps, owners: owners, store: store, sampler: sampler, traffic: traffic,
		deployments: deployments, states: states, events: events, interval: interval}
}

// Collect takes one reading of every running application container and
// stores whatever traffic the proxy has recorded since the last sweep.
// Containers whose reading failed are simply absent — FR-090's gap.
func (s *MetricsService) Collect(ctx context.Context) (int, error) {
	if buckets := s.traffic.Drain(); len(buckets) > 0 {
		if err := s.store.AddRequestBuckets(ctx, buckets); err != nil {
			s.traffic.PutBack(buckets)
			log.Printf("metrics: traffic not stored, kept for the next sweep: %v", err)
		}
	}
	usage, err := s.sampler.SampleUsage(ctx)
	if err != nil {
		return 0, fmt.Errorf("sample containers: %w", err)
	}
	now := time.Now().UTC()
	samples := make([]domain.ResourceSample, 0, len(usage))
	for _, u := range usage {
		if u.Source.ApplicationID == "" {
			continue
		}
		samples = append(samples, domain.ResourceSample{
			ApplicationID: u.Source.ApplicationID, DeploymentID: u.Source.DeploymentID, Service: u.Source.Service,
			ContainerID: u.ContainerID, SampledAt: now,
			CPUPercent: u.CPUPercent, MemoryBytes: u.MemoryBytes, MemoryLimit: u.MemoryLimit,
		})
	}
	if err := s.store.AppendResourceSamples(ctx, samples); err != nil {
		return 0, err
	}
	return len(samples), nil
}

func (s *MetricsService) isOwner(ctx context.Context, applicationID, userID string) (bool, error) {
	owners, err := s.owners.ListForApplication(ctx, applicationID)
	if err != nil {
		return false, err
	}
	for _, o := range owners {
		if o.UserID == userID && o.Status == "active" {
			return true, nil
		}
	}
	return false, nil
}

// Query implements FR-091, whose business rule is that metrics follow log
// access exactly (FR-089): owners only, and anyone else gets what they
// would for an application that doesn't exist.
//
// Unlike a log read, a metrics read is not audited: FR-089 asks for that
// about logs, which carry whatever an application printed, and nothing
// asks it of CPU and request counts. FR-093's cross-application
// administrator view would be audited — it doesn't exist yet.
func (s *MetricsService) Query(ctx context.Context, requesterID string, q domain.MetricsQuery) (domain.MetricsSnapshot, error) {
	if _, err := s.apps.GetByID(ctx, q.ApplicationID); err != nil {
		return domain.MetricsSnapshot{}, err
	}
	owner, err := s.isOwner(ctx, q.ApplicationID, requesterID)
	if err != nil {
		return domain.MetricsSnapshot{}, err
	}
	if !owner {
		return domain.MetricsSnapshot{}, domain.ErrApplicationNotFound
	}

	now := time.Now().UTC()
	if q.To.IsZero() {
		q.To = now
	}
	if q.From.IsZero() {
		q.From = q.To.Add(-domain.DefaultMetricsWindow)
	}
	if !q.From.Before(q.To) {
		return domain.MetricsSnapshot{}, domain.ErrInvalidMetricsWindow
	}
	if q.To.Sub(q.From) > domain.MaxMetricsWindow {
		q.From = q.To.Add(-domain.MaxMetricsWindow)
	}

	resource, err := s.store.QueryResource(ctx, q)
	if err != nil {
		return domain.MetricsSnapshot{}, err
	}
	traffic, err := s.store.QueryTraffic(ctx, q)
	if err != nil {
		return domain.MetricsSnapshot{}, err
	}

	snapshot := domain.MetricsSnapshot{From: q.From, To: q.To, Resource: resource, Traffic: traffic}
	snapshot.Instances, snapshot.LastScaleEvent = s.currentPicture(ctx, q.ApplicationID)
	if last, ok, err := s.store.LastResourceSampleAt(ctx, q.ApplicationID); err == nil && ok {
		snapshot.LastSampleAt = &last
	}
	snapshot.Collecting, snapshot.Note = s.collectionStatus(snapshot, now)
	return snapshot, nil
}

// currentPicture is the "instance count and last scale event" the MCP
// contract asks for (docs/07_MCP_Requirements.md §13.10). Best effort: an
// application with nothing running has neither, which is an answer rather
// than an error.
func (s *MetricsService) currentPicture(ctx context.Context, applicationID string) ([]domain.ServiceInstances, *domain.ScaleEvent) {
	deployment, err := s.deployments.CurrentRunning(ctx, applicationID)
	if err != nil {
		return nil, nil
	}
	states, err := s.states.ListForDeployment(ctx, deployment.ID)
	if err != nil {
		return nil, nil
	}
	instances := make([]domain.ServiceInstances, 0, len(states))
	for _, st := range states {
		running := st.ContainerID != nil && *st.ContainerID != ""
		count := 0
		if running {
			count = 1
		}
		instances = append(instances, domain.ServiceInstances{
			Service: st.ServiceName, Instances: count, ScaledToZero: !running && st.Eligible,
		})
	}
	var last *domain.ScaleEvent
	if events, err := s.events.ListForApplication(ctx, []string{deployment.ID}, 1); err == nil && len(events) > 0 {
		last = &events[0]
	}
	return instances, last
}

// collectionStatus is docs/13_API_Requirements.md §5.5's explicit
// "partial/unavailable" indicator: an empty window must never be mistaken
// for a quiet application.
func (s *MetricsService) collectionStatus(snapshot domain.MetricsSnapshot, now time.Time) (bool, string) {
	anyRunning := false
	for _, i := range snapshot.Instances {
		if i.Instances > 0 {
			anyRunning = true
			break
		}
	}
	if !anyRunning {
		return true, "No container is running for this application, so there is nothing to sample. Traffic is still counted whenever a request arrives."
	}
	stale := s.interval * 3
	if snapshot.LastSampleAt == nil {
		return false, "A container is running, but no resource reading has been taken yet."
	}
	if age := now.Sub(*snapshot.LastSampleAt); age > stale {
		return false, fmt.Sprintf("Resource sampling is behind: the last reading is %s old. What's shown may be incomplete.", age.Truncate(time.Second))
	}
	return true, ""
}
