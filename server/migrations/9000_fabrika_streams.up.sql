-- Fabrika (harness-design.md §12): streams that never stand still.
-- Fork-owned schema. Numbered 9000+ so upstream migrations never collide;
-- upstream tables are never altered. stream_id / step_id travel to the daemon
-- inside agent_task_queue.context (jsonb).
--
-- Repository rules: no foreign keys or cascades (relationships are kept in
-- application code), and secondary indexes would each need their own
-- CONCURRENTLY migration plus a cleanup hook in cmd/migrate. The tables stay
-- tiny (≤5 active streams, hundreds of steps a week), so only primary keys and
-- the unique keys that carry idempotency are declared here.

-- Stream: a long-lived direction with an explicit state and reason (§4.2, §5.1).
CREATE TABLE stream (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id       UUID NOT NULL,
    slug               TEXT NOT NULL,
    title              TEXT NOT NULL,
    -- Charter: goal, metric, done, mandate, people, budget share, repos,
    -- executor chains, skills allowlist (§4.2, §11).
    charter            JSONB NOT NULL DEFAULT '{}'::jsonb,
    summary            TEXT NOT NULL DEFAULT '',
    state              TEXT NOT NULL DEFAULT 'running'
        CHECK (state IN ('running', 'waiting', 'asking', 'budget', 'paused', 'broken')),
    reason             TEXT NOT NULL DEFAULT '',
    until              TIMESTAMPTZ,
    weight             INTEGER NOT NULL DEFAULT 1 CHECK (weight > 0),
    min_share          NUMERIC(4,3) NOT NULL DEFAULT 0 CHECK (min_share >= 0 AND min_share <= 1),
    max_parallel       INTEGER NOT NULL DEFAULT 1 CHECK (max_parallel BETWEEN 1 AND 3),
    manager_agent_id   UUID,
    -- Hidden carrier project streams/<slug> (§4.3).
    carrier_project_id UUID,
    next_seq           INTEGER NOT NULL DEFAULT 1,
    -- Consecutive failed steps without progress (§5.1 → broken).
    fail_streak        INTEGER NOT NULL DEFAULT 0,
    last_milestone_at  TIMESTAMPTZ,
    active             BOOLEAN NOT NULL DEFAULT true,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, slug)
);

-- Step: one executor turn with explicit input and output (§4.2, §5.2).
CREATE TABLE stream_step (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    stream_id         UUID NOT NULL,
    seq               INTEGER NOT NULL,
    kind              TEXT NOT NULL CHECK (kind IN ('manage', 'work', 'verify', 'summarize')),
    status            TEXT NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued', 'running', 'verifying', 'done', 'failed', 'cancelled')),
    brief             TEXT NOT NULL,
    moves             TEXT NOT NULL DEFAULT '',
    cost_class        TEXT NOT NULL DEFAULT 'S' CHECK (cost_class IN ('S', 'M', 'L')),
    executor          TEXT NOT NULL DEFAULT '',
    executor_agent_id UUID,
    -- Rerouting on quota does not consume an attempt (§6.4).
    attempt           INTEGER NOT NULL DEFAULT 0,
    max_attempts      INTEGER NOT NULL DEFAULT 3,
    rejections        INTEGER NOT NULL DEFAULT 0,
    hang_count        INTEGER NOT NULL DEFAULT 0,
    lease_until       TIMESTAMPTZ,
    recovery_note     TEXT NOT NULL DEFAULT '',
    output            JSONB,
    last_error        TEXT NOT NULL DEFAULT '',
    carrier_issue_id  UUID,
    task_id           UUID,
    origin            TEXT NOT NULL DEFAULT 'supervisor',
    -- Ruslan's message in focus may run a manage turn above max_parallel (§6.1).
    reserved_slot     BOOLEAN NOT NULL DEFAULT false,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at        TIMESTAMPTZ,
    finished_at       TIMESTAMPTZ,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Idempotent enqueue key (§6.1).
    UNIQUE (stream_id, seq)
);

