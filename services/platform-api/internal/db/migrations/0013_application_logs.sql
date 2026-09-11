-- Module S (Logging) — docs/02_Functional_Requirements.md FR-086/087.
-- One row per line an application container wrote, captured as it was
-- written (internal/runtimeengine/logs.go), with any secret value the
-- platform injected into that container already redacted. Rows outlive the
-- containers they came from, which is the point: a container the platform
-- removes (a failed deploy, a restart, a scale-down) keeps its history.
-- Nothing is purged yet — FR-088's retention period is TBD.
CREATE TABLE application_logs (
    id             BIGSERIAL PRIMARY KEY,
    application_id UUID NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    deployment_id  UUID REFERENCES deployments(id) ON DELETE SET NULL,
    service        TEXT NOT NULL,
    container_id   TEXT NOT NULL,
    stream         TEXT NOT NULL CHECK (stream IN ('stdout', 'stderr')),
    logged_at      TIMESTAMPTZ NOT NULL,
    message        TEXT NOT NULL
);

-- FR-087's query: an application's most recent lines, newest first.
CREATE INDEX application_logs_by_application ON application_logs (application_id, logged_at DESC, id DESC);

-- Resuming collection after a platform-api restart asks where each
-- container left off.
CREATE INDEX application_logs_by_container ON application_logs (container_id, logged_at DESC);
