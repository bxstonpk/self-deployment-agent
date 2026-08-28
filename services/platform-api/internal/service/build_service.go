// Build implements Module I (docs/02_Functional_Requirements.md):
// FR-035 (trigger build), FR-036 (status tracking), FR-037 (standard base
// image governance), FR-038 (failure handling/reporting).
//
// Source intake note: the docs never specify HOW the platform is meant to
// reach an employee's/agent's source code (git host? branch convention?
// upload?) — FR-035 only says "source is accessible to the platform's
// build system". Rather than invent an unstated git-hosting convention,
// this implementation accepts an uploaded tar.gz archive at build-request
// time, with a documented convention: a top-level directory per declared
// service name (e.g. `frontend/`, `api/`), matching deployment.yaml's
// `services` keys. `applications.source_repository_reference` still exists
// as the ENT-05-documented informational/audit field, just not as the
// build's input mechanism.
package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"gopkg.in/yaml.v3"

	"platform-api/internal/domain"
)

type BuildServiceSpec struct {
	Runtime   string
	Port      int
	BaseImage string
}

type BuildEngineRequest struct {
	BuildID         string
	ApplicationName string
	Services        map[string]BuildServiceSpec
	SourceArchive   []byte // raw tar.gz bytes
}

// BuildEngine performs the actual build. A failure should be returned as
// *domain.BuildFailure so FR-038's source-vs-platform distinction survives;
// any other error is treated as a platform-category failure.
type BuildEngine interface {
	Build(ctx context.Context, req BuildEngineRequest) (imageRefs map[string]string, err error)
}

type BuildRepository interface {
	Create(ctx context.Context, applicationID, triggeredBy string) (domain.Build, error)
	MarkInProgress(ctx context.Context, buildID string) (domain.Build, error)
	MarkSucceeded(ctx context.Context, buildID string, imageRefs map[string]string) (domain.Build, error)
	MarkFailed(ctx context.Context, buildID string, category domain.ErrorCategory, detail string) (domain.Build, error)
	LatestForApplication(ctx context.Context, applicationID string) (domain.Build, error)
	GetByID(ctx context.Context, buildID string) (domain.Build, error)
}

type BaseImageRepository interface {
	GetForRuntime(ctx context.Context, runtime string) (domain.BaseImage, error)
}

type BuildService struct {
	apps       ApplicationLifecycleRepository
	owners     ApplicationOwnerRepository
	builds     BuildRepository
	baseImages BaseImageRepository
	engine     BuildEngine
}

func NewBuildService(apps ApplicationLifecycleRepository, owners ApplicationOwnerRepository, builds BuildRepository, baseImages BaseImageRepository, engine BuildEngine) *BuildService {
	return &BuildService{apps: apps, owners: owners, builds: builds, baseImages: baseImages, engine: engine}
}

