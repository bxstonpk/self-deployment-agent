-- Adds 'health_remediation' as a valid notifications.category (Module R,
-- FR-085 step 4) — same drop-and-recreate-the-check-constraint pattern as
-- migration 0010's 'ownership_transfer' addition.
DO $$
DECLARE
    existing_constraint text;
BEGIN
    SELECT con.conname INTO existing_constraint
    FROM pg_constraint con
    JOIN pg_class rel ON rel.oid = con.conrelid
    JOIN pg_attribute att ON att.attrelid = rel.oid AND att.attnum = ANY(con.conkey)
    WHERE rel.relname = 'notifications' AND con.contype = 'c' AND att.attname = 'category';

    IF existing_constraint IS NOT NULL THEN
        EXECUTE format('ALTER TABLE notifications DROP CONSTRAINT %I', existing_constraint);
    END IF;
END $$;

ALTER TABLE notifications ADD CONSTRAINT notifications_category_check CHECK (category IN (
    'deployment_status', 'approval_request', 'ownership_transfer', 'health_remediation'
));
