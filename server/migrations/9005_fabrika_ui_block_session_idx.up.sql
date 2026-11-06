CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_ui_block_session ON ui_block (chat_session_id, created_at);
