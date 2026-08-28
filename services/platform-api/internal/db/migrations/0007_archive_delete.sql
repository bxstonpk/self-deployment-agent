-- Adds Archive and Delete (Module K, docs/02_Functional_Requirements.md
-- FR-049, FR-050) — the last two Application Lifecycle states reachable
-- without Modules N/O/P (Database/Secret/Domain Management), which don't
-- exist yet; real deprovisioning of those resources on Delete is a
-- documented gap, not silently skipped (see services/platform-api/README.md).
--
-- Same reuse-the-existing-table pattern as migration 0006: 'archived'
-- becomes a new valid deployments.status. No new value is needed for
-- Delete — by the time an application reaches Deleted, its deployment is
-- already 'suspended' or 'archived' (both preconditions for Delete per
-- FR-050), so there's nothing further to record at the deployment level.
--
-- applications.lifecycle_status already allows 'archived' and 'deleted' —
-- migration 0001 seeded the full fixed 10-state CHECK constraint up front,
-- unlike deployments.status which has grown incrementally as each state
-- was actually implemented.
DO $$
DECLARE
    existing_constraint text;
BEGIN
    SELECT con.conname INTO existing_constraint
    FROM pg_constraint con
    JOIN pg_class rel ON rel.oid = con.conrelid
    JOIN pg_attribute att ON att.attrelid = rel.oid AND att.attnum = ANY(con.conkey)
    WHERE rel.relname = 'deployments' AND con.contype = 'c' AND att.attname = 'status';

    IF existing_constraint IS NOT NULL THEN
        EXECUTE format('ALTER TABLE deployments DROP CONSTRAINT %I', existing_constraint);
    END IF;
END $$;

ALTER TABLE deployments ADD CONSTRAINT deployments_status_check CHECK (status IN (
    'scanning', 'pending_approval', 'deploying', 'health_check',
    'running', 'failed', 'rejected', 'superseded', 'suspended', 'archived'
));
