-- Fabrika queries (own sqlc file: sqlc.streams.yaml → internal/streams/sdb).

-- name: ListActiveStreams :many
SELECT * FROM stream WHERE active ORDER BY created_at;

-- name: ListStreams :many
SELECT * FROM stream WHERE workspace_id = $1 ORDER BY created_at;

-- name: GetStream :one
SELECT * FROM stream WHERE id = $1;

-- name: GetStreamBySlug :one
SELECT * FROM stream WHERE workspace_id = $1 AND slug = $2;

-- name: LockStream :one
SELECT * FROM stream WHERE id = $1 FOR UPDATE;

-- name: UpsertStream :one
INSERT INTO stream (workspace_id, slug, title, charter, summary, weight, min_share, max_parallel, manager_agent_id, carrier_project_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (workspace_id, slug) DO UPDATE SET
    title = EXCLUDED.title,
    charter = EXCLUDED.charter,
    weight = EXCLUDED.weight,
    min_share = EXCLUDED.min_share,
    max_parallel = EXCLUDED.max_parallel,
    manager_agent_id = EXCLUDED.manager_agent_id,
    carrier_project_id = COALESCE(EXCLUDED.carrier_project_id, stream.carrier_project_id),
    updated_at = now()
RETURNING *;

-- name: SetStreamState :exec
UPDATE stream SET state = $2, reason = $3, until = $4, updated_at = now() WHERE id = $1;

-- name: SetStreamActive :exec
UPDATE stream SET active = $2, updated_at = now() WHERE id = $1;

-- name: SetStreamSummary :exec
UPDATE stream SET summary = $2, updated_at = now() WHERE id = $1;

-- name: SetStreamFailStreak :exec
UPDATE stream SET fail_streak = $2, updated_at = now() WHERE id = $1;

-- name: SetStreamMaxParallel :exec
UPDATE stream SET max_parallel = $2, updated_at = now() WHERE id = $1;

-- name: TouchStreamMilestone :exec
UPDATE stream SET last_milestone_at = now(), fail_streak = 0, updated_at = now() WHERE id = $1;

-- name: AllocStepSeq :one
UPDATE stream SET next_seq = next_seq + 1 WHERE id = $1 RETURNING next_seq - 1 AS seq;

-- name: InsertStep :one
INSERT INTO stream_step (stream_id, seq, kind, brief, moves, cost_class, origin, reserved_slot, max_attempts)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (stream_id, seq) DO NOTHING
RETURNING *;

-- name: GetStep :one
SELECT * FROM stream_step WHERE id = $1;

-- name: GetStepBySeq :one
SELECT * FROM stream_step WHERE stream_id = $1 AND seq = $2;

-- name: ListOpenSteps :many
SELECT * FROM stream_step
WHERE stream_id = $1 AND status IN ('queued', 'running', 'verifying')
ORDER BY seq;

-- name: ListRecentSteps :many
SELECT * FROM stream_step WHERE stream_id = $1 ORDER BY seq DESC LIMIT $2;

-- name: ListStepsSince :many
SELECT * FROM stream_step WHERE stream_id = $1 AND seq > $2 ORDER BY seq;

-- name: LastStepOfKind :one
SELECT * FROM stream_step WHERE stream_id = $1 AND kind = $2 ORDER BY seq DESC LIMIT 1;

-- name: CountStepsSinceSummarize :one
SELECT count(*) FROM stream_step s
WHERE s.stream_id = $1
  AND s.status = 'done'
  AND s.seq > COALESCE((SELECT max(seq) FROM stream_step x WHERE x.stream_id = $1 AND x.kind = 'summarize' AND x.status = 'done'), 0);

-- name: MarkStepDispatched :exec
UPDATE stream_step SET
    status = 'running',
    executor = $2,
    executor_agent_id = $3,
    carrier_issue_id = $4,
    task_id = $5,
    lease_until = $6,
    started_at = COALESCE(started_at, now()),
    updated_at = now()
WHERE id = $1;

-- name: ExtendStepLease :exec
UPDATE stream_step SET lease_until = $2, updated_at = now() WHERE id = $1 AND (lease_until IS NULL OR lease_until < $2);

-- name: SetStepTask :exec
UPDATE stream_step SET task_id = $2, updated_at = now() WHERE id = $1;

-- name: RequeueStep :exec
-- Back to queued for a retry/reroute; the carrier issue is closed separately.
UPDATE stream_step SET
    status = 'queued',
    attempt = $2,
    hang_count = $3,
    rejections = $4,
    recovery_note = $5,
    last_error = $6,
    carrier_issue_id = NULL,
    task_id = NULL,
    lease_until = NULL,
    updated_at = now()
WHERE id = $1;

-- name: FinishStep :exec
UPDATE stream_step SET
    status = $2,
    output = $3,
    last_error = $4,
    lease_until = NULL,
    finished_at = now(),
    updated_at = now()
WHERE id = $1;

-- name: SetStepVerifying :exec
UPDATE stream_step SET status = 'verifying', output = $2, lease_until = NULL, updated_at = now() WHERE id = $1;

-- name: CountRecentBrief :one
SELECT count(*) FROM stream_step s
WHERE s.brief = sqlc.arg(brief) AND s.id IN (
    SELECT x.id FROM stream_step x WHERE x.stream_id = sqlc.arg(stream_id) AND x.kind = 'work' ORDER BY x.seq DESC LIMIT 10
);

-- name: InsertWait :one
INSERT INTO stream_wait (stream_id, step_id, kind, "check", deadline, on_deadline)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ListOpenWaits :many
SELECT * FROM stream_wait WHERE stream_id = $1 AND status = 'open' ORDER BY deadline;

-- name: ResolveWait :exec
UPDATE stream_wait SET status = $2, resolved_at = now() WHERE id = $1 AND status = 'open';

-- name: GetWait :one
SELECT * FROM stream_wait WHERE id = $1;

-- name: InsertAsk :one
INSERT INTO ask (stream_id, origin_step_id, kind, text, options, items, default_option, zone, deadline, value, cost_of_delay)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: ListOpenAsks :many
SELECT * FROM ask WHERE stream_id = $1 AND status IN ('queued', 'shown') ORDER BY created_at;

-- name: ListAllOpenAsks :many
SELECT a.* FROM ask a JOIN stream s ON s.id = a.stream_id
WHERE s.workspace_id = $1 AND a.status IN ('queued', 'shown')
ORDER BY a.created_at;

-- name: ListShownAsks :many
SELECT a.* FROM ask a JOIN stream s ON s.id = a.stream_id
WHERE s.workspace_id = $1 AND a.status = 'shown'
ORDER BY a.shown_at, a.id;

-- name: CountAsksShownSince :one
SELECT count(*) FROM ask a JOIN stream s ON s.id = a.stream_id
WHERE s.workspace_id = $1 AND a.shown_at >= $2;

-- name: MarkAskShown :exec
UPDATE ask SET status = 'shown', shown_at = now() WHERE id = $1 AND status = 'queued';

-- name: MarkAskReminded :exec
UPDATE ask SET reminded_at = now() WHERE id = $1;

-- name: ResolveAsk :execrows
UPDATE ask SET status = $2, answer = $3, resolved_at = now() WHERE id = $1 AND status IN ('queued', 'shown');

-- name: ListAsksResolvedSince :many
SELECT * FROM ask WHERE stream_id = $1 AND status IN ('answered', 'defaulted') AND resolved_at > $2 ORDER BY resolved_at;

-- name: GetAsk :one
SELECT * FROM ask WHERE id = $1;

-- name: InsertMilestone :one
INSERT INTO milestone (stream_id, step_id, text, evidence, metric_delta)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: LatestAcceptedVerify :one
-- The newest verify accepted after the last milestone: a milestone needs one
-- (§8 p.3), and one acceptance backs one milestone.
SELECT * FROM stream_step
WHERE stream_id = @stream_id AND kind = 'verify' AND status = 'done'
  AND output->>'verdict' = 'accept'
  AND finished_at > @since::timestamptz
ORDER BY finished_at DESC
LIMIT 1;

-- name: ListRecentMilestones :many
SELECT * FROM milestone WHERE stream_id = $1 ORDER BY created_at DESC LIMIT $2;

-- name: ListMilestonesSince :many
SELECT * FROM milestone WHERE stream_id = $1 AND created_at >= $2 ORDER BY created_at;

-- name: AppendJournal :exec
INSERT INTO stream_journal (stream_id, step_id, event, detail) VALUES ($1, $2, $3, $4);

-- name: ListJournal :many
SELECT * FROM stream_journal WHERE stream_id = $1 ORDER BY id DESC LIMIT $2;

-- name: ListJournalSince :many
SELECT * FROM stream_journal WHERE stream_id = $1 AND created_at >= $2 ORDER BY id;

-- name: ListExecutorStatus :many
SELECT * FROM executor_status;

-- name: UpsertExecutorStatus :exec
INSERT INTO executor_status (executor, exhausted_until, reason, probe_level, updated_at)
VALUES ($1, $2, $3, $4, now())
ON CONFLICT (executor) DO UPDATE SET
    exhausted_until = EXCLUDED.exhausted_until,
    reason = EXCLUDED.reason,
    probe_level = EXCLUDED.probe_level,
    updated_at = now();

-- name: ClearExecutorStatus :exec
UPDATE executor_status SET exhausted_until = NULL, probe_level = 0, reason = '', updated_at = now()
WHERE executor = $1 AND (exhausted_until IS NOT NULL OR probe_level <> 0);

-- name: RecordSupervisorTick :exec
INSERT INTO stream_supervisor_state (id, last_tick_at, last_error, ticks)
VALUES (true, now(), $1, 1)
ON CONFLICT (id) DO UPDATE SET last_tick_at = now(), last_error = EXCLUDED.last_error, ticks = stream_supervisor_state.ticks + 1;

-- name: GetSupervisorState :one
SELECT * FROM stream_supervisor_state WHERE id;

-- name: LastStepHeartbeat :one
-- Canary for the watchdog: newest step progress across all streams.
SELECT COALESCE(max(updated_at), 'epoch'::timestamptz)::timestamptz AS at FROM stream_step WHERE status IN ('running', 'done');

-- Upstream task reads. Read-only; agent_task_queue schema is not changed.

-- name: LatestTaskForIssue :one
SELECT id, status, failure_reason, error, result, runtime_id, created_at, started_at, completed_at, agent_id
FROM agent_task_queue
WHERE issue_id = $1
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: TaskLastProgress :one
-- Any progress (message, tool call, token usage) extends the step lease (§6.3).
SELECT GREATEST(
    COALESCE((SELECT max(m.created_at) FROM task_message m WHERE m.task_id = sqlc.arg(task_id)::uuid), 'epoch'::timestamptz),
    COALESCE((SELECT max(u.updated_at) FROM task_usage u WHERE u.task_id = sqlc.arg(task_id)::uuid), 'epoch'::timestamptz)
)::timestamptz AS at;

-- name: TagTaskContext :exec
-- stream_id/step_id ride in the jsonb context; retries copy it forward.
UPDATE agent_task_queue
SET context = COALESCE(context, '{}'::jsonb) || sqlc.arg(tags)::jsonb
WHERE id = sqlc.arg(id);

-- name: RuntimeLastSeen :one
SELECT last_seen_at FROM agent_runtime WHERE id = $1;

-- name: LastReasonEnd :one
-- When the stream's last live reason ended: a step finished, a wait or ask
-- resolved, a state change. Orphans found long after it are silent stops.
SELECT GREATEST(
    (SELECT s.created_at FROM stream s WHERE s.id = sqlc.arg(stream_id)::uuid),
    COALESCE((SELECT max(x.finished_at) FROM stream_step x WHERE x.stream_id = sqlc.arg(stream_id)::uuid), 'epoch'::timestamptz),
    COALESCE((SELECT max(j.created_at) FROM stream_journal j WHERE j.stream_id = sqlc.arg(stream_id)::uuid AND j.event NOT IN ('orphan', 'reconcile_error', 'ask_shown')), 'epoch'::timestamptz)
)::timestamptz AS at;

-- Мультика: one lifelong chat (§9.2, week 2).

-- name: GetChatFocus :one
SELECT * FROM chat_focus WHERE chat_session_id = $1;

-- name: SetChatFocus :exec
INSERT INTO chat_focus (chat_session_id, focus_stream_id, focus_until, updated_at)
VALUES ($1, $2, $3, now())
ON CONFLICT (chat_session_id) DO UPDATE
SET focus_stream_id = EXCLUDED.focus_stream_id, focus_until = EXCLUDED.focus_until, updated_at = now();

-- name: ClearChatFocus :exec
UPDATE chat_focus SET focus_stream_id = NULL, focus_until = NULL, updated_at = now()
WHERE chat_session_id = $1;

-- name: GetChatSessionOwner :one
SELECT agent_id, workspace_id FROM chat_session WHERE id = $1;

-- name: MultikaChannelSession :one
-- The chat the Telegram integration currently routes Мультика's direct chat to.
-- Only the bot installer's own chat counts (Руслан installed Мультика's bot).
SELECT b.chat_session_id FROM channel_chat_session_binding b
JOIN channel_installation i ON i.id = b.installation_id
JOIN chat_session cs ON cs.id = b.chat_session_id AND cs.creator_id = i.installer_user_id
WHERE i.agent_id = $1 AND i.workspace_id = $2 AND i.status = 'active'
  AND b.retired_at IS NULL AND b.chat_type = 'p2p'
