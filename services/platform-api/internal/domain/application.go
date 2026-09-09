// Package domain holds the core types for the Draft-state slice of the
// Application Registry (docs/06_System_Requirements.md, MOD-02) and
// Identity & Access (MOD-01). It intentionally covers only what the current
// Application Lifecycle state needs — see docs/12_Data_Requirements.md for
// the full entity catalog this will grow into.
package domain

import (
	"errors"
	"time"
)

type LifecycleStatus string

// Fixed Application Lifecycle, per docs/05_Process_Flows.md and
// docs/02_Functional_Requirements.md Module K.
const (
	StatusDraft      LifecycleStatus = "draft"
	StatusValidated  LifecycleStatus = "validated"
	StatusBuild      LifecycleStatus = "build"
	StatusDeploying  LifecycleStatus = "deploying"
	StatusRunning    LifecycleStatus = "running"
	StatusSuspended  LifecycleStatus = "suspended"
	StatusFailed     LifecycleStatus = "failed"
	StatusRolledBack LifecycleStatus = "rolled_back"
	StatusArchived   LifecycleStatus = "archived"
	StatusDeleted    LifecycleStatus = "deleted"
)

var (
	ErrNotFound                   = errors.New("not found")
	ErrNameTaken                  = errors.New("application name already registered")
	ErrInvalidName                = errors.New("application name must be a valid DNS label (lowercase letters, digits, hyphens; must start with a letter)")
	ErrDepartmentUnknown          = errors.New("owning department does not exist")
	ErrUnauthorized               = errors.New("requester is not authorized to perform this action")
	ErrInvalidLifecycleTransition = errors.New("application is not in a state this operation can be performed from")
	ErrNoDeploymentYAML           = errors.New("application has no deployment.yaml draft to validate")
	ErrInvalidYAML                = errors.New("deployment.yaml is not syntactically valid YAML")
	// ErrNotPrimaryOwner implements FR-017's precondition ("requester is the
	// primary Application Owner") — stricter than ErrUnauthorized, which
	// covers "not an owner at all" for day-to-day actions any active owner
	// (including a co-owner/contributor) may perform.
	ErrNotPrimaryOwner = errors.New("only the application's primary owner may perform this action")
	// ErrTargetUserUnknown implements FR-017's implicit precondition that a
	// co-owner/contributor grant target must already be a known platform
	// user — this platform has no "invite by email" provisioning flow, only
	// dev-auth's self-service upsert-on-first-request (see GetOrCreateByEmail's
	// doc comment); granting access to someone who has never signed in
	// themselves would otherwise silently create a phantom account.
	ErrTargetUserUnknown = errors.New("target user has not signed in to the platform yet")
	// ErrTargetUserInactive implements FR-017's exception flow ("grant to an
	// inactive/deactivated user is rejected").
	ErrTargetUserInactive = errors.New("target user is not active")
	// ErrInvalidOwnershipRole guards GrantCoOwner's role parameter: only
	// "secondary" (co-owner) or "technical" (contributor) are grantable
	// this way — "primary" is never granted, only assigned at registration
	// or (once built) transferred via FR-016.
	ErrInvalidOwnershipRole = errors.New("ownership_role must be \"secondary\" (co-owner) or \"technical\" (contributor)")
	// ErrCoOwnerGrantNotFound is RevokeCoOwner's distinct "nothing to
	// revoke" outcome — kept separate from the generic ErrNotFound (which
	// maps to "application not found" in the HTTP layer) since the
	// application itself is not in question here, only whether this
	// specific user currently holds an active non-primary grant on it.
	ErrCoOwnerGrantNotFound = errors.New("no active co-owner/contributor grant found for that user on this application")
)

type Department struct {
	ID             string
	Name           string
	CostCenterCode string
	Status         string
	CreatedAt      time.Time
}

type User struct {
	ID           string
	FullName     string
	Email        string
	DepartmentID string
	Status       string
	CreatedAt    time.Time
}

type Application struct {
	ID                  string
	Name                string
	Description         string
	OwningDepartmentID  string
	CreatedBy           string
	LifecycleStatus     LifecycleStatus
	DeploymentYAMLDraft string
	CreatedAt           time.Time
	UpdatedAt           time.Time
	ValidatedAt         *time.Time
}

type OwnershipRole string

const (
	OwnerRolePrimary   OwnershipRole = "primary"
	OwnerRoleSecondary OwnershipRole = "secondary"
	OwnerRoleTechnical OwnershipRole = "technical"
)

type ApplicationOwner struct {
	ID            string
	ApplicationID string
	UserID        string
	OwnershipRole OwnershipRole
	AssignedBy    string
	AssignedAt    time.Time
	Status        string
}
