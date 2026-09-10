package service_test

import (
	"context"
	"testing"
	"time"

	"platform-api/internal/domain"
	"platform-api/internal/service"
)

func newReportingService() (*service.ReportingService, *fakeApplicationRepo, *fakeOwnerRepo, *fakeDeploymentRepo, *fakeAuditRepo) {
	apps := newFakeApplicationRepo()
	owners := newFakeOwnerRepo()
	depts := &fakeDepartmentRepo{
		known: map[string]bool{"dept-1": true, "dept-2": true},
		names: map[string]string{"dept-1": "Engineering", "dept-2": "Finance"},
	}
	deployments := newFakeDeploymentRepo()
	audit := newFakeAuditRepo()
	svc := service.NewReportingService(apps, owners, depts, deployments, audit)
	return svc, apps, owners, deployments, audit
}

// seedOwnedApp puts an application in the fake repos with the given user
// as its active primary owner, returning its generated id.
func seedOwnedApp(t *testing.T, apps *fakeApplicationRepo, owners *fakeOwnerRepo, name, departmentID, ownerID, deploymentYAML string, status domain.LifecycleStatus) string {
	t.Helper()
	app, err := apps.Create(context.Background(), domain.Application{
		Name: name, OwningDepartmentID: departmentID, CreatedBy: ownerID,
		LifecycleStatus: status, DeploymentYAMLDraft: deploymentYAML,
	})
	if err != nil {
		t.Fatalf("seed application: %v", err)
	}
	owners.owners[app.ID] = []domain.ApplicationOwner{
		{ApplicationID: app.ID, UserID: ownerID, OwnershipRole: domain.OwnerRolePrimary, Status: "active"},
	}
	return app.ID
}

const goServiceYAML = "app:\n  name: x\n  owner: Engineering\nservices:\n  api:\n    runtime: go\n    port: 8080\n"

func TestApplicationInventory_ReturnsOnlyApplicationsTheCallerOwns(t *testing.T) {
	svc, apps, owners, _, _ := newReportingService()
	seedOwnedApp(t, apps, owners, "mine", "dept-1", "owner-1", goServiceYAML, domain.StatusDraft)
	seedOwnedApp(t, apps, owners, "someone-elses", "dept-1", "other-owner", goServiceYAML, domain.StatusDraft)

	rows, err := svc.ApplicationInventory(context.Background(), "owner-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "mine" {
		t.Fatalf("expected only the caller's own application, got %+v", rows)
	}
}

func TestApplicationInventory_IncludesDepartmentOwnersStackAndLifecycleState(t *testing.T) {
	svc, apps, owners, deployments, _ := newReportingService()
	appID := seedOwnedApp(t, apps, owners, "overtime", "dept-2", "owner-1", goServiceYAML, domain.StatusRunning)
	owners.owners[appID] = append(owners.owners[appID], domain.ApplicationOwner{
		ApplicationID: appID, UserID: "co-owner-1", OwnershipRole: domain.OwnerRoleSecondary, Status: "active",
	})
	// A revoked owner must not appear in the report.
	owners.owners[appID] = append(owners.owners[appID], domain.ApplicationOwner{
		ApplicationID: appID, UserID: "former-owner", OwnershipRole: domain.OwnerRoleSecondary, Status: "revoked",
	})
	deployments.byID["dep-1"] = domain.Deployment{
		ID: "dep-1", ApplicationID: appID, Environment: domain.EnvironmentProduction,
		Status: domain.DeploymentRunning, CreatedAt: time.Now(),
	}

	rows, err := svc.ApplicationInventory(context.Background(), "owner-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected one row, got %d", len(rows))
	}
	row := rows[0]
	if row.DepartmentName != "Finance" || row.DepartmentID != "dept-2" {
		t.Errorf("expected the department resolved to its name, got %q/%q", row.DepartmentID, row.DepartmentName)
	}
	if row.LifecycleStatus != domain.StatusRunning {
		t.Errorf("expected running, got %q", row.LifecycleStatus)
	}
	if len(row.Runtimes) != 1 || row.Runtimes[0] != "go" {
		t.Errorf("expected the stack read from deployment.yaml, got %v", row.Runtimes)
	}
	if len(row.OwnerUserIDs) != 2 {
		t.Errorf("expected exactly the two ACTIVE owners (revoked excluded), got %v", row.OwnerUserIDs)
	}
	if row.Environment != "production" {
		t.Errorf("expected the latest deployment's environment, got %q", row.Environment)
	}
}

