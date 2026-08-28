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

// TriggerBuild implements FR-035: only callable on a Validated application,
// with no build already queued/in-progress (FR-036's attributability
// business rule, enforced first in code here and again by the DB's partial
// unique index as a safety net — see migration 0003). On completion the
// application moves to Build (success) or Failed (failure); a failed build
// is a normal, fully-reported outcome (FR-038), not an error return.
func (s *BuildService) TriggerBuild(ctx context.Context, applicationID, requesterID string, sourceArchive []byte) (domain.Build, error) {
	app, err := s.apps.GetByID(ctx, applicationID)
	if err != nil {
		return domain.Build{}, err
	}
	if err := s.requireOwner(ctx, applicationID, requesterID); err != nil {
		return domain.Build{}, err
	}
	if app.LifecycleStatus != domain.StatusValidated {
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
	if _, err := s.apps.UpdateLifecycleStatus(ctx, applicationID, domain.StatusValidated, domain.StatusBuild, false); err != nil {
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
		build, err = s.builds.MarkFailed(ctx, build.ID, category, buildErr.Error())
		if err != nil {
			return domain.Build{}, err
		}
		if _, err := s.apps.UpdateLifecycleStatus(ctx, applicationID, domain.StatusBuild, domain.StatusFailed, false); err != nil {
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
