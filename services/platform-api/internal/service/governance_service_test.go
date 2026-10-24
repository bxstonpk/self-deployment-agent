package service_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"platform-api/internal/domain"
	"platform-api/internal/service"
)

type fakeGovernanceRepo struct {
	profiles   map[string]domain.ApplicationProfile
	rules      []domain.DependencyRule
	exceptions []domain.StandardException
	next       int
}

func newFakeGovernanceRepo() *fakeGovernanceRepo {
	return &fakeGovernanceRepo{profiles: map[string]domain.ApplicationProfile{}}
}

func (f *fakeGovernanceRepo) id(prefix string) string {
	f.next++
	return fmt.Sprintf("%s-%d", prefix, f.next)
}

func (f *fakeGovernanceRepo) GetProfile(ctx context.Context, applicationID string) (domain.ApplicationProfile, error) {
	p, ok := f.profiles[applicationID]
	if !ok {
		return domain.ApplicationProfile{}, domain.ErrNotFound
	}
	return p, nil
}

func (f *fakeGovernanceRepo) PutProfile(ctx context.Context, p domain.ApplicationProfile) (domain.ApplicationProfile, error) {
	p.UpdatedAt = time.Now()
	f.profiles[p.ApplicationID] = p
	return p, nil
}

func (f *fakeGovernanceRepo) ListDependencyRules(ctx context.Context) ([]domain.DependencyRule, error) {
	return append([]domain.DependencyRule{}, f.rules...), nil
}

func (f *fakeGovernanceRepo) AddDependencyRule(ctx context.Context, r domain.DependencyRule) (domain.DependencyRule, error) {
	for _, x := range f.rules {
		if x.Ecosystem == r.Ecosystem && x.Package == r.Package {
			return domain.DependencyRule{}, domain.ErrDependencyRuleExists
		}
	}
	r.ID = f.id("rule")
	f.rules = append(f.rules, r)
	return r, nil
}

func (f *fakeGovernanceRepo) DeleteDependencyRule(ctx context.Context, id string) (domain.DependencyRule, error) {
	for i, x := range f.rules {
		if x.ID == id {
			f.rules = append(f.rules[:i], f.rules[i+1:]...)
			return x, nil
		}
	}
	return domain.DependencyRule{}, domain.ErrNotFound
}

func (f *fakeGovernanceRepo) CreateException(ctx context.Context, e domain.StandardException) (domain.StandardException, error) {
	e.ID = f.id("exc")
	e.Status = domain.ExceptionPending
	f.exceptions = append(f.exceptions, e)
	return e, nil
}

func (f *fakeGovernanceRepo) GetException(ctx context.Context, id string) (domain.StandardException, error) {
	for _, e := range f.exceptions {
		if e.ID == id {
			return e, nil
		}
	}
	return domain.StandardException{}, domain.ErrNotFound
}

func (f *fakeGovernanceRepo) ListExceptionsForApplication(ctx context.Context, applicationID string) ([]domain.StandardException, error) {
	var out []domain.StandardException
	for _, e := range f.exceptions {
		if e.ApplicationID == applicationID {
			out = append(out, e)
		}
	}
	return out, nil
}

func (f *fakeGovernanceRepo) ListExceptionsByStatus(ctx context.Context, status string) ([]domain.StandardException, error) {
	var out []domain.StandardException
	for _, e := range f.exceptions {
		if e.Status == status {
			out = append(out, e)
		}
	}
	return out, nil
}

func (f *fakeGovernanceRepo) DecideException(ctx context.Context, id, status, decidedBy, reason string) (domain.StandardException, error) {
	for i, e := range f.exceptions {
		if e.ID != id {
			continue
		}
		if e.Status != domain.ExceptionPending {
			return domain.StandardException{}, domain.ErrExceptionNotPending
		}
		e.Status, e.DecidedBy, e.DecisionReason = status, &decidedBy, reason
		f.exceptions[i] = e
		return e, nil
	}
	return domain.StandardException{}, domain.ErrNotFound
}

type fakeStacks struct{}

func (fakeStacks) List(ctx context.Context) ([]domain.SupportedStack, error) {
	return []domain.SupportedStack{{Kind: domain.StackKindBackend, Name: "go", Status: "active"}}, nil
}

