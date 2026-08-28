-- Adds the Supported Stack catalog (Module F) and the fields needed to
-- record a successful Draft -> Validated transition (Module H).
-- Scope: FR-019 (catalog), FR-021/FR-030 (stack compliance), FR-029/FR-034
-- (validation pass + reporting). FR-032 (resource quota) is intentionally
-- NOT enforced yet -- exact quota numbers are TBD (DEC-014,
-- docs/17_Decision_Log.md) and Module M (Resource Manager) doesn't exist
-- yet; the validation report says so explicitly rather than faking a pass.

CREATE TABLE supported_stacks (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    kind        TEXT NOT NULL CHECK (kind IN ('frontend', 'backend', 'database', 'cache')),
    name        TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'deprecated', 'blocked')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (kind, name)
);

-- Supported Stack v1 baseline per docs/06_System_Requirements.md and
-- docs/01_BRD.md: Frontend React/Next.js/Vue, Backend Go/Node.js/Python,
-- Database PostgreSQL, Cache Redis. IT Administrators extend this via the
-- table, not a code change (FR-019 business rule / NFR-029).
INSERT INTO supported_stacks (kind, name) VALUES
    ('frontend', 'react'),
    ('frontend', 'nextjs'),
    ('frontend', 'vue'),
    ('backend',  'go'),
    ('backend',  'nodejs'),
    ('backend',  'python'),
    ('database', 'postgres'),
    ('cache',    'redis');

ALTER TABLE applications
    ADD COLUMN validated_at TIMESTAMPTZ;
