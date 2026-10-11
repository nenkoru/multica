-- name: ListSprints :many
SELECT * FROM sprint
WHERE workspace_id = $1
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status'))
ORDER BY start_date DESC;

-- name: GetSprintInWorkspace :one
SELECT * FROM sprint
WHERE id = $1 AND workspace_id = $2;

-- name: CreateSprint :one
INSERT INTO sprint (
    workspace_id, name, goal, start_date, end_date, status
) VALUES (
    $1, $2, $3, $4, $5, $6
) RETURNING *;

-- name: UpdateSprint :one
UPDATE sprint SET
    name = COALESCE(sqlc.narg('name'), name),
    goal = sqlc.narg('goal'),
    start_date = COALESCE(sqlc.narg('start_date'), start_date),
    end_date = COALESCE(sqlc.narg('end_date'), end_date),
    status = COALESCE(sqlc.narg('status'), status),
    updated_at = now()
WHERE id = $1 AND workspace_id = sqlc.arg('workspace_id')
RETURNING *;

-- name: DeleteSprint :exec
DELETE FROM sprint WHERE id = $1 AND workspace_id = $2;

-- name: GetSprintIssueStats :many
SELECT sprint_id,
       count(*)::bigint AS total_count,
       count(*) FILTER (WHERE status IN ('done', 'cancelled') OR status IN (SELECT key FROM issue_status WHERE issue_status.workspace_id = issue.workspace_id AND category IN ('done', 'closed')))::bigint AS done_count
FROM issue
WHERE sprint_id = ANY(sqlc.arg('sprint_ids')::uuid[])
  AND EXISTS (SELECT 1 FROM sprint WHERE sprint.id = issue.sprint_id AND sprint.workspace_id = issue.workspace_id)
GROUP BY sprint_id;
