// Development governance (the platform owner, 2026-10-24): the company
// standard from the moment an application is started, not only when it is
// deployed.
//
//   - Starting an application is registering it with a profile (what it is
//     for, who uses it, whether it handles personal data, its stack). Not a
//     request for permission: nobody approves it. A build needs the profile.
//   - The standard is published (GET /standards): the supported stacks, the
//     rules in words, and the libraries the company doesn't allow.
//   - Stepping outside it is an exception the owner asks for and a platform
//     administrator decides. An approved dependency exception lets that
//     library through the build's check; stack and other exceptions are a
//     recorded decision, not something the platform enforces by itself.
package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"platform-api/internal/domain"
)

type GovernanceRepository interface {
	GetProfile(ctx context.Context, applicationID string) (domain.ApplicationProfile, error)
	PutProfile(ctx context.Context, p domain.ApplicationProfile) (domain.ApplicationProfile, error)

	ListDependencyRules(ctx context.Context) ([]domain.DependencyRule, error)
	AddDependencyRule(ctx context.Context, r domain.DependencyRule) (domain.DependencyRule, error)
	DeleteDependencyRule(ctx context.Context, id string) (domain.DependencyRule, error)

	CreateException(ctx context.Context, e domain.StandardException) (domain.StandardException, error)
	GetException(ctx context.Context, id string) (domain.StandardException, error)
	ListExceptionsForApplication(ctx context.Context, applicationID string) ([]domain.StandardException, error)
	ListExceptionsByStatus(ctx context.Context, status string) ([]domain.StandardException, error)
	// DecideException records the decision only while the exception is
	// still pending (domain.ErrExceptionNotPending otherwise).
	DecideException(ctx context.Context, id, status, decidedBy, reason string) (domain.StandardException, error)
}

type StackLister interface {
	List(ctx context.Context) ([]domain.SupportedStack, error)
}

// GovernanceAdmins is who decides exceptions and manages the rules: the
// platform administrators (PLATFORM_ADMIN_EMAILS).
type GovernanceAdmins interface {
	IsAdmin(email string) bool
	AdminUserIDs(ctx context.Context) []string
}

type GovernanceNotifier interface {
	NotifyOwners(ctx context.Context, applicationID string, category domain.NotificationCategory, title, detail, resourceType, resourceID string)
	NotifyUser(ctx context.Context, recipientUserID string, category domain.NotificationCategory, title, detail, resourceType, resourceID string)
}

type GovernanceService struct {
	repo     GovernanceRepository
	apps     ApplicationLifecycleRepository
	owners   ApplicationOwnerRepository
	stacks   StackLister
	admins   GovernanceAdmins
	notifier GovernanceNotifier
	audit    AuditRecorder
	// requireProfile makes a build refuse an application without a
	// profile (GOVERNANCE_REQUIRE_PROFILE, on unless turned off).
	requireProfile bool
	previews       PreviewParents
}

// SetPreviews makes a preview's build use its application's exceptions.
func (s *GovernanceService) SetPreviews(p PreviewParents) { s.previews = p }

func NewGovernanceService(repo GovernanceRepository, apps ApplicationLifecycleRepository, owners ApplicationOwnerRepository,
	stacks StackLister, admins GovernanceAdmins, notifier GovernanceNotifier, audit AuditRecorder, requireProfile bool) *GovernanceService {
	return &GovernanceService{repo: repo, apps: apps, owners: owners, stacks: stacks, admins: admins,
		notifier: notifier, audit: audit, requireProfile: requireProfile}
}

func (s *GovernanceService) isOwner(ctx context.Context, applicationID, userID string) (bool, error) {
	owners, err := s.owners.ListForApplication(ctx, applicationID)
	if err != nil {
		return false, err
	}
	for _, o := range owners {
		if o.UserID == userID && o.Status == "active" {
			return true, nil
		}
	}
	return false, nil
}

// requireOwnerOrAdmin is who may read an application's governance records:
// its owners, and the administrators who decide on them.
func (s *GovernanceService) requireOwnerOrAdmin(ctx context.Context, applicationID string, caller domain.User, ownerOnly bool) error {
	if _, err := s.apps.GetByID(ctx, applicationID); err != nil {
		return err
	}
	ok, err := s.isOwner(ctx, applicationID, caller.ID)
	if err != nil {
		return err
	}
	if ok || (!ownerOnly && s.admins.IsAdmin(caller.Email)) {
		return nil
	}
	return domain.ErrUnauthorized
}

