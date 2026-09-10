// Reporting implements Module AB's service layer
// (docs/02_Functional_Requirements.md FR-127, FR-128). See
// internal/domain/report.go's package comment for the scope this slice
// covers and why both reports are owner-scoped.
package service

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"time"

	"gopkg.in/yaml.v3"

	"platform-api/internal/domain"
)

// OwnedApplicationLister is the reverse-direction seam both reports need:
// "which applications does this person own", rather than every other
// service's "who owns this application". See application_owner_repo.go.
type OwnedApplicationLister interface {
	ListApplicationIDsForUser(ctx context.Context, userID string) ([]string, error)
	ListForApplication(ctx context.Context, applicationID string) ([]domain.ApplicationOwner, error)
}

type DepartmentLister interface {
	List(ctx context.Context) ([]domain.Department, error)
}

// ReportDeploymentReader is the read-only slice of DeploymentRepository
// these reports need — deliberately narrower than the full interface
// deploy_service.go depends on, since nothing here writes.
type ReportDeploymentReader interface {
	ListForApplication(ctx context.Context, applicationID string) ([]domain.Deployment, error)
	LatestForApplication(ctx context.Context, applicationID string) (domain.Deployment, error)
}

// ReportAuditReader is how DeploymentActivity distinguishes a
// rollback-originated deployment from a forward one: deploy_service.go
// records the two under different audit actions against the same new
// deployment's id (see auditDeployOutcome). Reading the raw repository
// rather than AuditService.Query is deliberate — Query re-derives
// per-entry ownership visibility, which is redundant work here since this
// service has already scoped itself to the caller's own applications
// before it ever looks at an audit entry.
type ReportAuditReader interface {
	Query(ctx context.Context, q domain.AuditQuery) ([]domain.AuditEntry, error)
}

type ReportingService struct {
	apps        ApplicationRepository
	owners      OwnedApplicationLister
	departments DepartmentLister
	deployments ReportDeploymentReader
	audit       ReportAuditReader
}

func NewReportingService(
	apps ApplicationRepository, owners OwnedApplicationLister, departments DepartmentLister,
	deployments ReportDeploymentReader, audit ReportAuditReader,
) *ReportingService {
	return &ReportingService{apps: apps, owners: owners, departments: departments, deployments: deployments, audit: audit}
}

func (s *ReportingService) departmentNames(ctx context.Context) map[string]string {
	names := map[string]string{}
	departments, err := s.departments.List(ctx)
	if err != nil {
		// A missing department name degrades the report's labels, not its
		// counts — reported as a blank name rather than failing the whole
		// report over a lookup that's purely cosmetic.
		return names
	}
	for _, d := range departments {
		names[d.ID] = d.Name
	}
	return names
}

// runtimesFrom implements FR-127's "stack" column, read from whatever the
// application's current deployment.yaml draft actually says. A draft that
// no longer parses yields no runtimes rather than an error: this is a
// reporting view of current state, and one malformed draft shouldn't take
// down an inventory covering every other application too.
func runtimesFrom(deploymentYAML string) []string {
	if deploymentYAML == "" {
		return nil
	}
	var parsed domain.DeploymentYAML
	if err := yaml.NewDecoder(bytes.NewReader([]byte(deploymentYAML))).Decode(&parsed); err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, svc := range parsed.Services {
		if svc.Runtime == "" || seen[svc.Runtime] {
			continue
		}
		seen[svc.Runtime] = true
		out = append(out, svc.Runtime)
	}
	sort.Strings(out) // stable output — map iteration order isn't
	return out
}

// ApplicationInventory implements FR-127, scoped per domain/report.go's
// package comment: every application the caller currently owns, with its
// department, owners, lifecycle state, stack and most recent deployment
// environment as of right now. Per FR-127's business rule this is
// deliberately current-state only — the historical record is Module W's
// job, not this report's.
func (s *ReportingService) ApplicationInventory(ctx context.Context, requesterID string) ([]domain.InventoryRow, error) {
	appIDs, err := s.owners.ListApplicationIDsForUser(ctx, requesterID)
	if err != nil {
		return nil, err
	}
	departmentNames := s.departmentNames(ctx)

	rows := make([]domain.InventoryRow, 0, len(appIDs))
	for _, appID := range appIDs {
		app, err := s.apps.GetByID(ctx, appID)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				continue // ownership row outliving its application; skip rather than fail the report
			}
			return nil, err
		}

		row := domain.InventoryRow{
			ApplicationID:   app.ID,
			Name:            app.Name,
			DepartmentID:    app.OwningDepartmentID,
			DepartmentName:  departmentNames[app.OwningDepartmentID],
			LifecycleStatus: app.LifecycleStatus,
			Runtimes:        runtimesFrom(app.DeploymentYAMLDraft),
			CreatedAt:       app.CreatedAt,
		}

		owners, err := s.owners.ListForApplication(ctx, appID)
		if err != nil {
			return nil, err
		}
		for _, o := range owners {
			if o.Status == "active" {
				row.OwnerUserIDs = append(row.OwnerUserIDs, o.UserID)
			}
		}

		if latest, err := s.deployments.LatestForApplication(ctx, appID); err == nil {
			row.Environment = string(latest.Environment)
		} else if !errors.Is(err, domain.ErrNotFound) {
			return nil, err
		}

		rows = append(rows, row)
	}

	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows, nil
}

