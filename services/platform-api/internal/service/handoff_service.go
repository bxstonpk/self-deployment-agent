// IT handoff packages (DEC-033). This platform is the company's development
// server; IT runs the applications for real. The platform owner,
// 2026-11-16: "whichever application we promote, we send to IT — not every
// application". So when a production deployment of an application goes live
// here, the platform packs that deployment as one .tar for IT: its images
// (docker save, which keeps how they run), a dump of its database, its
// source with the Dockerfiles it was built with, and a docker-compose.yml,
// .env.example and README.md to run it anywhere with Docker. Secret values
// are not in it, only their names.
//
// Administrators and approvers download it from the Portal; every download
// is audited. One package is kept per application, the newest that
// finished.
package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"platform-api/internal/domain"
)

// HandoffRepository records the packages; the files are on the handoff
// volume.
type HandoffRepository interface {
	// Create records a package being made; domain.ErrHandoffInProgress if
	// one is already being made for the application.
	Create(ctx context.Context, p domain.HandoffPackage) (domain.HandoffPackage, error)
	Complete(ctx context.Context, id, fileName string, size int64, sha256 string) (domain.HandoffPackage, error)
	Fail(ctx context.Context, id, reason string) error
	// ReplaceOlder marks the application's other ready packages replaced and
	// returns them, so their files can go.
	ReplaceOlder(ctx context.Context, applicationID, keepID string) ([]domain.HandoffPackage, error)
	GetByID(ctx context.Context, id string) (domain.HandoffPackage, error)
	// Current is, per application, its ready package and its newest one if
	// that is a different one (still being made, or failed), newest first.
	Current(ctx context.Context) ([]domain.HandoffPackage, error)
	// FailUnfinished closes the packages a restart interrupted.
	FailUnfinished(ctx context.Context) (int, error)
}

// HandoffDeployments reads the deployment a package is made of.
type HandoffDeployments interface {
	GetByID(ctx context.Context, deploymentID string) (domain.Deployment, error)
	CurrentRunning(ctx context.Context, applicationID string) (domain.Deployment, error)
}

// HandoffBuilds reads the build a deployment runs.
type HandoffBuilds interface {
	GetByID(ctx context.Context, buildID string) (domain.Build, error)
}

// HandoffSources opens a build's kept source archive.
type HandoffSources interface {
	Open(applicationID, buildID string) (io.ReadCloser, int64, error)
}

// HandoffImages writes images as one `docker save` archive.
type HandoffImages interface {
	SaveImages(ctx context.Context, refs []string, w io.Writer) error
}

// HandoffDatabaseDump is an application's database dumped for a package.
type HandoffDatabaseDump struct {
	Engine       string // "mysql" or "postgres"
	DatabaseName string
	Username     string
	Dump         []byte
	TakenAt      time.Time
}

// HandoffDatabase dumps an application's database:
// domain.ErrDatabaseNotProvisioned (or domain.ErrNotFound) if it has none.
type HandoffDatabase interface {
	HandoffDump(ctx context.Context, applicationID string) (HandoffDatabaseDump, error)
}

// HandoffSecrets lists an application's secrets. Only their names are read.
type HandoffSecrets interface {
	ListForApplication(ctx context.Context, applicationID string) ([]domain.ApplicationSecret, error)
}

// HandoffDeps is everything the service reads and writes.
type HandoffDeps struct {
	Repo        HandoffRepository
	Apps        SourceApps
	Owners      SourceOwners
	Users       SourceUsers
	Deployments HandoffDeployments
	Builds      HandoffBuilds
	Sources     HandoffSources
	BaseImages  BaseImageRepository
	// Dockerfile is internal/buildengine.GenerateDockerfile: the recipe the
	// platform built each service with.
	Dockerfile func(runtime, baseImage string, port int) string
	Images     HandoffImages
	Database   HandoffDatabase
	Secrets    HandoffSecrets
	Copies     CopyParents
	Admins     AdminChecker
	IsApprover func(email string) bool
	Audit      AuditRecorder
	// Dir holds one directory per application, its packages inside.
	Dir string
}

