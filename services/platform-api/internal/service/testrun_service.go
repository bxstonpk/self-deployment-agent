// Tests run by the platform (DEC-029 part 2): an application's tests run on
// the platform, in the build sandbox, so an employee's machine needs no
// toolchain — and the report an approver signs is the platform's own.
//
// A deployment names a test run (test_run_id). The platform attaches that
// run's report only when the run passed and was made on the same source as
// the build being deployed: same files, same contents (domain.SourceTreeDigest),
// compared against the build's kept source archive.
package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"platform-api/internal/domain"
)

// TestLimits bound one service's test run.
type TestLimits struct {
	MemoryBytes int64
	CPUs        float64
}

type TestEngineRequest struct {
	RunID           string
	ApplicationName string
	Services        map[string]BuildServiceSpec
	SourceArchive   []byte
	Limits          TestLimits
}

// TestEngine runs each service's tests and reports every service, failing
// or not (internal/buildengine).
type TestEngine interface {
	RunTests(ctx context.Context, req TestEngineRequest) []domain.ServiceTestResult
}

type TestRunRepository interface {
	Create(ctx context.Context, applicationID, requestedBy, sourceDigest string) (domain.TestRun, error)
	Finish(ctx context.Context, id string, status domain.TestRunStatus, report string) (domain.TestRun, error)
	Get(ctx context.Context, id string) (domain.TestRun, error)
}

// SourceOpener reads a build's kept source archive (internal/sourcestore).
type SourceOpener interface {
	Open(applicationID, buildID string) (io.ReadCloser, int64, error)
}

// maxPlatformReportBytes leaves room, under the 64 KiB a deployment's test
// report may be, for notes the requester adds.
const maxPlatformReportBytes = 56 * 1024

var ErrNoServicesToTest = errors.New("the application has no saved deployment.yaml with services to test; save and validate it first")

type TestRunService struct {
	apps       ApplicationLifecycleRepository
	owners     ApplicationOwnerRepository
	runs       TestRunRepository
	baseImages BaseImageRepository
	builds     BuildRepository
	sources    SourceOpener
	engine     TestEngine
	audit      AuditRecorder
	limits     TestLimits
	timeout    time.Duration
	// slots is how many test runs the host takes at once; one more is
	// refused (ErrTestRunBusy) rather than queued behind a long one.
	slots chan struct{}
}

func NewTestRunService(apps ApplicationLifecycleRepository, owners ApplicationOwnerRepository, runs TestRunRepository,
	baseImages BaseImageRepository, builds BuildRepository, sources SourceOpener, engine TestEngine, audit AuditRecorder,
	limits TestLimits, timeout time.Duration, concurrent int) *TestRunService {
	if concurrent < 1 {
		concurrent = 1
	}
	return &TestRunService{apps: apps, owners: owners, runs: runs, baseImages: baseImages, builds: builds, sources: sources,
		engine: engine, audit: audit, limits: limits, timeout: timeout, slots: make(chan struct{}, concurrent)}
}

func (s *TestRunService) requireOwner(ctx context.Context, applicationID, userID string) (domain.Application, error) {
	app, err := s.apps.GetByID(ctx, applicationID)
	if err != nil {
		return domain.Application{}, err
	}
	if app.LifecycleStatus == domain.StatusDeleted {
		return domain.Application{}, domain.ErrNotFound
	}
	owners, err := s.owners.ListForApplication(ctx, applicationID)
	if err != nil {
		return domain.Application{}, err
	}
	for _, o := range owners {
		if o.UserID == userID && o.Status == "active" {
			return app, nil
		}
	}
	return domain.Application{}, domain.ErrUnauthorized
}

