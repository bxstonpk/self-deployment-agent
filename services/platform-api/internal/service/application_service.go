// Package service implements the business logic for the Draft-state slice:
// FR-011 (register), FR-012 (naming/uniqueness), FR-013 (metadata edit),
// FR-015 (owner assignment) from docs/02_Functional_Requirements.md.
package service

import (
	"context"
	"regexp"
	"strings"
	"time"

	"platform-api/internal/domain"
)

// dnsLabelPattern mirrors the DB CHECK constraint in
// 0001_init_draft_state.sql — kept in sync deliberately so invalid names are
// rejected with a clear error before ever reaching the database.
var dnsLabelPattern = regexp.MustCompile(`^[a-z]([a-z0-9-]{0,61}[a-z0-9])?$`)

// reservedApplicationNames guards names that would collide with platform
// infrastructure once Domain Management (Module P) generates subdomains from
// the application name. This list is intentionally small for v1 and should
// move to IT-governed configuration alongside the Supported Stack catalog
// (Module F) rather than staying hardcoded — see docs/17_Decision_Log.md.
var reservedApplicationNames = map[string]bool{
	"admin": true, "api": true, "www": true, "platform": true,
	"mcp": true, "internal": true, "status": true,
}

type ApplicationRepository interface {
	Create(ctx context.Context, app domain.Application) (domain.Application, error)
	GetByID(ctx context.Context, id string) (domain.Application, error)
	NameExists(ctx context.Context, name string) (bool, error)
	List(ctx context.Context, limit, offset int) ([]domain.Application, error)
	UpdateMetadata(ctx context.Context, id, description string) (domain.Application, error)
}

type ApplicationOwnerRepository interface {
	AssignPrimaryOwner(ctx context.Context, applicationID, userID, assignedBy string) (domain.ApplicationOwner, error)
	ListForApplication(ctx context.Context, applicationID string) ([]domain.ApplicationOwner, error)
}

type DepartmentRepository interface {
	Exists(ctx context.Context, id string) (bool, error)
}

type ApplicationService struct {
	apps        ApplicationRepository
	owners      ApplicationOwnerRepository
	departments DepartmentRepository
	now         func() time.Time
}

func NewApplicationService(apps ApplicationRepository, owners ApplicationOwnerRepository, departments DepartmentRepository) *ApplicationService {
	return &ApplicationService{apps: apps, owners: owners, departments: departments, now: time.Now}
}

type RegisterApplicationInput struct {
	Name                string
	Description         string
	OwningDepartmentID  string
	DeploymentYAMLDraft string
	RegisteredBy        domain.User // the authenticated caller (FR-011 preconditions)
}

// Register implements FR-011 (Register New Application): validates the name
// (FR-012), creates the application in Draft state, and assigns the
// registering employee as the initial primary owner (FR-015 default path).
func (s *ApplicationService) Register(ctx context.Context, in RegisterApplicationInput) (domain.Application, error) {
	name := strings.ToLower(strings.TrimSpace(in.Name))
	if !dnsLabelPattern.MatchString(name) || reservedApplicationNames[name] {
		return domain.Application{}, domain.ErrInvalidName
	}

	taken, err := s.apps.NameExists(ctx, name)
	if err != nil {
		return domain.Application{}, err
	}
	if taken {
		return domain.Application{}, domain.ErrNameTaken
	}

	deptOK, err := s.departments.Exists(ctx, in.OwningDepartmentID)
	if err != nil {
		return domain.Application{}, err
	}
	if !deptOK {
		return domain.Application{}, domain.ErrDepartmentUnknown
	}

	app, err := s.apps.Create(ctx, domain.Application{
		Name:                name,
		Description:         strings.TrimSpace(in.Description),
		OwningDepartmentID:  in.OwningDepartmentID,
		CreatedBy:           in.RegisteredBy.ID,
		LifecycleStatus:     domain.StatusDraft,
		DeploymentYAMLDraft: in.DeploymentYAMLDraft,
	})
	if err != nil {
		return domain.Application{}, err
	}

	// FR-015: registration always leaves the application with exactly one
	// active primary owner — the registering employee, by default.
	if _, err := s.owners.AssignPrimaryOwner(ctx, app.ID, in.RegisteredBy.ID, in.RegisteredBy.ID); err != nil {
		return domain.Application{}, err
	}

	return app, nil
}

func (s *ApplicationService) Get(ctx context.Context, id string) (domain.Application, error) {
	return s.apps.GetByID(ctx, id)
}

func (s *ApplicationService) List(ctx context.Context, limit, offset int) ([]domain.Application, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	return s.apps.List(ctx, limit, offset)
}

// UpdateMetadata implements FR-013: only descriptive metadata may be edited
// this way. It deliberately never touches LifecycleStatus — a
// deployment.yaml content change (Module G) is a separate flow once that
// module exists.
func (s *ApplicationService) UpdateMetadata(ctx context.Context, id, requestingUserID, description string) (domain.Application, error) {
	owners, err := s.owners.ListForApplication(ctx, id)
	if err != nil {
		return domain.Application{}, err
	}
	authorized := false
	for _, o := range owners {
		if o.UserID == requestingUserID && o.Status == "active" {
			authorized = true
			break
		}
	}
	if !authorized {
		return domain.Application{}, domain.ErrUnauthorized
	}
	return s.apps.UpdateMetadata(ctx, id, strings.TrimSpace(description))
}

func (s *ApplicationService) ListOwners(ctx context.Context, applicationID string) ([]domain.ApplicationOwner, error) {
	return s.owners.ListForApplication(ctx, applicationID)
}
