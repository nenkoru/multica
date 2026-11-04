package streams

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/streams/genui"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// screensHarness gives the harness Мультика, her chat and the screens
// pipeline on a private bus.
func screensHarness(t *testing.T, slug string) (*harness, pgtype.UUID, pgtype.UUID, *events.Bus) {
	h := newHarness(t, slug)
	multika := h.f.Agent(t, slug+"-multika", h.f.Runtime(t, "rt-"+slug+"-multika"))
	h.sup.MultikaAgentID, _ = parseUUID(multika)
	sid, _ := parseUUID(h.f.ChatSession(t, multika))
	h.sup.Screens = &Screens{LinkHosts: []string{"github.com"}}
	bus := events.New()
	h.sup.subscribeScreens(bus)
	ws, _ := parseUUID(testWorkspaceID)
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = testPool.Exec(ctx, `DELETE FROM ui_action WHERE ui_block_id IN (SELECT id FROM ui_block WHERE chat_session_id = $1)`, sid)
		_, _ = testPool.Exec(ctx, `DELETE FROM ui_block WHERE chat_session_id = $1`, sid)
	})
	return h, ws, sid, bus
}

func randomTask() pgtype.UUID {
	var u pgtype.UUID
	_, _ = rand.Read(u.Bytes[:])
	u.Bytes[6] = (u.Bytes[6] & 0x0f) | 0x40
	u.Bytes[8] = (u.Bytes[8] & 0x3f) | 0x80
	u.Valid = true
	return u
}

// TestScreenLinesStreamAsWritten: finished lines inside the block go out as
// chat:ui_line in order; an invalid line and a redelivered frame do not.
func TestScreenLinesStreamAsWritten(t *testing.T) {
	h, ws, sid, bus := screensHarness(t, "scrlive")
	_ = h
	var mu sync.Mutex
	var got []LineEvent
	bus.Subscribe(EventUILine, func(e events.Event) {
		mu.Lock()
		defer mu.Unlock()
		if e.ChatSessionOwnerID != "owner" || e.WorkspaceID != uuidString(ws) {
			t.Errorf("event routing %+v", e)
		}
		got = append(got, e.Payload.(LineEvent))
	})
	task := uuidString(randomTask())
	h.sup.Screens.live = map[string]*liveScreen{task: {multika: true, lastSeq: -1}}
	reply := "Смотри:\n```openui\nroot = Stack([k, bad])\nk = KpiStrip([Kpi(\"a\", \"1\")])\nbad = Marquee(\"x\")\n```\nВсё."
	seq := 0
	for i := 0; i < len(reply); i += 9 {
		chunk := reply[i:min(i+9, len(reply))]
		e := events.Event{Type: protocol.EventTaskMessage, WorkspaceID: uuidString(ws), ChatSessionID: uuidString(sid), ChatSessionOwnerID: "owner",
			Payload: protocol.TaskMessagePayload{TaskID: task, Seq: seq, Type: "text", Content: chunk}}
		bus.Publish(e)
		bus.Publish(e) // redelivered frame
		seq++
	}
	if len(got) != 2 || got[0].Root != "root" || got[1].Nodes[0].ID != "k" || len(got[1].Nodes) != 2 {
		t.Fatalf("lines %+v", got)
	}
}

const screenSrc = `root = Stack([kpis, acts])
kpis = KpiStrip([Kpi("Всего", "$4.50")])
acts = Actions([Button("Урезать вдвое", set_share("%[1]s", 0.5)), Button("Поднять на $20", raise_budget("%[1]s", 20)), Button("Куда-то", open("https://github.com/nenkoru/multica"))])`

