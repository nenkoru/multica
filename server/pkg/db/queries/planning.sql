-- name: LockPlanningWorkspace :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg('workspace_id')::uuid::text, 1435));
