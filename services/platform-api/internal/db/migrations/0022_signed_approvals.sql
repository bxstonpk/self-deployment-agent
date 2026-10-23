-- Signed approvals (DEC-017, decided 2026-10-23): a deployment needing
-- approval goes ahead only on a decision signed with a platform
-- administrator's private key. See internal/domain/approval_signing.go.
ALTER TABLE deployment_approvals
    -- What the requester attached for the approver to read: the tests run
    -- and their results. Its SHA-256 is part of what the approver signs.
    ADD COLUMN test_report        TEXT NOT NULL DEFAULT '',
    -- The decision's proof: which key signed it, when, and the signature.
    ADD COLUMN signer_fingerprint TEXT,
    ADD COLUMN signed_at          TIMESTAMPTZ,
    ADD COLUMN signature          TEXT;
