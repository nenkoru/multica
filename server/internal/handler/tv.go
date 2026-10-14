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
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/logger"
)

// The TV dashboard is a read-only, single-screen summary of a workspace for a
// wall monitor. It is authenticated by a kiosk token (auth.KioskTokenPrefix)
// bound to one workspace; the token reaches only the aggregate below and the
// sanitized realtime feed, never issue descriptions, comments or chat.

const (
	tvEpicLimit         = 5
	tvEpicIssueLimit    = 3
	tvNeedsOwnerLimit   = 5
	tvFeedLimit         = 8
	tvStaleAfter        = 24 * time.Hour
	tvFeedWindow        = 24 * time.Hour
	tvDoneFallbackSpan  = 7 * 24 * time.Hour
	tvKioskTouchEvery   = 5 * time.Minute
	tvKioskCookieMaxAge = 365 * 24 * time.Hour
)

// tvSinceExpr is when the issue aliased `i` entered its current status: the
// newest status change into it, or its last update when none was recorded.
const tvSinceExpr = `COALESCE((
    SELECT max(al.created_at) FROM activity_log al
    WHERE al.issue_id = i.id AND al.action = 'status_changed' AND al.details->>'to' = i.status
  ), i.updated_at)`

// tvAssigneeJoin resolves the polymorphic assignee of the row aliased `x`.
const tvAssigneeJoin = `
LEFT JOIN agent a ON x.assignee_type = 'agent' AND a.id = x.assignee_id
LEFT JOIN "user" u ON x.assignee_type = 'member' AND u.id = x.assignee_id
LEFT JOIN squad sq ON x.assignee_type = 'squad' AND sq.id = x.assignee_id`

const tvAssigneeName = `COALESCE(a.name, u.name, sq.name)`

type TVSprint struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
	Total     int64  `json:"total"`
	Done      int64  `json:"done"`
}

type TVIssue struct {
	ID             string  `json:"id"`
	Identifier     string  `json:"identifier"`
	Title          string  `json:"title"`
	Status         string  `json:"status"`
	AssigneeType   *string `json:"assignee_type"`
	AssigneeName   *string `json:"assignee_name"`
	Since          string  `json:"since"`
	LastActivityAt string  `json:"last_activity_at"`
	Stale          bool    `json:"stale"`
}

type TVEpic struct {
	ID             string    `json:"id"`
	Title          string    `json:"title"`
	InProgress     int64     `json:"in_progress"`
	InReview       int64     `json:"in_review"`
	Blocked        int64     `json:"blocked"`
	DoneInWindow   int64     `json:"done_in_window"`
	LastActivityAt *string   `json:"last_activity_at"`
	Issues         []TVIssue `json:"issues"`
}

type TVEvent struct {
	Kind            string  `json:"kind"`
	At              string  `json:"at"`
	IssueIdentifier *string `json:"issue_identifier"`
	IssueTitle      *string `json:"issue_title"`
	ActorName       *string `json:"actor_name"`
	FailureReason   *string `json:"failure_reason"`
}

type TVOverviewResponse struct {
	GeneratedAt     string    `json:"generated_at"`
	WorkspaceName   string    `json:"workspace_name"`
	OwnerLabel      string    `json:"owner_label"`
	Sprint          *TVSprint `json:"sprint"`
	DoneWindowStart string    `json:"done_window_start"`
	AgentsWorking   int64     `json:"agents_working"`
	NeedsOwnerTotal int64     `json:"needs_owner_total"`
	StaleTotal      int64     `json:"stale_total"`
	NoEpicActive    int64     `json:"no_epic_active"`
	Epics           []TVEpic  `json:"epics"`
	EpicsHidden     int64     `json:"epics_hidden"`
	NeedsOwner      []TVIssue `json:"needs_owner"`
	Feed            []TVEvent `json:"feed"`
}

// KioskSession is what a valid kiosk token resolves to.
type KioskSession struct {
	TokenID     pgtype.UUID
	WorkspaceID pgtype.UUID
	OwnerLabel  string
}

var ErrKioskTokenInvalid = errors.New("invalid kiosk token")