type fakeGovernanceAdmins struct{}

func (fakeGovernanceAdmins) IsAdmin(email string) bool { return email == "admin@sti-th.com" }
func (fakeGovernanceAdmins) AdminUserIDs(ctx context.Context) []string {
	return []string{"admin-1"}
}

var (
	govOwner = domain.User{ID: "owner-1", Email: "somchai@sti-th.com"}
	govAdmin = domain.User{ID: "admin-1", Email: "admin@sti-th.com"}
	govOther = domain.User{ID: "other-1", Email: "other@sti-th.com"}
)

type governanceFixture struct {
	svc    *service.GovernanceService
	repo   *fakeGovernanceRepo
	notify *fakeNotificationRecorder
	audit  *fakeAuditRecorder
	build  *service.BuildService
	builds *fakeBuildRepo
	engine *fakeBuildEngine
}

func newGovernance(t *testing.T, requireProfile bool) governanceFixture {
	t.Helper()
	app := validatedApp("app-1", "overtime")
	apps := newFakeLifecycleRepo(app)
	owners := newFakeOwnerRepo()
	owners.owners[app.ID] = []domain.ApplicationOwner{{
		ApplicationID: app.ID, UserID: govOwner.ID, OwnershipRole: domain.OwnerRolePrimary, Status: "active",
	}}
	repo := newFakeGovernanceRepo()
	notify := newFakeNotificationRecorder()
	audit := newFakeAuditRecorder()
	svc := service.NewGovernanceService(repo, apps, owners, fakeStacks{}, fakeGovernanceAdmins{}, notify, audit, requireProfile)
	builds := newFakeBuildRepo()
	engine := &fakeBuildEngine{result: map[string]string{"frontend": "platform-build/overtime-frontend:1"}}
	build := service.NewBuildService(apps, owners, builds, newFakeBaseImageRepo(), engine, newFakeAuditRecorder())
	build.SetGovernance(svc)
	return governanceFixture{svc: svc, repo: repo, notify: notify, audit: audit, build: build, builds: builds, engine: engine}
}

func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const govGoMod = `module example.com/api

go 1.23

require github.com/go-sql-driver/mysql v1.8.1 // the database driver

require (
	github.com/go-chi/chi/v5 v5.1.0
	// a comment line
	filippo.io/edwards25519 v1.1.0 // indirect
)
`

const govRequirements = `# the API
fastapi==0.115.0
uvicorn[standard]>=0.30
PyMySQL ; python_version >= "3.8"
-r more.txt
--index-url https://example.invalid/simple
git+https://example.invalid/x.git
`

