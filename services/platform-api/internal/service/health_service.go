// Health implements Module R (docs/02_Functional_Requirements.md):
// FR-084 (continuous post-deploy health monitoring) and FR-085 (automatic
// remediation of an instance that fails it). FR-082 (default health check
// configuration) and FR-083 (the pre-activation gate) already exist —
// runtimeengine.DockerRuntime.HealthCheck, used at deploy/resume/restart/
// cold-start time (see deploy_service.go, lifecycle_service.go,
// scale_service.go). This adds the same check run continuously against
// every already-Running instance, not only at those gate points, and
// restarts one that starts failing it — the same Stop/StartContainer/
// HealthCheck sequence LifecycleService.Restart already uses for a
// requester-initiated restart, just system-triggered and scoped to the
// one instance that actually failed.
//
// Scope, documented not hidden:
//   - FR-099 (automatic rollback triggered by a health regression) builds
//     on this but isn't included here: distinguishing "a freshly activated
//     version regressed" from "an instance flaked" needs its own scoping
//     (a time window since Traffic Activation, and picking the rollback
//     target), and isn't required by FR-084/FR-085's own acceptance
//     criteria. See DeploymentService.Rollback's doc comment for this gap.
//   - "Remediation event is logged (Module S)" (FR-085 step 4) is
//     satisfied by Module S continuing to capture the replacement
//     container's output the same as any other — every StartContainer
//     call here carries the same Log source a normal restart would.
//   - The numbers below (failure debounce, remediation timeout, circuit
//     breaker limit/window) are this platform's own engineering defaults,
//     the same status as ScaleSweepInterval/MetricsSampleInterval: nothing
//     in the requirements specifies them.
package service

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"platform-api/internal/domain"
)

const (
	// healthCheckTimeout bounds one continuous-monitoring poll. Shorter
	// than FR-083's 15s deploy-time gate: this instance was healthy
	// moments ago, rather than just starting cold.
	healthCheckTimeout = 5 * time.Second

	// consecutiveFailuresBeforeRemediation debounces a single blip (a GC
	// pause, a dropped packet) from triggering a restart — only a
	// sustained failure, seen on this many consecutive sweeps, is treated
	// as FR-084's "begins failing health checks".
	consecutiveFailuresBeforeRemediation = 2

	// remediationHealthCheckTimeout is how long a freshly started
	// replacement instance gets to prove healthy before its remediation
	// is itself judged a failure — mirrors Restart's own 15s.
	remediationHealthCheckTimeout = 15 * time.Second

	// remediationCircuitLimit / remediationCircuitWindow implement FR-085's
	// alternative flow: "repeated remediation failures on the same service
	// within a short window ... may pause further automatic remediation
	// attempts pending human review, to avoid a restart loop masking a
	// systemic issue."
	remediationCircuitLimit  = 3
	remediationCircuitWindow = 5 * time.Minute
)

// ApplicationNamer is the narrow read this service needs for a
// notification's title — same "one method, named for its single caller"
// seam as SecretService's and ValidationService's own GetByID-only
// interfaces.
type ApplicationNamer interface {
	GetByID(ctx context.Context, id string) (domain.Application, error)
}

type HealthMonitorService struct {
	apps        ApplicationNamer
	deployments RunningDeploymentLookup
	states      ServiceRuntimeStateRepository
	resources   RuntimeWirer
	runtime     RuntimeEngine
	notify      NotificationRecorder

	mu           sync.Mutex
	failures     map[string]int         // deploymentID+"/"+serviceName -> consecutive failed sweeps
	remediations map[string][]time.Time // same key -> recent successful-remediation timestamps
	paused       map[string]bool        // same key -> escalation already sent for the current pause
}

func NewHealthMonitorService(
	apps ApplicationNamer, deployments RunningDeploymentLookup, states ServiceRuntimeStateRepository,
	resources RuntimeWirer, runtime RuntimeEngine, notify NotificationRecorder,
) *HealthMonitorService {
	return &HealthMonitorService{
		apps: apps, deployments: deployments, states: states, resources: resources, runtime: runtime, notify: notify,
		failures: make(map[string]int), remediations: make(map[string][]time.Time), paused: make(map[string]bool),
	}
}

