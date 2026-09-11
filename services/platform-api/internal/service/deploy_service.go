// Deploy implements Module J (docs/02_Functional_Requirements.md):
// FR-039 (initiate), FR-040 (ordered pipeline: Image Scan -> [production
// approval gate] -> Deployment -> Health Check -> Traffic Activation ->
// Completed), FR-041 (image scan gate), FR-042 (production approval gate),
// FR-043 (status tracking, via the persisted Deployment record), and the
// pre-activation half of FR-044 (a failed deploy/redeploy attempt never
// touches an already-Running version — see markDeploymentFailed).
//
// Scope adaptation: this platform already has a standalone Build state
// (see build_service.go), so deploying consumes the application's LATEST
// SUCCESSFUL build rather than re-triggering one inline, per the doc
// comment on migration 0004.
package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"platform-api/internal/domain"
)

type ImageScanner interface {
	Scan(ctx context.Context, imageRef string) (domain.ScanReport, error)
}

type RuntimeEngine interface {
	StartContainer(ctx context.Context, spec domain.ContainerSpec) (domain.RunningContainer, error)
	HealthCheck(ctx context.Context, url string, timeout time.Duration) error
	Stop(ctx context.Context, containerID string) error
}

type DeploymentRepository interface {
	Create(ctx context.Context, applicationID, buildID, requestedBy string, environment domain.Environment) (domain.Deployment, error)
	UpdateScanResult(ctx context.Context, deploymentID string, reports map[string]domain.ScanReport) (domain.Deployment, error)
	SetStatus(ctx context.Context, deploymentID string, status domain.DeploymentStatus) (domain.Deployment, error)
	SetFailed(ctx context.Context, deploymentID, reason string) (domain.Deployment, error)
	SetRejected(ctx context.Context, deploymentID, reason string) (domain.Deployment, error)
	SetRunning(ctx context.Context, deploymentID string, containers map[string]domain.RunningContainer) (domain.Deployment, error)
	SetSuperseded(ctx context.Context, deploymentID string) (domain.Deployment, error)
	UpdateContainers(ctx context.Context, deploymentID string, containers map[string]domain.RunningContainer) (domain.Deployment, error)
	GetByID(ctx context.Context, deploymentID string) (domain.Deployment, error)
	LatestForApplication(ctx context.Context, applicationID string) (domain.Deployment, error)
	PreviousRunning(ctx context.Context, applicationID, excludeDeploymentID string) (domain.Deployment, error)
	// ListForApplication implements the FR-095 version history read path:
	// every deployment ever attempted for an application, newest first. This
	// is what a rollback requester (FR-098 step 1) picks a target from.
	ListForApplication(ctx context.Context, applicationID string) ([]domain.Deployment, error)
}

type DeploymentApprovalRepository interface {
	Create(ctx context.Context, deploymentID, requestedBy string) (domain.DeploymentApproval, error)
	Decide(ctx context.Context, deploymentID, decidedBy string, decision domain.ApprovalDecision, reason string) (domain.DeploymentApproval, error)
}

// ScaleInitializer is the seam into Module L (scale_service.go): a newly
// Running deployment needs its per-service scale-to-zero eligibility
// determined and recorded, and a superseded deployment needs that state
// torn down so neither the idle sweeper nor the cold-start proxy consider
// it anymore.
type ScaleInitializer interface {
	InitializeForDeployment(ctx context.Context, deploymentID string, deploymentYAML string, imageRefs map[string]string, containers map[string]domain.RunningContainer) error
	CleanupForDeployment(ctx context.Context, deploymentID string) error
}

// ResourceProvisioner is the seam into Modules N and O
// (ApplicationResources): an application that declares a database gets one
// provisioned before its containers start (FR-061), and every container it
// starts is handed that database's connection environment and private
// network (FR-062/FR-063) plus the application's secrets (FR-067).
type ResourceProvisioner interface {
	EnsureProvisioned(ctx context.Context, app domain.Application, declaredType string) (domain.ProvisionedDatabase, error)
	WiringFor(ctx context.Context, applicationID string) (RuntimeWiring, error)
}