func TestScanDependencies_ReadsEveryServiceRootManifest(t *testing.T) {
	archive := tarGz(t, map[string]string{
		"frontend/package.json":                `{"dependencies":{"react":"^19"},"devDependencies":{"vite":"^7","react":"^19"}}`,
		"api/go.mod":                           govGoMod,
		"worker/requirements.txt":              govRequirements,
		"frontend/node_modules/x/package.json": `{"dependencies":{"not-installed-from-here":"1"}}`,
		"frontend/src/package.json":            `{"dependencies":{"nested":"1"}}`,
		"README.md":                            "not a service",
	})
	deps, err := service.ScanDependencies(archive)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, d := range deps {
		got[d.Manifest] = append(got[d.Manifest], d.Ecosystem+":"+d.Package)
	}
	want := map[string][]string{
		"api/go.mod":              {"go:filippo.io/edwards25519", "go:github.com/go-chi/chi/v5", "go:github.com/go-sql-driver/mysql"},
		"frontend/package.json":   {"npm:react", "npm:vite"},
		"worker/requirements.txt": {"pypi:PyMySQL", "pypi:fastapi", "pypi:uvicorn"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dependencies\n got %v\nwant %v", got, want)
	}
}

func TestScanDependencies_AManifestThatDoesntParseIsAnError(t *testing.T) {
	_, err := service.ScanDependencies(tarGz(t, map[string]string{"frontend/package.json": "{not json"}))
	if err == nil || !strings.Contains(err.Error(), "frontend/package.json") {
		t.Fatalf("want an error naming the manifest, got %v", err)
	}
}

func TestNormalizePackage_ComparesNamesTheWayEachEcosystemDoes(t *testing.T) {
	cases := []struct{ eco, a, b string }{
		{"pypi", "Py_YAML", "py-yaml"},
		{"pypi", "zope.interface", "Zope-Interface"},
		{"npm", "Left-Pad", "left-pad"},
	}
	for _, c := range cases {
		if domain.NormalizePackage(c.eco, c.a) != domain.NormalizePackage(c.eco, c.b) {
			t.Errorf("%s: %q and %q should be the same package", c.eco, c.a, c.b)
		}
	}
	if domain.NormalizePackage("go", "github.com/A/b") == domain.NormalizePackage("go", "github.com/a/b") {
		t.Error("go module paths are compared as written")
	}
}

func addRule(t *testing.T, g governanceFixture, eco, pkg string) domain.DependencyRule {
	t.Helper()
	r, err := g.svc.AddDependencyRule(context.Background(), govAdmin, eco, pkg, "unmaintained")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

var forbiddenSource = map[string]string{
	"frontend/package.json": `{"dependencies":{"react":"^19","Left-Pad":"1.3.0"}}`,
}

func TestBuild_ALibraryTheCompanyDoesntAllowFailsTheBuildAsTheSourcesProblem(t *testing.T) {
	g := newGovernance(t, false)
	addRule(t, g, "npm", "left-pad")

	b, err := g.build.TriggerBuild(context.Background(), "app-1", govOwner.ID, tarGz(t, forbiddenSource))
	if err != nil {
		t.Fatal(err)
	}
	if b.Status != domain.BuildFailed || b.ErrorCategory == nil || *b.ErrorCategory != domain.ErrorCategorySource {
		t.Fatalf("want a failed build of category source, got %s %v", b.Status, b.ErrorCategory)
	}
	if !strings.Contains(*b.ErrorDetail, `"Left-Pad" in frontend/package.json (unmaintained)`) || !strings.Contains(*b.ErrorDetail, "request_exception") {
		t.Fatalf("the detail should say which library, where, why, and what to do: %s", *b.ErrorDetail)
	}
	if g.engine.called {
		t.Fatal("nothing is built from a source that fails the check")
	}
}

func TestBuild_AnApprovedExceptionLetsTheLibraryThrough(t *testing.T) {
	g := newGovernance(t, false)
	addRule(t, g, "npm", "left-pad")
	ctx := context.Background()
	e, created, err := g.svc.RequestException(ctx, govOwner, "app-1", "dependency", "npm:LEFT-PAD", "a vendor SDK needs it")
	if err != nil || !created {
		t.Fatalf("request: %v %v", created, err)
	}
	if e.Subject != "npm:left-pad" {
		t.Fatalf("the subject is normalized like the rule: %q", e.Subject)
	}

	// Pending isn't approved.
	b, _ := g.build.TriggerBuild(ctx, "app-1", govOwner.ID, tarGz(t, forbiddenSource))
	if b.Status != domain.BuildFailed {
		t.Fatalf("a pending exception mustn't let it through: %s", b.Status)
	}

	if _, err := g.svc.DecideException(ctx, govAdmin, e.ID, true, ""); err != nil {
		t.Fatal(err)
	}
	// The failed attempt left it Failed, which a rebuild starts from.
	g.engine.called = false
	b, err = g.build.TriggerBuild(ctx, "app-1", govOwner.ID, tarGz(t, forbiddenSource))
	if err != nil {
		t.Fatal(err)
	}
	if b.Status != domain.BuildSucceeded || !g.engine.called {
		t.Fatalf("approved: want built, got %s (engine called %v)", b.Status, g.engine.called)
	}
}

func TestBuild_RefusesAnApplicationStartedWithoutAProfile(t *testing.T) {
	g := newGovernance(t, true)
	ctx := context.Background()
	_, err := g.build.TriggerBuild(ctx, "app-1", govOwner.ID, tarGz(t, map[string]string{"frontend/package.json": `{}`}))
	if !errors.Is(err, domain.ErrProfileRequired) {
		t.Fatalf("want ErrProfileRequired, got %v", err)
	}
	if _, err := g.builds.LatestForApplication(ctx, "app-1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("a refused build leaves no build record")
	}

	if _, err := g.svc.PutProfile(ctx, govOwner, domain.ApplicationProfile{
		ApplicationID: "app-1", Purpose: "overtime requests", HandlesPersonalData: true,
	}); err != nil {
		t.Fatal(err)
	}
	b, err := g.build.TriggerBuild(ctx, "app-1", govOwner.ID, tarGz(t, map[string]string{"frontend/package.json": `{}`}))
	if err != nil || b.Status != domain.BuildSucceeded {
		t.Fatalf("with a profile: %v %v", b.Status, err)
	}
}

func TestPutProfile_OwnersOnlyAndAPurposeIsRequired(t *testing.T) {
	g := newGovernance(t, true)
	ctx := context.Background()
	p := domain.ApplicationProfile{ApplicationID: "app-1", Purpose: "  ", HandlesPersonalData: false}
	if _, err := g.svc.PutProfile(ctx, govOwner, p); !errors.Is(err, domain.ErrInvalidProfile) {
		t.Fatalf("an empty purpose: %v", err)
	}
	p.Purpose = "leave requests"
	if _, err := g.svc.PutProfile(ctx, govOther, p); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("not an owner: %v", err)
	}
	if _, err := g.svc.PutProfile(ctx, govAdmin, p); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("an admin reads profiles, the owners write them: %v", err)
	}
	if _, err := g.svc.PutProfile(ctx, govOwner, p); err != nil {
		t.Fatal(err)
	}
	if got, err := g.svc.GetProfile(ctx, govAdmin, "app-1"); err != nil || got.Purpose != "leave requests" {
		t.Fatalf("an admin reads it: %v %v", got.Purpose, err)
	}
	if _, err := g.svc.GetProfile(ctx, govOther, "app-1"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("someone else doesn't: %v", err)
	}
	entries := g.audit.all()
	if len(entries) != 1 || entries[0].Action != domain.AuditActionRecordProfile {
		t.Fatalf("one audit entry for the profile, got %+v", entries)
	}
}

