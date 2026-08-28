package service_test

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"platform-api/internal/domain"
	"platform-api/internal/service"
)

type fakeDeploymentRepo struct {
	byID map[string]domain.Deployment
	next int
}

func newFakeDeploymentRepo() *fakeDeploymentRepo {
	return &fakeDeploymentRepo{byID: map[string]domain.Deployment{}}
}

func (f *fakeDeploymentRepo) Create(ctx context.Context, applicationID, buildID, requestedBy string, environment domain.Environment) (domain.Deployment, error) {
	f.next++
	id := "deploy-" + string(rune('a'+f.next))
	// Real Postgres timestamps at microsecond resolution, so sequential
	// inserts never tie in practice; time.Now() alone is coarse enough that
	// two Creates within the same test can land on the same instant, making
	// created_at-ordered reads (LatestForApplication, ListForApplication)
	// nondeterministic here. Offsetting by f.next keeps creation order
	// strictly increasing, matching real insert-order behavior.
	createdAt := time.Now().Add(time.Duration(f.next) * time.Millisecond)
	d := domain.Deployment{
		ID: id, ApplicationID: applicationID, BuildID: buildID, RequestedBy: requestedBy,
		Environment: environment, Status: domain.DeploymentScanning, CreatedAt: createdAt, UpdatedAt: createdAt,
	}
	f.byID[id] = d
	return d, nil
}

func (f *fakeDeploymentRepo) UpdateScanResult(ctx context.Context, deploymentID string, reports map[string]domain.ScanReport) (domain.Deployment, error) {
	d := f.byID[deploymentID]
	passed := true
	critical, high := 0, 0
	for _, r := range reports {
		if !r.Passed {
			passed = false
		}
		critical += r.CriticalCount
		high += r.HighCount
	}
	d.ScanPassed = &passed
	d.ScanCriticalCount = &critical
	d.ScanHighCount = &high
	d.ScanReports = reports
	f.byID[deploymentID] = d
	return d, nil
}

func (f *fakeDeploymentRepo) SetStatus(ctx context.Context, deploymentID string, status domain.DeploymentStatus) (domain.Deployment, error) {
	d := f.byID[deploymentID]
	d.Status = status
	f.byID[deploymentID] = d
	return d, nil
}

func (f *fakeDeploymentRepo) SetFailed(ctx context.Context, deploymentID, reason string) (domain.Deployment, error) {
	d := f.byID[deploymentID]
	d.Status = domain.DeploymentFailed
	d.FailureReason = &reason
	now := time.Now()
	d.CompletedAt = &now
	f.byID[deploymentID] = d
	return d, nil
}

func (f *fakeDeploymentRepo) SetRejected(ctx context.Context, deploymentID, reason string) (domain.Deployment, error) {
	d := f.byID[deploymentID]
	d.Status = domain.DeploymentRejected
	d.RejectionReason = &reason
	now := time.Now()
	d.CompletedAt = &now
	f.byID[deploymentID] = d
	return d, nil
}

func (f *fakeDeploymentRepo) SetRunning(ctx context.Context, deploymentID string, containers map[string]domain.RunningContainer) (domain.Deployment, error) {
	d := f.byID[deploymentID]
	d.Status = domain.DeploymentRunning
	d.Containers = containers
	now := time.Now()
	d.CompletedAt = &now
	f.byID[deploymentID] = d
	return d, nil
}

func (f *fakeDeploymentRepo) UpdateContainers(ctx context.Context, deploymentID string, containers map[string]domain.RunningContainer) (domain.Deployment, error) {
	d := f.byID[deploymentID]
	d.Containers = containers
	f.byID[deploymentID] = d
	return d, nil
}

func (f *fakeDeploymentRepo) SetSuperseded(ctx context.Context, deploymentID string) (domain.Deployment, error) {
	d := f.byID[deploymentID]
	d.Status = domain.DeploymentSuperseded
	f.byID[deploymentID] = d
	return d, nil
}

func (f *fakeDeploymentRepo) GetByID(ctx context.Context, deploymentID string) (domain.Deployment, error) {
	d, ok := f.byID[deploymentID]
	if !ok {
		return domain.Deployment{}, domain.ErrNotFound
	}
	return d, nil
}

