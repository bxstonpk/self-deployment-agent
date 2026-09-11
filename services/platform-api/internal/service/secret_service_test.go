package service_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"platform-api/internal/domain"
	"platform-api/internal/secretbox"
	"platform-api/internal/service"
)

// fakeSecretRepo mirrors application_secrets, including the two guards the
// real table enforces: one value per (application, name), and a write can
// only replace a secret with the same manager.
type fakeSecretRepo struct {
	rows map[string]domain.ApplicationSecret
	seq  int
}

func newFakeSecretRepo() *fakeSecretRepo {
	return &fakeSecretRepo{rows: map[string]domain.ApplicationSecret{}}
}

func secretKey(applicationID, name string) string { return applicationID + "/" + name }

func (f *fakeSecretRepo) Upsert(ctx context.Context, s domain.ApplicationSecret) (domain.ApplicationSecret, error) {
	k := secretKey(s.ApplicationID, s.Name)
	if existing, ok := f.rows[k]; ok {
		if existing.ManagedBy != s.ManagedBy {
			return domain.ApplicationSecret{}, domain.ErrSecretManagedByPlatform
		}
		existing.Ciphertext, existing.KeyID = s.Ciphertext, s.KeyID
		existing.Version++
		existing.UpdatedBy, existing.UpdatedAt = s.UpdatedBy, time.Now()
		f.rows[k] = existing
		return existing, nil
	}
	f.seq++
	s.ID = fmt.Sprintf("secret-%d", f.seq)
	s.Version = 1
	s.CreatedAt, s.UpdatedAt = time.Now(), time.Now()
	f.rows[k] = s
	return s, nil
}

func (f *fakeSecretRepo) Get(ctx context.Context, applicationID, name string) (domain.ApplicationSecret, error) {
	s, ok := f.rows[secretKey(applicationID, name)]
	if !ok {
		return domain.ApplicationSecret{}, domain.ErrSecretNotFound
	}
	return s, nil
}

