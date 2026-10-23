-- Extends the Supported Stack catalog (Module F) with the two entries the
-- company's LAN development standard needs that the v1 baseline lacked:
--
--   * backend runtime `typescript` — Node.js code compiled before it runs
--     (`npm run build`, then `npm start`; see buildengine/templates.go).
--     Plain JavaScript stays `nodejs`.
--   * database `mysql` — a dedicated MySQL 8.4 container per application,
--     provisioned and isolated exactly like `postgres` (Module N), with
--     its password in Module O's secret store.
--
-- Additive only: nothing already supported changes or goes away.
INSERT INTO supported_stacks (kind, name) VALUES
    ('backend',  'typescript'),
    ('database', 'mysql')
ON CONFLICT (kind, name) DO NOTHING;

-- Node 22 rather than the node:20-alpine the v1 runtimes were seeded with:
-- Node 20 left upstream support in April 2026, and this image has to pass
-- the same CRITICAL-severity image scan (FR-041) as every other build.
INSERT INTO base_images (runtime, image_reference) VALUES
    ('typescript', 'node:22-alpine')
ON CONFLICT (runtime) DO NOTHING;

ALTER TABLE provisioned_databases DROP CONSTRAINT IF EXISTS provisioned_databases_engine_check;
ALTER TABLE provisioned_databases ADD CONSTRAINT provisioned_databases_engine_check
    CHECK (engine IN ('postgres', 'mysql'));
