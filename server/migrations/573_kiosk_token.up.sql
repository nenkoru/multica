-- Read-only kiosk tokens for the TV dashboard. A token is bound to one
-- workspace, can read only the TV aggregate and is revoked by setting
-- revoked_at. owner_label names the issue label that marks blocked work
-- waiting on the workspace owner.
CREATE TABLE kiosk_token (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    name TEXT NOT NULL,
    token_hash TEXT NOT NULL,
    token_prefix TEXT NOT NULL,
    owner_label TEXT NOT NULL DEFAULT '',
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ
);