func (f *fakeSecretRepo) ListForApplication(ctx context.Context, applicationID string) ([]domain.ApplicationSecret, error) {
	var out []domain.ApplicationSecret
	for _, s := range f.rows {
		if s.ApplicationID == applicationID {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (f *fakeSecretRepo) Delete(ctx context.Context, applicationID, name string, managedBy domain.SecretManagedBy) (bool, error) {
	k := secretKey(applicationID, name)
	if s, ok := f.rows[k]; ok && s.ManagedBy == managedBy {
		delete(f.rows, k)
		return true, nil
	}
	return false, nil
}

func (f *fakeSecretRepo) DeleteAllForApplication(ctx context.Context, applicationID string) (int64, error) {
	var n int64
	for k, s := range f.rows {
		if s.ApplicationID == applicationID {
			delete(f.rows, k)
			n++
		}
	}
	return n, nil
}

func newTestBox(t *testing.T) *secretbox.Box {
	t.Helper()
	key := make([]byte, secretbox.KeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	b, err := secretbox.New(key)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

const apiKeyValue = "sk-live-7c3e9a1f5b2d8e4a6c0f3b9d1e7a5c2f"

type secretFixture struct {
	svc   *service.SecretService
	repo  *fakeSecretRepo
	audit *fakeAuditRecorder
	apps  *fakeLifecycleRepo
	owner *fakeOwnerRepo
}

// Two applications with different owners, so every test can check that
// one never reaches the other.
func newSecretFixture(t *testing.T) secretFixture {
	t.Helper()
	apps := newFakeLifecycleRepo(
		domain.Application{ID: "app-1", Name: "leave-tracker", LifecycleStatus: domain.StatusRunning},
		domain.Application{ID: "app-2", Name: "overtime", LifecycleStatus: domain.StatusRunning},
	)
	owners := newFakeOwnerRepo()
	owners.owners["app-1"] = []domain.ApplicationOwner{{ApplicationID: "app-1", UserID: "owner-1", OwnershipRole: domain.OwnerRolePrimary, Status: "active"}}
	owners.owners["app-2"] = []domain.ApplicationOwner{{ApplicationID: "app-2", UserID: "owner-2", OwnershipRole: domain.OwnerRolePrimary, Status: "active"}}
	repo := newFakeSecretRepo()
	audit := newFakeAuditRecorder()
	return secretFixture{
		svc:  service.NewSecretService(apps, owners, repo, newTestBox(t), audit),
		repo: repo, audit: audit, apps: apps, owner: owners,
	}
}

func TestSetSecret_StoresOnlyCiphertext(t *testing.T) {
	f := newSecretFixture(t)

	meta, err := f.svc.Set(context.Background(), "app-1", "owner-1", "API_KEY", apiKeyValue)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.Name != "API_KEY" || meta.Version != 1 || meta.ManagedBy != domain.SecretManagedByEmployee {
		t.Fatalf("unexpected metadata: %+v", meta)
	}

	row := f.repo.rows[secretKey("app-1", "API_KEY")]
	if len(row.Ciphertext) == 0 || row.KeyID == "" {
		t.Fatalf("expected a ciphertext and key id to be stored, got %+v", row)
	}
	if bytes.Contains(row.Ciphertext, []byte(apiKeyValue)) {
		t.Fatal("the stored ciphertext contains the plaintext value")
	}
}

func TestSetSecret_NonOwnerRejectedAndNothingStored(t *testing.T) {
	f := newSecretFixture(t)

	// owner-2 owns a different application — being an owner of something
	// is not enough.
	_, err := f.svc.Set(context.Background(), "app-1", "owner-2", "API_KEY", apiKeyValue)
	if !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
	if len(f.repo.rows) != 0 {
		t.Fatalf("a rejected write was stored: %v", f.repo.rows)
	}
}

func TestSetSecret_RejectsReservedAndInvalidNames(t *testing.T) {
	f := newSecretFixture(t)
	cases := map[string]error{
		"DATABASE_URL":                domain.ErrReservedSecretName,
		"DATABASE_PASSWORD":           domain.ErrReservedSecretName,
		"PLATFORM_ANYTHING":           domain.ErrReservedSecretName,
		"api_key":                     domain.ErrInvalidSecretName,
		"1KEY":                        domain.ErrInvalidSecretName,
		"API-KEY":                     domain.ErrInvalidSecretName,
		"":                            domain.ErrInvalidSecretName,
		"A" + strings.Repeat("B", 64): domain.ErrInvalidSecretName,
	}
	for name, want := range cases {
		if _, err := f.svc.Set(context.Background(), "app-1", "owner-1", name, apiKeyValue); !errors.Is(err, want) {
			t.Errorf("name %q: expected %v, got %v", name, want, err)
		}
	}
	if len(f.repo.rows) != 0 {
		t.Fatalf("an invalid name was stored: %v", f.repo.rows)
	}
}

func TestSetSecret_RejectsInvalidValues(t *testing.T) {
	f := newSecretFixture(t)
	for _, value := range []string{"", "has\x00nul", strings.Repeat("x", domain.MaxSecretValueBytes+1)} {
		if _, err := f.svc.Set(context.Background(), "app-1", "owner-1", "API_KEY", value); !errors.Is(err, domain.ErrInvalidSecretValue) {
			t.Errorf("value of length %d: expected ErrInvalidSecretValue, got %v", len(value), err)
		}
	}
}

func TestSetSecret_ReplacingBumpsVersionAndInjectsTheNewValue(t *testing.T) {
	f := newSecretFixture(t)
	ctx := context.Background()

	if _, err := f.svc.Set(ctx, "app-1", "owner-1", "API_KEY", "first-value"); err != nil {
		t.Fatal(err)
	}
	meta, err := f.svc.Set(ctx, "app-1", "owner-1", "API_KEY", "second-value")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Version != 2 {
		t.Fatalf("expected version 2 after a replace, got %d", meta.Version)
	}
	env, err := f.svc.EnvFor(ctx, "app-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(env) != 1 || env[0] != "API_KEY=second-value" {
		t.Fatalf("expected only the new value to be injected, got %v", env)
	}
}

func TestSetSecret_DeletedApplicationRejected(t *testing.T) {
	f := newSecretFixture(t)
	app := f.apps.apps["app-1"]
	app.LifecycleStatus = domain.StatusDeleted
	f.apps.apps["app-1"] = app

	if _, err := f.svc.Set(context.Background(), "app-1", "owner-1", "API_KEY", apiKeyValue); !errors.Is(err, domain.ErrInvalidLifecycleTransition) {
		t.Fatalf("expected ErrInvalidLifecycleTransition, got %v", err)
	}
}

func TestListSecrets_MetadataOnly_IncludingPlatformManaged(t *testing.T) {
	f := newSecretFixture(t)
	ctx := context.Background()
	if _, err := f.svc.Set(ctx, "app-1", "owner-1", "API_KEY", apiKeyValue); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.PutManaged(ctx, "app-1", domain.DatabasePasswordSecret, "generated-db-password"); err != nil {
		t.Fatal(err)
	}

	list, err := f.svc.List(ctx, "app-1", "owner-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("expected both secrets listed, got %+v", list)
	}
	byName := map[string]domain.SecretMetadata{}
	for _, m := range list {
		byName[m.Name] = m
	}
	if byName[domain.DatabasePasswordSecret].ManagedBy != domain.SecretManagedByPlatform {
		t.Fatalf("the database password should be listed as platform-managed: %+v", byName)
	}
	// SecretMetadata has no value field at all; this guards against one
	// being added and populated later.
	rendered := fmt.Sprintf("%+v", list)
	if strings.Contains(rendered, apiKeyValue) || strings.Contains(rendered, "generated-db-password") {
		t.Fatalf("a listing exposed a value: %s", rendered)
	}
}

func TestListSecrets_AnotherApplicationsOwnerRejected(t *testing.T) {
	f := newSecretFixture(t)
	if _, err := f.svc.List(context.Background(), "app-1", "owner-2"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
}

func TestDeleteSecret_PlatformManagedRefused(t *testing.T) {
	f := newSecretFixture(t)
	ctx := context.Background()
	if err := f.svc.PutManaged(ctx, "app-1", domain.DatabasePasswordSecret, "generated-db-password"); err != nil {
		t.Fatal(err)
	}

	if err := f.svc.Delete(ctx, "app-1", "owner-1", domain.DatabasePasswordSecret); !errors.Is(err, domain.ErrSecretManagedByPlatform) {
		t.Fatalf("expected ErrSecretManagedByPlatform, got %v", err)
	}
	if _, ok := f.repo.rows[secretKey("app-1", domain.DatabasePasswordSecret)]; !ok {
		t.Fatal("the platform-managed secret was deleted anyway")
	}
}

func TestDeleteSecret_RemovesItAndItIsNoLongerInjected(t *testing.T) {
	f := newSecretFixture(t)
	ctx := context.Background()
	if _, err := f.svc.Set(ctx, "app-1", "owner-1", "API_KEY", apiKeyValue); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Delete(ctx, "app-1", "owner-1", "API_KEY"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	env, _ := f.svc.EnvFor(ctx, "app-1")
	if len(env) != 0 {
		t.Fatalf("a deleted secret is still injected: %v", env)
	}
	if err := f.svc.Delete(ctx, "app-1", "owner-1", "API_KEY"); !errors.Is(err, domain.ErrSecretNotFound) {
		t.Fatalf("expected ErrSecretNotFound on a second delete, got %v", err)
	}
}

func TestEnvFor_InjectsEmployeeSecretsOnly(t *testing.T) {
	f := newSecretFixture(t)
	ctx := context.Background()
	if _, err := f.svc.Set(ctx, "app-1", "owner-1", "API_KEY", apiKeyValue); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.PutManaged(ctx, "app-1", domain.DatabasePasswordSecret, "generated-db-password"); err != nil {
		t.Fatal(err)
	}

	env, err := f.svc.EnvFor(ctx, "app-1")
	if err != nil {
		t.Fatal(err)
	}
	// Module N injects its own DATABASE_PASSWORD (and builds DATABASE_URL
	// from it); a second copy from here would be a duplicate at best.
	if len(env) != 1 || env[0] != "API_KEY="+apiKeyValue {
		t.Fatalf("expected exactly the employee secret, got %v", env)
	}
}

// FR-069 as a property of the store, not just of the queries: even with
// direct write access to the table, moving a ciphertext into another
// application's row gets that application nothing but a failed start.
func TestEnvFor_CiphertextMovedToAnotherApplicationFailsClosed(t *testing.T) {
	f := newSecretFixture(t)
	ctx := context.Background()
	if _, err := f.svc.Set(ctx, "app-1", "owner-1", "API_KEY", apiKeyValue); err != nil {
		t.Fatal(err)
	}
	stolen := f.repo.rows[secretKey("app-1", "API_KEY")]
	stolen.ApplicationID = "app-2"
	f.repo.rows[secretKey("app-2", "API_KEY")] = stolen

	env, err := f.svc.EnvFor(ctx, "app-2")
	if !errors.Is(err, domain.ErrSecretUnreadable) {
		t.Fatalf("expected ErrSecretUnreadable, got %v (env %v)", err, env)
	}
	if env != nil {
		t.Fatalf("a partial environment was returned alongside the error: %v", env)
	}
}

// A changed PLATFORM_SECRET_KEY must stop applications starting, not start
// them without their credentials.
func TestEnvFor_KeyChangedFailsClosed(t *testing.T) {
	f := newSecretFixture(t)
	ctx := context.Background()
	if _, err := f.svc.Set(ctx, "app-1", "owner-1", "API_KEY", apiKeyValue); err != nil {
		t.Fatal(err)
	}
	rekeyed := service.NewSecretService(f.apps, f.owner, f.repo, newTestBox(t), f.audit)

	if _, err := rekeyed.EnvFor(ctx, "app-1"); !errors.Is(err, domain.ErrSecretUnreadable) {
		t.Fatalf("expected ErrSecretUnreadable under a different key, got %v", err)
	}
}

func TestManagedValue_RoundTripsAndRefusesEmployeeSecrets(t *testing.T) {
	f := newSecretFixture(t)
	ctx := context.Background()
	if err := f.svc.PutManaged(ctx, "app-1", domain.DatabasePasswordSecret, "generated-db-password"); err != nil {
		t.Fatal(err)
	}
	got, err := f.svc.ManagedValue(ctx, "app-1", domain.DatabasePasswordSecret)
	if err != nil || got != "generated-db-password" {
		t.Fatalf("expected the managed value back, got %q, %v", got, err)
	}

	if _, err := f.svc.Set(ctx, "app-1", "owner-1", "API_KEY", apiKeyValue); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ManagedValue(ctx, "app-1", "API_KEY"); !errors.Is(err, domain.ErrSecretNotFound) {
		t.Fatalf("ManagedValue must not read an employee secret, got %v", err)
	}
}

func TestAudit_RecordsSetAndDelete_NeverTheValue(t *testing.T) {
	f := newSecretFixture(t)
	ctx := context.Background()
	if _, err := f.svc.Set(ctx, "app-1", "owner-1", "API_KEY", apiKeyValue); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Delete(ctx, "app-1", "owner-1", "API_KEY"); err != nil {
		t.Fatal(err)
	}

	entries := f.audit.all()
	if len(entries) != 2 || entries[0].Action != domain.AuditActionSetSecret || entries[1].Action != domain.AuditActionDeleteSecret {
		t.Fatalf("expected set then delete audited, got %+v", entries)
	}
	for _, e := range entries {
		if strings.Contains(e.Detail, apiKeyValue) {
			t.Fatalf("an audit entry contains the secret value: %q", e.Detail)
		}
		if e.ResourceType != "application" || e.ResourceID != "app-1" || e.ActorUserID != "owner-1" {
			t.Fatalf("audit entry not attributed to the owner and application: %+v", e)
		}
	}
}

func TestDeleteAllForApplication_LeavesOtherApplicationsAlone(t *testing.T) {
	f := newSecretFixture(t)
	ctx := context.Background()
	if _, err := f.svc.Set(ctx, "app-1", "owner-1", "API_KEY", apiKeyValue); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.PutManaged(ctx, "app-1", domain.DatabasePasswordSecret, "generated-db-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Set(ctx, "app-2", "owner-2", "API_KEY", "app-2-value"); err != nil {
		t.Fatal(err)
	}

	if err := f.svc.DeleteAllForApplication(ctx, "app-1"); err != nil {
		t.Fatal(err)
	}
	if left, _ := f.repo.ListForApplication(ctx, "app-1"); len(left) != 0 {
		t.Fatalf("secrets survived their application's deletion: %+v", left)
	}
	if left, _ := f.repo.ListForApplication(ctx, "app-2"); len(left) != 1 {
		t.Fatalf("another application's secrets were touched: %+v", left)
	}
}
