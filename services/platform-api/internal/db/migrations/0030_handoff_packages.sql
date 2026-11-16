-- IT handoff packages (DEC-033). This platform is the development server:
-- an application promoted to production here is handed to IT, who run it
-- for real. Each package is one .tar of the production deployment that is
-- running — its images, a dump of its database, its source, and the files
-- to run it anywhere with Docker — kept on the handoff volume.
--
-- One package is kept per application: the newest that finished. A newer
-- one replaces it, and a restart closes any that were still being made.
CREATE TABLE handoff_packages (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id UUID NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    deployment_id  UUID NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    -- promotion: made by the platform when a production deployment went
    -- live. manual: an administrator or approver asked for it again.
    trigger        TEXT NOT NULL CHECK (trigger IN ('promotion', 'manual')),
    requested_by   UUID REFERENCES users(id),
    status         TEXT NOT NULL CHECK (status IN ('building', 'ready', 'failed', 'replaced')),
    error          TEXT NOT NULL DEFAULT '',
    file_name      TEXT NOT NULL DEFAULT '',
    size_bytes     BIGINT NOT NULL DEFAULT 0 CHECK (size_bytes >= 0),
    sha256         TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at   TIMESTAMPTZ,
    -- A manual package always names who asked; a promotion's never does.
    CHECK ((trigger = 'manual') = (requested_by IS NOT NULL)),
    -- A ready package has its file; a failed one says why.
    CHECK (status <> 'ready' OR (file_name <> '' AND size_bytes > 0 AND sha256 <> '')),
    CHECK (status <> 'failed' OR error <> ''),
    CHECK ((status = 'building') = (completed_at IS NULL))
);

-- One package in the making per application at a time.
CREATE UNIQUE INDEX one_building_handoff_per_application ON handoff_packages (application_id) WHERE status = 'building';
CREATE INDEX handoff_packages_by_application ON handoff_packages (application_id, created_at DESC);
