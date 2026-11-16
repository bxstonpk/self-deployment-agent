package service_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"platform-api/internal/domain"
	"platform-api/internal/service"
	"platform-api/internal/sourcestore"
)

// --- fakes -------------------------------------------------------------------

type fakeHandoffRepo struct {
	mu   sync.Mutex
	pkgs []domain.HandoffPackage
	next int
}

func (f *fakeHandoffRepo) Create(_ context.Context, p domain.HandoffPackage) (domain.HandoffPackage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, q := range f.pkgs {
		if q.ApplicationID == p.ApplicationID && q.Status == domain.HandoffBuilding {
			return domain.HandoffPackage{}, domain.ErrHandoffInProgress
		}
	}
	f.next++
	p.ID = "pkg-" + string(rune('0'+f.next))
	p.Status = domain.HandoffBuilding
	p.CreatedAt = time.Date(2026, 11, 16, 9, 0, f.next, 0, time.UTC)
	f.pkgs = append(f.pkgs, p)
	return p, nil
}

func (f *fakeHandoffRepo) update(id string, fn func(*domain.HandoffPackage)) (domain.HandoffPackage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.pkgs {
		if f.pkgs[i].ID == id {
			fn(&f.pkgs[i])
			return f.pkgs[i], nil
		}
	}
	return domain.HandoffPackage{}, domain.ErrHandoffNotFound
}

func (f *fakeHandoffRepo) Complete(_ context.Context, id, name string, size int64, sum string) (domain.HandoffPackage, error) {
	now := time.Now()
	return f.update(id, func(p *domain.HandoffPackage) {
		p.Status, p.FileName, p.SizeBytes, p.SHA256, p.CompletedAt = domain.HandoffReady, name, size, sum, &now
	})
}

func (f *fakeHandoffRepo) Fail(_ context.Context, id, reason string) error {
	now := time.Now()
	_, err := f.update(id, func(p *domain.HandoffPackage) {
		p.Status, p.Error, p.CompletedAt = domain.HandoffFailed, reason, &now
	})
	return err
}

func (f *fakeHandoffRepo) ReplaceOlder(_ context.Context, applicationID, keepID string) ([]domain.HandoffPackage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.HandoffPackage
	for i := range f.pkgs {
		p := &f.pkgs[i]
		if p.ApplicationID == applicationID && p.ID != keepID && p.Status == domain.HandoffReady {
			p.Status = domain.HandoffReplaced
			out = append(out, *p)
		}
	}
	return out, nil
}

func (f *fakeHandoffRepo) GetByID(_ context.Context, id string) (domain.HandoffPackage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.pkgs {
		if p.ID == id {
			return p, nil
		}
	}
	return domain.HandoffPackage{}, domain.ErrHandoffNotFound
}

