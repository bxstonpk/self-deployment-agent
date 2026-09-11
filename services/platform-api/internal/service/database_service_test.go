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

// fakeProvisionedDatabaseRepo is the in-memory stand-in for
// provisioned_databases, including its "one live database per application"
// constraint — the fake enforces it too, so a test can't pass here and
// fail against the real unique index.
type fakeProvisionedDatabaseRepo struct {
	rows    []domain.ProvisionedDatabase
	nextID  int
	legacy  []domain.LegacyDatabasePassword
	cleared []string
}

func newFakeProvisionedDatabaseRepo() *fakeProvisionedDatabaseRepo {
	return &fakeProvisionedDatabaseRepo{}
}

func (f *fakeProvisionedDatabaseRepo) Create(ctx context.Context, d domain.ProvisionedDatabase) (domain.ProvisionedDatabase, error) {
	for _, existing := range f.rows {
		if existing.ApplicationID == d.ApplicationID && existing.Status == domain.DatabaseProvisioned {
			return domain.ProvisionedDatabase{}, errors.New("one_live_database_per_application violated")
		}
	}
	f.nextID++
	d.ID = "db-" + string(rune('0'+f.nextID))
	d.Status = domain.DatabaseProvisioned
	f.rows = append(f.rows, d)
	return d, nil
}

func (f *fakeProvisionedDatabaseRepo) GetLiveForApplication(ctx context.Context, applicationID string) (domain.ProvisionedDatabase, error) {
	for _, d := range f.rows {
		if d.ApplicationID == applicationID && d.Status == domain.DatabaseProvisioned {
			return d, nil
		}
	}
	return domain.ProvisionedDatabase{}, domain.ErrDatabaseNotProvisioned
}

func (f *fakeProvisionedDatabaseRepo) MarkDeprovisioned(ctx context.Context, id string) error {
	for i := range f.rows {
		if f.rows[i].ID == id && f.rows[i].Status == domain.DatabaseProvisioned {
			f.rows[i].Status = domain.DatabaseDeprovisioned
		}
	}
	return nil
}

func (f *fakeProvisionedDatabaseRepo) ListLegacyPlaintextPasswords(ctx context.Context) ([]domain.LegacyDatabasePassword, error) {
	return f.legacy, nil
}

func (f *fakeProvisionedDatabaseRepo) ClearPlaintextPassword(ctx context.Context, id string) error {
	f.cleared = append(f.cleared, id)
	return nil
}

type fakeDatabaseRuntime struct {
	networksCreated []string
	networksRemoved []string
	dbSpecs         []domain.DatabaseSpec
	stopped         []string

	createNetworkErr error
	startErr         error
	readyErr         error
	stopErr          error
	removeNetworkErr error

	readyWaits int
}

func newFakeDatabaseRuntime() *fakeDatabaseRuntime {
	return &fakeDatabaseRuntime{}
}

func (f *fakeDatabaseRuntime) CreateNetwork(ctx context.Context, name string) (string, error) {
	if f.createNetworkErr != nil {
		return "", f.createNetworkErr
	}
	f.networksCreated = append(f.networksCreated, name)
	return "net-" + name, nil
}

func (f *fakeDatabaseRuntime) RemoveNetwork(ctx context.Context, networkID string) error {
	if f.removeNetworkErr != nil {
		return f.removeNetworkErr
	}
	f.networksRemoved = append(f.networksRemoved, networkID)
	return nil
}

func (f *fakeDatabaseRuntime) StartDatabase(ctx context.Context, spec domain.DatabaseSpec) (string, error) {
	if f.startErr != nil {
		return "", f.startErr
	}
	f.dbSpecs = append(f.dbSpecs, spec)
	return "container-" + spec.Name, nil
}

func (f *fakeDatabaseRuntime) Stop(ctx context.Context, containerID string) error {
	if f.stopErr != nil {
		return f.stopErr
	}
	f.stopped = append(f.stopped, containerID)
	return nil
}

func (f *fakeDatabaseRuntime) WaitDatabaseReady(ctx context.Context, containerID string, spec domain.DatabaseSpec, timeout time.Duration) error {
	f.readyWaits++
	return f.readyErr
}

func newDatabaseService() (*service.DatabaseService, *fakeProvisionedDatabaseRepo, *fakeDatabaseRuntime) {
	svc, repo, runtime, _ := newDatabaseServiceWithSecrets()
	return svc, repo, runtime
}

