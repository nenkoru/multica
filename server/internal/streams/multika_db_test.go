package streams

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func newUUID(t *testing.T) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	if err := testPool.QueryRow(context.Background(), `SELECT gen_random_uuid()`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (h *harness) chatSteps() int {
	h.t.Helper()
	return h.f.Count(h.t, `SELECT count(*) FROM stream_step WHERE stream_id = $1`, h.stream.ID)
}

// TestMultikaFocusRoutesRemarksAndEndsOnSilence walks §9.2: "давай про Терн"
// dives in, remarks reach the manager (one turn while it has not started),
// a redelivered claim does nothing twice, and 30 min of silence surfaces.
func TestMultikaFocusRoutesRemarksAndEndsOnSilence(t *testing.T) {
	h := newHarness(t, "mk-focus")
	ctx := context.Background()
	h.f.Exec(t, `UPDATE stream SET title = 'Терн' WHERE id = $1`, h.stream.ID)
	multika := h.f.Agent(t, "mk-focus-multika", h.f.Runtime(t, "rt-mk-focus-multika"))
	h.sup.MultikaAgentID, _ = parseUUID(multika)
	sid, _ := parseUUID(h.f.ChatSession(t, multika))
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM chat_focus WHERE chat_session_id = $1`, sid)
		_, _ = testPool.Exec(ctx, `DELETE FROM multika_turn WHERE chat_session_id = $1`, sid)
	})
	now := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	h.sup.Now = func() time.Time { return now }
	agent := h.sup.MultikaAgentID

	before := h.chatSteps()
	task1 := newUUID(t)
	out := h.sup.OnMultikaClaim(ctx, task1, sid, agent, "давай про Терн, что там с нодами?")
	if !strings.Contains(out, "фокус поставлен на «Терн»") || !strings.Contains(out, "Фокус: Терн") {
		t.Fatalf("enter context:\n%s", out)
	}
	if got := h.chatSteps(); got != before+1 {
		t.Fatalf("want one manager turn, steps %d → %d", before, got)
	}
	// Redelivered claim of the same task: same context, no second turn.
	again := h.sup.OnMultikaClaim(ctx, task1, sid, agent, "давай про Терн, что там с нодами?")
	if !strings.Contains(again, "фокус поставлен на «Терн»") || h.chatSteps() != before+1 {
		t.Fatalf("redelivery acted twice: steps %d\n%s", h.chatSteps(), again)
	}

	now = now.Add(10 * time.Minute)
	h.sup.OnMultikaClaim(ctx, newUUID(t), sid, agent, "и сколько нод живо")
	if got := h.chatSteps(); got != before+1 {
		t.Fatalf("remark before the turn started must join it, steps %d", got)
	}
	var brief string
	if err := testPool.QueryRow(ctx, `SELECT brief FROM stream_step WHERE stream_id = $1 ORDER BY seq DESC LIMIT 1`, h.stream.ID).Scan(&brief); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(brief, "что там с нодами") || !strings.Contains(brief, "сколько нод живо") {
		t.Fatalf("brief: %q", brief)
	}

	// 30 min of silence: the focus ends and the manager folds decisions.
	now = now.Add(FocusIdle + time.Minute)
	out = h.sup.OnMultikaClaim(ctx, newUUID(t), sid, agent, "привет")
	if !strings.Contains(out, "снят после 30 мин тишины") || !strings.Contains(out, "Фокуса нет") {
		t.Fatalf("idle context:\n%s", out)
	}
	if got := h.chatSteps(); got != before+2 {
		t.Fatalf("want the leave-focus manager turn, steps %d", got)
	}
	if f := h.sup.MultikaFocusOf(ctx, h.stream.WorkspaceID, sid); f.Stream != nil {
		t.Fatalf("focus survived silence: %+v", f)
	}

	// Other agents' chats are not Мультика's.
	if out := h.sup.OnMultikaClaim(ctx, newUUID(t), sid, h.stream.ManagerAgentID, "давай про Терн"); out != "" {
		t.Fatalf("non-Мультика turn got context: %q", out)
	}
}

func TestMultikaExitAndWebFocus(t *testing.T) {
	h := newHarness(t, "mk-exit")
	ctx := context.Background()
	multika := h.f.Agent(t, "mk-exit-multika", h.f.Runtime(t, "rt-mk-exit-multika"))
	h.sup.MultikaAgentID, _ = parseUUID(multika)
	sid, _ := parseUUID(h.f.ChatSession(t, multika))
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM chat_focus WHERE chat_session_id = $1`, sid)
		_, _ = testPool.Exec(ctx, `DELETE FROM multika_turn WHERE chat_session_id = $1`, sid)
	})
	ws := h.stream.WorkspaceID
	if got, err := h.sup.MultikaSession(ctx, ws); err != nil || got.Bytes != sid.Bytes {
		t.Fatalf("session: %v %v", got, err)
	}
	if _, err := h.sup.SetMultikaFocus(ctx, ws, sid, "mk-exit"); err != nil {
		t.Fatal(err)
	}
	if f := h.sup.MultikaFocusOf(ctx, ws, sid); f.Stream == nil || f.Stream.Slug != "mk-exit" {
		t.Fatalf("web focus not set: %+v", f)
	}
	before := h.chatSteps()
	out := h.sup.OnMultikaClaim(ctx, newUUID(t), sid, h.sup.MultikaAgentID, "всё")
	if !strings.Contains(out, "вынырнул") || h.sup.MultikaFocusOf(ctx, ws, sid).Stream != nil {
		t.Fatalf("exit:\n%s", out)
	}
	if h.chatSteps() != before+1 {
		t.Fatal("exit must queue the summary turn")
	}
	if _, err := h.sup.SetMultikaFocus(ctx, ws, sid, "no-such-stream"); err == nil {
		t.Fatal("unknown slug accepted")
	}
}
