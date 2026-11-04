package streams

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/streams/sdb"
)

func charterReply(body string) string {
	return "Завожу.\n```fabrika-charter\n" + body + "\n```"
}

const goodCharter = `{"slug": "iv-new", "title": "Новый поток", "charter": {"goal": "цель", "metric": "метрика", "done": "готово", "mandate": "делать самой"}}`

// interviewHarness is a template stream plus Мультика's chat in its workspace.
func interviewHarness(t *testing.T) (*harness, pgtype.UUID) {
	t.Helper()
	h := newHarness(t, "iv-tpl")
	ctx := context.Background()
	multika := h.f.Agent(t, "iv-multika", h.f.Runtime(t, "rt-iv-multika"))
	h.sup.MultikaAgentID, _ = parseUUID(multika)
	h.sup.StreamTemplate = "iv-tpl"
	sid, _ := parseUUID(h.f.ChatSession(t, multika))
	t.Cleanup(func() {
		for _, table := range []string{"stream_step", "stream_wait", "ask", "milestone", "stream_journal"} {
			_, _ = testPool.Exec(ctx, "DELETE FROM "+table+" WHERE stream_id IN (SELECT id FROM stream WHERE slug LIKE 'iv-%' AND slug <> 'iv-tpl')")
		}
		_, _ = testPool.Exec(ctx, `DELETE FROM stream WHERE slug LIKE 'iv-%' AND slug <> 'iv-tpl'`)
		_, _ = testPool.Exec(ctx, `DELETE FROM multika_charter WHERE chat_session_id = $1`, sid)
		_, _ = testPool.Exec(ctx, `DELETE FROM multika_turn WHERE chat_session_id = $1`, sid)
		_, _ = testPool.Exec(ctx, `DELETE FROM chat_focus WHERE chat_session_id = $1`, sid)
	})
	return h, sid
}

func (h *harness) streamCount(slug string) int {
	h.t.Helper()
	return h.f.Count(h.t, `SELECT count(*) FROM stream WHERE slug = $1`, slug)
}

// §9.3: the charter block after Руслан's yes creates the stream with the
// template's executors, focuses it, queues the first manager turn, and
// Мультика hears about it on her next turn.
func TestInterviewCharterCreatesStream(t *testing.T) {
	h, sid := interviewHarness(t)
	ctx := context.Background()
	agent := h.sup.MultikaAgentID

	// Plain talk and the questions themselves create nothing.
	h.sup.OnMultikaReply(ctx, sid, newUUID(t), "1. Цель? Вариант: …")
	if h.streamCount("iv-new") != 0 {
		t.Fatal("stream from a reply without a block")
	}

	task := newUUID(t)
	h.sup.OnMultikaReply(ctx, sid, task, charterReply(goodCharter))
	h.sup.OnMultikaReply(ctx, sid, task, charterReply(goodCharter)) // same turn again
	if h.streamCount("iv-new") != 1 {
		t.Fatalf("streams %d", h.streamCount("iv-new"))
	}
	st, err := h.sup.Q.GetStreamBySlug(ctx, sdb.GetStreamBySlugParams{WorkspaceID: h.stream.WorkspaceID, Slug: "iv-new"})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := ParseCharter(st.Charter)
	if !st.Active || st.ManagerAgentID != h.stream.ManagerAgentID || len(c.Chains["M"]) != 2 || c.Mandate != "делать самой" {
		t.Fatalf("stream %+v charter %+v", st, c)
	}
	if f := h.sup.MultikaFocusOf(ctx, st.WorkspaceID, sid); f.Stream == nil || f.Stream.Slug != "iv-new" {
		t.Fatalf("focus %+v", f)
	}
	h.tick()
	if n := h.f.Count(t, `SELECT count(*) FROM stream_step WHERE stream_id = $1 AND kind = 'manage'`, st.ID); n != 1 {
		t.Fatalf("first manager turns %d", n)
	}

	out := h.sup.OnMultikaClaim(ctx, newUUID(t), sid, agent, "ну что?")
	if !strings.Contains(out, "«Новый поток» (iv-new) заведён по уставу") || !strings.Contains(out, "fabrika-charter") {
		t.Fatalf("context:\n%s", out)
	}
	// Reported once.
	if out := h.sup.OnMultikaClaim(ctx, newUUID(t), sid, agent, "ещё"); strings.Contains(out, "заведён по уставу") {
		t.Fatalf("outcome reported twice:\n%s", out)
	}

	// The same slug again does not overwrite the charter.
	h.sup.OnMultikaReply(ctx, sid, newUUID(t), charterReply(strings.Replace(goodCharter, `"цель"`, `"другая"`, 1)))
	if out := h.sup.OnMultikaClaim(ctx, newUUID(t), sid, agent, "и?"); !strings.Contains(out, "НЕ заведён: поток «iv-new» уже есть") {
		t.Fatalf("context:\n%s", out)
	}
}

