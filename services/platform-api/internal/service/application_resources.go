package service

import (
	"context"
	"fmt"

	"platform-api/internal/domain"
)

// DatabaseLifecycle is Module N as ApplicationResources needs it.
type DatabaseLifecycle interface {
	EnsureProvisioned(ctx context.Context, app domain.Application, declaredType string) (domain.ProvisionedDatabase, error)
	WiringFor(ctx context.Context, applicationID string) (RuntimeWiring, error)
	Deprovision(ctx context.Context, applicationID string) error
}

// SecretInjector is Module O as ApplicationResources needs it.
type SecretInjector interface {
	EnvFor(ctx context.Context, applicationID string) ([]string, error)
	DeleteAllForApplication(ctx context.Context, applicationID string) error
}

// ApplicationResources is everything the platform provides an application
// beyond its image — its database (Module N) and its secrets (Module O) —
// behind the one seam every container-starting path already uses. Deploy,
// resume, restart and scale-to-zero cold start each call WiringFor once and
// hand the result straight to the runtime, so adding Module O did not add
// a fifth call site to get wrong (see database_wiring_test.go for why that
// matters).
type ApplicationResources struct {
	databases DatabaseLifecycle
	secrets   SecretInjector
}

func NewApplicationResources(databases DatabaseLifecycle, secrets SecretInjector) *ApplicationResources {
	return &ApplicationResources{databases: databases, secrets: secrets}
}

func (r *ApplicationResources) EnsureProvisioned(ctx context.Context, app domain.Application, declaredType string) (domain.ProvisionedDatabase, error) {
	return r.databases.EnsureProvisioned(ctx, app, declaredType)
}

// WiringFor merges the database's connection environment with the
// application's secrets. Fails closed if either can't be resolved: a
// container never starts with part of what it was promised.
func (r *ApplicationResources) WiringFor(ctx context.Context, applicationID string) (RuntimeWiring, error) {
	wiring, err := r.databases.WiringFor(ctx, applicationID)
	if err != nil {
		return RuntimeWiring{}, err
	}
	secretEnv, err := r.secrets.EnvFor(ctx, applicationID)
	if err != nil {
		return RuntimeWiring{}, fmt.Errorf("resolve application secrets: %w", err)
	}
	// A fresh slice rather than appending in place: the database's Env isn't
	// this function's to grow.
	env := make([]string, 0, len(wiring.Env)+len(secretEnv))
	env = append(env, wiring.Env...)
	env = append(env, secretEnv...)
	wiring.Env = env
	return wiring, nil
}

// Deprovision implements FR-050's resource teardown on Delete: the
// database first (FR-065), then every secret. If the database teardown
// fails, the secrets are left for the retry rather than half the
// application's resources going.
func (r *ApplicationResources) Deprovision(ctx context.Context, applicationID string) error {
	if err := r.databases.Deprovision(ctx, applicationID); err != nil {
		return err
	}
	if err := r.secrets.DeleteAllForApplication(ctx, applicationID); err != nil {
		return fmt.Errorf("delete application secrets: %w", err)
	}
	return nil
}
