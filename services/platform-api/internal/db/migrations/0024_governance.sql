-- Development governance (the platform owner, 2026-10-24): the company
-- standard applies from the moment an application is started, not only when
-- it is deployed. Starting is registering, not asking permission; using
-- something outside the standard is an exception the administrator decides.

-- What an application is for, recorded when it is started (start_project).
CREATE TABLE application_profiles (
    application_id        UUID PRIMARY KEY REFERENCES applications(id) ON DELETE CASCADE,
    purpose               TEXT NOT NULL,
    users_description     TEXT NOT NULL DEFAULT '',
    handles_personal_data BOOLEAN NOT NULL,
    data_description      TEXT NOT NULL DEFAULT '',
    frontend              TEXT NOT NULL DEFAULT '',
    backend               TEXT NOT NULL DEFAULT '',
    database_engine       TEXT NOT NULL DEFAULT '',
    updated_by            UUID NOT NULL REFERENCES users(id),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Libraries the company doesn't allow, per ecosystem. A build that uses one
-- fails unless the application has an approved exception for it.
CREATE TABLE dependency_rules (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ecosystem  TEXT NOT NULL CHECK (ecosystem IN ('npm', 'go', 'pypi')),
    package    TEXT NOT NULL,
    reason     TEXT NOT NULL,
    created_by UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (ecosystem, package)
);

-- An application's request to step outside the standard, and the decision.
CREATE TABLE standard_exceptions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id  UUID NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    requested_by    UUID NOT NULL REFERENCES users(id),
    kind            TEXT NOT NULL CHECK (kind IN ('dependency', 'stack', 'other')),
    subject         TEXT NOT NULL,
    reason          TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected')),
    decided_by      UUID REFERENCES users(id),
    decision_reason TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    decided_at      TIMESTAMPTZ
);
CREATE INDEX standard_exceptions_pending ON standard_exceptions (created_at) WHERE status = 'pending';
CREATE INDEX standard_exceptions_application ON standard_exceptions (application_id);

ALTER TABLE notifications DROP CONSTRAINT notifications_category_check;
ALTER TABLE notifications ADD CONSTRAINT notifications_category_check CHECK (category IN (
    'deployment_status', 'approval_request', 'ownership_transfer', 'health_remediation', 'database_backup', 'alert',
    'standard_exception'
));