func TestInterviewCharterRejectsIncompleteOrOverreaching(t *testing.T) {
	h, sid := interviewHarness(t)
	ctx := context.Background()
	cases := []struct{ body, want string }{
		{`{"slug": "iv-x", "title": "X", "charter": {"goal": "g", "metric": "m", "done": "d"}}`, "не заполнено: mandate"},
		{`{"slug": "Iv X", "title": "X", "charter": {"goal": "g", "metric": "m", "done": "d", "mandate": "m"}}`, "slug «Iv X» не подходит"},
		{`{"slug": "multika", "title": "X", "charter": {"goal": "g", "metric": "m", "done": "d", "mandate": "m"}}`, "не подходит"},
		{`{"slug": "iv-x", "title": "X", "charter": {"goal": "g", "metric": "m", "done": "d", "mandate": "m", "chains": {"M": [{"name": "x", "agent_id": "y"}]}}}`, "цепочки исполнителей"},
		{`{"slug": "iv-x", "title": "X", "owner": "me", "charter": {"goal": "g"}}`, "не читается"},
		{`{"slug": "iv-x", "title": "X", "charter": {"goal": "g", "metric": "m", "done": "d", "mandate": "m", "budget_share": 1.5}}`, "доля бюджета"},
	}
	for _, tc := range cases {
		h.sup.OnMultikaReply(ctx, sid, newUUID(t), charterReply(tc.body))
		out := h.sup.OnMultikaClaim(ctx, newUUID(t), sid, h.sup.MultikaAgentID, "?")
		if !strings.Contains(out, "НЕ заведён") || !strings.Contains(out, tc.want) {
			t.Errorf("%s: context\n%s", tc.body, out)
		}
	}
	h.sup.OnMultikaReply(ctx, sid, newUUID(t), "Завожу.\n```fabrika-charter\n{\"slug\": \"iv-x\"")
	if out := h.sup.OnMultikaClaim(ctx, newUUID(t), sid, h.sup.MultikaAgentID, "?"); !strings.Contains(out, "не закрыт") {
		t.Errorf("unclosed block: context\n%s", out)
	}
	if n := h.f.Count(t, `SELECT count(*) FROM stream WHERE slug IN ('iv-x', 'multika')`); n != 0 {
		t.Fatalf("created %d", n)
	}

	h.sup.StreamTemplate = ""
	h.sup.OnMultikaReply(ctx, sid, newUUID(t), charterReply(goodCharter))
	if h.streamCount("iv-new") != 0 {
		t.Fatal("created without a template")
	}
}

// §9.3: with five active the block must name whom to pause.
func TestInterviewCharterNeedsPauseAtFiveActive(t *testing.T) {
	h, sid := interviewHarness(t)
	ctx := context.Background()
	active := h.f.Count(t, `SELECT count(*) FROM stream WHERE workspace_id = $1 AND active`, uuidString(h.stream.WorkspaceID))
	for i := active; i < MaxActiveStreams; i++ {
		h.f.Exec(t, `INSERT INTO stream (workspace_id, slug, title) VALUES ($1, $2, 'Заполнитель')`, uuidString(h.stream.WorkspaceID), "iv-f"+string(rune('a'+i)))
	}
	h.sup.OnMultikaReply(ctx, sid, newUUID(t), charterReply(goodCharter))
	if h.streamCount("iv-new") != 0 {
		t.Fatal("sixth active stream created")
	}
	if out := h.sup.OnMultikaClaim(ctx, newUUID(t), sid, h.sup.MultikaAgentID, "?"); !strings.Contains(out, "кого на паузу") {
		t.Fatalf("context:\n%s", out)
	}

	withPause := strings.Replace(goodCharter, `"slug": "iv-new"`, `"slug": "iv-new", "pause": "iv-tpl"`, 1)
	h.sup.OnMultikaReply(ctx, sid, newUUID(t), charterReply(withPause))
	if h.streamCount("iv-new") != 1 {
		t.Fatal("not created with a pause")
	}
	if st := h.reload(); st.Active {
		t.Fatal("paused stream still active")
	}
}

// A pause in the block with room to spare pauses nothing.
func TestInterviewPauseOnlyWhenFull(t *testing.T) {
	h, sid := interviewHarness(t)
	withPause := strings.Replace(goodCharter, `"slug": "iv-new"`, `"slug": "iv-new", "pause": "iv-tpl"`, 1)
	h.sup.OnMultikaReply(context.Background(), sid, newUUID(t), charterReply(withPause))
	if h.streamCount("iv-new") != 1 {
		t.Fatal("not created")
	}
	if st := h.reload(); !st.Active {
		t.Fatal("paused a stream with room to spare")
	}
}

func TestParseCharterBlockTakesTheLast(t *testing.T) {
	d, err := ParseCharterBlock(charterReply(`{"slug": "a", "title": "A", "charter": {}}`) + "\nисправила:\n" + charterReply(`{"slug": "b", "title": "B", "charter": {}}`))
	if err != nil || d == nil || d.Slug != "b" {
		t.Fatalf("%+v %v", d, err)
	}
	if d, err := ParseCharterBlock("```json\n{\"slug\": \"a\"}\n```"); d != nil || err != nil {
		t.Fatalf("plain json read as a charter: %+v %v", d, err)
	}
}