// ResolveKioskToken validates a raw kiosk token and returns its session. It
// also records last use, at most once per tvKioskTouchEvery.
func (h *Handler) ResolveKioskToken(ctx context.Context, raw string) (KioskSession, error) {
	var s KioskSession
	if !strings.HasPrefix(raw, auth.KioskTokenPrefix) {
		return s, ErrKioskTokenInvalid
	}
	err := h.DB.QueryRow(ctx, `
SELECT id, workspace_id, owner_label FROM kiosk_token
WHERE token_hash = $1 AND revoked_at IS NULL`, auth.HashToken(raw)).Scan(&s.TokenID, &s.WorkspaceID, &s.OwnerLabel)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, ErrKioskTokenInvalid
	}
	if err != nil {
		return s, err
	}
	if _, err := h.DB.Exec(ctx, `
UPDATE kiosk_token SET last_used_at = now()
WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < now() - make_interval(secs => $2))`,
		s.TokenID, tvKioskTouchEvery.Seconds()); err != nil {
		slog.Warn("kiosk token: last_used_at update failed", "error", err)
	}
	return s, nil
}

// KioskTokenFromRequest reads a kiosk token from the bearer header or the
// kiosk cookie. Only kiosk-prefixed values are returned.
func KioskTokenFromRequest(r *http.Request) string {
	if header := r.Header.Get("Authorization"); strings.HasPrefix(header, "Bearer "+auth.KioskTokenPrefix) {
		return strings.TrimPrefix(header, "Bearer ")
	}
	if cookie, err := r.Cookie(auth.KioskCookieName); err == nil && strings.HasPrefix(cookie.Value, auth.KioskTokenPrefix) {
		return cookie.Value
	}
	return ""
}

func (h *Handler) requireKiosk(w http.ResponseWriter, r *http.Request) (KioskSession, bool) {
	raw := KioskTokenFromRequest(r)
	if raw == "" {
		writeError(w, http.StatusUnauthorized, "kiosk token required")
		return KioskSession{}, false
	}
	s, err := h.ResolveKioskToken(r.Context(), raw)
	if errors.Is(err, ErrKioskTokenInvalid) {
		writeError(w, http.StatusUnauthorized, "invalid kiosk token")
		return KioskSession{}, false
	}
	if err != nil {
		slog.Error("kiosk token lookup failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to check kiosk token")
		return KioskSession{}, false
	}
	return s, true
}

// ---------------------------------------------------------------------------
// Kiosk token management (workspace owners and admins)
// ---------------------------------------------------------------------------

type KioskTokenResponse struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	TokenPrefix string  `json:"token_prefix"`
	OwnerLabel  string  `json:"owner_label"`
	CreatedAt   string  `json:"created_at"`
	LastUsedAt  *string `json:"last_used_at"`
	RevokedAt   *string `json:"revoked_at"`
}

type CreateKioskTokenRequest struct {
	Name       string `json:"name"`
	OwnerLabel string `json:"owner_label"`
}

type CreateKioskTokenResponse struct {
	KioskTokenResponse
	Token string `json:"token"`
}

func (h *Handler) requireKioskAdmin(w http.ResponseWriter, r *http.Request) (pgtype.UUID, string, bool) {
	wsID := h.resolveWorkspaceID(r)
	member, ok := h.requireWorkspaceRole(w, r, wsID, "workspace not found", "owner", "admin")
	if !ok {
		return pgtype.UUID{}, "", false
	}
	return member.WorkspaceID, uuidToString(member.UserID), true
}

func scanKioskToken(row pgx.Row) (KioskTokenResponse, error) {
	var (
		out       KioskTokenResponse
		id        pgtype.UUID
		createdAt pgtype.Timestamptz
		lastUsed  pgtype.Timestamptz
		revoked   pgtype.Timestamptz
	)
	if err := row.Scan(&id, &out.Name, &out.TokenPrefix, &out.OwnerLabel, &createdAt, &lastUsed, &revoked); err != nil {
		return out, err
	}
	out.ID = uuidToString(id)
	out.CreatedAt = timestampToString(createdAt)
	out.LastUsedAt = timestampToPtr(lastUsed)
	out.RevokedAt = timestampToPtr(revoked)
	return out, nil
}

const kioskTokenColumns = `id, name, token_prefix, owner_label, created_at, last_used_at, revoked_at`

