package handler

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/logger"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type SprintResponse struct {
	ID          string  `json:"id"`
	WorkspaceID string  `json:"workspace_id"`
	Name        string  `json:"name"`
	Goal        *string `json:"goal"`
	StartDate   string  `json:"start_date"`
	EndDate     string  `json:"end_date"`
	Status      string  `json:"status"`
	IssueCount  int64   `json:"issue_count"`
	DoneCount   int64   `json:"done_count"`
	// Progress is the per-status breakdown behind the list and detail bars.
	// IssueCount/DoneCount keep their original meaning for installed clients.
	Progress  PlanningProgress `json:"progress"`
	CreatedAt string           `json:"created_at"`
	UpdatedAt string           `json:"updated_at"`
}

func sprintToResponse(s db.Sprint) SprintResponse {
	return SprintResponse{
		ID:          uuidToString(s.ID),
		WorkspaceID: uuidToString(s.WorkspaceID),
		Name:        s.Name,
		Goal:        textToPtr(s.Goal),
		StartDate:   s.StartDate.Time.Format("2006-01-02"),
		EndDate:     s.EndDate.Time.Format("2006-01-02"),
		Status:      s.Status,
		CreatedAt:   timestampToString(s.CreatedAt),
		UpdatedAt:   timestampToString(s.UpdatedAt),
	}
}

func (h *Handler) loadSprintIssueStats(ctx context.Context, sprintID pgtype.UUID) (int64, int64, error) {
	stats, err := h.Queries.GetSprintIssueStats(ctx, []pgtype.UUID{sprintID})
	if err != nil {
		return 0, 0, err
	}
	if len(stats) == 0 {
		return 0, 0, nil
	}
	return stats[0].TotalCount, stats[0].DoneCount, nil
}

type CreateSprintRequest struct {
	Name      string  `json:"name"`
	Goal      *string `json:"goal"`
	StartDate string  `json:"start_date"`
	EndDate   string  `json:"end_date"`
}

type UpdateSprintRequest struct {
	Name      *string `json:"name"`
	Goal      *string `json:"goal"`
	StartDate *string `json:"start_date"`
	EndDate   *string `json:"end_date"`
	Status    *string `json:"status"`
}

var validSprintStatuses = []string{"planned", "active", "completed", "cancelled"}

func (h *Handler) ListSprints(w http.ResponseWriter, r *http.Request) {
	if !h.cfg.PlanningEnabled {
		writeError(w, http.StatusNotFound, "planning is disabled")
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	var statusFilter pgtype.Text
	if s := r.URL.Query().Get("status"); s != "" {
		if !validateProjectEnum(w, "status", s, validSprintStatuses) {
			return
		}
		statusFilter = pgtype.Text{String: s, Valid: true}
	}
	sprints, err := h.Queries.ListSprints(r.Context(), db.ListSprintsParams{
		WorkspaceID: wsUUID,
		Status:      statusFilter,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list sprints")
		return
	}

	// Batch-fetch issue stats for all sprints
	statsMap := make(map[string]db.GetSprintIssueStatsRow)
	if len(sprints) > 0 {
		sprintIDs := make([]pgtype.UUID, len(sprints))
		for i, s := range sprints {
			sprintIDs[i] = s.ID
		}
		stats, err := h.Queries.GetSprintIssueStats(r.Context(), sprintIDs)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to count planning issues")
			return
		}
		{
			for _, s := range stats {
				statsMap[uuidToString(s.SprintID)] = s
			}
		}
	}

	progress, err := h.loadPlanningProgressByIDs(r.Context(), planningSprintRelation, wsUUID, sprintIDsOf(sprints))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count planning issues")
		return
	}

	resp := make([]SprintResponse, len(sprints))
	for i, s := range sprints {
		resp[i] = sprintToResponse(s)
		if p, ok := progress[resp[i].ID]; ok {
			resp[i].Progress = *p
		}
		if st, ok := statsMap[resp[i].ID]; ok {
			resp[i].IssueCount = st.TotalCount
			resp[i].DoneCount = st.DoneCount
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"sprints": resp, "total": len(resp)})
}

