// Scale implements Module L (docs/02_Functional_Requirements.md):
// FR-051 (eligibility), FR-052 (idle scale-down), FR-053 (cold-start
// scale-up), FR-054 (min/max, partial — see doc comment below), FR-055
// (opt-out via scaling.min>=1, already implicit in FR-051's eligibility
// rule), FR-056 (scale event logging, best-effort per its own exception
// flow).
package service

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"time"

	"golang.org/x/sync/singleflight"
	"gopkg.in/yaml.v3"

	"platform-api/internal/domain"
)

type ServiceRuntimeStateRepository interface {
	Upsert(ctx context.Context, s domain.ServiceRuntimeState) error
	Get(ctx context.Context, deploymentID, serviceName string) (domain.ServiceRuntimeState, error)
	SetContainer(ctx context.Context, deploymentID, serviceName, containerID string, hostPort int) error
	ClearContainer(ctx context.Context, deploymentID, serviceName, expectedContainerID string) (bool, error)
	TouchActive(ctx context.Context, deploymentID, serviceName string) error
	ListEligibleActive(ctx context.Context) ([]domain.ServiceRuntimeState, error)
	// ListForDeployment returns every service's state regardless of
	// eligibility — used by Suspend/Resume/Restart (lifecycle_service.go),
	// which must act on ALL services, not just the scale-to-zero-eligible
	// ones ListEligibleActive is scoped to.
	ListForDeployment(ctx context.Context, deploymentID string) ([]domain.ServiceRuntimeState, error)
	DeleteForDeployment(ctx context.Context, deploymentID string) error
}

type ScaleEventRepository interface {
	Record(ctx context.Context, deploymentID, serviceName string, direction domain.ScaleDirection, triggerReason string) error
	ListForApplication(ctx context.Context, deploymentIDs []string, limit int) ([]domain.ScaleEvent, error)
}

type ApplicationByNameRepository interface {
	GetByName(ctx context.Context, name string) (domain.Application, error)
}

type RunningDeploymentLookup interface {
	CurrentRunning(ctx context.Context, applicationID string) (domain.Deployment, error)
}

type ScaleService struct {
	apps        ApplicationByNameRepository
	deployments RunningDeploymentLookup
	states      ServiceRuntimeStateRepository
	events      ScaleEventRepository
	stacks      StackRepository
	runtime     RuntimeEngine

	// coldStart coalesces concurrent EnsureRunning calls for the same
	// (deployment, service) — FR-053's alternative flow ("multiple
	// simultaneous requests during cold-start ... queued/buffered rather
	// than all separately triggering redundant start attempts"). It is
	// deliberately NOT shared with the idle sweeper: singleflight shares
	// ONE execution's result across all concurrent callers for a key,
	// which is correct when every caller wants the same outcome (a host
	// port) but would be wrong if a sweep-triggered shutdown and a
	// cold-start request happened to coalesce — a caller wanting a host
	// port could receive the sweeper's "stopped" result instead. The
	// remaining sweeper-vs-cold-start race (documented, not hidden) is
	// caught at the database layer instead — see ClearContainer's
	// compare-and-swap and the comment in SweepIdle.
	coldStart singleflight.Group
}

func NewScaleService(
	apps ApplicationByNameRepository, deployments RunningDeploymentLookup,
	states ServiceRuntimeStateRepository, events ScaleEventRepository,
	stacks StackRepository, runtime RuntimeEngine,
) *ScaleService {
	return &ScaleService{apps: apps, deployments: deployments, states: states, events: events, stacks: stacks, runtime: runtime}
}

// InitializeForDeployment implements FR-051: determines and persists each
// service's scale-to-zero eligibility the moment a deployment reaches
// Running, from service kind (backend runtimes only — this determination
// "cannot be overridden by configuration", per FR-051's business rule) and
// the app-wide `scaling.min` (FR-055's opt-out: min >= 1 means never
// eligible). Called once, right after the deployment pipeline activates
// traffic — see deploy_service.go.
func (s *ScaleService) InitializeForDeployment(ctx context.Context, deploymentID string, deploymentYAML string, imageRefs map[string]string, containers map[string]domain.RunningContainer) error {
	var parsed domain.DeploymentYAML
	if err := yaml.NewDecoder(bytes.NewReader([]byte(deploymentYAML))).Decode(&parsed); err != nil {
		return fmt.Errorf("parse deployment.yaml for scale initialization: %w", err)
	}

	appWideMin := 0
	if parsed.Scaling.Min != nil {
		appWideMin = *parsed.Scaling.Min
	}

	for name, svc := range parsed.Services {
		running, ok := containers[name]
		if !ok {
			continue // defensive; every declared service should have started a container in deployAndActivate
		}
		containerPort := svc.Port
		if containerPort <= 0 {
			containerPort = domain.DefaultContainerPort
		}
		kind, _, err := s.stacks.FindKind(ctx, svc.Runtime)
		if err != nil {
			return fmt.Errorf("determine service kind for %s: %w", name, err)
		}
		// FR-051: static frontends never eligible, regardless of scaling.min.
		eligible := kind == domain.StackKindBackend && appWideMin == 0

		containerID := running.ContainerID
		hostPort := running.HostPort
		if err := s.states.Upsert(ctx, domain.ServiceRuntimeState{
			DeploymentID: deploymentID, ServiceName: name, ImageRef: imageRefs[name],
			ContainerPort: containerPort, Eligible: eligible,
			ContainerID: &containerID, HostPort: &hostPort,
		}); err != nil {
			return fmt.Errorf("initialize scale state for %s: %w", name, err)
		}
		s.recordEventBestEffort(ctx, deploymentID, name, domain.ScaledUp, domain.TriggerInitialActivation)
	}
	return nil
}

