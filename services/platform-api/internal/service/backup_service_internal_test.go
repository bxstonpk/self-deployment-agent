package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"platform-api/internal/domain"
	"platform-api/internal/secretbox"
)

type memBackups struct {
	rows map[string]domain.DatabaseBackup
}

func (m *memBackups) Create(ctx context.Context, b domain.DatabaseBackup) (domain.DatabaseBackup, error) {
	m.rows[b.ID] = b
	return b, nil
}
func (m *memBackups) Get(ctx context.Context, id string) (domain.DatabaseBackup, error) {
	if b, ok := m.rows[id]; ok {
		return b, nil
	}
	return domain.DatabaseBackup{}, domain.ErrBackupNotFound
}
func (m *memBackups) sorted(keep func(domain.DatabaseBackup) bool) []domain.DatabaseBackup {
	out := []domain.DatabaseBackup{}
	for _, b := range m.rows {
		if keep(b) {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TakenAt.After(out[j].TakenAt) })
	return out
}
func (m *memBackups) ListForApplication(ctx context.Context, appID string) ([]domain.DatabaseBackup, error) {
	return m.sorted(func(b domain.DatabaseBackup) bool {
		return b.ApplicationID == appID && b.Status != domain.BackupDeleted
	}), nil
}
func (m *memBackups) AllForApplication(ctx context.Context, appID string) ([]domain.DatabaseBackup, error) {
	return m.ListForApplication(ctx, appID)
}
func (m *memBackups) LatestFull(ctx context.Context, dbID string) (domain.DatabaseBackup, error) {
	l := m.sorted(func(b domain.DatabaseBackup) bool {
		return b.DatabaseID == dbID && b.Kind == domain.BackupFull && b.Status == domain.BackupSucceeded
	})
	if len(l) == 0 {
		return domain.DatabaseBackup{}, domain.ErrBackupNotFound
	}
	return l[0], nil
}
func (m *memBackups) LastAttempt(ctx context.Context, dbID string, since time.Time) (time.Time, domain.BackupStatus, bool, error) {
	l := m.sorted(func(b domain.DatabaseBackup) bool {
		return b.DatabaseID == dbID && !b.TakenAt.Before(since) && b.Trigger == domain.BackupScheduled
	})
	for _, b := range l {
		if b.Status == domain.BackupSucceeded {
			return b.TakenAt, b.Status, true, nil
		}
	}
	if len(l) == 0 {
		return time.Time{}, "", false, nil
	}
	return l[0].TakenAt, l[0].Status, true, nil
}
func (m *memBackups) OlderThan(ctx context.Context, dbID string, t time.Time) ([]domain.DatabaseBackup, error) {
	return m.sorted(func(b domain.DatabaseBackup) bool {
		return b.DatabaseID == dbID && b.TakenAt.Before(t) && b.Status != domain.BackupDeleted
	}), nil
}
func (m *memBackups) MarkDeleted(ctx context.Context, id string) error {
	b := m.rows[id]
	b.Status = domain.BackupDeleted
	m.rows[id] = b
	return nil
}

type memDatabases struct{ dbs []domain.ProvisionedDatabase }

func (m *memDatabases) ListLive(ctx context.Context) ([]domain.ProvisionedDatabase, error) {
	return m.dbs, nil
}
func (m *memDatabases) GetLiveForApplication(ctx context.Context, appID string) (domain.ProvisionedDatabase, error) {
	for _, d := range m.dbs {
		if d.ApplicationID == appID {
			return d, nil
		}
	}
	return domain.ProvisionedDatabase{}, domain.ErrDatabaseNotProvisioned
}

type memApps struct {
	ApplicationLifecycleRepository
	apps map[string]domain.Application
}

func (m *memApps) GetByID(ctx context.Context, id string) (domain.Application, error) {
	if a, ok := m.apps[id]; ok {
		return a, nil
	}
	return domain.Application{}, domain.ErrNotFound
}

type memOwners struct {
	ApplicationOwnerRepository
	owner string
}

func (m *memOwners) ListForApplication(ctx context.Context, appID string) ([]domain.ApplicationOwner, error) {
	return []domain.ApplicationOwner{{UserID: m.owner, Status: "active"}}, nil
}