// terminalOutcome classifies a deployment that has finished. In-flight
// deployments (scanning/pending_approval/deploying/health_check) return
// false — FR-128 counts *outcomes*, and one that hasn't reached an
// outcome yet is neither a success nor a failure to report.
func terminalOutcome(status domain.DeploymentStatus) (succeeded bool, terminal bool) {
	switch status {
	case domain.DeploymentRunning, domain.DeploymentSuperseded, domain.DeploymentArchived, domain.DeploymentSuspended:
		return true, true
	case domain.DeploymentFailed, domain.DeploymentRejected:
		return false, true
	default:
		return false, false
	}
}

// DeploymentActivity implements FR-128: deployment outcomes over a period,
// broken down by environment and department, for applications the caller
// owns.
//
// The succeeded/failed/rolled-back split can't come from deployments.status
// alone: a rollback produces an ordinary deployment row that ends up
// 'running' or 'failed' like any other (there is no 'rolled_back'
// deployment status — see domain.DeploymentStatus). What distinguishes
// them is which action created it, which only the audit log records
// (deployment.deploy vs deployment.rollback, both against the new
// deployment's own id). This is exactly what FR-128's acceptance criterion
// asks for — "a generated report's totals reconcile with the underlying
// audit log for the same period and scope" — so the audit log is the
// source of truth for that classification rather than a second, drifting
// copy of it on the deployments table.
func (s *ReportingService) DeploymentActivity(ctx context.Context, requesterID string, from, to time.Time) (domain.DeploymentActivityReport, error) {
	appIDs, err := s.owners.ListApplicationIDsForUser(ctx, requesterID)
	if err != nil {
		return domain.DeploymentActivityReport{}, err
	}
	departmentNames := s.departmentNames(ctx)

	// One query, not one per deployment: every rollback recorded in the
	// window, turned into a set of deployment ids to test membership
	// against. Entries for applications the caller doesn't own are
	// harmless here — they can only ever match deployments already scoped
	// to the caller below.
	rollbackEntries, err := s.audit.Query(ctx, domain.AuditQuery{
		ResourceType: "deployment", Action: domain.AuditActionRollback, From: &from, To: &to,
	})
	if err != nil {
		return domain.DeploymentActivityReport{}, err
	}
	rolledBack := make(map[string]bool, len(rollbackEntries))
	for _, e := range rollbackEntries {
		rolledBack[e.ResourceID] = true
	}

	report := domain.DeploymentActivityReport{
		From:          from,
		To:            to,
		ByEnvironment: map[string]domain.DeploymentOutcomeCounts{},
		ByDepartment:  map[string]domain.DeploymentOutcomeCounts{},
	}

	var earliest *time.Time
	for _, appID := range appIDs {
		app, err := s.apps.GetByID(ctx, appID)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				continue
			}
			return domain.DeploymentActivityReport{}, err
		}
		departmentName := departmentNames[app.OwningDepartmentID]
		if departmentName == "" {
			departmentName = app.OwningDepartmentID
		}

		deployments, err := s.deployments.ListForApplication(ctx, appID)
		if err != nil {
			return domain.DeploymentActivityReport{}, err
		}
		for _, d := range deployments {
			if earliest == nil || d.CreatedAt.Before(*earliest) {
				created := d.CreatedAt
				earliest = &created
			}
			if d.CreatedAt.Before(from) || d.CreatedAt.After(to) {
				continue
			}
			succeeded, terminal := terminalOutcome(d.Status)
			if !terminal {
				continue
			}

			bump := func(c *domain.DeploymentOutcomeCounts) {
				switch {
				case rolledBack[d.ID]:
					c.RolledBack++
				case succeeded:
					c.Succeeded++
				default:
					c.Failed++
				}
			}
			bump(&report.Total)
			byEnv := report.ByEnvironment[string(d.Environment)]
			bump(&byEnv)
			report.ByEnvironment[string(d.Environment)] = byEnv
			byDept := report.ByDepartment[departmentName]
			bump(&byDept)
			report.ByDepartment[departmentName] = byDept
		}
	}

	// FR-128 exception flow: say so when the requested range starts before
	// anything this platform actually holds, rather than reporting zeros
	// for a period there was simply no data for.
	if earliest != nil && earliest.After(from) {
		report.AvailableFrom = earliest
	}
	return report, nil
}