// TestScreenStoreAndPress: the screen is stored once with an action id on
// every button; a press runs once; red asks for the app's confirmation.
func TestScreenStoreAndPress(t *testing.T) {
	h, ws, sid, _ := screensHarness(t, "scrpress")
	ctx := context.Background()
	h.sup.APIMonthlyUSD = 100
	h.f.Exec(t, `UPDATE stream SET charter = charter || '{"budget_share": 0.4, "keep": "me"}'::jsonb, min_share = 0.5 WHERE id = $1`, h.stream.ID)
	task := randomTask()
	src := fmt.Sprintf(screenSrc, h.stream.Slug)
	b, err := h.sup.storeModelScreen(ctx, ws, sid, task, pgtype.UUID{}, modelBlockSeq, src)
	if err != nil || b.Status != genui.StatusValid {
		t.Fatalf("store: %v %s %s", err, b.Status, b.Issues)
	}
	again, err := h.sup.storeModelScreen(ctx, ws, sid, task, pgtype.UUID{}, modelBlockSeq, src)
	if err != nil || again.ID != b.ID {
		t.Fatalf("redelivery stored a second block: %v", err)
	}
	var n int
	_ = testPool.QueryRow(ctx, `SELECT count(*) FROM ui_action WHERE ui_block_id = $1`, b.ID).Scan(&n)
	if n != 3 {
		t.Fatalf("actions %d", n)
	}
	var doc struct {
		Components []struct {
			ID        string                                `json:"id"`
			Component map[string]map[string]json.RawMessage `json:"component"`
		} `json:"components"`
	}
	if err := json.Unmarshal(b.A2ui, &doc); err != nil {
		t.Fatal(err)
	}
	var buttons []struct {
		Label    string `json:"label"`
		ActionID string `json:"action_id"`
		Zone     string `json:"zone"`
	}
	for _, c := range doc.Components {
		if raw, ok := c.Component["Actions"]["buttons"]; ok {
			_ = json.Unmarshal(raw, &buttons)
		}
	}
	if len(buttons) != 3 || buttons[0].ActionID == "" || buttons[0].Zone != "normal" || buttons[1].Zone != "red" {
		t.Fatalf("buttons %+v", buttons)
	}
	cut, _ := parseUUID(buttons[0].ActionID)
	raise, _ := parseUUID(buttons[1].ActionID)
	var owner pgtype.UUID
	if err := testPool.QueryRow(ctx, `SELECT creator_id FROM chat_session WHERE id = $1`, sid).Scan(&owner); err != nil {
		t.Fatal(err)
	}

	res, err := h.sup.PressScreenAction(ctx, ws, owner, cut, false, "")
	if err != nil || res.Status != PressDone {
		t.Fatalf("cut: %+v %v", res, err)
	}
	st := h.reload()
	c, _ := ParseCharter(st.Charter)
	minF, _ := st.MinShare.Float64Value()
	if c.BudgetShare != 0.2 || minF.Float64 != 0.25 || !strings.Contains(string(st.Charter), `"keep"`) {
		t.Fatalf("after cut: share %v min %v charter %s", c.BudgetShare, minF.Float64, st.Charter)
	}
	if res, _ := h.sup.PressScreenAction(ctx, ws, owner, cut, false, ""); res.Status != PressAlready {
		t.Fatalf("second press: %+v", res)
	}
	if h.journalCount("share_set") != 1 {
		t.Fatalf("share_set journaled %d times", h.journalCount("share_set"))
	}

	// Red: a confirm without the server's window only opens one; cancel
	// closes it and its id no longer confirms; confirm applies once.
	res, err = h.sup.PressScreenAction(ctx, ws, owner, raise, true, "")
	if err != nil || res.Status != PressConfirm || res.Confirm == nil || res.Confirm.Window == "" || !strings.Contains(res.Confirm.Text, "$20.00") {
		t.Fatalf("red press: %+v %v", res, err)
	}
	first := res.Confirm.Window
	if res, _ := h.sup.CancelScreenAction(ctx, ws, owner, raise); res.Status != PressCancelled {
		t.Fatalf("cancel: %+v", res)
	}
	if res, _ := h.sup.PressScreenAction(ctx, ws, owner, raise, true, first); res.Status != PressConfirm || res.Confirm.Window == first {
		t.Fatalf("cancelled window confirmed: %+v", res)
	}
	if c, _ := ParseCharter(h.reload().Charter); c.BudgetShare != 0.2 {
		t.Fatalf("cancel changed the budget: %v", c.BudgetShare)
	}
	// The window's numbers no longer hold: a new window, nothing applied.
	res, _ = h.sup.PressScreenAction(ctx, ws, owner, raise, false, "")
	h.f.Exec(t, `UPDATE stream SET charter = charter || '{"budget_share": 0.3}'::jsonb WHERE id = $1`, h.stream.ID)
	res2, err := h.sup.PressScreenAction(ctx, ws, owner, raise, true, res.Confirm.Window)
	if err != nil || res2.Status != PressConfirm || !strings.Contains(res2.Confirm.Text, "$30.00 → $50.00") {
		t.Fatalf("changed stream: %+v %v", res2, err)
	}
	res, err = h.sup.PressScreenAction(ctx, ws, owner, raise, true, res2.Confirm.Window)
	if err != nil || res.Status != PressDone {
		t.Fatalf("confirmed: %+v %v", res, err)
	}
	if c, _ := ParseCharter(h.reload().Charter); c.BudgetShare != 0.5 {
		t.Fatalf("raise: share %v", c.BudgetShare)
	}

	// Another user, another workspace or a made-up id is stale.
	if _, err := h.sup.PressScreenAction(ctx, ws, owner, randomTask(), false, ""); err != ErrActionStale {
		t.Fatalf("unknown action: %v", err)
	}
	other, _ := parseUUID("00000000-0000-4000-8000-00000000abcd")
	if _, err := h.sup.PressScreenAction(ctx, other, owner, cut, false, ""); err != ErrActionStale {
		t.Fatalf("foreign workspace: %v", err)
	}
	if _, err := h.sup.PressScreenAction(ctx, ws, randomTask(), cut, false, ""); err != ErrActionStale {
		t.Fatalf("foreign user: %v", err)
	}
	if h.sup.ownsChat(ctx, ws, randomTask(), sid) || !h.sup.ownsChat(ctx, ws, owner, sid) {
		t.Fatal("chat ownership")
	}

	blocks, err := h.sup.ScreenBlocks(ctx, ws, sid, 10)
	if err != nil || len(blocks) != 1 || blocks[0].ID != uuidString(b.ID) {
		t.Fatalf("blocks %+v %v", blocks, err)
	}
}