func (f *fakeHandoffRepo) Current(_ context.Context) ([]domain.HandoffPackage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	newest := map[string]domain.HandoffPackage{}
	for _, p := range f.pkgs {
		if n, ok := newest[p.ApplicationID]; !ok || p.CreatedAt.After(n.CreatedAt) {
			newest[p.ApplicationID] = p
		}
	}
	var out []domain.HandoffPackage
	for _, p := range f.pkgs {
		if p.Status == domain.HandoffReady || newest[p.ApplicationID].ID == p.ID {
			out = append(out, p)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (f *fakeHandoffRepo) FailUnfinished(_ context.Context) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for i := range f.pkgs {
		if f.pkgs[i].Status == domain.HandoffBuilding {
			f.pkgs[i].Status, f.pkgs[i].Error = domain.HandoffFailed, "restarted"
			n++
		}
	}
	return n, nil
}

func (f *fakeHandoffRepo) get(id string) domain.HandoffPackage {
	p, _ := f.GetByID(context.Background(), id)
	return p
}

type fakeHandoffDeployments struct {
	mu   sync.Mutex
	byID map[string]domain.Deployment
	// running is each application's current running deployment id.
	running map[string]string
}

func (f *fakeHandoffDeployments) GetByID(_ context.Context, id string) (domain.Deployment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.byID[id]
	if !ok {
		return domain.Deployment{}, domain.ErrNotFound
	}
	return d, nil
}

func (f *fakeHandoffDeployments) CurrentRunning(_ context.Context, applicationID string) (domain.Deployment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.running[applicationID]
	if !ok {
		return domain.Deployment{}, domain.ErrNoRunningDeployment
	}
	return f.byID[id], nil
}

func (f *fakeHandoffDeployments) put(d domain.Deployment) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byID[d.ID] = d
	f.running[d.ApplicationID] = d.ID
}

type fakeHandoffImages struct {
	mu    sync.Mutex
	saved [][]string
	err   error
	// gate, if set, holds SaveImages until it is closed.
	gate chan struct{}
}

func (f *fakeHandoffImages) SaveImages(_ context.Context, refs []string, w io.Writer) error {
	if f.gate != nil {
		<-f.gate
	}
	f.mu.Lock()
	f.saved = append(f.saved, append([]string(nil), refs...))
	err := f.err
	f.mu.Unlock()
	if err != nil {
		return err
	}
	_, werr := io.WriteString(w, "docker-save:"+strings.Join(refs, ","))
	return werr
}

type fakeHandoffDatabase struct {
	dumps map[string]service.HandoffDatabaseDump
}

func (f fakeHandoffDatabase) HandoffDump(_ context.Context, applicationID string) (service.HandoffDatabaseDump, error) {
	d, ok := f.dumps[applicationID]
	if !ok {
		return service.HandoffDatabaseDump{}, domain.ErrDatabaseNotProvisioned
	}
	return d, nil
}

type fakeHandoffSecrets map[string][]domain.ApplicationSecret

func (f fakeHandoffSecrets) ListForApplication(_ context.Context, applicationID string) ([]domain.ApplicationSecret, error) {
	return f[applicationID], nil
}

type fakeParents map[string]string

func (f fakeParents) ParentOf(_ context.Context, applicationID string) (string, error) {
	return f[applicationID], nil
}

// --- fixture -------------------------------------------------------------------

type handoffFixture struct {
	svc         *service.HandoffService
	repo        *fakeHandoffRepo
	deployments *fakeHandoffDeployments
	images      *fakeHandoffImages
	audit       *fakeAuditRecorder
	store       *sourcestore.Store
	dir         string
	app, copy   domain.Application
	admin       domain.User
	approver    domain.User
	owner       domain.User
}

const handoffYAML = `app:
  name: report
services:
  frontend:
    runtime: react
  api:
    runtime: python
    port: 8080
database:
  type: mysql
`

func handoffTarGz(t *testing.T, files map[string]string, extra ...*tar.Header) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if err := tw.WriteHeader(&tar.Header{Name: n, Mode: 0o644, Size: int64(len(files[n])), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write([]byte(files[n]))
	}
	for _, h := range extra {
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write(make([]byte, h.Size))
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func newHandoffFixture(t *testing.T) handoffFixture {
	t.Helper()
	f := handoffFixture{
		repo:   &fakeHandoffRepo{},
		images: &fakeHandoffImages{},
		audit:  newFakeAuditRecorder(),
		dir:    t.TempDir(),
		app: domain.Application{ID: "app-report", Name: "report", Description: "Production\nreport",
			DeploymentYAMLDraft: handoffYAML},
		copy:     domain.Application{ID: "app-report-dev", Name: "report-dev", DeploymentYAMLDraft: handoffYAML},
		admin:    domain.User{ID: "u-admin", Email: "admin@sti-th.com"},
		approver: domain.User{ID: "u-approver", Email: "approver@sti-th.com"},
		owner:    domain.User{ID: "u-owner", Email: "owner@sti-th.com"},
	}
	apps := newFakeApplicationRepo()
	apps.byID[f.app.ID] = f.app
	apps.byID[f.copy.ID] = f.copy
	owners := newFakeOwnerRepo()
	owners.owners[f.app.ID] = []domain.ApplicationOwner{
		{ApplicationID: f.app.ID, UserID: f.owner.ID, OwnershipRole: domain.OwnerRolePrimary, Status: "active"},
		{ApplicationID: f.app.ID, UserID: "u-gone", OwnershipRole: domain.OwnerRolePrimary, Status: "revoked"},
	}
	builds := &fakeSourceBuilds{builds: []domain.Build{
		{ID: "b-1", ApplicationID: f.app.ID, Status: domain.BuildSucceeded,
			ImageRefs: map[string]string{"api": "platform-build/report-api:b-1", "frontend": "platform-build/report-frontend:b-1"}},
		{ID: "b-2", ApplicationID: f.app.ID, Status: domain.BuildSucceeded,
			ImageRefs: map[string]string{"api": "platform-build/report-api:b-2", "frontend": "platform-build/report-frontend:b-2"}},
	}}
	f.deployments = &fakeHandoffDeployments{byID: map[string]domain.Deployment{}, running: map[string]string{}}
	f.store = sourcestore.New(t.TempDir())
	src := handoffTarGz(t, map[string]string{
		"api/app.py":                "STIX = 'http://192.168.90.132:3000/api/list'  # the planning server\nSMTP = '192.168.0.200'\n",
		"api/test_app.py":           "assert url == 'http://10.9.9.9:1'\n",
		"api/Dockerfile":            "FROM their-own-image\n",
		"api/requirements.txt":      "flask\n",
		"frontend/src/api.js":       "fetch('./api/items'); const docs = 'https://localhost:5173'\n",
		"frontend/package.json":     `{"name":"report"}`,
		"./frontend/../README.md":   "# report\n",
	}, &tar.Header{Name: "../escape.txt", Mode: 0o644, Size: 3, Typeflag: tar.TypeReg},
		&tar.Header{Name: "/etc/passwd", Mode: 0o644, Size: 3, Typeflag: tar.TypeReg},
		&tar.Header{Name: "api/link", Linkname: "/etc/shadow", Typeflag: tar.TypeSymlink})
	for _, b := range []string{"b-1", "b-2"} {
		if err := f.store.Save(f.app.ID, b, src); err != nil {
			t.Fatal(err)
		}
	}
	secrets := fakeHandoffSecrets{f.app.ID: {
		{Name: "STIX_USER_PASS", ManagedBy: domain.SecretManagedByEmployee, Ciphertext: []byte("sealed-secret-value")},
		{Name: "APP_URL", ManagedBy: domain.SecretManagedByEmployee},
		{Name: domain.DatabasePasswordSecret, ManagedBy: domain.SecretManagedByPlatform},
	}}
	db := fakeHandoffDatabase{dumps: map[string]service.HandoffDatabaseDump{f.app.ID: {
		Engine: "mysql", DatabaseName: "appdb", Username: "appuser", Dump: []byte("INSERT INTO t VALUES (1);\n"),
		TakenAt: time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC),
	}}}
	f.svc = service.NewHandoffService(service.HandoffDeps{
		Repo: f.repo, Apps: apps, Owners: owners, Users: fakeSourceUsers{f.owner.ID: f.owner},
		Deployments: f.deployments, Builds: builds, Sources: f.store, BaseImages: &fakeBaseImageRepo{byRuntime: map[string]domain.BaseImage{
			"python": {Runtime: "python", ImageReference: "python:3.12-slim", Status: "active"},
			"react":  {Runtime: "react", ImageReference: "node:20-alpine", Status: "active"},
		}},
		Dockerfile: func(runtime, base string, port int) string {
			return "FROM " + base + " # platform recipe for " + runtime + "\n"
		},
		Images: f.images, Database: db, Secrets: secrets, Copies: fakeParents{f.copy.ID: f.app.ID},
		Admins: fakeSourceAdmins{}, IsApprover: func(e string) bool { return e == "approver@sti-th.com" },
		Audit: f.audit, Dir: f.dir,
	})
	return f
}

func (f handoffFixture) promote(t *testing.T, id, build string) domain.Deployment {
	t.Helper()
	d := domain.Deployment{ID: id, ApplicationID: f.app.ID, BuildID: build, Environment: domain.EnvironmentProduction,
		Status: domain.DeploymentRunning}
	f.deployments.put(d)
	f.svc.Promoted(context.Background(), f.app, d)
	return d
}

func (f handoffFixture) ready(t *testing.T) domain.HandoffPackage {
	t.Helper()
	entries, err := f.svc.List(context.Background(), f.admin)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.ApplicationID == f.app.ID && e.Ready != nil {
			return *e.Ready
		}
	}
	t.Fatalf("no ready package: %+v", entries)
	return domain.HandoffPackage{}
}

// readPackage downloads a package as the admin and returns its files.
func (f handoffFixture) readPackage(t *testing.T, id string) (map[string]string, map[string]int64) {
	t.Helper()
	rc, size, name, err := f.svc.Open(context.Background(), f.admin, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	body, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(body)) != size || !strings.HasPrefix(name, "report-handoff-") || !strings.HasSuffix(name, ".tar") {
		t.Fatalf("size %d of %d, name %q", len(body), size, name)
	}
	sum := sha256.Sum256(body)
	if got := f.repo.get(id).SHA256; got != hex.EncodeToString(sum[:]) {
		t.Fatalf("recorded sha256 %s, file's %x", got, sum)
	}
	files, modes := map[string]string{}, map[string]int64{}
	tr := tar.NewReader(bytes.NewReader(body))
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		if !strings.HasPrefix(h.Name, "report-handoff/") {
			t.Fatalf("entry outside the package folder: %q", h.Name)
		}
		files[strings.TrimPrefix(h.Name, "report-handoff/")] = string(b)
		modes[strings.TrimPrefix(h.Name, "report-handoff/")] = h.Mode
	}
	return files, modes
}

