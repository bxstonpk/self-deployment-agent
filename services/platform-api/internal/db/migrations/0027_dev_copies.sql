-- Previews (0026) are replaced by dev copies: the platform owner decided on
-- 2026-10-24 that an employee changing an application should keep the data
-- they entered, rather than lose it with a 24-hour preview. 0026 stays as it
-- was applied; this removes what it added.
DROP TABLE IF EXISTS application_previews;
UPDATE deployments SET environment = 'dev' WHERE environment = 'preview';
ALTER TABLE deployments DROP CONSTRAINT IF EXISTS deployments_environment_check;
ALTER TABLE deployments ADD CONSTRAINT deployments_environment_check CHECK (environment IN ('dev', 'production'));

-- Dev copies (DEC-029 part 3, the platform owner, 2026-10-24): each
-- application can have one long-lived development copy, "<name>-dev", its
-- own application with its own database and address. Deploying to it never
-- waits for approval; the application itself (production) still does. The
-- copy's data stays between deployments, so what an employee entered while
-- trying it is still there after each change.
CREATE TABLE dev_copies (
    application_id UUID PRIMARY KEY REFERENCES applications(id) ON DELETE CASCADE,
    parent_id      UUID NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    created_by     UUID NOT NULL REFERENCES users(id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Set once the copy has been removed (reset, or its application ended).
    ended_at       TIMESTAMPTZ,
    -- Test data Claude says it put in the copy's database itself, so it is
    -- never taken for real data when the data is copied to production.
    test_data      BOOLEAN NOT NULL DEFAULT false,
    test_data_note TEXT NOT NULL DEFAULT '',
    CHECK (application_id <> parent_id)
);
CREATE UNIQUE INDEX one_live_dev_copy_per_application ON dev_copies (parent_id) WHERE ended_at IS NULL;

-- A production deployment can take its data from the dev copy, once: the
-- choice is part of what the approver signs, and the copy runs when the
-- deployment goes live.
ALTER TABLE deployments
    ADD COLUMN dev_data_from UUID REFERENCES applications(id),
    ADD COLUMN dev_data_summary TEXT NOT NULL DEFAULT '',
    ADD COLUMN dev_data_copied_at TIMESTAMPTZ,
    ADD COLUMN dev_data_copy_error TEXT NOT NULL DEFAULT '';