type memSecrets struct{}

func (memSecrets) PutManaged(ctx context.Context, appID, name, value string) error { return nil }
func (memSecrets) ManagedValue(ctx context.Context, appID, name string) (string, error) {
	return "db-password", nil
}

// memDatabaseRuntime is a database whose contents are a dump string.
type memDatabaseRuntime struct {
	contents string
	dumpErr  error
	restored []string
	dumps    int
}

func (m *memDatabaseRuntime) DumpDatabase(ctx context.Context, containerID string, spec domain.DatabaseSpec) ([]byte, error) {
	m.dumps++
	if m.dumpErr != nil {
		return nil, m.dumpErr
	}
	return []byte(m.contents), nil
}
func (m *memDatabaseRuntime) RestoreDatabase(ctx context.Context, containerID string, spec domain.DatabaseSpec, dump []byte) error {
	m.restored = append(m.restored, string(dump))
	m.contents = string(dump)
	return nil
}

type memNotifier struct{ titles []string }

func (m *memNotifier) NotifyOwners(ctx context.Context, appID string, c domain.NotificationCategory, title, detail, rt, rid string) {
	m.titles = append(m.titles, title)
}

type backupFixture struct {
	svc      *BackupService
	backups  *memBackups
	runtime  *memDatabaseRuntime
	notifier *memNotifier
	apps     *memApps
	dir      string
	clock    time.Time
}

var bangkok = time.FixedZone("ICT", 7*3600)

func rows(n int, changed map[int]string) string {
	var b strings.Builder
	b.WriteString("DROP TABLE IF EXISTS items;\nCREATE TABLE items (id int, title text);\n")
	for i := 1; i <= n; i++ {
		if v, ok := changed[i]; ok {
			b.WriteString(v + "\n")
			continue
		}
		// Distinct rows, like real data — identical ones would compress
		// to almost nothing and make every size comparison meaningless.
		h := sha256.Sum256([]byte(fmt.Sprint(i)))
		fmt.Fprintf(&b, "INSERT INTO items VALUES (%d,'%s');\n", i, hex.EncodeToString(h[:]))
	}
	return b.String()
}

func newBackupFixture(t *testing.T) *backupFixture {
	t.Helper()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, err := secretbox.New(key)
	if err != nil {
		t.Fatal(err)
	}
	fx := &backupFixture{
		backups:  &memBackups{rows: map[string]domain.DatabaseBackup{}},
		runtime:  &memDatabaseRuntime{contents: rows(500, nil)},
		notifier: &memNotifier{},
		apps: &memApps{apps: map[string]domain.Application{"app-1": {ID: "app-1", Name: "leave",
			DeploymentYAMLDraft: "database:\n  type: mysql\n"}}},
		dir: t.TempDir(),
	}
	dbs := &memDatabases{dbs: []domain.ProvisionedDatabase{{ID: "db-1", ApplicationID: "app-1", Engine: "mysql",
		ContainerID: "c-1", DatabaseName: "appdb", Username: "appuser"}}}
	fx.svc = NewBackupService(fx.backups, dbs, fx.apps, &memOwners{owner: "alice"}, memSecrets{}, fx.runtime, box,
		fx.notifier, nil, fx.dir, BackupSchedule{Location: bangkok, Hour: 1, FullWeekday: time.Sunday})
	fx.svc.now = func() time.Time { return fx.clock }
	return fx
}

func (fx *backupFixture) at(t *testing.T, s string) {
	t.Helper()
	tm, err := time.ParseInLocation("2006-01-02 15:04", s, bangkok)
	if err != nil {
		t.Fatal(err)
	}
	fx.clock = tm
}

func (fx *backupFixture) live() []domain.DatabaseBackup {
	l, _ := fx.backups.ListForApplication(context.Background(), "app-1")
	return l
}

func (fx *backupFixture) files(t *testing.T) int {
	entries, _ := os.ReadDir(filepath.Join(fx.dir, "app-1"))
	return len(entries)
}

