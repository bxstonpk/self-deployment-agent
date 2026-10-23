package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"platform-api/internal/domain"
	"platform-api/internal/hostinfo"
	"platform-api/internal/service"
)

type fakeAdminRepo struct {
	apps         []domain.AdminApplication
	usage        map[string]domain.AdminUsage
	traffic      map[string]domain.AdminTraffic
	byMinute     []domain.TrafficPoint
	usageSince   time.Time
	trafficSince time.Time
}

func (f *fakeAdminRepo) ApplicationsOverview(ctx context.Context) ([]domain.AdminApplication, error) {
	return append([]domain.AdminApplication(nil), f.apps...), nil
}
func (f *fakeAdminRepo) CurrentUsage(ctx context.Context, since time.Time) (map[string]domain.AdminUsage, error) {
	f.usageSince = since
	return f.usage, nil
}
func (f *fakeAdminRepo) TrafficSince(ctx context.Context, since time.Time) (map[string]domain.AdminTraffic, error) {
	f.trafficSince = since
	return f.traffic, nil
}
func (f *fakeAdminRepo) TrafficByMinute(ctx context.Context, since time.Time) ([]domain.TrafficPoint, error) {
	return f.byMinute, nil
}

type fakeHost struct{}

func (fakeHost) Status() hostinfo.Status {
	return hostinfo.Status{Available: true, CPUCount: 4, CPUPercent: 37.5, MemoryUsed: 1 << 30, MemoryTotal: 4 << 30}
}
func (fakeHost) History() []hostinfo.Point { return []hostinfo.Point{{CPUPercent: 37.5}} }

func newAdminOverview(repo *fakeAdminRepo) *service.AdminOverviewService {
	admins := service.NewEmailAdminDirectory(nil, []string{"Prasit.K@sti-th.com"})
	return service.NewAdminOverviewService(repo, fakeHost{}, admins,
		func() (float64, error) { return 42, nil }, 15*time.Second)
}

func TestAdminOverview_OnlyForAdministrators(t *testing.T) {
	svc := newAdminOverview(&fakeAdminRepo{})
	_, err := svc.Overview(context.Background(), domain.User{Email: "someone@sti-th.com"})
	if !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("a non-administrator must be refused, got %v", err)
	}
	// The address's case doesn't make someone a different person.
	if _, err := svc.Overview(context.Background(), domain.User{Email: "prasit.k@STI-TH.com"}); err != nil {
		t.Fatalf("an administrator must get the dashboard, got %v", err)
	}
}

func TestAdminOverview_CombinesUsageAndTrafficPerApplication(t *testing.T) {
	repo := &fakeAdminRepo{
		apps: []domain.AdminApplication{
			{ID: "a1", Name: "leave", Status: "running", Instances: 2},
			{ID: "a2", Name: "idle", Status: "running"},
		},
		usage:    map[string]domain.AdminUsage{"a1": {CPUPercent: 12.5, MemoryBytes: 300 << 20, Containers: 2}},
		traffic:  map[string]domain.AdminTraffic{"a1": {Requests: 40, Errors: 2, LatencyMsTotal: 800}},
		byMinute: []domain.TrafficPoint{{Requests: 10}, {Requests: 30, Errors: 2}},
	}
	o, err := newAdminOverview(repo).Overview(context.Background(), domain.User{Email: "prasit.k@sti-th.com"})
	if err != nil {
		t.Fatal(err)
	}
	leave, idle := o.Applications[0], o.Applications[1]
	if leave.CPUPercent != 12.5 || leave.MemoryBytes != 300<<20 || leave.Requests != 40 || leave.Errors != 2 || leave.LatencyMsAvg != 20 {
		t.Errorf("leave: %+v", leave)
	}
	if idle.CPUPercent != 0 || idle.Requests != 0 || idle.LatencyMsAvg != 0 {
		t.Errorf("an application with no readings shows zeros, got %+v", idle)
	}
	if o.DiskUsedPercent == nil || *o.DiskUsedPercent != 42 {
		t.Errorf("disk: %v", o.DiskUsedPercent)
	}
	if !o.Host.Available || o.Host.CPUCount != 4 || len(o.HostHistory) != 1 || len(o.Traffic) != 2 {
		t.Errorf("host/traffic not passed through: %+v %+v %+v", o.Host, o.HostHistory, o.Traffic)
	}
	// Usage looks back only far enough for the latest reading of each
	// container (2 sampler intervals and then some), traffic over its window.
	if age := o.GeneratedAt.Sub(repo.usageSince); age < 30*time.Second || age > time.Minute {
		t.Errorf("usage window %s", age)
	}
	if age := o.GeneratedAt.Sub(repo.trafficSince); age != o.TrafficWindow {
		t.Errorf("traffic window %s, want %s", age, o.TrafficWindow)
	}
}