func TestApplicationInventory_UnparseableDeploymentYAMLReportsNoRuntimesRatherThanFailing(t *testing.T) {
	svc, apps, owners, _, _ := newReportingService()
	seedOwnedApp(t, apps, owners, "broken", "dept-1", "owner-1", "this: is: not: valid: yaml:", domain.StatusDraft)
	seedOwnedApp(t, apps, owners, "fine", "dept-1", "owner-1", goServiceYAML, domain.StatusDraft)

	rows, err := svc.ApplicationInventory(context.Background(), "owner-1")
	if err != nil {
		t.Fatalf("one malformed draft must not fail the whole inventory: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected both applications listed, got %d", len(rows))
	}
	for _, row := range rows {
		if row.Name == "broken" && len(row.Runtimes) != 0 {
			t.Errorf("expected no runtimes for an unparseable draft, got %v", row.Runtimes)
		}
		if row.Name == "fine" && len(row.Runtimes) != 1 {
			t.Errorf("expected the valid draft's runtime to still be read, got %v", row.Runtimes)
		}
	}
}

func TestApplicationInventory_NeverDeployedApplicationHasNoEnvironment(t *testing.T) {
	svc, apps, owners, _, _ := newReportingService()
	seedOwnedApp(t, apps, owners, "fresh", "dept-1", "owner-1", goServiceYAML, domain.StatusDraft)

	rows, err := svc.ApplicationInventory(context.Background(), "owner-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rows[0].Environment != "" {
		t.Fatalf("expected no environment for a never-deployed application, got %q", rows[0].Environment)
	}
}

