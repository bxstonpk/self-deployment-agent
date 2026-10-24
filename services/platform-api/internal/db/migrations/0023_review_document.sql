-- What the approver reads before signing (the platform owner, 2026-10-24:
-- "review every file before it goes up"): the requester's REVIEW.md —
-- workflow, structure, paths, what changed. Bound into the signed text by
-- its SHA-256, like test_report. The build's source itself is kept as a file
-- (SOURCE_DIR), not in the database.
ALTER TABLE deployment_approvals ADD COLUMN review_document TEXT NOT NULL DEFAULT '';