-- Wait: an external condition as an event source (§4.2).
CREATE TABLE stream_wait (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    stream_id    UUID NOT NULL,
    step_id      UUID,
    kind         TEXT NOT NULL CHECK (kind IN ('ci', 'git-tag', 'http', 'process', 'time', 'gpu-window')),
    "check"      TEXT NOT NULL DEFAULT '',
    deadline     TIMESTAMPTZ NOT NULL,
    on_deadline  TEXT NOT NULL DEFAULT 'replan' CHECK (on_deadline IN ('replan', 'retry', 'ask')),
    status       TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'fired', 'expired', 'cancelled')),
    -- Cursor so a restart never replays the same observation (§6.1).
    cursor       JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at  TIMESTAMPTZ
);

-- Ask: an asynchronous question to Ruslan, delivered in batches (§7.2).
CREATE TABLE ask (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    stream_id       UUID NOT NULL,
    origin_step_id  UUID,
    kind            TEXT NOT NULL CHECK (kind IN ('choice', 'batch')),
    text            TEXT NOT NULL CHECK (char_length(text) <= 200),
    options         JSONB NOT NULL DEFAULT '[]'::jsonb,
    items           JSONB NOT NULL DEFAULT '[]'::jsonb,
    -- Index into options; never set for zone=red.
    default_option  INTEGER,
    zone            TEXT NOT NULL DEFAULT 'normal' CHECK (zone IN ('normal', 'red')),
    deadline        TIMESTAMPTZ NOT NULL,
    value           DOUBLE PRECISION NOT NULL DEFAULT 1,
    cost_of_delay   DOUBLE PRECISION NOT NULL DEFAULT 1,
    answer          JSONB,
    status          TEXT NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued', 'shown', 'answered', 'defaulted', 'dropped')),
    shown_at        TIMESTAMPTZ,
    reminded_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at     TIMESTAMPTZ,
    CONSTRAINT ask_red_has_no_default CHECK (zone <> 'red' OR default_option IS NULL)
);

-- Milestone: a verified result one can touch (§4.2, §8).
CREATE TABLE milestone (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    stream_id     UUID NOT NULL,
    step_id       UUID,
    text          TEXT NOT NULL,
    evidence      JSONB NOT NULL DEFAULT '[]'::jsonb,
    metric_delta  TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Journal: append-only stream events for audit and recovery (§4.2).
CREATE TABLE stream_journal (
    id          BIGSERIAL PRIMARY KEY,
    stream_id   UUID NOT NULL,
    step_id     UUID,
    event       TEXT NOT NULL,
    detail      JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Executor status: "exhausted until T" per provider or runtime (§4.2, §6.4).
CREATE TABLE executor_status (
    executor         TEXT PRIMARY KEY,
    exhausted_until  TIMESTAMPTZ,
    reason           TEXT NOT NULL DEFAULT '',
    -- 0,1,2… → probe after 15 min, 30 min, 1 h when the provider gives no T.
    probe_level      INTEGER NOT NULL DEFAULT 0,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Chat focus: which stream Ruslan's chat is focused on (§4.2, week 2).
CREATE TABLE chat_focus (
    chat_session_id  UUID PRIMARY KEY,
    focus_stream_id  UUID,
    focus_until      TIMESTAMPTZ,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- UI block: a screen inside a chat message (§10, week 3).
CREATE TABLE ui_block (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    message_id     UUID NOT NULL,
    openui_source  TEXT NOT NULL DEFAULT '',
    a2ui           JSONB,
    status         TEXT NOT NULL DEFAULT 'streaming'
        CHECK (status IN ('streaming', 'valid', 'repaired', 'failed')),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- UI action: a server-held action behind a short button token (§10.4, §10.5).
CREATE TABLE ui_action (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ui_block_id      UUID,
    ask_id           UUID,
    token            TEXT NOT NULL UNIQUE,
    action           JSONB NOT NULL,
    zone             TEXT NOT NULL DEFAULT 'normal' CHECK (zone IN ('normal', 'red')),
    idempotency_key  TEXT UNIQUE,
    status           TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'confirming', 'done', 'failed', 'expired')),
    result           JSONB,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    done_at          TIMESTAMPTZ
);

-- Supervisor liveness for /api/streams/health and the external watchdog (§6.6).
CREATE TABLE stream_supervisor_state (
    id            BOOLEAN PRIMARY KEY DEFAULT true CHECK (id),
    last_tick_at  TIMESTAMPTZ NOT NULL,
    last_error    TEXT NOT NULL DEFAULT '',
    ticks         BIGINT NOT NULL DEFAULT 0
);
