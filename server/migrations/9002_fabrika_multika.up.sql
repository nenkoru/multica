-- Fabrika week 2 (§9.2, §13 day 6): one lifelong chat with Мультика.
-- multika_turn makes claim-time processing of a chat turn (focus switch,
-- one-line answers to asks, routing a remark to a stream) idempotent: a
-- redelivered claim must not answer the next question in the batch.
CREATE TABLE multika_turn (
    task_id          UUID PRIMARY KEY,
    chat_session_id  UUID NOT NULL,
    focus_stream_id  UUID,
    actions          JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