type DeploymentService struct {
	apps          ApplicationLifecycleRepository
	owners        ApplicationOwnerRepository
	builds        BuildRepository
	deployments   DeploymentRepository
	approvals     DeploymentApprovalRepository
	scanner       ImageScanner
	runtime       RuntimeEngine
	scale         ScaleInitializer
	audit         AuditRecorder
	notifications NotificationRecorder
	resources     ResourceProvisioner
}

func NewDeploymentService(
	apps ApplicationLifecycleRepository, owners ApplicationOwnerRepository, builds BuildRepository,
	deployments DeploymentRepository, approvals DeploymentApprovalRepository,
	scanner ImageScanner, runtime RuntimeEngine, scale ScaleInitializer, audit AuditRecorder, notifications NotificationRecorder,
	resources ResourceProvisioner,
) *DeploymentService {
	return &DeploymentService{
		apps: apps, owners: owners, builds: builds, deployments: deployments,
		approvals: approvals, scanner: scanner, runtime: runtime, scale: scale, audit: audit,
		notifications: notifications, resources: resources,
	}
}

// auditDeployOutcome records one deployment-pipeline action's terminal
// outcome, called via defer from InitiateDeploy/Rollback/DecideApproval
// after their guard clauses pass — see audit_service.go's package comment
// for the FR-103 scope boundary. deployment.Status alone decides
// success/failure here (not just err): a deploy/rollback attempt that
// fails partway through the pipeline (e.g. a health check failure) returns
// (deployment, nil) with Status Failed or Rejected, not a Go error — see
// deployAndActivate's and markDeploymentFailedFrom's doc comments.
// PendingApproval is treated as success: the requested action (submit for
// deployment) completed correctly, it's just paused at a gate the eventual
// DecideApproval call audits separately.
func (s *DeploymentService) auditDeployOutcome(ctx context.Context, action domain.AuditAction, applicationID, requesterID string, deployment *domain.Deployment, err *error) {
	outcome := domain.AuditSuccess
	detail := ""
	switch {
	case *err != nil:
		outcome, detail = domain.AuditFailure, (*err).Error()
	case deployment.Status == domain.DeploymentFailed:
		outcome = domain.AuditFailure
		if deployment.FailureReason != nil {
			detail = *deployment.FailureReason
		}
	case deployment.Status == domain.DeploymentRejected:
		outcome = domain.AuditFailure
		if deployment.RejectionReason != nil {
			detail = *deployment.RejectionReason
		}
	}
	resourceID := deployment.ID
	if resourceID == "" {
		resourceID = applicationID
	}
	if auditErr := s.audit.Record(ctx, domain.AuditEntry{
		ActorUserID: requesterID, Action: action,
		ResourceType: "deployment", ResourceID: resourceID, Outcome: outcome, Detail: detail,
	}); auditErr != nil && *err == nil {
		*err = fmt.Errorf("%s completed but audit trail failed to record: %w", action, auditErr)
	}
}

func (s *DeploymentService) requireOwner(ctx context.Context, applicationID, userID string) error {
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

func isInFlight(status domain.DeploymentStatus) bool {
	switch status {
	case domain.DeploymentScanning, domain.DeploymentPendingApproval, domain.DeploymentDeploying, domain.DeploymentHealthCheck:
		return true
	default:
		return false
	}
}

// InitiateDeploy implements FR-039: accepts a deployment request for an
// application with a successful build and drives it through Image Scan
// and, depending on environment, either the production approval gate or
// straight through to Deploy/HealthCheck/Activate (FR-042's alternative
// flow: dev skips the gate entirely).
func (s *DeploymentService) InitiateDeploy(ctx context.Context, applicationID, requesterID string, environment domain.Environment) (deployment domain.Deployment, err error) {
	if environment != domain.EnvironmentDev && environment != domain.EnvironmentProduction {
		return domain.Deployment{}, domain.ErrInvalidEnvironment
	}

	app, err := s.apps.GetByID(ctx, applicationID)
	if err != nil {
		return domain.Deployment{}, err
	}
	if err := s.requireOwner(ctx, applicationID, requesterID); err != nil {
		return domain.Deployment{}, err
	}

	build, err := s.builds.LatestForApplication(ctx, applicationID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.Deployment{}, domain.ErrNoSuccessfulBuild
		}
		return domain.Deployment{}, err
	}
	if build.Status != domain.BuildSucceeded {
		return domain.Deployment{}, domain.ErrNoSuccessfulBuild
	}

	if latest, err := s.deployments.LatestForApplication(ctx, applicationID); err == nil {
		if isInFlight(latest.Status) {
			return domain.Deployment{}, domain.ErrDeploymentAlreadyInFlight
		}
	} else if !errors.Is(err, domain.ErrNotFound) {
		return domain.Deployment{}, err
	}

	// Audited from here on — see auditDeployOutcome's doc comment for the
	// FR-103 scope boundary and why deployment.Status, not just err, decides
	// the recorded outcome.
	defer s.auditDeployOutcome(ctx, domain.AuditActionInitiateDeploy, applicationID, requesterID, &deployment, &err)

	deployment, err = s.deployments.Create(ctx, applicationID, build.ID, requesterID, environment)
	if err != nil {
		return domain.Deployment{}, err
	}

	return s.runScanThenBeyond(ctx, app, build, deployment, requesterID)
}

