package main

import (
	"net/http"

	"github.com/go-chi/chi/v5"

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
	r.Post("/api/streams/waits/{id}/fire", h.FireWait)
	r.Get("/api/streams/{slug}", h.Get)
	r.Post("/api/streams/{slug}/manage", h.Manage)
	r.Group(func(r chi.Router) {
		r.Use(handler.RequireHumanActor)
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
