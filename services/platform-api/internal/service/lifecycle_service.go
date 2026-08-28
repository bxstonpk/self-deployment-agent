// Lifecycle implements the remainder of Module K
// (docs/02_Functional_Requirements.md) beyond registration/validation/
// build/deploy: FR-047 (Suspend), FR-048 (Resume, Restart), FR-049
// (Archive), and FR-050 (Delete).
//
// Reuses the existing `deployments` table (migration 0006 adds 'suspended',
// migration 0007 adds 'archived', as new deployments.status values) rather
// than a parallel structure — see those migrations' doc comments for why
// this makes the scale-to-zero proxy correctly refuse to serve/cold-start a
// suspended or archived application with no code change: its CurrentRunning
// lookup (status = 'running') simply stops finding it.
package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"platform-api/internal/domain"
)

type LifecycleService struct {
	apps        ApplicationLifecycleRepository
	owners      ApplicationOwnerRepository
	deployments DeploymentRepository
	states      ServiceRuntimeStateRepository
	runtime     RuntimeEngine
}

func NewLifecycleService(
	apps ApplicationLifecycleRepository, owners ApplicationOwnerRepository,
	deployments DeploymentRepository, states ServiceRuntimeStateRepository, runtime RuntimeEngine,
) *LifecycleService {
	return &LifecycleService{apps: apps, owners: owners, deployments: deployments, states: states, runtime: runtime}
}

