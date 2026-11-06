package engine

import (
	"context"
	"testing"
	"time"
)

// Fabrika fork: an installation with DenyNewChat keeps one lifelong chat.
func newDenyNewHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	set := h.router.sets
	h.router = NewRouter(h.issues, h.tasks, h.reader, RouterConfig{
		Logger: discardLogger(), Lifecycle: h.lifecycle,
		DenyNewChat: func(ResolvedInstallation) bool { return true },
	})
	for k, v := range set {
		h.router.Register(k, v)
	}
	h.media.noMedia = true
	return h
}

func TestRouter_DenyNewChat_BareNewIsRefusedWithNotice(t *testing.T) {
	h := newDenyNewHarness(t)
	msg := p2pMessage(t)
	msg.Text = "/new"
	if err := h.router.Handle(context.Background(), msg); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if h.binder.startCalls != 0 || h.tasks.wasCalled() {
		t.Fatalf("bare /new must not start a chat or a run: starts=%d", h.binder.startCalls)
	}
	if !waitFor(time.Second, func() bool {
		for _, r := range h.replier.calls() {
			if r.Outcome == OutcomeNewChatDisabled {
				return true
			}
		}
		return false
	}) {
		t.Fatal("the sender was never told /new is off")
	}
}

func TestRouter_DenyNewChat_NewWithBodyLandsInCurrentChat(t *testing.T) {
	h := newDenyNewHarness(t)
	msg := p2pMessage(t)
	msg.Text = "/new что с Терном"
	if err := h.router.Handle(context.Background(), msg); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if h.binder.startCalls != 0 {
		t.Fatalf("must not start a new chat, starts=%d", h.binder.startCalls)
	}
	h.binder.mu.Lock()
	got := h.binder.lastAppend.Message.Text
	h.binder.mu.Unlock()
	if got != "что с Терном" {
		t.Fatalf("appended text=%q, want the body in the current chat", got)
	}
}
