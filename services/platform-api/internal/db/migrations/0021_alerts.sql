-- FR-092 alerting with DEC-028's thresholds and rhythm (decided
-- 2026-10-23): see internal/domain/alert.go.

-- What is firing right now. Kept in the database, not memory, so a
-- restart neither forgets an alert nor announces it a second time.
CREATE TABLE alert_states (
    key              TEXT PRIMARY KEY,
    application_id   UUID REFERENCES applications(id) ON DELETE CASCADE,
    service          TEXT NOT NULL DEFAULT '',
    rule             TEXT NOT NULL,
    level            TEXT NOT NULL CHECK (level IN ('warning', 'critical')),
    value            DOUBLE PRECISION NOT NULL,
    message          TEXT NOT NULL,
    started_at       TIMESTAMPTZ NOT NULL,
    last_notified_at TIMESTAMPTZ NOT NULL
);

-- Every notification an alert produced. Purged with application logs,
-- after the same 90 days (DEC-027).
CREATE TABLE alert_events (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    key            TEXT NOT NULL,
    application_id UUID REFERENCES applications(id) ON DELETE CASCADE,
    service        TEXT NOT NULL DEFAULT '',
    rule           TEXT NOT NULL,
    level          TEXT NOT NULL DEFAULT '',
    kind           TEXT NOT NULL CHECK (kind IN ('fired', 'escalated', 'reminded', 'resolved')),
    value          DOUBLE PRECISION NOT NULL,
    message        TEXT NOT NULL,
    at             TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX alert_events_by_application ON alert_events (application_id, at DESC);
CREATE INDEX alert_events_by_age ON alert_events (at);

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
    'deployment_status', 'approval_request', 'ownership_transfer', 'health_remediation', 'database_backup', 'alert'
));