type HandoffService struct {
	d   HandoffDeps
	now func() time.Time
	// One package is made at a time: each holds a database dump in memory
	// and writes images the size of the application.
	slot chan struct{}
	// again: an application promoted again while its package was being
	// made, so another is made of what then runs.
	mu    sync.Mutex
	again map[string]bool
	// wg lets tests wait for the packages started in the background.
	wg sync.WaitGroup
}

func NewHandoffService(d HandoffDeps) *HandoffService {
	return &HandoffService{d: d, now: time.Now, slot: make(chan struct{}, 1), again: map[string]bool{}}
}

// handoffTimeout bounds making one package.
const handoffTimeout = time.Hour

// Recover closes the packages a restart interrupted, and removes their
// half-written files.
func (s *HandoffService) Recover(ctx context.Context) (int, error) {
	n, err := s.d.Repo.FailUnfinished(ctx)
	if err != nil {
		return 0, err
	}
	parts, _ := filepath.Glob(filepath.Join(s.d.Dir, "*", "*.part"))
	for _, p := range parts {
		_ = os.Remove(p)
	}
	return n, nil
}

// Promoted is the deployment pipeline's hook: a deployment has gone live.
// A production deployment of an application (not a dev copy) is packed
// for IT, in the background.
func (s *HandoffService) Promoted(ctx context.Context, app domain.Application, deployment domain.Deployment) {
	if deployment.Environment != domain.EnvironmentProduction {
		return
	}
	if s.d.Copies != nil {
		if parent, err := s.d.Copies.ParentOf(ctx, app.ID); err != nil {
			log.Printf("handoff: %s: %v", app.Name, err)
			return
		} else if parent != "" {
			return
		}
	}
	// Under mu: a package being made finishes (leaves "building") under it
	// too, so either this one is recorded, or the one being made sees again.
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.start(context.WithoutCancel(ctx), app, deployment, domain.HandoffOnPromotion, ""); err != nil {
		if errors.Is(err, domain.ErrHandoffInProgress) {
			s.again[app.ID] = true
			return
		}
		log.Printf("handoff: %s: %v", app.Name, err)
	}
}

// Rebuild makes the application's package again, of the production
// deployment running now. Administrators and approvers only.
func (s *HandoffService) Rebuild(ctx context.Context, caller domain.User, applicationID string) (domain.HandoffPackage, error) {
	if !s.allowed(caller) {
		return domain.HandoffPackage{}, domain.ErrUnauthorized
	}
	app, err := s.d.Apps.GetByID(ctx, applicationID)
	if err != nil {
		return domain.HandoffPackage{}, err
	}
	deployment, err := s.productionDeployment(ctx, app)
	if err != nil {
		return domain.HandoffPackage{}, err
	}
	p, err := s.start(context.WithoutCancel(ctx), app, deployment, domain.HandoffManual, caller.ID)
	outcome, detail := domain.AuditSuccess, ""
	if err != nil {
		outcome, detail = domain.AuditFailure, err.Error()
	} else {
		detail = "package " + p.ID + " of deployment " + deployment.ID
	}
	s.audit(ctx, caller.ID, domain.AuditActionHandoffBuild, app.ID, outcome, detail)
	return p, err
}

func (s *HandoffService) productionDeployment(ctx context.Context, app domain.Application) (domain.Deployment, error) {
	if s.d.Copies != nil {
		parent, err := s.d.Copies.ParentOf(ctx, app.ID)
		if err != nil {
			return domain.Deployment{}, err
		}
		if parent != "" {
			return domain.Deployment{}, domain.ErrHandoffNotProduction
		}
	}
	deployment, err := s.d.Deployments.CurrentRunning(ctx, app.ID)
	if errors.Is(err, domain.ErrNoRunningDeployment) || errors.Is(err, domain.ErrNotFound) {
		return domain.Deployment{}, domain.ErrHandoffNotProduction
	}
	if err != nil {
		return domain.Deployment{}, err
	}
	if deployment.Environment != domain.EnvironmentProduction {
		return domain.Deployment{}, domain.ErrHandoffNotProduction
	}
	return deployment, nil
}

