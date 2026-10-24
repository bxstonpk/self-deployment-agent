// Previews (DEC-029 part 3): try a change on the platform before deploying
// it, without touching the running application.
//
// A preview is its own application, "<name>-preview-<4 hex>", registered by
// the employee who starts it, with the original's deployment.yaml (its
// database backups off), profile, and approved dependency exceptions. So it
// gets its own empty database, its own address and its own lifecycle; the
// application it previews keeps serving, its data untouched. It deploys to
// the "preview" environment, which waits for approval only if
// APPROVAL_REQUIRED_ENVIRONMENTS names it.
//
// One live preview per application: starting another replaces it. A
// preview is torn down (archived, deleted, its database removed) when it
// expires (PREVIEW_TTL_HOURS), when the application it previews is archived
// or deleted, or when an owner stops it.
package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"time"

	"gopkg.in/yaml.v3"

	"platform-api/internal/domain"
)

type PreviewRepository interface {
	// Live is an application's current preview (domain.ErrNotFound if none).
	Live(ctx context.Context, parentID string) (domain.Preview, error)
	// Of says whether an application is itself a preview, and of what.
	Of(ctx context.Context, applicationID string) (domain.Preview, error)
	Create(ctx context.Context, p domain.Preview) (domain.Preview, error)
	End(ctx context.Context, applicationID string) error
	// Due is every live preview to tear down: expired, or its application
	// archived or deleted.
	Due(ctx context.Context, now time.Time) ([]domain.Preview, error)
}

// PreviewSteps are the ordinary operations a preview is made of — the same
// ones an employee's own calls go through, with the same checks.
type PreviewSteps struct {
	Register   func(ctx context.Context, in RegisterApplicationInput) (domain.Application, error)
	Validate   func(ctx context.Context, applicationID, requesterID string) (domain.ValidationReport, domain.Application, error)
	Build      func(ctx context.Context, applicationID, requesterID string, archive []byte) (domain.Build, error)
	Deploy     func(ctx context.Context, applicationID, requesterID string, env domain.Environment) (domain.Deployment, error)
	Archive    func(ctx context.Context, applicationID, requesterID string) (domain.Application, error)
	Delete     func(ctx context.Context, applicationID, requesterID string, confirm bool) (domain.Application, error)
	GetProfile func(ctx context.Context, applicationID string) (domain.ApplicationProfile, error)
	PutProfile func(ctx context.Context, p domain.ApplicationProfile) (domain.ApplicationProfile, error)
}

type PreviewService struct {
	apps     ApplicationLifecycleRepository
	owners   ApplicationOwnerRepository
	previews PreviewRepository
	steps    PreviewSteps
	audit    AuditRecorder
	ttl      time.Duration
	now      func() time.Time
	suffix   func() string
}

func NewPreviewService(apps ApplicationLifecycleRepository, owners ApplicationOwnerRepository, previews PreviewRepository,
	steps PreviewSteps, audit AuditRecorder, ttl time.Duration) *PreviewService {
	return &PreviewService{apps: apps, owners: owners, previews: previews, steps: steps, audit: audit, ttl: ttl,
		now: time.Now, suffix: randomSuffix}
}

func randomSuffix() string {
	b := make([]byte, 2)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *PreviewService) requireOwner(ctx context.Context, applicationID, userID string) error {
	owners, err := s.owners.ListForApplication(ctx, applicationID)
	if err != nil {
		return err
	}
	for _, o := range owners {
		if o.UserID == userID && o.Status == "active" {
			return nil
		}
	}
	return domain.ErrUnauthorized
}

// PreviewResult is a started preview and how far it got.
type PreviewResult struct {
	Preview    domain.Preview
	Build      domain.Build
	Deployment domain.Deployment
}