// runScanThenBeyond implements FR-041 (Image Scan Gate) and, on a pass,
// either pauses at the FR-042 production approval checkpoint or continues
// straight to deployAndActivate.
func (s *DeploymentService) runScanThenBeyond(ctx context.Context, app domain.Application, build domain.Build, deployment domain.Deployment, requesterID string) (domain.Deployment, error) {
	reports := make(map[string]domain.ScanReport, len(build.ImageRefs))
	allPassed := true
	for serviceName, imageRef := range build.ImageRefs {
		report, err := s.scanner.Scan(ctx, imageRef)
		if err != nil {
			return s.markDeploymentFailed(ctx, app, deployment,
				fmt.Sprintf("image scan error for service %s: %v", serviceName, err))
		}
		reports[serviceName] = report
		if !report.Passed {
			allPassed = false
		}
	}

	deployment, err := s.deployments.UpdateScanResult(ctx, deployment.ID, reports)
	if err != nil {
		return domain.Deployment{}, err
	}

	if !allPassed {
		return s.markDeploymentFailed(ctx, app, deployment,
			"image scan found blocking-severity (CRITICAL) vulnerabilities — see scan_reports for detail")
	}

	if deployment.Environment == domain.EnvironmentProduction {
		deployment, err = s.deployments.SetStatus(ctx, deployment.ID, domain.DeploymentPendingApproval)
		if err != nil {
			return domain.Deployment{}, err
		}
		if _, err := s.approvals.Create(ctx, deployment.ID, requesterID); err != nil {
			return domain.Deployment{}, err
		}
		// FR-108: recipients are every active owner, standing in for "the
		// designated approver(s)" — see NotifyOwners's doc comment for why.
		s.notifications.NotifyOwners(ctx, app.ID, domain.NotificationApprovalRequest,
			fmt.Sprintf("%s: production deployment awaiting approval", app.Name),
			fmt.Sprintf("Deployment %s is awaiting production approval.", deployment.ID),
			"deployment", deployment.ID)
		return deployment, nil // pipeline pauses here — FR-042 main flow step 1
	}

	return s.deployAndActivate(ctx, app, build, deployment, domain.StatusDeploying)
}