func testApp() domain.Application {
	return domain.Application{ID: "app-1", Name: "Leave Tracker"}
}

func TestEnsureProvisioned_CreatesIsolatedPostgres(t *testing.T) {
	svc, _, runtime, secrets := newDatabaseServiceWithSecrets()

	db, err := svc.EnsureProvisioned(context.Background(), testApp(), "postgres")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(runtime.networksCreated) != 1 {
		t.Fatalf("expected one private network (FR-062), got %v", runtime.networksCreated)
	}
	if len(runtime.dbSpecs) != 1 {
		t.Fatalf("expected one database container, got %d", len(runtime.dbSpecs))
	}
	spec := runtime.dbSpecs[0]
	if spec.NetworkID != runtime.networksCreated[0] && spec.NetworkID != "net-"+runtime.networksCreated[0] {
		t.Fatalf("database was not attached to the application's own network: %q vs %v", spec.NetworkID, runtime.networksCreated)
	}
	if db.NetworkID != spec.NetworkID {
		t.Fatalf("recorded network %q != started network %q", db.NetworkID, spec.NetworkID)
	}
	if db.Host != spec.Name {
		t.Fatalf("host should be the container name Docker resolves on the private network, got %q", db.Host)
	}
	if db.Port != 5432 {
		t.Fatalf("expected the in-network postgres port, got %d", db.Port)
	}
	if stored := secrets.password("app-1"); len(stored) < 20 || stored != spec.Password {
		t.Fatalf("expected the generated password in the secret store and in the database spec, got %q", stored)
	}
	if db.Status != domain.DatabaseProvisioned {
		t.Fatalf("expected status provisioned, got %q", db.Status)
	}
}

// A redeploy must not replace the database and lose its data — FR-061's
// whole point is a persistent store, so provisioning is idempotent.
func TestEnsureProvisioned_IsIdempotent(t *testing.T) {
	svc, _, runtime := newDatabaseService()

	first, err := svc.EnsureProvisioned(context.Background(), testApp(), "postgres")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	second, err := svc.EnsureProvisioned(context.Background(), testApp(), "postgres")
	if err != nil {
		t.Fatalf("unexpected error on the second call: %v", err)
	}

	if first.ID != second.ID {
		t.Fatalf("second provision created a new database (%s -> %s): the application's data would be gone", first.ID, second.ID)
	}
	if len(runtime.dbSpecs) != 1 {
		t.Fatalf("expected exactly one container ever started, got %d", len(runtime.dbSpecs))
	}
}

func TestEnsureProvisioned_UnsupportedTypeNeverReachesRuntime(t *testing.T) {
	svc, repo, runtime := newDatabaseService()

	_, err := svc.EnsureProvisioned(context.Background(), testApp(), "mongodb")
	if !errors.Is(err, domain.ErrUnsupportedDatabaseType) {
		t.Fatalf("expected ErrUnsupportedDatabaseType, got %v", err)
	}
	if len(runtime.dbSpecs) != 0 || len(runtime.networksCreated) != 0 {
		t.Fatalf("an unsupported type touched the runtime: containers=%v networks=%v", runtime.dbSpecs, runtime.networksCreated)
	}
	if len(repo.rows) != 0 {
		t.Fatalf("an unsupported type was recorded as provisioned: %v", repo.rows)
	}
}

func TestEnsureProvisioned_NoDeclaredTypeProvisionsNothing(t *testing.T) {
	svc, _, runtime := newDatabaseService()

	_, err := svc.EnsureProvisioned(context.Background(), testApp(), "")
	if !errors.Is(err, domain.ErrDatabaseNotProvisioned) {
		t.Fatalf("expected ErrDatabaseNotProvisioned, got %v", err)
	}
	if len(runtime.dbSpecs) != 0 {
		t.Fatalf("an application declaring no database got one anyway: %v", runtime.dbSpecs)
	}
}