// previewYAML is the original's deployment.yaml for the preview: its own
// name, and no database backups (it holds nothing worth keeping).
func previewYAML(original, name string) (string, error) {
	var doc map[string]any
	if err := yaml.NewDecoder(bytes.NewReader([]byte(original))).Decode(&doc); err != nil || doc == nil {
		return "", ErrNoServicesToTest
	}
	if services, ok := doc["services"].(map[string]any); !ok || len(services) == 0 {
		return "", ErrNoServicesToTest
	}
	app, _ := doc["app"].(map[string]any)
	if app == nil {
		app = map[string]any{}
	}
	app["name"] = name
	doc["app"] = app
	if db, ok := doc["database"].(map[string]any); ok {
		db["backup"] = domain.BackupPolicyNone
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// Start makes a preview of the application from the uploaded source (the
// same tar.gz a build takes): registers it, validates, builds and deploys
// it. A live preview of the same application is torn down first.
func (s *PreviewService) Start(ctx context.Context, caller domain.User, parentID string, archive []byte) (PreviewResult, error) {
	parent, err := s.apps.GetByID(ctx, parentID)
	if err != nil {
		return PreviewResult{}, err
	}
	if parent.LifecycleStatus == domain.StatusDeleted {
		return PreviewResult{}, domain.ErrNotFound
	}
	if err := s.requireOwner(ctx, parentID, caller.ID); err != nil {
		return PreviewResult{}, err
	}
	if parent.LifecycleStatus == domain.StatusArchived {
		return PreviewResult{}, domain.ErrPreviewParentInactive
	}
	if _, err := s.previews.Of(ctx, parentID); err == nil {
		return PreviewResult{}, domain.ErrPreviewOfPreview
	} else if !errors.Is(err, domain.ErrNotFound) {
		return PreviewResult{}, err
	}
	if len(parent.Name) > domain.MaxPreviewParentName {
		return PreviewResult{}, domain.ErrPreviewNameTooLong
	}
	if len(archive) == 0 {
		return PreviewResult{}, domain.ErrNoSourceArchive
	}
	if _, err := previewYAML(parent.DeploymentYAMLDraft, parent.Name); err != nil {
		return PreviewResult{}, err
	}

	if old, err := s.previews.Live(ctx, parentID); err == nil {
		if err := s.teardown(ctx, old, "replaced by a new preview"); err != nil {
			return PreviewResult{}, fmt.Errorf("the current preview couldn't be removed to make room: %w", err)
		}
	} else if !errors.Is(err, domain.ErrNotFound) {
		return PreviewResult{}, err
	}

	// A name is never reused (a deleted application keeps its name), so a
	// taken one just means another try.
	var app domain.Application
	for attempt := 0; ; attempt++ {
		name := parent.Name + "-preview-" + s.suffix()
		draft, err := previewYAML(parent.DeploymentYAMLDraft, name)
		if err != nil {
			return PreviewResult{}, err
		}
		app, err = s.steps.Register(ctx, RegisterApplicationInput{
			Name: name, Description: "Preview of " + parent.Name, OwningDepartmentID: parent.OwningDepartmentID,
			DeploymentYAMLDraft: draft, RegisteredBy: caller,
		})
		if err == nil {
			break
		}
		if !errors.Is(err, domain.ErrNameTaken) || attempt >= 4 {
			return PreviewResult{}, err
		}
	}
	p, err := s.previews.Create(ctx, domain.Preview{
		ApplicationID: app.ID, ParentID: parentID, CreatedBy: caller.ID, ExpiresAt: s.now().Add(s.ttl),
	})
	if err != nil {
		return PreviewResult{}, err
	}
	p.Name, p.ParentName = app.Name, parent.Name
	if err := s.audit.Record(ctx, domain.AuditEntry{
		ActorUserID: caller.ID, Action: domain.AuditActionPreviewStart, ResourceType: "application", ResourceID: parentID,
		Outcome: domain.AuditSuccess, Detail: fmt.Sprintf("%s, until %s", app.Name, p.ExpiresAt.UTC().Format(time.RFC3339)),
	}); err != nil {
		return PreviewResult{Preview: p}, err
	}

	if s.steps.GetProfile != nil {
		if profile, err := s.steps.GetProfile(ctx, parentID); err == nil {
			profile.ApplicationID, profile.UpdatedBy = app.ID, caller.ID
			if _, err := s.steps.PutProfile(ctx, profile); err != nil {
				return PreviewResult{Preview: p}, err
			}
		} else if !errors.Is(err, domain.ErrNotFound) {
			return PreviewResult{Preview: p}, err
		}
	}

	// Nothing went live yet: a failure here leaves nothing worth keeping.
	fail := func(err error) (PreviewResult, error) {
		if terr := s.teardown(context.WithoutCancel(ctx), p, "it didn't build"); terr != nil {
			log.Printf("previews: %s: %v (the sweeper retries)", app.Name, terr)
		}
		return PreviewResult{Preview: p}, err
	}
	report, _, err := s.steps.Validate(ctx, app.ID, caller.ID)
	if err != nil {
		return fail(err)
	}
	if !report.Valid {
		return fail(fmt.Errorf("%w: the application's deployment.yaml doesn't validate", domain.ErrNotValidated))
	}
	build, err := s.steps.Build(ctx, app.ID, caller.ID, archive)
	if err != nil {
		return fail(err)
	}
	if build.Status != domain.BuildSucceeded {
		res, _ := fail(nil)
		res.Build = build
		return res, nil
	}
	// A deployment that fails stays until the preview expires, so its logs
	// can be read; an owner can stop it sooner.
	deployment, err := s.steps.Deploy(ctx, app.ID, caller.ID, domain.EnvironmentPreview)
	return PreviewResult{Preview: p, Build: build, Deployment: deployment}, err
}

// Get is an application's live preview.
func (s *PreviewService) Get(ctx context.Context, caller domain.User, parentID string) (domain.Preview, error) {
	if err := s.requireOwner(ctx, parentID, caller.ID); err != nil {
		return domain.Preview{}, err
	}
	p, err := s.previews.Live(ctx, parentID)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Preview{}, domain.ErrNoPreview
	}
	return p, err
}

// Stop tears an application's preview down now.
func (s *PreviewService) Stop(ctx context.Context, caller domain.User, parentID string) (domain.Preview, error) {
	p, err := s.Get(ctx, caller, parentID)
	if err != nil {
		return domain.Preview{}, err
	}
	return p, s.teardown(ctx, p, "stopped by "+caller.Email)
}

// teardown archives and deletes the preview's application, which stops its
// containers and removes its database, then ends the preview. It acts as
// the preview's creator, its owner.
func (s *PreviewService) teardown(ctx context.Context, p domain.Preview, why string) error {
	app, err := s.apps.GetByID(ctx, p.ApplicationID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	if err == nil && app.LifecycleStatus != domain.StatusDeleted {
		if app.LifecycleStatus == domain.StatusRunning || app.LifecycleStatus == domain.StatusSuspended {
			if _, err := s.steps.Archive(ctx, app.ID, p.CreatedBy); err != nil {
				return fmt.Errorf("archive %s: %w", app.Name, err)
			}
		}
		if _, err := s.steps.Delete(ctx, app.ID, p.CreatedBy, true); err != nil {
			return fmt.Errorf("delete %s: %w", app.Name, err)
		}
	}
	if err := s.previews.End(ctx, p.ApplicationID); err != nil {
		return err
	}
	return s.audit.Record(ctx, domain.AuditEntry{
		ActorUserID: p.CreatedBy, Action: domain.AuditActionPreviewEnd, ResourceType: "application", ResourceID: p.ParentID,
		Outcome: domain.AuditSuccess, Detail: fmt.Sprintf("%s: %s", app.Name, why),
	})
}

// Sweep tears down every preview that's due. Each is independent: one that
// fails is tried again next time.
func (s *PreviewService) Sweep(ctx context.Context) (int, error) {
	due, err := s.previews.Due(ctx, s.now())
	if err != nil {
		return 0, err
	}
	n := 0
	for _, p := range due {
		why := "expired"
		if !s.now().After(p.ExpiresAt) {
			why = "its application was archived or deleted"
		}
		if err := s.teardown(ctx, p, why); err != nil {
			log.Printf("previews: %s: %v", p.Name, err)
			continue
		}
		n++
	}
	return n, nil
}

// ParentOf is the application a preview was made from ("" if it isn't one):
// what governance reads exceptions from, and what decides a deployment's
// environment.
func (s *PreviewService) ParentOf(ctx context.Context, applicationID string) (string, error) {
	p, err := s.previews.Of(ctx, applicationID)
	if errors.Is(err, domain.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return p.ParentID, nil
}