// DecideApproval implements FR-042's approval decision handling.
//
// Known gap (documented, not silently skipped): this does not enforce
// approver != requester. FR-042's business rule says production deploys
// "can never bypass this gate regardless of requester role" — properly
// guaranteeing a genuinely independent approver needs the RBAC/Platform
// Administrator role modeling that Module A/B doesn't have yet (blocked on
// DEC-001/DEC-002, docs/17_Decision_Log.md). Requiring a *different* owner
// today would make single-owner applications undeployable to production,
// which is a worse outcome than documenting the gap honestly.
func (s *DeploymentService) DecideApproval(ctx context.Context, deploymentID, approverID string, approve bool, reason string) (deployment domain.Deployment, err error) {
	deployment, err = s.deployments.GetByID(ctx, deploymentID)
	if err != nil {
		return domain.Deployment{}, err
	}
	if deployment.Status != domain.DeploymentPendingApproval {
		return domain.Deployment{}, domain.ErrDeploymentNotPendingApproval
	}
	if err := s.requireOwner(ctx, deployment.ApplicationID, approverID); err != nil {
		return domain.Deployment{}, err
	}

	// Audited from here on. Unlike auditDeployOutcome (InitiateDeploy/
	// Rollback), a Rejected deployment.Status here is NOT a failure of THIS
	// action — DecideApproval's job is to durably record whichever decision
	// the approver made, and it did so correctly either way. Outcome
	// reflects whether the decision was recorded, not which way it went.
	defer func() {
		outcome := domain.AuditSuccess
		detail := "approved"
		if !approve {
			detail = "rejected"
			if reason != "" {
				detail = "rejected: " + reason
			}
		}
		if err != nil {
			outcome, detail = domain.AuditFailure, err.Error()
		}
		if auditErr := s.audit.Record(ctx, domain.AuditEntry{
			ActorUserID: approverID, Action: domain.AuditActionDecideApproval,
			ResourceType: "deployment", ResourceID: deploymentID, Outcome: outcome, Detail: detail,
		}); auditErr != nil && err == nil {
			err = fmt.Errorf("approval decision recorded but audit trail failed to record: %w", auditErr)
		}
	}()

	decision := domain.ApprovalRejected
	if approve {
		decision = domain.ApprovalApproved
	}
	if _, err := s.approvals.Decide(ctx, deploymentID, approverID, decision, reason); err != nil {
		return domain.Deployment{}, err
	}

	app, err := s.apps.GetByID(ctx, deployment.ApplicationID)
	if err != nil {
		return domain.Deployment{}, err
	}

	if !approve {
		deployment, err = s.deployments.SetRejected(ctx, deploymentID, reason)
		if err != nil {
			return domain.Deployment{}, err
		}
		if app.LifecycleStatus != domain.StatusRunning {
			if _, err := s.apps.UpdateLifecycleStatus(ctx, app.ID, app.LifecycleStatus, domain.StatusFailed, false); err != nil {
				return domain.Deployment{}, err
			}
		}
		// FR-107: rejection is a definitive "this deployment did not go
		// through" milestone, distinct from a mid-pipeline failure but
		// worth the same notification — found while manually verifying
		// approval-request notifications: the approver already knows they
		// just rejected it, but every OTHER owner only finds out here.
		s.notifications.NotifyOwners(ctx, app.ID, domain.NotificationDeploymentStatus,
			fmt.Sprintf("%s: production deployment rejected", app.Name),
			fmt.Sprintf("Deployment %s was rejected: %s", deployment.ID, reason),
			"deployment", deployment.ID)
		return deployment, nil
	}

	build, err := s.builds.GetByID(ctx, deployment.BuildID)
	if err != nil {
		return domain.Deployment{}, err
	}
	return s.deployAndActivate(ctx, app, build, deployment, domain.StatusDeploying)
}