func TestWiringFor_CarriesConnectionDetailsAndNetwork(t *testing.T) {
	svc, _, _, secrets := newDatabaseServiceWithSecrets()

	db, err := svc.EnsureProvisioned(context.Background(), testApp(), "postgres")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wiring, err := svc.WiringFor(context.Background(), "app-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wiring.NetworkID != db.NetworkID {
		t.Fatalf("wiring network %q != database network %q", wiring.NetworkID, db.NetworkID)
	}

	env := strings.Join(wiring.Env, "\n")
	for _, want := range []string{"DATABASE_URL=", "DATABASE_HOST=" + db.Host, "DATABASE_PORT=5432", "DATABASE_NAME=appdb", "DATABASE_USER=appuser"} {
		if !strings.Contains(env, want) {
			t.Fatalf("wiring env is missing %q:\n%s", want, env)
		}
	}
	password := secrets.password("app-1")
	if !strings.Contains(env, "DATABASE_PASSWORD="+password) {
		t.Fatalf("wiring env does not carry the generated password")
	}
	// The DSN must be usable as-is: a password with '@' or '/' in it would
	// otherwise split the URL in the wrong place.
	if !strings.Contains(env, "postgres://appuser:"+password+"@"+db.Host+":5432/appdb") {
		t.Fatalf("DATABASE_URL is not a well-formed DSN:\n%s", env)
	}
}

// The uniform call sites in deploy/resume/restart/cold-start depend on
// this: an application without a database gets empty wiring and no error,
// so nothing has to branch on whether one exists.
func TestWiringFor_NoDatabaseIsEmptyNotAnError(t *testing.T) {
	svc, _, _ := newDatabaseService()

	wiring, err := svc.WiringFor(context.Background(), "app-without-db")
	if err != nil {
		t.Fatalf("expected no error for an application without a database, got %v", err)
	}
	if len(wiring.Env) != 0 || wiring.NetworkID != "" {
		t.Fatalf("expected empty wiring, got %+v", wiring)
	}
}

func TestDeprovision_TearsDownContainerNetworkAndRecord(t *testing.T) {
	svc, repo, runtime := newDatabaseService()

	db, err := svc.EnsureProvisioned(context.Background(), testApp(), "postgres")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := svc.Deprovision(context.Background(), "app-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(runtime.stopped) != 1 || runtime.stopped[0] != db.ContainerID {
		t.Fatalf("expected the database container to be stopped, got %v", runtime.stopped)
	}
	if len(runtime.networksRemoved) != 1 || runtime.networksRemoved[0] != db.NetworkID {
		t.Fatalf("expected the private network to be removed, got %v", runtime.networksRemoved)
	}
	if _, err := repo.GetLiveForApplication(context.Background(), "app-1"); !errors.Is(err, domain.ErrDatabaseNotProvisioned) {
		t.Fatalf("expected no live database after deprovision, got %v", err)
	}
}

// FR-065's deletion must be able to finish. A container or network that a
// previous partial attempt already removed is a success, not a failure.
func TestDeprovision_ToleratesResourcesAlreadyGone(t *testing.T) {
	svc, repo, runtime := newDatabaseService()

	if _, err := svc.EnsureProvisioned(context.Background(), testApp(), "postgres"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	runtime.stopErr = errors.New("No such container")
	runtime.removeNetworkErr = errors.New("network not found")

	if err := svc.Deprovision(context.Background(), "app-1"); err != nil {
		t.Fatalf("deprovision must survive already-gone runtime resources, got %v", err)
	}
	if _, err := repo.GetLiveForApplication(context.Background(), "app-1"); !errors.Is(err, domain.ErrDatabaseNotProvisioned) {
		t.Fatalf("the record was left live, so the deletion could never complete: %v", err)
	}
}

func TestDeprovision_WithoutADatabaseIsANoOp(t *testing.T) {
	svc, _, runtime := newDatabaseService()

	if err := svc.Deprovision(context.Background(), "app-without-db"); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(runtime.stopped) != 0 || len(runtime.networksRemoved) != 0 {
		t.Fatalf("deprovision touched the runtime for an application with no database")
	}
}

func TestEnsureProvisioned_RuntimeFailureRecordsNothing(t *testing.T) {
	svc, repo, runtime := newDatabaseService()
	runtime.startErr = errors.New("no such image")

	if _, err := svc.EnsureProvisioned(context.Background(), testApp(), "postgres"); err == nil {
		t.Fatal("expected the runtime failure to surface")
	}
	if len(repo.rows) != 0 {
		t.Fatalf("a database that never started was recorded as provisioned: %v", repo.rows)
	}
	// A retry must be able to reuse the network rather than trip over it.
	runtime.startErr = nil
	if _, err := svc.EnsureProvisioned(context.Background(), testApp(), "postgres"); err != nil {
		t.Fatalf("retry after a runtime failure should succeed, got %v", err)
	}
}
