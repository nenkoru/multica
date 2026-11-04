package streams

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/testutil"
)

type sentButtons struct {
	inst pgtype.UUID
	chat int64
	text string
	rows [][]AskButton
}

type fakeButtonSender struct {
	sent []sentButtons
	err  error
}

func (f *fakeButtonSender) SendButtons(_ context.Context, inst pgtype.UUID, chat int64, text string, rows [][]AskButton) error {
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, sentButtons{inst, chat, text, rows})
	return nil
}

// multikaTelegram gives the harness a Мультика agent with a Telegram direct
// chat (installation + p2p binding) and returns the installation id and chat id.
func (h *harness) multikaTelegram(chatID int64) (pgtype.UUID, int64) {
	h.t.Helper()
	ctx := context.Background()
	slug := h.stream.Slug
	multika := h.f.Agent(h.t, slug+"-multika", h.f.Runtime(h.t, "rt-"+slug+"-multika"))
	h.sup.MultikaAgentID, _ = parseUUID(multika)
	sid := h.f.ChatSession(h.t, multika)
	inst := h.f.Insert(h.t, "channel_installation", testutil.Cols{
		"workspace_id": testWorkspaceID, "agent_id": multika, "channel_type": "telegram",
		"config":            `{"app_id":"` + strconv.FormatInt(chatID+1_000_000, 10) + `"}`,
		"installer_user_id": testUserID,
	})
	bind := h.f.Insert(h.t, "channel_chat_session_binding", testutil.Cols{
		"chat_session_id": sid, "installation_id": inst, "channel_type": "telegram",
		"channel_chat_id": strconv.FormatInt(chatID, 10), "chat_type": "p2p",
	})
	h.t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM channel_chat_session_binding WHERE id = $1`, bind)
		_, _ = testPool.Exec(ctx, `DELETE FROM channel_installation WHERE id = $1`, inst)
	})
	id, _ := parseUUID(inst)
	return id, chatID
}

// askShown makes the manager raise a two-way question and shows it.
func (h *harness) askShown(text string) {
	h.t.Helper()
	h.tick()
	mg := h.only(KindManage, StepRunning)
	h.finish(mg, contract(map[string]any{
		"result":       "нужен Руслан",
		"human_action": map[string]any{"text": text, "options": []string{"продлить", "не продлевать"}, "default": 1, "zone": "normal", "deadline": time.Now().Add(48 * time.Hour).Format(time.RFC3339)},
	}))
	h.tick()
	ws, _ := parseUUID(testWorkspaceID)
	if _, err := h.sup.Digest(context.Background(), ws, SlotMorning); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) cleanupActions() {
	h.t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM ui_action WHERE ask_id IN (SELECT id FROM ask WHERE stream_id = $1)`, h.stream.ID)
	})
}

func (h *harness) askStatus() string {
	h.t.Helper()
	var s string
	if err := testPool.QueryRow(context.Background(), `SELECT status FROM ask WHERE stream_id = $1 ORDER BY created_at DESC LIMIT 1`, h.stream.ID).Scan(&s); err != nil {
		h.t.Fatal(err)
	}
	return s
}

