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

type AuditService struct {
	repo   AuditRepository
	owners ApplicationOwnerRepository
}

func NewAuditService(repo AuditRepository, owners ApplicationOwnerRepository) *AuditService {
	return &AuditService{repo: repo, owners: owners}
}

// Record implements AuditRecorder. Every instrumented service method calls
// this immediately after its own state-changing write succeeds or fails —
// see e.g. lifecycle_service.go's Suspend. Deliberately best-effort with
// respect to *when* it runs relative to that write (not the same database
// transaction — no cross-repository transaction wrapper exists in this
// codebase), but not best-effort with respect to the caller finding out:
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

// Query implements FR-104, scoped to what this platform can actually
// authorize without the Auditor/Security Administrator/Platform
// Administrator roles FR-104 assumes (blocked on DEC-001/DEC-002): a
// requester sees an entry if they performed the action themselves, or it
// concerns an application they own.
//
// Known gap: FR-104's business rule that "every audit query is itself
// logged" (self-referential auditing, to prevent unchecked surveillance of
// employee activity) is not implemented — that would need this method to
// call Record on every read, which risks a logging feedback loop worth its
// own careful design rather than bolting on here.
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
		if e.ResourceType != "application" || e.ResourceID == "" {
			continue
		}
		owned, cached := ownedCache[e.ResourceID]
		if !cached {
			owned = s.isOwner(ctx, requesterID, e.ResourceID)
			ownedCache[e.ResourceID] = owned
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
