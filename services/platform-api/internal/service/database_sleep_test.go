package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"platform-api/internal/domain"
	"platform-api/internal/service"
)

// listingDatabaseRepo adds ListLive, as the Postgres repository has it.
type listingDatabaseRepo struct{ *fakeProvisionedDatabaseRepo }

func (f listingDatabaseRepo) ListLive(ctx context.Context) ([]domain.ProvisionedDatabase, error) {
	var out []domain.ProvisionedDatabase
	for _, d := range f.rows {
		if d.Status == domain.DatabaseProvisioned {
			out = append(out, d)
		}
	}
	return out, nil
}

type fakeSleeper struct {
	running   map[string]bool // database container -> running
	appBusy   map[string]bool // application -> its containers running
	tuned     map[string]bool
	calls     []string
	wakeError error
}

func (f *fakeSleeper) TuneDatabase(ctx context.Context, id, engine string) (bool, error) {
	if engine != "mysql" || f.tuned[id] {
		return false, nil
	}
	f.tuned[id] = true
	return true, nil
}
func (f *fakeSleeper) DatabaseRunning(ctx context.Context, id string) (bool, error) {
	return f.running[id], nil
}
func (f *fakeSleeper) SleepDatabase(ctx context.Context, id string) error {
	f.calls = append(f.calls, "sleep "+id)
	f.running[id] = false
	return nil
}
func (f *fakeSleeper) WakeDatabase(ctx context.Context, id string) error {
	f.calls = append(f.calls, "wake "+id)
	if f.wakeError != nil {
		return f.wakeError
	}
	f.running[id] = true
	return nil
}
func (f *fakeSleeper) RunningServices(ctx context.Context, appID string) ([]string, error) {
	out := []string{"frontend"} // a static frontend always runs (FR-051)
	if f.appBusy[appID] {
		out = append(out, "api")
	}
	return out, nil
}

func sleepyDatabases(t *testing.T) (*service.DatabaseService, *fakeSleeper) {
	t.Helper()
	repo := newFakeProvisionedDatabaseRepo()
	_, _ = repo.Create(context.Background(), domain.ProvisionedDatabase{ApplicationID: "app-1", Engine: "mysql",
		ContainerID: "c-1", DatabaseName: "appdb", Username: "appuser"})
	secrets := newFakeDatabaseSecretStore()
	_ = secrets.PutManaged(context.Background(), "app-1", domain.DatabasePasswordSecret, "pw")
	svc := service.NewDatabaseService(listingDatabaseRepo{repo}, newFakeDatabaseRuntime(), secrets)
	sl := &fakeSleeper{running: map[string]bool{"c-1": true}, appBusy: map[string]bool{}, tuned: map[string]bool{}}
	apps := newFakeLifecycleRepo(domain.Application{ID: "app-1", Name: "leave", LifecycleStatus: domain.StatusRunning,
		DeploymentYAMLDraft: "app:\n  name: leave\nservices:\n  frontend:\n    runtime: react\n  api:\n    runtime: go\n    port: 8080\n"})
	svc.SetSleeper(sl, apps, 15*time.Minute)
	return svc, sl
}

func TestDatabases_SleepOnlyAfterTheApplicationHasBeenIdleForTheTimeout(t *testing.T) {
	svc, sl := sleepyDatabases(t)
	ctx := context.Background()
	t0 := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)

	sl.appBusy["app-1"] = true
	if n, _ := svc.SleepIdle(ctx, t0); n != 0 {
		t.Fatal("its application is running")
	}
	sl.appBusy["app-1"] = false
	if n, _ := svc.SleepIdle(ctx, t0.Add(time.Minute)); n != 0 {
		t.Fatal("idle only just now")
	}
	if n, _ := svc.SleepIdle(ctx, t0.Add(10*time.Minute)); n != 0 {
		t.Fatal("not idle long enough")
	}
	if n, _ := svc.SleepIdle(ctx, t0.Add(17*time.Minute)); n != 1 || sl.running["c-1"] {
		t.Fatalf("idle 16 minutes: asleep (%d, running %v)", n, sl.running["c-1"])
	}
	if strings.Join(sl.calls, "|") != "sleep c-1" {
		t.Fatalf("stopped, never removed: %v", sl.calls)
	}
}

func TestDatabases_ARunningApplicationResetsTheIdleClock(t *testing.T) {
	svc, sl := sleepyDatabases(t)
	ctx := context.Background()
	t0 := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	_, _ = svc.SleepIdle(ctx, t0)
	sl.appBusy["app-1"] = true
	_, _ = svc.SleepIdle(ctx, t0.Add(10*time.Minute))
	sl.appBusy["app-1"] = false
	if n, _ := svc.SleepIdle(ctx, t0.Add(20*time.Minute)); n != 0 {
		t.Fatal("idle again only since 20 minutes")
	}
}

