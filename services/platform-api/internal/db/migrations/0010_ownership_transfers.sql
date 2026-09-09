-- Module E (docs/02_Functional_Requirements.md FR-016): a pending
-- ownership-transfer request, from the current primary owner to a
-- nominated new one, that the nominee must accept before it takes effect.
-- See internal/domain/ownership_transfer.go's package comment for the
-- scope this slice covers.
CREATE TABLE ownership_transfers (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id UUID NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    from_user_id   UUID NOT NULL REFERENCES users(id),
    to_user_id     UUID NOT NULL REFERENCES users(id),
    status         TEXT NOT NULL DEFAULT 'pending'
                       CHECK (status IN ('pending', 'accepted', 'expired')),
    initiated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at     TIMESTAMPTZ NOT NULL,
    resolved_at    TIMESTAMPTZ
);

-- FR-016's main flow implies one live transfer at a time per application —
-- a second nomination while one is already pending would be ambiguous
-- about which the nominee is actually accepting.
CREATE UNIQUE INDEX one_pending_transfer_per_application
    ON ownership_transfers (application_id)
    WHERE status = 'pending';

CREATE INDEX idx_ownership_transfers_application ON ownership_transfers (application_id);

-- Adds 'ownership_transfer' as a valid notifications.category — same
-- reuse-the-existing-table, column-join constraint-lookup pattern as
-- migration 0007 (not the pg_get_constraintdef() pattern-matching
-- approach migration 0006 originally used and later moved away from).
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
    'deployment_status', 'approval_request', 'ownership_transfer'
));
