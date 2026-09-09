// Package service implements the business logic for the Draft-state slice:
// FR-011 (register), FR-012 (naming/uniqueness), FR-013 (metadata edit),
// FR-015 (owner assignment) from docs/02_Functional_Requirements.md.
package service

import (
	"context"
	"fmt"
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
	// AddOwner and Revoke implement FR-017 (Co-Owner/Contributor
	// Management) — see application_owner_repo.go's doc comments.
	AddOwner(ctx context.Context, applicationID, userID string, role domain.OwnershipRole, assignedBy string) (domain.ApplicationOwner, error)
	Revoke(ctx context.Context, applicationID, userID string) (int64, error)
	// ReplacePrimaryOwner implements FR-016's accepted-transfer effect —
	// see application_owner_repo.go's doc comment.
	ReplacePrimaryOwner(ctx context.Context, applicationID, oldPrimaryUserID, newPrimaryUserID, assignedBy string) (domain.ApplicationOwner, error)
}

// TransferRepository implements FR-016's pending-transfer bookkeeping —
// see ownership_transfer_repo.go's doc comments.
type TransferRepository interface {
	Create(ctx context.Context, applicationID, fromUserID, toUserID string, expiresAt time.Time) (domain.OwnershipTransfer, error)
	GetByID(ctx context.Context, id string) (domain.OwnershipTransfer, error)
	GetPendingForApplication(ctx context.Context, applicationID string) (domain.OwnershipTransfer, error)
	MarkResolved(ctx context.Context, id string, status domain.TransferStatus) (domain.OwnershipTransfer, error)
}

type DepartmentRepository interface {
	Exists(ctx context.Context, id string) (bool, error)
}

// UserRepository is the narrow seam GrantCoOwner/InitiateTransfer need to
// resolve a target employee's email to their user id — see GetByEmail's
// doc comment for why this must never fall back to provisioning one.
type UserRepository interface {
	GetByEmail(ctx context.Context, email string) (domain.User, error)
}

type ApplicationService struct {
	apps           ApplicationRepository
	owners         ApplicationOwnerRepository
	departments    DepartmentRepository
	users          UserRepository
	transfers      TransferRepository
	notifications  SingleUserNotifier
	transferWindow time.Duration
	audit          AuditRecorder
	now            func() time.Time
}

