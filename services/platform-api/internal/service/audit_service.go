// Audit implements Module W's service layer (docs/02_Functional_Requirements.md
// FR-103/104/105/106). See internal/domain/audit.go's package comment and
// internal/db/migrations/0008_audit_log.sql for the scope adaptations this
// slice makes (no distinct agent actor identity, no audit-access RBAC).
package service

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"log"
	"strconv"
	"time"

	"platform-api/internal/domain"
)

type AuditRepository interface {
	Record(ctx context.Context, entry domain.AuditEntry) (domain.AuditEntry, error)
	Query(ctx context.Context, q domain.AuditQuery) ([]domain.AuditEntry, error)
	VerifyChain(ctx context.Context) (ok bool, brokenAtSeq int64, err error)
}

// AuditRecorder is the narrow seam every other service depends on to write
// to the audit trail — exactly FR-103's write path, kept separate from the
// query/export/verify surface below that only the audit-log HTTP handler
// needs. Mirrors this codebase's existing pattern of narrow,
// consumer-shaped interfaces (see ApplicationLifecycleRepository's doc
// comment in validation_service.go).
type AuditRecorder interface {
	Record(ctx context.Context, entry domain.AuditEntry) error
}

// deploymentApplicationLookup and buildApplicationLookup are the narrow
// seams Query needs to extend FR-104's "or it concerns an application they
// own" scope check to deployment- and build-scoped entries
// (deployment.deploy/rollback/approval_decision, build.trigger) — not just
// application-scoped ones. Satisfied by DeploymentRepository/
// BuildRepository's existing GetByID methods; no wrapper needed.
type deploymentApplicationLookup interface {
	GetByID(ctx context.Context, deploymentID string) (domain.Deployment, error)
}

type buildApplicationLookup interface {
	GetByID(ctx context.Context, buildID string) (domain.Build, error)
}

type AuditService struct {
	repo        AuditRepository
	owners      ApplicationOwnerRepository
	deployments deploymentApplicationLookup
	builds      buildApplicationLookup
}

func NewAuditService(repo AuditRepository, owners ApplicationOwnerRepository, deployments deploymentApplicationLookup, builds buildApplicationLookup) *AuditService {
	return &AuditService{repo: repo, owners: owners, deployments: deployments, builds: builds}
}

// Record implements AuditRecorder. Every instrumented service method calls
// this immediately after its own state-changing write succeeds or fails —
// see e.g. lifecycle_service.go's Suspend. Deliberately best-effort with
// respect to *when* it runs relative to that write (not the same database
// transaction — no wrapper exists for spanning one across repositories;
// application_owner_repo.go's ReplacePrimaryOwner opens a transaction
// internally, but that's one repository guarding its own invariant, not a
// seam a service can join), but not best-effort about the caller finding out:
// unlike scale_event_repo.go's Record (whose doc comment explicitly asks
// callers to treat a failure as non-fatal), a failure here is returned as a
// real error, satisfying FR-103's "no critical action succeeds silently
// without an audit trail" as closely as this codebase's transaction model
// allows — the state change has already happened by the time this runs, so
// the caller sees an error for an action that nonetheless took effect,
// rather than a bare, unaudited success.
func (s *AuditService) Record(ctx context.Context, entry domain.AuditEntry) error {
	// Same reasoning as build_service.go's TriggerBuild / deploy_service.go's
	// markDeploymentFailedFrom: this write must survive the triggering
	// request's own context being cancelled (a client disconnect/timeout on
	// a slow action is exactly when losing the audit trail would matter
	// most), bounded so a genuinely dead database doesn't hang the caller
	// forever either.
	detachedCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()

	if _, err := s.repo.Record(detachedCtx, entry); err != nil {
		log.Printf("audit: FAILED to record %s on %s/%s (actor %s, outcome %s): %v",
			entry.Action, entry.ResourceType, entry.ResourceID, entry.ActorUserID, entry.Outcome, err)
		return fmt.Errorf("audit trail write failed: %w", err)
	}
	return nil
}

// isOwner reports whether requesterID is an active owner of applicationID,
// the same check every other service uses to gate write actions — reused
// here to gate audit *read* visibility per FR-104's scope reduction (see
// package doc comment).
func (s *AuditService) isOwner(ctx context.Context, requesterID, applicationID string) bool {
	owners, err := s.owners.ListForApplication(ctx, applicationID)
	if err != nil {
		return false
	}
	for _, o := range owners {
		if o.UserID == requesterID && o.Status == "active" {
			return true
		}
	}
	return false
}