func TestDeploymentActivity_CountsTerminalOutcomesByEnvironmentAndDepartment(t *testing.T) {
	svc, apps, owners, deployments, _ := newReportingService()
	appID := seedOwnedApp(t, apps, owners, "overtime", "dept-1", "owner-1", goServiceYAML, domain.StatusRunning)
	now := time.Now()
	deployments.byID["d1"] = domain.Deployment{ID: "d1", ApplicationID: appID, Environment: domain.EnvironmentDev, Status: domain.DeploymentRunning, CreatedAt: now}
	deployments.byID["d2"] = domain.Deployment{ID: "d2", ApplicationID: appID, Environment: domain.EnvironmentDev, Status: domain.DeploymentFailed, CreatedAt: now}
	deployments.byID["d3"] = domain.Deployment{ID: "d3", ApplicationID: appID, Environment: domain.EnvironmentProduction, Status: domain.DeploymentSuperseded, CreatedAt: now}
	// In flight: has no outcome yet, so it must not be counted at all.
	deployments.byID["d4"] = domain.Deployment{ID: "d4", ApplicationID: appID, Environment: domain.EnvironmentProduction, Status: domain.DeploymentDeploying, CreatedAt: now}

	report, err := svc.DeploymentActivity(context.Background(), "owner-1", now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Total.Succeeded != 2 || report.Total.Failed != 1 || report.Total.RolledBack != 0 {
		t.Fatalf("unexpected totals: %+v", report.Total)
	}
	if report.ByEnvironment["dev"].Succeeded != 1 || report.ByEnvironment["dev"].Failed != 1 {
		t.Errorf("unexpected dev counts: %+v", report.ByEnvironment["dev"])
	}
	if report.ByEnvironment["production"].Succeeded != 1 {
		t.Errorf("unexpected production counts: %+v", report.ByEnvironment["production"])
	}
	if report.ByDepartment["Engineering"].Succeeded != 2 || report.ByDepartment["Engineering"].Failed != 1 {
		t.Errorf("unexpected department counts: %+v", report.ByDepartment["Engineering"])
	}
}

// FR-128's succeeded/failed/rolled-back split can't come from
// deployments.status alone — a rollback produces an ordinary 'running'
// row. The audit log is what distinguishes it, and this is the test that
// holds that wiring in place.
func TestDeploymentActivity_RollbackOriginatedDeploymentCountsAsRolledBackNotSucceeded(t *testing.T) {
	svc, apps, owners, deployments, audit := newReportingService()
	appID := seedOwnedApp(t, apps, owners, "overtime", "dept-1", "owner-1", goServiceYAML, domain.StatusRunning)
	now := time.Now()
	deployments.byID["forward"] = domain.Deployment{ID: "forward", ApplicationID: appID, Environment: domain.EnvironmentDev, Status: domain.DeploymentSuperseded, CreatedAt: now}
	deployments.byID["viaRollback"] = domain.Deployment{ID: "viaRollback", ApplicationID: appID, Environment: domain.EnvironmentDev, Status: domain.DeploymentRunning, CreatedAt: now}
	audit.entries = []domain.AuditEntry{
		{Seq: 1, ActorUserID: "owner-1", Action: domain.AuditActionRollback, ResourceType: "deployment", ResourceID: "viaRollback", OccurredAt: now},
	}

	report, err := svc.DeploymentActivity(context.Background(), "owner-1", now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Total.Succeeded != 1 || report.Total.RolledBack != 1 || report.Total.Failed != 0 {
		t.Fatalf("expected the rollback-originated deployment counted separately, got %+v", report.Total)
	}
}

func TestDeploymentActivity_ExcludesDeploymentsOutsideTheRequestedRange(t *testing.T) {
	svc, apps, owners, deployments, _ := newReportingService()
	appID := seedOwnedApp(t, apps, owners, "overtime", "dept-1", "owner-1", goServiceYAML, domain.StatusRunning)
	now := time.Now()
	deployments.byID["old"] = domain.Deployment{ID: "old", ApplicationID: appID, Environment: domain.EnvironmentDev, Status: domain.DeploymentRunning, CreatedAt: now.Add(-72 * time.Hour)}
	deployments.byID["recent"] = domain.Deployment{ID: "recent", ApplicationID: appID, Environment: domain.EnvironmentDev, Status: domain.DeploymentRunning, CreatedAt: now}

	report, err := svc.DeploymentActivity(context.Background(), "owner-1", now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Total.Succeeded != 1 {
		t.Fatalf("expected only the in-range deployment counted, got %+v", report.Total)
	}
}

// FR-128 exception flow: a range starting before any data exists says so
// rather than silently reporting zeros for a period there was no data for.
func TestDeploymentActivity_ReportsAvailableFromWhenRangePredatesTheData(t *testing.T) {
	svc, apps, owners, deployments, _ := newReportingService()
	appID := seedOwnedApp(t, apps, owners, "overtime", "dept-1", "owner-1", goServiceYAML, domain.StatusRunning)
	now := time.Now()
	earliest := now.Add(-24 * time.Hour)
	deployments.byID["d1"] = domain.Deployment{ID: "d1", ApplicationID: appID, Environment: domain.EnvironmentDev, Status: domain.DeploymentRunning, CreatedAt: earliest}

	report, err := svc.DeploymentActivity(context.Background(), "owner-1", now.Add(-365*24*time.Hour), now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.AvailableFrom == nil {
		t.Fatal("expected AvailableFrom to be reported for a range predating the data")
	}
	if !report.AvailableFrom.Equal(earliest) {
		t.Fatalf("expected AvailableFrom to be the earliest deployment's timestamp, got %v", report.AvailableFrom)
	}
}

func TestDeploymentActivity_ExcludesOtherPeoplesApplications(t *testing.T) {
	svc, apps, owners, deployments, _ := newReportingService()
	mine := seedOwnedApp(t, apps, owners, "mine", "dept-1", "owner-1", goServiceYAML, domain.StatusRunning)
	theirs := seedOwnedApp(t, apps, owners, "theirs", "dept-1", "other-owner", goServiceYAML, domain.StatusRunning)
	now := time.Now()
	deployments.byID["mine-1"] = domain.Deployment{ID: "mine-1", ApplicationID: mine, Environment: domain.EnvironmentDev, Status: domain.DeploymentRunning, CreatedAt: now}
	deployments.byID["theirs-1"] = domain.Deployment{ID: "theirs-1", ApplicationID: theirs, Environment: domain.EnvironmentDev, Status: domain.DeploymentRunning, CreatedAt: now}

	report, err := svc.DeploymentActivity(context.Background(), "owner-1", now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Total.Succeeded != 1 {
		t.Fatalf("expected only the caller's own application's deployment counted, got %+v", report.Total)
	}
}
