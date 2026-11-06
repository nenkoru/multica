package telegram

import "github.com/jackc/pgx/v5/pgtype"

// Notices overrides user-visible notices for one agent (Fabrika fork): the
// agent Руслан talks to says "Мультика недоступна, потоки идут" instead of a
// generic run failure (harness-design.md §3.6). Empty fields keep the
// upstream text.
type Notices struct {
	TaskFailed      string
	AgentOffline    string
	NewChatDisabled string
}

// NoticesFunc returns the notices for an agent; ok=false keeps upstream text.
type NoticesFunc func(agentID pgtype.UUID) (Notices, bool)

// msgNewChatDisabled is the fallback for OutcomeNewChatDisabled.
const msgNewChatDisabled = "/new is disabled for this chat: it is the one lifelong chat with this agent."

func pickNotice(f NoticesFunc, agentID pgtype.UUID, pick func(Notices) string, fallback string) string {
	if f == nil {
		return fallback
	}
	n, ok := f(agentID)
	if !ok {
		return fallback
	}
	if v := pick(n); v != "" {
		return v
	}
	return fallback
}
