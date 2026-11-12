package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestPlanningPrivateProjectProjection(t *testing.T) {
	f := newPrivateProjectFixture(t)
	epic := planningTestEpic(t, "Private projection epic")
	sprint := planningTestSprint(t, "Private projection sprint", "2026-11-01", "2026-11-15", "active")
	for _, id := range []string{f.secretIssue, f.publicIssue} {
		dbfx.Exec(t, `UPDATE issue SET epic_id = $1, sprint_id = $2, status = 'blocked' WHERE id = $3`, epic, sprint, id)
		planningStatusChange(t, id, "blocked", time.Now().UTC())
	}
	for _, tc := range []struct {
		user string
		want int64
	}{{f.outsider, 1}, {f.insider, 2}, {testUserID, 2}} {
		var got EpicOverviewResponse
		req := withURLParam(asUser(newRequest("GET", "/api/epics/"+epic+"/overview", nil), tc.user), "id", epic)
		testutil.Call(t, testHandler.GetEpicOverview, req).Want(200).JSON(&got)
		if got.Progress.Total != tc.want || int64(len(got.Attention)) != tc.want || int64(len(got.Activity)) != tc.want {
			t.Fatalf("user %s: projection %+v", tc.user, got)
		}
		if tc.want == 1 && got.Attention[0].ID != f.publicIssue {
			t.Fatal("hidden issue reached planning attention")
		}
		var detail EpicResponse
		testutil.Call(t, testHandler.GetEpic, withURLParam(asUser(newRequest("GET", "/api/epics/"+epic, nil), tc.user), "id", epic)).Want(200).JSON(&detail)
		if detail.IssueCount != tc.want || detail.Progress.Total != tc.want {
			t.Fatalf("unfiltered epic counts: %+v", detail)
		}
		var sg SprintOverviewResponse
		testutil.Call(t, testHandler.GetSprintOverview, withURLParam(asUser(newRequest("GET", "/api/sprints/"+sprint+"/overview", nil), tc.user), "id", sprint)).Want(200).JSON(&sg)
		if sg.Progress.Total != tc.want {
			t.Fatalf("unfiltered sprint counts: %+v", sg)
		}
	}
	// A caller who sees only part of a relation must not bulk-edit hidden issues.
	req := withURLParam(asUser(newRequest("POST", "/api/sprints/"+sprint+"/complete", map[string]any{"open_issues": "unlink"}), f.outsider), "id", sprint)
	testutil.Call(t, testHandler.CompleteSprint, req).Want(http.StatusForbidden)
	if n := dbfx.Count(t, `SELECT count(*) FROM issue WHERE id = $1 AND sprint_id = $2`, f.secretIssue, sprint); n != 1 {
		t.Fatal("denied completion changed a hidden issue")
	}
}

func TestFabrikaMixedProjectGuard(t *testing.T) {
	f := newPrivateProjectFixture(t)
	called := false
	h := testHandler.RequireFabrikaProjectAccess(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, tc := range []struct{ user string; want int }{{f.outsider, 404}, {f.insider, 204}, {testUserID, 204}} {
		called = false
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, asUser(newRequest("GET", "/api/streams", nil), tc.user))
		if rec.Code != tc.want || called != (tc.want == 204) {
			t.Fatalf("status %d, called %v, want %d", rec.Code, called, tc.want)
		}
	}
}

func TestKioskPrivateWorkspaceFailsClosed(t *testing.T) {
	newPrivateProjectFixture(t)
	var token CreateKioskTokenResponse
	testutil.Call(t, testHandler.CreateKioskToken, newRequest("POST", "/api/kiosk-tokens", map[string]any{"name": "private guard"})).Want(201).JSON(&token)
	dbfx.Cleanup(t, `DELETE FROM kiosk_token WHERE id = $1`, token.ID)
	for _, get := range []http.HandlerFunc{testHandler.GetTVOverview, testHandler.GetTVGame, testHandler.GetTVHealth} {
		testutil.Call(t, get, tvRequest("GET", "/api/tv/overview", token.Token, nil)).Want(404)
	}
}
