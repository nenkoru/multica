package handler

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/logger"
)

// GET /api/tv/game feeds the TV game layer (RUSL-1786): who works on what,
// per agent, plus the recent completions and failed runs the alarm layer
// turns into events. Same kiosk credential and the same rules as the TV
// overview: top-level issues only (a sub-issue's run shows on its parent),
// titles but no descriptions or comments.

const (
	tvGameAgentLimit   = 16
	tvGameWorkLimit    = 60
	tvGameRecentLimit  = 20
	tvGameAgentWindow  = 7 * 24 * time.Hour
	tvGameRecentWindow = 24 * time.Hour
	tvGameFailedWindow = 10 * time.Minute
	tvGameStreakDays   = 60
)

type TVGameAgent struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Slots int32  `json:"slots"`
}

type TVGameWork struct {
	IssueID        string  `json:"issue_id"`
	Title          string  `json:"title"`
	AgentID        string  `json:"agent_id"`
	EpicID         *string `json:"epic_id"`
	State          string  `json:"state"`
	LastActivityAt *string `json:"last_activity_at"`
	// Since is when the run started, or when the issue entered its status.
	Since *string `json:"since"`
}

type TVGameEpic struct {
	ID             string  `json:"id"`
	Title          string  `json:"title"`
	Open           int64   `json:"open"`
	DoneInSprint   int64   `json:"done_in_sprint"`
	LastActivityAt *string `json:"last_activity_at"`
}

type TVGameDone struct {
	IssueID string  `json:"issue_id"`
	Title   string  `json:"title"`
	EpicID  *string `json:"epic_id"`
	AgentID *string `json:"agent_id"`
	At      string  `json:"at"`
}

type TVGameEpicClosed struct {
	EpicID string `json:"epic_id"`
	Title  string `json:"title"`
	At     string `json:"at"`
}

type TVGameFailed struct {
	TaskID    string  `json:"task_id"`
	IssueID   string  `json:"issue_id"`
	Title     string  `json:"title"`
	AgentID   string  `json:"agent_id"`
	AgentName string  `json:"agent_name"`
	EpicID    *string `json:"epic_id"`
	At        string  `json:"at"`
}

type TVGameResponse struct {
	GeneratedAt       string             `json:"generated_at"`
	HostCeiling       int                `json:"host_ceiling"`
	Running           int64              `json:"running"`
	Queued            int64              `json:"queued"`
	DoneInSprint      int64              `json:"done_in_sprint"`
	DoneToday         int64              `json:"done_today"`
	StreakDays        int                `json:"streak_days"`
	Agents            []TVGameAgent      `json:"agents"`
	Work              []TVGameWork       `json:"work"`
	Epics             []TVGameEpic       `json:"epics"`
	RecentDone        []TVGameDone       `json:"recent_done"`
	RecentEpicsClosed []TVGameEpicClosed `json:"recent_epics_closed"`
	RecentFailed      []TVGameFailed     `json:"recent_failed"`
}

// tvRootIssue resolves the top-level issue of the issue aliased `i` (one
// parent level, as the board nests) into `r`.
const tvRootIssue = `JOIN issue r ON r.id = COALESCE(i.parent_issue_id, i.id)`