func (f *fakeDeploymentRepo) LatestForApplication(ctx context.Context, applicationID string) (domain.Deployment, error) {
	var latest domain.Deployment
	found := false
	for _, d := range f.byID {
		if d.ApplicationID != applicationID {
			continue
		}
		if !found || d.CreatedAt.After(latest.CreatedAt) {
			latest = d
			found = true
		}
	}
	if !found {
		return domain.Deployment{}, domain.ErrNotFound
	}
	return latest, nil
}

func (f *fakeDeploymentRepo) PreviousRunning(ctx context.Context, applicationID, excludeDeploymentID string) (domain.Deployment, error) {
	for _, d := range f.byID {
		if d.ApplicationID == applicationID && d.Status == domain.DeploymentRunning && d.ID != excludeDeploymentID {
			return d, nil
		}
	}
	return domain.Deployment{}, domain.ErrNotFound
}

func (f *fakeDeploymentRepo) ListForApplication(ctx context.Context, applicationID string) ([]domain.Deployment, error) {
	var results []domain.Deployment
	for _, d := range f.byID {
		if d.ApplicationID == applicationID {
			results = append(results, d)
		}
	}
	sort.Slice(results, func(i, j int) bool { return results[i].CreatedAt.After(results[j].CreatedAt) })
	return results, nil
}

type fakeApprovalRepo struct {
	byDeployment map[string]domain.DeploymentApproval
}

func newFakeApprovalRepo() *fakeApprovalRepo {
	return &fakeApprovalRepo{byDeployment: map[string]domain.DeploymentApproval{}}
}

func (f *fakeApprovalRepo) Create(ctx context.Context, deploymentID, requestedBy string) (domain.DeploymentApproval, error) {
	a := domain.DeploymentApproval{ID: "appr-" + deploymentID, DeploymentID: deploymentID, RequestedBy: requestedBy, Decision: domain.ApprovalPending, CreatedAt: time.Now()}
	f.byDeployment[deploymentID] = a
	return a, nil
}

func (f *fakeApprovalRepo) Decide(ctx context.Context, deploymentID, decidedBy string, decision domain.ApprovalDecision, reason string) (domain.DeploymentApproval, error) {
	a := f.byDeployment[deploymentID]
	a.DecidedBy = &decidedBy
	a.Decision = decision
	a.Reason = &reason
	now := time.Now()
	a.DecidedAt = &now
	f.byDeployment[deploymentID] = a
	return a, nil
}

type fakeScanner struct {
	reportByImage map[string]domain.ScanReport
	defaultReport domain.ScanReport
	err           error
}

func newPassingScanner() *fakeScanner {
	return &fakeScanner{defaultReport: domain.ScanReport{Passed: true}}
}

func (f *fakeScanner) Scan(ctx context.Context, imageRef string) (domain.ScanReport, error) {
	if f.err != nil {
		return domain.ScanReport{}, f.err
	}
	if r, ok := f.reportByImage[imageRef]; ok {
		return r, nil
	}
	return f.defaultReport, nil
}

type fakeRuntime struct {
	started        []string
	stopped        []string
	healthCheckErr error
	startErr       error
	nextPort       int
}

func newHealthyRuntime() *fakeRuntime {
	return &fakeRuntime{nextPort: 20000}
}

func (f *fakeRuntime) StartContainer(ctx context.Context, name, imageRef string, containerPort int) (domain.RunningContainer, error) {
	if f.startErr != nil {
		return domain.RunningContainer{}, f.startErr
	}
	f.started = append(f.started, name)
	f.nextPort++
	return domain.RunningContainer{ContainerID: "container-" + name, HostPort: f.nextPort, URL: "http://localhost:0"}, nil
}

func (f *fakeRuntime) HealthCheck(ctx context.Context, url string, timeout time.Duration) error {
	return f.healthCheckErr
}

func (f *fakeRuntime) Stop(ctx context.Context, containerID string) error {
	f.stopped = append(f.stopped, containerID)
	return nil
}

type fakeScaleInitializer struct {
	initialized []string
	cleaned     []string
}

