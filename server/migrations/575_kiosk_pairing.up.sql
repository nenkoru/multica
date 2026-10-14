-- One-time pairing codes for TV screens and phones. A redeemed code mints a
-- device token (kiosk_token.parent_id = the token the code was issued for),
-- so revoking the parent token signs out every paired device.
ALTER TABLE kiosk_token ADD COLUMN IF NOT EXISTS parent_id UUID;

CREATE TABLE kiosk_pairing (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    token_id UUID NOT NULL,
    code_hash TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ,
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