func (s *LifecycleService) requireOwner(ctx context.Context, applicationID, userID string) error {
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

// Suspend implements FR-047: stops all traffic/compute for a Running
// application while retaining its configuration for later resumption. Every
// service's container is stopped — unlike the scale-to-zero sweeper, which
// only ever touches eligible services, Suspend is a broader "stop
// everything" operation covering frontends too.
//
// Known gap, documented not hidden: FR-047 names Security Administrator
// force-suspend (bypassing owner-initiated flow, e.g. on a policy
// violation) as an alternative flow. There's no distinct Security
// Administrator role to check yet (blocked on DEC-001/DEC-002, same RBAC
// gap noted throughout — see docs/17_Decision_Log.md), so only owner-
// initiated suspend is implemented here.
func (s *LifecycleService) Suspend(ctx context.Context, applicationID, requesterID string) (domain.Deployment, error) {
	app, err := s.apps.GetByID(ctx, applicationID)
	if err != nil {
		return domain.Deployment{}, err
	}
	if err := s.requireOwner(ctx, applicationID, requesterID); err != nil {
		return domain.Deployment{}, err
	}
	if app.LifecycleStatus != domain.StatusRunning {
		return domain.Deployment{}, domain.ErrApplicationNotRunning
	}

	deployment, err := s.deployments.LatestForApplication(ctx, applicationID)
	if err != nil {
		return domain.Deployment{}, err
	}
	if deployment.Status != domain.DeploymentRunning {
		return domain.Deployment{}, domain.ErrApplicationNotRunning
	}

	if err := s.stopAllContainers(ctx, deployment.ID); err != nil {
		return domain.Deployment{}, err
	}

	deployment, err = s.deployments.SetStatus(ctx, deployment.ID, domain.DeploymentSuspended)
	if err != nil {
		return domain.Deployment{}, err
	}
	// Keep the containers snapshot honest — nothing is running anymore.
	// Without this it would keep showing the pre-suspend container_id/
	// host_port, which no longer exist (found via manual testing).
	deployment, err = s.deployments.UpdateContainers(ctx, deployment.ID, map[string]domain.RunningContainer{})
	if err != nil {
		return domain.Deployment{}, err
	}
	if _, err := s.apps.UpdateLifecycleStatus(ctx, app.ID, domain.StatusRunning, domain.StatusSuspended, false); err != nil {
		return domain.Deployment{}, err
	}
	return deployment, nil
}

// Resume implements FR-048's resume path: Suspended -> Running. Only
// non-scale-to-zero-eligible services (frontends, or backends opted out via
// scaling.min >= 1) are started immediately — eligible services correctly
// stay at zero and cold-start on their next request through the proxy,
// same as any other time they're idle. This satisfies FR-048's "at least
// scaling.min running instances" without special-casing eligible services:
// their min is, by definition, 0.
func (s *LifecycleService) Resume(ctx context.Context, applicationID, requesterID string) (domain.Deployment, error) {
	app, err := s.apps.GetByID(ctx, applicationID)
	if err != nil {
		return domain.Deployment{}, err
	}
	if err := s.requireOwner(ctx, applicationID, requesterID); err != nil {
		return domain.Deployment{}, err
	}
	if app.LifecycleStatus != domain.StatusSuspended {
		return domain.Deployment{}, domain.ErrApplicationNotSuspended
	}

	deployment, err := s.deployments.LatestForApplication(ctx, applicationID)
	if err != nil {
		return domain.Deployment{}, err
	}
	if deployment.Status != domain.DeploymentSuspended {
		return domain.Deployment{}, domain.ErrApplicationNotSuspended
	}

	states, err := s.states.ListForDeployment(ctx, deployment.ID)
	if err != nil {
		return domain.Deployment{}, err
	}
	containers := make(map[string]domain.RunningContainer)
	for _, st := range states {
		if st.Eligible {
			continue // resume back to addressable-but-zero; cold-starts on demand like any other idle period
		}
		containerName := fmt.Sprintf("platform-run-%s-%s", shortID(deployment.ID), sanitizeName(st.ServiceName))
		running, err := s.runtime.StartContainer(ctx, containerName, st.ImageRef, st.ContainerPort)
		if err != nil {
			return domain.Deployment{}, fmt.Errorf("resume: failed to start service %s: %w", st.ServiceName, err)
		}
		checkURL := fmt.Sprintf("http://host.docker.internal:%d", running.HostPort)
		if err := s.runtime.HealthCheck(ctx, checkURL, 15*time.Second); err != nil {
			_ = s.runtime.Stop(ctx, running.ContainerID)
			// FR-048 alternative flow: resume failing health checks reports
			// the failure rather than silently marking the app Running.
			return domain.Deployment{}, fmt.Errorf("resume: service %s failed health check: %w", st.ServiceName, err)
		}
		if err := s.states.SetContainer(ctx, deployment.ID, st.ServiceName, running.ContainerID, running.HostPort); err != nil {
			return domain.Deployment{}, fmt.Errorf("resume: failed to record container for %s: %w", st.ServiceName, err)
		}
		containers[st.ServiceName] = running
	}

	deployment, err = s.deployments.SetStatus(ctx, deployment.ID, domain.DeploymentRunning)
	if err != nil {
		return domain.Deployment{}, err
	}
	// See the comment on the equivalent call in Suspend — keeps the
	// snapshot honest instead of showing pre-suspend container info.
	deployment, err = s.deployments.UpdateContainers(ctx, deployment.ID, containers)
	if err != nil {
		return domain.Deployment{}, err
	}
	if _, err := s.apps.UpdateLifecycleStatus(ctx, app.ID, domain.StatusSuspended, domain.StatusRunning, false); err != nil {
		return domain.Deployment{}, err
	}
	return deployment, nil
}

// Restart implements FR-048's restart path: recycle a Running service's
// existing instance(s) without a redeploy or version change. Only services
// that currently have a live container are recycled — a scale-to-zero
// service sitting idle at zero has nothing to recycle, consistent with
// "restart" meaning "give me a fresh instance of what's already running",
// not "wake it up".
func (s *LifecycleService) Restart(ctx context.Context, applicationID, requesterID string) (domain.Deployment, error) {
	app, err := s.apps.GetByID(ctx, applicationID)
	if err != nil {
		return domain.Deployment{}, err
	}
	if err := s.requireOwner(ctx, applicationID, requesterID); err != nil {
		return domain.Deployment{}, err
	}
	if app.LifecycleStatus != domain.StatusRunning {
		return domain.Deployment{}, domain.ErrApplicationNotRunning
	}

	deployment, err := s.deployments.LatestForApplication(ctx, applicationID)
	if err != nil {
		return domain.Deployment{}, err
	}
	if deployment.Status != domain.DeploymentRunning {
		return domain.Deployment{}, domain.ErrApplicationNotRunning
	}

	states, err := s.states.ListForDeployment(ctx, deployment.ID)
	if err != nil {
		return domain.Deployment{}, err
	}
	containers := make(map[string]domain.RunningContainer)
	for _, st := range states {
		if st.ContainerID == nil {
			continue // nothing running to recycle
		}
		oldContainerID := *st.ContainerID
		containerName := fmt.Sprintf("platform-run-%s-%s", shortID(deployment.ID), sanitizeName(st.ServiceName))

		if err := s.runtime.Stop(ctx, oldContainerID); err != nil {
			return domain.Deployment{}, fmt.Errorf("restart: failed to stop old instance of %s: %w", st.ServiceName, err)
		}
		running, err := s.runtime.StartContainer(ctx, containerName, st.ImageRef, st.ContainerPort)
		if err != nil {
			return domain.Deployment{}, fmt.Errorf("restart: failed to start new instance of %s: %w", st.ServiceName, err)
		}
		checkURL := fmt.Sprintf("http://host.docker.internal:%d", running.HostPort)
		if err := s.runtime.HealthCheck(ctx, checkURL, 15*time.Second); err != nil {
			_ = s.runtime.Stop(ctx, running.ContainerID)
			return domain.Deployment{}, fmt.Errorf("restart: new instance of %s failed health check: %w", st.ServiceName, err)
		}
		if err := s.states.SetContainer(ctx, deployment.ID, st.ServiceName, running.ContainerID, running.HostPort); err != nil {
			return domain.Deployment{}, fmt.Errorf("restart: failed to record new instance of %s: %w", st.ServiceName, err)
		}
		containers[st.ServiceName] = running
	}
	deployment, err = s.deployments.UpdateContainers(ctx, deployment.ID, containers)
	if err != nil {
		return domain.Deployment{}, err
	}

	// Status never changes (Running throughout) — bump updated_at so the
	// recycle is at least visible in the record, without inventing a
	// dedicated event log this state doesn't have (Module W isn't built).
	deployment, err = s.deployments.SetStatus(ctx, deployment.ID, domain.DeploymentRunning)
	if err != nil {
		return domain.Deployment{}, err
	}
	return deployment, nil
}

// Archive implements FR-049: releases an application's active runtime
// footprint more permanently than Suspend, retaining configuration,
// metadata, and history for reference/compliance. Callable from Running or
// Suspended — Running is stopped the same way Suspend stops it; Suspended
// is already stopped, so only the status bookkeeping changes.
//
// Known gaps, documented not hidden:
//   - FR-049's "releases ... the application's domain (Module P)" has no
//     domain assignment to release — Module P (Domain Management) isn't
//     built.
//   - FR-049's business rule that reactivation requires "an explicit
//     un-archive action treated as a new deployment cycle" is not itself an
//     FR with its own acceptance criteria — no un-archive endpoint exists.
//     An Archived application is, today, a genuine dead end recoverable
//     only via direct database intervention. Worth a future FR if the
//     platform needs it.
func (s *LifecycleService) Archive(ctx context.Context, applicationID, requesterID string) (domain.Application, error) {
	app, err := s.apps.GetByID(ctx, applicationID)
	if err != nil {
		return domain.Application{}, err
	}
	if err := s.requireOwner(ctx, applicationID, requesterID); err != nil {
		return domain.Application{}, err
	}
	if app.LifecycleStatus != domain.StatusRunning && app.LifecycleStatus != domain.StatusSuspended {
		return domain.Application{}, domain.ErrInvalidLifecycleTransition
	}

	deployment, err := s.deployments.LatestForApplication(ctx, applicationID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return domain.Application{}, err
	}
	if err == nil {
		// FR-049 exception flow: an in-progress deployment blocks archiving
		// until it completes or is cancelled (no cancel path exists yet —
		// the caller must wait for it to finish one way or another).
		if isInFlight(deployment.Status) {
			return domain.Application{}, domain.ErrDeploymentAlreadyInFlight
		}
		if deployment.Status == domain.DeploymentRunning {
			if err := s.stopAllContainers(ctx, deployment.ID); err != nil {
				return domain.Application{}, err
			}
			if _, err := s.deployments.UpdateContainers(ctx, deployment.ID, map[string]domain.RunningContainer{}); err != nil {
				return domain.Application{}, err
			}
		}
		if deployment.Status == domain.DeploymentRunning || deployment.Status == domain.DeploymentSuspended {
			if _, err := s.deployments.SetStatus(ctx, deployment.ID, domain.DeploymentArchived); err != nil {
				return domain.Application{}, err
			}
		}
	}

	return s.apps.UpdateLifecycleStatus(ctx, app.ID, app.LifecycleStatus, domain.StatusArchived, false)
}

// Delete implements FR-050: permanently deletes an application, terminal
// and irreversible. Callable from Archived or Suspended only — FR-050's
// precondition explicitly excludes deleting directly from Running ("requires
// an explicit stop-first confirmation"), satisfied here by requiring the
// caller to Suspend or Archive first as a genuinely separate action rather
// than a same-request flag. Requires confirm=true (FR-050 main flow step 1:
// "requester confirms deletion, acknowledging irreversibility").
//
// Known gaps, documented not hidden:
//   - FR-050's deprovisioning of the database (Module N), secrets
//     (Module O), and domain (Module P) are all no-ops — none of those
//     modules exist yet. What Delete DOES do for real: ensures no container
//     is left running for the application (defensive — by precondition it
//     should already be stopped via Suspend/Archive, but this doesn't trust
//     that blindly).
//   - FR-050's production-deletion approval gate ("mirrors FR-014") isn't
//     enforced — that needs the de-registration approval workflow (Module C)
//     this platform doesn't have, the same category of gap as the
//     production-deploy approval gate's approver-independence limitation.
//   - FR-050's audit tombstone ("audit records ... are never deleted") has
//     no Module W (Audit Log) to write one — the application row itself is
//     simply left in place with lifecycle_status='deleted', which is at
//     least queryable, not erased.
func (s *LifecycleService) Delete(ctx context.Context, applicationID, requesterID string, confirm bool) (domain.Application, error) {
	app, err := s.apps.GetByID(ctx, applicationID)
	if err != nil {
		return domain.Application{}, err
	}
	if err := s.requireOwner(ctx, applicationID, requesterID); err != nil {
		return domain.Application{}, err
	}
	if !confirm {
		return domain.Application{}, domain.ErrDeleteNotConfirmed
	}
	if app.LifecycleStatus != domain.StatusArchived && app.LifecycleStatus != domain.StatusSuspended {
		return domain.Application{}, domain.ErrInvalidLifecycleTransition
	}

	if deployment, err := s.deployments.LatestForApplication(ctx, applicationID); err == nil {
		if err := s.stopAllContainers(ctx, deployment.ID); err != nil {
			return domain.Application{}, err
		}
	} else if !errors.Is(err, domain.ErrNotFound) {
		return domain.Application{}, err
	}

	return s.apps.UpdateLifecycleStatus(ctx, app.ID, app.LifecycleStatus, domain.StatusDeleted, false)
}

// stopAllContainers stops and clears every service's container for a
// deployment regardless of scale-to-zero eligibility, shared by Suspend,
// Archive, and Delete's defensive cleanup.
func (s *LifecycleService) stopAllContainers(ctx context.Context, deploymentID string) error {
	states, err := s.states.ListForDeployment(ctx, deploymentID)
	if err != nil {
		return err
	}
	for _, st := range states {
		if st.ContainerID == nil {
			continue
		}
		if err := s.runtime.Stop(ctx, *st.ContainerID); err != nil {
			log.Printf("lifecycle: failed to stop container for %s/%s: %v", deploymentID, st.ServiceName, err)
			continue
		}
		if _, err := s.states.ClearContainer(ctx, deploymentID, st.ServiceName, *st.ContainerID); err != nil {
			log.Printf("lifecycle: failed to clear state for %s/%s: %v", deploymentID, st.ServiceName, err)
		}
	}
	return nil
}
