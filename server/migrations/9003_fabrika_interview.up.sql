-- Fabrika week 2 (§9.3, §13 day 8): a new stream from Мультика's interview.
-- One row per Мультика chat turn that carried a charter block: it makes the
-- creation idempotent per turn and carries the outcome (created or why not)
-- into Мультика's next context, so she tells Руслан what really happened.
CREATE TABLE multika_charter (
    task_id          UUID PRIMARY KEY,
    chat_session_id  UUID NOT NULL,
    workspace_id     UUID NOT NULL,
    slug             TEXT NOT NULL DEFAULT '',
    stream_id        UUID,
    error            TEXT NOT NULL DEFAULT '',
    reported_at      TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
