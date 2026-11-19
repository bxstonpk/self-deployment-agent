package service_test

import (
	"context"
	"slices"
	"testing"

	"platform-api/internal/domain"
	"platform-api/internal/service"
)

func newScaleServiceOver(runtime service.RuntimeEngine, deployments *fakeRunningDeploymentLookup) *service.ScaleService {
	return service.NewScaleService(&fakeAppByNameRepo{byName: map[string]domain.Application{}}, deployments,
		newFakeServiceRuntimeStateRepo(), &fakeScaleEventRepo{}, newFakeStackRepo(), runtime, newFakeDatabaseService())
}

// listingRuntime is the fake runtime with Docker's list of application
// containers, for the orphan sweep.
type listingRuntime struct {
	*fakeRuntime
	containers []domain.PlatformContainer
}

func (l *listingRuntime) ListPlatformContainers(ctx context.Context) ([]domain.PlatformContainer, error) {
	return l.containers, nil
}

// A service that scaled to zero and was woken runs in a container only its
// runtime state knows: superseding the deployment must stop that one too,
// not only the containers the deployment started with (found on the LAN
// host as 38 superseded deployments' API containers, still running).
func TestCleanupForDeployment_StopsTheContainerAColdStartMade(t *testing.T) {
	svc, states, _, runtime := newScaleService()
	ctx := context.Background()
	if err := svc.InitializeForDeployment(ctx, "dep-old", multiServiceYAML,
		map[string]string{"frontend": "img-front", "api": "img-api"},
		map[string]domain.RunningContainer{
			"frontend": {ContainerID: "c-front", HostPort: 10001},
			"api":      {ContainerID: "c-api", HostPort: 10002},
		}); err != nil {
		t.Fatal(err)
	}
	// Scaled to zero, then woken: a new container the deployment's own
	// list never had.
	if _, err := states.ClearContainer(ctx, "dep-old", "api", "c-api"); err != nil {
		t.Fatal(err)
	}
	if err := states.SetContainer(ctx, "dep-old", "api", "c-woken", 10003); err != nil {
		t.Fatal(err)
	}

	if err := svc.CleanupForDeployment(ctx, "dep-old"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(runtime.stopped, "c-woken") || !slices.Contains(runtime.stopped, "c-front") {
		t.Fatalf("stopped %v, want the woken API container and the frontend", runtime.stopped)
	}
	if left, _ := states.ListForDeployment(ctx, "dep-old"); len(left) != 0 {
		t.Fatalf("runtime state left behind: %v", left)
	}
}

func TestSweepOrphans_RemovesContainersOfDeploymentsNoLongerLive(t *testing.T) {
	_, _, _, runtime, deployments := newScaleServiceWithDatabase(newFakeDatabaseService())
	for id, status := range map[string]domain.DeploymentStatus{
		"dep-live":      domain.DeploymentRunning,
		"dep-deploying": domain.DeploymentDeploying,
		"dep-old":       domain.DeploymentSuperseded,
		"dep-failed":    domain.DeploymentFailed,
		"dep-suspended": domain.DeploymentSuspended,
	} {
		deployments.byID[id] = domain.Deployment{ID: id, Status: status}
	}
	lister := &listingRuntime{fakeRuntime: runtime, containers: []domain.PlatformContainer{
		{ID: "c1", Name: "platform-run-app-frontend-live", DeploymentID: "dep-live"},
		{ID: "c2", Name: "platform-run-live-api", DeploymentID: "dep-live"},
		{ID: "c3", Name: "platform-run-new-api", DeploymentID: "dep-deploying"},
		{ID: "c4", Name: "platform-run-old-api", DeploymentID: "dep-old"},
		{ID: "c5", Name: "platform-run-old2-api", DeploymentID: "dep-old"},
		{ID: "c6", Name: "platform-run-failed-api", DeploymentID: "dep-failed"},
		{ID: "c7", Name: "platform-run-susp-api", DeploymentID: "dep-suspended"},
		{ID: "c8", Name: "platform-run-gone-api", DeploymentID: "dep-of-a-deleted-app"},
		{ID: "c9", Name: "platform-run-unlabelled"},
	}}
	sweeper := newScaleServiceOver(lister, deployments)

	n, err := sweeper.SweepOrphans(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// A suspended deployment's containers are kept: Resume starts them
	// before it marks the deployment Running again.
	want := []string{"c4", "c5", "c6", "c8"}
	slices.Sort(runtime.stopped)
	if n != len(want) || !slices.Equal(runtime.stopped, want) {
		t.Fatalf("removed %d: %v, want %v", n, runtime.stopped, want)
	}
}