// CleanupForDeployment removes runtime state for a superseded deployment —
// it's no longer live, so neither the proxy nor the sweeper should
// consider it. Containers are stopped by the caller (deploy_service.go),
// which already has their IDs from the supersede flow.
func (s *ScaleService) CleanupForDeployment(ctx context.Context, deploymentID string) error {
	return s.states.DeleteForDeployment(ctx, deploymentID)
}

// EnsureRunningByName implements the proxy's entry point (FR-053): resolve
// a public application/service name to its current live deployment, then
// ensure a container is up.
func (s *ScaleService) EnsureRunningByName(ctx context.Context, appName, serviceName string) (hostPort int, err error) {
	app, err := s.apps.GetByName(ctx, appName)
	if err != nil {
		return 0, err
	}
	deployment, err := s.deployments.CurrentRunning(ctx, app.ID)
	if err != nil {
		return 0, err
	}
	return s.EnsureRunning(ctx, deployment.ID, serviceName)
}

// EnsureRunning implements FR-052 (touch activity on a live service) and
// FR-053 (cold-start a scaled-to-zero one). Concurrent calls for the same
// key are coalesced via singleflight — see the field doc comment.
func (s *ScaleService) EnsureRunning(ctx context.Context, deploymentID, serviceName string) (int, error) {
	key := deploymentID + ":" + serviceName
	v, err, _ := s.coldStart.Do(key, func() (any, error) {
		state, err := s.states.Get(ctx, deploymentID, serviceName)
		if err != nil {
			return nil, err
		}

		if !state.ScaledToZero() {
			if err := s.states.TouchActive(ctx, deploymentID, serviceName); err != nil {
				log.Printf("scale: touch active failed for %s/%s: %v (serving anyway)", deploymentID, serviceName, err)
			}
			return *state.HostPort, nil
		}

		containerName := fmt.Sprintf("platform-run-%s-%s", shortID(deploymentID), sanitizeName(serviceName))
		running, err := s.runtime.StartContainer(ctx, containerName, state.ImageRef, state.ContainerPort)
		if err != nil {
			return nil, fmt.Errorf("cold start service %s: %w", serviceName, err)
		}

		checkURL := fmt.Sprintf("http://host.docker.internal:%d", running.HostPort)
		if err := s.runtime.HealthCheck(ctx, checkURL, 15*time.Second); err != nil {
			_ = s.runtime.Stop(ctx, running.ContainerID)
			return nil, fmt.Errorf("cold-started service %s failed health check: %w", serviceName, err)
		}

		if err := s.states.SetContainer(ctx, deploymentID, serviceName, running.ContainerID, running.HostPort); err != nil {
			return nil, fmt.Errorf("record cold-started container: %w", err)
		}
		s.recordEventBestEffort(ctx, deploymentID, serviceName, domain.ScaledUp, domain.TriggerColdStart)
		return running.HostPort, nil
	})
	if err != nil {
		return 0, err
	}
	return v.(int), nil
}

// SweepIdle implements FR-052: scales down every eligible service that's
// had no activity for idleTimeout. Intended to be called periodically by a
// background goroutine (see cmd/api/main.go).
//
// Known race, documented rather than hidden: this reads candidates, then
// acts on each individually without a distributed lock shared with
// EnsureRunning's singleflight (see that field's doc comment for why they
// aren't shared). The actual data-safety guard is ClearContainer's
// compare-and-swap: it only clears a row if container_id still matches
// what THIS sweep observed, so a concurrent cold-start's new container
// can never be silently lost from the database, even in the ID window
// where this function makes a wrong shutdown decision on stale data.
func (s *ScaleService) SweepIdle(ctx context.Context, idleTimeout time.Duration) (scaledDown int, err error) {
	candidates, err := s.states.ListEligibleActive(ctx)
	if err != nil {
		return 0, fmt.Errorf("list eligible active services: %w", err)
	}

	for _, c := range candidates {
		if time.Since(c.LastActiveAt) < idleTimeout {
			continue
		}
		if c.ContainerID == nil {
			continue // already scaled to zero by the time we got here
		}

		if err := s.runtime.Stop(ctx, *c.ContainerID); err != nil {
			log.Printf("scale: failed to stop idle container for %s/%s: %v", c.DeploymentID, c.ServiceName, err)
			continue
		}
		cleared, err := s.states.ClearContainer(ctx, c.DeploymentID, c.ServiceName, *c.ContainerID)
		if err != nil {
			log.Printf("scale: failed to clear state for %s/%s: %v", c.DeploymentID, c.ServiceName, err)
			continue
		}
		if !cleared {
			// A concurrent cold-start already replaced this container —
			// our stop() call above may have killed the OLD one after a
			// new one was already recorded. Rare, narrow window; logged
			// rather than silently ignored.
			log.Printf("scale: idle sweep for %s/%s raced a concurrent cold-start; state left as-is", c.DeploymentID, c.ServiceName)
			continue
		}
		scaledDown++
		s.recordEventBestEffort(ctx, c.DeploymentID, c.ServiceName, domain.ScaledToZero, domain.TriggerIdleTimeout)
	}
	return scaledDown, nil
}

func (s *ScaleService) ScaleEvents(ctx context.Context, deploymentIDs []string, limit int) ([]domain.ScaleEvent, error) {
	return s.events.ListForApplication(ctx, deploymentIDs, limit)
}

func (s *ScaleService) recordEventBestEffort(ctx context.Context, deploymentID, serviceName string, direction domain.ScaleDirection, reason string) {
	if err := s.events.Record(ctx, deploymentID, serviceName, direction, reason); err != nil {
		log.Printf("scale: failed to record %s event for %s/%s: %v", direction, deploymentID, serviceName, err)
	}
}