// applicationIDFor resolves an entry's own resource back to the
// application it concerns, so FR-104's ownership check applies uniformly
// regardless of which resource type actually recorded the entry
// (deploy_service.go records under "deployment", build_service.go under
// "build" — only application_service.go/lifecycle_service.go/
// validation_service.go record directly under "application"). Returns ""
// for a resource type this doesn't (or can't) resolve — audit_log.export
// entries in particular are never resolved this way, since Export always
// records the requester as the actor, which the caller-is-actor branch
// above already handles.
func (s *AuditService) applicationIDFor(ctx context.Context, e domain.AuditEntry) string {
	switch e.ResourceType {
	case "application":
		return e.ResourceID
	case "deployment":
		dep, err := s.deployments.GetByID(ctx, e.ResourceID)
		if err != nil {
			return ""
		}
		return dep.ApplicationID
	case "build":
		build, err := s.builds.GetByID(ctx, e.ResourceID)
		if err != nil {
			return ""
		}
		return build.ApplicationID
	default:
		return ""
	}
}

// Query implements FR-104, scoped to what this platform can actually
// authorize without the Auditor/Security Administrator/Platform
// Administrator roles FR-104 assumes (blocked on DEC-001/DEC-002): a
// requester sees an entry if they performed the action themselves, or it
// concerns an application they own — regardless of which resource type
// (application/deployment/build) actually recorded it, per
// applicationIDFor's doc comment.
//
// Real bug found and fixed here: this used to check e.ResourceType ==
// "application" directly, silently skipping every deployment- and
// build-scoped entry (deployment.deploy, deployment.rollback,
// deployment.approval_decision, build.trigger) for anyone but the actor
// themselves — invisible until FR-017 (Co-Owner/Contributor Management)
// made a second owner on the same application possible for the first
// time. Verified live: with Bob granted co-owner access on an application
// Alice primary-owns, Bob deploying it produced a deployment.deploy entry
// Alice — the accountable primary owner — could not see via this query at
// all, despite the deployment concerning an application she owns.
func (s *AuditService) Query(ctx context.Context, requesterID string, q domain.AuditQuery) ([]domain.AuditEntry, error) {
	entries, err := s.repo.Query(ctx, q)
	if err != nil {
		return nil, err
	}

	ownedCache := make(map[string]bool)
	visible := make([]domain.AuditEntry, 0, len(entries))
	for _, e := range entries {
		if e.ActorUserID == requesterID {
			visible = append(visible, e)
			continue
		}
		applicationID := s.applicationIDFor(ctx, e)
		if applicationID == "" {
			continue
		}
		owned, cached := ownedCache[applicationID]
		if !cached {
			owned = s.isOwner(ctx, requesterID, applicationID)
			ownedCache[applicationID] = owned
		}
		if owned {
			visible = append(visible, e)
		}
	}
	return visible, nil
}

// Export implements FR-105: a CSV of an already-authorized query result
// set, with the export action itself recorded (main flow step 4). FR-105's
// "never includes plaintext secret values" holds vacuously today — Module O
// (Secret Management) doesn't exist, so no audit entry can reference one.
func (s *AuditService) Export(ctx context.Context, requesterID string, q domain.AuditQuery) ([]byte, error) {
	entries, err := s.Query(ctx, requesterID, q)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"seq", "occurred_at", "actor_user_id", "action", "resource_type", "resource_id", "outcome", "detail", "entry_hash"})
	for _, e := range entries {
		_ = w.Write([]string{
			strconv.FormatInt(e.Seq, 10), e.OccurredAt.Format(time.RFC3339), e.ActorUserID,
			string(e.Action), e.ResourceType, e.ResourceID, string(e.Outcome), e.Detail, e.EntryHash,
		})
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}

	if err := s.Record(ctx, domain.AuditEntry{
		ActorUserID: requesterID, Action: domain.AuditActionExport, ResourceType: "audit_log",
		Outcome: domain.AuditSuccess, Detail: fmt.Sprintf("exported %d entries", len(entries)),
	}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// VerifyIntegrity implements FR-106's detective control.
func (s *AuditService) VerifyIntegrity(ctx context.Context) (ok bool, brokenAtSeq int64, err error) {
	return s.repo.VerifyChain(ctx)
}
