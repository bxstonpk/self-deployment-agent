package service_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"platform-api/internal/domain"
	"platform-api/internal/service"
)

type fakeTestRunRepo struct {
	mu   sync.Mutex
	runs map[string]domain.TestRun
	n    int
}

func (f *fakeTestRunRepo) Create(ctx context.Context, applicationID, requestedBy, digest string) (domain.TestRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	r := domain.TestRun{ID: "run-" + string(rune('0'+f.n)), ApplicationID: applicationID, RequestedBy: requestedBy,
		Status: domain.TestRunRunning, SourceDigest: digest, StartedAt: time.Now()}
	f.runs[r.ID] = r
	return r, nil
}

func (f *fakeTestRunRepo) Finish(ctx context.Context, id string, status domain.TestRunStatus, report string) (domain.TestRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.runs[id]
	now := time.Now()
	r.Status, r.Report, r.FinishedAt = status, report, &now
	f.runs[id] = r
	return r, nil
}

func (f *fakeTestRunRepo) Get(ctx context.Context, id string) (domain.TestRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.runs[id]
	if !ok {
		return domain.TestRun{}, domain.ErrNotFound
	}
	return r, nil
}

type fakeTestEngine struct {
	results []domain.ServiceTestResult
	block   chan struct{}
	started chan struct{}
	last    service.TestEngineRequest
}

func (f *fakeTestEngine) RunTests(ctx context.Context, req service.TestEngineRequest) []domain.ServiceTestResult {
	f.last = req
	if f.started != nil {
		close(f.started)
	}
	if f.block != nil {
		<-f.block
	}
	return f.results
}

type fakeSources map[string][]byte

func (f fakeSources) Open(applicationID, buildID string) (io.ReadCloser, int64, error) {
	b, ok := f[applicationID+"/"+buildID]
	if !ok {
		return nil, 0, domain.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), int64(len(b)), nil
}

const testedYAML = `
app:
  name: leave
  owner: HR
services:
  api:
    runtime: go
    port: 8080
`

type testRunFixture struct {
	svc     *service.TestRunService
	engine  *fakeTestEngine
	builds  *fakeBuildRepo
	sources fakeSources
	audit   *fakeAuditRecorder
}

func newTestRuns(t *testing.T) testRunFixture {
	t.Helper()
	app := domain.Application{ID: "app-1", Name: "leave", LifecycleStatus: domain.StatusRunning, DeploymentYAMLDraft: testedYAML}
	owners := newFakeOwnerRepo()
	owners.owners["app-1"] = []domain.ApplicationOwner{{ApplicationID: "app-1", UserID: "owner-1", Status: "active"}}
	engine := &fakeTestEngine{results: []domain.ServiceTestResult{{Service: "api", Runtime: "go", Status: domain.TestRunPassed, Output: "== go test ./...\nok  leave 0.01s"}}}
	builds := newFakeBuildRepo()
	sources := fakeSources{}
	audit := newFakeAuditRecorder()
	svc := service.NewTestRunService(newFakeLifecycleRepo(app), owners, &fakeTestRunRepo{runs: map[string]domain.TestRun{}},
		newFakeBaseImageRepo(), builds, sources, engine, audit, service.TestLimits{MemoryBytes: 1 << 30, CPUs: 2}, time.Minute, 1)
	return testRunFixture{svc: svc, engine: engine, builds: builds, sources: sources, audit: audit}
}

var leaveSource = map[string]string{"api/main.go": "package main\n", "api/main_test.go": "package main\n"}

func TestRunTests_ThePlatformsReportSaysWhatRanOnWhichSource(t *testing.T) {
	f := newTestRuns(t)
	archive := tarGz(t, leaveSource)
	run, results, err := f.svc.Run(context.Background(), "app-1", "owner-1", archive)
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := domain.SourceTreeDigest(archive)
	if run.Status != domain.TestRunPassed || run.SourceDigest != digest || len(results) != 1 {
		t.Fatalf("run: %+v", run)
	}
	for _, want := range []string{"Tests run by the platform", "test run: " + run.ID, "source-digest: " + digest,
		"result: passed", "== api (go): passed", "ok  leave 0.01s"} {
		if !strings.Contains(run.Report, want) {
			t.Errorf("report lacks %q:\n%s", want, run.Report)
		}
	}
	if f.engine.last.Services["api"].BaseImage == "" || f.engine.last.Limits.MemoryBytes != 1<<30 {
		t.Fatalf("the engine gets the governed image and the limits: %+v", f.engine.last)
	}
	if e := f.audit.all(); len(e) != 1 || e[0].Action != domain.AuditActionRunTests || e[0].Outcome != domain.AuditSuccess {
		t.Fatalf("audited: %+v", e)
	}
}