// --- tests -------------------------------------------------------------------

func TestHandoffPacksAProductionDeploymentWhenItGoesLive(t *testing.T) {
	f := newHandoffFixture(t)
	f.promote(t, "d-1", "b-1")
	f.svc.Wait()

	p := f.ready(t)
	if p.Trigger != domain.HandoffOnPromotion || p.DeploymentID != "d-1" || p.RequestedBy != "" {
		t.Fatalf("package %+v", p)
	}
	files, _ := f.readPackage(t, p.ID)

	for _, want := range []string{"README.md", "docker-compose.yml", ".env.example", "proxy/default.conf", "deployment.yaml",
		"database/01-data.sql", "images/images.tar", "source/api/app.py", "source/api/Dockerfile", "source/frontend/Dockerfile",
		"source/frontend/src/api.js", "source/README.md", "SHA256SUMS"} {
		if _, ok := files[want]; !ok {
			t.Errorf("package has no %s; has %v", want, keys(files))
		}
	}
	if files["images/images.tar"] != "docker-save:platform-build/report-api:b-1,platform-build/report-frontend:b-1" {
		t.Errorf("images: %q", files["images/images.tar"])
	}
	if files["database/01-data.sql"] != "INSERT INTO t VALUES (1);\n" {
		t.Errorf("dump: %q", files["database/01-data.sql"])
	}
	// The platform's recipe, not the one the source carried (never used).
	if !strings.Contains(files["source/api/Dockerfile"], "FROM python:3.12-slim # platform recipe for python") {
		t.Errorf("api Dockerfile: %q", files["source/api/Dockerfile"])
	}
	for name := range files {
		if strings.Contains(name, "escape") || strings.Contains(name, "passwd") || strings.Contains(name, "link") {
			t.Errorf("unsafe source entry packed: %s", name)
		}
	}

	// Every file is in SHA256SUMS, with its real sum.
	sums := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(files["SHA256SUMS"]), "\n") {
		sum, name, _ := strings.Cut(line, "  ")
		sums[name] = sum
	}
	for name, body := range files {
		if name == "SHA256SUMS" {
			continue
		}
		got := sha256.Sum256([]byte(body))
		if sums[name] != hex.EncodeToString(got[:]) {
			t.Errorf("SHA256SUMS for %s: %q", name, sums[name])
		}
	}

	compose := files["docker-compose.yml"]
	for _, want := range []string{`image: mysql:8.4`, `MYSQL_DATABASE: "appdb"`, `MYSQL_USER: "appuser"`,
		`./database:/docker-entrypoint-initdb.d:ro`, `image: "platform-build/report-api:b-1"`, `PORT: "8080"`, `PORT: "3000"`,
		`DATABASE_URL: "mysql://appuser:${DATABASE_PASSWORD}@db:3306/appdb"`, `env_file: .env`, `"${HTTP_PORT:-8080}:80"`,
		`condition: service_healthy`} {
		if !strings.Contains(compose, want) {
			t.Errorf("docker-compose.yml lacks %q:\n%s", want, compose)
		}
	}
	proxy := files["proxy/default.conf"]
	if !strings.Contains(proxy, "location /api/ {\n        proxy_pass http://api:8080/;") ||
		!strings.Contains(proxy, "location / {\n        proxy_pass http://frontend:3000;") {
		t.Errorf("proxy:\n%s", proxy)
	}

	env := files[".env.example"]
	for _, want := range []string{"HTTP_PORT=8080", "DATABASE_PASSWORD=\n", "APP_URL=\n", "STIX_USER_PASS=\n"} {
		if !strings.Contains(env, want) {
			t.Errorf(".env.example lacks %q:\n%s", want, env)
		}
	}
	// Names only: no secret value, and the platform's own password isn't a setting to copy.
	for name, body := range files {
		if strings.Contains(body, "sealed-secret-value") {
			t.Errorf("%s carries a secret's value", name)
		}
	}
	if strings.Count(env, "DATABASE_PASSWORD=") != 1 {
		t.Errorf("DATABASE_PASSWORD listed twice:\n%s", env)
	}

	readme := files["README.md"]
	for _, want := range []string{"owner@sti-th.com (primary)", "`d-1`", "docker load -i images/images.tar",
		"`http://192.168.90.132:3000`", "`api/app.py:1`", "`192.168.0.200`", "`api/app.py:2`", "Production report"} {
		if !strings.Contains(readme, want) {
			t.Errorf("README lacks %q:\n%s", want, readme)
		}
	}
	for _, unwanted := range []string{"10.9.9.9", "localhost", "u-gone"} {
		if strings.Contains(readme, unwanted) {
			t.Errorf("README lists %q (a test file, local address or former owner)", unwanted)
		}
	}
	if strings.Count(readme, "192.168.90.132") != 1 {
		t.Errorf("the planning server's address is listed more than once:\n%s", readme)
	}
}