ORDER BY b.created_at DESC LIMIT 1;

-- name: LatestAgentChatSession :one
SELECT id FROM chat_session
WHERE agent_id = $1 AND workspace_id = $2 AND status = 'active'
ORDER BY updated_at DESC LIMIT 1;

-- name: InsertMultikaTurn :one
INSERT INTO multika_turn (task_id, chat_session_id, focus_stream_id, actions)
VALUES ($1, $2, $3, $4)
ON CONFLICT (task_id) DO NOTHING
RETURNING *;

-- name: GetMultikaTurn :one
SELECT * FROM multika_turn WHERE task_id = $1;

-- name: AppendQueuedStepBrief :execrows
-- Coalesces chat remarks into a manager turn that has not started yet.
UPDATE stream_step SET brief = brief || E'\n' || @line::text
WHERE id = @id AND status = 'queued' AND task_id IS NULL;

-- name: MultikaTelegramTarget :one
-- Where Мультика's Telegram direct chat lives, for messages with buttons.
SELECT b.installation_id, b.channel_chat_id FROM channel_chat_session_binding b
JOIN channel_installation i ON i.id = b.installation_id
JOIN chat_session cs ON cs.id = b.chat_session_id AND cs.creator_id = i.installer_user_id
WHERE i.agent_id = $1 AND i.workspace_id = $2 AND i.status = 'active'
  AND i.channel_type = 'telegram' AND b.retired_at IS NULL AND b.chat_type = 'p2p'
