-- Sender attribution for chat messages written by an agent run (RUSL-1557).
--
-- author_type / author_id are NULL for every ordinary row: a member's own
-- message and the session agent's task-owned reply are already identified by
-- role + session. They are stamped only when a different actor wrote the row —
-- today an agent run acting through its task token:
--   * role='assistant', task_id NULL  — a proactive message the agent posted
--     itself (POST /api/chat/sessions/{id}/agent-messages); no turn is queued.
--   * role='user'                     — a `multica chat send` turn the agent
--     queued through the runtime owner's session, so the feed can show that the
--     agent, not the member, wrote it.
--
-- Additive and app-validated, like message_kind: no CHECK / FK / cascade.
ALTER TABLE chat_message
    ADD COLUMN IF NOT EXISTS author_type TEXT,
    ADD COLUMN IF NOT EXISTS author_id UUID;