func (h *Handler) CreateKioskToken(w http.ResponseWriter, r *http.Request) {
	wsID, userID, ok := h.requireKioskAdmin(w, r)
	if !ok {
		return
	}
	var req CreateKioskTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.OwnerLabel = strings.TrimSpace(req.OwnerLabel)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	raw, err := auth.GenerateKioskToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}
	token, err := scanKioskToken(h.DB.QueryRow(r.Context(), `
INSERT INTO kiosk_token (workspace_id, name, token_hash, token_prefix, owner_label, created_by)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING `+kioskTokenColumns,
		wsID, req.Name, auth.HashToken(raw), raw[:12], req.OwnerLabel, parseUUID(userID)))
	if err != nil {
		slog.Error("create kiosk token failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to create token")
		return
	}
	writeJSON(w, http.StatusCreated, CreateKioskTokenResponse{KioskTokenResponse: token, Token: raw})
}

func (h *Handler) ListKioskTokens(w http.ResponseWriter, r *http.Request) {
	wsID, _, ok := h.requireKioskAdmin(w, r)
	if !ok {
		return
	}
	rows, err := h.DB.Query(r.Context(), `
SELECT `+kioskTokenColumns+` FROM kiosk_token
WHERE workspace_id = $1 ORDER BY created_at DESC, id`, wsID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list tokens")
		return
	}
	defer rows.Close()
	out := []KioskTokenResponse{}
	for rows.Next() {
		token, err := scanKioskToken(rows)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list tokens")
			return
		}
		out = append(out, token)
	}
	if rows.Err() != nil {
		writeError(w, http.StatusInternalServerError, "failed to list tokens")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) RevokeKioskToken(w http.ResponseWriter, r *http.Request) {
	wsID, _, ok := h.requireKioskAdmin(w, r)
	if !ok {
		return
	}
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "token id")
	if !ok {
		return
	}
	tag, err := h.DB.Exec(r.Context(), `
UPDATE kiosk_token SET revoked_at = COALESCE(revoked_at, now())
WHERE id = $1 AND workspace_id = $2`, id, wsID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to revoke token")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "token not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Kiosk session: one-time exchange of the token for an HttpOnly cookie
// ---------------------------------------------------------------------------

type CreateTVSessionRequest struct {
	Token string `json:"token"`
}

// CreateTVSession trades a kiosk token sent in the body for the kiosk cookie,
// so the token is never kept in a URL, browser history or access logs.
func (h *Handler) CreateTVSession(w http.ResponseWriter, r *http.Request) {
	var req CreateTVSessionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	token := strings.TrimSpace(req.Token)
	if _, err := h.ResolveKioskToken(r.Context(), token); err != nil {
		if errors.Is(err, ErrKioskTokenInvalid) {
			writeError(w, http.StatusUnauthorized, "invalid kiosk token")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to check kiosk token")
		return
	}
	auth.SetKioskCookie(w, token, tvKioskCookieMaxAge)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) DeleteTVSession(w http.ResponseWriter, r *http.Request) {
	auth.SetKioskCookie(w, "", -1)
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Aggregate
// ---------------------------------------------------------------------------

func (h *Handler) GetTVOverview(w http.ResponseWriter, r *http.Request) {
	session, ok := h.requireKiosk(w, r)
	if !ok {
		return
	}
	resp, err := h.loadTVOverview(r.Context(), session, time.Now())
	if err != nil {
		slog.Error("tv overview failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to load tv overview")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) loadTVOverview(ctx context.Context, s KioskSession, now time.Time) (TVOverviewResponse, error) {
	resp := TVOverviewResponse{
		GeneratedAt: now.UTC().Format(time.RFC3339),
		OwnerLabel:  s.OwnerLabel,
		Epics:       []TVEpic{},
		NeedsOwner:  []TVIssue{},
		Feed:        []TVEvent{},
	}
	ws, err := h.Queries.GetWorkspace(ctx, s.WorkspaceID)
	if err != nil {
		return resp, fmt.Errorf("workspace: %w", err)
	}
	resp.WorkspaceName = ws.Name
	prefix := ws.IssuePrefix

	if resp.Sprint, err = h.loadTVSprint(ctx, s.WorkspaceID); err != nil {
		return resp, fmt.Errorf("sprint: %w", err)
	}
	// "Done" in an epic row counts from the current sprint's first day, or
	// over the last week when no sprint is running.
	windowStart := now.Add(-tvDoneFallbackSpan)
	if resp.Sprint != nil {
		if start, err := time.Parse("2006-01-02", resp.Sprint.StartDate); err == nil {
			windowStart = start
		}
	}
	resp.DoneWindowStart = windowStart.UTC().Format(time.RFC3339)

	if err := h.loadTVCounters(ctx, s.WorkspaceID, now, &resp); err != nil {
		return resp, fmt.Errorf("counters: %w", err)
	}
	if err := h.loadTVEpics(ctx, s.WorkspaceID, prefix, windowStart, now, &resp); err != nil {
		return resp, fmt.Errorf("epics: %w", err)
	}
	if s.OwnerLabel != "" {
		if err := h.loadTVNeedsOwner(ctx, s.WorkspaceID, prefix, s.OwnerLabel, now, &resp); err != nil {
			return resp, fmt.Errorf("needs owner: %w", err)
		}
	}
	if resp.Feed, err = h.loadTVFeed(ctx, s.WorkspaceID, prefix, now); err != nil {
		return resp, fmt.Errorf("feed: %w", err)
	}
	return resp, nil
}

func (h *Handler) loadTVSprint(ctx context.Context, wsID pgtype.UUID) (*TVSprint, error) {
	var (
		id         pgtype.UUID
		out        TVSprint
		start, end pgtype.Date
	)
	err := h.DB.QueryRow(ctx, `
SELECT id, name, status, start_date, end_date FROM sprint
WHERE workspace_id = $1
  AND (status = 'active' OR (status = 'planned' AND start_date <= current_date AND end_date >= current_date))
ORDER BY (status = 'active') DESC, start_date DESC, id
LIMIT 1`, wsID).Scan(&id, &out.Name, &out.Status, &start, &end)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out.ID = uuidToString(id)
	out.StartDate = start.Time.Format("2006-01-02")
	out.EndDate = end.Time.Format("2006-01-02")
	progress, err := h.loadPlanningProgressByIDs(ctx, planningSprintRelation, wsID, []pgtype.UUID{id})
	if err != nil {
		return nil, err
	}
	if p := progress[out.ID]; p != nil {
		out.Total = p.Total
		out.Done = p.Done
	}
	return &out, nil
}

func (h *Handler) loadTVCounters(ctx context.Context, wsID pgtype.UUID, now time.Time, resp *TVOverviewResponse) error {
	return h.DB.QueryRow(ctx, fmt.Sprintf(`
SELECT
  (SELECT count(DISTINCT q.agent_id) FROM agent_task_queue q JOIN agent ag ON ag.id = q.agent_id
   WHERE ag.workspace_id = $1 AND q.status IN ('dispatched', 'running'))::bigint,
  (SELECT count(*) FROM issue i
   WHERE i.workspace_id = $1 AND i.parent_issue_id IS NULL
     AND %[1]s IN ('in_progress', 'in_review', 'blocked')
     AND COALESCE(i.last_activity_at, i.updated_at) < $2)::bigint,
  (SELECT count(*) FROM issue i
   WHERE i.workspace_id = $1 AND i.parent_issue_id IS NULL AND i.epic_id IS NULL
     AND %[1]s IN ('in_progress', 'in_review', 'blocked'))::bigint`, planningBucketExpr),
		wsID, now.Add(-tvStaleAfter)).Scan(&resp.AgentsWorking, &resp.StaleTotal, &resp.NoEpicActive)
}

// loadTVEpics lists open epics with work in flight or activity in the last
// day, busiest first. Only top-level issues count: sub-issues are shown
// through their parent.
func (h *Handler) loadTVEpics(ctx context.Context, wsID pgtype.UUID, prefix string, windowStart, now time.Time, resp *TVOverviewResponse) error {
	rows, err := h.DB.Query(ctx, fmt.Sprintf(`
SELECT e.id, e.title,
  count(*) FILTER (WHERE t.bucket = 'in_progress')::bigint,
  count(*) FILTER (WHERE t.bucket = 'in_review')::bigint,
  count(*) FILTER (WHERE t.bucket = 'blocked')::bigint,
  count(*) FILTER (WHERE t.bucket = 'done' AND t.closed_at >= $2)::bigint,
  max(t.activity_at)
FROM epic e
JOIN (
  SELECT i.epic_id, %[1]s AS bucket, COALESCE(i.last_activity_at, i.updated_at) AS activity_at,
    CASE WHEN i.updated_at >= $2 THEN %[2]s END AS closed_at
  FROM issue i
  WHERE i.workspace_id = $1 AND i.parent_issue_id IS NULL AND i.epic_id IS NOT NULL
) t ON t.epic_id = e.id
WHERE e.workspace_id = $1 AND e.status = 'open'
GROUP BY e.id, e.title`, planningBucketExpr, tvSinceExpr), wsID, windowStart)
	if err != nil {
		return err
	}
	defer rows.Close()
	type epicRow struct {
		epic TVEpic
		last time.Time
	}
	var all []epicRow
	for rows.Next() {
		var (
			id   pgtype.UUID
			row  epicRow
			last pgtype.Timestamptz
		)
		if err := rows.Scan(&id, &row.epic.Title, &row.epic.InProgress, &row.epic.InReview, &row.epic.Blocked, &row.epic.DoneInWindow, &last); err != nil {
			return err
		}
		row.epic.ID = uuidToString(id)
		row.epic.Issues = []TVIssue{}
		if last.Valid {
			row.last = last.Time
			row.epic.LastActivityAt = timestampToPtr(last)
		}
		open := row.epic.InProgress + row.epic.InReview + row.epic.Blocked
		if open == 0 && now.Sub(row.last) > tvStaleAfter {
			continue
		}
		all = append(all, row)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	sort.SliceStable(all, func(a, b int) bool {
		if !all[a].last.Equal(all[b].last) {
			return all[a].last.After(all[b].last)
		}
		return all[a].epic.ID < all[b].epic.ID
	})
	if len(all) > tvEpicLimit {
		resp.EpicsHidden = int64(len(all) - tvEpicLimit)
		all = all[:tvEpicLimit]
	}
	if len(all) == 0 {
		return nil
	}

	ids := make([]pgtype.UUID, len(all))
	index := map[string]int{}
	for n, row := range all {
		ids[n] = parseUUID(row.epic.ID)
		index[row.epic.ID] = n
	}
	issueRows, err := h.DB.Query(ctx, fmt.Sprintf(`
SELECT x.epic_id, x.id, x.number, x.title, x.status, x.assignee_type, %[3]s, x.since, x.activity_at
FROM (
  SELECT i.epic_id, i.id, i.number, i.title, i.status, i.assignee_type, i.assignee_id,
    %[2]s AS since, COALESCE(i.last_activity_at, i.updated_at) AS activity_at,
    row_number() OVER (PARTITION BY i.epic_id ORDER BY COALESCE(i.last_activity_at, i.updated_at) DESC, i.id) AS rn
  FROM issue i
  WHERE i.workspace_id = $1 AND i.parent_issue_id IS NULL AND i.epic_id = ANY($2::uuid[])
    AND %[1]s = 'in_progress'
) x %[4]s
WHERE x.rn <= %[5]d
ORDER BY x.epic_id, x.rn`, planningBucketExpr, tvSinceExpr, tvAssigneeName, tvAssigneeJoin, tvEpicIssueLimit), wsID, ids)
	if err != nil {
		return err
	}
	defer issueRows.Close()
	for issueRows.Next() {
		var epicID pgtype.UUID
		issue, err := scanTVIssue(issueRows, prefix, now, &epicID)
		if err != nil {
			return err
		}
		if n, ok := index[uuidToString(epicID)]; ok {
			all[n].epic.Issues = append(all[n].epic.Issues, issue)
		}
	}
	if err := issueRows.Err(); err != nil {
		return err
	}
	for _, row := range all {
		resp.Epics = append(resp.Epics, row.epic)
	}
	return nil
}

// scanTVIssue reads (id, number, title, status, assignee_type, assignee_name,
// since, activity_at), optionally preceded by one extra leading column.
func scanTVIssue(row pgx.Row, prefix string, now time.Time, lead any) (TVIssue, error) {
	var (
		out          TVIssue
		id           pgtype.UUID
		number       int32
		assigneeType pgtype.Text
		assigneeName pgtype.Text
		since        pgtype.Timestamptz
		activity     pgtype.Timestamptz
	)
	dest := []any{&id, &number, &out.Title, &out.Status, &assigneeType, &assigneeName, &since, &activity}
	if lead != nil {
		dest = append([]any{lead}, dest...)
	}
	if err := row.Scan(dest...); err != nil {
		return out, err
	}
	out.ID = uuidToString(id)
	out.Identifier = prefix + "-" + strconv.Itoa(int(number))
	out.AssigneeType = textToPtr(assigneeType)
	out.AssigneeName = textToPtr(assigneeName)
	out.Since = timestampToString(since)
	out.LastActivityAt = timestampToString(activity)
	out.Stale = activity.Valid && now.Sub(activity.Time) > tvStaleAfter
	return out, nil
}

// loadTVNeedsOwner lists blocked issues carrying the owner label, oldest
// block first. Sub-issues count here: the label is an explicit request.
func (h *Handler) loadTVNeedsOwner(ctx context.Context, wsID pgtype.UUID, prefix, label string, now time.Time, resp *TVOverviewResponse) error {
	rows, err := h.DB.Query(ctx, fmt.Sprintf(`
SELECT x.total, x.id, x.number, x.title, x.status, x.assignee_type, %[3]s, x.since, x.activity_at
FROM (
  SELECT i.id, i.number, i.title, i.status, i.assignee_type, i.assignee_id,
    %[2]s AS since, COALESCE(i.last_activity_at, i.updated_at) AS activity_at,
    count(*) OVER () AS total
  FROM issue i
  WHERE i.workspace_id = $1 AND %[1]s = 'blocked'
    AND EXISTS (
      SELECT 1 FROM issue_to_label il JOIN issue_label l ON l.id = il.label_id
      WHERE il.issue_id = i.id AND l.workspace_id = $1 AND l.resource_type = 'issue' AND lower(l.name) = lower($2)
    )
) x %[4]s
ORDER BY x.since ASC, x.id
LIMIT %[5]d`, planningBucketExpr, tvSinceExpr, tvAssigneeName, tvAssigneeJoin, tvNeedsOwnerLimit), wsID, label)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var total int64
		issue, err := scanTVIssue(rows, prefix, now, &total)
		if err != nil {
			return err
		}
		resp.NeedsOwnerTotal = total
		resp.NeedsOwner = append(resp.NeedsOwner, issue)
	}
	return rows.Err()
}

// loadTVFeed merges top-level issues closed as done and failed agent runs in
// the last day, newest first. Run errors stay server-side; only the coarse
// failure reason is shown.
func (h *Handler) loadTVFeed(ctx context.Context, wsID pgtype.UUID, prefix string, now time.Time) ([]TVEvent, error) {
	since := now.Add(-tvFeedWindow)
	rows, err := h.DB.Query(ctx, fmt.Sprintf(`
SELECT kind, at, number, title, actor, reason FROM (
  SELECT 'done' AS kind, x.closed_at AS at, x.number, x.title, %[3]s AS actor, NULL::text AS reason
  FROM (
    SELECT i.id, i.number, i.title, i.assignee_type, i.assignee_id, %[2]s AS closed_at
    FROM issue i
    WHERE i.workspace_id = $1 AND i.parent_issue_id IS NULL AND i.updated_at >= $2 AND %[1]s = 'done'
  ) x %[4]s
  WHERE x.closed_at >= $2
  UNION ALL
  SELECT 'run_failed', q.completed_at, i.number, i.title, ag.name, q.failure_reason
  FROM agent_task_queue q
  JOIN agent ag ON ag.id = q.agent_id
  LEFT JOIN issue i ON i.id = q.issue_id
  WHERE ag.workspace_id = $1 AND q.status = 'failed' AND q.completed_at >= $2
) feed
ORDER BY at DESC
LIMIT %[5]d`, planningBucketExpr, tvSinceExpr, tvAssigneeName, tvAssigneeJoin, tvFeedLimit), wsID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TVEvent{}
	for rows.Next() {
		var (
			ev     TVEvent
			at     pgtype.Timestamptz
			number pgtype.Int4
			title  pgtype.Text
			actor  pgtype.Text
			reason pgtype.Text
		)
		if err := rows.Scan(&ev.Kind, &at, &number, &title, &actor, &reason); err != nil {
			return nil, err
		}
		ev.At = timestampToString(at)
		if number.Valid {
			identifier := prefix + "-" + strconv.Itoa(int(number.Int32))
			ev.IssueIdentifier = &identifier
		}
		ev.IssueTitle = textToPtr(title)
		ev.ActorName = textToPtr(actor)
		ev.FailureReason = textToPtr(reason)
		out = append(out, ev)
	}
	return out, rows.Err()
}
