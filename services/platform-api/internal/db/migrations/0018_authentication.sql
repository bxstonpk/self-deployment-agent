-- Real authentication (DEC-001/DEC-003 as revised 2026-10-23,
-- docs/17_Decision_Log.md): passwords for employees, sessions for the
-- Admin Portal, and API keys that each Claude Code machine fetches through
-- a device authorization before it may deploy. See internal/domain/auth.go.

-- bcrypt. NULL for an account that has never set a password — every user
-- the old self-declared header created. Such an account can't sign in
-- until its owner registers with the enrollment code, which claims it
-- (keeping its applications) by setting a password.
ALTER TABLE users
    ADD COLUMN password_hash   TEXT,
    ADD COLUMN password_set_at TIMESTAMPTZ;

-- Sign-in matches email case-insensitively. Not UNIQUE: the old header
-- identity matched exactly, so two rows differing only in case may
-- already exist; the earliest one is the account (see AuthRepo).
CREATE INDEX users_email_lower ON users (lower(email));

CREATE TABLE credentials (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind         TEXT NOT NULL CHECK (kind IN ('session', 'api_key')),
    name         TEXT NOT NULL DEFAULT '',
    token_prefix TEXT NOT NULL,
    -- hex SHA-256 of the token; the token itself is never stored.
    token_hash   TEXT NOT NULL UNIQUE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ NOT NULL,
    last_used_at TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ
);

CREATE INDEX idx_credentials_user ON credentials (user_id, kind, created_at DESC);

CREATE TABLE device_authorizations (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_code_hash TEXT NOT NULL UNIQUE,
    user_code        TEXT NOT NULL,
    client_name      TEXT NOT NULL,
    status           TEXT NOT NULL DEFAULT 'pending'
                         CHECK (status IN ('pending', 'approved', 'denied', 'consumed')),
    user_id          UUID REFERENCES users(id),
    credential_id    UUID REFERENCES credentials(id),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at       TIMESTAMPTZ NOT NULL,
    decided_at       TIMESTAMPTZ
);

-- A user code identifies one live request; it may be reused only once
-- that request is finished with.
CREATE UNIQUE INDEX one_live_user_code ON device_authorizations (user_code)
    WHERE status IN ('pending', 'approved');