func (f *fakeScaleInitializer) InitializeForDeployment(ctx context.Context, deploymentID string, deploymentYAML string, imageRefs map[string]string, containers map[string]domain.RunningContainer) error {
	f.initialized = append(f.initialized, deploymentID)
	return nil
}

func (f *fakeScaleInitializer) CleanupForDeployment(ctx context.Context, deploymentID string) error {
	f.cleaned = append(f.cleaned, deploymentID)
	return nil
}

func newDeployService(app domain.Application, build domain.Build, ownerID string) (
	*service.DeploymentService, *fakeLifecycleRepo, *fakeDeploymentRepo, *fakeRuntime, *fakeScanner, *fakeBuildRepo,
) {
	apps := newFakeLifecycleRepo(app)
	owners := newFakeOwnerRepo()
	owners.owners[app.ID] = []domain.ApplicationOwner{{
		ApplicationID: app.ID, UserID: ownerID, OwnershipRole: domain.OwnerRolePrimary, Status: "active",
	}}
	builds := newFakeBuildRepo()
	builds.byID[build.ID] = build
	builds.byApp[app.ID] = build.ID
	deployments := newFakeDeploymentRepo()
	approvals := newFakeApprovalRepo()
	scanner := newPassingScanner()
	runtime := newHealthyRuntime()
	scale := &fakeScaleInitializer{}

	svc := service.NewDeploymentService(apps, owners, builds, deployments, approvals, scanner, runtime, scale)
	return svc, apps, deployments, runtime, scanner, builds
}

func builtApp(id, name string) (domain.Application, domain.Build) {
	app := domain.Application{
		ID: id, Name: name, LifecycleStatus: domain.StatusBuild,
		DeploymentYAMLDraft: "app:\n  name: " + name + "\n  owner: HR\nservices:\n  api:\n    runtime: go\n    port: 8080\n",
	}
	build := domain.Build{ID: "build-" + id, ApplicationID: id, Status: domain.BuildSucceeded, ImageRefs: map[string]string{"api": "platform-build/" + name + "-api:latest"}}
	return app, build
}

func TestInitiateDeploy_Dev_Success_ActivatesAndSetsRunning(t *testing.T) {
	app, build := builtApp("app-1", "overtime")
	svc, lifecycle, deployments, runtime, _, _ := newDeployService(app, build, "owner-1")

	d, err := svc.InitiateDeploy(context.Background(), "app-1", "owner-1", domain.EnvironmentDev)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Status != domain.DeploymentRunning {
		t.Errorf("expected deployment Running, got %q (failure=%v)", d.Status, d.FailureReason)
	}
	if lifecycle.apps["app-1"].LifecycleStatus != domain.StatusRunning {
		t.Errorf("expected application Running, got %q", lifecycle.apps["app-1"].LifecycleStatus)
	}
	if len(runtime.started) != 1 {
		t.Errorf("expected exactly one container started, got %v", runtime.started)
	}
	if deployments.byID[d.ID].Environment != domain.EnvironmentDev {
		t.Errorf("expected dev environment recorded")
	}
}

func TestInitiateDeploy_Production_PausesForApproval(t *testing.T) {
	app, build := builtApp("app-1", "overtime")
	svc, lifecycle, _, runtime, _, _ := newDeployService(app, build, "owner-1")

	d, err := svc.InitiateDeploy(context.Background(), "app-1", "owner-1", domain.EnvironmentProduction)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Status != domain.DeploymentPendingApproval {
		t.Errorf("expected PendingApproval, got %q", d.Status)
	}
	if len(runtime.started) != 0 {
		t.Error("expected no containers started before approval")
	}
	if lifecycle.apps["app-1"].LifecycleStatus != domain.StatusBuild {
		t.Errorf("expected application to remain in Build state pending approval, got %q", lifecycle.apps["app-1"].LifecycleStatus)
	}
}

func TestDecideApproval_Approved_ContinuesToRunning(t *testing.T) {
	app, build := builtApp("app-1", "overtime")
	svc, lifecycle, _, runtime, _, _ := newDeployService(app, build, "owner-1")

	d, err := svc.InitiateDeploy(context.Background(), "app-1", "owner-1", domain.EnvironmentProduction)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	approved, err := svc.DecideApproval(context.Background(), d.ID, "owner-1", true, "looks good")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if approved.Status != domain.DeploymentRunning {
		t.Errorf("expected Running after approval, got %q", approved.Status)
	}
	if lifecycle.apps["app-1"].LifecycleStatus != domain.StatusRunning {
		t.Errorf("expected application Running, got %q", lifecycle.apps["app-1"].LifecycleStatus)
	}
	if len(runtime.started) != 1 {
		t.Errorf("expected container started after approval, got %v", runtime.started)
	}
}

