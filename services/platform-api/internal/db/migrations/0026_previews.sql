-- Previews (DEC-029 part 3): a short-lived copy of an application, to try a
-- change before deploying it. A preview is its own application, linked here
-- to the one it previews, so it has its own database, address and
-- lifecycle, and the application it previews is never touched. It is
-- deleted when it expires, or when its application is archived or deleted.
CREATE TABLE application_previews (
    application_id UUID PRIMARY KEY REFERENCES applications(id) ON DELETE CASCADE,
    parent_id      UUID NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    created_by     UUID NOT NULL REFERENCES users(id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at     TIMESTAMPTZ NOT NULL,
    -- Set once the preview has been torn down.
    ended_at       TIMESTAMPTZ,
    CHECK (application_id <> parent_id)
);
-- One live preview per application: a new one replaces it.
CREATE UNIQUE INDEX one_live_preview_per_application ON application_previews (parent_id) WHERE ended_at IS NULL;
CREATE INDEX application_previews_expiry ON application_previews (expires_at) WHERE ended_at IS NULL;

ALTER TABLE deployments DROP CONSTRAINT IF EXISTS deployments_environment_check;
ALTER TABLE deployments ADD CONSTRAINT deployments_environment_check
    CHECK (environment IN ('dev', 'production', 'preview'));