// start records the package and makes it in the background.
func (s *HandoffService) start(ctx context.Context, app domain.Application, deployment domain.Deployment,
	trigger domain.HandoffTrigger, requestedBy string) (domain.HandoffPackage, error) {
	p, err := s.d.Repo.Create(ctx, domain.HandoffPackage{
		ApplicationID: app.ID, DeploymentID: deployment.ID, Trigger: trigger, RequestedBy: requestedBy,
		Status: domain.HandoffBuilding,
	})
	if err != nil {
		return domain.HandoffPackage{}, err
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.run(p, app, deployment)
	}()
	return p, nil
}

func (s *HandoffService) run(p domain.HandoffPackage, app domain.Application, deployment domain.Deployment) {
	ctx, cancel := context.WithTimeout(context.Background(), handoffTimeout)
	defer cancel()
	var written writtenPackage
	var err error
	select {
	case s.slot <- struct{}{}:
		written, err = s.make(ctx, p, app, deployment)
		<-s.slot
	case <-ctx.Done():
		err = ctx.Err()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil {
		err = s.finish(ctx, p, app, deployment, written)
	}
	if err != nil {
		s.fail(p, app, err)
	}
	again := s.again[app.ID]
	delete(s.again, app.ID)
	if !again {
		return
	}
	// Promoted again while this one was being made: pack what runs now.
	bg := context.Background()
	if fresh, err := s.d.Apps.GetByID(bg, app.ID); err == nil {
		if current, err := s.productionDeployment(bg, fresh); err == nil && current.ID != deployment.ID {
			if _, err := s.start(bg, fresh, current, domain.HandoffOnPromotion, ""); err != nil {
				log.Printf("handoff: %s: %v", app.Name, err)
			}
		}
	}
}

func (s *HandoffService) fail(p domain.HandoffPackage, app domain.Application, cause error) {
	log.Printf("handoff: %s: package %s failed: %v", app.Name, p.ID, cause)
	if err := s.d.Repo.Fail(context.Background(), p.ID, cause.Error()); err != nil {
		log.Printf("handoff: record the failure of %s: %v", p.ID, err)
	}
	_ = os.Remove(s.partPath(p))
}

func (s *HandoffService) appDir(applicationID string) string {
	return filepath.Join(s.d.Dir, applicationID)
}

func (s *HandoffService) filePath(p domain.HandoffPackage) string {
	return filepath.Join(s.appDir(p.ApplicationID), p.ID+".tar")
}

func (s *HandoffService) partPath(p domain.HandoffPackage) string {
	return s.filePath(p) + ".part"
}

// writtenPackage is a package file made and not yet recorded ready.
type writtenPackage struct {
	name string
	size int64
	sum  string
}

// make writes the package's file (as .part until it is recorded).
func (s *HandoffService) make(ctx context.Context, p domain.HandoffPackage, app domain.Application, deployment domain.Deployment) (writtenPackage, error) {
	if err := os.MkdirAll(s.appDir(app.ID), 0o700); err != nil {
		return writtenPackage{}, err
	}
	content, err := s.gather(ctx, app, deployment)
	if err != nil {
		return writtenPackage{}, err
	}
	size, sum, err := s.write(ctx, p, content)
	if err != nil {
		return writtenPackage{}, err
	}
	name := fmt.Sprintf("%s-handoff-%s.tar", app.Name, content.generatedAt.UTC().Format("20060102-1504"))
	return writtenPackage{name: name, size: size, sum: sum}, nil
}

// finish records a written package ready, replacing the application's
// older one and removing its file.
func (s *HandoffService) finish(ctx context.Context, p domain.HandoffPackage, app domain.Application,
	deployment domain.Deployment, w writtenPackage) error {
	if err := os.Rename(s.partPath(p), s.filePath(p)); err != nil {
		return err
	}
	if _, err := s.d.Repo.Complete(ctx, p.ID, w.name, w.size, w.sum); err != nil {
		_ = os.Remove(s.filePath(p))
		return err
	}
	size := w.size
	replaced, err := s.d.Repo.ReplaceOlder(ctx, app.ID, p.ID)
	if err != nil {
		log.Printf("handoff: %s: replace older packages: %v", app.Name, err)
	}
	for _, old := range replaced {
		_ = os.Remove(s.filePath(old))
	}
	log.Printf("handoff: %s: package %s ready (%d bytes, deployment %s)", app.Name, p.ID, size, deployment.ID)
	return nil
}

// HandoffEntry is one application's packages, as the Portal lists them.
type HandoffEntry struct {
	ApplicationID   string
	ApplicationName string
	Ready           *domain.HandoffPackage // the one to download, if any
	Latest          *domain.HandoffPackage // the newest, when it isn't Ready: being made, or failed
}

// List is every application's current packages. Administrators and
// approvers only.
func (s *HandoffService) List(ctx context.Context, caller domain.User) ([]HandoffEntry, error) {
	if !s.allowed(caller) {
		return nil, domain.ErrUnauthorized
	}
	pkgs, err := s.d.Repo.Current(ctx)
	if err != nil {
		return nil, err
	}
	byApp := map[string]*HandoffEntry{}
	order := []string{}
	for i := range pkgs {
		p := pkgs[i]
		e, ok := byApp[p.ApplicationID]
		if !ok {
			e = &HandoffEntry{ApplicationID: p.ApplicationID}
			if app, err := s.d.Apps.GetByID(ctx, p.ApplicationID); err == nil {
				e.ApplicationName = app.Name
			}
			byApp[p.ApplicationID] = e
			order = append(order, p.ApplicationID)
		}
		switch {
		case p.Status == domain.HandoffReady && e.Ready == nil:
			e.Ready = &p
		case p.Status != domain.HandoffReady && e.Latest == nil && e.Ready == nil:
			e.Latest = &p
		}
	}
	out := make([]HandoffEntry, 0, len(order))
	for _, id := range order {
		out = append(out, *byApp[id])
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ApplicationName < out[j].ApplicationName })
	return out, nil
}