func TestHandoffIsOnlyForProductionDeploymentsOfApplications(t *testing.T) {
	f := newHandoffFixture(t)
	f.svc.Promoted(context.Background(), f.app, domain.Deployment{ID: "d-dev", ApplicationID: f.app.ID, BuildID: "b-1",
		Environment: domain.EnvironmentDev})
	f.svc.Promoted(context.Background(), f.copy, domain.Deployment{ID: "d-copy", ApplicationID: f.copy.ID, BuildID: "b-1",
		Environment: domain.EnvironmentProduction})
	f.svc.Wait()
	if len(f.repo.pkgs) != 0 {
		t.Fatalf("packages made: %+v", f.repo.pkgs)
	}
	if _, err := f.svc.Rebuild(context.Background(), f.admin, f.copy.ID); !errors.Is(err, domain.ErrHandoffNotProduction) {
		t.Fatalf("rebuild of a dev copy: %v", err)
	}
	if _, err := f.svc.Rebuild(context.Background(), f.admin, f.app.ID); !errors.Is(err, domain.ErrHandoffNotProduction) {
		t.Fatalf("rebuild with nothing running: %v", err)
	}
}

func TestHandoffIsForAdministratorsAndApproversAndEveryDownloadIsAudited(t *testing.T) {
	f := newHandoffFixture(t)
	f.deployments.put(domain.Deployment{ID: "d-1", ApplicationID: f.app.ID, BuildID: "b-1",
		Environment: domain.EnvironmentProduction, Status: domain.DeploymentRunning})

	if _, err := f.svc.Rebuild(context.Background(), f.owner, f.app.ID); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("owner rebuild: %v", err)
	}
	if _, err := f.svc.List(context.Background(), f.owner); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("owner list: %v", err)
	}
	p, err := f.svc.Rebuild(context.Background(), f.approver, f.app.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.svc.Wait()
	if got := f.repo.get(p.ID); got.Status != domain.HandoffReady || got.Trigger != domain.HandoffManual || got.RequestedBy != f.approver.ID {
		t.Fatalf("manual package: %+v", got)
	}
	if _, _, _, err := f.svc.Open(context.Background(), f.owner, p.ID); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("owner download: %v", err)
	}
	rc, _, _, err := f.svc.Open(context.Background(), f.approver, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	rc.Close()

	var actions []string
	for _, e := range f.audit.all() {
		if e.ActorUserID != f.approver.ID || e.ResourceID != f.app.ID || e.Outcome != domain.AuditSuccess {
			t.Errorf("audit entry %+v", e)
		}
		actions = append(actions, string(e.Action))
	}
	if strings.Join(actions, ",") != "handoff.build,handoff.download" {
		t.Fatalf("audited %v", actions)
	}
}