func (h *Handler) GetTVGame(w http.ResponseWriter, r *http.Request) {
	session, ok := h.requireKiosk(w, r)
	if !ok {
		return
	}
	resp, err := h.loadTVGame(r.Context(), session.WorkspaceID, time.Now())
	if err != nil {
		slog.Error("tv game failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to load tv game")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) loadTVGame(ctx context.Context, ws pgtype.UUID, now time.Time) (TVGameResponse, error) {
	ceiling := h.cfg.TV.HostCeiling
	if ceiling <= 0 {
		ceiling = DefaultTVConfig().HostCeiling
	}
	resp := TVGameResponse{
		GeneratedAt:       now.UTC().Format(time.RFC3339),
		HostCeiling:       ceiling,
		Agents:            []TVGameAgent{},
		Work:              []TVGameWork{},
		Epics:             []TVGameEpic{},
		RecentDone:        []TVGameDone{},
		RecentEpicsClosed: []TVGameEpicClosed{},
		RecentFailed:      []TVGameFailed{},
	}
	if err := h.DB.QueryRow(ctx, `
SELECT
  count(*) FILTER (WHERE q.status IN ('dispatched', 'running')),
  count(*) FILTER (WHERE q.status = 'queued')
FROM agent_task_queue q JOIN agent a ON a.id = q.agent_id
WHERE a.workspace_id = $1 AND q.status IN ('queued', 'dispatched', 'running')`, ws).Scan(&resp.Running, &resp.Queued); err != nil {
		return resp, fmt.Errorf("counters: %w", err)
	}

	sprint, err := h.loadTVSprint(ctx, ws)
	if err != nil {
		return resp, fmt.Errorf("sprint: %w", err)
	}
	var sprintID pgtype.UUID
	if sprint != nil {
		resp.DoneInSprint = sprint.Done
		sprintID = parseUUID(sprint.ID)
	}

	if err := h.loadTVGameAgents(ctx, ws, now, &resp); err != nil {
		return resp, fmt.Errorf("agents: %w", err)
	}
	if err := h.loadTVGameWork(ctx, ws, &resp); err != nil {
		return resp, fmt.Errorf("work: %w", err)
	}
	if err := h.loadTVGameEpics(ctx, ws, sprintID, &resp); err != nil {
		return resp, fmt.Errorf("epics: %w", err)
	}
	if err := h.loadTVGameRecent(ctx, ws, now, &resp); err != nil {
		return resp, fmt.Errorf("recent: %w", err)
	}
	if err := h.loadTVGameStreak(ctx, ws, now, &resp); err != nil {
		return resp, fmt.Errorf("streak: %w", err)
	}
	return resp, nil
}

// Agents with a run in the last week, or one going now; busiest first.
func (h *Handler) loadTVGameAgents(ctx context.Context, ws pgtype.UUID, now time.Time, resp *TVGameResponse) error {
	rows, err := h.DB.Query(ctx, `
SELECT a.id, a.name, a.max_concurrent_tasks
FROM agent a
JOIN agent_task_queue q ON q.agent_id = a.id
WHERE a.workspace_id = $1 AND a.archived_at IS NULL
  AND (q.created_at >= $2 OR q.status IN ('dispatched', 'running'))
GROUP BY a.id, a.name, a.max_concurrent_tasks
ORDER BY count(*) FILTER (WHERE q.status IN ('dispatched', 'running')) DESC, max(q.created_at) DESC, a.id
LIMIT $3`, ws, now.Add(-tvGameAgentWindow), tvGameAgentLimit)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id pgtype.UUID
		var agent TVGameAgent
		if err := rows.Scan(&id, &agent.Name, &agent.Slots); err != nil {
			return err
		}
		agent.ID = uuidToString(id)
		resp.Agents = append(resp.Agents, agent)
	}
	return rows.Err()
}

// Work is a running task (shown on its top-level issue), or a top-level
// issue in review or blocked that is assigned to an agent. An issue with a
// running task shows as running only.
func (h *Handler) loadTVGameWork(ctx context.Context, ws pgtype.UUID, resp *TVGameResponse) error {
	rows, err := h.DB.Query(ctx, fmt.Sprintf(`
WITH running AS (
  SELECT DISTINCT ON (r.id, q.agent_id) r.id AS issue_id, r.title, q.agent_id, COALESCE(r.epic_id, i.epic_id) AS epic_id,
    'running' AS state, COALESCE(r.last_activity_at, r.updated_at) AS activity_at, q.started_at AS sort_at,
    COALESCE(q.started_at, q.dispatched_at) AS since_at
  FROM agent_task_queue q
  JOIN agent a ON a.id = q.agent_id
  JOIN issue i ON i.id = q.issue_id
  %[2]s
  WHERE a.workspace_id = $1 AND q.status IN ('dispatched', 'running')
  ORDER BY r.id, q.agent_id, q.started_at
),
waiting AS (
  SELECT i.id AS issue_id, i.title, i.assignee_id AS agent_id, i.epic_id,
    CASE %[1]s WHEN 'in_review' THEN 'in_review' ELSE 'blocked' END AS state,
    COALESCE(i.last_activity_at, i.updated_at) AS activity_at, COALESCE(i.last_activity_at, i.updated_at) AS sort_at,
    %[3]s AS since_at
  FROM issue i
  WHERE i.workspace_id = $1 AND i.parent_issue_id IS NULL AND i.assignee_type = 'agent'
    AND %[1]s IN ('in_review', 'blocked')
    AND NOT EXISTS (SELECT 1 FROM running x WHERE x.issue_id = i.id)
)
SELECT issue_id, title, agent_id, epic_id, state, activity_at, since_at FROM (
  SELECT * FROM running
  UNION ALL
  SELECT * FROM waiting
) w
ORDER BY (state = 'running') DESC, sort_at DESC NULLS LAST, issue_id
LIMIT $2`, planningBucketExpr, tvRootIssue, tvSinceExpr), ws, tvGameWorkLimit)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var issueID, agentID, epicID pgtype.UUID
		var activity, since pgtype.Timestamptz
		var work TVGameWork
		if err := rows.Scan(&issueID, &work.Title, &agentID, &epicID, &work.State, &activity, &since); err != nil {
			return err
		}
		work.IssueID = uuidToString(issueID)
		work.AgentID = uuidToString(agentID)
		work.EpicID = tvUUIDPtr(epicID)
		work.LastActivityAt = timestampToPtr(activity)
		work.Since = timestampToPtr(since)
		resp.Work = append(resp.Work, work)
	}
	return rows.Err()
}

