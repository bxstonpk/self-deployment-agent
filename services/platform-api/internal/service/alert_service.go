// Alerts implements FR-092 with DEC-028 — see internal/domain/alert.go.
package service

import (
	"context"
	"fmt"
	"log"
	"time"

	"platform-api/internal/domain"
)

type AlertRepository interface {
	TrafficSince(ctx context.Context, t time.Time) ([]domain.TrafficWindowStats, error)
	ResourcesSince(ctx context.Context, t time.Time) ([]domain.ResourceWindowStats, error)
	States(ctx context.Context) ([]domain.AlertState, error)
	StatesForApplication(ctx context.Context, applicationID string) ([]domain.AlertState, error)
	PutState(ctx context.Context, s domain.AlertState) error
	DeleteState(ctx context.Context, key string) error
	AddEvent(ctx context.Context, e domain.AlertEvent) error
	EventsForApplication(ctx context.Context, applicationID string, since time.Time) ([]domain.AlertEvent, error)
}

type AlertNotifier interface {
	NotifyOwners(ctx context.Context, applicationID string, category domain.NotificationCategory, title, detail, resourceType, resourceID string)
	NotifyUser(ctx context.Context, recipientUserID string, category domain.NotificationCategory, title, detail, resourceType, resourceID string)
}

// AdminDirectory resolves who receives platform-level alerts: the
// platform administrators configured in PLATFORM_ADMIN_EMAILS.
type AdminDirectory interface {
	AdminUserIDs(ctx context.Context) []string
}

type AlertService struct {
	repo       AlertRepository
	apps       ApplicationLifecycleRepository
	owners     ApplicationOwnerRepository
	notifier   AlertNotifier
	admins     AdminDirectory
	thresholds domain.AlertThresholds
	// diskUsed measures the host's disk; nil skips the disk rule.
	diskUsed func() (float64, error)
	now      func() time.Time
}

func NewAlertService(repo AlertRepository, apps ApplicationLifecycleRepository, owners ApplicationOwnerRepository,
	notifier AlertNotifier, admins AdminDirectory, thresholds domain.AlertThresholds, diskUsed func() (float64, error)) *AlertService {
	return &AlertService{repo: repo, apps: apps, owners: owners, notifier: notifier, admins: admins,
		thresholds: thresholds, diskUsed: diskUsed, now: time.Now}
}

func (s *AlertService) Thresholds() domain.AlertThresholds { return s.thresholds }

// observation is what one evaluation found for one key.
type observation struct {
	state   domain.AlertState // Level "" means fine
	message string
}

func level(value, warning, critical float64) domain.AlertLevel {
	switch {
	case value >= critical:
		return domain.AlertCritical
	case value >= warning:
		return domain.AlertWarning
	}
	return ""
}

func key(applicationID, service string, rule domain.AlertRule) string {
	if applicationID == "" {
		return "platform:" + string(rule)
	}
	return "app:" + applicationID + ":" + service + ":" + string(rule)
}

