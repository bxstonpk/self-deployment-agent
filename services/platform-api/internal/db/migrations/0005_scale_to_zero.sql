-- Adds Scale-to-Zero (Module L, docs/02_Functional_Requirements.md
-- FR-051..FR-056): idle detection + scale-down, cold-start scale-up on the
-- next request, and scale event logging.
--
-- Schema note: deployment.yaml's `scaling` block is application-wide (per
-- the fixed example in docs/promt.md Section 5), not per-service, so
-- FR-051's "employee declares scaling.min:0 on an ineligible service"
-- normalize-and-flag alternative flow doesn't have a case to apply to in
-- this schema shape — eligibility is purely service-kind-driven (backend
-- runtimes only, per FR-051's business rule that it can never be
-- overridden by configuration) combined with the app-wide scaling.min.
--
-- Idle threshold note: FR-052's business rule says the idle threshold is a
-- "platform-defined default... TBD" — NOT an employee-configurable
-- deployment.yaml field. So it's platform config (PLATFORM_IDLE_TIMEOUT_SECONDS
-- env var, see internal/config/config.go), not a column here.

CREATE TABLE service_runtime_state (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    deployment_id   UUID NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    service_name    TEXT NOT NULL,
    image_ref       TEXT NOT NULL,
    container_port  INT NOT NULL,
    -- FR-051: determined once at activation time from service kind +
    -- scaling.min, never re-derived from later employee input.
    eligible        BOOLEAN NOT NULL,
    -- NULL container_id / host_port = scaled to zero right now.
    container_id    TEXT,
    host_port       INT,
    last_active_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (deployment_id, service_name)
);

CREATE INDEX idx_service_runtime_state_eligible_active
    ON service_runtime_state (eligible, last_active_at)
    WHERE eligible = true AND container_id IS NOT NULL;

-- FR-056: every scale-up (including cold-start) and scale-down
-- (including scale-to-zero) event, queryable per service.
CREATE TABLE scale_events (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    deployment_id   UUID NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    service_name    TEXT NOT NULL,
    direction       TEXT NOT NULL CHECK (direction IN ('scaled_up', 'scaled_to_zero')),
    trigger_reason  TEXT NOT NULL, -- 'cold_start' | 'idle_timeout' | 'initial_activation'
    occurred_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_scale_events_deployment ON scale_events (deployment_id, occurred_at DESC);