func TestRunTests_OneFailingServiceFailsTheRun(t *testing.T) {
	f := newTestRuns(t)
	f.engine.results = []domain.ServiceTestResult{
		{Service: "api", Runtime: "go", Status: domain.TestRunFailed, Output: "--- FAIL: TestLeave"},
		{Service: "frontend", Runtime: "react", Status: domain.TestRunPassed},
	}
	run, _, err := f.svc.Run(context.Background(), "app-1", "owner-1", tarGz(t, leaveSource))
	if err != nil || run.Status != domain.TestRunFailed || !strings.Contains(run.Report, "--- FAIL: TestLeave") {
		t.Fatalf("%v %v\n%s", run.Status, err, run.Report)
	}
}

func TestRunTests_OwnersOnly(t *testing.T) {
	f := newTestRuns(t)
	if _, _, err := f.svc.Run(context.Background(), "app-1", "someone-else", tarGz(t, leaveSource)); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized, got %v", err)
	}
}

func TestRunTests_AnotherRunWhileOneIsRunningIsRefusedNotQueued(t *testing.T) {
	f := newTestRuns(t)
	f.engine.block, f.engine.started = make(chan struct{}), make(chan struct{})
	done := make(chan error)
	go func() {
		_, _, err := f.svc.Run(context.Background(), "app-1", "owner-1", tarGz(t, leaveSource))
		done <- err
	}()
	<-f.engine.started
	if _, _, err := f.svc.Run(context.Background(), "app-1", "owner-1", tarGz(t, leaveSource)); !errors.Is(err, domain.ErrTestRunBusy) {
		t.Fatalf("want ErrTestRunBusy, got %v", err)
	}
	close(f.engine.block)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func succeededBuild(t *testing.T, f testRunFixture, source map[string]string) {
	t.Helper()
	b, _ := f.builds.Create(context.Background(), "app-1", "owner-1")
	_, _ = f.builds.MarkSucceeded(context.Background(), b.ID, map[string]string{"api": "img"})
	f.sources["app-1/"+b.ID] = tarGz(t, source)
}

func TestReportForDeploy_OnlyAPassedRunOfTheSameSource(t *testing.T) {
	f := newTestRuns(t)
	ctx := context.Background()
	run, _, err := f.svc.Run(ctx, "app-1", "owner-1", tarGz(t, leaveSource))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := f.svc.ReportForDeploy(ctx, "app-1", "owner-1", run.ID); !errors.Is(err, domain.ErrNoSuccessfulBuild) {
		t.Fatalf("no build yet: %v", err)
	}

	// The same files, packed again (a new gzip): the same source.
	succeededBuild(t, f, leaveSource)
	report, err := f.svc.ReportForDeploy(ctx, "app-1", "owner-1", run.ID)
	if err != nil || report != run.Report {
		t.Fatalf("same source: %v", err)
	}

	changed := map[string]string{"api/main.go": "package main // changed\n", "api/main_test.go": "package main\n"}
	succeededBuild(t, f, changed)
	if _, err := f.svc.ReportForDeploy(ctx, "app-1", "owner-1", run.ID); !errors.Is(err, domain.ErrTestRunSourceMismatch) {
		t.Fatalf("changed source: %v", err)
	}

	f.engine.results = []domain.ServiceTestResult{{Service: "api", Runtime: "go", Status: domain.TestRunFailed}}
	failed, _, _ := f.svc.Run(ctx, "app-1", "owner-1", tarGz(t, changed))
	if _, err := f.svc.ReportForDeploy(ctx, "app-1", "owner-1", failed.ID); !errors.Is(err, domain.ErrTestRunNotPassed) {
		t.Fatalf("failed run: %v", err)
	}
	if _, err := f.svc.ReportForDeploy(ctx, "app-1", "someone-else", run.ID); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("not an owner: %v", err)
	}
}

func TestSourceTreeDigest_IsAboutTheFilesNotTheArchive(t *testing.T) {
	a, _ := domain.SourceTreeDigest(tarGz(t, leaveSource))
	time.Sleep(1100 * time.Millisecond) // a different gzip header time
	b, _ := domain.SourceTreeDigest(tarGz(t, leaveSource))
	c, _ := domain.SourceTreeDigest(tarGz(t, map[string]string{"api/main.go": "package main\n"}))
	if a == "" || a != b || a == c {
		t.Fatalf("same files same digest, different files different: %s %s %s", a, b, c)
	}
}
