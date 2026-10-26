package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"platform-api/internal/domain"
	"platform-api/internal/service"
)

type fakeAdminHolds struct {
	holds map[string]domain.AdminHold
}

func newFakeAdminHolds() *fakeAdminHolds {
	return &fakeAdminHolds{holds: map[string]domain.AdminHold{}}
}

func (f *fakeAdminHolds) Place(ctx context.Context, applicationID, heldBy, reason string) (domain.AdminHold, error) {
	h := domain.AdminHold{ApplicationID: applicationID, Reason: reason, HeldBy: heldBy,
		HeldByEmail: "admin@sti-th.com", HeldAt: time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)}
	f.holds[applicationID] = h
	return h, nil
}

func (f *fakeAdminHolds) Get(ctx context.Context, applicationID string) (domain.AdminHold, error) {
	h, ok := f.holds[applicationID]
	if !ok {
		return domain.AdminHold{}, domain.ErrNotFound
	}
	return h, nil
}

func (f *fakeAdminHolds) Release(ctx context.Context, applicationID string) error {
	delete(f.holds, applicationID)
	return nil
}

var (
	holdAdmin = domain.User{ID: "admin-1", Email: "admin@sti-th.com"}
	notAdmin  = domain.User{ID: "owner-1", Email: "owner@sti-th.com"}
)

func heldLifecycle(t *testing.T) (*service.LifecycleService, *fakeLifecycleRepo, *fakeDeploymentRepo, *fakeRuntime,
	*fakeAdminHolds, *fakeNotificationRecorder) {
	t.Helper()
	app, deployment := runningAppAndDeployment("app-1", "dep-1")
	svc, apps, deployments, states, runtime := newLifecycleService(app, deployment, "owner-1")
	front := "c-front"
	port := 111
	states.byKey[stateKey("dep-1", "frontend")] = domain.ServiceRuntimeState{
		DeploymentID: "dep-1", ServiceName: "frontend", ImageRef: "img-front", ContainerPort: 3000,
		ContainerID: &front, HostPort: &port,
	}
	holds, notifier := newFakeAdminHolds(), newFakeNotificationRecorder()
	svc.SetAdminHolds(holds, fakeGovernanceAdmins{}, notifier)
	return svc, apps, deployments, runtime, holds, notifier
}

func TestAdminSuspend_StopsItHoldsItAndTellsTheOwners(t *testing.T) {
	svc, apps, deployments, runtime, holds, notifier := heldLifecycle(t)

	hold, err := svc.AdminSuspend(context.Background(), "app-1", holdAdmin, "  leaks customer data  ")
	if err != nil {
		t.Fatalf("admin suspend: %v", err)
	}
	if hold.Reason != "leaks customer data" || holds.holds["app-1"].HeldBy != "admin-1" {
		t.Errorf("hold = %+v", hold)
	}
	if apps.apps["app-1"].LifecycleStatus != domain.StatusSuspended || deployments.byID["dep-1"].Status != domain.DeploymentSuspended {
		t.Errorf("not suspended: app %q, deployment %q", apps.apps["app-1"].LifecycleStatus, deployments.byID["dep-1"].Status)
	}
	if len(runtime.stopped) != 1 {
		t.Errorf("stopped %v", runtime.stopped)
	}
	calls := notifier.all()
	if len(calls) != 1 || !strings.Contains(calls[0].Detail, "leaks customer data") ||
		!strings.Contains(calls[0].Detail, "Only a platform administrator can resume it") {
		t.Errorf("owners told %+v", calls)
	}
}

func TestAdminSuspend_OnlyAnAdministrator(t *testing.T) {
	svc, apps, _, _, holds, _ := heldLifecycle(t)
	if _, err := svc.AdminSuspend(context.Background(), "app-1", notAdmin, "x"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Errorf("non-admin: %v", err)
	}
	if len(holds.holds) != 0 || apps.apps["app-1"].LifecycleStatus != domain.StatusRunning {
		t.Error("a non-admin's attempt changed something")
	}
}

func TestAdminSuspend_NeedsAReason(t *testing.T) {
	svc, _, _, _, _, _ := heldLifecycle(t)
	if _, err := svc.AdminSuspend(context.Background(), "app-1", holdAdmin, "   "); !errors.Is(err, domain.ErrHoldReasonRequired) {
		t.Errorf("blank reason: %v", err)
	}
}

