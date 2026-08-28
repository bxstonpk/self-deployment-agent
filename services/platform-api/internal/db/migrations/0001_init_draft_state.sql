-- Draft-state schema.
-- Scope: Application Lifecycle "Draft" state — FR-011, FR-012, FR-013, FR-015
-- (docs/02_Functional_Requirements.md, Modules D "Application Registration" and
-- E "Application Ownership"). Entities per docs/12_Data_Requirements.md:
-- ENT-01 User, ENT-02 Department, ENT-05 Application, ENT-06 ApplicationOwner.
--
-- Deliberately out of scope for this migration (added in later state increments):
-- Role/Permission (ENT-03/04, full RBAC — Module A/B, blocked on DEC-001),
-- ApplicationVersion (ENT-07 — created in the "Build" state, Module I),
-- AuditLog (ENT-16 — Module W, needed by FR-013 but not yet implemented).

CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE departments (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name              TEXT NOT NULL UNIQUE,
    cost_center_code  TEXT,
    parent_department_id UUID REFERENCES departments(id),
    status            TEXT NOT NULL DEFAULT 'active'
                          CHECK (status IN ('active', 'inactive', 'archived')),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    full_name               TEXT NOT NULL,
    email                   TEXT NOT NULL UNIQUE,
    department_id           UUID REFERENCES departments(id),
    job_title               TEXT,
    -- SSO/IdP subject reference. NULL for dev-mode users until DEC-001
    -- (Identity Provider integration, docs/17_Decision_Log.md) is resolved.
    authentication_identity TEXT UNIQUE,
    status                  TEXT NOT NULL DEFAULT 'active'
                                CHECK (status IN ('active', 'suspended', 'offboarded')),
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_login_at           TIMESTAMPTZ,
    last_modified_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE applications (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                  TEXT NOT NULL UNIQUE,
    description           TEXT NOT NULL DEFAULT '',
    owning_department_id  UUID NOT NULL REFERENCES departments(id),
    created_by            UUID NOT NULL REFERENCES users(id),
    -- Application Lifecycle states, fixed order per docs/05_Process_Flows.md.
    lifecycle_status      TEXT NOT NULL DEFAULT 'draft'
                              CHECK (lifecycle_status IN (
                                  'draft', 'validated', 'build', 'deploying', 'running',
                                  'suspended', 'failed', 'rolled_back', 'archived', 'deleted'
                              )),
    -- Raw deployment.yaml text as authored so far (FR-011 alternative flow:
    -- an application may be registered with just name+owner before the full
    -- contract is authored). Superseded by ApplicationVersion snapshots once
    -- the Build state (Module I) is implemented.
    deployment_yaml_draft TEXT,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- FR-012: names must be valid DNS labels (they feed domain generation).
    CONSTRAINT application_name_dns_label
        CHECK (name ~ '^[a-z]([a-z0-9-]{0,61}[a-z0-9])?$')
);

CREATE TABLE application_owners (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id UUID NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    user_id        UUID NOT NULL REFERENCES users(id),
    ownership_role TEXT NOT NULL DEFAULT 'primary'
                       CHECK (ownership_role IN ('primary', 'secondary', 'technical')),
    assigned_by    UUID REFERENCES users(id),
    assigned_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    status         TEXT NOT NULL DEFAULT 'active'
                       CHECK (status IN ('active', 'revoked')),
    UNIQUE (application_id, user_id, ownership_role)
);

-- FR-015 business rule: an application has exactly one active primary owner.
CREATE UNIQUE INDEX one_active_primary_owner_per_application
    ON application_owners (application_id)
    WHERE ownership_role = 'primary' AND status = 'active';

CREATE INDEX idx_applications_owning_department ON applications (owning_department_id);
CREATE INDEX idx_application_owners_application ON application_owners (application_id);
CREATE INDEX idx_application_owners_user ON application_owners (user_id);
