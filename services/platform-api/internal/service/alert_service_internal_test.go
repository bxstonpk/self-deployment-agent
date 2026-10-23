package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"platform-api/internal/domain"
)

type memAlerts struct {
	traffic   []domain.TrafficWindowStats
	resources []domain.ResourceWindowStats
	states    map[string]domain.AlertState
	events    []domain.AlertEvent
}

func (m *memAlerts) TrafficSince(ctx context.Context, t time.Time) ([]domain.TrafficWindowStats, error) {
	return m.traffic, nil
}
func (m *memAlerts) ResourcesSince(ctx context.Context, t time.Time) ([]domain.ResourceWindowStats, error) {
	return m.resources, nil
}
func (m *memAlerts) States(ctx context.Context) ([]domain.AlertState, error) {
	out := []domain.AlertState{}
	for _, s := range m.states {
		out = append(out, s)
	}
	return out, nil
}
func (m *memAlerts) StatesForApplication(ctx context.Context, appID string) ([]domain.AlertState, error) {
	return m.States(ctx)
}
func (m *memAlerts) PutState(ctx context.Context, s domain.AlertState) error {
	m.states[s.Key] = s
	return nil
}
func (m *memAlerts) DeleteState(ctx context.Context, key string) error {
	delete(m.states, key)
	return nil
}
func (m *memAlerts) AddEvent(ctx context.Context, e domain.AlertEvent) error {
	m.events = append(m.events, e)
	return nil
}
func (m *memAlerts) EventsForApplication(ctx context.Context, appID string, since time.Time) ([]domain.AlertEvent, error) {
	return m.events, nil
}

type sentNotification struct{ to, title, detail string }

type memAlertNotifier struct{ sent []sentNotification }

func (m *memAlertNotifier) NotifyOwners(ctx context.Context, appID string, c domain.NotificationCategory, title, detail, rt, rid string) {
	m.sent = append(m.sent, sentNotification{"owners:" + appID, title, detail})
}
func (m *memAlertNotifier) NotifyUser(ctx context.Context, userID string, c domain.NotificationCategory, title, detail, rt, rid string) {
	m.sent = append(m.sent, sentNotification{"user:" + userID, title, detail})
}

type fixedAdmins []string

func (f fixedAdmins) AdminUserIDs(ctx context.Context) []string { return f }

type alertFixture struct {
	svc      *AlertService
	repo     *memAlerts
	notifier *memAlertNotifier
	clock    time.Time
	disk     float64
}

func newAlertFixture() *alertFixture {
	fx := &alertFixture{repo: &memAlerts{states: map[string]domain.AlertState{}}, notifier: &memAlertNotifier{},
		clock: time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)}
	apps := &memApps{apps: map[string]domain.Application{"app-1": {ID: "app-1", Name: "leave"}}}
	fx.svc = NewAlertService(fx.repo, apps, &memOwners{owner: "alice"}, fx.notifier, fixedAdmins{"admin-1"},
		domain.DefaultAlertThresholds(), func() (float64, error) { return fx.disk, nil })
	fx.svc.now = func() time.Time { return fx.clock }
	return fx
}

func (fx *alertFixture) traffic(requests, errors int64) {
	fx.repo.traffic = []domain.TrafficWindowStats{{ApplicationID: "app-1", Service: "api", Requests: requests,
		Errors: errors, LatencyMsSum: float64(requests) * 50}}
}

func (fx *alertFixture) eval(t *testing.T) []sentNotification {
	t.Helper()
	before := len(fx.notifier.sent)
	if _, err := fx.svc.Evaluate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return fx.notifier.sent[before:]
}

