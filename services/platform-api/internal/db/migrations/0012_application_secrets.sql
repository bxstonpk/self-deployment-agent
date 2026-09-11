-- Module O (Secret Management) — docs/02_Functional_Requirements.md
-- FR-066/067/069/070. See internal/domain/secret.go for the scope this
-- covers and doesn't.
--
-- ciphertext is AES-256-GCM output from internal/secretbox, bound (as
-- associated data) to this row's application_id and name: a ciphertext
-- copied into another application's row does not decrypt there. key_id is
-- a fingerprint of the key it was sealed under, so a changed
-- PLATFORM_SECRET_KEY is reported as exactly that rather than as
-- corruption. There is deliberately no plaintext column.
CREATE TABLE application_secrets (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id UUID NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    name           TEXT NOT NULL CHECK (name ~ '^[A-Z][A-Z0-9_]{0,63}$'),
    managed_by     TEXT NOT NULL CHECK (managed_by IN ('employee', 'platform')),
    ciphertext     BYTEA NOT NULL,
    key_id         TEXT NOT NULL,
    version        INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_by     UUID REFERENCES users(id),
    updated_by     UUID REFERENCES users(id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- One value per name per application: the name IS the environment
    -- variable the value is injected as, so two would be ambiguous.
    UNIQUE (application_id, name),
    -- An employee-managed secret always has a human author; a
    -- platform-managed one never does.
    CHECK ((managed_by = 'employee') = (created_by IS NOT NULL))
);

-- FR-063's at-rest half. Module N stored each generated database password
-- in plaintext in this column, because application_secrets didn't exist
-- yet. New rows no longer write it. Live rows are moved into
-- application_secrets at startup by
-- DatabaseService.MigrateLegacyPlaintextPasswords — that needs the
-- encryption key, which SQL doesn't have — after which the column is empty
-- everywhere and a later migration can drop it.
ALTER TABLE provisioned_databases ALTER COLUMN password DROP NOT NULL;

-- A deprovisioned database no longer exists, so its password protects
-- nothing — but it is still a plaintext credential sitting in this table.
-- No key is needed to delete it, so it goes now.
UPDATE provisioned_databases SET password = NULL WHERE status = 'deprovisioned';