// deployAndActivate implements the Deployment, Health Check, and Traffic
// Activation pipeline steps. On success the application moves to Running;
// if a different deployment was already Running for this application, its
// containers are stopped as a clean cutover to the new version.
//
// transientAppStatus is the application-level LifecycleStatus to occupy
// while this activation is in flight: StatusDeploying for a normal forward
// deploy, StatusRolledBack for Rollback (FR-101's business rule — the
// pipeline mechanics are otherwise identical, mirroring FR-101's own framing
// of rollback as "redeploying the target version's build artifact").
func (s *DeploymentService) deployAndActivate(ctx context.Context, app domain.Application, build domain.Build, deployment domain.Deployment, transientAppStatus domain.LifecycleStatus) (domain.Deployment, error) {
	fromAppStatus := app.LifecycleStatus
	wasAlreadyRunning := fromAppStatus == domain.StatusRunning

	var parsed domain.DeploymentYAML
	if err := yaml.NewDecoder(bytes.NewReader([]byte(app.DeploymentYAMLDraft))).Decode(&parsed); err != nil {
		return s.markDeploymentFailed(ctx, app, deployment, fmt.Sprintf("deployment.yaml no longer parses: %v", err))
	}

	deployment, err := s.deployments.SetStatus(ctx, deployment.ID, domain.DeploymentDeploying)
	if err != nil {
		return domain.Deployment{}, err
	}
	if _, err := s.apps.UpdateLifecycleStatus(ctx, app.ID, fromAppStatus, transientAppStatus, false); err != nil {
		return domain.Deployment{}, err
	}
	app.LifecycleStatus = transientAppStatus // keep local copy consistent for the failure helper below

	// FR-061: an application that declares a database gets a real, isolated
	// one before any of its containers start. Idempotent — a redeploy keeps
	// the database (and its data) it already had. A provisioning failure
	// fails the deployment rather than starting an application that would
	// come up without the database it declared, per FR-063's exception flow
	// ("credential injection fails -> start is blocked").
	if declared := strings.TrimSpace(parsed.Database.Type); declared != "" {
		if _, err := s.resources.EnsureProvisioned(ctx, app, declared); err != nil {
			return s.markDeploymentFailedFrom(ctx, app.ID, transientAppStatus, wasAlreadyRunning, deployment,
				fmt.Sprintf("failed to provision the declared %s database: %v", declared, err))
		}
	}
	wiring, err := s.resources.WiringFor(ctx, app.ID)
	if err != nil {
		return s.markDeploymentFailedFrom(ctx, app.ID, transientAppStatus, wasAlreadyRunning, deployment,
			fmt.Sprintf("failed to resolve the application's database and secrets: %v", err))
	}

	containers := make(map[string]domain.RunningContainer, len(build.ImageRefs))
	for serviceName, imageRef := range build.ImageRefs {
		containerPort := parsed.Services[serviceName].Port
		if containerPort <= 0 {
			containerPort = domain.DefaultContainerPort
		}
		containerName := fmt.Sprintf("platform-run-%s-%s-%s", sanitizeName(app.Name), sanitizeName(serviceName), shortID(deployment.ID))

		running, err := s.runtime.StartContainer(ctx, domain.ContainerSpec{
			Name: containerName, ImageRef: imageRef, ContainerPort: containerPort,
			Env: wiring.Env, NetworkID: wiring.NetworkID,
			Log: domain.LogSource{ApplicationID: app.ID, DeploymentID: deployment.ID, Service: serviceName},
		})
		if err != nil {
			return s.markDeploymentFailedFrom(ctx, app.ID, transientAppStatus, wasAlreadyRunning, deployment,
				fmt.Sprintf("failed to start container for service %s: %v", serviceName, err))
		}
		containers[serviceName] = running
	}

	deployment, err = s.deployments.SetStatus(ctx, deployment.ID, domain.DeploymentHealthCheck)
	if err != nil {
		return domain.Deployment{}, err
	}

	for serviceName, running := range containers {
		// NOT running.URL: that's "http://localhost:<hostPort>", meaningful
		// only from the *host* machine's browser (what gets reported back
		// to the employee). From inside the platform-api container itself,
		// "localhost" means platform-api's own network namespace, not the
		// sibling container just started — health checks go through
		// host.docker.internal instead. See docker-compose.yml's
		// extra_hosts entry (needed for portability to Linux Docker Engine,
		// where that name isn't automatic like it is on Docker Desktop).
		internalCheckURL := fmt.Sprintf("http://host.docker.internal:%d", running.HostPort)
		if err := s.runtime.HealthCheck(ctx, internalCheckURL, 15*time.Second); err != nil {
			for _, c := range containers {
				_ = s.runtime.Stop(ctx, c.ContainerID)
			}
			return s.markDeploymentFailedFrom(ctx, app.ID, transientAppStatus, wasAlreadyRunning, deployment,
				fmt.Sprintf("service %s failed health check: %v", serviceName, err))
		}
	}

	deployment, err = s.deployments.SetRunning(ctx, deployment.ID, containers)
	if err != nil {
		return domain.Deployment{}, err
	}
	if _, err := s.apps.UpdateLifecycleStatus(ctx, app.ID, transientAppStatus, domain.StatusRunning, false); err != nil {
		return domain.Deployment{}, err
	}

	// FR-107: the deployment/rollback pipeline reached its successful
	// milestone (Completed -> Running).
	s.notifications.NotifyOwners(ctx, app.ID, domain.NotificationDeploymentStatus,
		fmt.Sprintf("%s: deployment succeeded", app.Name),
		fmt.Sprintf("Deployment %s is now running.", deployment.ID),
		"deployment", deployment.ID)

	// Module L (FR-051): determine and persist each service's
	// scale-to-zero eligibility now that the deployment is live. Not
	// pipeline-fatal if it fails — the deployment already succeeded and is
	// serving traffic; a scale-management gap is logged, not rolled back.
	if err := s.scale.InitializeForDeployment(ctx, deployment.ID, app.DeploymentYAMLDraft, build.ImageRefs, containers); err != nil {
		log.Printf("deploy: scale-to-zero initialization failed for deployment %s: %v", deployment.ID, err)
	}

	if previous, err := s.deployments.PreviousRunning(ctx, app.ID, deployment.ID); err == nil {
		for _, c := range previous.Containers {
			_ = s.runtime.Stop(ctx, c.ContainerID)
		}
		if _, err := s.deployments.SetSuperseded(ctx, previous.ID); err != nil {
			return domain.Deployment{}, err
		}
		if err := s.scale.CleanupForDeployment(ctx, previous.ID); err != nil {
			log.Printf("deploy: scale-to-zero cleanup failed for superseded deployment %s: %v", previous.ID, err)
		}
	} else if !errors.Is(err, domain.ErrNotFound) {
		return domain.Deployment{}, err
	}

	return deployment, nil
}

