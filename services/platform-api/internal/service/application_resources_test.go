package service_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"platform-api/internal/domain"
	"platform-api/internal/service"
)

type fakeSecretInjector struct {
	env        []string
	err        error
	deletedFor []string
}

func (f *fakeSecretInjector) EnvFor(ctx context.Context, applicationID string) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.env, nil
}

func (f *fakeSecretInjector) DeleteAllForApplication(ctx context.Context, applicationID string) error {
	f.deletedFor = append(f.deletedFor, applicationID)
	return nil
}

func TestApplicationResources_WiringMergesDatabaseAndSecrets(t *testing.T) {
	databases := newFakeDatabaseService().withDatabase()
	secrets := &fakeSecretInjector{env: []string{"API_KEY=value-1"}}
	r := service.NewApplicationResources(databases, secrets)

	wiring, err := r.WiringFor(context.Background(), "app-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	env := strings.Join(wiring.Env, "\n")
	if !strings.Contains(env, "DATABASE_URL=") || !strings.Contains(env, "API_KEY=value-1") {
		t.Fatalf("expected both the database and the secret in the environment, got %v", wiring.Env)
	}
	if wiring.NetworkID != "net-test" {
		t.Fatalf("the database's private network was lost in the merge: %q", wiring.NetworkID)
	}
}

func TestApplicationResources_NoDatabaseStillInjectsSecrets(t *testing.T) {
	r := service.NewApplicationResources(newFakeDatabaseService(), &fakeSecretInjector{env: []string{"API_KEY=value-1"}})

	wiring, err := r.WiringFor(context.Background(), "app-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(wiring.Env) != 1 || wiring.Env[0] != "API_KEY=value-1" || wiring.NetworkID != "" {
		t.Fatalf("expected only the secret and no network, got %+v", wiring)
	}
}

// FR-067's exception flow at the seam every start path uses.
func TestApplicationResources_UnreadableSecretFailsClosed(t *testing.T) {
	unreadable := fmt.Errorf("%w: API_KEY: key mismatch", domain.ErrSecretUnreadable)
	r := service.NewApplicationResources(newFakeDatabaseService().withDatabase(), &fakeSecretInjector{err: unreadable})

	wiring, err := r.WiringFor(context.Background(), "app-1")
	if !errors.Is(err, domain.ErrSecretUnreadable) {
		t.Fatalf("expected ErrSecretUnreadable, got %v", err)
	}
	if len(wiring.Env) != 0 || wiring.NetworkID != "" {
		t.Fatalf("a partial wiring came back with the error: %+v", wiring)
	}
}

// Every start path calls WiringFor again; a merge that grew the
// database's slice in place would pile secrets up call after call.
func TestApplicationResources_RepeatedWiringDoesNotAccumulate(t *testing.T) {
	r := service.NewApplicationResources(newFakeDatabaseService().withDatabase(), &fakeSecretInjector{env: []string{"API_KEY=value-1"}})

	first, _ := r.WiringFor(context.Background(), "app-1")
	second, _ := r.WiringFor(context.Background(), "app-1")
	if len(first.Env) != len(second.Env) {
		t.Fatalf("the environment grew between calls: %d then %d", len(first.Env), len(second.Env))
	}
}

func TestApplicationResources_DeprovisionRemovesDatabaseThenSecrets(t *testing.T) {
	databases := newFakeDatabaseService().withDatabase()
	secrets := &fakeSecretInjector{}
	r := service.NewApplicationResources(databases, secrets)

	if err := r.Deprovision(context.Background(), "app-1"); err != nil {
		t.Fatal(err)
	}
	if len(databases.deprovisioned) != 1 || len(secrets.deletedFor) != 1 {
		t.Fatalf("expected both torn down, got databases=%v secrets=%v", databases.deprovisioned, secrets.deletedFor)
	}
}

func TestApplicationResources_FailedDatabaseTeardownKeepsSecretsForTheRetry(t *testing.T) {
	databases := newFakeDatabaseService().withDatabase()
	databases.deprovisionErr = errors.New("docker daemon unreachable")
	secrets := &fakeSecretInjector{}
	r := service.NewApplicationResources(databases, secrets)

	if err := r.Deprovision(context.Background(), "app-1"); err == nil {
		t.Fatal("expected the database failure to surface")
	}
	if len(secrets.deletedFor) != 0 {
		t.Fatalf("secrets were deleted even though the deletion did not complete: %v", secrets.deletedFor)
	}
}
