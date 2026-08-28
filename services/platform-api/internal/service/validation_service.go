// Validation implements Module H (docs/02_Functional_Requirements.md):
// FR-029 (aggregate pass), FR-030 (stack compliance), FR-031 (security
// pre-check), FR-033 (naming/domain conflict), FR-034 (result reporting).
// FR-032 (resource quota) is honestly reported as "skipped", not faked —
// see ValidationReport's doc comment in internal/domain.
//
// Also covers the save half of Module G's FR-023 (author deployment.yaml)
// and the structural part of FR-024 (schema validation), since a
// validation pass needs something to validate.
package service

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"platform-api/internal/domain"
)

type StackRepository interface {
	FindKind(ctx context.Context, name string) (kind domain.StackKind, allowed bool, err error)
	IsAllowed(ctx context.Context, kind domain.StackKind, name string) (bool, error)
	List(ctx context.Context) ([]domain.SupportedStack, error)
}

// ApplicationLifecycleRepository is the subset of application persistence
// the validation flow needs. Satisfied by *postgres.ApplicationRepo — kept
// as its own narrow interface here rather than reusing
// service.ApplicationRepository so ValidationService doesn't take on a
// dependency on Register/List/etc. it doesn't use.
type ApplicationLifecycleRepository interface {
	GetByID(ctx context.Context, id string) (domain.Application, error)
	UpdateLifecycleStatus(ctx context.Context, id string, from, to domain.LifecycleStatus, markValidated bool) (domain.Application, error)
	UpdateDeploymentYAML(ctx context.Context, id, yamlContent string) (domain.Application, error)
}

type ValidationService struct {
	apps   ApplicationLifecycleRepository
	owners ApplicationOwnerRepository
	stacks StackRepository
}

func NewValidationService(apps ApplicationLifecycleRepository, owners ApplicationOwnerRepository, stacks StackRepository) *ValidationService {
	return &ValidationService{apps: apps, owners: owners, stacks: stacks}
}

func (s *ValidationService) requireOwner(ctx context.Context, applicationID, userID string) error {
	owners, err := s.owners.ListForApplication(ctx, applicationID)
	if err != nil {
		return err
	}
	for _, o := range owners {
		if o.UserID == userID && o.Status == "active" {
			return nil
		}
	}
	return domain.ErrUnauthorized
}

// SaveDeploymentYAML implements FR-023's save path: accepts syntactically
// valid YAML (full schema/business validation happens separately via
// Validate, per FR-024's alternative flow allowing incomplete drafts to be
// saved). Saving reverts a Validated application to Draft, since the
// contract just changed.
func (s *ValidationService) SaveDeploymentYAML(ctx context.Context, applicationID, requesterID, yamlContent string) (domain.Application, error) {
	if err := s.requireOwner(ctx, applicationID, requesterID); err != nil {
		return domain.Application{}, err
	}

	var probe any
	if err := yaml.Unmarshal([]byte(yamlContent), &probe); err != nil {
		return domain.Application{}, fmt.Errorf("%w: %s", domain.ErrInvalidYAML, err.Error())
	}

	return s.apps.UpdateDeploymentYAML(ctx, applicationID, yamlContent)
}

// Validate implements FR-029 (docs/02_Functional_Requirements.md Module H):
// runs the aggregate pre-deployment validation pass and, on success,
// transitions the application Draft -> Validated. Only callable from Draft
// — an already-Validated application must go through SaveDeploymentYAML
// (which reverts it to Draft) before it can be re-validated, so there is
// never a stale "Validated" status sitting on top of an edited contract.
func (s *ValidationService) Validate(ctx context.Context, applicationID, requesterID string) (domain.ValidationReport, domain.Application, error) {
	app, err := s.apps.GetByID(ctx, applicationID)
	if err != nil {
		return domain.ValidationReport{}, domain.Application{}, err
	}
	if err := s.requireOwner(ctx, applicationID, requesterID); err != nil {
		return domain.ValidationReport{}, domain.Application{}, err
	}
	if app.LifecycleStatus != domain.StatusDraft {
		return domain.ValidationReport{}, domain.Application{}, domain.ErrInvalidLifecycleTransition
	}
	if strings.TrimSpace(app.DeploymentYAMLDraft) == "" {
		return domain.ValidationReport{}, domain.Application{}, domain.ErrNoDeploymentYAML
	}

	report, parsed, schemaOK := s.checkSchema(app)
	checks := []domain.ValidationCheck{report}

	if schemaOK {
		stackCheck := s.checkStackCompliance(ctx, parsed)
		checks = append(checks, stackCheck)
	} else {
		checks = append(checks, domain.ValidationCheck{
			Name: "stack_compliance", Status: domain.CheckSkipped,
			Details: []string{"skipped — schema check failed first"},
		})
	}

	checks = append(checks,
		domain.ValidationCheck{
			Name: "resource_quota", Status: domain.CheckSkipped,
			Details: []string{"Department resource quotas are not yet enforced — Module M (Resource Manager) is not implemented and exact quota numbers are TBD (DEC-014, docs/17_Decision_Log.md)."},
		},
		domain.ValidationCheck{
			Name: "naming_domain_conflict", Status: domain.CheckPassed,
			Details: []string{"Enforced at registration time (FR-012); no additional domain-derivation conflict check yet (Module P not implemented)."},
		},
	)

	valid := true
	for _, c := range checks {
		if c.Status == domain.CheckFailed {
			valid = false
		}
	}
	statusReport := domain.ValidationReport{Valid: valid, Checks: checks}

	if !valid {
		return statusReport, app, nil
	}

	updated, err := s.apps.UpdateLifecycleStatus(ctx, applicationID, domain.StatusDraft, domain.StatusValidated, true)
	if err != nil {
		return statusReport, app, err
	}
	return statusReport, updated, nil
}