func TestDecideApproval_Rejected_MarksFailedWithReason(t *testing.T) {
	app, build := builtApp("app-1", "overtime")
	svc, lifecycle, _, runtime, _, _ := newDeployService(app, build, "owner-1")

	d, err := svc.InitiateDeploy(context.Background(), "app-1", "owner-1", domain.EnvironmentProduction)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	rejected, err := svc.DecideApproval(context.Background(), d.ID, "owner-1", false, "not ready")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rejected.Status != domain.DeploymentRejected {
		t.Errorf("expected Rejected, got %q", rejected.Status)
	}
	if rejected.RejectionReason == nil || *rejected.RejectionReason != "not ready" {
		t.Errorf("expected rejection reason recorded, got %v", rejected.RejectionReason)
	}
	if lifecycle.apps["app-1"].LifecycleStatus != domain.StatusFailed {
		t.Errorf("expected application Failed (first-ever deploy rejected), got %q", lifecycle.apps["app-1"].LifecycleStatus)
	}
	if len(runtime.started) != 0 {
		t.Error("expected no containers started for a rejected deployment")
	}
}

func TestDecideApproval_NotPendingApproval_Rejected(t *testing.T) {
	app, build := builtApp("app-1", "overtime")
	svc, _, _, _, _, _ := newDeployService(app, build, "owner-1")

	d, err := svc.InitiateDeploy(context.Background(), "app-1", "owner-1", domain.EnvironmentDev)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	_, err = svc.DecideApproval(context.Background(), d.ID, "owner-1", true, "")
	if !errors.Is(err, domain.ErrDeploymentNotPendingApproval) {
		t.Errorf("expected ErrDeploymentNotPendingApproval, got %v", err)
	}
}

func TestInitiateDeploy_ScanFailure_MarksDeploymentAndAppFailed(t *testing.T) {
	app, build := builtApp("app-1", "overtime")
	svc, lifecycle, _, runtime, scanner, _ := newDeployService(app, build, "owner-1")
	scanner.defaultReport = domain.ScanReport{Passed: false, CriticalCount: 3}

	d, err := svc.InitiateDeploy(context.Background(), "app-1", "owner-1", domain.EnvironmentDev)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Status != domain.DeploymentFailed {
		t.Errorf("expected Failed, got %q", d.Status)
	}
	if lifecycle.apps["app-1"].LifecycleStatus != domain.StatusFailed {
		t.Errorf("expected application Failed, got %q", lifecycle.apps["app-1"].LifecycleStatus)
	}
	if len(runtime.started) != 0 {
		t.Error("expected no containers started after a failed scan")
	}
}

func TestInitiateDeploy_HealthCheckFailure_FirstDeploy_MarksAppFailed(t *testing.T) {
	app, build := builtApp("app-1", "overtime")
	svc, lifecycle, _, runtime, _, _ := newDeployService(app, build, "owner-1")
	runtime.healthCheckErr = errors.New("connection refused")

	d, err := svc.InitiateDeploy(context.Background(), "app-1", "owner-1", domain.EnvironmentDev)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Status != domain.DeploymentFailed {
		t.Errorf("expected Failed, got %q", d.Status)
	}
	if lifecycle.apps["app-1"].LifecycleStatus != domain.StatusFailed {
		t.Errorf("expected application Failed (no prior good version existed), got %q", lifecycle.apps["app-1"].LifecycleStatus)
	}
	if len(runtime.stopped) != 1 {
		t.Errorf("expected the failed attempt's container to be cleaned up, got %v", runtime.stopped)
	}
}

