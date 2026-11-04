package main

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/integrations/channel/engine"
	"github.com/multica-ai/multica/server/internal/integrations/telegram"

	"github.com/multica-ai/multica/server/internal/handler"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/streams"
)

// Fabrika (fork-owned): /api/streams. Kept out of router.go so upstream
// merges touch two call sites only.

// registerStreamsHealth mounts the unauthenticated probe the external
// watchdog polls (harness-design.md §6.6). It exposes timestamps only.
func registerStreamsHealth(r chi.Router, sup *streams.Supervisor) {
	if sup == nil {
		return
	}
	h := streamsHTTP(sup)
	r.Get("/api/streams/health", h.Health)
}

// registerStreamsRoutes mounts the member routes inside the workspace group.
// Pause/resume/activate are Ruslan's controls and refuse agent credentials.
func registerStreamsRoutes(r chi.Router, sup *streams.Supervisor) {
	if sup == nil {
		return
	}
	h := streamsHTTP(sup)
	r.Get("/api/streams", h.List)
	r.Post("/api/streams/digest", h.Digest)
	r.Post("/api/streams/answer", h.Answer)
	r.Post("/api/streams/asks/push", h.PushAsks)
	r.Post("/api/streams/waits/{id}/fire", h.FireWait)
	r.Get("/api/streams/multika", h.Multika)
	r.Get("/api/streams/{slug}", h.Get)
	r.Post("/api/streams/{slug}/manage", h.Manage)
	r.Group(func(r chi.Router) {
		r.Use(handler.RequireHumanActor)
		r.Put("/api/streams/multika/focus", h.MultikaFocus)
		r.Put("/api/streams/{slug}", h.Upsert)
		r.Post("/api/streams/{slug}/pause", h.Pause)
		r.Post("/api/streams/{slug}/resume", h.Resume)
		r.Post("/api/streams/{slug}/activate", h.SetActive(true))
		r.Post("/api/streams/{slug}/deactivate", h.SetActive(false))
	})
}

func streamsHTTP(sup *streams.Supervisor) *streams.HTTP {
	return &streams.HTTP{Sup: sup, WorkspaceID: func(r *http.Request) string {
		return middleware.WorkspaceIDFromContext(r.Context())
	}}
}

// multikaDenyNewChat keeps Мультика's channel chat lifelong (§10.5: /new is
// off). Nil when the feature flag is off.
func multikaDenyNewChat(sup *streams.Supervisor) func(engine.ResolvedInstallation) bool {
	if sup == nil || !sup.MultikaEnabled() {
		return nil
	}
	return func(inst engine.ResolvedInstallation) bool { return sup.IsMultika(inst.AgentID) }
}

// multikaTelegramNotices gives Мультика honest Russian notices (§3.6).
func multikaTelegramNotices(sup *streams.Supervisor) telegram.NoticesFunc {
	if sup == nil || !sup.MultikaEnabled() {
		return nil
	}
	return func(agentID pgtype.UUID) (telegram.Notices, bool) {
		if !sup.IsMultika(agentID) {
			return telegram.Notices{}, false
		}
		return telegram.Notices{
			TaskFailed:      streams.MultikaUnavailableText,
			AgentOffline:    streams.MultikaOfflineText,
			NewChatDisabled: streams.MultikaNewChatDisabledText,
		}, true
	}
}

// multikaTelegramCallbacks routes inline button presses on Мультика's bot to
// the question buttons (§7). Nil without the flag: the polling loop then does
// not even ask Telegram for callback queries.
func multikaTelegramCallbacks(sup *streams.Supervisor) telegram.CallbackFunc {
	if sup == nil || !sup.MultikaEnabled() {
		return nil
	}
	return sup.PressAskButton
}

// askButtonSender adapts telegram.ButtonSender to streams.AskButtonSender.
type askButtonSender struct{ s *telegram.ButtonSender }

func (a askButtonSender) SendButtons(ctx context.Context, installationID pgtype.UUID, chatID int64, text string, rows [][]streams.AskButton) error {
	out := make([][]telegram.Button, 0, len(rows))
	for _, row := range rows {
		r := make([]telegram.Button, 0, len(row))
		for _, b := range row {
			r = append(r, telegram.Button{Text: b.Text, Data: b.Data})
		}
		out = append(out, r)
	}
	_, err := a.s.Send(ctx, installationID, chatID, text, out)
	return err
}

// wireMultikaAskButtons lets the supervisor send question buttons through
// the Telegram integration's bots.
func wireMultikaAskButtons(sup *streams.Supervisor, s *telegram.ButtonSender) {
	if sup == nil || !sup.MultikaEnabled() {
		return
	}
	sup.AskButtons = askButtonSender{s: s}
}