// checkSchema implements FR-024 as far as it applies at validation time,
// plus FR-031's one concretely-expressible security pre-check: any
// top-level field outside the approved contract shape is rejected outright
// (KnownFields), so there is no way to smuggle raw infrastructure config
// (Kubernetes/Docker/Nginx/etc.) through deployment.yaml — the AI agent is
// never trusted as the security boundary here, this is enforced
// server-side regardless of what generated the file.
func (s *ValidationService) checkSchema(app domain.Application) (domain.ValidationCheck, domain.DeploymentYAML, bool) {
	var parsed domain.DeploymentYAML
	dec := yaml.NewDecoder(bytes.NewReader([]byte(app.DeploymentYAMLDraft)))
	dec.KnownFields(true)
	if err := dec.Decode(&parsed); err != nil {
		msg := err.Error()
		if strings.Contains(msg, "not found in type") {
			return domain.ValidationCheck{
				Name: "security_precheck", Status: domain.CheckFailed,
				Details: []string{"deployment.yaml contains a field outside the approved application contract schema (app/services/database/scaling/resources/domain) — raw infrastructure configuration is never accepted: " + msg},
			}, parsed, false
		}
		return domain.ValidationCheck{
			Name: "schema", Status: domain.CheckFailed,
			Details: []string{"deployment.yaml does not match the required schema: " + msg},
		}, parsed, false
	}

	var problems []string
	if strings.TrimSpace(parsed.App.Name) == "" {
		problems = append(problems, "app.name is required")
	} else if parsed.App.Name != app.Name {
		problems = append(problems, fmt.Sprintf("app.name (%q) must match the registered application name (%q)", parsed.App.Name, app.Name))
	}
	if len(parsed.Services) == 0 {
		problems = append(problems, "services must declare at least one service")
	}
	for name, svc := range parsed.Services {
		if strings.TrimSpace(svc.Runtime) == "" {
			problems = append(problems, fmt.Sprintf("services.%s.runtime is required", name))
		}
	}
	if parsed.Scaling.Min != nil && parsed.Scaling.Max != nil && *parsed.Scaling.Min > *parsed.Scaling.Max {
		problems = append(problems, "scaling.min must not be greater than scaling.max")
	}
	if tier := parsed.Resources.Tier; tier != "" && tier != "small" && tier != "medium" && tier != "large" {
		problems = append(problems, fmt.Sprintf("resources.tier %q is not a recognized tier (small, medium, large)", tier))
	}
	if vis := parsed.Domain.Visibility; vis != "" && vis != "internal" && vis != "external" {
		problems = append(problems, fmt.Sprintf("domain.visibility %q must be \"internal\" or \"external\"", vis))
	}

	if len(problems) > 0 {
		return domain.ValidationCheck{Name: "schema", Status: domain.CheckFailed, Details: problems}, parsed, false
	}
	return domain.ValidationCheck{Name: "schema", Status: domain.CheckPassed}, parsed, true
}

// checkStackCompliance implements FR-030/FR-021: every declared
// service runtime and database type must be active in the Supported
// Stack catalog (Module F).
func (s *ValidationService) checkStackCompliance(ctx context.Context, parsed domain.DeploymentYAML) domain.ValidationCheck {
	var problems []string

	for name, svc := range parsed.Services {
		kind, allowed, err := s.stacks.FindKind(ctx, svc.Runtime)
		if err != nil {
			problems = append(problems, fmt.Sprintf("services.%s: error checking stack catalog: %v", name, err))
			continue
		}
		if !allowed {
			problems = append(problems, fmt.Sprintf("services.%s declares unsupported runtime %q", name, svc.Runtime))
			continue
		}
		if kind == domain.StackKindBackend && svc.Port == 0 {
			problems = append(problems, fmt.Sprintf("services.%s: backend/API services must declare a port", name))
		}
	}

	if dbType := parsed.Database.Type; dbType != "" {
		ok, err := s.stacks.IsAllowed(ctx, domain.StackKindDatabase, dbType)
		if err != nil {
			problems = append(problems, fmt.Sprintf("database: error checking stack catalog: %v", err))
		} else if !ok {
			problems = append(problems, fmt.Sprintf("database.type declares unsupported database %q", dbType))
		}
	}

	if len(problems) > 0 {
		return domain.ValidationCheck{Name: "stack_compliance", Status: domain.CheckFailed, Details: problems}
	}
	return domain.ValidationCheck{Name: "stack_compliance", Status: domain.CheckPassed}
}