// Open epics with open top-level issues or work done in the sprint.
func (h *Handler) loadTVGameEpics(ctx context.Context, ws, sprintID pgtype.UUID, resp *TVGameResponse) error {
	rows, err := h.DB.Query(ctx, fmt.Sprintf(`
SELECT e.id, e.title,
  count(*) FILTER (WHERE t.bucket NOT IN ('done', 'cancelled'))::bigint,
  count(*) FILTER (WHERE t.bucket = 'done' AND t.sprint_id = $2)::bigint,
  max(t.activity_at)
FROM epic e
JOIN (
  SELECT i.epic_id, i.sprint_id, %[1]s AS bucket, COALESCE(i.last_activity_at, i.updated_at) AS activity_at
  FROM issue i
  WHERE i.workspace_id = $1 AND i.parent_issue_id IS NULL AND i.epic_id IS NOT NULL
) t ON t.epic_id = e.id
WHERE e.workspace_id = $1 AND e.status = 'open'
GROUP BY e.id, e.title
HAVING count(*) FILTER (WHERE t.bucket NOT IN ('done', 'cancelled')) > 0
    OR count(*) FILTER (WHERE t.bucket = 'done' AND t.sprint_id = $2) > 0
ORDER BY max(t.activity_at) DESC NULLS LAST, e.id
LIMIT 12`, planningBucketExpr), ws, sprintID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id pgtype.UUID
		var last pgtype.Timestamptz
		var epic TVGameEpic
		if err := rows.Scan(&id, &epic.Title, &epic.Open, &epic.DoneInSprint, &last); err != nil {
			return err
		}
		epic.ID = uuidToString(id)
		epic.LastActivityAt = timestampToPtr(last)
		resp.Epics = append(resp.Epics, epic)
	}
	return rows.Err()
}

