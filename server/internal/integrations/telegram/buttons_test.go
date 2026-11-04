package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/integrations/channel"
)

// fakeBotCalls records Bot API calls by method name with their JSON bodies.
type fakeBotCalls struct {
	mu    sync.Mutex
	calls map[string][]map[string]any
}

func (f *fakeBotCalls) add(method string, body map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls == nil {
		f.calls = map[string][]map[string]any{}
	}
	f.calls[method] = append(f.calls[method], body)
}

func (f *fakeBotCalls) get(method string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[method]
}

func TestPollerAsksForCallbacksOnlyWithHandler(t *testing.T) {
	for _, withHandler := range []bool{false, true} {
		var got []string
		gotOne := make(chan struct{}, 1)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body getUpdatesParams
			_ = json.NewDecoder(r.Body).Decode(&body)
			if got == nil {
				got = body.AllowedUpdates
				gotOne <- struct{}{}
			}
			<-r.Context().Done()
		}))
		ch := &telegramChannel{
			api:     newBotAPI(srv.URL, "123:abc", srv.Client()),
			handler: func(context.Context, channel.InboundMessage) error { return nil },
			logger:  testLogger(),
		}
		if withHandler {
			ch.callbacks = func(context.Context, pgtype.UUID, int64, int64, string) (string, string) { return "", "" }
		}
		ctx, cancel := context.WithCancel(context.Background())
		go func() { _ = ch.Connect(ctx) }()
		select {
		case <-gotOne:
		case <-time.After(5 * time.Second):
			t.Fatal("no getUpdates call")
		}
		cancel()
		srv.Close()
		want := "message"
		if withHandler {
			want = "message,callback_query"
		}
		if strings.Join(got, ",") != want {
			t.Errorf("handler=%v: allowed_updates = %v, want %s", withHandler, got, want)
		}
	}
}

func TestCallbackPressAnswersAndClosesKeyboard(t *testing.T) {
	var rec fakeBotCalls
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		rec.add(r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:], body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer srv.Close()

	inst := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	var seen struct {
		inst         pgtype.UUID
		chat, from   int64
		data         string
		handlerCalls int
	}
	ch := &telegramChannel{
		api:            newBotAPI(srv.URL, "123:abc", srv.Client()),
		logger:         testLogger(),
		installationID: inst,
		callbacks: func(_ context.Context, i pgtype.UUID, chat, from int64, data string) (string, string) {
			seen.inst, seen.chat, seen.from, seen.data = i, chat, from, data
			seen.handlerCalls++
			return "Принято: а) да", "✓ а) да"
		},
	}
	err := ch.dispatch(context.Background(), Update{UpdateID: 5, CallbackQuery: &CallbackQuery{
		ID:      "cb1",
		From:    &User{ID: 42},
		Message: &Message{MessageID: 7, Chat: Chat{ID: 42}, Text: "1. Вопрос?"},
		Data:    "fa1.0011",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if seen.handlerCalls != 1 || seen.inst != inst || seen.chat != 42 || seen.from != 42 || seen.data != "fa1.0011" {
		t.Fatalf("handler saw %+v", seen)
	}
	answers := rec.get("answerCallbackQuery")
	if len(answers) != 1 || answers[0]["callback_query_id"] != "cb1" || answers[0]["text"] != "Принято: а) да" {
		t.Fatalf("answerCallbackQuery = %v", answers)
	}
	edits := rec.get("editMessageText")
	if len(edits) != 1 || edits[0]["text"] != "1. Вопрос?\n\n✓ а) да" || edits[0]["reply_markup"] != nil {
		t.Fatalf("editMessageText = %v", edits)
	}
}

func TestCallbackPressNotSettledKeepsKeyboard(t *testing.T) {
	var rec fakeBotCalls
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.add(r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:], nil)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer srv.Close()
	ch := &telegramChannel{
		api:    newBotAPI(srv.URL, "123:abc", srv.Client()),
		logger: testLogger(),
		callbacks: func(context.Context, pgtype.UUID, int64, int64, string) (string, string) {
			return "Уже отвечено.", ""
		},
	}
	_ = ch.dispatch(context.Background(), Update{CallbackQuery: &CallbackQuery{
		ID: "cb2", From: &User{ID: 42}, Message: &Message{MessageID: 7, Chat: Chat{ID: 42}}, Data: "fa1.x",
	}})
	if len(rec.get("answerCallbackQuery")) != 1 || len(rec.get("editMessageText")) != 0 {
		t.Fatalf("calls = %v", rec.calls)
	}
}

func TestInlineKeyboardRejectsOversizedData(t *testing.T) {
	if _, err := inlineKeyboard([][]Button{{{Text: "x", Data: strings.Repeat("a", 65)}}}); err == nil {
		t.Fatal("65-byte callback data accepted")
	}
	kb, err := inlineKeyboard([][]Button{{{Text: "да", Data: "fa1.1"}, {Text: "нет", Data: "fa1.2"}}})
	if err != nil || len(kb.InlineKeyboard) != 1 || kb.InlineKeyboard[0][1].CallbackData != "fa1.2" {
		t.Fatalf("kb = %+v, err = %v", kb, err)
	}
}