// TestAskButtonsPushOnceAndPressAnswers: shown questions go to Мультика's
// Telegram chat once, a press answers the question, a second press and a
// press after a text answer change nothing.
func TestAskButtonsPushOnceAndPressAnswers(t *testing.T) {
	h := newHarness(t, "askbtn")
	h.cleanupActions()
	ctx := context.Background()
	ws, _ := parseUUID(testWorkspaceID)
	inst, chat := h.multikaTelegram(424242)
	fake := &fakeButtonSender{}
	h.sup.AskButtons = fake
	h.askShown("Продлить домен за 1200 ₽?")

	n, err := h.sup.PushAskButtons(ctx, ws)
	if err != nil || n != 1 || len(fake.sent) != 1 {
		t.Fatalf("push n=%d err=%v sent=%d", n, err, len(fake.sent))
	}
	msg := fake.sent[0]
	if msg.inst != inst || msg.chat != chat || !strings.HasSuffix(msg.text, "Продлить домен за 1200 ₽?") {
		t.Fatalf("sent %+v", msg)
	}
	if len(msg.rows) != 2 || msg.rows[0][0].Text != "а) продлить" || msg.rows[1][0].Text != "б) не продлевать" {
		t.Fatalf("rows %+v", msg.rows)
	}
	if n, _ := h.sup.PushAskButtons(ctx, ws); n != 0 {
		t.Fatalf("second push resent %d", n)
	}

	// Another chat, a group or a stranger is not Руслан's direct chat.
	if toast, done := h.sup.PressAskButton(ctx, inst, chat+1, chat+1, msg.rows[0][0].Data); toast != pressNotOurs || done != "" {
		t.Fatalf("foreign chat: %q %q", toast, done)
	}
	if toast, _ := h.sup.PressAskButton(ctx, inst, chat, chat+1, msg.rows[0][0].Data); toast != pressNotOurs {
		t.Fatalf("foreign sender: %q", toast)
	}
	if toast, done := h.sup.PressAskButton(ctx, inst, chat, chat, "other-bot-data"); toast != "" || done != "" {
		t.Fatalf("foreign data: %q %q", toast, done)
	}

	toast, done := h.sup.PressAskButton(ctx, inst, chat, chat, msg.rows[0][0].Data)
	if done != "✓ а) продлить" || !strings.Contains(toast, "Принято") {
		t.Fatalf("press: %q %q", toast, done)
	}
	if h.askStatus() != "answered" {
		t.Fatalf("ask %s", h.askStatus())
	}
	var choice int
	if err := testPool.QueryRow(ctx, `SELECT (answer->>'choice')::int FROM ask WHERE stream_id = $1`, h.stream.ID).Scan(&choice); err != nil || choice != 0 {
		t.Fatalf("answer choice %d err %v", choice, err)
	}
	if h.journalCount("ask_answered") != 1 {
		t.Fatal("answer not journaled once")
	}
	// The other button of the same question is closed now.
	if toast, done := h.sup.PressAskButton(ctx, inst, chat, chat, msg.rows[1][0].Data); toast != pressClosed || done == "" {
		t.Fatalf("sibling: %q %q", toast, done)
	}
	if toast, _ := h.sup.PressAskButton(ctx, inst, chat, chat, msg.rows[0][0].Data); toast != pressClosed {
		t.Fatalf("repeat: %q", toast)
	}
	if h.journalCount("ask_answered") != 1 {
		t.Fatal("repeat press answered twice")
	}
	if toast, _ := h.sup.PressAskButton(ctx, inst, chat, chat, askTokenPrefix+"0000"); toast != pressStale {
		t.Fatalf("unknown token: %q", toast)
	}
}

// TestAskButtonsFailedSendIsRetried: a send that fails leaves no live
// buttons, so the next push sends the question again.
func TestAskButtonsFailedSendIsRetried(t *testing.T) {
	h := newHarness(t, "askbtnfail")
	h.cleanupActions()
	ctx := context.Background()
	ws, _ := parseUUID(testWorkspaceID)
	h.multikaTelegram(434343)
	fake := &fakeButtonSender{err: errors.New("telegram down")}
	h.sup.AskButtons = fake
	h.askShown("Продлить сертификат?")

	if _, err := h.sup.PushAskButtons(ctx, ws); err == nil {
		t.Fatal("send error swallowed")
	}
	fake.err = nil
	if n, err := h.sup.PushAskButtons(ctx, ws); err != nil || n != 1 {
		t.Fatalf("retry n=%d err=%v", n, err)
	}
}

// TestAskButtonsOffWithoutFlag: no Мультика agent — nothing is sent.
func TestAskButtonsOffWithoutFlag(t *testing.T) {
	h := newHarness(t, "askbtnoff")
	ctx := context.Background()
	ws, _ := parseUUID(testWorkspaceID)
	fake := &fakeButtonSender{}
	h.sup.AskButtons = fake
	if n, err := h.sup.PushAskButtons(ctx, ws); n != 0 || err != nil || len(fake.sent) != 0 {
		t.Fatalf("n=%d err=%v sent=%d", n, err, len(fake.sent))
	}
}

