package service_test

import (
	"context"

	"platform-api/internal/domain"
	"platform-api/internal/service"
)

// fakeDatabaseService stands in for the ApplicationResources seam (Modules
// N and O) across every other service's tests, and for Module N inside
// ApplicationResources' own tests. Callers need to know whether the wiring
// was requested and handed to the container, not to re-exercise
// database_service.go or secret_service.go — their own tests do that.
//
// Zero value = "this application declares no database", which is the case
// for every pre-existing test, so they keep passing unchanged.
type fakeDatabaseService struct {
	wiring         service.RuntimeWiring
	provisioned    []string // application ids EnsureProvisioned was called for
	deprovisioned  []string // application ids Deprovision was called for
	provisionErr   error
	wiringErr      error
	deprovisionErr error
}

func newFakeDatabaseService() *fakeDatabaseService {
	return &fakeDatabaseService{}
}

// withDatabase makes the fake behave as though the application has a
// provisioned database, so a test can assert the wiring reaches a
// container.
func (f *fakeDatabaseService) withDatabase() *fakeDatabaseService {
	f.wiring = service.RuntimeWiring{
		Env:       []string{"DATABASE_URL=postgres://appuser:secret@platform-db-test:5432/appdb?sslmode=disable"},
		NetworkID: "net-test",
	}
	return f
}

func (f *fakeDatabaseService) EnsureProvisioned(ctx context.Context, app domain.Application, declaredType string) (domain.ProvisionedDatabase, error) {
	f.provisioned = append(f.provisioned, app.ID)
	if f.provisionErr != nil {
		return domain.ProvisionedDatabase{}, f.provisionErr
	}
	return domain.ProvisionedDatabase{ApplicationID: app.ID, Engine: declaredType}, nil
}

func (f *fakeDatabaseService) WiringFor(ctx context.Context, applicationID string) (service.RuntimeWiring, error) {
	if f.wiringErr != nil {
		return service.RuntimeWiring{}, f.wiringErr
	}
	return f.wiring, nil
}

func (f *fakeDatabaseService) Deprovision(ctx context.Context, applicationID string) error {
	f.deprovisioned = append(f.deprovisioned, applicationID)
	return f.deprovisionErr
}
