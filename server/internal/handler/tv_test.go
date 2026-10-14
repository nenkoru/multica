package handler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func tvRequest(method, path, token string, body any) *http.Request {
	req := newRequest(method, path, body)
	// A kiosk request carries no member identity.
	req.Header.Del("X-User-ID")
	req.Header.Del("X-Workspace-ID")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req
}

func TestKioskTokenLifecycle(t *testing.T) {
	var created CreateKioskTokenResponse
	testutil.Call(t, testHandler.CreateKioskToken, newRequest("POST", "/api/kiosk-tokens", map[string]string{
		"name": "Living room TV", "owner_label": "Needs owner",
	})).Want(http.StatusCreated).JSON(&created)
	dbfx.Cleanup(t, `DELETE FROM kiosk_token WHERE id = $1`, created.ID)
	if !strings.HasPrefix(created.Token, auth.KioskTokenPrefix) || created.TokenPrefix != created.Token[:12] || created.OwnerLabel != "Needs owner" {
		t.Fatalf("created = %+v", created)
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM kiosk_token WHERE id = $1 AND token_hash = $2`, created.ID, auth.HashToken(created.Token)); n != 1 {
		t.Fatal("only the token hash is stored")
	}

	var listed []KioskTokenResponse
	testutil.Call(t, testHandler.ListKioskTokens, newRequest("GET", "/api/kiosk-tokens", nil)).Want(http.StatusOK).JSON(&listed)
	found := false
	for _, token := range listed {
		found = found || token.ID == created.ID
	}
	if !found {
		t.Fatalf("listed tokens %+v miss %s", listed, created.ID)
	}

	var overview TVOverviewResponse
	testutil.Call(t, testHandler.GetTVOverview, tvRequest("GET", "/api/tv/overview", created.Token, nil)).Want(http.StatusOK).JSON(&overview)
	if overview.WorkspaceName != "Handler Tests" || overview.OwnerLabel != "Needs owner" {
		t.Fatalf("overview header = %q / %q", overview.WorkspaceName, overview.OwnerLabel)
	}

	// The one-time exchange answers with an HttpOnly cookie, and the cookie
	// alone authenticates the aggregate.
	rec := httptest.NewRecorder()
	testHandler.CreateTVSession(rec, tvRequest("POST", "/api/tv/session", "", map[string]string{"token": created.Token}))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("session exchange = %d %s", rec.Code, rec.Body.String())
	}
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.KioskCookieName {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly || cookie.Value != created.Token || cookie.Path != auth.KioskCookiePath {
		t.Fatalf("kiosk cookie = %+v", cookie)
	}
	byCookie := tvRequest("GET", "/api/tv/overview", "", nil)
	byCookie.AddCookie(cookie)
	testutil.Call(t, testHandler.GetTVOverview, byCookie).Want(http.StatusOK)

	testutil.Call(t, testHandler.GetTVOverview, tvRequest("GET", "/api/tv/overview", "", nil)).Want(http.StatusUnauthorized)
	testutil.Call(t, testHandler.GetTVOverview, tvRequest("GET", "/api/tv/overview", "mul_not_a_kiosk_token", nil)).Want(http.StatusUnauthorized)
	testutil.Call(t, testHandler.CreateTVSession, tvRequest("POST", "/api/tv/session", "", map[string]string{"token": auth.KioskTokenPrefix + "wrong"})).Want(http.StatusUnauthorized)

	testutil.Call(t, testHandler.RevokeKioskToken, withURLParam(newRequest("DELETE", "/api/kiosk-tokens/"+created.ID, nil), "id", created.ID)).Want(http.StatusNoContent)
	testutil.Call(t, testHandler.GetTVOverview, tvRequest("GET", "/api/tv/overview", created.Token, nil)).Want(http.StatusUnauthorized)
	testutil.Call(t, testHandler.CreateTVSession, tvRequest("POST", "/api/tv/session", "", map[string]string{"token": created.Token})).Want(http.StatusUnauthorized)
}

func TestKioskTokenManagementNeedsAdmin(t *testing.T) {
	user := dbfx.User(t, "TV plain member", fmt.Sprintf("tv-member-%d@multica.test", time.Now().UnixNano()))
	dbfx.Member(t, testWorkspaceID, user, "member")
	req := newRequest("POST", "/api/kiosk-tokens", map[string]string{"name": "nope"})
	req.Header.Set("X-User-ID", user)
	testutil.Call(t, testHandler.CreateKioskToken, req).WantOneOf(http.StatusForbidden, http.StatusNotFound)
	if n := dbfx.Count(t, `SELECT count(*) FROM kiosk_token WHERE name = 'nope'`); n != 0 {
		t.Fatal("a plain member must not mint kiosk tokens")
	}
}

func TestTVOverviewAggregates(t *testing.T) {
	now := time.Now().UTC()
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	ws := dbfx.Workspace(t, "TV aggregate", fmt.Sprintf("tv-agg-%d", now.UnixNano()), testutil.Cols{"issue_prefix": "TVA"})
	fx := testutil.New(testPool, ws, testUserID)

	raw := auth.KioskTokenPrefix + "aggregate0123456789"
	fx.Insert(t, "kiosk_token", testutil.Cols{"workspace_id": ws, "name": "tv", "token_hash": auth.HashToken(raw), "token_prefix": raw[:12], "owner_label": "Needs owner"})

	sprintStart := now.AddDate(0, 0, -2).Format("2006-01-02")
	fx.Insert(t, "sprint", testutil.Cols{"workspace_id": ws, "name": "Sprint 2", "start_date": sprintStart, "end_date": now.AddDate(0, 0, 3).Format("2006-01-02"), "status": "active"})
	fx.Insert(t, "sprint", testutil.Cols{"workspace_id": ws, "name": "Later", "start_date": now.AddDate(0, 0, 10).Format("2006-01-02"), "end_date": now.AddDate(0, 0, 20).Format("2006-01-02"), "status": "planned"})

	busy := fx.Insert(t, "epic", testutil.Cols{"workspace_id": ws, "title": "Busy epic"})
	quiet := fx.Insert(t, "epic", testutil.Cols{"workspace_id": ws, "title": "Quiet epic"})
	closed := fx.Insert(t, "epic", testutil.Cols{"workspace_id": ws, "title": "Closed epic", "status": "closed"})

	runtime := fx.Runtime(t, "TV runtime")
	runtimeAgent := fx.Agent(t, "TV agent", runtime)
	issue := func(title, status string, activity time.Time, over testutil.Cols) string {
		cols := testutil.Cols{"status": status, "last_activity_at": activity, "updated_at": activity}
		for k, v := range over {
			cols[k] = v
		}
		return fx.Issue(t, title, cols)
	}
	statusChange := func(issueID, to string, at time.Time) {
		fx.Insert(t, "activity_log", testutil.Cols{
			"workspace_id": ws, "issue_id": issueID, "actor_type": "member", "actor_id": testUserID,
			"action": "status_changed", "details": `{"from":"todo","to":"` + to + `"}`, "created_at": at,
		})
	}

	inEpic := testutil.Cols{"epic_id": busy}
	newest := issue("Newest work", "in_progress", ago(time.Minute), testutil.Cols{"epic_id": busy, "assignee_type": "agent", "assignee_id": runtimeAgent, "description": "SECRET-DESCRIPTION"})
	statusChange(newest, "in_progress", ago(2*time.Hour))
	second := issue("Second work", "in_progress", ago(time.Hour), inEpic)
	issue("Third work", "in_progress", ago(2*time.Hour), inEpic)
	issue("Stale work", "in_progress", ago(72*time.Hour), inEpic)
	// A sub-issue is represented by its parent, never on its own.
	issue("Sub-issue work", "in_progress", now, testutil.Cols{"epic_id": busy, "parent_issue_id": second})
	issue("Under review", "in_review", ago(3*time.Hour), inEpic)
	blocked := issue("Waiting on owner", "blocked", ago(5*time.Hour), inEpic)
	statusChange(blocked, "blocked", ago(30*time.Hour))
	issue("Blocked by tooling", "blocked", ago(4*time.Hour), inEpic)
	doneNow := issue("Shipped today", "done", ago(30*time.Minute), testutil.Cols{"epic_id": busy, "assignee_type": "member", "assignee_id": testUserID})
	statusChange(doneNow, "done", ago(30*time.Minute))
	doneOld := issue("Shipped long ago", "done", ago(30*24*time.Hour), inEpic)
	statusChange(doneOld, "done", ago(30*24*time.Hour))
	issue("Not started", "todo", ago(time.Hour), inEpic)

	issue("Quiet done", "done", ago(10*24*time.Hour), testutil.Cols{"epic_id": quiet})
	issue("Closed epic work", "in_progress", ago(time.Minute), testutil.Cols{"epic_id": closed})
	issue("Loose work", "in_progress", ago(time.Minute), nil)
	issue("Loose review", "in_review", ago(time.Minute), nil)
	issue("Loose todo", "todo", ago(time.Minute), nil)

	label := fx.Insert(t, "issue_label", testutil.Cols{"workspace_id": ws, "name": "Needs owner", "color": "#ff0000", "resource_type": "issue"})
	fx.InsertNoID(t, "issue_to_label", testutil.Cols{"issue_id": blocked, "label_id": label}, "issue_id = $1 AND label_id = $2", blocked, label)

	fx.Task(t, runtimeAgent, testutil.Cols{"status": "running", "runtime_id": runtime, "issue_id": newest, "started_at": ago(time.Minute)})
	fx.Task(t, runtimeAgent, testutil.Cols{"status": "failed", "issue_id": second, "completed_at": ago(10 * time.Minute), "failure_reason": "timeout", "error": "SECRET-ERROR"})
	fx.Task(t, runtimeAgent, testutil.Cols{"status": "failed", "issue_id": second, "completed_at": ago(48 * time.Hour), "failure_reason": "timeout"})
	// A failed chat run (no issue) stays off the screen.
	fx.Task(t, runtimeAgent, testutil.Cols{"status": "failed", "completed_at": ago(5 * time.Minute), "failure_reason": "timeout"})

	resp := testutil.Call(t, testHandler.GetTVOverview, tvRequest("GET", "/api/tv/overview", raw, nil)).Want(http.StatusOK)
	body := resp.Text()
	for _, secret := range []string{"SECRET-DESCRIPTION", "SECRET-ERROR"} {
		if strings.Contains(body, secret) {
			t.Fatalf("aggregate leaks %s: %s", secret, body)
		}
	}
	var got TVOverviewResponse
	resp.JSON(&got)

	if got.WorkspaceName != "TV aggregate" || got.Sprint == nil || got.Sprint.Name != "Sprint 2" || got.Sprint.StartDate != sprintStart {
		t.Fatalf("sprint = %+v", got.Sprint)
	}
	// The sprint has no issues linked in this test.
	if got.Sprint.Total != 0 || got.Sprint.Done != 0 {
		t.Fatalf("sprint progress = %+v", got.Sprint)
	}
	if got.AgentsWorking != 1 {
		t.Fatalf("agents working = %d, want 1", got.AgentsWorking)
	}
	// Only the in-progress issue idle for 72h is stale.
	if got.StaleTotal != 1 {
		t.Fatalf("stale total = %d, want 1", got.StaleTotal)
	}
	if got.NoEpicActive != 2 {
		t.Fatalf("no-epic active = %d, want 2", got.NoEpicActive)
	}

	if len(got.Epics) != 1 || got.Epics[0].ID != busy || got.EpicsHidden != 0 {
		t.Fatalf("epics = %+v (hidden %d), want only the busy epic", got.Epics, got.EpicsHidden)
	}
	e := got.Epics[0]
	if e.InProgress != 4 || e.InReview != 1 || e.Blocked != 2 || e.DoneInWindow != 1 {
		t.Fatalf("busy epic mix = %+v", e)
	}
	if len(e.Issues) != 3 || e.Issues[0].ID != newest || e.Issues[1].ID != second {
		t.Fatalf("busy epic issues = %+v", e.Issues)
	}
	top := e.Issues[0]
	if top.Identifier == "" || !strings.HasPrefix(top.Identifier, "TVA-") || top.AssigneeName == nil || *top.AssigneeName != "TV agent" || top.Stale {
		t.Fatalf("top issue = %+v", top)
	}
	if since, err := time.Parse(time.RFC3339, top.Since); err != nil || now.Sub(since) < 90*time.Minute {
		t.Fatalf("top issue since = %q, want its move into in_progress 2h ago", top.Since)
	}

	if got.NeedsOwnerTotal != 1 || len(got.NeedsOwner) != 1 || got.NeedsOwner[0].ID != blocked {
		t.Fatalf("needs owner = %d %+v", got.NeedsOwnerTotal, got.NeedsOwner)
	}

	if len(got.Feed) != 2 || got.Feed[0].Kind != "run_failed" || got.Feed[1].Kind != "done" {
		t.Fatalf("feed = %+v, want the failed run then today's done issue", got.Feed)
	}
	if got.Feed[0].FailureReason == nil || *got.Feed[0].FailureReason != "timeout" || got.Feed[0].ActorName == nil || *got.Feed[0].ActorName != "TV agent" {
		t.Fatalf("failed run event = %+v", got.Feed[0])
	}
	if got.Feed[1].IssueTitle == nil || *got.Feed[1].IssueTitle != "Shipped today" {
		t.Fatalf("done event = %+v", got.Feed[1])
	}
}

func TestTVOverviewWithoutOwnerLabel(t *testing.T) {
	ws := dbfx.Workspace(t, "TV empty", fmt.Sprintf("tv-empty-%d", time.Now().UnixNano()))
	raw := auth.KioskTokenPrefix + "empty0123456789abcd"
	dbfx.Insert(t, "kiosk_token", testutil.Cols{"workspace_id": ws, "name": "tv", "token_hash": auth.HashToken(raw), "token_prefix": raw[:12]})

	var got TVOverviewResponse
	testutil.Call(t, testHandler.GetTVOverview, tvRequest("GET", "/api/tv/overview", raw, nil)).Want(http.StatusOK).JSON(&got)
	if got.Sprint != nil || got.Epics == nil || got.NeedsOwner == nil || got.Feed == nil || len(got.Epics) != 0 || got.NeedsOwnerTotal != 0 {
		t.Fatalf("empty workspace overview must be zeroes and empty arrays: %+v", got)
	}
}

func TestKioskPairingCodeIsSingleUseAndFollowsItsToken(t *testing.T) {
	var created CreateKioskTokenResponse
	testutil.Call(t, testHandler.CreateKioskToken, newRequest("POST", "/api/kiosk-tokens", map[string]string{
		"name": "Phone", "owner_label": "Needs owner",
	})).Want(http.StatusCreated).JSON(&created)
	dbfx.Cleanup(t, `DELETE FROM kiosk_token WHERE id = $1 OR parent_id = $1`, created.ID)
	dbfx.Cleanup(t, `DELETE FROM kiosk_pairing WHERE token_id = $1`, created.ID)

	var pairing KioskPairingResponse
	testutil.Call(t, testHandler.CreateKioskPairing, withURLParam(newRequest("POST", "/api/kiosk-tokens/"+created.ID+"/pairing", nil), "id", created.ID)).
		Want(http.StatusCreated).JSON(&pairing)
	if len(pairing.Code) != 32 || pairing.Path != "/tv#pair="+pairing.Code {
		t.Fatalf("pairing = %+v", pairing)
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM kiosk_pairing WHERE code_hash = $1`, auth.HashToken(pairing.Code)); n != 1 {
		t.Fatal("only the code hash is stored")
	}

	rec := httptest.NewRecorder()
	testHandler.CreateTVSession(rec, tvRequest("POST", "/api/tv/session", "", map[string]string{"code": pairing.Code}))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("redeem = %d %s", rec.Code, rec.Body.String())
	}
	var device string
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.KioskCookieName {
			device = c.Value
		}
	}
	if !strings.HasPrefix(device, auth.KioskTokenPrefix) || device == created.Token {
		t.Fatalf("the code must mint a separate device token, got %q", device)
	}
	var overview TVOverviewResponse
	testutil.Call(t, testHandler.GetTVOverview, tvRequest("GET", "/api/tv/overview", device, nil)).Want(http.StatusOK).JSON(&overview)
	if overview.OwnerLabel != "Needs owner" {
		t.Fatalf("device token must inherit the owner label, got %q", overview.OwnerLabel)
	}

	// A second use of the same code is rejected.
	testutil.Call(t, testHandler.CreateTVSession, tvRequest("POST", "/api/tv/session", "", map[string]string{"code": pairing.Code})).Want(http.StatusUnauthorized)

	// An expired code is rejected too.
	var late KioskPairingResponse
	testutil.Call(t, testHandler.CreateKioskPairing, withURLParam(newRequest("POST", "/api/kiosk-tokens/"+created.ID+"/pairing", nil), "id", created.ID)).
		Want(http.StatusCreated).JSON(&late)
	dbfx.Exec(t, `UPDATE kiosk_pairing SET expires_at = now() - interval '1 second' WHERE code_hash = $1`, auth.HashToken(late.Code))
	testutil.Call(t, testHandler.CreateTVSession, tvRequest("POST", "/api/tv/session", "", map[string]string{"code": late.Code})).Want(http.StatusUnauthorized)

	// Revoking the parent token signs the paired device out.
	testutil.Call(t, testHandler.RevokeKioskToken, withURLParam(newRequest("DELETE", "/api/kiosk-tokens/"+created.ID, nil), "id", created.ID)).Want(http.StatusNoContent)
	testutil.Call(t, testHandler.GetTVOverview, tvRequest("GET", "/api/tv/overview", device, nil)).Want(http.StatusUnauthorized)
	// And no new codes can be issued for it.
	testutil.Call(t, testHandler.CreateKioskPairing, withURLParam(newRequest("POST", "/api/kiosk-tokens/"+created.ID+"/pairing", nil), "id", created.ID)).Want(http.StatusNotFound)
}
