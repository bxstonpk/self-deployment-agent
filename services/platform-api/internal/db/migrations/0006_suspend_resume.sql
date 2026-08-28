-- Adds Suspend/Resume/Restart (Module K, docs/02_Functional_Requirements.md
-- FR-047, FR-048): a Running application can be Suspended (stops all
-- traffic/compute, retains config), Resumed back to Running, or Restarted
-- in place (recycle instances, no redeploy, no version change).
--
-- Reuses the existing `deployments` table rather than inventing a parallel
-- one: 'suspended' becomes a new valid deployments.status alongside the
-- pipeline statuses from migration 0004. This means the scale-to-zero
-- proxy's existing CurrentRunning lookup (WHERE status = 'running') simply
-- stops finding a suspended deployment with NO code change needed there —
-- a suspended app is correctly unreachable/uncold-startable by construction,
-- not by an extra check bolted on.
--
-- The CHECK constraint is dropped and re-added via its actual name, found
-- by joining pg_constraint's key columns against pg_attribute rather than
-- pattern-matching pg_get_constraintdef()'s rendered text — confirmed the
-- hard way that Postgres rewrites `CHECK (status IN (...))` as
-- `CHECK ((status = ANY (ARRAY[...])))` when displaying it back, so a LIKE
-- '%IN%' pattern never matches and this silently found nothing to drop.
-- Matching on the actual constrained column is robust regardless of how
-- the definition is rendered.
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
    'running', 'failed', 'rejected', 'superseded', 'suspended'
));