// markDeploymentFailed is the pre-Deploying failure path (scan failures):
// the application never left its original state, so that's what "from" is.
func (s *DeploymentService) markDeploymentFailed(ctx context.Context, app domain.Application, deployment domain.Deployment, reason string) (domain.Deployment, error) {
	return s.markDeploymentFailedFrom(ctx, app.ID, app.LifecycleStatus, app.LifecycleStatus == domain.StatusRunning, deployment, reason)
}

// markDeploymentFailedFrom implements FR-044's alternative flow: a failure
// that happens while a PREVIOUS version was already Running (a redeploy
// attempt) must leave that previous version's Running status untouched —
// there is no employee-visible downtime from a failed redeploy attempt.
// Only a first-ever deploy failing (no prior good version) moves the
// application to Failed.
func (s *DeploymentService) markDeploymentFailedFrom(ctx context.Context, applicationID string, fromAppStatus domain.LifecycleStatus, wasAlreadyRunning bool, deployment domain.Deployment, reason string) (domain.Deployment, error) {
	// Detached, same reasoning as build_service.go's TriggerBuild failure
	// path: this IS the failure-cleanup write, often reached because `ctx`
	// was cancelled out from under a slow deploy attempt (health checks
	// alone can take up to 15s) — using the same cancelled `ctx` here risks
	// this write failing too, leaving the deployment/application stuck in
	// a transient status (`deploying`/`health_check`) that nothing accepts
	// as a valid starting state, permanently unrecoverable through the API.
	detachedCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()

	deployment, err := s.deployments.SetFailed(detachedCtx, deployment.ID, reason)
	if err != nil {
		return domain.Deployment{}, err
	}

	target := domain.StatusFailed
	if wasAlreadyRunning {
		target = domain.StatusRunning
	}
	if _, err := s.apps.UpdateLifecycleStatus(detachedCtx, applicationID, fromAppStatus, target, false); err != nil {
		return domain.Deployment{}, err
	}

	// FR-107: the deployment/rollback pipeline reached its failure
	// milestone. Uses detachedCtx too — same reasoning as the writes above
	// in this function, this notification would otherwise silently never
	// fire for exactly the slow-request-cancellation case where a human
	// finding out asynchronously matters most.
	s.notifications.NotifyOwners(detachedCtx, applicationID, domain.NotificationDeploymentStatus,
		"Deployment failed", reason, "deployment", deployment.ID)

	return deployment, nil
}

func (s *DeploymentService) LatestDeployment(ctx context.Context, applicationID string) (domain.Deployment, error) {
	return s.deployments.LatestForApplication(ctx, applicationID)
}

// GetDeployment looks up a single deployment by its own id, independent of
// which application it belongs to — needed by the MCP server's
// get_deployment_status tool (docs/07_MCP_Requirements.md Section 13.8),
// which polls by deployment_id, not application_id.
func (s *DeploymentService) GetDeployment(ctx context.Context, deploymentID string) (domain.Deployment, error) {
	return s.deployments.GetByID(ctx, deploymentID)
}

// DeploymentHistory implements FR-095's read path: every deployment ever
// attempted for the application, newest first, so a rollback requester
// (FR-098 step 1) has something to pick a target from.
func (s *DeploymentService) DeploymentHistory(ctx context.Context, applicationID string) ([]domain.Deployment, error) {
	return s.deployments.ListForApplication(ctx, applicationID)
}

