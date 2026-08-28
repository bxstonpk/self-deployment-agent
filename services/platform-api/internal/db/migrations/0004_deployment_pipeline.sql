-- Adds the Deployment pipeline (Module J, docs/02_Functional_Requirements.md
-- FR-039..FR-044): image scan gate, production approval gate, and the
-- deploy/health-check/traffic-activation steps.
--
-- Scope adaptation: FR-039's main flow lists Build as pipeline step 4, but
-- this platform already has a standalone Build state/endpoint (see
-- migration 0003 and internal/service/build_service.go). Deploying a
-- validated, already-built application consumes its LATEST SUCCESSFUL
-- build rather than re-triggering one inline — a reasonable adaptation
-- given the state-by-state build order, not a deviation from the pipeline
-- shape itself (Image Scan -> Registry -> Deployment -> Health Check ->
-- Traffic Activation -> Completed still all happen here, in order).
--
-- "Registry push" has no real registry yet (DEC-005, docs/17_Decision_Log.md
-- is still Open) — images stay in the local Docker daemon that both the
-- Build Engine and this pipeline's Deployment step talk to. This is
-- recorded honestly in code/README, not hidden.

CREATE TABLE deployments (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id      UUID NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    build_id            UUID NOT NULL REFERENCES builds(id),
    environment         TEXT NOT NULL DEFAULT 'dev' CHECK (environment IN ('dev', 'production')),
    requested_by        UUID NOT NULL REFERENCES users(id),
    -- Each value corresponds to a pipeline step, satisfying FR-040's
    -- "current step always visible" without a separate step column.
    status              TEXT NOT NULL DEFAULT 'scanning' CHECK (status IN (
        'scanning', 'pending_approval', 'deploying', 'health_check',
        'running', 'failed', 'rejected', 'superseded'
    )),
    -- Aggregate across all declared services (AND of pass/fail, sum of
    -- counts); per-service detail lives in scan_reports.
    scan_passed         BOOLEAN,
    scan_critical_count INT,
    scan_high_count     INT,
    scan_reports        JSONB, -- service name -> ScanReport
    -- rejection_reason: set when an approver rejects (FR-042).
    -- failure_reason: set on any other pipeline-step failure (scan, deploy,
    -- health check) — kept distinct so callers can tell "a human said no"
    -- from "a gate failed" without parsing free text.
    rejection_reason    TEXT,
    failure_reason      TEXT,
    -- service name -> {container_id, host_port, url}; see
    -- internal/runtimeengine — Docker stands in for the eventual K3s+Knative
    -- Runtime Platform (DEC-004), swappable behind this same shape.
    containers          JSONB,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at        TIMESTAMPTZ
);

CREATE INDEX idx_deployments_application ON deployments (application_id, created_at DESC);

-- FR-040 business rule support: only one deployment attempt is in flight
-- for a given application at a time.
CREATE UNIQUE INDEX one_in_flight_deployment_per_application
    ON deployments (application_id)
    WHERE status IN ('scanning', 'pending_approval', 'deploying', 'health_check');

-- FR-042: Production Approval Gate. A row here is created the moment a
-- production deployment reaches the checkpoint; decided_by/decision/reason
-- are filled in when an owner acts on it.
CREATE TABLE deployment_approvals (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    deployment_id UUID NOT NULL UNIQUE REFERENCES deployments(id) ON DELETE CASCADE,
    requested_by  UUID NOT NULL REFERENCES users(id),
    decided_by    UUID REFERENCES users(id),
    decision      TEXT NOT NULL DEFAULT 'pending' CHECK (decision IN ('pending', 'approved', 'rejected')),
    reason        TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    decided_at    TIMESTAMPTZ
);