func healthStateKey(deploymentID, serviceName string) string {
	return deploymentID + "/" + serviceName
}

// Sweep implements FR-084's polling loop: checks every instance currently
// in the serving pool (a scaled-to-zero service has nothing to poll — see
// ServiceRuntimeState.ScaledToZero and FR-084's own business rule), and
// hands a sustained failure to remediate. Intended to be called
// periodically by a background goroutine (see cmd/api/main.go).
func (s *HealthMonitorService) Sweep(ctx context.Context) (checked, remediated int, err error) {
	active, err := s.states.ListAllActive(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("list active services: %w", err)
	}

	for _, st := range active {
		if st.ContainerID == nil || st.HostPort == nil {
			continue // scaled to zero (or racing a concurrent change) by the time we got here
		}
		checked++
		key := healthStateKey(st.DeploymentID, st.ServiceName)
		checkURL := fmt.Sprintf("http://host.docker.internal:%d", *st.HostPort)
		if err := s.runtime.HealthCheck(ctx, checkURL, healthCheckTimeout); err == nil {
			s.resetFailures(key)
			continue
		}
		if !s.recordFailure(key) {
			continue // not sustained yet — one miss can be a blip
		}
		if s.remediate(ctx, st) {
			remediated++
		}
	}
	return checked, remediated, nil
}

// remediate implements FR-085: pulls the unhealthy instance from the
// serving pool and, unless the circuit breaker is open, starts a fresh
// one in its place. Every exit that leaves the service without a replacement
// running is deliberate: an instance known to be broken is never left
// silently serving traffic just because the platform gave up trying to fix
// it.
func (s *HealthMonitorService) remediate(ctx context.Context, st domain.ServiceRuntimeState) bool {
	key := healthStateKey(st.DeploymentID, st.ServiceName)

	deployment, err := s.deployments.GetByID(ctx, st.DeploymentID)
	if err != nil {
		log.Printf("health: failed to resolve deployment %s: %v", st.DeploymentID, err)
		return false
	}

	oldContainerID := *st.ContainerID
	cleared, err := s.states.ClearContainer(ctx, st.DeploymentID, st.ServiceName, oldContainerID)
	if err != nil {
		log.Printf("health: failed to claim %s/%s for remediation: %v", st.DeploymentID, st.ServiceName, err)
		return false
	}
	if !cleared {
		// Something else (a redeploy, a manual restart, the idle sweeper)
		// already changed this row since we observed it — same race
		// ScaleService.SweepIdle documents. Let the next sweep re-evaluate
		// whatever is there now rather than acting on stale data.
		log.Printf("health: %s/%s changed before remediation could claim it; left as-is", st.DeploymentID, st.ServiceName)
		s.resetFailures(key)
		return false
	}
	s.resetFailures(key)
	if err := s.runtime.Stop(ctx, oldContainerID); err != nil {
		log.Printf("health: failed to stop unhealthy container for %s/%s: %v", st.DeploymentID, st.ServiceName, err)
		// Not fatal — the container may already be gone, which is exactly
		// why it stopped answering health checks.
	}

	if open, alreadyNotified := s.circuitOpen(key); open {
		log.Printf("health: %s/%s has been remediated %d times in the last %s; pausing further automatic remediation",
			st.DeploymentID, st.ServiceName, remediationCircuitLimit, remediationCircuitWindow)
		if !alreadyNotified {
			s.escalate(ctx, deployment.ApplicationID, "Automatic remediation paused",
				fmt.Sprintf("%s has failed health checks and been automatically restarted %d times in the last %s. "+
					"To avoid masking a deeper problem, the platform stopped it rather than restarting it again — it currently has no running instance. Please investigate, then restart or redeploy.",
					st.ServiceName, remediationCircuitLimit, remediationCircuitWindow))
		}
		return false
	}

	wiring, err := s.resources.WiringFor(ctx, deployment.ApplicationID)
	if err != nil {
		log.Printf("health: failed to resolve database/secrets for %s/%s: %v", st.DeploymentID, st.ServiceName, err)
		return false
	}
	containerName := fmt.Sprintf("platform-run-%s-%s", shortID(st.DeploymentID), sanitizeName(st.ServiceName))
	running, err := s.runtime.StartContainer(ctx, domain.ContainerSpec{
		Name: containerName, ImageRef: st.ImageRef, ContainerPort: st.ContainerPort,
		Env: wiring.Env, NetworkID: wiring.NetworkID,
		Log: domain.LogSource{ApplicationID: deployment.ApplicationID, DeploymentID: st.DeploymentID, Service: st.ServiceName},
	})
	if err != nil {
		log.Printf("health: failed to start replacement instance of %s/%s: %v", st.DeploymentID, st.ServiceName, err)
		s.escalate(ctx, deployment.ApplicationID, "Automatic remediation failed",
			fmt.Sprintf("%s failed its health check, and the platform could not start a replacement instance. It currently has no running instance — please investigate and restart or redeploy.", st.ServiceName))
		return false
	}
	checkURL := fmt.Sprintf("http://host.docker.internal:%d", running.HostPort)
	if err := s.runtime.HealthCheck(ctx, checkURL, remediationHealthCheckTimeout); err != nil {
		log.Printf("health: replacement instance of %s/%s also failed its health check: %v", st.DeploymentID, st.ServiceName, err)
		_ = s.runtime.Stop(ctx, running.ContainerID)
		s.escalate(ctx, deployment.ApplicationID, "Automatic remediation failed",
			fmt.Sprintf("%s failed its health check, and the replacement instance also failed to become healthy. It currently has no running instance — please investigate and restart or redeploy.", st.ServiceName))
		return false
	}
	if err := s.states.SetContainer(ctx, st.DeploymentID, st.ServiceName, running.ContainerID, running.HostPort); err != nil {
		log.Printf("health: failed to record replacement instance of %s/%s: %v", st.DeploymentID, st.ServiceName, err)
		return false
	}

	s.recordRemediation(key)
	s.notify.NotifyOwners(ctx, deployment.ApplicationID, domain.NotificationHealthRemediation,
		fmt.Sprintf("%s was automatically restarted", st.ServiceName),
		fmt.Sprintf("The %s instance stopped responding to health checks and was automatically replaced. It is healthy again.", st.ServiceName),
		"application", deployment.ApplicationID)
	log.Printf("health: replaced unhealthy instance of %s/%s", st.DeploymentID, st.ServiceName)
	return true
}