// TestAskButtonsLosingPressOfCrashQuestionChangesNothing: both buttons of a
// red crash question are pressed at once. The press that loses the race
// must not pause the stream or journal an answer the ask does not hold.
func TestAskButtonsLosingPressOfCrashQuestionChangesNothing(t *testing.T) {
	h := newHarness(t, "askbtnrace")
	h.cleanupActions()
	ctx := context.Background()
	ws, _ := parseUUID(testWorkspaceID)
	inst, chat := h.multikaTelegram(454545)
	fake := &fakeButtonSender{}
	h.sup.AskButtons = fake
	h.askShown("Продлить сертификат после аварии?")
	h.f.Exec(t, `UPDATE ask SET zone = 'red', default_option = NULL WHERE stream_id = $1`, h.stream.ID)
	h.f.Exec(t, `UPDATE stream SET state = 'broken' WHERE id = $1`, h.stream.ID)
	if _, err := h.sup.PushAskButtons(ctx, ws); err != nil || len(fake.sent) != 1 {
		t.Fatalf("push err=%v sent=%d", err, len(fake.sent))
	}
	rows := fake.sent[0].rows
	var askID pgtype.UUID
	if err := testPool.QueryRow(ctx, `SELECT id FROM ask WHERE stream_id = $1`, h.stream.ID).Scan(&askID); err != nil {
		t.Fatal(err)
	}
	// Both presses read the ask while it is still open, then the first wins.
	stale, err := h.sup.Q.GetAsk(ctx, askID)
	if err != nil {
		t.Fatal(err)
	}
	// Red zone: the press asks for confirmation in a separate message;
	// «Отмена» leaves the question open, «Подтвердить» answers it.
	if toast, done := h.sup.PressAskButton(ctx, inst, chat, chat, rows[0][0].Data); toast != pressConfirmSent || done != "" {
		t.Fatalf("red press: %q %q", toast, done)
	}
	if len(fake.sent) != 2 || len(fake.sent[1].rows[0]) != 2 || !strings.Contains(fake.sent[1].text, "а) продлить") {
		t.Fatalf("confirm message %+v", fake.sent)
	}
	if toast, done := h.sup.PressAskButton(ctx, inst, chat, chat, fake.sent[1].rows[0][1].Data); toast != pressCancelled || done == "" {
		t.Fatalf("cancel: %q %q", toast, done)
	}
	if h.askStatus() != "shown" {
		t.Fatalf("cancel answered the question: %s", h.askStatus())
	}
	if _, done := h.sup.PressAskButton(ctx, inst, chat, chat, rows[0][0].Data); done != "" || len(fake.sent) != 3 {
		t.Fatalf("second red press: %q sent=%d", done, len(fake.sent))
	}
	if _, done := h.sup.PressAskButton(ctx, inst, chat, chat, fake.sent[2].rows[0][0].Data); done != "✓ а) продлить" {
		t.Fatalf("confirmed press: %q", done)
	}
	// A stale confirm button of the first round changes nothing.
	if _, done := h.sup.PressAskButton(ctx, inst, chat, chat, fake.sent[1].rows[0][0].Data); strings.HasPrefix(done, "✓") {
		t.Fatalf("stale confirm applied: %q", done)
	}
	out, err := h.sup.applyReply(ctx, stale, Reply{Choice: 1}, askButtonsChan, time.Now())
	if err != nil || out != replyClosed {
		t.Fatalf("losing reply: out=%v err=%v", out, err)
	}
	if st := h.reload(); st.State == StatePaused {
		t.Fatal("losing «pause» reply paused the stream")
	}
	if h.journalCount("ask_answered") != 1 {
		t.Fatalf("ask_answered journaled %d times", h.journalCount("ask_answered"))
	}
	var choice int
	if err := testPool.QueryRow(ctx, `SELECT (answer->>'choice')::int FROM ask WHERE id = $1`, askID).Scan(&choice); err != nil || choice != 0 {
		t.Fatalf("stored choice %d err %v", choice, err)
	}
}

// TestMultikaTelegramTargetIsTheInstallersChat: another member's direct chat
// with Мультика's bot never receives the question buttons.
func TestMultikaTelegramTargetIsTheInstallersChat(t *testing.T) {
	h := newHarness(t, "askbtnowner")
	h.cleanupActions()
	ctx := context.Background()
	ws, _ := parseUUID(testWorkspaceID)
	inst, chat := h.multikaTelegram(464646)
	// A newer direct chat of someone else with the same bot.
	other := h.f.User(t, "askbtn-other", "askbtn-other@example.test")
	sid := h.f.ChatSession(t, uuidString(h.sup.MultikaAgentID), testutil.Cols{"creator_id": other})
	bind := h.f.Insert(t, "channel_chat_session_binding", testutil.Cols{
		"chat_session_id": sid, "installation_id": uuidString(inst), "channel_type": "telegram",
		"channel_chat_id": "474747", "chat_type": "p2p", "created_at": time.Now().Add(time.Hour),
	})
	t.Cleanup(func() { _, _ = testPool.Exec(ctx, `DELETE FROM channel_chat_session_binding WHERE id = $1`, bind) })
	fake := &fakeButtonSender{}
	h.sup.AskButtons = fake
	h.askShown("Продлить домен?")
	if _, err := h.sup.PushAskButtons(ctx, ws); err != nil || len(fake.sent) != 1 || fake.sent[0].chat != chat {
		t.Fatalf("err=%v sent=%+v", err, fake.sent)
	}
	if toast, _ := h.sup.PressAskButton(ctx, inst, 474747, 474747, fake.sent[0].rows[0][0].Data); toast != pressNotOurs {
		t.Fatalf("other member's press: %q", toast)
	}
}
