-- name: ListEpics :many
SELECT * FROM epic
WHERE workspace_id = $1
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status'))
ORDER BY created_at DESC;

-- name: GetEpicInWorkspace :one
SELECT * FROM epic
WHERE id = $1 AND workspace_id = $2;

-- name: CreateEpic :one
INSERT INTO epic (
    workspace_id, title, description, color, status
) VALUES (
    $1, $2, $3, $4, $5
) RETURNING *;

-- name: UpdateEpic :one
UPDATE epic SET
    title = COALESCE(sqlc.narg('title'), title),
    description = sqlc.narg('description'),
    color = COALESCE(sqlc.narg('color'), color),
    status = COALESCE(sqlc.narg('status'), status),
    updated_at = now()
WHERE id = $1 AND workspace_id = sqlc.arg('workspace_id')
RETURNING *;

-- name: DeleteEpic :exec
DELETE FROM epic WHERE id = $1 AND workspace_id = $2;

-- name: GetEpicIssueStats :many
SELECT epic_id,
       count(*)::bigint AS total_count,
       count(*) FILTER (WHERE status IN ('done', 'cancelled') OR status IN (SELECT key FROM issue_status WHERE issue_status.workspace_id = issue.workspace_id AND category IN ('done', 'closed')))::bigint AS done_count
FROM issue
WHERE epic_id = ANY(sqlc.arg('epic_ids')::uuid[])
  AND EXISTS (SELECT 1 FROM epic WHERE epic.id = issue.epic_id AND epic.workspace_id = issue.workspace_id)
GROUP BY epic_id;