ORDER BY b.created_at DESC LIMIT 1;

-- name: MultikaTelegramChatWorkspace :one
-- The workspace of a press, only when it came from Мультика's live direct chat.
SELECT i.workspace_id FROM channel_chat_session_binding b
JOIN channel_installation i ON i.id = b.installation_id
JOIN chat_session cs ON cs.id = b.chat_session_id AND cs.creator_id = i.installer_user_id
WHERE b.installation_id = @installation_id AND b.channel_chat_id = @channel_chat_id
  AND i.agent_id = @agent_id AND i.status = 'active'
  AND b.retired_at IS NULL AND b.chat_type = 'p2p';

-- name: InsertUIAction :one
INSERT INTO ui_action (ask_id, token, action, zone, idempotency_key)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (idempotency_key) DO NOTHING
RETURNING *;

-- name: CountAskActions :one
SELECT count(*) FROM ui_action WHERE ask_id = $1 AND status <> 'failed';

-- name: GetUIActionByToken :one
SELECT * FROM ui_action WHERE token = $1;

-- name: FinishUIAction :execrows
-- Claims a pending action; 0 rows means another press got there first.
UPDATE ui_action SET status = @status, result = @result, done_at = now()
WHERE id = @id AND status = 'pending';

-- name: ReleaseUIAction :exec
-- Gives a claimed action back when applying it failed.
UPDATE ui_action SET status = 'pending', result = NULL, done_at = NULL
WHERE id = $1 AND status = 'done';