// DEC-025 over a week: the first backup is a full whatever the day, then
// one differential a day, then Sunday's full retires the whole set.
func TestScheduledBackups_FullSundayDifferentialsAndWeeklyRetention(t *testing.T) {
	fx := newBackupFixture(t)
	ctx := context.Background()

	fx.at(t, "2026-10-21 00:30") // Wednesday, before the 01:00 window
	if n, _ := fx.svc.RunScheduled(ctx); n != 0 {
		t.Fatalf("nothing is due before the backup hour, took %d", n)
	}
	fx.at(t, "2026-10-21 01:10")
	if n, _ := fx.svc.RunScheduled(ctx); n != 1 || fx.live()[0].Kind != domain.BackupFull {
		t.Fatalf("the first backup must be a full, got %+v", fx.live())
	}
	fx.at(t, "2026-10-21 05:00")
	if n, _ := fx.svc.RunScheduled(ctx); n != 0 {
		t.Fatal("one backup a day")
	}

	firstFull := fx.live()[0]
	for i, day := range []string{"2026-10-22", "2026-10-23", "2026-10-24"} { // Thu, Fri, Sat
		fx.runtime.contents = rows(500, map[int]string{10 + i: "INSERT INTO items VALUES (changed);"})
		fx.at(t, day+" 01:05")
		if n, _ := fx.svc.RunScheduled(ctx); n != 1 {
			t.Fatalf("%s: expected a backup", day)
		}
		d := fx.live()[0]
		if d.Kind != domain.BackupDifferential || d.BaseBackupID != firstFull.ID {
			t.Fatalf("%s: expected a differential against %s, got %+v", day, firstFull.ID, d)
		}
		if d.StoredBytes*5 > firstFull.StoredBytes {
			t.Fatalf("%s: a one-row differential (%d bytes) should be far smaller than the full (%d)", day, d.StoredBytes, firstFull.StoredBytes)
		}
	}
	if len(fx.live()) != 4 || fx.files(t) != 4 {
		t.Fatalf("expected the full and three differentials, got %d records, %d files", len(fx.live()), fx.files(t))
	}

	fx.at(t, "2026-10-25 01:00") // Sunday
	if n, _ := fx.svc.RunScheduled(ctx); n != 1 {
		t.Fatal("expected Sunday's backup")
	}
	live := fx.live()
	if len(live) != 1 || live[0].Kind != domain.BackupFull || fx.files(t) != 1 {
		t.Fatalf("Sunday's full should leave only itself, got %d records, %d files", len(live), fx.files(t))
	}
}

func TestScheduledBackups_OptOutIsSkipped(t *testing.T) {
	fx := newBackupFixture(t)
	fx.apps.apps["app-1"] = domain.Application{ID: "app-1", Name: "leave",
		DeploymentYAMLDraft: "database:\n  type: mysql\n  backup: none\n"}
	fx.at(t, "2026-10-25 02:00")
	if n, _ := fx.svc.RunScheduled(context.Background()); n != 0 || fx.runtime.dumps != 0 {
		t.Fatal("an application with database.backup: none must not be backed up")
	}
}

// A failure is recorded, owners hear about it once, and the next attempt
// waits an hour rather than hammering a broken database every sweep.
func TestScheduledBackups_FailureNotifiesOnceAndRetriesHourly(t *testing.T) {
	fx := newBackupFixture(t)
	ctx := context.Background()
	fx.runtime.dumpErr = errors.New("mysqldump exited 2: Access denied")

	fx.at(t, "2026-10-21 01:00")
	_, _ = fx.svc.RunScheduled(ctx)
	fx.at(t, "2026-10-21 01:30")
	_, _ = fx.svc.RunScheduled(ctx)
	if fx.runtime.dumps != 1 || len(fx.notifier.titles) != 1 {
		t.Fatalf("expected one attempt and one notification within the hour, got %d attempts, %v", fx.runtime.dumps, fx.notifier.titles)
	}
	if b := fx.live()[0]; b.Status != domain.BackupFailed || !strings.Contains(b.Error, "Access denied") {
		t.Fatalf("the failure should be recorded with its reason, got %+v", b)
	}

	fx.runtime.dumpErr = nil
	fx.at(t, "2026-10-21 02:05")
	if n, _ := fx.svc.RunScheduled(ctx); n != 1 || len(fx.notifier.titles) != 1 {
		t.Fatalf("the retry an hour later should succeed without another notification, took %d", n)
	}
}