func (h *Handler) loadTVGameRecent(ctx context.Context, ws pgtype.UUID, now time.Time, resp *TVGameResponse) error {
	done, err := h.DB.Query(ctx, fmt.Sprintf(`
SELECT i.id, i.title, i.epic_id, CASE WHEN i.assignee_type = 'agent' THEN i.assignee_id END, %[1]s AS at
FROM issue i
WHERE i.workspace_id = $1 AND i.parent_issue_id IS NULL AND i.status = 'done' AND i.updated_at >= $2
ORDER BY at DESC, i.id
LIMIT $3`, tvSinceExpr), ws, now.Add(-tvGameRecentWindow), tvGameRecentLimit)
	if err != nil {
		return err
	}
	for done.Next() {
		var id, epicID, agentID pgtype.UUID
		var at pgtype.Timestamptz
		var row TVGameDone
		if err := done.Scan(&id, &row.Title, &epicID, &agentID, &at); err != nil {
			done.Close()
			return err
		}
		row.IssueID = uuidToString(id)
		row.EpicID = tvUUIDPtr(epicID)
		row.AgentID = tvUUIDPtr(agentID)
		row.At = timestampToString(at)
		if at.Valid && now.Sub(at.Time) > tvGameRecentWindow {
			continue
		}
		resp.RecentDone = append(resp.RecentDone, row)
	}
	done.Close()
	if err := done.Err(); err != nil {
		return err
	}

	closed, err := h.DB.Query(ctx, `
SELECT id, title, updated_at FROM epic
WHERE workspace_id = $1 AND status = 'closed' AND updated_at >= $2
ORDER BY updated_at DESC, id
LIMIT $3`, ws, now.Add(-tvGameRecentWindow), tvGameRecentLimit)
	if err != nil {
		return err
	}
	for closed.Next() {
		var id pgtype.UUID
		var at pgtype.Timestamptz
		var row TVGameEpicClosed
		if err := closed.Scan(&id, &row.Title, &at); err != nil {
			closed.Close()
			return err
		}
		row.EpicID = uuidToString(id)
		row.At = timestampToString(at)
		resp.RecentEpicsClosed = append(resp.RecentEpicsClosed, row)
	}
	closed.Close()
	if err := closed.Err(); err != nil {
		return err
	}

	failed, err := h.DB.Query(ctx, fmt.Sprintf(`
SELECT q.id, r.id, r.title, a.id, a.name, COALESCE(r.epic_id, i.epic_id), q.completed_at
FROM agent_task_queue q
JOIN agent a ON a.id = q.agent_id
JOIN issue i ON i.id = q.issue_id
%[1]s
WHERE a.workspace_id = $1 AND q.status = 'failed' AND q.completed_at >= $2
ORDER BY q.completed_at DESC, q.id
LIMIT $3`, tvRootIssue), ws, now.Add(-tvGameFailedWindow), tvGameRecentLimit)
	if err != nil {
		return err
	}
	defer failed.Close()
	for failed.Next() {
		var taskID, issueID, agentID, epicID pgtype.UUID
		var at pgtype.Timestamptz
		var row TVGameFailed
		if err := failed.Scan(&taskID, &issueID, &row.Title, &agentID, &row.AgentName, &epicID, &at); err != nil {
			return err
		}
		row.TaskID = uuidToString(taskID)
		row.IssueID = uuidToString(issueID)
		row.AgentID = uuidToString(agentID)
		row.EpicID = tvUUIDPtr(epicID)
		row.At = timestampToString(at)
		resp.RecentFailed = append(resp.RecentFailed, row)
	}
	return failed.Err()
}

func tvUUIDPtr(id pgtype.UUID) *string {
	if !id.Valid {
		return nil
	}
	s := uuidToString(id)
	return &s
}

// loadTVGameStreak counts top-level issues finished today (Moscow time) and
// the run of consecutive days, ending today or yesterday, with at least one.
func (h *Handler) loadTVGameStreak(ctx context.Context, ws pgtype.UUID, now time.Time, resp *TVGameResponse) error {
	rows, err := h.DB.Query(ctx, `
SELECT (al.created_at AT TIME ZONE 'Europe/Moscow')::date AS day, count(DISTINCT al.issue_id)
FROM activity_log al
JOIN issue i ON i.id = al.issue_id
WHERE i.workspace_id = $1 AND i.parent_issue_id IS NULL
  AND al.action = 'status_changed' AND al.details->>'to' = 'done'
  AND al.created_at > $2
GROUP BY day`, ws, now.Add(-tvGameStreakDays*24*time.Hour))
	if err != nil {
		return err
	}
	defer rows.Close()
	days := map[string]int64{}
	for rows.Next() {
		var day pgtype.Date
		var n int64
		if err := rows.Scan(&day, &n); err != nil {
			return err
		}
		days[day.Time.Format("2006-01-02")] = n
	}
	if err := rows.Err(); err != nil {
		return err
	}
	today := now.In(tvMoscow)
	resp.DoneToday = days[today.Format("2006-01-02")]
	day := today
	if resp.DoneToday == 0 {
		// Today is not over yet: an empty morning does not break the streak.
		day = day.AddDate(0, 0, -1)
	}
	for days[day.Format("2006-01-02")] > 0 {
		resp.StreakDays++
		day = day.AddDate(0, 0, -1)
	}
	return nil
}