-- name: FailUIAction :exec
UPDATE ui_action SET status = 'failed', done_at = now() WHERE id = $1 AND status = 'pending';

-- name: ExpireAskActions :exec
UPDATE ui_action SET status = 'expired', done_at = now()
WHERE ask_id = $1 AND status = 'pending';

-- name: CreateStream :one
-- A new stream only: an existing slug returns no row instead of overwriting
-- its charter.
INSERT INTO stream (workspace_id, slug, title, charter, weight, min_share, max_parallel, manager_agent_id, carrier_project_id, active)
VALUES (@workspace_id, @slug, @title, @charter, @weight, @min_share, @max_parallel, @manager_agent_id, @carrier_project_id, @active)
ON CONFLICT (workspace_id, slug) DO NOTHING
RETURNING *;

-- name: ClaimMultikaCharter :one
INSERT INTO multika_charter (task_id, chat_session_id, workspace_id)
VALUES ($1, $2, $3)
ON CONFLICT (task_id) DO NOTHING
RETURNING *;

-- name: FinishMultikaCharter :exec
UPDATE multika_charter SET slug = $2, stream_id = $3, error = $4 WHERE task_id = $1;

-- name: TakeMultikaCharters :many
-- Outcomes Мультика has not told Руслан yet, oldest first; taking marks them.
UPDATE multika_charter SET reported_at = now()
WHERE workspace_id = $1 AND reported_at IS NULL
RETURNING *;

