package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"platform-api/internal/domain"
	"platform-api/internal/service"
)

type fakeDevCopyRepo struct {
	byApp map[string]domain.DevCopy
	names map[string]string
}

func (f *fakeDevCopyRepo) withNames(c domain.DevCopy) domain.DevCopy {
	c.Name, c.ParentName = f.names[c.ApplicationID], f.names[c.ParentID]
	return c
}

func (f *fakeDevCopyRepo) Live(ctx context.Context, parentID string) (domain.DevCopy, error) {
	for _, c := range f.byApp {
		if c.ParentID == parentID && c.EndedAt == nil {
			return f.withNames(c), nil
		}
	}
	return domain.DevCopy{}, domain.ErrNotFound
}

func (f *fakeDevCopyRepo) Of(ctx context.Context, applicationID string) (domain.DevCopy, error) {
	c, ok := f.byApp[applicationID]
	if !ok {
		return domain.DevCopy{}, domain.ErrNotFound
	}
	return f.withNames(c), nil
}

func (f *fakeDevCopyRepo) Create(ctx context.Context, c domain.DevCopy) (domain.DevCopy, error) {
	c.CreatedAt = time.Now()
	f.byApp[c.ApplicationID] = c
	return c, nil
}

func (f *fakeDevCopyRepo) End(ctx context.Context, applicationID string) error {
	c := f.byApp[applicationID]
	now := time.Now()
	c.EndedAt = &now
	f.byApp[applicationID] = c
	return nil
}

func (f *fakeDevCopyRepo) Orphaned(ctx context.Context) ([]domain.DevCopy, error) { return nil, nil }

func (f *fakeDevCopyRepo) AddTestData(ctx context.Context, applicationID, note string) (domain.DevCopy, error) {
	c := f.byApp[applicationID]
	c.TestData = true
	if c.TestDataNote != "" {
		note = c.TestDataNote + "\n" + note
	}
	c.TestDataNote = note
	f.byApp[applicationID] = c
	return f.withNames(c), nil
}

const devParentYAML = `
app:
  name: leave
  owner: HR
services:
  frontend:
    runtime: react
  api:
    runtime: go
    port: 8080
database:
  type: mysql
`

type devCopyFixture struct {
	svc      *service.DevCopyService
	apps     *fakeLifecycleRepo
	repo     *fakeDevCopyRepo
	calls    []string
	profiles map[string]domain.ApplicationProfile
	taken    map[string]bool
	build    domain.BuildStatus
	hasDB    bool
}

