// Lifecycle implements the remainder of Module K
// (docs/02_Functional_Requirements.md) beyond registration/validation/
// build/deploy: FR-047 (Suspend) and FR-048 (Resume, Restart).
//
// Reuses the existing `deployments` table (migration 0006 adds 'suspended'
// as a new deployments.status value) rather than a parallel structure —
// see that migration's doc comment for why this makes the scale-to-zero
// proxy correctly refuse to serve/cold-start a suspended application with
// no code change: its CurrentRunning lookup (status = 'running') simply
// stops finding it.
package service

import (
	"context"
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

	states, err := s.states.ListForDeployment(ctx, deployment.ID)
	if err != nil {
		return domain.Deployment{}, err
	}
	for _, st := range states {
		if st.ContainerID == nil {
			continue // already scaled to zero — nothing to stop
		}
		if err := s.runtime.Stop(ctx, *st.ContainerID); err != nil {
			log.Printf("suspend: failed to stop container for %s/%s: %v", deployment.ID, st.ServiceName, err)
			continue
		}
		if _, err := s.states.ClearContainer(ctx, deployment.ID, st.ServiceName, *st.ContainerID); err != nil {
			log.Printf("suspend: failed to clear state for %s/%s: %v", deployment.ID, st.ServiceName, err)
		}
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