-- name: RunningStepsByExecutor :many
-- Slot accounting across active streams (§6.7): running steps per executor
-- and which streams have queued ones.
SELECT st.stream_id, st.status, st.executor, st.kind, st.cost_class, count(*)::int AS n
FROM stream_step st
JOIN stream s ON s.id = st.stream_id AND s.active
WHERE st.status IN ('running', 'queued')
GROUP BY st.stream_id, st.status, st.executor, st.kind, st.cost_class;

-- name: StreamCostSince :many
-- Usage of a stream's tasks since a moment, per model: provider-reported
-- cost (1e-10 USD) and the tokens of rows without one, for the rate table
-- (metrics.UsageCostUSD).
SELECT u.model,
       COALESCE(SUM(u.cost_usd_ticks), 0)::bigint AS ticks,
       COALESCE(SUM(u.input_tokens) FILTER (WHERE u.cost_usd_ticks IS NULL), 0)::bigint AS input_tokens,
       COALESCE(SUM(u.output_tokens) FILTER (WHERE u.cost_usd_ticks IS NULL), 0)::bigint AS output_tokens,
       COALESCE(SUM(u.cache_read_tokens) FILTER (WHERE u.cost_usd_ticks IS NULL), 0)::bigint AS cache_read_tokens,
       COALESCE(SUM(u.cache_write_tokens) FILTER (WHERE u.cost_usd_ticks IS NULL), 0)::bigint AS cache_write_tokens
FROM task_usage u
JOIN agent_task_queue q ON q.id = u.task_id
WHERE q.context->>'stream_id' = @stream_id::text AND u.created_at >= @since::timestamptz
GROUP BY u.model;

-- name: InsertUIBlock :one
-- The id is derived from (task_id, seq) in code: a redelivered reply finds
-- the stored block instead of storing a second one.
INSERT INTO ui_block (id, message_id, workspace_id, chat_session_id, task_id, seq, kind, openui_source, a2ui, status, issues, repairs, text_version)
VALUES (@id, @message_id, @workspace_id, @chat_session_id, @task_id, @seq, @kind, @openui_source, @a2ui, @status, @issues, @repairs, @text_version)
ON CONFLICT (id) DO NOTHING
RETURNING *;

-- name: GetUIBlock :one
SELECT * FROM ui_block WHERE id = $1;

-- name: SetUIBlockA2UI :exec
UPDATE ui_block SET a2ui = $2, updated_at = now() WHERE id = $1;

-- name: ListUIBlocksBySession :many
-- Newest first; failed screens are not shown.
SELECT * FROM ui_block
WHERE chat_session_id = $1 AND workspace_id = $2 AND status <> 'failed'
ORDER BY created_at DESC LIMIT $3;

-- name: InsertBlockUIAction :one
INSERT INTO ui_action (ui_block_id, ask_id, token, action, zone, idempotency_key)
VALUES (@ui_block_id, @ask_id, @token, @action, @zone, @idempotency_key)
ON CONFLICT (idempotency_key) DO NOTHING
RETURNING *;

-- name: GetUIAction :one
SELECT * FROM ui_action WHERE id = $1;

-- name: GetUIActionByKey :one
SELECT * FROM ui_action WHERE idempotency_key = $1;

-- name: ConfirmUIAction :execrows
-- A red action waits for the app's own confirmation window (§10.4). The
-- window (its id and text) is kept in result; a new press opens a new one.
UPDATE ui_action SET status = 'confirming', result = @confirm_window
WHERE id = @id AND status IN ('pending', 'confirming');

-- name: FinishUIActionAny :execrows
-- Claims a pending or confirming action; 0 rows: another press won.
UPDATE ui_action SET status = @status, result = @result, done_at = now()
WHERE id = @id AND status IN ('pending', 'confirming');

