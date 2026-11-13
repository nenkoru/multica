package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/journeyaudit"
)

func TestJourneyAuthStripsForgedActorAndSource(t *testing.T) {
	token := generateToken(validClaims(), auth.JWTSecret())
	for _, cookie := range []bool{false, true} {
		r := httptest.NewRequest("GET", "/api/issues", nil)
		if cookie {
			r.AddCookie(&http.Cookie{Name: "multica_auth", Value: token})
		} else {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		r.Header.Set("X-Agent-ID", "forged")
		r.Header.Set("X-Task-ID", "forged")
		r.Header.Set("X-Actor-Source", "task_token")
		r.Header.Set("X-Client-Platform", "cli")
		called := false
		h := authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			s := journeyaudit.From(r.Context())
			expected := "unknown"
			if cookie {
				expected = "human"
			}
			if s.Source != expected || s.AgentID != "" || s.TaskID != "" {
				t.Fatalf("forged source admitted %+v", s)
			}
			if r.Header.Get("X-Actor-Source") != "" {
				t.Fatal("actor header retained")
			}
		}))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if !called {
			t.Fatalf("auth rejected %d %s", w.Code, w.Body.String())
		}
	}
}

func TestJourneyReadsAreNotBoardOpens(t *testing.T) {
	for _, tc := range []struct{ method, path, kind string }{
		{"GET", "/api/issues", "issue_read"}, {"GET", "/api/issues/test", "issue_read"},
		{"POST", "/api/issues/query", "issue_read"},
		{"POST", "/api/issues/query/", "issue_read"},
		{"POST", "/api/issues/table/groups", "issue_read"},
		{"POST", "/api/issues/table/rows", "issue_read"},
		{"POST", "/api/issues/table/facets", "issue_read"},
		{"POST", "/api/issues/preview-trigger", "issue_read"},
		{"POST", "/api/issues/test/comments/trigger-preview", "issue_read"},
		{"POST", "/api/issues/test/read", "issue_read"},
		{"POST", "/api/issues/test/quick-actions/action/render", "issue_read"},
		{"POST", "/api/issues/test/quick-actions/action/run", "issue_action"},
		{"POST", "/api/issues/test/quick-actions/action/render/extra", "issue_action"},
		{"POST", "/api/issues", "issue_action"},
		{"POST", "/api/issues/quick-create", "issue_action"},
		{"POST", "/api/issues/batch-update", "issue_action"},
		{"POST", "/api/issues/test/comments/trigger-preview/extra", "issue_action"},
		{"POST", "/api/issues/test/comments", "issue_action"}, {"PATCH", "/api/issues/test", "issue_action"},
		{"GET", "/api/streams/audit/report", ""}, {"GET", "/api/health", ""},
	} {
		if got := journeyRequestKind(tc.method, tc.path); got != tc.kind {
			t.Fatalf("%+v got %s", tc, got)
		}
	}
}
