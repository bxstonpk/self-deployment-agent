-- Module X (docs/02_Functional_Requirements.md FR-107, FR-108): an in-app,
-- per-recipient notification list for deployment status and approval
-- request events. See internal/domain/notification.go's package comment
-- for what's deliberately out of scope (FR-109, outbound delivery
-- channels).
CREATE TABLE notifications (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    recipient_user_id UUID NOT NULL REFERENCES users(id),
    category          TEXT NOT NULL CHECK (category IN ('deployment_status', 'approval_request')),
    title             TEXT NOT NULL,
    detail            TEXT NOT NULL DEFAULT '',
    resource_type     TEXT NOT NULL,
    resource_id       TEXT NOT NULL DEFAULT '',
    read_at           TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Every list query filters by recipient and orders by recency; unread-only
-- queries additionally filter on read_at IS NULL — covered by the same
-- composite index rather than a second one.
CREATE INDEX idx_notifications_recipient ON notifications (recipient_user_id, read_at, created_at DESC);
