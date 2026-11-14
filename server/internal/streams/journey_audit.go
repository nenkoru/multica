package streams

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/journeyaudit"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// AuditBoard records an explicit first-party issue surface mount. API reads
// are recorded separately: a CLI read cannot manufacture a board opening.
func (h *HTTP) AuditBoard(w http.ResponseWriter, r *http.Request) {
	if !journeyaudit.Enabled() {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	ws, ok := h.workspace(w, r)
	if !ok {
		return
	}
	var req struct {
		SurfaceID string `json:"surface_id"`
		ProjectID string `json:"project_id"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	d.DisallowUnknownFields()
	if d.Decode(&req) != nil || d.Decode(&struct{}{}) != io.EOF {
		writeError(w, 400, "invalid audit event")
		return
	}
	if _, err := parseUUID(req.SurfaceID); err != nil {
		writeError(w, 400, "invalid surface id")
		return
	}
	if req.ProjectID != "" {
		project, err := parseUUID(req.ProjectID)
		if err != nil {
			writeError(w, 400, "invalid project id")
			return
		}
		var exists bool
		if err := h.Sup.Pool.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM project WHERE id=$1 AND workspace_id=$2)`, project, ws).Scan(&exists); err != nil || !exists {
			writeError(w, 404, "project not found")
			return
		}
	}
	s := journeyaudit.From(r.Context())
	if _, err := journeyaudit.Record(r.Context(), db.New(h.Sup.Pool), ws, journeyaudit.Entry{
		Stamp: s, Kind: "board_open", Outcome: "complete", ProjectID: req.ProjectID, SurfaceID: req.SurfaceID,
	}); err != nil {
		writeError(w, 503, "audit unavailable")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTP) AuditReport(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspace(w, r)
	if !ok {
		return
	}
	from, e1 := time.Parse(time.RFC3339, r.URL.Query().Get("from"))
	to, e2 := time.Parse(time.RFC3339, r.URL.Query().Get("to"))
	if e1 != nil || e2 != nil || !to.After(from) || to.Sub(from) > 31*24*time.Hour || to.After(time.Now().Add(time.Minute)) {
		writeError(w, 400, "exact window of at most 31 days required")
		return
	}
	user, err := parseUUID(r.Header.Get("X-User-ID"))
	if err != nil {
		writeError(w, 401, "user required")
		return
	}
	project := r.URL.Query().Get("project_id")
	if project != "" {
		pid, err := parseUUID(project)
		if err != nil {
			writeError(w, 400, "invalid project id")
			return
		}
		var exists bool
		if err = h.Sup.Pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM project WHERE id=$1 AND workspace_id=$2)`, pid, ws).Scan(&exists); err != nil || !exists {
			writeError(w, 404, "project not found")
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	out, err := journeyaudit.Report(ctx, h.Sup.Pool, ws, user, h.Sup.MultikaAgentID, project, from, to)
	if err != nil {
		writeError(w, 503, "audit report unavailable")
		return
	}
	writeJSON(w, 200, out)
}

// A daemon claim does not carry the incoming member credential. Recover ONLY
// the immutable input batch's recorded metadata, after the existing access gate.
func (s *Supervisor) auditChatContext(ctx context.Context, taskID, sessionID pgtype.UUID) context.Context {
	if !journeyaudit.Enabled() {
		return ctx
	}
	unknown := journeyaudit.Stamp{Source: "unknown", Route: "unknown", RouteEvidence: "unknown"}
	var missing bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM chat_message m JOIN agent_task_queue t ON t.id=$1 AND m.task_id=t.chat_input_task_id
 WHERE m.chat_session_id=$2 AND m.role='user' AND NOT EXISTS(SELECT 1 FROM activity_log a WHERE a.action=$3 AND a.details->>'message_id'=m.id::text))`, taskID, sessionID, journeyaudit.Action).Scan(&missing)
	if err != nil || missing {
		return journeyaudit.With(ctx, unknown)
	}
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT a.details->>'source',a.details->>'route',a.details->>'route_evidence',a.details->>'user_id',COALESCE(a.details->>'agent_id',''),COALESCE(a.details->>'task_id',''),COALESCE(a.details->>'via','')
 FROM activity_log a JOIN chat_message m ON m.id::text=a.details->>'message_id'
 JOIN agent_task_queue t ON t.id=$1 AND m.task_id=t.chat_input_task_id
 WHERE a.action=$2 AND m.chat_session_id=$3 AND a.workspace_id=(SELECT workspace_id FROM chat_session WHERE id=$3)`, taskID, journeyaudit.Action, sessionID)
	if err != nil {
		return journeyaudit.With(ctx, journeyaudit.Stamp{Source: "unknown", Route: "unknown", RouteEvidence: "unknown"})
	}
	defer rows.Close()
	var stamps []journeyaudit.Stamp
	for rows.Next() {
		var st journeyaudit.Stamp
		if rows.Scan(&st.Source, &st.Route, &st.RouteEvidence, &st.UserID, &st.AgentID, &st.TaskID, &st.Via) != nil {
			return ctx
		}
		stamps = append(stamps, st)
	}
	if rows.Err() != nil || len(stamps) != 1 {
		return journeyaudit.With(ctx, journeyaudit.Stamp{Source: "unknown", Route: "unknown", RouteEvidence: "unknown"})
	}
	return journeyaudit.With(ctx, stamps[0])
}
