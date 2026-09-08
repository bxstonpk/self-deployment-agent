-- Module W (docs/02_Functional_Requirements.md FR-103/104/105/106): an
-- append-only, hash-chained record of every significant state-changing
-- action this platform performs. Closes the gap flagged, and left
-- unaddressed, since migration 0001's own doc comment: "AuditLog (ENT-16 —
-- Module W, needed by FR-013 but not yet implemented)" — every lifecycle
-- migration since (0006 Suspend/Resume, 0007 Archive/Delete) and their
-- services carry the same "no Module W to write to" comment.
--
-- Scope adaptations, documented not hidden (see services/platform-api/README.md
-- and internal/domain/audit.go's package comment for the full list):
--   - No actor_type column: there is no distinct AI-agent/system actor
--     identity anywhere in this platform yet, only the acting user's id.
--   - FR-104's audit-access RBAC (Auditor/Security Administrator/Platform
--     Administrator) doesn't exist (blocked on DEC-001/DEC-002, the same
--     gap as everywhere else) — query access is scoped at the service
--     layer instead (AuditService.Query), not by a DB role/grant.
--   - FR-103's "audit write failure blocks the triggering action" is
--     approximated: the write happens immediately after the state change
--     succeeds (no cross-repository transaction wrapper exists in this
--     codebase to make them atomic), and its failure is surfaced to the
--     caller as an error rather than allowed to fail silently.
CREATE TABLE audit_log (
    id             UUID PRIMARY KEY,
    seq            BIGSERIAL NOT NULL UNIQUE,
    occurred_at    TIMESTAMPTZ NOT NULL,
    actor_user_id  UUID NOT NULL REFERENCES users(id),
    action         TEXT NOT NULL,
    resource_type  TEXT NOT NULL,
    resource_id    TEXT NOT NULL DEFAULT '',
    outcome        TEXT NOT NULL CHECK (outcome IN ('success', 'failure')),
    detail         TEXT NOT NULL DEFAULT '',
    -- FR-106 tamper-evidence chain. id/occurred_at are supplied by the
    -- application (not DB defaults like every other table's UUID/timestamp
    -- columns) because both must be fixed *before* entry_hash is computed
    -- over them — see AuditRepo.Record.
    prev_hash      TEXT NOT NULL,
    entry_hash     TEXT NOT NULL UNIQUE
);

CREATE INDEX idx_audit_log_actor ON audit_log (actor_user_id);
CREATE INDEX idx_audit_log_resource ON audit_log (resource_type, resource_id);
CREATE INDEX idx_audit_log_occurred_at ON audit_log (occurred_at);

-- FR-106's business rule in its strongest available form: "no role,
-- including Platform Administrator, has a standard platform capability to
-- delete or edit an individual audit entry" is enforced at the database
-- itself, not merely by the service layer never issuing UPDATE/DELETE.
CREATE OR REPLACE FUNCTION audit_log_immutable() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'audit_log is append-only: % is not permitted', TG_OP;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER audit_log_no_update BEFORE UPDATE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION audit_log_immutable();
CREATE TRIGGER audit_log_no_delete BEFORE DELETE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION audit_log_immutable();
