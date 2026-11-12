package handler

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"
)

type planningHiddenKey struct{}

func planningHidden(ctx context.Context) []pgtype.UUID {
	ids, _ := ctx.Value(planningHiddenKey{}).([]pgtype.UUID)
	return ids
}

// Planning relations are workspace-wide, but their issue counts, activity and
// burndown must obey the same project principals as the ordinary issue list.
func (h *Handler) planningRequest(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	hidden, ok := h.requestHiddenProjects(w, r, h.resolveWorkspaceID(r))
	if !ok {
		return r, false
	}
	// Deleting a relation or completing a sprint changes linked issues in bulk.
	// Until those operations can address a partial relation, require visibility
	// of the complete workspace rather than changing issues the caller cannot see.
	if !hidden.Empty() && r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusForbidden, "planning changes require access to all workspace projects")
		return r, false
	}
	return r.WithContext(context.WithValue(r.Context(), planningHiddenKey{}, hidden.Param())), true
}

// Fabrika summaries, journal entries and generated screens combine projects.
// A partial view cannot safely be reconstructed from their stored prose.
func (h *Handler) RequireFabrikaProjectAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hidden, ok := h.requestHiddenProjects(w, r, h.resolveWorkspaceID(r))
		if !ok {
			return
		}
		if !hidden.Empty() {
			writeError(w, http.StatusNotFound, "streams not found")
			return
		}
		next.ServeHTTP(w, r)
	})
}
