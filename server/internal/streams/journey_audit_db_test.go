package streams

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/journeyaudit"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestJourneyBoardAndAskAreMetadataOnly(t *testing.T) {
	h := newHarness(t, "journey-ask")
	t.Setenv("FABRIKA_JOURNEY_AUDIT", "true")
	ctx := journeyaudit.With(context.Background(), journeyaudit.Stamp{Source: "human", Route: "web", RouteEvidence: "server", UserID: testUserID})
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM activity_log WHERE workspace_id=$1 AND action=$2`, testWorkspaceID, journeyaudit.Action)
	})
	api := &HTTP{Sup: h.sup, WorkspaceID: func(*http.Request) string { return testWorkspaceID }}
	for _, body := range []string{`{"surface_id":"00000000-0000-0000-0000-000000000001","content":"private"}`, `{"surface_id":"invalid"}`} {
		r := httptest.NewRequest("POST", "/api/streams/audit/board", strings.NewReader(body)).WithContext(ctx)
		w := httptest.NewRecorder()
		api.AuditBoard(w, r)
		if w.Code != 400 {
			t.Fatalf("invalid body accepted %d", w.Code)
		}
	}
	r := httptest.NewRequest("POST", "/api/streams/audit/board", strings.NewReader(`{"surface_id":"00000000-0000-0000-0000-000000000001"}`)).WithContext(ctx)
	w := httptest.NewRecorder()
	api.AuditBoard(w, r)
	if w.Code != 204 {
		t.Fatalf("open %d %s", w.Code, w.Body.String())
	}
	if n := h.f.Count(t, `SELECT count(*) FROM activity_log WHERE workspace_id=$1 AND action=$2 AND details->>'kind'='board_open'`, testWorkspaceID, journeyaudit.Action); n != 1 {
		t.Fatalf("board opens %d", n)
	}
	// A normal explicit answer records once. A replay and malformed answer do not.
	h.askShown("Synthetic RUSL-3231 question")
	ws, _ := parseUUID(testWorkspaceID)
	res, err := h.sup.Answer(ctx, ws, "1 а", "chat")
	if err != nil || len(res.Applied) != 1 {
		t.Fatalf("answer %+v %v", res, err)
	}
	_, _ = h.sup.Answer(ctx, ws, "1 а", "chat")
	if n := h.f.Count(t, `SELECT count(*) FROM activity_log WHERE workspace_id=$1 AND action=$2 AND details->>'kind'='ask_answer' AND details->>'source'='human' AND details->>'route'='web'`, testWorkspaceID, journeyaudit.Action); n != 1 {
		t.Fatalf("answers %d", n)
	}
	r = httptest.NewRequest("GET", "/api/streams/audit/report?from=bad&to=bad", nil).WithContext(ctx)
	w = httptest.NewRecorder()
	api.AuditReport(w, r)
	if w.Code != 400 {
		t.Fatal("invalid window accepted")
	}
}

func TestJourneyChatBatchKeepsMissingSourceUnknown(t *testing.T) {
	h := newHarness(t, "journey-batch")
	t.Setenv("FABRIKA_JOURNEY_AUDIT", "true")
	ctx := context.Background()
	agent := h.agents["manager"]
	sid, _ := parseUUID(h.f.ChatSession(t, agent))
	task, _ := parseUUID(h.f.Task(t, agent, testutil.Cols{"chat_session_id": uuidString(sid), "runtime_id": h.carrier.rt}))
	h.f.Exec(t, `UPDATE agent_task_queue SET chat_input_task_id=id WHERE id=$1`, task)
	stamp := journeyaudit.Stamp{Source: "human", Route: "web", RouteEvidence: "server", UserID: testUserID}
	one := h.f.Insert(t, "chat_message", testutil.Cols{"chat_session_id": uuidString(sid), "task_id": uuidString(task), "role": "user", "content": "Synthetic RUSL-3231 new"})
	two := h.f.Insert(t, "chat_message", testutil.Cols{"chat_session_id": uuidString(sid), "task_id": uuidString(task), "role": "user", "content": "Synthetic RUSL-3231 legacy"})
	ws, _ := parseUUID(testWorkspaceID)
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM activity_log WHERE workspace_id=$1 AND action=$2`, ws, journeyaudit.Action)
	})
	record := func(id string) {
		t.Helper()
		if _, err := journeyaudit.Record(ctx, db.New(testPool), ws, journeyaudit.Entry{Stamp: stamp, Kind: "chat_message", Outcome: "complete", SessionID: uuidString(sid), MessageID: id}); err != nil {
			t.Fatal(err)
		}
	}
	record(one)
	if got := journeyaudit.From(h.sup.auditChatContext(ctx, task, sid)); got.Source != "unknown" {
		t.Fatalf("legacy message promoted to web: %+v", got)
	}
	record(two)
	if got := journeyaudit.From(h.sup.auditChatContext(ctx, task, sid)); got.Source != "human" || got.Route != "web" {
		t.Fatalf("fully known batch lost source: %+v", got)
	}
}
