CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_kiosk_pairing_code ON kiosk_pairing(code_hash);
