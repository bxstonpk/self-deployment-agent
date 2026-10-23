package service

import (
	"context"
	"log"
	"strings"
	"time"

	"platform-api/internal/domain"
	"platform-api/internal/hostinfo"
)

// The administrators' dashboard: the host's CPU, memory and disk, traffic
// across every application, and every application with what it's using —
// a task manager for the platform. Only for PLATFORM_ADMIN_EMAILS: owners
// already see their own applications' metrics, and nobody else's.

const (
	// adminTrafficWindow is what the per-application request, error and
	// latency columns cover.
	adminTrafficWindow = 5 * time.Minute
	// adminTrafficHistory is how far back the traffic chart goes; the host
	// charts go back as far as the sampler keeps.
	adminTrafficHistory = 15 * time.Minute
)

type AdminOverviewRepository interface {
	ApplicationsOverview(ctx context.Context) ([]domain.AdminApplication, error)
	CurrentUsage(ctx context.Context, since time.Time) (map[string]domain.AdminUsage, error)
	TrafficSince(ctx context.Context, since time.Time) (map[string]domain.AdminTraffic, error)
	TrafficByMinute(ctx context.Context, since time.Time) ([]domain.TrafficPoint, error)
}

// HostSource is the host's own readings (hostinfo.Sampler).
type HostSource interface {
	Status() hostinfo.Status
	History() []hostinfo.Point
}

// AdminChecker says whether an account is a platform administrator.
type AdminChecker interface {
	IsAdmin(email string) bool
}

// IsAdmin reports whether email is one of PLATFORM_ADMIN_EMAILS. Case
// doesn't matter: an email address's domain never does, and nobody types
// their own address in two cases on purpose.
func (d *EmailAdminDirectory) IsAdmin(email string) bool {
	for _, e := range d.emails {
		if strings.EqualFold(strings.TrimSpace(e), strings.TrimSpace(email)) {
			return true
		}
	}
	return false
}

type AdminOverview struct {
	GeneratedAt     time.Time
	Host            hostinfo.Status
	HostHistory     []hostinfo.Point
	DiskUsedPercent *float64 // the disk the platform keeps its data on; nil when unknown
	Traffic         []domain.TrafficPoint
	TrafficWindow   time.Duration
	Applications    []domain.AdminApplication
}

type AdminOverviewService struct {
	repo           AdminOverviewRepository
	host           HostSource
	admins         AdminChecker
	diskUsed       func() (float64, error)
	sampleInterval time.Duration
	now            func() time.Time
}

func NewAdminOverviewService(repo AdminOverviewRepository, host HostSource, admins AdminChecker,
	diskUsed func() (float64, error), sampleInterval time.Duration) *AdminOverviewService {
	return &AdminOverviewService{repo: repo, host: host, admins: admins, diskUsed: diskUsed,
		sampleInterval: sampleInterval, now: time.Now}
}

// Overview is the dashboard for requester, who must be an administrator.
func (s *AdminOverviewService) Overview(ctx context.Context, requester domain.User) (AdminOverview, error) {
	if !s.admins.IsAdmin(requester.Email) {
		return AdminOverview{}, domain.ErrUnauthorized
	}
	now := s.now().UTC()

	apps, err := s.repo.ApplicationsOverview(ctx)
	if err != nil {
		return AdminOverview{}, err
	}
	// Two of the metrics sampler's intervals and then some: the latest
	// reading of every container serving now, and nothing older.
	usage, err := s.repo.CurrentUsage(ctx, now.Add(-(2*s.sampleInterval + 15*time.Second)))
	if err != nil {
		return AdminOverview{}, err
	}
	traffic, err := s.repo.TrafficSince(ctx, now.Add(-adminTrafficWindow))
	if err != nil {
		return AdminOverview{}, err
	}
	for i := range apps {
		if u, ok := usage[apps[i].ID]; ok {
			apps[i].CPUPercent, apps[i].MemoryBytes = u.CPUPercent, u.MemoryBytes
		}
		if t, ok := traffic[apps[i].ID]; ok {
			apps[i].Requests, apps[i].Errors = t.Requests, t.Errors
			if t.Requests > 0 {
				apps[i].LatencyMsAvg = t.LatencyMsTotal / float64(t.Requests)
			}
		}
	}
	byMinute, err := s.repo.TrafficByMinute(ctx, now.Add(-adminTrafficHistory))
	if err != nil {
		return AdminOverview{}, err
	}

	out := AdminOverview{
		GeneratedAt: now, Host: s.host.Status(), HostHistory: s.host.History(),
		Traffic: byMinute, TrafficWindow: adminTrafficWindow, Applications: apps,
	}
	if s.diskUsed != nil {
		if pct, err := s.diskUsed(); err == nil {
			out.DiskUsedPercent = &pct
		} else {
			log.Printf("admin dashboard: disk usage unavailable: %v", err)
		}
	}
	return out, nil
}