func TestInitiateDeploy_HealthCheckFailure_Redeploy_LeavesAppRunning(t *testing.T) {
	app, build := builtApp("app-1", "overtime")
	svc, lifecycle, _, runtime, _, _ := newDeployService(app, build, "owner-1")

	// First deploy succeeds normally.
	if _, err := svc.InitiateDeploy(context.Background(), "app-1", "owner-1", domain.EnvironmentDev); err != nil {
		t.Fatalf("setup (first deploy): %v", err)
	}
	if lifecycle.apps["app-1"].LifecycleStatus != domain.StatusRunning {
		t.Fatalf("setup: expected app Running after first deploy, got %q", lifecycle.apps["app-1"].LifecycleStatus)
	}

	// A redeploy attempt then fails its health check.
	runtime.healthCheckErr = errors.New("crash loop")
	d, err := svc.InitiateDeploy(context.Background(), "app-1", "owner-1", domain.EnvironmentDev)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Status != domain.DeploymentFailed {
		t.Errorf("expected the redeploy attempt itself marked Failed, got %q", d.Status)
	}
	if lifecycle.apps["app-1"].LifecycleStatus != domain.StatusRunning {
		t.Errorf("FR-044: a failed redeploy must not take down the currently Running version, got %q", lifecycle.apps["app-1"].LifecycleStatus)
	}
}

func TestInitiateDeploy_SuccessfulRedeploy_SupersedesPreviousAndStopsItsContainers(t *testing.T) {
	app, build := builtApp("app-1", "overtime")
	svc, _, deployments, runtime, _, _ := newDeployService(app, build, "owner-1")

	first, err := svc.InitiateDeploy(context.Background(), "app-1", "owner-1", domain.EnvironmentDev)
	if err != nil {
		t.Fatalf("setup (first deploy): %v", err)
	}
	firstContainerID := first.Containers["api"].ContainerID

	second, err := svc.InitiateDeploy(context.Background(), "app-1", "owner-1", domain.EnvironmentDev)
	if err != nil {
		t.Fatalf("second deploy: %v", err)
	}
	if second.Status != domain.DeploymentRunning {
		t.Fatalf("expected second deployment Running, got %q", second.Status)
	}
	if deployments.byID[first.ID].Status != domain.DeploymentSuperseded {
		t.Errorf("expected first deployment marked Superseded, got %q", deployments.byID[first.ID].Status)
	}
	found := false
	for _, stopped := range runtime.stopped {
		if stopped == firstContainerID {
			found = true
		}
	}
	if !found {
		t.Errorf("expected the superseded deployment's container to be stopped, stopped=%v", runtime.stopped)
	}
}

func TestInitiateDeploy_NoSuccessfulBuild_Rejected(t *testing.T) {
	app, build := builtApp("app-1", "overtime")
	build.Status = domain.BuildFailed
	svc, _, _, _, _, _ := newDeployService(app, build, "owner-1")

	_, err := svc.InitiateDeploy(context.Background(), "app-1", "owner-1", domain.EnvironmentDev)
	if !errors.Is(err, domain.ErrNoSuccessfulBuild) {
		t.Errorf("expected ErrNoSuccessfulBuild, got %v", err)
	}
}

func TestInitiateDeploy_NonOwner_Rejected(t *testing.T) {
	app, build := builtApp("app-1", "overtime")
	svc, _, _, _, _, _ := newDeployService(app, build, "owner-1")

	_, err := svc.InitiateDeploy(context.Background(), "app-1", "stranger", domain.EnvironmentDev)
	if !errors.Is(err, domain.ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized, got %v", err)
	}
}

func TestInitiateDeploy_InvalidEnvironment_Rejected(t *testing.T) {
	app, build := builtApp("app-1", "overtime")
	svc, _, _, _, _, _ := newDeployService(app, build, "owner-1")

	_, err := svc.InitiateDeploy(context.Background(), "app-1", "owner-1", domain.Environment("staging"))
	if !errors.Is(err, domain.ErrInvalidEnvironment) {
		t.Errorf("expected ErrInvalidEnvironment, got %v", err)
	}
}