func newDevCopies(t *testing.T) *devCopyFixture {
	t.Helper()
	f := &devCopyFixture{build: domain.BuildSucceeded, taken: map[string]bool{}, hasDB: true,
		profiles: map[string]domain.ApplicationProfile{"app-1": {ApplicationID: "app-1", Purpose: "leave requests"}}}
	f.apps = newFakeLifecycleRepo(domain.Application{ID: "app-1", Name: "leave", LifecycleStatus: domain.StatusRunning,
		OwningDepartmentID: "hr", DeploymentYAMLDraft: devParentYAML})
	owners := newFakeOwnerRepo()
	owners.owners["app-1"] = []domain.ApplicationOwner{{ApplicationID: "app-1", UserID: "owner-1", Status: "active"}}
	f.repo = &fakeDevCopyRepo{byApp: map[string]domain.DevCopy{}, names: map[string]string{"app-1": "leave"}}
	n := 0
	steps := service.DevCopySteps{
		Register: func(ctx context.Context, in service.RegisterApplicationInput) (domain.Application, error) {
			f.calls = append(f.calls, "register "+in.Name)
			if f.taken[in.Name] {
				return domain.Application{}, domain.ErrNameTaken
			}
			n++
			app := domain.Application{ID: "dev-" + string(rune('0'+n)), Name: in.Name, LifecycleStatus: domain.StatusDraft,
				DeploymentYAMLDraft: in.DeploymentYAMLDraft}
			f.apps.apps[app.ID] = app
			f.repo.names[app.ID] = in.Name
			f.taken[in.Name] = true
			return app, nil
		},
		SaveYAML: func(ctx context.Context, id, who, y string) (domain.Application, error) {
			f.calls = append(f.calls, "save-yaml "+id)
			a := f.apps.apps[id]
			a.DeploymentYAMLDraft = y
			f.apps.apps[id] = a
			return a, nil
		},
		Validate: func(ctx context.Context, id, who string) (domain.ValidationReport, domain.Application, error) {
			f.calls = append(f.calls, "validate "+id)
			return domain.ValidationReport{Valid: true}, f.apps.apps[id], nil
		},
		Build: func(ctx context.Context, id, who string, archive []byte) (domain.Build, error) {
			f.calls = append(f.calls, "build "+id)
			return domain.Build{ID: "b-" + id, Status: f.build}, nil
		},
		Deploy: func(ctx context.Context, id, who string, env domain.Environment) (domain.Deployment, error) {
			f.calls = append(f.calls, "deploy "+id+" "+string(env))
			a := f.apps.apps[id]
			a.LifecycleStatus = domain.StatusRunning
			f.apps.apps[id] = a
			return domain.Deployment{ID: "d-" + id, Status: domain.DeploymentRunning, Environment: env}, nil
		},
		Archive: func(ctx context.Context, id, who string) (domain.Application, error) {
			f.calls = append(f.calls, "archive "+id)
			a := f.apps.apps[id]
			a.LifecycleStatus = domain.StatusArchived
			f.apps.apps[id] = a
			return a, nil
		},
		Delete: func(ctx context.Context, id, who string, confirm bool) (domain.Application, error) {
			f.calls = append(f.calls, "delete "+id)
			a := f.apps.apps[id]
			a.LifecycleStatus = domain.StatusDeleted
			f.apps.apps[id] = a
			return a, nil
		},
		GetProfile: func(ctx context.Context, id string) (domain.ApplicationProfile, error) {
			p, ok := f.profiles[id]
			if !ok {
				return domain.ApplicationProfile{}, domain.ErrNotFound
			}
			return p, nil
		},
		PutProfile: func(ctx context.Context, p domain.ApplicationProfile) (domain.ApplicationProfile, error) {
			f.profiles[p.ApplicationID] = p
			return p, nil
		},
		HasDatabase: func(ctx context.Context, id string) (bool, error) { return f.hasDB, nil },
		CopyDatabase: func(ctx context.Context, from, to, actor string) error {
			f.calls = append(f.calls, "copy-db "+from+" -> "+to)
			return nil
		},
	}
	f.svc = service.NewDevCopyService(f.apps, owners, f.repo, steps, newFakeAuditRecorder())
	return f
}

var devOwner = domain.User{ID: "owner-1", Email: "somchai@sti-th.com"}