-- name: CancelUIActionConfirm :execrows
UPDATE ui_action SET status = 'pending', result = NULL WHERE id = $1 AND status = 'confirming';

-- name: SetUIActionResult :exec
UPDATE ui_action SET result = $2 WHERE id = $1;

-- name: TaskChatAgent :one
SELECT agent_id, chat_session_id FROM agent_task_queue WHERE id = $1;

-- name: StreamDailyCost :many
-- Usage per stream, Moscow day and model since a moment; priced in code
-- like StreamCostSince.
SELECT (q.context->>'stream_id')::text AS stream_id,
       to_char(u.created_at AT TIME ZONE 'Europe/Moscow', 'YYYY-MM-DD')::text AS day,
       u.model,
       COALESCE(SUM(u.cost_usd_ticks), 0)::bigint AS ticks,
       COALESCE(SUM(u.input_tokens) FILTER (WHERE u.cost_usd_ticks IS NULL), 0)::bigint AS input_tokens,
       COALESCE(SUM(u.output_tokens) FILTER (WHERE u.cost_usd_ticks IS NULL), 0)::bigint AS output_tokens,
       COALESCE(SUM(u.cache_read_tokens) FILTER (WHERE u.cost_usd_ticks IS NULL), 0)::bigint AS cache_read_tokens,
       COALESCE(SUM(u.cache_write_tokens) FILTER (WHERE u.cost_usd_ticks IS NULL), 0)::bigint AS cache_write_tokens
FROM task_usage u
JOIN agent_task_queue q ON q.id = u.task_id
WHERE q.context->>'stream_id' = ANY(@stream_ids::text[])
  AND u.created_at >= @since::timestamptz
GROUP BY 1, 2, 3;

-- name: WorkspaceCostSince :many
-- All usage of the workspace's agents since a moment, per model; priced in
-- code like StreamCostSince.
SELECT u.model,
       COALESCE(SUM(u.cost_usd_ticks), 0)::bigint AS ticks,
       COALESCE(SUM(u.input_tokens) FILTER (WHERE u.cost_usd_ticks IS NULL), 0)::bigint AS input_tokens,
       COALESCE(SUM(u.output_tokens) FILTER (WHERE u.cost_usd_ticks IS NULL), 0)::bigint AS output_tokens,
       COALESCE(SUM(u.cache_read_tokens) FILTER (WHERE u.cost_usd_ticks IS NULL), 0)::bigint AS cache_read_tokens,
       COALESCE(SUM(u.cache_write_tokens) FILTER (WHERE u.cost_usd_ticks IS NULL), 0)::bigint AS cache_write_tokens
FROM task_usage u
JOIN agent_task_queue q ON q.id = u.task_id
JOIN agent a ON a.id = q.agent_id
WHERE a.workspace_id = @workspace_id AND u.created_at >= @since::timestamptz
GROUP BY u.model;

-- name: StreamCardStats :one
SELECT
    (SELECT count(*) FROM stream_step s WHERE s.stream_id = @stream_id AND s.status = 'done' AND s.finished_at >= @since::timestamptz)::int AS steps_done,
    (SELECT count(*) FROM milestone m WHERE m.stream_id = @stream_id AND m.created_at >= @since::timestamptz)::int AS milestones,
    (SELECT count(*) FROM ask a WHERE a.stream_id = @stream_id AND a.status IN ('queued', 'shown'))::int AS open_asks;

-- name: SetStreamShare :exec
UPDATE stream SET charter = $2, min_share = $3, updated_at = now() WHERE id = $1;

-- name: SetUIBlocksMessage :exec
UPDATE ui_block SET message_id = @message_id, updated_at = now()
WHERE task_id = @task_id AND message_id IS NULL;

-- name: ListUIBlocksByTask :many
SELECT * FROM ui_block WHERE task_id = $1 ORDER BY seq;

-- name: WorkspaceSlug :one
SELECT slug FROM workspace WHERE id = $1;

-- name: ChatSessionCreator :one
SELECT creator_id FROM chat_session WHERE id = $1;

-- name: ChatSessionOfUser :one
-- A chat of this user in this workspace: screens are private to the chat.
SELECT id FROM chat_session WHERE id = @id AND workspace_id = @workspace_id AND creator_id = @user_id;