func TestInitiateDeploy_AlreadyInFlight_Rejected(t *testing.T) {
	app, build := builtApp("app-1", "overtime")
	svc, _, _, _, _, _ := newDeployService(app, build, "owner-1")

	if _, err := svc.InitiateDeploy(context.Background(), "app-1", "owner-1", domain.EnvironmentProduction); err != nil {
		t.Fatalf("setup: %v", err) // leaves a pending_approval deployment in flight
	}

	_, err := svc.InitiateDeploy(context.Background(), "app-1", "owner-1", domain.EnvironmentDev)
	if !errors.Is(err, domain.ErrDeploymentAlreadyInFlight) {
		t.Errorf("expected ErrDeploymentAlreadyInFlight, got %v", err)
	}
}

// deployASecondVersion simulates a second Build-state run producing a new
// build, then deploys it, so the test ends up with two distinct successful
// deployments (v1 now Superseded, v2 now Running) to roll back between.
func deployASecondVersion(t *testing.T, svc *service.DeploymentService, builds *fakeBuildRepo, appID, ownerID string) domain.Deployment {
	t.Helper()
	v2 := domain.Build{
		ID: "build-v2", ApplicationID: appID, Status: domain.BuildSucceeded,
		ImageRefs: map[string]string{"api": "platform-build/v2-api:latest"},
	}
	builds.byID[v2.ID] = v2
	builds.byApp[appID] = v2.ID

	d, err := svc.InitiateDeploy(context.Background(), appID, ownerID, domain.EnvironmentDev)
	if err != nil {
		t.Fatalf("setup (second deploy): %v", err)
	}
	if d.Status != domain.DeploymentRunning {
		t.Fatalf("setup: expected second deployment Running, got %q (failure=%v)", d.Status, d.FailureReason)
	}
	return d
}