func TestANewerHandoffReplacesTheOlderAndItsFile(t *testing.T) {
	f := newHandoffFixture(t)
	f.promote(t, "d-1", "b-1")
	f.svc.Wait()
	first := f.ready(t)
	f.promote(t, "d-2", "b-2")
	f.svc.Wait()
	second := f.ready(t)

	if second.ID == first.ID || second.DeploymentID != "d-2" {
		t.Fatalf("ready is %+v", second)
	}
	if got := f.repo.get(first.ID).Status; got != domain.HandoffReplaced {
		t.Fatalf("first is %s", got)
	}
	if _, err := os.Stat(filepath.Join(f.dir, f.app.ID, first.ID+".tar")); !os.IsNotExist(err) {
		t.Fatalf("first file still there: %v", err)
	}
	if _, _, _, err := f.svc.Open(context.Background(), f.admin, first.ID); !errors.Is(err, domain.ErrHandoffNotReady) {
		t.Fatalf("download of a replaced package: %v", err)
	}
	files, _ := f.readPackage(t, second.ID)
	if !strings.Contains(files["images/images.tar"], "report-api:b-2") {
		t.Fatalf("second package images: %q", files["images/images.tar"])
	}
}

func TestAFailedHandoffSaysWhyAndKeepsTheReadyOne(t *testing.T) {
	f := newHandoffFixture(t)
	f.promote(t, "d-1", "b-1")
	f.svc.Wait()
	good := f.ready(t)

	f.images.err = errors.New("no space left on device")
	f.promote(t, "d-2", "b-2")
	f.svc.Wait()

	entries, err := f.svc.List(context.Background(), f.admin)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Ready == nil || entries[0].Ready.ID != good.ID ||
		entries[0].Latest == nil || entries[0].Latest.Status != domain.HandoffFailed ||
		!strings.Contains(entries[0].Latest.Error, "no space left on device") {
		t.Fatalf("entries %+v", entries)
	}
	parts, _ := filepath.Glob(filepath.Join(f.dir, f.app.ID, "*.part"))
	if len(parts) != 0 {
		t.Fatalf("left behind: %v", parts)
	}
}