func TestRequestException_NotifiesTheAdministratorsOnceAndAsksAgainReturnTheSameRequest(t *testing.T) {
	g := newGovernance(t, true)
	ctx := context.Background()
	if _, _, err := g.svc.RequestException(ctx, govOther, "app-1", "stack", "vue", "x"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("not an owner: %v", err)
	}
	for _, bad := range [][3]string{
		{"dependency", "left-pad", "no ecosystem"},
		{"dependency", "maven:junit", "an ecosystem rules don't cover"},
		{"database", "oracle", "not a kind"},
		{"other", "something", ""},
	} {
		if _, _, err := g.svc.RequestException(ctx, govOwner, "app-1", bad[0], bad[1], bad[2]); !errors.Is(err, domain.ErrInvalidException) {
			t.Errorf("%v: want ErrInvalidException, got %v", bad, err)
		}
	}

	e, created, err := g.svc.RequestException(ctx, govOwner, "app-1", "stack", "Vue", "the team already knows it")
	if err != nil || !created {
		t.Fatal(created, err)
	}
	again, created, err := g.svc.RequestException(ctx, govOwner, "app-1", "stack", "vue", "asking twice")
	if err != nil || created || again.ID != e.ID {
		t.Fatalf("asking again returns the pending request: %v %v %v", again.ID, created, err)
	}
	calls := g.notify.allUserCalls()
	if len(calls) != 1 || calls[0].RecipientUserID != "admin-1" || calls[0].Category != domain.NotificationStandardException {
		t.Fatalf("the administrators are told once: %+v", calls)
	}
}