// DEC-028's rhythm, end to end: start, silence, escalation, silence,
// hourly reminder, recovery.
func TestAlerts_RhythmStartEscalateRemindResolve(t *testing.T) {
	fx := newAlertFixture()

	fx.traffic(100, 6) // 6% -> warning
	sent := fx.eval(t)
	if len(sent) != 1 || !strings.HasPrefix(sent[0].title, "Warning: leave error_rate") || sent[0].to != "owners:app-1" {
		t.Fatalf("expected one warning to the owners, got %+v", sent)
	}
	fx.clock = fx.clock.Add(time.Minute)
	if sent := fx.eval(t); len(sent) != 0 {
		t.Fatalf("an alert already announced must stay quiet, got %+v", sent)
	}

	fx.traffic(100, 30) // 30% -> critical
	fx.clock = fx.clock.Add(time.Minute)
	if sent := fx.eval(t); len(sent) != 1 || !strings.HasPrefix(sent[0].title, "Now critical") {
		t.Fatalf("an escalation is announced once, got %+v", sent)
	}
	fx.clock = fx.clock.Add(30 * time.Minute)
	if sent := fx.eval(t); len(sent) != 0 {
		t.Fatalf("no reminder within the hour, got %+v", sent)
	}
	fx.traffic(100, 10) // back to warning: not news
	fx.clock = fx.clock.Add(10 * time.Minute)
	if sent := fx.eval(t); len(sent) != 0 {
		t.Fatalf("an improvement that's still an alert isn't announced, got %+v", sent)
	}
	fx.clock = fx.clock.Add(21 * time.Minute) // an hour since the escalation notice
	sent = fx.eval(t)
	if len(sent) != 1 || !strings.HasPrefix(sent[0].title, "Still warning") || !strings.Contains(sent[0].detail, "since 10:00") {
		t.Fatalf("expected an hourly reminder mentioning when it started, got %+v", sent)
	}

	fx.traffic(1000, 10) // 1% -> fine
	fx.clock = fx.clock.Add(time.Minute)
	if sent := fx.eval(t); len(sent) != 1 || !strings.HasPrefix(sent[0].title, "Resolved") {
		t.Fatalf("recovery is announced once, got %+v", sent)
	}
	if len(fx.repo.states) != 0 {
		t.Fatalf("nothing should be firing, have %v", fx.repo.states)
	}
	kinds := []domain.AlertEventKind{}
	for _, e := range fx.repo.events {
		kinds = append(kinds, e.Kind)
	}
	if strings.Join(toStrings(kinds), ",") != "fired,escalated,reminded,resolved" {
		t.Fatalf("unexpected history %v", kinds)
	}
}

func toStrings(k []domain.AlertEventKind) []string {
	out := []string{}
	for _, x := range k {
		out = append(out, string(x))
	}
	return out
}

func TestAlerts_TooLittleTrafficIsNotJudged(t *testing.T) {
	fx := newAlertFixture()
	fx.traffic(3, 3) // 100% of 3 requests
	if sent := fx.eval(t); len(sent) != 0 {
		t.Fatalf("three failed requests are not an outage, got %+v", sent)
	}
}

func TestAlerts_LatencyThresholds(t *testing.T) {
	fx := newAlertFixture()
	fx.repo.traffic = []domain.TrafficWindowStats{{ApplicationID: "app-1", Service: "api", Requests: 10, LatencyMsSum: 10 * 3500}}
	sent := fx.eval(t)
	if len(sent) != 1 || !strings.HasPrefix(sent[0].title, "Critical: leave latency") || !strings.Contains(sent[0].detail, "3500 ms") {
		t.Fatalf("expected a critical latency alert, got %+v", sent)
	}
}

// CPU must be over the line for the whole window, not one spike.
func TestAlerts_CPUMustBeSustained(t *testing.T) {
	fx := newAlertFixture()
	fx.repo.resources = []domain.ResourceWindowStats{{ApplicationID: "app-1", Service: "api", Samples: 3, MinCPUPercent: 99, Span: 2 * time.Minute}}
	for _, s := range fx.eval(t) {
		if strings.Contains(s.title, "cpu") {
			t.Fatalf("two minutes of readings aren't ten sustained minutes: %+v", s)
		}
	}
	fx.repo.resources[0].Span = 9 * time.Minute
	fx.repo.resources[0].MinCPUPercent = 90
	sent := fx.eval(t)
	if len(sent) != 1 || !strings.HasPrefix(sent[0].title, "Warning: leave cpu") {
		t.Fatalf("expected a sustained-CPU warning, got %+v", sent)
	}
}

func TestAlerts_DiskGoesToTheAdmins(t *testing.T) {
	fx := newAlertFixture()
	fx.disk = 96
	sent := fx.eval(t)
	if len(sent) != 1 || sent[0].to != "user:admin-1" || !strings.HasPrefix(sent[0].title, "Critical: platform host_disk") {
		t.Fatalf("expected a critical disk alert to the admin, got %+v", sent)
	}
}

// A service that stops being measured — scaled to zero, stopped — has
// nothing wrong with it any more.
func TestAlerts_UnmeasuredAlertResolves(t *testing.T) {
	fx := newAlertFixture()
	fx.traffic(100, 50)
	fx.eval(t)
	fx.repo.traffic = nil
	fx.clock = fx.clock.Add(time.Minute)
	if sent := fx.eval(t); len(sent) != 1 || !strings.HasPrefix(sent[0].title, "Resolved") {
		t.Fatalf("expected the alert to resolve, got %+v", sent)
	}
}
