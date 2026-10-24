// Dev copies (DEC-029 part 3, the platform owner, 2026-10-24): each
// application can have one long-lived development copy to try changes on.
//
// The copy is its own application, "<name>-dev" (then "-dev2", "-dev3": a
// deleted application keeps its name), registered by the employee who first
// deploys to dev, with the original's deployment.yaml (database backups off)
// and profile. So it has its own database and address, and nothing it runs
// reaches the application or its data. Deploying to it never waits for
// approval. Its database stays from one deployment to the next, so what an
// employee entered while trying it is still there after each change.
//
// The application itself is production, which still waits for approval.
// Its first production deployment can take its data from the dev copy: the
// requester says so, the approver signs it (with what Claude reported
// about test data it added itself), and the copy runs once the deployment
// is live. After that, production's data is its own and is never replaced.
//
// The copy goes when its application is archived or deleted, or when an
// owner resets it (removing its code and data; the next dev deployment
// makes a new one).
package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"platform-api/internal/domain"
)

type DevCopyRepository interface {
	// Live is an application's current dev copy (domain.ErrNotFound if none).
	Live(ctx context.Context, parentID string) (domain.DevCopy, error)
	// Of is the dev copy record of an application that is one.
	Of(ctx context.Context, applicationID string) (domain.DevCopy, error)
	Create(ctx context.Context, c domain.DevCopy) (domain.DevCopy, error)
	End(ctx context.Context, applicationID string) error
	// Orphaned is every live copy whose application was archived or deleted.
	Orphaned(ctx context.Context) ([]domain.DevCopy, error)
	AddTestData(ctx context.Context, applicationID, note string) (domain.DevCopy, error)
}

// DevCopySteps are the ordinary operations a dev copy is made of — the same
// ones an employee's own calls go through, with the same checks.
type DevCopySteps struct {
	Register   func(ctx context.Context, in RegisterApplicationInput) (domain.Application, error)
	SaveYAML   func(ctx context.Context, applicationID, requesterID, yamlContent string) (domain.Application, error)
	Validate   func(ctx context.Context, applicationID, requesterID string) (domain.ValidationReport, domain.Application, error)
	Build      func(ctx context.Context, applicationID, requesterID string, archive []byte) (domain.Build, error)
	Deploy     func(ctx context.Context, applicationID, requesterID string, env domain.Environment) (domain.Deployment, error)
	Archive    func(ctx context.Context, applicationID, requesterID string) (domain.Application, error)
	Delete     func(ctx context.Context, applicationID, requesterID string, confirm bool) (domain.Application, error)
	GetProfile func(ctx context.Context, applicationID string) (domain.ApplicationProfile, error)
	PutProfile func(ctx context.Context, p domain.ApplicationProfile) (domain.ApplicationProfile, error)
	// HasDatabase says whether an application has a live database.
	HasDatabase func(ctx context.Context, applicationID string) (bool, error)
	// CopyDatabase replaces to's database contents with from's, backing up
	// what it replaces first.
	CopyDatabase func(ctx context.Context, fromApplicationID, toApplicationID, actorID string) error
}

type DevCopyService struct {
	apps   ApplicationLifecycleRepository
	owners ApplicationOwnerRepository
	copies DevCopyRepository
	steps  DevCopySteps
	audit  AuditRecorder
}

func NewDevCopyService(apps ApplicationLifecycleRepository, owners ApplicationOwnerRepository, copies DevCopyRepository,
	steps DevCopySteps, audit AuditRecorder) *DevCopyService {
	return &DevCopyService{apps: apps, owners: owners, copies: copies, steps: steps, audit: audit}
}