// Run runs the tests of every service the application's deployment.yaml
// declares, on the uploaded source (the same tar.gz a build takes).
func (s *TestRunService) Run(ctx context.Context, applicationID, requesterID string, archive []byte) (domain.TestRun, []domain.ServiceTestResult, error) {
	app, err := s.requireOwner(ctx, applicationID, requesterID)
	if err != nil {
		return domain.TestRun{}, nil, err
	}
	if len(archive) == 0 {
		return domain.TestRun{}, nil, domain.ErrNoSourceArchive
	}
	var parsed domain.DeploymentYAML
	if err := yaml.NewDecoder(bytes.NewReader([]byte(app.DeploymentYAMLDraft))).Decode(&parsed); err != nil || len(parsed.Services) == 0 {
		return domain.TestRun{}, nil, ErrNoServicesToTest
	}
	specs := make(map[string]BuildServiceSpec, len(parsed.Services))
	for name, svc := range parsed.Services {
		base, err := s.baseImages.GetForRuntime(ctx, svc.Runtime)
		if err != nil || base.Status != "active" {
			return domain.TestRun{}, nil, fmt.Errorf("%w: runtime %q", domain.ErrNoBaseImageForRuntime, svc.Runtime)
		}
		specs[name] = BuildServiceSpec{Runtime: svc.Runtime, Port: svc.Port, BaseImage: base.ImageReference}
	}
	digest, err := domain.SourceTreeDigest(archive)
	if err != nil {
		return domain.TestRun{}, nil, fmt.Errorf("%w: %v", domain.ErrUnreadableSource, err)
	}

	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		return domain.TestRun{}, nil, domain.ErrTestRunBusy
	}

	run, err := s.runs.Create(ctx, applicationID, requesterID, digest)
	if err != nil {
		return domain.TestRun{}, nil, err
	}
	runCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	results := s.engine.RunTests(runCtx, TestEngineRequest{
		RunID: run.ID, ApplicationName: app.Name, Services: specs, SourceArchive: archive, Limits: s.limits,
	})
	status := overallStatus(results)
	finished := time.Now()
	report := renderTestReport(run, app.Name, status, finished, results)

	// Recorded even when the caller has gone (see TriggerBuild's note on
	// detachedCtx): a run left "running" would never finish.
	detached, cancelDetached := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancelDetached()
	run, err = s.runs.Finish(detached, run.ID, status, report)
	if err != nil {
		return domain.TestRun{}, nil, err
	}
	outcome := domain.AuditSuccess
	if status != domain.TestRunPassed {
		outcome = domain.AuditFailure
	}
	if err := s.audit.Record(detached, domain.AuditEntry{
		ActorUserID: requesterID, Action: domain.AuditActionRunTests, ResourceType: "test_run", ResourceID: run.ID,
		Outcome: outcome, Detail: fmt.Sprintf("%s: %s (source %s)", app.Name, status, shortDigest(digest)),
	}); err != nil {
		return domain.TestRun{}, nil, fmt.Errorf("tests ran, but the audit trail failed to record it: %w", err)
	}
	return run, results, nil
}

func (s *TestRunService) Get(ctx context.Context, applicationID, requesterID, runID string) (domain.TestRun, error) {
	if _, err := s.requireOwner(ctx, applicationID, requesterID); err != nil {
		return domain.TestRun{}, err
	}
	run, err := s.runs.Get(ctx, runID)
	if err != nil {
		return domain.TestRun{}, err
	}
	if run.ApplicationID != applicationID {
		return domain.TestRun{}, domain.ErrNotFound
	}
	return run, nil
}

// ReportForDeploy is the report a deployment carries for a test run: only
// a passed run, and only of the same source as the build being deployed.
func (s *TestRunService) ReportForDeploy(ctx context.Context, applicationID, requesterID, runID string) (string, error) {
	run, err := s.Get(ctx, applicationID, requesterID, runID)
	if err != nil {
		return "", err
	}
	if run.Status != domain.TestRunPassed {
		return "", domain.ErrTestRunNotPassed
	}
	build, err := s.builds.LatestForApplication(ctx, applicationID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return "", domain.ErrNoSuccessfulBuild
		}
		return "", err
	}
	if build.Status != domain.BuildSucceeded {
		return "", domain.ErrNoSuccessfulBuild
	}
	if s.sources == nil {
		return "", errors.New("the platform keeps no build sources to compare the test run with")
	}
	f, _, err := s.sources.Open(applicationID, build.ID)
	if err != nil {
		return "", fmt.Errorf("read the build's source: %w", err)
	}
	defer f.Close()
	archive, err := io.ReadAll(f)
	if err != nil {
		return "", fmt.Errorf("read the build's source: %w", err)
	}
	digest, err := domain.SourceTreeDigest(archive)
	if err != nil {
		return "", fmt.Errorf("read the build's source: %w", err)
	}
	if digest != run.SourceDigest {
		return "", domain.ErrTestRunSourceMismatch
	}
	return run.Report, nil
}

func overallStatus(results []domain.ServiceTestResult) domain.TestRunStatus {
	status := domain.TestRunPassed
	for _, r := range results {
		switch r.Status {
		case domain.TestRunError:
			return domain.TestRunError
		case domain.TestRunFailed:
			status = domain.TestRunFailed
		}
	}
	if len(results) == 0 {
		return domain.TestRunError
	}
	return status
}

func shortDigest(d string) string {
	if len(d) > 12 {
		return d[:12]
	}
	return d
}

// renderTestReport is the report as the approver reads and signs it.
func renderTestReport(run domain.TestRun, appName string, status domain.TestRunStatus, finished time.Time, results []domain.ServiceTestResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Tests run by the platform (not reported by the requester)\n")
	fmt.Fprintf(&b, "test run: %s\napplication: %s\nsource-digest: %s\nresult: %s\n", run.ID, appName, run.SourceDigest, status)
	fmt.Fprintf(&b, "ran: %s to %s\n", run.StartedAt.UTC().Format(time.RFC3339), finished.UTC().Format(time.RFC3339))
	budget := maxPlatformReportBytes - b.Len() - 200*len(results)
	per := budget
	if len(results) > 0 {
		per = budget / len(results)
	}
	for _, r := range results {
		out := r.Output
		if len(out) > per {
			cut := len(out) - per
			for cut < len(out) && !utf8.RuneStart(out[cut]) {
				cut++ // never split a character (Thai test names)
			}
			out = "(earlier output not kept)\n" + out[cut:]
		}
		fmt.Fprintf(&b, "\n== %s (%s): %s\n%s\n", r.Service, r.Runtime, r.Status, strings.TrimRight(out, "\n"))
	}
	return b.String()
}