func NewApplicationService(
	apps ApplicationRepository, owners ApplicationOwnerRepository, departments DepartmentRepository,
	users UserRepository, transfers TransferRepository, notifications SingleUserNotifier, transferWindow time.Duration,
	audit AuditRecorder,
) *ApplicationService {
	return &ApplicationService{
		apps: apps, owners: owners, departments: departments, users: users,
		transfers: transfers, notifications: notifications, transferWindow: transferWindow,
		audit: audit, now: time.Now,
	}
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
func (s *ApplicationService) Register(ctx context.Context, in RegisterApplicationInput) (app domain.Application, err error) {
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

	// Audited from here on: everything above is a validation rejection
	// before any real registration was attempted (FR-103 scope boundary —
	// see audit_service.go's package comment). Named returns + defer let
	// this cover every remaining return path (success and failure) without
	// touching the branching below.
	defer func() {
		outcome := domain.AuditSuccess
		detail := ""
		if err != nil {
			outcome, detail = domain.AuditFailure, err.Error()
		}
		if auditErr := s.audit.Record(ctx, domain.AuditEntry{
			ActorUserID: in.RegisteredBy.ID, Action: domain.AuditActionRegisterApplication,
			ResourceType: "application", ResourceID: app.ID, Outcome: outcome, Detail: detail,
		}); auditErr != nil && err == nil {
			err = fmt.Errorf("application registered but audit trail failed to record: %w", auditErr)
		}
	}()

	app, err = s.apps.Create(ctx, domain.Application{
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

// requirePrimaryOwner implements FR-017's precondition ("requester is the
// primary Application Owner") — stricter than the plain "any active owner"
// check every other service's requireOwner uses for day-to-day actions.
func (s *ApplicationService) requirePrimaryOwner(ctx context.Context, applicationID, userID string) error {
	owners, err := s.owners.ListForApplication(ctx, applicationID)
	if err != nil {
		return err
	}
	for _, o := range owners {
		if o.Status == "active" && o.OwnershipRole == domain.OwnerRolePrimary && o.UserID == userID {
			return nil
		}
	}
	return domain.ErrNotPrimaryOwner
}

func validCoOwnerRole(role domain.OwnershipRole) bool {
	return role == domain.OwnerRoleSecondary || role == domain.OwnerRoleTechnical
}

// GrantCoOwner implements FR-017's main flow: the primary owner grants
// co-owner (domain.OwnerRoleSecondary) or contributor
// (domain.OwnerRoleTechnical) access to another employee, scoped to this
// application only. Granting the same role to the same person twice (e.g.
// re-granting after a prior revoke) is idempotent, not an error — see
// AddOwner's doc comment.
//
// Once granted, a co-owner/contributor automatically gains day-to-day
// configuration and deployment access: every other service's requireOwner
// check already accepts any *active* owner row regardless of
// OwnershipRole, exactly matching FR-017's business rule ("co-owners may
// perform day-to-day configuration and deployment actions") with no
// further code changes needed there. Only actions FR-016/FR-017
// themselves reserve for the primary owner — granting/revoking access,
// and (once built) ownership transfer — check requirePrimaryOwner instead.
func (s *ApplicationService) GrantCoOwner(ctx context.Context, applicationID, requesterID, targetEmail string, role domain.OwnershipRole) (owner domain.ApplicationOwner, err error) {
	if !validCoOwnerRole(role) {
		return domain.ApplicationOwner{}, domain.ErrInvalidOwnershipRole
	}
	if _, err := s.apps.GetByID(ctx, applicationID); err != nil {
		return domain.ApplicationOwner{}, err
	}
	if err := s.requirePrimaryOwner(ctx, applicationID, requesterID); err != nil {
		return domain.ApplicationOwner{}, err
	}
	target, err := s.users.GetByEmail(ctx, targetEmail)
	if err != nil {
		return domain.ApplicationOwner{}, err
	}
	if target.Status != "active" {
		return domain.ApplicationOwner{}, domain.ErrTargetUserInactive
	}

	defer func() {
		outcome := domain.AuditSuccess
		detail := fmt.Sprintf("granted %s to %s", role, targetEmail)
		if err != nil {
			outcome, detail = domain.AuditFailure, err.Error()
		}
		if auditErr := s.audit.Record(ctx, domain.AuditEntry{
			ActorUserID: requesterID, Action: domain.AuditActionGrantOwner,
			ResourceType: "application", ResourceID: applicationID, Outcome: outcome, Detail: detail,
		}); auditErr != nil && err == nil {
			err = fmt.Errorf("owner granted but audit trail failed to record: %w", auditErr)
		}
	}()

	owner, err = s.owners.AddOwner(ctx, applicationID, target.ID, role, requesterID)
	return owner, err
}

// RevokeCoOwner implements FR-017's alternative flow: the primary owner
// removes a previously-granted co-owner/contributor, immediately revoking
// their application-scoped access (every requireOwner check re-derives
// active status on every call, so there is no separate "sign them out"
// step). Never revokes the primary owner themselves — Revoke's own WHERE
// clause excludes that role; see application_owner_repo.go.
func (s *ApplicationService) RevokeCoOwner(ctx context.Context, applicationID, requesterID, targetUserID string) (err error) {
	if _, err := s.apps.GetByID(ctx, applicationID); err != nil {
		return err
	}
	if err := s.requirePrimaryOwner(ctx, applicationID, requesterID); err != nil {
		return err
	}

	defer func() {
		outcome := domain.AuditSuccess
		detail := ""
		if err != nil {
			outcome, detail = domain.AuditFailure, err.Error()
		}
		if auditErr := s.audit.Record(ctx, domain.AuditEntry{
			ActorUserID: requesterID, Action: domain.AuditActionRevokeOwner,
			ResourceType: "application", ResourceID: applicationID, Outcome: outcome, Detail: detail,
		}); auditErr != nil && err == nil {
			err = fmt.Errorf("owner revoked but audit trail failed to record: %w", auditErr)
		}
	}()

	revoked, err := s.owners.Revoke(ctx, applicationID, targetUserID)
	if err != nil {
		return err
	}
	if revoked == 0 {
		return domain.ErrCoOwnerGrantNotFound
	}
	return nil
}

// --- FR-016: Transfer Application Ownership ---
//
// Only the owner-initiated, nominee-accepted main flow is implemented —
// see internal/domain/ownership_transfer.go's package comment for the
// admin-forced-transfer scope adaptation.

// InitiateTransfer implements FR-016's main flow steps 1-2: the current
// primary owner nominates a new one, who is notified (Module X) and must
// accept before anything actually changes — this method alone never
// touches application_owners.
func (s *ApplicationService) InitiateTransfer(ctx context.Context, applicationID, requesterID, targetEmail string) (transfer domain.OwnershipTransfer, err error) {
	if _, err := s.apps.GetByID(ctx, applicationID); err != nil {
		return domain.OwnershipTransfer{}, err
	}
	if err := s.requirePrimaryOwner(ctx, applicationID, requesterID); err != nil {
		return domain.OwnershipTransfer{}, err
	}
	target, err := s.users.GetByEmail(ctx, targetEmail)
	if err != nil {
		return domain.OwnershipTransfer{}, err
	}
	if target.Status != "active" {
		return domain.OwnershipTransfer{}, domain.ErrTargetUserInactive
	}

	defer func() {
		outcome := domain.AuditSuccess
		detail := fmt.Sprintf("nominated %s", targetEmail)
		if err != nil {
			outcome, detail = domain.AuditFailure, err.Error()
		}
		if auditErr := s.audit.Record(ctx, domain.AuditEntry{
			ActorUserID: requesterID, Action: domain.AuditActionInitiateTransfer,
			ResourceType: "application", ResourceID: applicationID, Outcome: outcome, Detail: detail,
		}); auditErr != nil && err == nil {
			err = fmt.Errorf("transfer initiated but audit trail failed to record: %w", auditErr)
		}
	}()

	transfer, err = s.transfers.Create(ctx, applicationID, requesterID, target.ID, s.now().Add(s.transferWindow))
	if err != nil {
		return domain.OwnershipTransfer{}, err
	}

	// FR-016 main flow step 2. Best-effort by design — see
	// NotificationService.NotifyUser's doc comment.
	s.notifications.NotifyUser(ctx, target.ID, domain.NotificationOwnershipTransfer,
		"You've been nominated as the new owner of an application",
		fmt.Sprintf("Accept or let it expire: POST /ownership-transfers/%s/accept", transfer.ID),
		"application", applicationID)

	return transfer, nil
}

// GetPendingTransfer implements the read side needed to show "a transfer
// is awaiting acceptance" — e.g. so a UI can render it. Not owner-gated:
// same visibility level as ListOwners, which any caller can already read.
func (s *ApplicationService) GetPendingTransfer(ctx context.Context, applicationID string) (domain.OwnershipTransfer, error) {
	return s.transfers.GetPendingForApplication(ctx, applicationID)
}

// AcceptTransfer implements FR-016's main flow steps 3-4: only the
// nominated new owner may accept, and only within the policy window
// (checked lazily here — no background sweeper marks an expired transfer
// until someone actually tries to act on it, see
// internal/domain/ownership_transfer.go's package comment). On success,
// the prior owner's row is revoked (retained for audit history) and the
// nominee becomes the new active primary owner.
func (s *ApplicationService) AcceptTransfer(ctx context.Context, transferID, accepterID string) (owner domain.ApplicationOwner, err error) {
	transfer, err := s.transfers.GetByID(ctx, transferID)
	if err != nil {
		return domain.ApplicationOwner{}, err
	}
	if transfer.Status != domain.TransferPending {
		return domain.ApplicationOwner{}, domain.ErrTransferNotPending
	}
	if accepterID != transfer.ToUserID {
		return domain.ApplicationOwner{}, domain.ErrNotTransferNominee
	}
	if s.now().After(transfer.ExpiresAt) {
		if _, expireErr := s.transfers.MarkResolved(ctx, transferID, domain.TransferExpired); expireErr != nil {
			return domain.ApplicationOwner{}, expireErr
		}
		return domain.ApplicationOwner{}, domain.ErrTransferExpired
	}

	defer func() {
		outcome := domain.AuditSuccess
		detail := ""
		if err != nil {
			outcome, detail = domain.AuditFailure, err.Error()
		}
		if auditErr := s.audit.Record(ctx, domain.AuditEntry{
			ActorUserID: accepterID, Action: domain.AuditActionAcceptTransfer,
			ResourceType: "application", ResourceID: transfer.ApplicationID, Outcome: outcome, Detail: detail,
		}); auditErr != nil && err == nil {
			err = fmt.Errorf("transfer accepted but audit trail failed to record: %w", auditErr)
		}
	}()

	if _, err := s.transfers.MarkResolved(ctx, transferID, domain.TransferAccepted); err != nil {
		return domain.ApplicationOwner{}, err
	}
	owner, err = s.owners.ReplacePrimaryOwner(ctx, transfer.ApplicationID, transfer.FromUserID, transfer.ToUserID, transfer.FromUserID)
	return owner, err
}