func (s *GovernanceService) requireAdmin(caller domain.User) error {
	if !s.admins.IsAdmin(caller.Email) {
		return domain.ErrUnauthorized
	}
	return nil
}

func (s *GovernanceService) record(ctx context.Context, actor string, action domain.AuditAction, resourceType, resourceID, detail string) error {
	if err := s.audit.Record(ctx, domain.AuditEntry{
		ActorUserID: actor, Action: action, ResourceType: resourceType, ResourceID: resourceID,
		Outcome: domain.AuditSuccess, Detail: detail,
	}); err != nil {
		return fmt.Errorf("recorded, but the audit trail failed to record it: %w", err)
	}
	return nil
}

// Standards is the company standard as published to whoever starts an
// application.
type Standards struct {
	Rules           []string
	Stacks          []domain.SupportedStack
	DependencyRules []domain.DependencyRule
	RequireProfile  bool
}

func (s *GovernanceService) Standards(ctx context.Context) (Standards, error) {
	stacks, err := s.stacks.List(ctx)
	if err != nil {
		return Standards{}, err
	}
	rules, err := s.repo.ListDependencyRules(ctx)
	if err != nil {
		return Standards{}, err
	}
	return Standards{Rules: domain.CompanyStandardRules, Stacks: stacks, DependencyRules: rules, RequireProfile: s.requireProfile}, nil
}

func (s *GovernanceService) AddDependencyRule(ctx context.Context, caller domain.User, ecosystem, pkg, reason string) (domain.DependencyRule, error) {
	if err := s.requireAdmin(caller); err != nil {
		return domain.DependencyRule{}, err
	}
	ecosystem = strings.ToLower(strings.TrimSpace(ecosystem))
	pkg = domain.NormalizePackage(ecosystem, pkg)
	reason = strings.TrimSpace(reason)
	if !domain.ValidEcosystem(ecosystem) || pkg == "" || reason == "" || !utf8.ValidString(pkg+reason) || len(pkg) > 214 || len(reason) > 1000 {
		return domain.DependencyRule{}, domain.ErrInvalidDependencyRule
	}
	rule, err := s.repo.AddDependencyRule(ctx, domain.DependencyRule{Ecosystem: ecosystem, Package: pkg, Reason: reason, CreatedBy: caller.ID})
	if err != nil {
		return domain.DependencyRule{}, err
	}
	return rule, s.record(ctx, caller.ID, domain.AuditActionDependencyRuleAdd, "dependency_rule", rule.ID,
		fmt.Sprintf("%s:%s — %s", rule.Ecosystem, rule.Package, rule.Reason))
}

func (s *GovernanceService) RemoveDependencyRule(ctx context.Context, caller domain.User, id string) error {
	if err := s.requireAdmin(caller); err != nil {
		return err
	}
	rule, err := s.repo.DeleteDependencyRule(ctx, id)
	if err != nil {
		return err
	}
	return s.record(ctx, caller.ID, domain.AuditActionDependencyRuleRemove, "dependency_rule", rule.ID, rule.Ecosystem+":"+rule.Package)
}

// PutProfile records what an application is for: start_project's step
// after registering it, and how its owners keep it current.
func (s *GovernanceService) PutProfile(ctx context.Context, caller domain.User, p domain.ApplicationProfile) (domain.ApplicationProfile, error) {
	if err := s.requireOwnerOrAdmin(ctx, p.ApplicationID, caller, true); err != nil {
		return domain.ApplicationProfile{}, err
	}
	p.Purpose = strings.TrimSpace(p.Purpose)
	for _, f := range []*string{&p.UsersDescription, &p.DataDescription, &p.Frontend, &p.Backend, &p.Database} {
		*f = strings.TrimSpace(*f)
	}
	all := p.Purpose + p.UsersDescription + p.DataDescription + p.Frontend + p.Backend + p.Database
	if p.Purpose == "" || !utf8.ValidString(all) || strings.ContainsRune(all, utf8.RuneError) || len(all) > 8000 {
		return domain.ApplicationProfile{}, domain.ErrInvalidProfile
	}
	p.UpdatedBy = caller.ID
	saved, err := s.repo.PutProfile(ctx, p)
	if err != nil {
		return domain.ApplicationProfile{}, err
	}
	personal := "no personal data"
	if saved.HandlesPersonalData {
		personal = "handles personal data"
	}
	return saved, s.record(ctx, caller.ID, domain.AuditActionRecordProfile, "application", p.ApplicationID,
		fmt.Sprintf("%s (%s)", saved.Purpose, personal))
}

