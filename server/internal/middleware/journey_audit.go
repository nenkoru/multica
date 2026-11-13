package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/journeyaudit"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func authenticatedAuditRequest(r *http.Request, token string, cookie bool) *http.Request {
	kind, hash := "session", ""
	switch {
	case strings.HasPrefix(token, "mat_"):
		kind = "task_token"
	case strings.HasPrefix(token, auth.CloudPATPrefix):
		kind = "cloud_pat"
	case strings.HasPrefix(token, "mul_"):
		kind, hash = "pat", auth.HashToken(token)
	}
	s := journeyaudit.HTTPStamp(r.Header.Get("X-User-ID"), r.Header.Get("X-Agent-ID"), r.Header.Get("X-Task-ID"), kind, hash, r.Header.Get(HeaderClientPlatform), cookie)
	return r.WithContext(journeyaudit.With(r.Context(), s))
}

// JourneyAudit observes authorized issue API requests, not page openings.
// Pending is durable before execution; a failed completion stays unknown.
// It runs after workspace membership and does not replace any handler gate.
func JourneyAudit(pool *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			kind := journeyRequestKind(r.Method, r.URL.Path)
			if !journeyaudit.Enabled() || kind == "" {
				next.ServeHTTP(w, r)
				return
			}
			var ws pgtype.UUID
			if ws.Scan(WorkspaceIDFromContext(r.Context())) != nil {
				http.Error(w, "audit workspace unavailable", 503)
				return
			}
			e := journeyaudit.Entry{Stamp: journeyaudit.From(r.Context()), Kind: kind, Outcome: "pending", Method: r.Method}
			id, err := journeyaudit.Record(r.Context(), db.New(pool), ws, e)
			if err != nil {
				http.Error(w, "audit unavailable", 503)
				return
			}
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			e.Outcome = "rejected"
			if ww.Status() < 400 {
				e.Outcome = "complete"
			}
			raw, _ := json.Marshal(e)
			ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Second)
			defer cancel()
			// No retry: the existing pending row preserves a crash/write failure.
			_, _ = pool.Exec(ctx, `UPDATE activity_log SET details=$2 WHERE id=$1 AND action=$3`, id, raw, journeyaudit.Action)
		})
	}
}

func journeyRequestKind(method, path string) string {
	path = strings.TrimSuffix(path, "/")
	if !(path == "/api/issues" || strings.HasPrefix(path, "/api/issues/") || strings.HasPrefix(path, "/api/comments/")) {
		return ""
	}
	if method == http.MethodPost {
		// These server routes read/preview without changing an issue. HTTP
		// method alone cannot classify their semantics (oversized filters
		// use POST rather than GET). Read receipts are emitted by readers.
		switch path {
		case "/api/issues/query", "/api/issues/table/groups", "/api/issues/table/rows", "/api/issues/table/facets", "/api/issues/preview-trigger":
			return "issue_read"
		}
		parts := strings.Split(strings.TrimPrefix(path, "/api/issues/"), "/")
		if len(parts) == 3 && parts[1] == "comments" && parts[2] == "trigger-preview" {
			return "issue_read"
		}
		if len(parts) == 2 && parts[1] == "read" {
			return "issue_read"
		}
		if len(parts) == 4 && parts[1] == "quick-actions" && parts[3] == "render" {
			return "issue_read"
		}
	}
	switch method {
	case http.MethodGet:
		return "issue_read"
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return "issue_action"
	}
	return ""
}
