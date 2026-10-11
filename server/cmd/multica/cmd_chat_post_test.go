package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

func TestChatPost(t *testing.T) {
	for _, output := range []string{"json", "table"} {
		t.Run(output, func(t *testing.T) {
			var calls atomic.Int32
			body := "Nightly import finished.\n0 errors."
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != "/api/chat/sessions/"+chatSessionTestID+"/agent-messages" {
					t.Errorf("request = %s %s", r.Method, r.URL)
				}
				if r.Header.Get("Authorization") != "Bearer mat_chat_test" || r.Header.Get("X-Workspace-ID") != "ws-1" {
					t.Error("task credential or workspace missing")
				}
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				if payload["content"] != body || len(payload) != 1 {
					t.Errorf("payload = %#v", payload)
				}
				w.WriteHeader(http.StatusCreated)
				fmt.Fprint(w, `{"message_id":"message-1","chat_session_id":"`+chatSessionTestID+`","author_type":"agent","author_id":"agent-1","created_at":"2026-10-11T02:00:00Z","delivery":{"status":"delivered","channel_type":"telegram"}}`)
			}))
			defer srv.Close()
			chatSessionTestEnv(t, srv.URL)
			out, err := executeChatSessionTest(t, newChatPostCmd(), chatSessionTestID, "--body", body, "--output", output)
			if err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 1 || !strings.Contains(out, "message-1") || !strings.Contains(out, "delivered") || !strings.Contains(out, "telegram") {
				t.Fatalf("calls=%d output=%s", calls.Load(), out)
			}
			if output == "json" {
				var resp map[string]any
				if err := json.Unmarshal([]byte(out), &resp); err != nil || resp["author_type"] != "agent" {
					t.Fatalf("invalid JSON response: %s (%v)", out, err)
				}
			}
		})
	}
}

func TestChatPostInvalidInputDoesNotRequest(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer srv.Close()
	chatSessionTestEnv(t, srv.URL)
	for name, args := range map[string][]string{
		"short id":   {"018f3caa", "--body", "hello"},
		"empty body": {chatSessionTestID, "--body", "   "},
		"no body":    {chatSessionTestID},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := executeChatSessionTest(t, newChatPostCmd(), args...); err == nil || calls.Load() != 0 {
				t.Fatalf("error=%v calls=%d", err, calls.Load())
			}
		})
	}
}

func TestChatPostFailureDoesNotRetry(t *testing.T) {
	for _, status := range []int{403, 404, 409, 429, 500, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(status)
				fmt.Fprint(w, `{"error":"post rejected"}`)
			}))
			defer srv.Close()
			chatSessionTestEnv(t, srv.URL)
			out, err := executeChatSessionTest(t, newChatPostCmd(), chatSessionTestID, "--body", "hello")
			if err == nil || calls.Load() != 1 || out != "" {
				t.Fatalf("error=%v calls=%d output=%q", err, calls.Load(), out)
			}
			var httpErr *cli.HTTPError
			if !errors.As(err, &httpErr) || httpErr.StatusCode != status {
				t.Fatalf("HTTP classification lost: %v", err)
			}
			var userErr *cli.UserMessageError
			if uncertain := errors.As(err, &userErr); uncertain != (status >= 500) {
				t.Fatalf("status %d: uncertain-delivery guidance = %v", status, uncertain)
			}
		})
	}
}

func TestChatPostCommandRegistration(t *testing.T) {
	cmd, _, err := chatCmd.Find([]string{"post"})
	if err != nil || cmd == chatCmd || cmd.Name() != "post" {
		t.Fatalf("chat post is not registered: %v", err)
	}
}

func TestChatRateLimitTellsTheAgentNotToRetry(t *testing.T) {
	for name, newCmd := range map[string]func() *cobra.Command{"post": newChatPostCmd, "send": newChatSendCmd} {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Retry-After", "1500")
				w.WriteHeader(http.StatusTooManyRequests)
				fmt.Fprint(w, `{"error":"agent chat rate limit reached","code":"agent_chat_rate_limited","scope":"session","limit":6,"window_seconds":3600,"retry_after_seconds":1500}`)
			}))
			defer srv.Close()
			chatSessionTestEnv(t, srv.URL)
			_, err := executeChatSessionTest(t, newCmd(), chatSessionTestID, "--body", "hello")
			if err == nil || calls.Load() != 1 {
				t.Fatalf("error=%v calls=%d", err, calls.Load())
			}
			message := cli.FormatError(err, false)
			for _, want := range []string{"6 messages", "into this chat session", "60 minutes", "about 25 minutes", "Do not retry now"} {
				if !strings.Contains(message, want) {
					t.Fatalf("rate-limit guidance is missing %q: %q", want, message)
				}
			}
			if strings.Contains(message, "try again") {
				t.Fatalf("rate-limit guidance invites a retry: %q", message)
			}
		})
	}
}
