package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"platform-api/internal/domain"
	"platform-api/internal/service"
)

// fakeDatabaseSecretStore stands in for Module O inside Module N's tests.
type fakeDatabaseSecretStore struct {
	values map[string]string
	putErr error
}

func newFakeDatabaseSecretStore() *fakeDatabaseSecretStore {
	return &fakeDatabaseSecretStore{values: map[string]string{}}
}

func (f *fakeDatabaseSecretStore) PutManaged(ctx context.Context, applicationID, name, value string) error {
	if f.putErr != nil {
		return f.putErr
	}
	f.values[applicationID+"/"+name] = value
	return nil
}

func (f *fakeDatabaseSecretStore) ManagedValue(ctx context.Context, applicationID, name string) (string, error) {
	v, ok := f.values[applicationID+"/"+name]
	if !ok {
		return "", domain.ErrSecretNotFound
	}
	return v, nil
}

func (f *fakeDatabaseSecretStore) password(applicationID string) string {
	return f.values[applicationID+"/"+domain.DatabasePasswordSecret]
}

func newDatabaseServiceWithSecrets() (*service.DatabaseService, *fakeProvisionedDatabaseRepo, *fakeDatabaseRuntime, *fakeDatabaseSecretStore) {
	repo := newFakeProvisionedDatabaseRepo()
	runtime := newFakeDatabaseRuntime()
	secrets := newFakeDatabaseSecretStore()
	return service.NewDatabaseService(repo, runtime, secrets), repo, runtime, secrets
}

// If the password can't be stored, no database is started with it: there
// must never be a running database whose password exists only in memory.
func TestEnsureProvisioned_SecretStoreFailureStartsNothing(t *testing.T) {
	svc, repo, runtime, secrets := newDatabaseServiceWithSecrets()
	secrets.putErr = errors.New("secret store unavailable")

	if _, err := svc.EnsureProvisioned(context.Background(), testApp(), "postgres"); err == nil {
		t.Fatal("expected the secret store failure to surface")
	}
	if len(runtime.dbSpecs) != 0 || len(repo.rows) != 0 {
		t.Fatalf("a database was started or recorded without its password stored: specs=%d rows=%d", len(runtime.dbSpecs), len(repo.rows))
	}
}

// Found by a real deployment: a database that isn't accepting connections
// yet must not be reported as provisioned, or left running.
func TestEnsureProvisioned_NotReadyIsNotProvisioned(t *testing.T) {
	svc, repo, runtime, _ := newDatabaseServiceWithSecrets()
	runtime.readyErr = errors.New("pg_isready exited 2")

	if _, err := svc.EnsureProvisioned(context.Background(), testApp(), "postgres"); err == nil {
		t.Fatal("expected a readiness failure to fail provisioning")
	}
	if len(repo.rows) != 0 {
		t.Fatalf("an unready database was recorded as provisioned: %v", repo.rows)
	}
	if len(runtime.stopped) != 1 {
		t.Fatalf("the unready database container was left running: %v", runtime.stopped)
	}
}

func TestEnsureProvisioned_WaitsForReadinessOnlyWhenStartingOne(t *testing.T) {
	svc, _, runtime, _ := newDatabaseServiceWithSecrets()
	ctx := context.Background()
	if _, err := svc.EnsureProvisioned(ctx, testApp(), "postgres"); err != nil {
		t.Fatal(err)
	}
	// A redeploy reuses the live database; nothing new to wait for.
	if _, err := svc.EnsureProvisioned(ctx, testApp(), "postgres"); err != nil {
		t.Fatal(err)
	}
	if runtime.readyWaits != 1 {
		t.Fatalf("expected exactly one readiness wait, got %d", runtime.readyWaits)
	}
}

// FR-067's exception flow for Module N: no password, no start — never a
// connection string that can't authenticate.
func TestWiringFor_MissingPasswordFailsClosed(t *testing.T) {
	svc, _, _, secrets := newDatabaseServiceWithSecrets()
	if _, err := svc.EnsureProvisioned(context.Background(), testApp(), "postgres"); err != nil {
		t.Fatal(err)
	}
	delete(secrets.values, "app-1/"+domain.DatabasePasswordSecret)

	wiring, err := svc.WiringFor(context.Background(), "app-1")
	if !errors.Is(err, domain.ErrSecretNotFound) {
		t.Fatalf("expected the missing password to fail the wiring, got %v", err)
	}
	if len(wiring.Env) != 0 {
		t.Fatalf("a connection string without a password was handed out: %v", wiring.Env)
	}
}

func TestMigrateLegacyPlaintextPasswords_MovesThenClears(t *testing.T) {
	svc, repo, _, secrets := newDatabaseServiceWithSecrets()
	repo.legacy = []domain.LegacyDatabasePassword{
		{DatabaseID: "db-a", ApplicationID: "app-a", Password: "legacy-plaintext-a"},
		{DatabaseID: "db-b", ApplicationID: "app-b", Password: "legacy-plaintext-b"},
	}

	moved, err := svc.MigrateLegacyPlaintextPasswords(context.Background())
	if err != nil || moved != 2 {
		t.Fatalf("expected 2 moved, got %d, %v", moved, err)
	}
	if secrets.password("app-a") != "legacy-plaintext-a" || secrets.password("app-b") != "legacy-plaintext-b" {
		t.Fatalf("passwords did not arrive in the secret store intact: %v", secrets.values)
	}
	if strings.Join(repo.cleared, ",") != "db-a,db-b" {
		t.Fatalf("expected both plaintext copies cleared, got %v", repo.cleared)
	}
}

// Interrupted halfway, the plaintext must still be there to retry from: it
// is cleared only once the sealed copy is safely stored.
func TestMigrateLegacyPlaintextPasswords_StoreFailureKeepsThePlaintext(t *testing.T) {
	svc, repo, _, secrets := newDatabaseServiceWithSecrets()
	repo.legacy = []domain.LegacyDatabasePassword{{DatabaseID: "db-a", ApplicationID: "app-a", Password: "legacy-plaintext-a"}}
	secrets.putErr = errors.New("secret store unavailable")

	if _, err := svc.MigrateLegacyPlaintextPasswords(context.Background()); err == nil {
		t.Fatal("expected the failure to surface")
	}
	if len(repo.cleared) != 0 {
		t.Fatalf("a plaintext password was cleared without being stored first: %v", repo.cleared)
	}
}
