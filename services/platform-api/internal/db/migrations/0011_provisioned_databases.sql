-- Module N (docs/02_Functional_Requirements.md FR-061/062/063/065): the
-- platform's record of one application's dedicated, isolated managed
-- database. See internal/domain/database.go's package comment for the
-- scope this slice covers and what it deliberately doesn't.
--
-- On `password`: stored here in plaintext, because FR-063's intended
-- destination — Module O (Secret Management) — does not exist. This is a
-- real limitation, called out in the domain package comment and both
-- READMEs rather than buried: anyone with read access to this database
-- can read every application's database password. Module O landing is
-- what turns this column into a reference instead of a secret.
CREATE TABLE provisioned_databases (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id   UUID NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    engine           TEXT NOT NULL DEFAULT 'postgres' CHECK (engine IN ('postgres')),
    container_id     TEXT NOT NULL,
    network_id       TEXT NOT NULL,
    host             TEXT NOT NULL,
    port             INTEGER NOT NULL,
    database_name    TEXT NOT NULL,
    username         TEXT NOT NULL,
    password         TEXT NOT NULL,
    status           TEXT NOT NULL DEFAULT 'provisioned'
                         CHECK (status IN ('provisioned', 'deprovisioned')),
    provisioned_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    deprovisioned_at TIMESTAMPTZ
);

-- FR-061/FR-062: one live database per application, dedicated to it. A
-- deprovisioned row is retained as history (same reasoning as revoked
-- ownership rows), so the constraint is partial rather than a plain
-- UNIQUE on application_id.
CREATE UNIQUE INDEX one_live_database_per_application
    ON provisioned_databases (application_id)
    WHERE status = 'provisioned';
