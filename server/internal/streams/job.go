package streams

import (
	"context"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/scheduler"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// SupervisorJob registers the reconcile tick on upstream's DB-backed
// scheduler next to IssueWakeupJob (§6.1). The plan lives in our tables, so
// latest-only catch-up is enough: a missed bucket loses nothing.
func SupervisorJob(s *Supervisor) scheduler.JobSpec {
	return scheduler.JobSpec{
		Name: "fabrika_streams_supervisor", Cadence: TickInterval, CatchUpMode: scheduler.CatchUpLatestOnly, CatchUpWindow: time.Hour,
		RunTimeout: 25 * time.Second, StaleTimeout: time.Minute, HeartbeatInterval: 10 * time.Second,
		AllowStaleReentry: true, MaxAttempts: 1, Scopes: scheduler.StaticScopes(scheduler.ScopeGlobal),
		Handler: func(ctx context.Context, _ scheduler.HandlerInput) (scheduler.HandlerResult, error) {
			return scheduler.HandlerResult{}, s.Tick(ctx)
		},
	}
}

// Subscribe wakes the supervisor on task completion and failure. The event
// only speeds up the reconcile; the tick does not depend on it.
func Subscribe(bus *events.Bus, s *Supervisor) {
	kick := func(events.Event) { s.Kick() }
	bus.Subscribe(protocol.EventTaskCompleted, kick)
	bus.Subscribe(protocol.EventTaskFailed, kick)
	s.subscribeScreens(bus)
	if s.MultikaEnabled() {
		bus.Subscribe(protocol.EventChatDone, func(e events.Event) {
			p, ok := e.Payload.(protocol.ChatDonePayload)
			if !ok || !strings.Contains(p.Content, "```fabrika-charter") {
				return
			}
			sessionID, err1 := parseUUID(p.ChatSessionID)
			taskID, err2 := parseUUID(p.TaskID)
			if err1 != nil || err2 != nil {
				return
			}
			// Off the publisher's goroutine: the chat reply is delivered
			// whatever happens to the charter.
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				s.OnMultikaReply(ctx, sessionID, taskID, p.Content)
			}()
		})
	}
}
