package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/logger"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Planning progress buckets. Every issue lands in exactly one. Built-in status
// keys decide first; a custom status falls back to its lifecycle category.
const (
	planningBucketDone       = "done"
	planningBucketInProgress = "in_progress"
	planningBucketInReview   = "in_review"
	planningBucketBlocked    = "blocked"
	planningBucketNotStarted = "not_started"
	planningBucketCancelled  = "cancelled"
)

// planningBucketExpr classifies the issue row aliased `i`.
const planningBucketExpr = `CASE
  WHEN i.status = 'done' THEN 'done'
  WHEN i.status = 'cancelled' THEN 'cancelled'
  WHEN i.status = 'in_review' THEN 'in_review'
  WHEN i.status = 'blocked' THEN 'blocked'
  WHEN i.status = 'in_progress' THEN 'in_progress'
  WHEN i.status IN ('todo', 'backlog') THEN 'not_started'
  ELSE COALESCE((
    SELECT CASE s.category
      WHEN 'done' THEN 'done'
      WHEN 'closed' THEN 'cancelled'
      WHEN 'started' THEN 'in_progress'
      ELSE 'not_started'
    END
    FROM issue_status s
    WHERE s.workspace_id = i.workspace_id AND s.key = i.status
    LIMIT 1
  ), 'not_started')
END`

const (
	planningAttentionLimit = 20
	planningActivityLimit  = 10
	// A burndown longer than this is not a sprint chart anymore; the series is
	// cut rather than returning one point per day of an unbounded range.
	planningBurndownMaxDays = 366
)

// planningActivityActions are the activity rows a planning page can describe
// in one line. Agent-run bookkeeping stays on the issue timeline.
var planningActivityActions = []string{"created", "status_changed", "assignee_changed", "priority_changed", "title_changed"}

type PlanningProgress struct {
	Total      int64 `json:"total"`
	Done       int64 `json:"done"`
	InProgress int64 `json:"in_progress"`
	InReview   int64 `json:"in_review"`
	Blocked    int64 `json:"blocked"`
	NotStarted int64 `json:"not_started"`
	Cancelled  int64 `json:"cancelled"`
}

func (p *PlanningProgress) add(bucket string, n int64) {
	p.Total += n
	switch bucket {
	case planningBucketDone:
		p.Done += n
	case planningBucketInProgress:
		p.InProgress += n
	case planningBucketInReview:
		p.InReview += n
	case planningBucketBlocked:
		p.Blocked += n
	case planningBucketCancelled:
		p.Cancelled += n
	default:
		p.NotStarted += n
	}
}

type PlanningAssigneeBreakdown struct {
	AssigneeType *string `json:"assignee_type"`
	AssigneeID   *string `json:"assignee_id"`
	Total        int64   `json:"total"`
	Done         int64   `json:"done"`
	Open         int64   `json:"open"`
}

type PlanningSprintBreakdown struct {
	SprintID  *string `json:"sprint_id"`
	Name      string  `json:"name"`
	Status    string  `json:"status"`
	StartDate string  `json:"start_date"`
	EndDate   string  `json:"end_date"`
	Total     int64   `json:"total"`
	Done      int64   `json:"done"`
}

type PlanningEpicBreakdown struct {
	EpicID *string `json:"epic_id"`
	Title  string  `json:"title"`
	Color  string  `json:"color"`
	Status string  `json:"status"`
	Total  int64   `json:"total"`
	Done   int64   `json:"done"`
}

type PlanningAttentionIssue struct {
	ID           string  `json:"id"`
	Identifier   string  `json:"identifier"`
	Title        string  `json:"title"`
	Status       string  `json:"status"`
	Bucket       string  `json:"bucket"`
	AssigneeType *string `json:"assignee_type"`
	AssigneeID   *string `json:"assignee_id"`
	UpdatedAt    string  `json:"updated_at"`
}

type PlanningActivityEntry struct {
	ID              string          `json:"id"`
	IssueID         string          `json:"issue_id"`
	IssueIdentifier string          `json:"issue_identifier"`
	IssueTitle      string          `json:"issue_title"`
	ActorType       *string         `json:"actor_type"`
	ActorID         *string         `json:"actor_id"`
	Action          string          `json:"action"`
	Details         json.RawMessage `json:"details"`
	CreatedAt       string          `json:"created_at"`
}