func (h *Handler) GetSprint(w http.ResponseWriter, r *http.Request) {
	if !h.cfg.PlanningEnabled {
		writeError(w, http.StatusNotFound, "planning is disabled")
		return
	}
	id := chi.URLParam(r, "id")
	workspaceID := h.resolveWorkspaceID(r)
	idUUID, ok := parseUUIDOrBadRequest(w, id, "sprint id")
	if !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	sprint, err := h.Queries.GetSprintInWorkspace(r.Context(), db.GetSprintInWorkspaceParams{
		ID: idUUID, WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "sprint not found")
		return
	}
	resp := sprintToResponse(sprint)
	resp.IssueCount, resp.DoneCount, err = h.loadSprintIssueStats(r.Context(), sprint.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count planning issues")
		return
	}
	progress, err := h.loadPlanningProgressByIDs(r.Context(), planningSprintRelation, sprint.WorkspaceID, []pgtype.UUID{sprint.ID})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count planning issues")
		return
	}
	if p, ok := progress[resp.ID]; ok {
		resp.Progress = *p
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) CreateSprint(w http.ResponseWriter, r *http.Request) {
	if !h.cfg.PlanningEnabled {
		writeError(w, http.StatusNotFound, "planning is disabled")
		return
	}
	var req CreateSprintRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.StartDate == "" {
		writeError(w, http.StatusBadRequest, "start_date is required")
		return
	}
	if req.EndDate == "" {
		writeError(w, http.StatusBadRequest, "end_date is required")
		return
	}

	startDate, err := time.Parse("2006-01-02", req.StartDate)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid start_date format; use YYYY-MM-DD")
		return
	}
	endDate, err := time.Parse("2006-01-02", req.EndDate)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid end_date format; use YYYY-MM-DD")
		return
	}
	if !endDate.After(startDate) {
		writeError(w, http.StatusBadRequest, "end_date must be after start_date")
		return
	}

	workspaceID := h.resolveWorkspaceID(r)
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}

	sprint, err := h.Queries.CreateSprint(r.Context(), db.CreateSprintParams{
		WorkspaceID: wsUUID,
		Name:        req.Name,
		Goal:        ptrToText(req.Goal),
		StartDate:   pgtype.Date{Time: startDate, Valid: true},
		EndDate:     pgtype.Date{Time: endDate, Valid: true},
		Status:      "planned",
	})
	if err != nil {
		if isCheckViolation(err) {
			writeError(w, http.StatusBadRequest, "sprint create rejected: a field value failed a database constraint")
			return
		}
		slog.Error("sprint create failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to create sprint")
		return
	}
	resp := sprintToResponse(sprint)
	h.publish("sprint:created", workspaceID, "member", userID, map[string]any{"sprint": resp})
	writeJSON(w, http.StatusCreated, resp)
}

