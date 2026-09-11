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

// FR-068 for the database credential: the database gets a new password,
// the store gets the same one, and it isn't the old one.
func TestRotatePassword_SetsItOnTheDatabaseThenStoresIt(t *testing.T) {
	svc, _, runtime, secrets := newDatabaseServiceWithSecrets()
	ctx := context.Background()
	if _, err := svc.EnsureProvisioned(ctx, testApp(), "postgres"); err != nil {
		t.Fatal(err)
	}
	old := secrets.password("app-1")

	if err := svc.RotatePassword(ctx, "app-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	stored := secrets.password("app-1")
	if stored == old || len(stored) < 20 {
		t.Fatalf("expected a fresh password in the store, got %q (old %q)", stored, old)
	}
	if len(runtime.passwordsSet) != 1 || runtime.passwordsSet[0] != stored {
		t.Fatalf("the database was not given the password the store holds: %v", runtime.passwordsSet)
	}
}

// If the store can't take the new password, the database must not keep
// it: the two would disagree, and every later start would fail to log in.
func TestRotatePassword_StoreFailurePutsTheDatabaseBack(t *testing.T) {
	svc, _, runtime, secrets := newDatabaseServiceWithSecrets()
	ctx := context.Background()
	if _, err := svc.EnsureProvisioned(ctx, testApp(), "postgres"); err != nil {
		t.Fatal(err)
	}
	old := secrets.password("app-1")
	secrets.putErr = errors.New("secret store unavailable")

	if err := svc.RotatePassword(ctx, "app-1"); err == nil {
		t.Fatal("expected the store failure to surface")
	}
	if len(runtime.passwordsSet) != 2 || runtime.passwordsSet[1] != old {
		t.Fatalf("expected the new password set, then the old one restored: %v", runtime.passwordsSet)
	}
	if secrets.password("app-1") != old {
		t.Fatal("the stored password changed even though the rotation failed")
	}
}

func TestRotatePassword_DatabaseFailureLeavesTheStoreAlone(t *testing.T) {
	svc, _, runtime, secrets := newDatabaseServiceWithSecrets()
	ctx := context.Background()
	if _, err := svc.EnsureProvisioned(ctx, testApp(), "postgres"); err != nil {
		t.Fatal(err)
	}
	old := secrets.password("app-1")
	runtime.setPasswordErr = errors.New("ALTER ROLE exited 1")

	if err := svc.RotatePassword(ctx, "app-1"); err == nil {
		t.Fatal("expected the database failure to surface")
	}
	if secrets.password("app-1") != old {
		t.Fatal("the store took a password the database never accepted")
	}
}

func TestRotatePassword_WithoutADatabase(t *testing.T) {
	svc, _, _, _ := newDatabaseServiceWithSecrets()
	if err := svc.RotatePassword(context.Background(), "app-without-db"); !errors.Is(err, domain.ErrDatabaseNotProvisioned) {
		t.Fatalf("expected ErrDatabaseNotProvisioned, got %v", err)
	}
}
