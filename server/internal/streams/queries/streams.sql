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

-- name: ResolveAsk :exec
UPDATE ask SET status = $2, answer = $3, resolved_at = now() WHERE id = $1 AND status IN ('queued', 'shown');

-- name: ListAsksResolvedSince :many
SELECT * FROM ask WHERE stream_id = $1 AND status IN ('answered', 'defaulted') AND resolved_at > $2 ORDER BY resolved_at;

-- name: GetAsk :one
SELECT * FROM ask WHERE id = $1;

-- name: InsertMilestone :one
INSERT INTO milestone (stream_id, step_id, text, evidence, metric_delta)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

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