func TestDecideException_AdministratorsOnlyARejectionNeedsAReasonAndOnceOnly(t *testing.T) {
	g := newGovernance(t, true)
	ctx := context.Background()
	e, _, err := g.svc.RequestException(ctx, govOwner, "app-1", "other", "a scheduled job", "nightly export")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.svc.DecideException(ctx, govOwner, e.ID, true, ""); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("an owner can't approve their own exception: %v", err)
	}
	if _, err := g.svc.DecideException(ctx, govAdmin, e.ID, false, " "); !errors.Is(err, domain.ErrRejectionReasonRequired) {
		t.Fatalf("a rejection needs a reason: %v", err)
	}
	got, err := g.svc.DecideException(ctx, govAdmin, e.ID, false, "use the platform's scale-to-zero instead")
	if err != nil || got.Status != domain.ExceptionRejected {
		t.Fatalf("reject: %v %v", got.Status, err)
	}
	if _, err := g.svc.DecideException(ctx, govAdmin, e.ID, true, ""); !errors.Is(err, domain.ErrExceptionNotPending) {
		t.Fatalf("decided once: %v", err)
	}
	calls := g.notify.all()
	if len(calls) != 1 || calls[0].ApplicationID != "app-1" || !strings.Contains(calls[0].Title, "rejected") {
		t.Fatalf("the owners hear the decision: %+v", calls)
	}

	// Rejected, the owner may ask again with a better case.
	_, created, err := g.svc.RequestException(ctx, govOwner, "app-1", "other", "a scheduled job", "now with a reason")
	if err != nil || !created {
		t.Fatalf("asking again after a rejection: %v %v", created, err)
	}
}

func TestRequestException_ADeletedApplicationCantAsk(t *testing.T) {
	g := newGovernance(t, true)
	apps := newFakeLifecycleRepo(domain.Application{ID: "gone", Name: "gone", LifecycleStatus: domain.StatusDeleted})
	owners := newFakeOwnerRepo()
	owners.owners["gone"] = []domain.ApplicationOwner{{ApplicationID: "gone", UserID: govOwner.ID, Status: "active"}}
	svc := service.NewGovernanceService(g.repo, apps, owners, fakeStacks{}, fakeGovernanceAdmins{}, g.notify, g.audit, true)
	if _, _, err := svc.RequestException(context.Background(), govOwner, "gone", "other", "x", "y"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("want ErrNotFound for a deleted application, got %v", err)
	}
}

func TestDependencyRules_AdministratorsManageThem(t *testing.T) {
	g := newGovernance(t, true)
	ctx := context.Background()
	if _, err := g.svc.AddDependencyRule(ctx, govOwner, "npm", "left-pad", "x"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("owner: %v", err)
	}
	if _, err := g.svc.AddDependencyRule(ctx, govAdmin, "maven", "junit", "x"); !errors.Is(err, domain.ErrInvalidDependencyRule) {
		t.Fatalf("unknown ecosystem: %v", err)
	}
	if _, err := g.svc.AddDependencyRule(ctx, govAdmin, "pypi", "x", " "); !errors.Is(err, domain.ErrInvalidDependencyRule) {
		t.Fatalf("no reason: %v", err)
	}
	r, err := g.svc.AddDependencyRule(ctx, govAdmin, "PyPI", "Py_YAML", "use the standard library's json")
	if err != nil || r.Ecosystem != "pypi" || r.Package != "py-yaml" {
		t.Fatalf("stored normalized: %+v %v", r, err)
	}
	if _, err := g.svc.AddDependencyRule(ctx, govAdmin, "pypi", "py.yaml", "again"); !errors.Is(err, domain.ErrDependencyRuleExists) {
		t.Fatalf("the same package spelled differently is the same rule: %v", err)
	}
	st, _ := g.svc.Standards(ctx)
	if len(st.DependencyRules) != 1 || len(st.Rules) == 0 || len(st.Stacks) == 0 || !st.RequireProfile {
		t.Fatalf("standards: %+v", st)
	}
	if err := g.svc.RemoveDependencyRule(ctx, govAdmin, r.ID); err != nil {
		t.Fatal(err)
	}
}

func TestCheckSource_ReportsWithoutBuilding(t *testing.T) {
	g := newGovernance(t, true)
	ctx := context.Background()
	addRule(t, g, "npm", "left-pad")
	res, err := g.svc.CheckSource(ctx, govOwner, "app-1", tarGz(t, forbiddenSource))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Dependencies) != 2 || len(res.Violations) != 1 || res.Violations[0].Dependency.Package != "Left-Pad" || res.HasProfile {
		t.Fatalf("check: %+v", res)
	}
	if g.engine.called {
		t.Fatal("a check builds nothing")
	}
	if _, err := g.svc.CheckSource(ctx, govOwner, "app-1", []byte("not gzip")); !errors.Is(err, domain.ErrUnreadableSource) {
		t.Fatalf("unreadable: %v", err)
	}
}
