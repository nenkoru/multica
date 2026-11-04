DELETE FROM ui_block WHERE message_id IS NULL OR status = 'pruned';
ALTER TABLE ui_block DROP CONSTRAINT IF EXISTS ui_block_status_check;
ALTER TABLE ui_block ADD CONSTRAINT ui_block_status_check
    CHECK (status IN ('streaming', 'valid', 'repaired', 'failed'));
ALTER TABLE ui_block
    DROP COLUMN IF EXISTS workspace_id,
    DROP COLUMN IF EXISTS chat_session_id,
    DROP COLUMN IF EXISTS task_id,
    DROP COLUMN IF EXISTS seq,
    DROP COLUMN IF EXISTS kind,
    DROP COLUMN IF EXISTS issues,
    DROP COLUMN IF EXISTS repairs,
    DROP COLUMN IF EXISTS text_version;
ALTER TABLE ui_block ALTER COLUMN message_id SET NOT NULL;