func (s *HealthMonitorService) escalate(ctx context.Context, applicationID, title, detail string) {
	appName := applicationID
	if app, err := s.apps.GetByID(ctx, applicationID); err == nil {
		appName = app.Name
	}
	s.notify.NotifyOwners(ctx, applicationID, domain.NotificationHealthRemediation,
		fmt.Sprintf("%s: %s", appName, title), detail, "application", applicationID)
}

func (s *HealthMonitorService) resetFailures(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.failures, key)
}

// recordFailure returns true once the failure has been seen on
// consecutiveFailuresBeforeRemediation consecutive sweeps.
func (s *HealthMonitorService) recordFailure(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures[key]++
	return s.failures[key] >= consecutiveFailuresBeforeRemediation
}

// circuitOpen reports whether this service has already been remediated
// remediationCircuitLimit times within remediationCircuitWindow, and
// whether the pause has already been reported — so a service stuck
// failing gets one escalation, not a fresh notification on every sweep
// tick until it recovers or the window ages out.
func (s *HealthMonitorService) circuitOpen(key string) (open, alreadyNotified bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := time.Now().Add(-remediationCircuitWindow)
	kept := s.remediations[key][:0]
	for _, t := range s.remediations[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	s.remediations[key] = kept
	if len(kept) < remediationCircuitLimit {
		delete(s.paused, key)
		return false, false
	}
	alreadyNotified = s.paused[key]
	s.paused[key] = true
	return true, alreadyNotified
}

func (s *HealthMonitorService) recordRemediation(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.remediations[key] = append(s.remediations[key], time.Now())
}
