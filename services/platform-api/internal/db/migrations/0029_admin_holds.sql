-- An administrator's suspension (the platform owner, 2026-10-26): a
-- platform administrator can suspend any running application, with a
-- reason, for a security or technical problem. While the hold is on, only
-- an administrator can resume it; its owners can't resume, archive or
-- delete it. One row per application while it is held; resuming removes
-- the row, and the audit log keeps both.
CREATE TABLE admin_holds (
    application_id UUID PRIMARY KEY REFERENCES applications(id) ON DELETE CASCADE,
    reason         TEXT NOT NULL CHECK (length(btrim(reason)) > 0),
    held_by        UUID NOT NULL REFERENCES users(id),
    held_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