func TestPromotedWhileAPackageIsBeingMadePacksTheNewerDeploymentAfter(t *testing.T) {
	f := newHandoffFixture(t)
	f.images.gate = make(chan struct{})
	f.promote(t, "d-1", "b-1")
	f.promote(t, "d-2", "b-2") // the first is still being made
	close(f.images.gate)
	f.svc.Wait()

	p := f.ready(t)
	if p.DeploymentID != "d-2" {
		t.Fatalf("ready package is of %s", p.DeploymentID)
	}
	if len(f.repo.pkgs) != 2 {
		t.Fatalf("packages %+v", f.repo.pkgs)
	}
}

func TestHandoffRecoverAndPurge(t *testing.T) {
	f := newHandoffFixture(t)
	if _, err := f.repo.Create(context.Background(), domain.HandoffPackage{ApplicationID: f.app.ID, DeploymentID: "d-x"}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(f.dir, f.app.ID), 0o700); err != nil {
		t.Fatal(err)
	}
	part := filepath.Join(f.dir, f.app.ID, "pkg-1.tar.part")
	if err := os.WriteFile(part, []byte("half"), 0o600); err != nil {
		t.Fatal(err)
	}
	n, err := f.svc.Recover(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("recover: %d, %v", n, err)
	}
	if _, err := os.Stat(part); !os.IsNotExist(err) {
		t.Fatalf("part file: %v", err)
	}

	f.promote(t, "d-1", "b-1")
	f.svc.Wait()
	if err := f.svc.PurgeApplication(context.Background(), f.app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.dir, f.app.ID)); !os.IsNotExist(err) {
		t.Fatalf("application's packages still there: %v", err)
	}
	if err := f.svc.PurgeApplication(context.Background(), "../etc"); err == nil {
		t.Fatal("purge of a path outside the handoff folder was accepted")
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