type PlanningBurndownDay struct {
	Date      string `json:"date"`
	Remaining int64  `json:"remaining"`
}

type PlanningBurndown struct {
	Total     int64                 `json:"total"`
	StartDate string                `json:"start_date"`
	EndDate   string                `json:"end_date"`
	Days      []PlanningBurndownDay `json:"days"`
}

type EpicOverviewResponse struct {
	EpicID         string                      `json:"epic_id"`
	Progress       PlanningProgress            `json:"progress"`
	LastActivityAt *string                     `json:"last_activity_at"`
	Assignees      []PlanningAssigneeBreakdown `json:"assignees"`
	Sprints        []PlanningSprintBreakdown   `json:"sprints"`
	Attention      []PlanningAttentionIssue    `json:"attention"`
	Activity       []PlanningActivityEntry     `json:"activity"`
}

type SprintOverviewResponse struct {
	SprintID       string                      `json:"sprint_id"`
	Progress       PlanningProgress            `json:"progress"`
	LastActivityAt *string                     `json:"last_activity_at"`
	Assignees      []PlanningAssigneeBreakdown `json:"assignees"`
	Epics          []PlanningEpicBreakdown     `json:"epics"`
	Attention      []PlanningAttentionIssue    `json:"attention"`
	Burndown       *PlanningBurndown           `json:"burndown"`
}

// planningRelation names the issue column a planning page is scoped by and the
// other planning column its breakdown groups on. Both come from this fixed
// table, never from request input.
type planningRelation struct {
	column string
	other  string
}

var (
	planningEpicRelation   = planningRelation{column: "epic_id", other: "sprint_id"}
	planningSprintRelation = planningRelation{column: "sprint_id", other: "epic_id"}
)

type planningOtherCount struct {
	total int64
	done  int64
}

type planningAggregates struct {
	progress       PlanningProgress
	lastActivityAt *string
	assignees      []PlanningAssigneeBreakdown
	// Keyed by the other relation's UUID; "" collects issues without one.
	others map[string]*planningOtherCount
}

