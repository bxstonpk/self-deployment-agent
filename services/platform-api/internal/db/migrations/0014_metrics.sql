-- Module T (Monitoring) — docs/02_Functional_Requirements.md FR-090/091.
-- Two tables because the platform has two honest sources: what the
-- container runtime reports about a container, and what the platform's own
-- proxy sees of the traffic reaching it. Neither asks anything of the
-- application. Nothing is purged yet: NFR-032's retention durations are
-- TBD, so rows accumulate — see internal/domain/metrics.go.

-- One row per container per sampling interval. A sample that couldn't be
-- read is simply absent: a gap, never an interpolated zero.
CREATE TABLE resource_samples (
    id                 BIGSERIAL PRIMARY KEY,
    application_id     UUID NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    deployment_id      UUID REFERENCES deployments(id) ON DELETE SET NULL,
    service            TEXT NOT NULL,
    container_id       TEXT NOT NULL,
    sampled_at         TIMESTAMPTZ NOT NULL,
    cpu_percent        DOUBLE PRECISION NOT NULL,
    memory_bytes       BIGINT NOT NULL,
    memory_limit_bytes BIGINT NOT NULL
);

CREATE INDEX resource_samples_by_application ON resource_samples (application_id, sampled_at DESC);

-- One row per minute per service per environment, accumulated from the
-- proxy. Counts are added into the row rather than replacing it, so a
-- flush that lands in a minute already recorded (or a second platform-api
-- process) adds to it instead of losing what was there.
CREATE TABLE request_buckets (
    application_id   UUID NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    service          TEXT NOT NULL,
    environment      TEXT NOT NULL,
    minute           TIMESTAMPTZ NOT NULL,
    requests         BIGINT NOT NULL,
    errors           BIGINT NOT NULL,
    latency_ms_total DOUBLE PRECISION NOT NULL,
    latency_ms_max   DOUBLE PRECISION NOT NULL,
    PRIMARY KEY (application_id, service, environment, minute)
);

CREATE INDEX request_buckets_by_application ON request_buckets (application_id, minute DESC);