// observe applies every rule to the current measurements.
func (s *AlertService) observe(ctx context.Context) (map[string]observation, error) {
	t := s.thresholds
	now := s.now()
	out := map[string]observation{}
	add := func(appID, service string, rule domain.AlertRule, lvl domain.AlertLevel, value float64, msg string) {
		k := key(appID, service, rule)
		out[k] = observation{state: domain.AlertState{Key: k, ApplicationID: appID, Service: service, Rule: rule,
			Level: lvl, Value: value, Message: msg}}
	}

	traffic, err := s.repo.TrafficSince(ctx, now.Add(-t.TrafficWindow))
	if err != nil {
		return nil, err
	}
	window := t.TrafficWindow.Round(time.Minute)
	for _, tr := range traffic {
		if tr.Requests >= t.MinRequestsForRate {
			rate := float64(tr.Errors) / float64(tr.Requests)
			add(tr.ApplicationID, tr.Service, domain.AlertErrorRate, level(rate, t.ErrorRateWarning, t.ErrorRateCritical), rate,
				fmt.Sprintf("%.1f%% of requests to %s failed with a 5xx over the last %s (%d of %d)",
					100*rate, tr.Service, window, tr.Errors, tr.Requests))
		}
		if tr.Requests >= t.MinRequestsLatency {
			mean := tr.LatencyMsSum / float64(tr.Requests)
			add(tr.ApplicationID, tr.Service, domain.AlertLatency, level(mean, t.LatencyWarningMs, t.LatencyCriticalMs), mean,
				fmt.Sprintf("%s answered in %.0f ms on average over the last %s", tr.Service, mean, window))
		}
	}

	cpu, err := s.repo.ResourcesSince(ctx, now.Add(-t.CPUWindow))
	if err != nil {
		return nil, err
	}
	for _, r := range cpu {
		// Sustained means readings across most of the window, every one of
		// them over the line.
		if r.Samples >= 2 && r.Span >= t.CPUWindow*3/4 {
			add(r.ApplicationID, r.Service, domain.AlertCPU, level(r.MinCPUPercent, t.CPUWarning, t.CPUCritical), r.MinCPUPercent,
				fmt.Sprintf("%s has used at least %.0f%% CPU for the last %s", r.Service, r.MinCPUPercent, t.CPUWindow.Round(time.Minute)))
		}
	}
	mem, err := s.repo.ResourcesSince(ctx, now.Add(-t.MemoryWindow))
	if err != nil {
		return nil, err
	}
	for _, r := range mem {
		if r.Samples >= 1 {
			add(r.ApplicationID, r.Service, domain.AlertMemory, level(r.AvgMemoryPercent, t.MemoryWarning, t.MemoryCritical), r.AvgMemoryPercent,
				fmt.Sprintf("%s used %.0f%% of its memory limit on average over the last %s", r.Service, r.AvgMemoryPercent, t.MemoryWindow.Round(time.Minute)))
		}
	}

	if s.diskUsed != nil {
		if used, err := s.diskUsed(); err == nil {
			add("", "", domain.AlertDisk, level(used, t.DiskWarning, t.DiskCritical), used,
				fmt.Sprintf("the platform host's data disk is %.0f%% full (databases, backups and logs live there)", used))
		}
	}
	return out, nil
}

func (s *AlertService) title(st domain.AlertState, kind domain.AlertEventKind) string {
	subject := "platform"
	if st.ApplicationID != "" {
		if app, err := s.apps.GetByID(context.Background(), st.ApplicationID); err == nil {
			subject = app.Name
		}
	}
	switch kind {
	case domain.AlertResolved:
		return fmt.Sprintf("Resolved: %s %s", subject, st.Rule)
	case domain.AlertReminded:
		return fmt.Sprintf("Still %s: %s %s", st.Level, subject, st.Rule)
	case domain.AlertEscalated:
		return fmt.Sprintf("Now critical: %s %s", subject, st.Rule)
	}
	return fmt.Sprintf("%s: %s %s", map[domain.AlertLevel]string{domain.AlertWarning: "Warning", domain.AlertCritical: "Critical"}[st.Level], subject, st.Rule)
}

func (s *AlertService) announce(ctx context.Context, st domain.AlertState, kind domain.AlertEventKind, detail string) {
	at := s.now()
	ev := domain.AlertEvent{Key: st.Key, ApplicationID: st.ApplicationID, Service: st.Service, Rule: st.Rule,
		Kind: kind, Value: st.Value, Message: detail, At: at}
	if kind != domain.AlertResolved {
		ev.Level = st.Level
	}
	if err := s.repo.AddEvent(ctx, ev); err != nil {
		log.Printf("alerts: %v", err)
	}
	title := s.title(st, kind)
	if st.ApplicationID != "" {
		s.notifier.NotifyOwners(ctx, st.ApplicationID, domain.NotificationAlert, title, detail, "application", st.ApplicationID)
		return
	}
	if s.admins == nil {
		return
	}
	for _, id := range s.admins.AdminUserIDs(ctx) {
		s.notifier.NotifyUser(ctx, id, domain.NotificationAlert, title, detail, "platform", string(st.Rule))
	}
}