func TestDevCopy_FirstDevDeployMakesTheCopyTheNextOnesReuseIt(t *testing.T) {
	f := newDevCopies(t)
	ctx := context.Background()
	res, err := f.svc.Deploy(ctx, devOwner, "app-1", []byte("v1"))
	if err != nil || !res.Created || res.Copy.Name != "leave-dev" {
		t.Fatalf("first: %+v %v", res, err)
	}
	want := []string{"register leave-dev", "validate dev-1", "build dev-1", "deploy dev-1 dev"}
	if strings.Join(f.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("steps\n got %v\nwant %v", f.calls, want)
	}
	var doc struct {
		App      struct{ Name string } `yaml:"app"`
		Database struct{ Type, Backup string }
	}
	_ = yaml.Unmarshal([]byte(f.apps.apps["dev-1"].DeploymentYAMLDraft), &doc)
	if doc.App.Name != "leave-dev" || doc.Database.Type != "mysql" || doc.Database.Backup != "none" {
		t.Fatalf("its deployment.yaml: %+v", doc)
	}
	if f.profiles["dev-1"].Purpose != "leave requests" {
		t.Fatal("the profile is copied")
	}

	f.calls = nil
	res, err = f.svc.Deploy(ctx, devOwner, "app-1", []byte("v2"))
	if err != nil || res.Created || res.Copy.ApplicationID != "dev-1" {
		t.Fatalf("second: %+v %v", res, err)
	}
	if strings.Join(f.calls, "|") != "build dev-1|deploy dev-1 dev" {
		t.Fatalf("the same running copy (and its database): built and deployed, nothing deleted: %v", f.calls)
	}
	// A service added to the application: the running copy takes it in
	// place, nothing reset (its database and data stay).
	a := f.apps.apps["app-1"]
		a.DeploymentYAMLDraft = strings.Replace(devParentYAML, "database:", "  worker:\n    runtime: go\n    port: 8081\ndatabase:", 1)
	f.apps.apps["app-1"] = a
	f.calls = nil
	if _, err := f.svc.Deploy(ctx, devOwner, "app-1", []byte("v3")); err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.calls, "|") != "save-yaml dev-1|build dev-1|deploy dev-1 dev" || !strings.Contains(f.apps.apps["dev-1"].DeploymentYAMLDraft, "worker") {
		t.Fatalf("the new contract saved on the copy, then built and deployed, nothing deleted: %v", f.calls)
	}
	if f.apps.apps["app-1"].LifecycleStatus != domain.StatusRunning {
		t.Fatal("the application itself is untouched")
	}
}