func TestDatabases_EveryStartWakesASleepingDatabase(t *testing.T) {
	svc, sl := sleepyDatabases(t)
	sl.running["c-1"] = false
	w, err := svc.WiringFor(context.Background(), "app-1")
	if err != nil || len(w.Env) == 0 || !sl.running["c-1"] {
		t.Fatalf("woken before the application gets its wiring: %v %v", err, sl.calls)
	}
	// Awake already: nothing to do.
	sl.calls = nil
	_, _ = svc.WiringFor(context.Background(), "app-1")
	if len(sl.calls) != 0 {
		t.Fatalf("a running database isn't started again: %v", sl.calls)
	}
}

func TestDatabases_AStartFailsWhenItsDatabaseWontWake(t *testing.T) {
	svc, sl := sleepyDatabases(t)
	sl.running["c-1"] = false
	sl.wakeError = errors.New("no space left on device")
	if _, err := svc.WiringFor(context.Background(), "app-1"); err == nil {
		t.Fatal("an application isn't started against a database that is down")
	}
}

func TestDatabases_TuneAllOnlyChangesMySQLOnce(t *testing.T) {
	svc, _ := sleepyDatabases(t)
	if n, _ := svc.TuneAll(context.Background()); n != 1 {
		t.Fatalf("the MySQL database: %d", n)
	}
	if n, _ := svc.TuneAll(context.Background()); n != 0 {
		t.Fatalf("already tuned: %d", n)
	}
}

// --- failed applications that never ran -------------------------------------------------

type fakeFailedApps struct{ apps []domain.Application }

func (f fakeFailedApps) ListFailed(ctx context.Context) ([]domain.Application, error) {
	return f.apps, nil
}

func TestFailedCleanup_RemovesOnlyApplicationsThatNeverRan(t *testing.T) {
	ctx := context.Background()
	never := domain.Application{ID: "never", Name: "broken", LifecycleStatus: domain.StatusFailed}
	ran := domain.Application{ID: "ran", Name: "real-data", LifecycleStatus: domain.StatusFailed}
	withDev := domain.Application{ID: "with-dev", Name: "trying", LifecycleStatus: domain.StatusFailed}
	apps := newFakeLifecycleRepo(never, ran, withDev,
		domain.Application{ID: "with-dev-dev", Name: "trying-dev", LifecycleStatus: domain.StatusRunning})
	owners := newFakeOwnerRepo()
	for _, id := range []string{"never", "ran", "with-dev"} {
		owners.owners[id] = []domain.ApplicationOwner{{ApplicationID: id, UserID: "owner-1", OwnershipRole: domain.OwnerRolePrimary, Status: "active"}}
	}
	deployments := newFakeDeploymentRepo()
	deployments.byID["d-ran"] = domain.Deployment{ID: "d-ran", ApplicationID: "ran", Status: domain.DeploymentSuperseded}
	builds := newFakeBuildRepo()
	b, _ := builds.Create(ctx, "never", "owner-1")
	_, _ = builds.MarkFailed(ctx, b.ID, domain.ErrorCategorySource, "build failed: main.go:3: syntax error")
	notify := newFakeNotificationRecorder()
	var deleted []string
	svc := service.NewFailedCleanupService(fakeFailedApps{[]domain.Application{never, ran, withDev}}, service.FailedCleanupLookups{
		Deployments: deployments, Builds: builds, Owners: owners, Apps: apps,
		DevCopy: func(ctx context.Context, parentID string) (domain.DevCopy, error) {
			if parentID == "with-dev" {
				return domain.DevCopy{ApplicationID: "with-dev-dev", ParentID: "with-dev"}, nil
			}
			return domain.DevCopy{}, domain.ErrNotFound
		},
	}, func(ctx context.Context, id, who string, confirm bool) (domain.Application, error) {
		deleted = append(deleted, id+" by "+who)
		return domain.Application{}, nil
	}, notify, newFakeAuditRecorder())

	n, err := svc.Sweep(ctx)
	if err != nil || n != 1 || strings.Join(deleted, "|") != "never by owner-1" {
		t.Fatalf("only the one that never ran, and has no dev copy: %d %v %v", n, deleted, err)
	}
	calls := notify.all()
	if len(calls) != 1 || calls[0].ApplicationID != "never" || !strings.Contains(calls[0].Detail, "syntax error") ||
		!strings.Contains(calls[0].Detail, "deploy it again") || !strings.Contains(calls[0].Detail, "same name") {
		t.Fatalf("the owners are told why, and to deploy again under the same name: %+v", calls)
	}
}