// Rollback implements Module V (FR-098, FR-100, FR-101): redeploys a
// previously-successful deployment's build artifact and configuration in
// place of the current one. Mechanically this is the same
// Deploy/HealthCheck/Activate pipeline as a forward deploy
// (deployAndActivate) — FR-101 itself frames rollback as "redeploying the
// target version's build artifact" — just sourced from an older build
// instead of the application's latest, and marked with the Rolled Back
// transient status per FR-101's business rule instead of Deploying.
//
// Scope adaptations, documented not hidden:
//   - Skips re-running the Image Scan (FR-041) and production approval gate
//     (FR-042) that a forward deploy goes through: the target was already a
//     completed, previously-Running deployment, so it passed both gates the
//     first time. FR-098's alternative flow allows requiring the same
//     approval gate for a production rollback depending on policy severity
//     classification, which needs policy-tier modeling this platform
//     doesn't have yet (same RBAC/policy gap as DecideApproval's
//     approver-independence gap, blocked on DEC-001/DEC-002).
//   - FR-099 (fully automatic rollback triggered by a post-activation health
//     regression) isn't wired to a trigger: that needs continuous runtime
//     health monitoring (Module T), which isn't built. What IS already true
//     without any new code: FR-044's existing pre-activation failure
//     handling means a failed forward deploy attempt never touches an
//     already-Running previous version in the first place — see
//     markDeploymentFailedFrom. FR-098 (this method) covers the deliberate,
//     requester-initiated rollback path.
func (s *DeploymentService) Rollback(ctx context.Context, applicationID, requesterID, targetDeploymentID string) (deployment domain.Deployment, err error) {
	app, err := s.apps.GetByID(ctx, applicationID)
	if err != nil {
		return domain.Deployment{}, err
	}
	if err := s.requireOwner(ctx, applicationID, requesterID); err != nil {
		return domain.Deployment{}, err
	}
	// Rollback makes sense from a Running application (rolling away from a
	// version that's live but misbehaving) or a Failed one (the forward
	// deploy attempt itself failed outright) — not from a transient
	// pipeline state, and not from Suspended, which has no live traffic to
	// roll back in the first place.
	if app.LifecycleStatus != domain.StatusRunning && app.LifecycleStatus != domain.StatusFailed {
		return domain.Deployment{}, domain.ErrInvalidLifecycleTransition
	}

	if current, err := s.deployments.LatestForApplication(ctx, applicationID); err == nil {
		if isInFlight(current.Status) {
			return domain.Deployment{}, domain.ErrDeploymentAlreadyInFlight
		}
	} else if !errors.Is(err, domain.ErrNotFound) {
		return domain.Deployment{}, err
	}

	target, err := s.deployments.GetByID(ctx, targetDeploymentID)
	if err != nil {
		return domain.Deployment{}, err
	}
	if target.ApplicationID != applicationID {
		return domain.Deployment{}, domain.ErrNotFound
	}
	// FR-100: the target must itself have been a successfully completed
	// deployment (currently Running, or Superseded by a later one) —
	// never Failed, Rejected, or still in flight.
	if target.Status != domain.DeploymentRunning && target.Status != domain.DeploymentSuperseded {
		return domain.Deployment{}, domain.ErrInvalidRollbackTarget
	}

	build, err := s.builds.GetByID(ctx, target.BuildID)
	if err != nil {
		return domain.Deployment{}, err
	}
	// FR-100: the target's build artifact must still be available, not
	// purged by retention policy (FR-097).
	if build.Status != domain.BuildSucceeded {
		return domain.Deployment{}, domain.ErrInvalidRollbackTarget
	}

	// Audited from here on — see auditDeployOutcome's doc comment for the
	// FR-103 scope boundary.
	defer s.auditDeployOutcome(ctx, domain.AuditActionRollback, applicationID, requesterID, &deployment, &err)

	deployment, err = s.deployments.Create(ctx, applicationID, build.ID, requesterID, target.Environment)
	if err != nil {
		return domain.Deployment{}, err
	}
	return s.deployAndActivate(ctx, app, build, deployment, domain.StatusRolledBack)
}

var nonNameChars = regexp.MustCompile(`[^a-z0-9-]+`)

func sanitizeName(s string) string {
	s = strings.ToLower(s)
	s = nonNameChars.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