func TestRestore_RebuildsTheExactDumpAndBacksUpFirst(t *testing.T) {
	fx := newBackupFixture(t)
	ctx := context.Background()
	fx.at(t, "2026-10-25 01:00")
	_, _ = fx.svc.RunScheduled(ctx)
	monday := rows(500, map[int]string{7: "INSERT INTO items VALUES (monday);"})
	fx.runtime.contents = monday
	fx.at(t, "2026-10-26 01:00")
	_, _ = fx.svc.RunScheduled(ctx)
	diff := fx.live()[0]

	fx.runtime.contents = "DROP TABLE items; -- someone's mistake\n"
	if _, err := fx.svc.Restore(ctx, "app-1", diff.ID, "alice", "wrong-name"); !errors.Is(err, domain.ErrRestoreConfirmation) {
		t.Fatalf("restoring without the exact name must be refused, got %v", err)
	}
	if _, err := fx.svc.Restore(ctx, "app-1", diff.ID, "mallory", "leave"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("a non-owner must not restore, got %v", err)
	}
	safety, err := fx.svc.Restore(ctx, "app-1", diff.ID, "alice", "leave")
	if err != nil {
		t.Fatal(err)
	}
	if len(fx.runtime.restored) != 1 || fx.runtime.restored[0] != monday {
		t.Fatal("the restore did not load Monday's exact dump")
	}
	if safety.Trigger != domain.BackupPreRestore || safety.Kind != domain.BackupDifferential {
		t.Fatalf("expected a pre-restore differential of the overwritten contents, got %+v", safety)
	}
	// ... which is itself restorable, and didn't retire the week's set.
	if len(fx.live()) != 3 {
		t.Fatalf("a pre-restore backup must not retire the set, have %d backups", len(fx.live()))
	}
	if _, err := fx.svc.Restore(ctx, "app-1", safety.ID, "alice", "leave"); err != nil || !strings.Contains(fx.runtime.contents, "someone's mistake") {
		t.Fatalf("the pre-restore backup should bring back what the restore replaced: %v", err)
	}
}

func TestRestore_RefusesATamperedFile(t *testing.T) {
	fx := newBackupFixture(t)
	ctx := context.Background()
	fx.at(t, "2026-10-25 01:00")
	_, _ = fx.svc.RunScheduled(ctx)
	full := fx.live()[0]
	p := filepath.Join(fx.dir, "app-1", full.FileName)
	data, _ := os.ReadFile(p)
	data[len(data)-1] ^= 0xff
	_ = os.WriteFile(p, data, 0o600)

	if _, err := fx.svc.Restore(ctx, "app-1", full.ID, "alice", "leave"); err == nil {
		t.Fatal("a tampered backup must not be restored")
	}
	if len(fx.runtime.restored) != 0 {
		t.Fatal("nothing should have been loaded into the database")
	}
}

func TestBackupFiles_AreSealed(t *testing.T) {
	fx := newBackupFixture(t)
	fx.runtime.contents = rows(50, map[int]string{3: "INSERT INTO items VALUES ('secret-payroll-row');"})
	fx.at(t, "2026-10-25 01:00")
	_, _ = fx.svc.RunScheduled(context.Background())
	data, _ := os.ReadFile(filepath.Join(fx.dir, "app-1", fx.live()[0].FileName))
	if strings.Contains(string(data), "secret-payroll-row") || strings.Contains(string(data), "INSERT") {
		t.Fatal("the backup file holds readable data")
	}
}

func TestBackupPolicyFor(t *testing.T) {
	for draft, want := range map[string]string{
		"":                              domain.BackupPolicyWeekly,
		"database:\n  type: mysql\n":    domain.BackupPolicyWeekly,
		"database:\n  backup: weekly\n": domain.BackupPolicyWeekly,
		"database:\n  backup: none\n":   domain.BackupPolicyNone,
		"not: [valid":                   domain.BackupPolicyWeekly,
	} {
		if got := BackupPolicyFor(domain.Application{DeploymentYAMLDraft: draft}); got != want {
			t.Errorf("%q: got %s, want %s", draft, got, want)
		}
	}
}