func TestRollback_ToSupersededVersion_ReactivatesItAndSupersedesCurrent(t *testing.T) {
	app, build := builtApp("app-1", "overtime")
	svc, lifecycle, deployments, runtime, _, builds := newDeployService(app, build, "owner-1")

	v1, err := svc.InitiateDeploy(context.Background(), "app-1", "owner-1", domain.EnvironmentDev)
	if err != nil {
		t.Fatalf("setup (first deploy): %v", err)
	}
	v2 := deployASecondVersion(t, svc, builds, "app-1", "owner-1")
	v2ContainerID := v2.Containers["api"].ContainerID

	rolledBack, err := svc.Rollback(context.Background(), "app-1", "owner-1", v1.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rolledBack.Status != domain.DeploymentRunning {
		t.Errorf("expected rollback deployment Running, got %q (failure=%v)", rolledBack.Status, rolledBack.FailureReason)
	}
	if rolledBack.BuildID != v1.BuildID {
		t.Errorf("expected rollback to redeploy v1's build %q, got %q", v1.BuildID, rolledBack.BuildID)
	}
	if rolledBack.ID == v1.ID {
		t.Errorf("expected rollback to create a NEW deployment record, not reuse v1's, for FR-101 auditability")
	}
	if lifecycle.apps["app-1"].LifecycleStatus != domain.StatusRunning {
		t.Errorf("expected application Running after a successful rollback, got %q", lifecycle.apps["app-1"].LifecycleStatus)
	}
	if deployments.byID[v2.ID].Status != domain.DeploymentSuperseded {
		t.Errorf("expected v2 marked Superseded by the rollback, got %q", deployments.byID[v2.ID].Status)
	}
	found := false
	for _, stopped := range runtime.stopped {
		if stopped == v2ContainerID {
			found = true
		}
	}
	if !found {
		t.Errorf("expected v2's container to be stopped on rollback cutover, stopped=%v", runtime.stopped)
	}
}

func TestRollback_FromFailedApplication_Succeeds(t *testing.T) {
	app, build := builtApp("app-1", "overtime")
	svc, lifecycle, _, _, _, _ := newDeployService(app, build, "owner-1")

	v1, err := svc.InitiateDeploy(context.Background(), "app-1", "owner-1", domain.EnvironmentDev)
	if err != nil {
		t.Fatalf("setup (first deploy): %v", err)
	}
	// A bad forward deploy fails outright (simulated directly — FR-044 already
	// covers the case where a failed redeploy leaves a Running app untouched;
	// here we force the app to Failed to exercise rollback's OTHER valid
	// starting state).
	if _, err := lifecycle.UpdateLifecycleStatus(context.Background(), "app-1", domain.StatusRunning, domain.StatusFailed, false); err != nil {
		t.Fatalf("setup: %v", err)
	}

	rolledBack, err := svc.Rollback(context.Background(), "app-1", "owner-1", v1.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rolledBack.Status != domain.DeploymentRunning {
		t.Errorf("expected rollback deployment Running, got %q", rolledBack.Status)
	}
	if lifecycle.apps["app-1"].LifecycleStatus != domain.StatusRunning {
		t.Errorf("expected application recovered to Running, got %q", lifecycle.apps["app-1"].LifecycleStatus)
	}
}

func TestRollback_TargetStillFailed_Rejected(t *testing.T) {
	app, build := builtApp("app-1", "overtime")
	svc, _, deployments, _, _, _ := newDeployService(app, build, "owner-1")

	if _, err := svc.InitiateDeploy(context.Background(), "app-1", "owner-1", domain.EnvironmentDev); err != nil {
		t.Fatalf("setup: %v", err)
	}
	// Fabricate a Failed deployment record to attempt rolling back to — this
	// must never be a valid rollback target (FR-100).
	failed := domain.Deployment{ID: "deploy-failed", ApplicationID: "app-1", BuildID: build.ID, Status: domain.DeploymentFailed}
	deployments.byID[failed.ID] = failed

	_, err := svc.Rollback(context.Background(), "app-1", "owner-1", failed.ID)
	if !errors.Is(err, domain.ErrInvalidRollbackTarget) {
		t.Errorf("expected ErrInvalidRollbackTarget, got %v", err)
	}
}

func TestRollback_TargetBelongsToDifferentApplication_Rejected(t *testing.T) {
	app, build := builtApp("app-1", "overtime")
	svc, _, deployments, _, _, _ := newDeployService(app, build, "owner-1")

	if _, err := svc.InitiateDeploy(context.Background(), "app-1", "owner-1", domain.EnvironmentDev); err != nil {
		t.Fatalf("setup: %v", err)
	}
	other := domain.Deployment{ID: "deploy-other-app", ApplicationID: "app-2", BuildID: build.ID, Status: domain.DeploymentRunning}
	deployments.byID[other.ID] = other

	_, err := svc.Rollback(context.Background(), "app-1", "owner-1", other.ID)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected ErrNotFound (cross-application target must not be reachable), got %v", err)
	}
}

func TestRollback_ApplicationNotRunningOrFailed_Rejected(t *testing.T) {
	app, build := builtApp("app-1", "overtime")
	app.LifecycleStatus = domain.StatusValidated // never deployed — nothing to roll back to or from
	svc, _, _, _, _, _ := newDeployService(app, build, "owner-1")

	_, err := svc.Rollback(context.Background(), "app-1", "owner-1", "does-not-matter")
	if !errors.Is(err, domain.ErrInvalidLifecycleTransition) {
		t.Errorf("expected ErrInvalidLifecycleTransition, got %v", err)
	}
}

func TestRollback_NonOwner_Rejected(t *testing.T) {
	app, build := builtApp("app-1", "overtime")
	svc, _, _, _, _, _ := newDeployService(app, build, "owner-1")

	v1, err := svc.InitiateDeploy(context.Background(), "app-1", "owner-1", domain.EnvironmentDev)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	_, err = svc.Rollback(context.Background(), "app-1", "stranger", v1.ID)
	if !errors.Is(err, domain.ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized, got %v", err)
	}
}

func TestDeploymentHistory_ReturnsAllAttemptsNewestFirst(t *testing.T) {
	app, build := builtApp("app-1", "overtime")
	svc, _, _, _, _, builds := newDeployService(app, build, "owner-1")

	v1, err := svc.InitiateDeploy(context.Background(), "app-1", "owner-1", domain.EnvironmentDev)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	v2 := deployASecondVersion(t, svc, builds, "app-1", "owner-1")

	history, err := svc.DeploymentHistory(context.Background(), "app-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("expected 2 deployments in history, got %d", len(history))
	}
	if history[0].ID != v2.ID || history[1].ID != v1.ID {
		t.Errorf("expected newest-first ordering [%s, %s], got [%s, %s]", v2.ID, v1.ID, history[0].ID, history[1].ID)
	}
}
