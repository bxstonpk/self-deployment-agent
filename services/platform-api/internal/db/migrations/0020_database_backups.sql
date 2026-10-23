-- FR-064 Backup Scheduling with DEC-025's policy (decided 2026-10-23): a
-- full backup every Sunday, a differential Monday to Saturday, and the
-- previous week's set deleted once a new full succeeds. See
-- internal/domain/backup.go. The backups themselves are sealed files on
-- the backup volume; this table is the platform's record of them.
CREATE TABLE database_backups (
    id             UUID PRIMARY KEY,
    application_id UUID NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    database_id    UUID NOT NULL REFERENCES provisioned_databases(id) ON DELETE CASCADE,
    kind           TEXT NOT NULL CHECK (kind IN ('full', 'differential')),
    base_backup_id UUID REFERENCES database_backups(id),
    trigger        TEXT NOT NULL CHECK (trigger IN ('scheduled', 'manual', 'pre_restore')),
    requested_by   UUID REFERENCES users(id),
    status         TEXT NOT NULL CHECK (status IN ('succeeded', 'failed', 'deleted')),
    error          TEXT NOT NULL DEFAULT '',
    file_name      TEXT NOT NULL DEFAULT '',
    stored_bytes   BIGINT NOT NULL DEFAULT 0,
    dump_bytes     BIGINT NOT NULL DEFAULT 0,
    dump_sha256    TEXT NOT NULL DEFAULT '',
    key_id         TEXT NOT NULL DEFAULT '',
    taken_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at     TIMESTAMPTZ,
    -- A differential always has a base, a full never does.
    CHECK ((kind = 'differential') = (base_backup_id IS NOT NULL) OR status = 'failed')
);

CREATE INDEX database_backups_by_application ON database_backups (application_id, taken_at DESC);

-- Owners are told when a backup fails.
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
    'deployment_status', 'approval_request', 'ownership_transfer', 'health_remediation', 'database_backup'
));
