ALTER TABLE chat_message
    DROP COLUMN IF EXISTS author_type,
    DROP COLUMN IF EXISTS author_id;