// loadPlanningAggregates folds one grouped scan into the status progress, the
// per-assignee breakdown and the breakdown over the other planning relation.
func (h *Handler) loadPlanningAggregates(ctx context.Context, rel planningRelation, wsID, id pgtype.UUID) (planningAggregates, error) {
	out := planningAggregates{assignees: []PlanningAssigneeBreakdown{}, others: map[string]*planningOtherCount{}}
	rows, err := h.DB.Query(ctx, fmt.Sprintf(`
SELECT bucket, assignee_type, assignee_id, other_id, count(*)::bigint, max(activity_at)
FROM (
  SELECT %s AS bucket, i.assignee_type, i.assignee_id, i.%s AS other_id,
         COALESCE(i.last_activity_at, i.updated_at) AS activity_at
  FROM issue i
  WHERE i.workspace_id = $1 AND i.%s = $2
) t
GROUP BY bucket, assignee_type, assignee_id, other_id`, planningBucketExpr, rel.other, rel.column), wsID, id)
	if err != nil {
		return out, err
	}
	defer rows.Close()

	assignees := map[string]*PlanningAssigneeBreakdown{}
	var last pgtype.Timestamptz
	for rows.Next() {
		var (
			bucket       string
			assigneeType pgtype.Text
			assigneeID   pgtype.UUID
			otherID      pgtype.UUID
			count        int64
			activityAt   pgtype.Timestamptz
		)
		if err := rows.Scan(&bucket, &assigneeType, &assigneeID, &otherID, &count, &activityAt); err != nil {
			return out, err
		}
		out.progress.add(bucket, count)
		if activityAt.Valid && (!last.Valid || activityAt.Time.After(last.Time)) {
			last = activityAt
		}

		assigneeKey := ""
		if assigneeType.Valid && assigneeID.Valid {
			assigneeKey = assigneeType.String + ":" + uuidToString(assigneeID)
		}
		entry, ok := assignees[assigneeKey]
		if !ok {
			entry = &PlanningAssigneeBreakdown{}
			if assigneeKey != "" {
				entry.AssigneeType = textToPtr(assigneeType)
				entry.AssigneeID = uuidToPtr(assigneeID)
			}
			assignees[assigneeKey] = entry
		}
		entry.Total += count
		if bucket == planningBucketDone {
			entry.Done += count
		}
		if bucket != planningBucketDone && bucket != planningBucketCancelled {
			entry.Open += count
		}

		otherKey := ""
		if otherID.Valid {
			otherKey = uuidToString(otherID)
		}
		other, ok := out.others[otherKey]
		if !ok {
			other = &planningOtherCount{}
			out.others[otherKey] = other
		}
		other.total += count
		if bucket == planningBucketDone {
			other.done += count
		}
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	if last.Valid {
		s := timestampToString(last)
		out.lastActivityAt = &s
	}
	keys := make([]string, 0, len(assignees))
	for key := range assignees {
		keys = append(keys, key)
	}
	// Largest load first, unassigned last, key as the stable tie-break.
	sort.Slice(keys, func(a, b int) bool {
		if (keys[a] == "") != (keys[b] == "") {
			return keys[b] == ""
		}
		if assignees[keys[a]].Total != assignees[keys[b]].Total {
			return assignees[keys[a]].Total > assignees[keys[b]].Total
		}
		return keys[a] < keys[b]
	})
	for _, key := range keys {
		out.assignees = append(out.assignees, *assignees[key])
	}
	return out, nil
}

func planningOtherIDs(others map[string]*planningOtherCount) []pgtype.UUID {
	ids := make([]pgtype.UUID, 0, len(others))
	for key := range others {
		if key != "" {
			ids = append(ids, parseUUID(key))
		}
	}
	return ids
}

func (h *Handler) loadPlanningAttention(ctx context.Context, rel planningRelation, wsID, id pgtype.UUID, prefix string) ([]PlanningAttentionIssue, error) {
	out := []PlanningAttentionIssue{}
	rows, err := h.DB.Query(ctx, fmt.Sprintf(`
SELECT id, number, title, status, bucket, assignee_type, assignee_id, updated_at
FROM (
  SELECT i.id, i.number, i.title, i.status, %s AS bucket, i.assignee_type, i.assignee_id, i.updated_at
  FROM issue i
  WHERE i.workspace_id = $1 AND i.%s = $2
) t
WHERE bucket IN ('blocked', 'in_review')
ORDER BY (bucket = 'blocked') DESC, updated_at DESC, id
LIMIT %d`, planningBucketExpr, rel.column, planningAttentionLimit), wsID, id)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			issueID      pgtype.UUID
			number       int32
			item         PlanningAttentionIssue
			assigneeType pgtype.Text
			assigneeID   pgtype.UUID
			updatedAt    pgtype.Timestamptz
		)
		if err := rows.Scan(&issueID, &number, &item.Title, &item.Status, &item.Bucket, &assigneeType, &assigneeID, &updatedAt); err != nil {
			return out, err
		}
		item.ID = uuidToString(issueID)
		item.Identifier = prefix + "-" + strconv.Itoa(int(number))
		item.AssigneeType = textToPtr(assigneeType)
		item.AssigneeID = uuidToPtr(assigneeID)
		item.UpdatedAt = timestampToString(updatedAt)
		out = append(out, item)
	}
	return out, rows.Err()
}

// loadPlanningActivity returns the newest activity rows across the relation's
// issues. The lateral subquery takes each issue's newest rows through the
// (issue_id, created_at DESC, id DESC) keyset index, so the scan is bounded by
// issue count rather than by the size of the workspace activity log.
func (h *Handler) loadPlanningActivity(ctx context.Context, rel planningRelation, wsID, id pgtype.UUID, prefix string) ([]PlanningActivityEntry, error) {
	out := []PlanningActivityEntry{}
	rows, err := h.DB.Query(ctx, fmt.Sprintf(`
SELECT a.id, i.id, i.number, i.title, a.actor_type, a.actor_id, a.action, a.details, a.created_at
FROM issue i
CROSS JOIN LATERAL (
  SELECT al.id, al.actor_type, al.actor_id, al.action, al.details, al.created_at
  FROM activity_log al
  WHERE al.issue_id = i.id AND al.action = ANY($3::text[])
  ORDER BY al.created_at DESC, al.id DESC
  LIMIT %d
) a
WHERE i.workspace_id = $1 AND i.%s = $2
ORDER BY a.created_at DESC, a.id DESC
LIMIT %d`, planningActivityLimit, rel.column, planningActivityLimit), wsID, id, planningActivityActions)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			activityID pgtype.UUID
			issueID    pgtype.UUID
			number     int32
			item       PlanningActivityEntry
			actorType  pgtype.Text
			actorID    pgtype.UUID
			details    []byte
			createdAt  pgtype.Timestamptz
		)
		if err := rows.Scan(&activityID, &issueID, &number, &item.IssueTitle, &actorType, &actorID, &item.Action, &details, &createdAt); err != nil {
			return out, err
		}
		item.ID = uuidToString(activityID)
		item.IssueID = uuidToString(issueID)
		item.IssueIdentifier = prefix + "-" + strconv.Itoa(int(number))
		item.ActorType = textToPtr(actorType)
		item.ActorID = uuidToPtr(actorID)
		if len(details) == 0 {
			details = []byte("{}")
		}
		item.Details = json.RawMessage(details)
		item.CreatedAt = timestampToString(createdAt)
		out = append(out, item)
	}
	return out, rows.Err()
}