// Open is a ready package's file, to download. Administrators and
// approvers only; audited.
func (s *HandoffService) Open(ctx context.Context, caller domain.User, packageID string) (io.ReadCloser, int64, string, error) {
	if !s.allowed(caller) {
		return nil, 0, "", domain.ErrUnauthorized
	}
	p, err := s.d.Repo.GetByID(ctx, packageID)
	if err != nil {
		return nil, 0, "", err
	}
	if p.Status != domain.HandoffReady {
		return nil, 0, "", domain.ErrHandoffNotReady
	}
	f, err := os.Open(s.filePath(p))
	if err != nil {
		s.audit(ctx, caller.ID, domain.AuditActionHandoffDownload, p.ApplicationID, domain.AuditFailure, "package "+p.ID+": file missing")
		return nil, 0, "", domain.ErrHandoffNotFound
	}
	s.audit(ctx, caller.ID, domain.AuditActionHandoffDownload, p.ApplicationID, domain.AuditSuccess,
		"package "+p.ID+" ("+p.FileName+", sha256 "+p.SHA256+")")
	return f, p.SizeBytes, p.FileName, nil
}

// PurgeApplication removes an application's package files, with the
// application.
func (s *HandoffService) PurgeApplication(_ context.Context, applicationID string) error {
	if strings.TrimSpace(applicationID) == "" || strings.ContainsAny(applicationID, `/\.`) {
		return fmt.Errorf("invalid application id %q", applicationID)
	}
	return os.RemoveAll(s.appDir(applicationID))
}

func (s *HandoffService) allowed(caller domain.User) bool {
	if s.d.Admins != nil && s.d.Admins.IsAdmin(caller.Email) {
		return true
	}
	return s.d.IsApprover != nil && s.d.IsApprover(caller.Email)
}

func (s *HandoffService) audit(ctx context.Context, actorID string, action domain.AuditAction, applicationID string,
	outcome domain.AuditOutcome, detail string) {
	if s.d.Audit == nil || actorID == "" {
		return
	}
	if err := s.d.Audit.Record(ctx, domain.AuditEntry{
		ActorUserID: actorID, Action: action, ResourceType: "application", ResourceID: applicationID,
		Outcome: outcome, Detail: detail,
	}); err != nil {
		log.Printf("handoff: audit %s: %v", action, err)
	}
}

// Wait blocks until every package started so far has finished (tests).
func (s *HandoffService) Wait() { s.wg.Wait() }
