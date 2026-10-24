package domain

import (
	"context"
	"errors"
	"time"
)

// Dev copies (DEC-029 part 3, migration 0026): an application's long-lived
// development copy, "<name>-dev", its own application with its own database
// and address. Deploying to it never waits for approval; its data stays
// between deployments.

type DevCopy struct {
	ApplicationID string // the copy's own application
	ParentID      string // the application it is the dev copy of
	CreatedBy     string
	CreatedAt     time.Time
	EndedAt       *time.Time
	// TestData: Claude says it put test data in the copy's database itself.
	TestData     bool
	TestDataNote string

	// Filled by reads.
	Name       string
	ParentName string
}

const (
	AuditActionDevCopyCreate AuditAction = "application.dev_copy_create"
	AuditActionDevCopyRemove AuditAction = "application.dev_copy_remove"
	AuditActionDevTestData   AuditAction = "application.dev_test_data"
	AuditActionCopyDevData   AuditAction = "application.copy_dev_data"

	// MaxDevCopyParentName leaves room for "-dev" and a number in a
	// 63-character application name.
	MaxDevCopyParentName = 56
)

var (
	ErrNoDevCopy             = errors.New("this application has no dev copy yet: deploy to dev first")
	ErrDevCopyOfDevCopy      = errors.New("this is a dev copy: deploy the application it belongs to instead")
	ErrDevCopyEnvironment    = errors.New(`a dev copy deploys only to "dev"`)
	ErrDevCopyNameTooLong    = errors.New("the application's name is too long for a dev copy address (56 characters at most)")
	ErrDevCopyParentInactive = errors.New("an archived or deleted application has no dev copy")
	ErrDevDataNotProduction  = errors.New("dev data can only be copied by a production deployment")
	ErrDevDataNotFirst       = errors.New("production has already run and has its own data: dev data is copied only by the first production deployment, so real data is never replaced")
	ErrDevDataNoDatabase     = errors.New("there is no dev copy with a database to copy data from")
	ErrInvalidTestDataNote   = errors.New("say what test data was added (a short note)")
	ErrDevCopyYAMLChanged    = errors.New("the application's deployment.yaml changed since the dev copy first deployed, and a deployed copy can't take a new one: resetting the dev copy (reset_dev) picks it up, but removes the copy's data, so ask the employee first")
)

// DevDataRequest is a production deployment's request to take its data
// from the dev copy.
type DevDataRequest struct {
	Copy bool
}

type devDataKey struct{}

func WithDevDataRequest(ctx context.Context, r DevDataRequest) context.Context {
	return context.WithValue(ctx, devDataKey{}, r)
}

func DevDataRequestFrom(ctx context.Context) DevDataRequest {
	r, _ := ctx.Value(devDataKey{}).(DevDataRequest)
	return r
}
