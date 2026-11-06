-- Fabrika week 3 (§10, §13 days 11-15): screens in chat.
-- A ui_block is one compiled screen of a chat reply. Its id is derived from
-- (task_id, seq) in code, so a redelivered chat:done stores it once. The
-- message id is known only after the reply is persisted, hence nullable.
ALTER TABLE ui_block ALTER COLUMN message_id DROP NOT NULL;
ALTER TABLE ui_block
    ADD COLUMN workspace_id    UUID,
    ADD COLUMN chat_session_id UUID,
    ADD COLUMN task_id         UUID,
    ADD COLUMN seq             INT NOT NULL DEFAULT 0,
    ADD COLUMN kind            TEXT NOT NULL DEFAULT 'model' CHECK (kind IN ('model', 'code')),
    ADD COLUMN issues          JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN repairs         INT NOT NULL DEFAULT 0,
    ADD COLUMN text_version    TEXT NOT NULL DEFAULT '';
ALTER TABLE ui_block DROP CONSTRAINT IF EXISTS ui_block_status_check;
ALTER TABLE ui_block ADD CONSTRAINT ui_block_status_check
    CHECK (status IN ('streaming', 'valid', 'repaired', 'pruned', 'failed'));