func (s *GovernanceService) GetProfile(ctx context.Context, caller domain.User, applicationID string) (domain.ApplicationProfile, error) {
	if err := s.requireOwnerOrAdmin(ctx, applicationID, caller, false); err != nil {
		return domain.ApplicationProfile{}, err
	}
	return s.repo.GetProfile(ctx, applicationID)
}

// RequestException asks the administrators to allow something outside the
// standard. Asking again for what is already pending or approved returns
// that request instead of a second one.
func (s *GovernanceService) RequestException(ctx context.Context, caller domain.User, applicationID, kind, subject, reason string) (domain.StandardException, bool, error) {
	if err := s.requireOwnerOrAdmin(ctx, applicationID, caller, true); err != nil {
		return domain.StandardException{}, false, err
	}
	if app, err := s.apps.GetByID(ctx, applicationID); err != nil {
		return domain.StandardException{}, false, err
	} else if app.LifecycleStatus == domain.StatusDeleted {
		return domain.StandardException{}, false, domain.ErrNotFound
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	subject = strings.TrimSpace(subject)
	reason = strings.TrimSpace(reason)
	if kind != "dependency" && kind != "stack" && kind != "other" {
		return domain.StandardException{}, false, domain.ErrInvalidException
	}
	if kind == "dependency" {
		eco, pkg, ok := strings.Cut(subject, ":")
		eco = strings.ToLower(strings.TrimSpace(eco))
		if !ok || !domain.ValidEcosystem(eco) || strings.TrimSpace(pkg) == "" {
			return domain.StandardException{}, false, domain.ErrInvalidException
		}
		subject = domain.DependencySubject(eco, pkg)
	}
	if subject == "" || reason == "" || !utf8.ValidString(subject+reason) || len(subject) > 300 || len(reason) > 4000 {
		return domain.StandardException{}, false, domain.ErrInvalidException
	}

	existing, err := s.repo.ListExceptionsForApplication(ctx, applicationID)
	if err != nil {
		return domain.StandardException{}, false, err
	}
	for _, e := range existing {
		if e.Kind == kind && strings.EqualFold(e.Subject, subject) && e.Status != domain.ExceptionRejected {
			return e, false, nil
		}
	}

	e, err := s.repo.CreateException(ctx, domain.StandardException{
		ApplicationID: applicationID, RequestedBy: caller.ID, Kind: kind, Subject: subject, Reason: reason,
	})
	if err != nil {
		return domain.StandardException{}, false, err
	}
	app, _ := s.apps.GetByID(ctx, applicationID)
	for _, id := range s.admins.AdminUserIDs(ctx) {
		s.notifier.NotifyUser(ctx, id, domain.NotificationStandardException,
			fmt.Sprintf("%s asks for an exception: %s %s", app.Name, kind, subject),
			fmt.Sprintf("%s: %s", caller.Email, reason), "standard_exception", e.ID)
	}
	return e, true, s.record(ctx, caller.ID, domain.AuditActionRequestException, "standard_exception", e.ID,
		fmt.Sprintf("%s %s %s: %s", app.Name, kind, subject, reason))
}

func (s *GovernanceService) ListExceptions(ctx context.Context, caller domain.User, applicationID string) ([]domain.StandardException, error) {
	if err := s.requireOwnerOrAdmin(ctx, applicationID, caller, false); err != nil {
		return nil, err
	}
	return s.repo.ListExceptionsForApplication(ctx, applicationID)
}

// ExceptionsByStatus is the administrators' queue (status "pending") and
// history.
func (s *GovernanceService) ExceptionsByStatus(ctx context.Context, caller domain.User, status string) ([]domain.StandardException, error) {
	if err := s.requireAdmin(caller); err != nil {
		return nil, err
	}
	if status != domain.ExceptionPending && status != domain.ExceptionApproved && status != domain.ExceptionRejected {
		return nil, domain.ErrInvalidException
	}
	return s.repo.ListExceptionsByStatus(ctx, status)
}

// DecideException is an administrator's answer. Rejecting needs a reason,
// so the owner knows what to change.
func (s *GovernanceService) DecideException(ctx context.Context, caller domain.User, id string, approve bool, reason string) (domain.StandardException, error) {
	if err := s.requireAdmin(caller); err != nil {
		return domain.StandardException{}, err
	}
	reason = strings.TrimSpace(reason)
	if !approve && reason == "" {
		return domain.StandardException{}, domain.ErrRejectionReasonRequired
	}
	status := domain.ExceptionApproved
	if !approve {
		status = domain.ExceptionRejected
	}
	e, err := s.repo.DecideException(ctx, id, status, caller.ID, reason)
	if err != nil {
		return domain.StandardException{}, err
	}
	detail := reason
	if detail == "" {
		detail = "approved by " + caller.Email
	}
	s.notifier.NotifyOwners(ctx, e.ApplicationID, domain.NotificationStandardException,
		fmt.Sprintf("Exception %s: %s %s", status, e.Kind, e.Subject), detail, "standard_exception", e.ID)
	return e, s.record(ctx, caller.ID, domain.AuditActionDecideException, "standard_exception", e.ID,
		fmt.Sprintf("%s %s %s: %s", status, e.Kind, e.Subject, reason))
}

// SourceCheck is what a project's source looks like against the standard.
type SourceCheck struct {
	Dependencies []domain.Dependency
	Violations   []DependencyViolation
	Excepted     []domain.Dependency
	HasProfile   bool
	// ProfileRequired is whether a build would refuse it without one.
	ProfileRequired bool
}

// CheckSource runs the build's dependency check without building
// (check_project), so a project finds out while it is being written.
func (s *GovernanceService) CheckSource(ctx context.Context, caller domain.User, applicationID string, archive []byte) (SourceCheck, error) {
	if err := s.requireOwnerOrAdmin(ctx, applicationID, caller, true); err != nil {
		return SourceCheck{}, err
	}
	deps, err := ScanDependencies(archive)
	if err != nil {
		return SourceCheck{}, fmt.Errorf("%w: %v", domain.ErrUnreadableSource, err)
	}
	violations, excepted, err := s.check(ctx, applicationID, deps)
	if err != nil {
		return SourceCheck{}, err
	}
	_, perr := s.repo.GetProfile(ctx, applicationID)
	if perr != nil && !errors.Is(perr, domain.ErrNotFound) {
		return SourceCheck{}, perr
	}
	return SourceCheck{Dependencies: deps, Violations: violations, Excepted: excepted, HasProfile: perr == nil, ProfileRequired: s.requireProfile}, nil
}

func (s *GovernanceService) check(ctx context.Context, applicationID string, deps []domain.Dependency) ([]DependencyViolation, []domain.Dependency, error) {
	rules, err := s.repo.ListDependencyRules(ctx)
	if err != nil {
		return nil, nil, err
	}
	// A preview builds what its application would: the same exceptions.
	if s.previews != nil {
		if parent, err := s.previews.ParentOf(ctx, applicationID); err != nil {
			return nil, nil, err
		} else if parent != "" {
			applicationID = parent
		}
	}
	exceptions, err := s.repo.ListExceptionsForApplication(ctx, applicationID)
	if err != nil {
		return nil, nil, err
	}
	approved := map[string]bool{}
	for _, e := range exceptions {
		if e.Kind == "dependency" && e.Status == domain.ExceptionApproved {
			approved[e.Subject] = true
		}
	}
	v, a := CheckDependencies(deps, rules, approved)
	return v, a, nil
}

// RequireProfile is the build's precondition: an application is started
// with start_project, which records its profile, before it is built.
func (s *GovernanceService) RequireProfile(ctx context.Context, applicationID string) error {
	if !s.requireProfile {
		return nil
	}
	if _, err := s.repo.GetProfile(ctx, applicationID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.ErrProfileRequired
		}
		return err
	}
	return nil
}

// SourceFailure is the build's check of the source: a *domain.BuildFailure
// of category "source" when a library isn't allowed, nil when it's fine.
func (s *GovernanceService) SourceFailure(ctx context.Context, applicationID string, archive []byte) error {
	deps, err := ScanDependencies(archive)
	if err != nil {
		return &domain.BuildFailure{Category: domain.ErrorCategorySource, Detail: "the dependency check couldn't read the source: " + err.Error()}
	}
	violations, _, err := s.check(ctx, applicationID, deps)
	if err != nil {
		return err
	}
	if len(violations) > 0 {
		return &domain.BuildFailure{Category: domain.ErrorCategorySource, Detail: DescribeViolations(violations)}
	}
	return nil
}
