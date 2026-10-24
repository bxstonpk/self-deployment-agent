-- Tests run by the platform (DEC-029 part 2): an application's tests run in
-- a build sandbox on the platform, and a deployment can carry the report.
CREATE TABLE test_runs (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id UUID NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    requested_by   UUID NOT NULL REFERENCES users(id),
    status         TEXT NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'passed', 'failed', 'error')),
    -- domain.SourceTreeDigest of the archive the tests ran on.
    source_digest  TEXT NOT NULL,
    report         TEXT NOT NULL DEFAULT '',
    started_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at    TIMESTAMPTZ,
    CHECK ((status = 'running') = (finished_at IS NULL))
);
CREATE INDEX test_runs_application ON test_runs (application_id, started_at DESC);