// loadSprintBurndown builds remaining-issue counts per UTC day from the actual
// closing dates. An issue closes on its newest status change into its current
// terminal status; one without such a row (imported, or closed before activity
// was recorded) falls back to its last update. The scope is the sprint's
// current issue set: scope changes over time are not reconstructed.
func (h *Handler) loadSprintBurndown(ctx context.Context, sprint db.Sprint, total int64, today time.Time) (*PlanningBurndown, error) {
	start := sprint.StartDate.Time
	end := sprint.EndDate.Time
	if total == 0 || today.Before(start) {
		return nil, nil
	}
	rows, err := h.DB.Query(ctx, fmt.Sprintf(`
SELECT (closed_at AT TIME ZONE 'UTC')::date AS day, count(*)::bigint
FROM (
  SELECT COALESCE((
           SELECT max(al.created_at) FROM activity_log al
           WHERE al.issue_id = t.id AND al.action = 'status_changed' AND al.details->>'to' = t.status
         ), t.updated_at) AS closed_at
  FROM (
    SELECT i.id, i.status, i.updated_at, %s AS bucket
    FROM issue i
    WHERE i.workspace_id = $1 AND i.sprint_id = $2
  ) t
  WHERE t.bucket IN ('done', 'cancelled')
) c
GROUP BY day`, planningBucketExpr), sprint.WorkspaceID, sprint.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	closedByDay := map[string]int64{}
	var closedBeforeStart int64
	for rows.Next() {
		var day pgtype.Date
		var count int64
		if err := rows.Scan(&day, &count); err != nil {
			return nil, err
		}
		if !day.Valid {
			continue
		}
		if day.Time.Before(start) {
			closedBeforeStart += count
			continue
		}
		closedByDay[day.Time.Format("2006-01-02")] += count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	last := end
	if today.Before(last) {
		last = today
	}
	out := &PlanningBurndown{
		Total:     total,
		StartDate: start.Format("2006-01-02"),
		EndDate:   end.Format("2006-01-02"),
		Days:      []PlanningBurndownDay{},
	}
	remaining := total - closedBeforeStart
	for day, n := start, 0; !day.After(last) && n < planningBurndownMaxDays; day, n = day.AddDate(0, 0, 1), n+1 {
		key := day.Format("2006-01-02")
		remaining -= closedByDay[key]
		out.Days = append(out.Days, PlanningBurndownDay{Date: key, Remaining: remaining})
	}
	return out, nil
}

// loadPlanningProgressByIDs returns the status progress of every listed epic or
// sprint in one grouped query, for list pages.
func (h *Handler) loadPlanningProgressByIDs(ctx context.Context, rel planningRelation, wsID pgtype.UUID, ids []pgtype.UUID) (map[string]*PlanningProgress, error) {
	out := map[string]*PlanningProgress{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := h.DB.Query(ctx, fmt.Sprintf(`
SELECT rel_id, bucket, count(*)::bigint
FROM (
  SELECT i.%s AS rel_id, %s AS bucket
  FROM issue i
  WHERE i.workspace_id = $1 AND i.%s = ANY($2::uuid[])
) t
GROUP BY rel_id, bucket`, rel.column, planningBucketExpr, rel.column), wsID, ids)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var relID pgtype.UUID
		var bucket string
		var count int64
		if err := rows.Scan(&relID, &bucket, &count); err != nil {
			return out, err
		}
		key := uuidToString(relID)
		if out[key] == nil {
			out[key] = &PlanningProgress{}
		}
		out[key].add(bucket, count)
	}
	return out, rows.Err()
}

func writePlanningOverviewFailure(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("planning overview failed", append(logger.RequestAttrs(r), "error", err)...)
	writeError(w, http.StatusInternalServerError, "failed to load planning overview")
}

func (h *Handler) GetEpicOverview(w http.ResponseWriter, r *http.Request) {
	if !h.cfg.PlanningEnabled {
		writeError(w, http.StatusNotFound, "planning is disabled")
		return
	}
	idUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "epic id")
	if !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok {
		return
	}
	ctx := r.Context()
	epic, err := h.Queries.GetEpicInWorkspace(ctx, db.GetEpicInWorkspaceParams{ID: idUUID, WorkspaceID: wsUUID})
	if err != nil {
		writeError(w, http.StatusNotFound, "epic not found")
		return
	}
	agg, err := h.loadPlanningAggregates(ctx, planningEpicRelation, epic.WorkspaceID, epic.ID)
	if err != nil {
		writePlanningOverviewFailure(w, r, err)
		return
	}
	resp := EpicOverviewResponse{
		EpicID:         uuidToString(epic.ID),
		Progress:       agg.progress,
		LastActivityAt: agg.lastActivityAt,
		Assignees:      agg.assignees,
		Sprints:        []PlanningSprintBreakdown{},
		Attention:      []PlanningAttentionIssue{},
		Activity:       []PlanningActivityEntry{},
	}
	if agg.progress.Total == 0 {
		writeJSON(w, http.StatusOK, resp)
		return
	}

	if ids := planningOtherIDs(agg.others); len(ids) > 0 {
		rows, err := h.DB.Query(ctx, `
SELECT id, name, status, start_date, end_date FROM sprint
WHERE workspace_id = $1 AND id = ANY($2::uuid[])
ORDER BY start_date DESC, id`, epic.WorkspaceID, ids)
		if err != nil {
			writePlanningOverviewFailure(w, r, err)
			return
		}
		for rows.Next() {
			var sprintID pgtype.UUID
			var item PlanningSprintBreakdown
			var startDate, endDate pgtype.Date
			if err := rows.Scan(&sprintID, &item.Name, &item.Status, &startDate, &endDate); err != nil {
				rows.Close()
				writePlanningOverviewFailure(w, r, err)
				return
			}
			key := uuidToString(sprintID)
			item.SprintID = &key
			item.StartDate = startDate.Time.Format("2006-01-02")
			item.EndDate = endDate.Time.Format("2006-01-02")
			item.Total, item.Done = agg.others[key].total, agg.others[key].done
			resp.Sprints = append(resp.Sprints, item)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			writePlanningOverviewFailure(w, r, err)
			return
		}
	}
	if none, ok := agg.others[""]; ok {
		resp.Sprints = append(resp.Sprints, PlanningSprintBreakdown{Total: none.total, Done: none.done})
	}

	prefix := h.getIssuePrefix(ctx, epic.WorkspaceID)
	if agg.progress.Blocked+agg.progress.InReview > 0 {
		if resp.Attention, err = h.loadPlanningAttention(ctx, planningEpicRelation, epic.WorkspaceID, epic.ID, prefix); err != nil {
			writePlanningOverviewFailure(w, r, err)
			return
		}
	}
	if resp.Activity, err = h.loadPlanningActivity(ctx, planningEpicRelation, epic.WorkspaceID, epic.ID, prefix); err != nil {
		writePlanningOverviewFailure(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) GetSprintOverview(w http.ResponseWriter, r *http.Request) {
	if !h.cfg.PlanningEnabled {
		writeError(w, http.StatusNotFound, "planning is disabled")
		return
	}
	idUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "sprint id")
	if !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok {
		return
	}
	ctx := r.Context()
	sprint, err := h.Queries.GetSprintInWorkspace(ctx, db.GetSprintInWorkspaceParams{ID: idUUID, WorkspaceID: wsUUID})
	if err != nil {
		writeError(w, http.StatusNotFound, "sprint not found")
		return
	}
	agg, err := h.loadPlanningAggregates(ctx, planningSprintRelation, sprint.WorkspaceID, sprint.ID)
	if err != nil {
		writePlanningOverviewFailure(w, r, err)
		return
	}
	resp := SprintOverviewResponse{
		SprintID:       uuidToString(sprint.ID),
		Progress:       agg.progress,
		LastActivityAt: agg.lastActivityAt,
		Assignees:      agg.assignees,
		Epics:          []PlanningEpicBreakdown{},
		Attention:      []PlanningAttentionIssue{},
	}
	if agg.progress.Total == 0 {
		writeJSON(w, http.StatusOK, resp)
		return
	}

	if ids := planningOtherIDs(agg.others); len(ids) > 0 {
		rows, err := h.DB.Query(ctx, `
SELECT id, title, color, status FROM epic
WHERE workspace_id = $1 AND id = ANY($2::uuid[])
ORDER BY LOWER(title), id`, sprint.WorkspaceID, ids)
		if err != nil {
			writePlanningOverviewFailure(w, r, err)
			return
		}
		for rows.Next() {
			var epicID pgtype.UUID
			var item PlanningEpicBreakdown
			if err := rows.Scan(&epicID, &item.Title, &item.Color, &item.Status); err != nil {
				rows.Close()
				writePlanningOverviewFailure(w, r, err)
				return
			}
			key := uuidToString(epicID)
			item.EpicID = &key
			item.Total, item.Done = agg.others[key].total, agg.others[key].done
			resp.Epics = append(resp.Epics, item)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			writePlanningOverviewFailure(w, r, err)
			return
		}
	}
	if none, ok := agg.others[""]; ok {
		resp.Epics = append(resp.Epics, PlanningEpicBreakdown{Total: none.total, Done: none.done})
	}

	if agg.progress.Blocked+agg.progress.InReview > 0 {
		prefix := h.getIssuePrefix(ctx, sprint.WorkspaceID)
		if resp.Attention, err = h.loadPlanningAttention(ctx, planningSprintRelation, sprint.WorkspaceID, sprint.ID, prefix); err != nil {
			writePlanningOverviewFailure(w, r, err)
			return
		}
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	if resp.Burndown, err = h.loadSprintBurndown(ctx, sprint, agg.progress.Total, today); err != nil {
		writePlanningOverviewFailure(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

type CompleteSprintRequest struct {
	OpenIssues     string  `json:"open_issues"`
	TargetSprintID *string `json:"target_sprint_id"`
}

type CompleteSprintResponse struct {
	Sprint        SprintResponse  `json:"sprint"`
	OpenIssues    string          `json:"open_issues"`
	TargetSprint  *SprintResponse `json:"target_sprint"`
	MovedCount    int64           `json:"moved_count"`
	UnlinkedCount int64           `json:"unlinked_count"`
	KeptCount     int64           `json:"kept_count"`
}

const (
	sprintOpenIssuesKeep   = "keep"
	sprintOpenIssuesMove   = "move"
	sprintOpenIssuesUnlink = "unlink"
)

// CompleteSprint closes a sprint and applies the caller's explicit choice for
// its unfinished issues. It is the only path that moves or unlinks issues on
// completion: PUT status=completed never touches them. Finished issues always
// stay on the completed sprint.
func (h *Handler) CompleteSprint(w http.ResponseWriter, r *http.Request) {
	if !h.cfg.PlanningEnabled {
		writeError(w, http.StatusNotFound, "planning is disabled")
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	idUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "sprint id")
	if !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	var req CompleteSprintRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.OpenIssues != sprintOpenIssuesKeep && req.OpenIssues != sprintOpenIssuesMove && req.OpenIssues != sprintOpenIssuesUnlink {
		writeError(w, http.StatusBadRequest, "open_issues must be one of: keep, move, unlink")
		return
	}
	if req.TargetSprintID != nil && req.OpenIssues != sprintOpenIssuesMove {
		writeError(w, http.StatusBadRequest, "target_sprint_id is only valid with open_issues=move")
		return
	}
	var targetUUID pgtype.UUID
	if req.TargetSprintID != nil {
		if targetUUID, ok = parseUUIDOrBadRequest(w, *req.TargetSprintID, "target_sprint_id"); !ok {
			return
		}
	}

	ctx := r.Context()
	tx, err := h.beginWakeupWrite(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start sprint completion")
		return
	}
	defer tx.Rollback(ctx)
	qtx := h.Queries.WithTx(tx)
	if err := qtx.LockPlanningWorkspace(ctx, wsUUID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to lock planning update")
		return
	}
	sprint, err := qtx.GetSprintInWorkspace(ctx, db.GetSprintInWorkspaceParams{ID: idUUID, WorkspaceID: wsUUID})
	if err != nil {
		writeError(w, http.StatusNotFound, "sprint not found")
		return
	}
	if sprint.Status == "completed" || sprint.Status == "cancelled" {
		writeError(w, http.StatusConflict, "sprint is already "+sprint.Status)
		return
	}

	var target *db.Sprint
	if req.OpenIssues == sprintOpenIssuesMove {
		var found db.Sprint
		if targetUUID.Valid {
			if targetUUID == sprint.ID {
				writeError(w, http.StatusBadRequest, "target_sprint_id must be a different sprint")
				return
			}
			found, err = qtx.GetSprintInWorkspace(ctx, db.GetSprintInWorkspaceParams{ID: targetUUID, WorkspaceID: wsUUID})
			if err != nil {
				writeError(w, http.StatusBadRequest, "target sprint not found")
				return
			}
			if found.Status != "planned" && found.Status != "active" {
				writeError(w, http.StatusBadRequest, "target sprint must be planned or active")
				return
			}
		} else {
			// Default target: the next planned sprint, preferring one that
			// starts on or after this sprint.
			var nextID pgtype.UUID
			err = tx.QueryRow(ctx, `
SELECT id FROM sprint
WHERE workspace_id = $1 AND status = 'planned' AND id <> $2
ORDER BY (start_date >= $3) DESC, start_date ASC, created_at ASC, id
LIMIT 1`, wsUUID, sprint.ID, sprint.StartDate).Scan(&nextID)
			if errors.Is(err, pgx.ErrNoRows) {
				writeError(w, http.StatusConflict, "no planned sprint to move open issues to")
				return
			}
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to resolve target sprint")
				return
			}
			found, err = qtx.GetSprintInWorkspace(ctx, db.GetSprintInWorkspaceParams{ID: nextID, WorkspaceID: wsUUID})
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to resolve target sprint")
				return
			}
		}
		target = &found
	}

	resp := CompleteSprintResponse{OpenIssues: req.OpenIssues}
	openFilter := fmt.Sprintf("i.workspace_id = $1 AND i.sprint_id = $2 AND (%s) NOT IN ('done', 'cancelled')", planningBucketExpr)
	// Advance revisions so stale editors reload, as sprint deletion does.
	const touch = "revision = revision + 1, updated_at = now(), last_activity_at = GREATEST(COALESCE(last_activity_at, updated_at), now())"
	switch req.OpenIssues {
	case sprintOpenIssuesMove:
		tag, err := tx.Exec(ctx, "UPDATE issue i SET sprint_id = $3, "+touch+" WHERE "+openFilter, wsUUID, sprint.ID, target.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to move open issues")
			return
		}
		resp.MovedCount = tag.RowsAffected()
	case sprintOpenIssuesUnlink:
		tag, err := tx.Exec(ctx, "UPDATE issue i SET sprint_id = NULL, "+touch+" WHERE "+openFilter, wsUUID, sprint.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to unlink open issues")
			return
		}
		resp.UnlinkedCount = tag.RowsAffected()
	default:
		if err := tx.QueryRow(ctx, "SELECT count(*)::bigint FROM issue i WHERE "+openFilter, wsUUID, sprint.ID).Scan(&resp.KeptCount); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to count open issues")
			return
		}
	}

	completed, err := qtx.UpdateSprint(ctx, db.UpdateSprintParams{
		ID:          sprint.ID,
		WorkspaceID: sprint.WorkspaceID,
		Goal:        sprint.Goal,
		Status:      pgtype.Text{String: "completed", Valid: true},
	})
	if err != nil {
		slog.Error("sprint completion failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to complete sprint")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit sprint completion")
		return
	}

	resp.Sprint = sprintToResponse(completed)
	if resp.Sprint.IssueCount, resp.Sprint.DoneCount, err = h.loadSprintIssueStats(ctx, completed.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count planning issues")
		return
	}
	h.publish("sprint:updated", workspaceID, "member", userID, map[string]any{"sprint": resp.Sprint})
	if target != nil {
		targetResp := sprintToResponse(*target)
		if targetResp.IssueCount, targetResp.DoneCount, err = h.loadSprintIssueStats(ctx, target.ID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to count planning issues")
			return
		}
		resp.TargetSprint = &targetResp
		h.publish("sprint:updated", workspaceID, "member", userID, map[string]any{"sprint": targetResp})
	}
	writeJSON(w, http.StatusOK, resp)
}