// TestScreenShareRollsBack: a share change that fails leaves the button
// pressable and the stream untouched — never done-but-not-applied.
func TestScreenShareRollsBack(t *testing.T) {
	h, ws, sid, _ := screensHarness(t, "scrtx")
	ctx := context.Background()
	src := fmt.Sprintf("root = Actions([Button(\"Урезать\", set_share(\"%s\", 0.5))])", h.stream.Slug)
	b, err := h.sup.storeModelScreen(ctx, ws, sid, randomTask(), pgtype.UUID{}, modelBlockSeq, src)
	if err != nil {
		t.Fatal(err)
	}
	var id, owner pgtype.UUID
	if err := testPool.QueryRow(ctx, `SELECT id FROM ui_action WHERE ui_block_id = $1`, b.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `SELECT creator_id FROM chat_session WHERE id = $1`, sid).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	// A charter that is not an object cannot be rewritten.
	h.f.Exec(t, `UPDATE stream SET charter = '[]'::jsonb WHERE id = $1`, h.stream.ID)
	if _, err := h.sup.PressScreenAction(ctx, ws, owner, id, false, ""); err == nil {
		t.Fatal("press on an unreadable charter must fail")
	}
	ua, err := h.sup.Q.GetUIAction(ctx, id)
	if err != nil || ua.Status != "pending" || h.journalCount("share_set") != 0 {
		t.Fatalf("after failure: status %s journal %d", ua.Status, h.journalCount("share_set"))
	}
}

// TestScreenStreamCardAndDigest: code-built screens compile cleanly from
// the stream's records.
func TestScreenStreamCardAndDigest(t *testing.T) {
	h, ws, sid, _ := screensHarness(t, "scrcard")
	ctx := context.Background()
	src, err := h.sup.streamCardSource(ctx, ws, h.stream, h.sup.now())
	if err != nil {
		t.Fatal(err)
	}
	res := genui.Finalize(src, h.sup.screenOptions(ctx, ws), nil)
	if res.Status != genui.StatusValid {
		t.Fatalf("stream card %s: %v\n%s", res.Status, res.Issues, src)
	}
	task := randomTask()
	h.sup.showStreamCard(ctx, ws, sid, task, h.stream)
	if b, err := h.sup.Q.GetUIBlock(ctx, blockID(task, codeBlockSeq)); err != nil || b.Kind != "code" {
		t.Fatalf("stream card block: %v", err)
	}
	h.askShown("Продлить домен?")
	src, err = h.sup.digestScreenSource(ctx, ws, h.sup.now())
	if err != nil {
		t.Fatal(err)
	}
	res = genui.Finalize(src, h.sup.screenOptions(ctx, ws), nil)
	if res.Status != genui.StatusValid || !strings.Contains(src, "Нужен ты: 1 вопрос") {
		t.Fatalf("digest %s: %v\n%s", res.Status, res.Issues, src)
	}
	if txt := genui.Text(res.Doc); !strings.Contains(txt, "Продлить домен?") {
		t.Fatalf("digest text:\n%s", txt)
	}
}

func TestTelegramReplyTextHidesScreens(t *testing.T) {
	s := &Supervisor{Screens: &Screens{}}
	out := s.TelegramReplyText(context.Background(), "", "Вот:\n```openui\nroot = KpiStrip([Kpi(\"Всего\", \"$4\")])\n```\nИтог.")
	if strings.Contains(out, "```") || !strings.Contains(out, "Всего: $4") || !strings.Contains(out, "Итог.") {
		t.Fatalf("telegram text %q", out)
	}
	if p := TelegramPartialText("Вот:\n```openui\nroot = Kp"); strings.Contains(p, "openui") {
		t.Fatalf("partial %q", p)
	}
}
