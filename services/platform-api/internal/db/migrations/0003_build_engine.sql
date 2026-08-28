-- Adds the Build Engine (Module I, docs/02_Functional_Requirements.md
-- FR-035..FR-038): a governed base-image catalog (FR-037) and a record of
-- each build attempt with real status/failure tracking (FR-036, FR-038).
--
-- Scope note: this migration also adds applications.source_repository_reference
-- (ENT-05's documented attribute, docs/12_Data_Requirements.md) as an
-- informational/audit field. It is NOT used to fetch source for the build —
-- source is supplied as an uploaded tar.gz archive at build-request time
-- (see internal/service/build_service.go for why: the docs never specify a
-- git-hosting/branch convention, and inventing one wasn't defensible).

CREATE TABLE base_images (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    runtime         TEXT NOT NULL UNIQUE,
    image_reference TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'deprecated', 'blocked')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One governed base image per supported runtime (docs/06_System_Requirements.md
-- Supported Stack v1). Pinned to real, pullable public images so builds can
-- be exercised for real, not mocked.
-- Note on the go tag specifically, learned the hard way while exercising
-- the Image Scan gate (FR-041, Module J) for real against this catalog:
-- golang:1.23-alpine's OS packages (openssl) carried 23 CRITICALs; after
-- fixing the Dockerfile template to multi-stage (dropping the Go SDK
-- toolchain out of the runtime image entirely), the compiled binary's
-- EMBEDDED stdlib (from that same 1.23.4 toolchain) still carried a
-- CRITICAL TLS session-resumption CVE. golang:1.25-alpine scanned clean at
-- both layers when this migration was written. Per FR-022, base image
-- versions need ONGOING IT governance, not a one-time pin — this is a
-- starting point, not a guarantee it stays clean indefinitely.
INSERT INTO base_images (runtime, image_reference) VALUES
    ('go',      'golang:1.25-alpine'),
    ('nodejs',  'node:20-alpine'),
    ('python',  'python:3.12-alpine'),
    ('react',   'node:20-alpine'),
    ('nextjs',  'node:20-alpine'),
    ('vue',     'node:20-alpine');

ALTER TABLE applications
    ADD COLUMN source_repository_reference TEXT;

CREATE TABLE builds (
    id                          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id              UUID NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    triggered_by                UUID NOT NULL REFERENCES users(id),
    status                      TEXT NOT NULL DEFAULT 'queued'
                                    CHECK (status IN ('queued', 'in_progress', 'succeeded', 'failed')),
    -- FR-038: distinguish a source-code problem (employee-fixable) from a
    -- platform/infrastructure problem (not the employee's fault to debug).
    error_category              TEXT CHECK (error_category IN ('source', 'platform')),
    error_detail                TEXT,
    image_refs                  JSONB,
    started_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at                TIMESTAMPTZ
);

CREATE INDEX idx_builds_application ON builds (application_id, started_at DESC);

-- FR-036 business rule: build status is always attributable to a specific
-- application; at most one build may be in_progress per application at a
-- time (enforced defensively in code via this index, since Postgres partial
-- unique indexes are the same mechanism already used for application
-- ownership in migration 0001).
CREATE UNIQUE INDEX one_in_progress_build_per_application
    ON builds (application_id)
    WHERE status IN ('queued', 'in_progress');