func (s *BuildService) requireOwner(ctx context.Context, applicationID, userID string) error {
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

// TriggerBuild implements FR-035: callable on a Validated application (the
// first-ever build), or on a Running/Failed one — a *rebuild* of new source
// code for an application that's already live, with no build already
// queued/in-progress (FR-036's attributability business rule, enforced
// first in code here and again by the DB's partial unique index as a
// safety net — see migration 0003).
//
// Allowing Running/Failed here closes a real gap found while building the
// MCP server (services/mcp-server): deploy_application (MCP Section 13.6)
// is specced to trigger Build -> Image Scan -> Deploy as one pipeline, but
// there was previously no way to build a SECOND version for an
// already-`running` application at all — not even directly, only for the
// very first build. This mirrors deploy_service.go's InitiateDeploy, which
// already allows redeploying while Running; a rebuild is the same kind of
// operation one step earlier in the pipeline.
//
// On completion the application moves to Build (success — the SAME
// transient status a first-ever build uses; a previously-Running
// application's earlier deployment is untouched and still serving traffic
// until a subsequent deploy_application/InitiateDeploy call activates the
// new build, exactly like "Deploying" doesn't mean traffic is already
// down) or, on failure, back to whatever it was before (FR-044's same
// "a failed attempt must not take down an already-good version" principle,
// applied one step earlier than InitiateDeploy's own version of it — a
// failed BUILD never touches running infrastructure at all, so there's
// even less reason to move a Running application to Failed over it).
func (s *BuildService) TriggerBuild(ctx context.Context, applicationID, requesterID string, sourceArchive []byte) (domain.Build, error) {
	app, err := s.apps.GetByID(ctx, applicationID)
	if err != nil {
		return domain.Build{}, err
	}
	if err := s.requireOwner(ctx, applicationID, requesterID); err != nil {
		return domain.Build{}, err
	}
	fromStatus := app.LifecycleStatus
	if fromStatus != domain.StatusValidated && fromStatus != domain.StatusRunning && fromStatus != domain.StatusFailed {
		return domain.Build{}, domain.ErrNotValidated
	}
	if len(sourceArchive) == 0 {
		return domain.Build{}, domain.ErrNoSourceArchive
	}

	if latest, err := s.builds.LatestForApplication(ctx, applicationID); err == nil {
		if latest.Status == domain.BuildQueued || latest.Status == domain.BuildInProgress {
			return domain.Build{}, domain.ErrBuildAlreadyInFlight
		}
	} else if !errors.Is(err, domain.ErrNotFound) {
		return domain.Build{}, err
	}

	var parsed domain.DeploymentYAML
	if err := yaml.NewDecoder(bytes.NewReader([]byte(app.DeploymentYAMLDraft))).Decode(&parsed); err != nil {
		// Should not happen for a Validated application, but fail closed
		// rather than build against an unparseable contract.
		return domain.Build{}, fmt.Errorf("application is Validated but its deployment.yaml no longer parses: %w", err)
	}

	specs := make(map[string]BuildServiceSpec, len(parsed.Services))
	for name, svc := range parsed.Services {
		base, err := s.baseImages.GetForRuntime(ctx, svc.Runtime)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				return domain.Build{}, fmt.Errorf("%w: runtime %q", domain.ErrNoBaseImageForRuntime, svc.Runtime)
			}
			return domain.Build{}, err
		}
		if base.Status != "active" {
			return domain.Build{}, fmt.Errorf("%w: runtime %q's base image is %q", domain.ErrNoBaseImageForRuntime, svc.Runtime, base.Status)
		}
		specs[name] = BuildServiceSpec{Runtime: svc.Runtime, Port: svc.Port, BaseImage: base.ImageReference}
	}

	build, err := s.builds.Create(ctx, applicationID, requesterID)
	if err != nil {
		return domain.Build{}, err
	}
	if _, err := s.apps.UpdateLifecycleStatus(ctx, applicationID, fromStatus, domain.StatusBuild, false); err != nil {
		return domain.Build{}, err
	}
	build, err = s.builds.MarkInProgress(ctx, build.ID)
	if err != nil {
		return domain.Build{}, err
	}

	imageRefs, buildErr := s.engine.Build(ctx, BuildEngineRequest{
		BuildID: build.ID, ApplicationName: app.Name, Services: specs, SourceArchive: sourceArchive,
	})
	if buildErr != nil {
		category := domain.ErrorCategoryPlatform
		var bf *domain.BuildFailure
		if errors.As(buildErr, &bf) {
			category = bf.Category
		}
		// Recording the failure must not itself be cancellable by the
		// same signal that caused the failure — a slow build whose caller
		// disconnects/times out cancels `ctx`, and `s.engine.Build` above
		// returns an error as a direct result of that cancellation. Using
		// the still-cancelled `ctx` for this cleanup write would then make
		// THIS write fail too, leaving the build stuck `in_progress`
		// forever and the application stuck at `Build` — unrecoverable
		// through the API, since neither TriggerBuild nor anything else
		// accepts `Build` as a valid starting state. Found for real: a
		// client-side timeout during a genuinely slow (not hung) rebuild
		// produced exactly this stuck state, confirmed via direct
		// inspection (`builds.status = 'in_progress'`, no `completed_at`,
		// `applications.lifecycle_status = 'build'`). detachedCtx keeps
		// this write's identity/values but survives the parent's
		// cancellation, with its own bounded timeout so a genuinely dead
		// database doesn't hang forever either.
		detachedCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()

		build, err = s.builds.MarkFailed(detachedCtx, build.ID, category, buildErr.Error())
		if err != nil {
			return domain.Build{}, err
		}
		// A failed build never touches running infrastructure — if the
		// application was already Running (this was a rebuild attempt, not
		// the first build), its still-live previous deployment is entirely
		// unaffected, so there's no reason to move it to Failed. Only a
		// first-ever build (fromStatus == Validated) or a rebuild attempted
		// while already Failed has nothing good to fall back to.
		target := domain.StatusFailed
		if fromStatus == domain.StatusRunning {
			target = domain.StatusRunning
		}
		if _, err := s.apps.UpdateLifecycleStatus(detachedCtx, applicationID, domain.StatusBuild, target, false); err != nil {
			return domain.Build{}, err
		}
		return build, nil
	}

	build, err = s.builds.MarkSucceeded(ctx, build.ID, imageRefs)
	if err != nil {
		return domain.Build{}, err
	}
	return build, nil
}

func (s *BuildService) LatestBuild(ctx context.Context, applicationID string) (domain.Build, error) {
	return s.builds.LatestForApplication(ctx, applicationID)
}
