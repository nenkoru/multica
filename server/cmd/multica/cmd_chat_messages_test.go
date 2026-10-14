package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const chatMessagesTestPage = `{"messages":[{"id":"m1","role":"user","content":"привет,\nкак дела","created_at":"2026-10-14T15:00:00Z"},` +
	`{"id":"m2","role":"assistant","content":"Хорошо","created_at":"2026-10-14T15:00:09Z"}],` +
	`"limit":2,"has_more":true,"next_cursor":{"created_at":"2026-10-14T15:00:00Z","id":"m1"}}`

func TestChatMessagesJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/chat/sessions/"+chatSessionTestID+"/messages/page" {
			t.Errorf("request = %s %s", r.Method, r.URL)
		}
		if got := r.URL.Query().Get("limit"); got != "2" {
			t.Errorf("limit = %q", got)
		}
		if r.URL.Query().Has("before_id") || r.URL.Query().Has("before_created_at") {
			t.Errorf("cursor sent without flags: %s", r.URL.RawQuery)
		}
		fmt.Fprint(w, chatMessagesTestPage)
	}))
	defer srv.Close()
	chatSessionTestEnv(t, srv.URL)
	out, err := executeChatSessionTest(t, newChatMessagesCmd(), chatSessionTestID, "--limit", "2")
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Messages []map[string]any `json:"messages"`
		HasMore  bool             `json:"has_more"`
	}
	if err := json.Unmarshal([]byte(out), &page); err != nil || len(page.Messages) != 2 || !page.HasMore {
		t.Fatalf("page = %s (%v)", out, err)
	}
	if page.Messages[0]["content"] != "привет,\nкак дела" {
		t.Fatalf("content changed: %v", page.Messages[0]["content"])
	}
}

func TestChatMessagesCursorAndTable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("before_created_at") != "2026-10-14T15:00:00Z" || q.Get("before_id") != "m1" || q.Get("limit") != "20" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		fmt.Fprint(w, chatMessagesTestPage)
	}))
	defer srv.Close()
	chatSessionTestEnv(t, srv.URL)
	out, err := executeChatSessionTest(t, newChatMessagesCmd(), chatSessionTestID,
		"--before-created-at", "2026-10-14T15:00:00Z", "--before-id", "m1", "--output", "table")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "привет, как дела") || !strings.Contains(out, "assistant") {
		t.Fatalf("table = %s", out)
	}
}

func TestChatMessagesRejectsBadInput(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer srv.Close()
	chatSessionTestEnv(t, srv.URL)
	for _, args := range [][]string{
		{"not-a-uuid"},
		{chatSessionTestID, "--limit", "0"},
		{chatSessionTestID, "--limit", "101"},
		{chatSessionTestID, "--before-id", "m1"},
		{chatSessionTestID, "--output", "yaml"},
	} {
		if _, err := executeChatSessionTest(t, newChatMessagesCmd(), args...); err == nil {
			t.Errorf("%v: expected an error", args)
		}
	}
	if calls != 0 {
		t.Fatalf("invalid input reached the server %d times", calls)
	}
}