func (s *DevCopyService) requireOwner(ctx context.Context, applicationID, userID string) error {
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

// devCopyYAML is the application's deployment.yaml for its dev copy: its
// own name, and no database backups.
func devCopyYAML(original, name string) (string, error) {
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

// DevCopyResult is a dev deployment and how far it got.
type DevCopyResult struct {
	Copy       domain.DevCopy
	Created    bool
	Build      domain.Build
	Deployment domain.Deployment
}

// parentFor is the application an owner means: itself, or, given its dev
// copy, the application the copy belongs to.
func (s *DevCopyService) parentFor(ctx context.Context, applicationID string) (domain.Application, error) {
	if c, err := s.copies.Of(ctx, applicationID); err == nil {
		applicationID = c.ParentID
	} else if !errors.Is(err, domain.ErrNotFound) {
		return domain.Application{}, err
	}
	app, err := s.apps.GetByID(ctx, applicationID)
	if err != nil {
		return domain.Application{}, err
	}
	if app.LifecycleStatus == domain.StatusDeleted {
		return domain.Application{}, domain.ErrNotFound
	}
	return app, nil
}

// Deploy puts the uploaded source (the same tar.gz a build takes) on the
// application's dev copy, making the copy first if there is none: the
// application's current deployment.yaml, validated, built, deployed to dev.
// The copy's database is kept.
//
// applicationID may be the application or its dev copy: either way, the
// copy is what is deployed.
func (s *DevCopyService) Deploy(ctx context.Context, caller domain.User, applicationID string, archive []byte) (DevCopyResult, error) {
	parent, err := s.parentFor(ctx, applicationID)
	if err != nil {
		return DevCopyResult{}, err
	}
	applicationID = parent.ID
	if err := s.requireOwner(ctx, applicationID, caller.ID); err != nil {
		return DevCopyResult{}, err
	}
	if parent.LifecycleStatus == domain.StatusArchived {
		return DevCopyResult{}, domain.ErrDevCopyParentInactive
	}
	if len(parent.Name) > domain.MaxDevCopyParentName {
		return DevCopyResult{}, domain.ErrDevCopyNameTooLong
	}
	if len(archive) == 0 {
		return DevCopyResult{}, domain.ErrNoSourceArchive
	}
	if _, err := devCopyYAML(parent.DeploymentYAMLDraft, parent.Name); err != nil {
		return DevCopyResult{}, err
	}

	res := DevCopyResult{}
	c, err := s.copies.Live(ctx, applicationID)
	if err == nil {
		// Archived or deleted on its own (delete_application on the copy):
		// that copy is over; make a new one.
		if app, aerr := s.apps.GetByID(ctx, c.ApplicationID); aerr == nil &&
			(app.LifecycleStatus == domain.StatusDeleted || app.LifecycleStatus == domain.StatusArchived) {
			if terr := s.teardown(ctx, c, "it was archived or deleted on its own"); terr != nil {
				return DevCopyResult{}, terr
			}
			err = domain.ErrNotFound
		}
	}
	needsValidation := true
	switch {
	case err == nil:
		// The copy follows the application's deployment.yaml as it is now.
		// Like any application's, it can be changed only before the copy
		// first deploys; after that, an unchanged one goes straight to the
		// build (the copy's data stays), and a changed one needs a reset.
		draft, err := devCopyYAML(parent.DeploymentYAMLDraft, c.Name)
		if err != nil {
			return DevCopyResult{}, err
		}
		copyApp, err := s.apps.GetByID(ctx, c.ApplicationID)
		if err != nil {
			return DevCopyResult{}, err
		}
		switch {
		case copyApp.LifecycleStatus == domain.StatusDraft || copyApp.LifecycleStatus == domain.StatusValidated:
			if _, err := s.steps.SaveYAML(ctx, c.ApplicationID, caller.ID, draft); err != nil {
				return DevCopyResult{Copy: c}, err
			}
		case copyApp.DeploymentYAMLDraft == draft:
			needsValidation = false
		default:
			return DevCopyResult{Copy: c}, domain.ErrDevCopyYAMLChanged
		}
	case errors.Is(err, domain.ErrNotFound):
		if c, err = s.create(ctx, caller, parent); err != nil {
			return DevCopyResult{}, err
		}
		res.Created = true
	default:
		return DevCopyResult{}, err
	}
	res.Copy = c

	if needsValidation {
		report, _, err := s.steps.Validate(ctx, c.ApplicationID, caller.ID)
		if err != nil {
			return res, err
		}
		if !report.Valid {
			return res, fmt.Errorf("%w: the application's deployment.yaml doesn't validate", domain.ErrNotValidated)
		}
	}
	if res.Build, err = s.steps.Build(ctx, c.ApplicationID, caller.ID, archive); err != nil || res.Build.Status != domain.BuildSucceeded {
		// A failed build leaves the copy's running version and data as
		// they were: nothing to tear down.
		return res, err
	}
	res.Deployment, err = s.steps.Deploy(ctx, c.ApplicationID, caller.ID, domain.EnvironmentDev)
	return res, err
}

// create registers the dev copy: "<name>-dev", or the next number free.
func (s *DevCopyService) create(ctx context.Context, caller domain.User, parent domain.Application) (domain.DevCopy, error) {
	var app domain.Application
	for n := 1; ; n++ {
		name := parent.Name + "-dev"
		if n > 1 {
			name += strconv.Itoa(n)
		}
		draft, err := devCopyYAML(parent.DeploymentYAMLDraft, name)
		if err != nil {
			return domain.DevCopy{}, err
		}
		app, err = s.steps.Register(ctx, RegisterApplicationInput{
			Name: name, Description: "Dev copy of " + parent.Name, OwningDepartmentID: parent.OwningDepartmentID,
			DeploymentYAMLDraft: draft, RegisteredBy: caller,
		})
		if err == nil {
			break
		}
		if !errors.Is(err, domain.ErrNameTaken) || n >= 20 || len(name) > 62 {
			return domain.DevCopy{}, err
		}
	}
	c, err := s.copies.Create(ctx, domain.DevCopy{ApplicationID: app.ID, ParentID: parent.ID, CreatedBy: caller.ID})
	if err != nil {
		return domain.DevCopy{}, err
	}
	c.Name, c.ParentName = app.Name, parent.Name
	if s.steps.GetProfile != nil {
		if profile, err := s.steps.GetProfile(ctx, parent.ID); err == nil {
			profile.ApplicationID, profile.UpdatedBy = app.ID, caller.ID
			if _, err := s.steps.PutProfile(ctx, profile); err != nil {
				return c, err
			}
		} else if !errors.Is(err, domain.ErrNotFound) {
			return c, err
		}
	}
	return c, s.audit.Record(ctx, domain.AuditEntry{
		ActorUserID: caller.ID, Action: domain.AuditActionDevCopyCreate, ResourceType: "application", ResourceID: parent.ID,
		Outcome: domain.AuditSuccess, Detail: app.Name,
	})
}

// Get is an application's dev copy (given the application or the copy).
func (s *DevCopyService) Get(ctx context.Context, caller domain.User, applicationID string) (domain.DevCopy, error) {
	parent, err := s.parentFor(ctx, applicationID)
	if err != nil {
		return domain.DevCopy{}, err
	}
	if err := s.requireOwner(ctx, parent.ID, caller.ID); err != nil {
		return domain.DevCopy{}, err
	}
	c, err := s.copies.Live(ctx, parent.ID)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.DevCopy{}, domain.ErrNoDevCopy
	}
	return c, err
}

// RecordTestData is Claude saying it put test data in the dev copy's
// database itself — kept with the copy, shown to whoever deploys to
// production and to the approver, so it is never taken for real data.
func (s *DevCopyService) RecordTestData(ctx context.Context, caller domain.User, applicationID, note string) (domain.DevCopy, error) {
	c, err := s.Get(ctx, caller, applicationID)
	if err != nil {
		return domain.DevCopy{}, err
	}
	note = domain.OneLine(note)
	if note == "" || !utf8.ValidString(note) || len(note) > 500 {
		return domain.DevCopy{}, domain.ErrInvalidTestDataNote
	}
	entry := time.Now().UTC().Format("2006-01-02 15:04") + " " + caller.Email + ": " + note
	updated, err := s.copies.AddTestData(ctx, c.ApplicationID, entry)
	if err != nil {
		return domain.DevCopy{}, err
	}
	return updated, s.audit.Record(ctx, domain.AuditEntry{
		ActorUserID: caller.ID, Action: domain.AuditActionDevTestData, ResourceType: "application", ResourceID: c.ApplicationID,
		Outcome: domain.AuditSuccess, Detail: note,
	})
}

// Reset removes the dev copy, its code and its data; the next dev
// deployment makes a new one.
func (s *DevCopyService) Reset(ctx context.Context, caller domain.User, applicationID string) (domain.DevCopy, error) {
	c, err := s.Get(ctx, caller, applicationID)
	if err != nil {
		return domain.DevCopy{}, err
	}
	return c, s.teardown(ctx, c, "reset by "+caller.Email)
}

// teardown archives and deletes the copy's application, which stops its
// containers and removes its database. It acts as the copy's creator.
func (s *DevCopyService) teardown(ctx context.Context, c domain.DevCopy, why string) error {
	app, err := s.apps.GetByID(ctx, c.ApplicationID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	if err == nil && app.LifecycleStatus != domain.StatusDeleted {
		if app.LifecycleStatus == domain.StatusRunning || app.LifecycleStatus == domain.StatusSuspended {
			if _, err := s.steps.Archive(ctx, app.ID, c.CreatedBy); err != nil {
				return fmt.Errorf("archive %s: %w", app.Name, err)
			}
		}
		if _, err := s.steps.Delete(ctx, app.ID, c.CreatedBy, true); err != nil {
			return fmt.Errorf("delete %s: %w", app.Name, err)
		}
	}
	if err := s.copies.End(ctx, c.ApplicationID); err != nil {
		return err
	}
	return s.audit.Record(ctx, domain.AuditEntry{
		ActorUserID: c.CreatedBy, Action: domain.AuditActionDevCopyRemove, ResourceType: "application", ResourceID: c.ParentID,
		Outcome: domain.AuditSuccess, Detail: fmt.Sprintf("%s: %s", c.Name, why),
	})
}

// Sweep removes the dev copies of archived and deleted applications.
func (s *DevCopyService) Sweep(ctx context.Context) (int, error) {
	orphaned, err := s.copies.Orphaned(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, c := range orphaned {
		if err := s.teardown(ctx, c, "its application was archived or deleted"); err != nil {
			log.Printf("dev copies: %s: %v", c.Name, err)
			continue
		}
		n++
	}
	return n, nil
}

// ParentOf is the application a dev copy belongs to ("" if it isn't one).
func (s *DevCopyService) ParentOf(ctx context.Context, applicationID string) (string, error) {
	c, err := s.copies.Of(ctx, applicationID)
	if errors.Is(err, domain.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return c.ParentID, nil
}

// DevCopyLabel is how a dev copy's pages name what it is a copy of (its
// banner); ok is false for an application that isn't one.
func (s *DevCopyService) DevCopyLabel(ctx context.Context, applicationID string) (string, bool) {
	c, err := s.copies.Of(ctx, applicationID)
	if err != nil {
		return "", false
	}
	return c.ParentName, true
}

// DataSource is the dev copy a production deployment can take its data
// from, and the line the approver signs about it.
func (s *DevCopyService) DataSource(ctx context.Context, parentID string) (domain.DevCopy, string, error) {
	c, err := s.copies.Live(ctx, parentID)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.DevCopy{}, "", domain.ErrDevDataNoDatabase
	}
	if err != nil {
		return domain.DevCopy{}, "", err
	}
	has, err := s.steps.HasDatabase(ctx, c.ApplicationID)
	if err != nil {
		return domain.DevCopy{}, "", err
	}
	if !has {
		return domain.DevCopy{}, "", domain.ErrDevDataNoDatabase
	}
	return c, DevDataSummary(c), nil
}

// DevDataSummary is the line about the copied data the approver signs.
func DevDataSummary(c domain.DevCopy) string {
	summary := "copy from " + c.Name
	if c.TestData {
		return summary + "; Claude reported adding test data: " + strings.ReplaceAll(c.TestDataNote, "\n", " | ")
	}
	return summary + "; no test data reported by Claude"
}

// CopyData is the copy a live production deployment runs.
func (s *DevCopyService) CopyData(ctx context.Context, fromApplicationID, toApplicationID, actorID string) error {
	err := s.steps.CopyDatabase(ctx, fromApplicationID, toApplicationID, actorID)
	outcome, detail := domain.AuditSuccess, "from "+fromApplicationID
	if err != nil {
		outcome, detail = domain.AuditFailure, err.Error()
	}
	if aerr := s.audit.Record(ctx, domain.AuditEntry{
		ActorUserID: actorID, Action: domain.AuditActionCopyDevData, ResourceType: "application", ResourceID: toApplicationID,
		Outcome: outcome, Detail: detail,
	}); aerr != nil && err == nil {
		return aerr
	}
	return err
}