func TestAdminSuspend_HoldsOneItsOwnersAlreadySuspended(t *testing.T) {
	svc, apps, _, _, holds, _ := heldLifecycle(t)
	if _, err := svc.Suspend(context.Background(), "app-1", "owner-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AdminSuspend(context.Background(), "app-1", holdAdmin, "security review"); err != nil {
		t.Fatalf("admin suspend of a suspended app: %v", err)
	}
	if _, ok := holds.holds["app-1"]; !ok || apps.apps["app-1"].LifecycleStatus != domain.StatusSuspended {
		t.Error("expected it held and still suspended")
	}
}

func TestAdminHold_OwnersCantResumeArchiveOrDelete(t *testing.T) {
	svc, apps, _, _, _, _ := heldLifecycle(t)
	ctx := context.Background()
	if _, err := svc.AdminSuspend(ctx, "app-1", holdAdmin, "security review"); err != nil {
		t.Fatal(err)
	}

	_, err := svc.Resume(ctx, "app-1", "owner-1")
	if !errors.Is(err, domain.ErrAdminHold) || !strings.Contains(err.Error(), "security review") ||
		!strings.Contains(err.Error(), "admin@sti-th.com") {
		t.Errorf("owner resume: %v", err)
	}
	if _, err := svc.Archive(ctx, "app-1", "owner-1"); !errors.Is(err, domain.ErrAdminHold) {
		t.Errorf("owner archive: %v", err)
	}
	if _, err := svc.Delete(ctx, "app-1", "owner-1", true); !errors.Is(err, domain.ErrAdminHold) {
		t.Errorf("owner delete: %v", err)
	}
	if apps.apps["app-1"].LifecycleStatus != domain.StatusSuspended {
		t.Errorf("status moved to %q", apps.apps["app-1"].LifecycleStatus)
	}
}

func TestAdminResume_StartsItAndLiftsTheHold(t *testing.T) {
	svc, apps, deployments, runtime, holds, notifier := heldLifecycle(t)
	ctx := context.Background()
	if _, err := svc.AdminSuspend(ctx, "app-1", holdAdmin, "security review"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AdminResume(ctx, "app-1", notAdmin); !errors.Is(err, domain.ErrUnauthorized) {
		t.Errorf("non-admin resume: %v", err)
	}

	app, err := svc.AdminResume(ctx, "app-1", holdAdmin)
	if err != nil {
		t.Fatalf("admin resume: %v", err)
	}
	if app.LifecycleStatus != domain.StatusRunning || apps.apps["app-1"].LifecycleStatus != domain.StatusRunning ||
		deployments.byID["dep-1"].Status != domain.DeploymentRunning {
		t.Errorf("not running: %q / %q", app.LifecycleStatus, deployments.byID["dep-1"].Status)
	}
	if len(runtime.started) != 1 {
		t.Errorf("started %v", runtime.started)
	}
	if len(holds.holds) != 0 {
		t.Error("hold still on")
	}
	if calls := notifier.all(); len(calls) != 2 || !strings.Contains(calls[1].Title, "resumed by a platform administrator") {
		t.Errorf("owners told %+v", calls)
	}
	// And the owners have their application back.
	if _, err := svc.Suspend(ctx, "app-1", "owner-1"); err != nil {
		t.Errorf("owner suspend after the hold: %v", err)
	}
	if _, err := svc.Resume(ctx, "app-1", "owner-1"); err != nil {
		t.Errorf("owner resume after the hold: %v", err)
	}
}

func TestAdminResume_OnlyAnAdministratorsSuspension(t *testing.T) {
	svc, _, _, _, _, _ := heldLifecycle(t)
	if _, err := svc.Suspend(context.Background(), "app-1", "owner-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AdminResume(context.Background(), "app-1", holdAdmin); !errors.Is(err, domain.ErrNotAdminHeld) {
		t.Errorf("admin resume of an owner's suspension: %v", err)
	}
}

func TestAdminResume_KeepsTheHoldIfItDoesntStart(t *testing.T) {
	svc, _, _, runtime, holds, _ := heldLifecycle(t)
	ctx := context.Background()
	if _, err := svc.AdminSuspend(ctx, "app-1", holdAdmin, "security review"); err != nil {
		t.Fatal(err)
	}
	runtime.healthCheckErr = errors.New("unhealthy")
	if _, err := svc.AdminResume(ctx, "app-1", holdAdmin); err == nil {
		t.Fatal("expected the failed start to be reported")
	}
	if _, ok := holds.holds["app-1"]; !ok {
		t.Error("the hold was lifted though the application didn't start")
	}
}