func TestDevCopy_ResetRemovesItAndTheNextIsANewOne(t *testing.T) {
	f := newDevCopies(t)
	ctx := context.Background()
	if _, err := f.svc.Deploy(ctx, devOwner, "app-1", []byte("v1")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Reset(ctx, devOwner, "app-1"); err != nil {
		t.Fatal(err)
	}
	if f.apps.apps["dev-1"].LifecycleStatus != domain.StatusDeleted {
		t.Fatal("its code and data go")
	}
	res, err := f.svc.Deploy(ctx, devOwner, "app-1", []byte("v2"))
	if err != nil || !res.Created || res.Copy.Name != "leave-dev2" {
		t.Fatalf("a name is never reused: %+v %v", res.Copy, err)
	}
}

func TestDevCopy_TestDataIsRecordedAndSignedAbout(t *testing.T) {
	f := newDevCopies(t)
	ctx := context.Background()
	if _, err := f.svc.RecordTestData(ctx, devOwner, "app-1", "x"); !errors.Is(err, domain.ErrNoDevCopy) {
		t.Fatalf("no copy yet: %v", err)
	}
	if _, err := f.svc.Deploy(ctx, devOwner, "app-1", []byte("v1")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.RecordTestData(ctx, devOwner, "app-1", "  "); !errors.Is(err, domain.ErrInvalidTestDataNote) {
		t.Fatalf("a note is needed: %v", err)
	}
	if _, err := f.svc.RecordTestData(ctx, devOwner, "dev-1", "3 ใบลาทดสอบ (test-1..3)"); err != nil {
		t.Fatalf("given the copy itself: %v", err)
	}
	c, summary, err := f.svc.DataSource(ctx, "app-1")
	if err != nil || !c.TestData {
		t.Fatalf("%+v %v", c, err)
	}
	if !strings.Contains(summary, "copy from leave-dev") || !strings.Contains(summary, "Claude reported adding test data") ||
		!strings.Contains(summary, "3 ใบลาทดสอบ") {
		t.Fatalf("what the approver signs: %s", summary)
	}
	f.hasDB = false
	if _, _, err := f.svc.DataSource(ctx, "app-1"); !errors.Is(err, domain.ErrDevDataNoDatabase) {
		t.Fatalf("no database, nothing to copy: %v", err)
	}
}

func TestDevCopy_Refusals(t *testing.T) {
	f := newDevCopies(t)
	ctx := context.Background()
	if _, err := f.svc.Deploy(ctx, domain.User{ID: "someone"}, "app-1", []byte("x")); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("not an owner: %v", err)
	}
	if _, err := f.svc.Deploy(ctx, devOwner, "app-1", []byte("v1")); err != nil {
		t.Fatal(err)
	}
	f.calls = nil
	if res, err := f.svc.Deploy(ctx, devOwner, "dev-1", []byte("v2")); err != nil || res.Created || res.Copy.ApplicationID != "dev-1" {
		t.Fatalf("given the copy's own id, the copy is deployed (no copy of a copy): %+v %v", res, err)
	}
	a := f.apps.apps["dev-1"]
	a.LifecycleStatus = domain.StatusDeleted
	f.apps.apps["dev-1"] = a
	if res, err := f.svc.Deploy(ctx, devOwner, "app-1", []byte("v3")); err != nil || !res.Created || res.Copy.Name != "leave-dev2" {
		t.Fatalf("a copy deleted on its own is replaced: %+v %v", res.Copy, err)
	}
	a = f.apps.apps["app-1"]
	a.Name = strings.Repeat("x", 57)
	f.apps.apps["app-1"] = a
	if _, err := f.svc.Deploy(ctx, devOwner, "app-1", []byte("x")); !errors.Is(err, domain.ErrDevCopyNameTooLong) {
		t.Fatalf("too long: %v", err)
	}
}

// --- the deployment side -------------------------------------------------------------

type fakeDeployDevCopies struct {
	parents map[string]string
	source  domain.DevCopy
	copied  []string
}

func (f *fakeDeployDevCopies) ParentOf(ctx context.Context, id string) (string, error) { return f.parents[id], nil }
func (f *fakeDeployDevCopies) DataSource(ctx context.Context, parentID string) (domain.DevCopy, string, error) {
	if f.source.ApplicationID == "" {
		return domain.DevCopy{}, "", domain.ErrDevDataNoDatabase
	}
	return f.source, service.DevDataSummary(f.source), nil
}
func (f *fakeDeployDevCopies) CopyData(ctx context.Context, from, to, actor string) error {
	f.copied = append(f.copied, from+"->"+to)
	return nil
}

func TestDeploy_ADevCopyDeploysToDevWithoutApproval_EvenWhenDevIsGated(t *testing.T) {
	app, build := builtApp("app-1", "leave-dev")
	svc, _, _, _, _, _ := newDeployService(app, build, "owner-1")
	svc.SetApprovalEnvironments([]domain.Environment{domain.EnvironmentDev, domain.EnvironmentProduction})
	svc.SetDevCopies(&fakeDeployDevCopies{parents: map[string]string{"app-1": "parent-1"}})
	if _, err := svc.InitiateDeploy(reviewed(), "app-1", "owner-1", domain.EnvironmentProduction); !errors.Is(err, domain.ErrDevCopyEnvironment) {
		t.Fatalf("a dev copy deploys only to dev: %v", err)
	}
	d, err := svc.InitiateDeploy(context.Background(), "app-1", "owner-1", domain.EnvironmentDev)
	if err != nil || d.Status != domain.DeploymentRunning {
		t.Fatalf("not gated, no REVIEW.md needed: %v %v", d.Status, err)
	}
}

func TestDeploy_TheApplicationsOwnDevDeployStillFollowsTheGate(t *testing.T) {
	app, build := builtApp("app-1", "leave")
	svc, _, _, _, _, _ := newDeployService(app, build, "owner-1")
	svc.SetApprovalEnvironments([]domain.Environment{domain.EnvironmentDev, domain.EnvironmentProduction})
	svc.SetDevCopies(&fakeDeployDevCopies{parents: map[string]string{}})
	d, err := svc.InitiateDeploy(reviewed(), "app-1", "owner-1", domain.EnvironmentDev)
	if err != nil || d.Status != domain.DeploymentPendingApproval {
		t.Fatalf("the application itself: %v %v", d.Status, err)
	}
}

func TestDeploy_DevDataGoesToTheFirstProductionOnly_AsSigned(t *testing.T) {
	app, build := builtApp("app-1", "leave")
	svc, _, deployments, _, _, _ := newDeployService(app, build, "owner-1")
	copies := &fakeDeployDevCopies{parents: map[string]string{},
		source: domain.DevCopy{ApplicationID: "dev-1", Name: "leave-dev", TestData: true, TestDataNote: "3 test leave requests"}}
	svc.SetDevCopies(copies)
	ctx := domain.WithDevDataRequest(reviewed(), domain.DevDataRequest{Copy: true})

	if _, err := svc.InitiateDeploy(ctx, "app-1", "owner-1", domain.EnvironmentDev); !errors.Is(err, domain.ErrDevDataNotProduction) {
		t.Fatalf("only production takes dev data: %v", err)
	}
	d, err := svc.InitiateDeploy(ctx, "app-1", "owner-1", domain.EnvironmentProduction)
	if err != nil || d.Status != domain.DeploymentPendingApproval {
		t.Fatalf("production waits: %v %v", d.Status, err)
	}
	if d.DevDataSummary == "" || !strings.Contains(d.DevDataSummary, "3 test leave requests") || len(copies.copied) != 0 {
		t.Fatalf("recorded for the approver, nothing copied yet: %+v %v", d.DevDataSummary, copies.copied)
	}
	if _, err := svc.DecideApproval(context.Background(), d.ID, "owner-1", true, ""); err != nil {
		t.Fatal(err)
	}
	if len(copies.copied) != 1 || copies.copied[0] != "dev-1->app-1" || deployments.byID[d.ID].DevDataCopiedAt == nil {
		t.Fatalf("copied once it went live: %v %+v", copies.copied, deployments.byID[d.ID])
	}

	if _, err := svc.InitiateDeploy(ctx, "app-1", "owner-1", domain.EnvironmentProduction); !errors.Is(err, domain.ErrDevDataNotFirst) {
		t.Fatalf("production has its own data now: %v", err)
	}
}

func TestSigningTemplate_NamesTheDevDataOnlyWhenThereIsSome(t *testing.T) {
	s := domain.ApprovalSubject{DeploymentID: "d1", ApplicationID: "a1", ApplicationName: "leave", Environment: "production", BuildID: "b1"}
	if strings.Contains(s.SigningTemplate(), "dev-data") {
		t.Fatal("unchanged for every other approval")
	}
	s.DevData = "copy from leave-dev; Claude reported adding test data: 3 rows"
	if !strings.Contains(s.SigningTemplate(), "dev-data: copy from leave-dev; Claude reported adding test data: 3 rows\n") {
		t.Fatal(s.SigningTemplate())
	}
}

func TestGovernance_ADevCopyBuildsWithItsApplicationsExceptions(t *testing.T) {
	g := newGovernance(t, false)
	ctx := context.Background()
	addRule(t, g, "npm", "left-pad")
	e, _, err := g.svc.RequestException(ctx, govOwner, "app-1", "dependency", "npm:left-pad", "vendor SDK")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.svc.DecideException(ctx, govAdmin, e.ID, true, ""); err != nil {
		t.Fatal(err)
	}
	g.svc.SetDevCopies(&fakeDeployDevCopies{parents: map[string]string{"dev-1": "app-1"}})
	if err := g.svc.SourceFailure(ctx, "dev-1", tarGz(t, forbiddenSource)); err != nil {
		t.Fatalf("the dev copy of app-1 may use what app-1 may: %v", err)
	}
	if err := g.svc.SourceFailure(ctx, "other", tarGz(t, forbiddenSource)); err == nil {
		t.Fatal("another application may not")
	}
}
