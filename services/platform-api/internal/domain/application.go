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
	ErrNotFound          = errors.New("not found")
	ErrNameTaken         = errors.New("application name already registered")
	ErrInvalidName       = errors.New("application name must be a valid DNS label (lowercase letters, digits, hyphens; must start with a letter)")
	ErrDepartmentUnknown = errors.New("owning department does not exist")
	ErrUnauthorized      = errors.New("requester is not authorized to perform this action")
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