func (h *Handler) UpdateSprint(w http.ResponseWriter, r *http.Request) {
	if !h.cfg.PlanningEnabled {
		writeError(w, http.StatusNotFound, "planning is disabled")
		return
	}
	id := chi.URLParam(r, "id")
	workspaceID := h.resolveWorkspaceID(r)
	idUUID, ok := parseUUIDOrBadRequest(w, id, "sprint id")
	if !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	tx, err := h.beginWakeupWrite(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start planning update")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	if err := qtx.LockPlanningWorkspace(r.Context(), wsUUID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to lock planning update")
		return
	}
	prevSprint, err := qtx.GetSprintInWorkspace(r.Context(), db.GetSprintInWorkspaceParams{
		ID: idUUID, WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "sprint not found")
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	var req UpdateSprintRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var rawFields map[string]json.RawMessage
	json.Unmarshal(bodyBytes, &rawFields)

	params := db.UpdateSprintParams{
		ID:          prevSprint.ID,
		WorkspaceID: prevSprint.WorkspaceID,
		Goal:        prevSprint.Goal,
	}
	if req.Name != nil {
		if strings.TrimSpace(*req.Name) == "" {
			writeError(w, http.StatusBadRequest, "name is required")
			return
		}
		params.Name = pgtype.Text{String: *req.Name, Valid: true}
	}
	if req.Status != nil {
		if !validateProjectEnum(w, "status", *req.Status, validSprintStatuses) {
			return
		}
		params.Status = pgtype.Text{String: *req.Status, Valid: true}
	}
	if _, ok := rawFields["goal"]; ok {
		if req.Goal != nil {
			params.Goal = pgtype.Text{String: *req.Goal, Valid: true}
		} else {
			params.Goal = pgtype.Text{Valid: false}
		}
	}

	// Handle date updates with validation
	startDate := prevSprint.StartDate
	endDate := prevSprint.EndDate
	if req.StartDate != nil {
		t, err := time.Parse("2006-01-02", *req.StartDate)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid start_date format; use YYYY-MM-DD")
			return
		}
		startDate = pgtype.Date{Time: t, Valid: true}
	}
	if req.EndDate != nil {
		t, err := time.Parse("2006-01-02", *req.EndDate)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid end_date format; use YYYY-MM-DD")
			return
		}
		endDate = pgtype.Date{Time: t, Valid: true}
	}
	if !endDate.Time.After(startDate.Time) {
		writeError(w, http.StatusBadRequest, "end_date must be after start_date")
		return
	}
	params.StartDate = startDate
	params.EndDate = endDate

	sprint, err := qtx.UpdateSprint(r.Context(), params)
	if err != nil {
		if isCheckViolation(err) {
			writeError(w, http.StatusBadRequest, "sprint update rejected: a field value failed a database constraint")
			return
		}
		slog.Error("sprint update failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to update sprint")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit planning update")
		return
	}
	resp := sprintToResponse(sprint)
	resp.IssueCount, resp.DoneCount, err = h.loadSprintIssueStats(r.Context(), sprint.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count planning issues")
		return
	}
	h.publish("sprint:updated", workspaceID, "member", userID, map[string]any{"sprint": resp})
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) DeleteSprint(w http.ResponseWriter, r *http.Request) {
	if !h.cfg.PlanningEnabled {
		writeError(w, http.StatusNotFound, "planning is disabled")
		return
	}
	id := chi.URLParam(r, "id")
	workspaceID := h.resolveWorkspaceID(r)
	idUUID, ok := parseUUIDOrBadRequest(w, id, "sprint id")
	if !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	sprint, err := h.Queries.GetSprintInWorkspace(r.Context(), db.GetSprintInWorkspaceParams{
		ID: idUUID, WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "sprint not found")
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	tx, err := h.beginWakeupWrite(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin deletion")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	if err := qtx.LockPlanningWorkspace(r.Context(), sprint.WorkspaceID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to lock planning")
		return
	}
	// Unlink, never delete, issues. Advance revisions so stale editors reload.
	if _, err := tx.Exec(r.Context(), "UPDATE issue SET sprint_id = NULL, revision = revision + 1, updated_at = now(), last_activity_at = GREATEST(COALESCE(last_activity_at, updated_at), now()) WHERE workspace_id = $1 AND sprint_id = $2", sprint.WorkspaceID, sprint.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to unlink issues")
		return
	}
	if err := qtx.DeleteSprint(r.Context(), db.DeleteSprintParams{ID: sprint.ID, WorkspaceID: sprint.WorkspaceID}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete sprint")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit deletion")
		return
	}

	h.publish("sprint:deleted", workspaceID, "member", userID, map[string]any{"sprint_id": uuidToString(sprint.ID)})
	w.WriteHeader(http.StatusNoContent)
}

func sprintIDsOf(sprints []db.Sprint) []pgtype.UUID {
	ids := make([]pgtype.UUID, len(sprints))
	for i, s := range sprints {
		ids[i] = s.ID
	}
	return ids
}