// Evaluate judges every rule once and announces what changed, with
// DEC-028's rhythm: once when an alert starts, once if it escalates to
// critical, a reminder at most every ReminderInterval while it lasts,
// and once when it clears. Returns how many notifications went out.
func (s *AlertService) Evaluate(ctx context.Context) (int, error) {
	observed, err := s.observe(ctx)
	if err != nil {
		return 0, err
	}
	current, err := s.repo.States(ctx)
	if err != nil {
		return 0, err
	}
	now := s.now()
	sent := 0
	firing := map[string]domain.AlertState{}
	for _, st := range current {
		firing[st.Key] = st
	}

	for k, obs := range observed {
		st, wasFiring := firing[k]
		delete(firing, k)
		next := obs.state
		switch {
		case next.Level == "" && wasFiring:
			st.Value = next.Value
			s.announce(ctx, st, domain.AlertResolved, next.Message)
			_ = s.repo.DeleteState(ctx, k)
			sent++
		case next.Level == "":
		case !wasFiring:
			next.StartedAt, next.LastNotifiedAt = now, now
			if err := s.repo.PutState(ctx, next); err != nil {
				log.Printf("alerts: %v", err)
				continue
			}
			s.announce(ctx, next, domain.AlertFired, next.Message)
			sent++
		case next.Level.Rank() > st.Level.Rank():
			next.StartedAt, next.LastNotifiedAt = st.StartedAt, now
			_ = s.repo.PutState(ctx, next)
			s.announce(ctx, next, domain.AlertEscalated, next.Message)
			sent++
		case now.Sub(st.LastNotifiedAt) >= s.thresholds.ReminderInterval:
			next.StartedAt, next.LastNotifiedAt = st.StartedAt, now
			_ = s.repo.PutState(ctx, next)
			s.announce(ctx, next, domain.AlertReminded,
				fmt.Sprintf("%s (since %s)", next.Message, st.StartedAt.Format("15:04 02 Jan")))
			sent++
		default:
			// Still firing, already told: keep the latest value and level,
			// say nothing. A drop from critical to warning isn't news.
			next.StartedAt, next.LastNotifiedAt = st.StartedAt, st.LastNotifiedAt
			_ = s.repo.PutState(ctx, next)
		}
	}

	// Firing alerts with nothing to judge any more — the service stopped
	// getting traffic, scaled to zero, or its application stopped running
	// — have cleared.
	for k, st := range firing {
		s.announce(ctx, st, domain.AlertResolved, fmt.Sprintf("%s: no longer measured as a problem", st.Rule))
		_ = s.repo.DeleteState(ctx, k)
		sent++
	}
	return sent, nil
}

// ForApplication is what an owner sees: what's firing and the recent
// history.
func (s *AlertService) ForApplication(ctx context.Context, applicationID, requesterID string) ([]domain.AlertState, []domain.AlertEvent, error) {
	if _, err := s.apps.GetByID(ctx, applicationID); err != nil {
		return nil, nil, err
	}
	owners, err := s.owners.ListForApplication(ctx, applicationID)
	if err != nil {
		return nil, nil, err
	}
	allowed := false
	for _, o := range owners {
		if o.UserID == requesterID && o.Status == "active" {
			allowed = true
		}
	}
	if !allowed {
		return nil, nil, domain.ErrUnauthorized
	}
	active, err := s.repo.StatesForApplication(ctx, applicationID)
	if err != nil {
		return nil, nil, err
	}
	events, err := s.repo.EventsForApplication(ctx, applicationID, s.now().Add(-7*24*time.Hour))
	if err != nil {
		return nil, nil, err
	}
	return active, events, nil
}
