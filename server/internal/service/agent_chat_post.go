package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// ChatAuthorTypeAgent marks a chat_message row written by an agent run acting
// through its task token, rather than by the member the session belongs to or
// by the session agent's own turn. It is the only author_type written today.
const ChatAuthorTypeAgent = "agent"

// AgentChatRateLimit bounds how often one agent may write into chat on its own
// initiative. Every agent-authored row counts — a proactive post and a
// `multica chat send` turn alike — because both reach a person unprompted and
// the second also spends a run of the session agent.
type AgentChatRateLimit struct {
	// Window is the sliding window both counters are read over.
	Window time.Duration
	// PerSession caps one agent's messages into one chat session.
	PerSession int
	// PerAgent caps one agent's messages across every session.
	PerAgent int
}

// Defaults are deliberately tight: a proactive message is an interruption, and
// the cap is the only server-side stop for an agent caught in a loop.
const (
	defaultAgentChatRateWindow     = time.Hour
	defaultAgentChatRatePerSession = 6
	defaultAgentChatRatePerAgent   = 20
)

// agentChatRateLimit resolves the effective limit: an explicit value set on the
// service (tests), else the MULTICA_AGENT_CHAT_RATE_* environment, else the
// defaults. A non-positive or unparsable override keeps the default, so a typo
// can never switch the limit off.
func (s *TaskService) agentChatRateLimit() AgentChatRateLimit {
	limit := s.AgentChatRate
	if limit.Window <= 0 {
		limit.Window = defaultAgentChatRateWindow
		if secs := positiveEnvInt("MULTICA_AGENT_CHAT_RATE_WINDOW_SECONDS"); secs > 0 {
			limit.Window = time.Duration(secs) * time.Second
		}
	}
	if limit.PerSession <= 0 {
		limit.PerSession = defaultAgentChatRatePerSession
		if n := positiveEnvInt("MULTICA_AGENT_CHAT_RATE_PER_SESSION"); n > 0 {
			limit.PerSession = n
		}
	}
	if limit.PerAgent <= 0 {
		limit.PerAgent = defaultAgentChatRatePerAgent
		if n := positiveEnvInt("MULTICA_AGENT_CHAT_RATE_PER_AGENT"); n > 0 {
			limit.PerAgent = n
		}
	}
	return limit
}

func positiveEnvInt(name string) int {
	n, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// AgentChatRateLimitedError reports which cap an agent-authored chat message
// hit and when the oldest counted message leaves the window.
type AgentChatRateLimitedError struct {
	// Scope is "session" or "agent".
	Scope      string
	Limit      int
	Window     time.Duration
	RetryAfter time.Duration
}

func (e *AgentChatRateLimitedError) Error() string {
	return fmt.Sprintf("agent chat rate limit reached: %d messages per %s per %s", e.Limit, e.Window, e.Scope)
}

// enforceAgentChatRate must run inside the transaction that inserts the row it
// guards. The advisory lock serialises one agent's concurrent sends so the
// count it reads cannot be overtaken before the insert commits.
func (s *TaskService) enforceAgentChatRate(ctx context.Context, qtx *db.Queries, agentID, sessionID pgtype.UUID) error {
	limit := s.agentChatRateLimit()
	if err := qtx.LockAgentChatAuthorRate(ctx, agentID); err != nil {
		return fmt.Errorf("lock agent chat rate: %w", err)
	}
	counts, err := qtx.CountAgentAuthoredChatMessages(ctx, db.CountAgentAuthoredChatMessagesParams{
		ChatSessionID: sessionID,
		AgentID:       agentID,
		WindowSecs:    limit.Window.Seconds(),
	})
	if err != nil {
		return fmt.Errorf("count agent chat messages: %w", err)
	}
	if int(counts.SessionCount) >= limit.PerSession {
		return &AgentChatRateLimitedError{
			Scope: "session", Limit: limit.PerSession, Window: limit.Window,
			RetryAfter: agentChatRetryAfter(counts.OldestInSession, limit.Window),
		}
	}
	if int(counts.AgentCount) >= limit.PerAgent {
		return &AgentChatRateLimitedError{
			Scope: "agent", Limit: limit.PerAgent, Window: limit.Window,
			RetryAfter: agentChatRetryAfter(counts.OldestForAgent, limit.Window),
		}
	}
	return nil
}

func agentChatRetryAfter(oldest pgtype.Timestamptz, window time.Duration) time.Duration {
	if !oldest.Valid {
		return window
	}
	wait := time.Until(oldest.Time.Add(window))
	if wait < time.Second {
		return time.Second
	}
	return wait
}

// AgentChatPostResult carries the row a proactive agent post persisted and the
// channel route the session had at that moment, if any.
type AgentChatPostResult struct {
	Message db.ChatMessage
	// Binding is nil for a session with no live channel route (web-only chat,
	// or a route retired by a fresh-session command).
	Binding *db.ChannelChatSessionBinding
}

// PostAgentChatMessage persists a message an agent wrote into a chat session
// on its own initiative: an assistant-side row stamped with the authoring
// agent and owned by no task. Nothing is enqueued — the message is the whole
// event, and the session agent only runs when the member answers (the claim
// then hands it these rows as context, see ListAgentPostedChatMessagesForTask).
//
// The caller must have authenticated the agent, gated the session, and
// validated the content. The archived check and the rate limit are enforced
// here, under the session lock, because either can change before the insert.
func (s *TaskService) PostAgentChatMessage(ctx context.Context, session db.ChatSession, authorAgentID pgtype.UUID, content string) (*AgentChatPostResult, error) {
	var out AgentChatPostResult
	if err := s.runInTx(ctx, func(qtx *db.Queries) error {
		// Same first lock as SendDirectChatMessage and the delete path.
		if _, err := qtx.LockChatSessionForRuntimeBind(ctx, session.ID); err != nil {
			return fmt.Errorf("lock chat session: %w", err)
		}
		current, err := qtx.GetChatSession(ctx, session.ID)
		if err != nil {
			return fmt.Errorf("reload chat session: %w", err)
		}
		if current.Status != "active" {
			return ErrChatSessionArchived
		}
		if err := s.enforceAgentChatRate(ctx, qtx, authorAgentID, session.ID); err != nil {
			return err
		}

		// A channel-backed session scopes what its agent may read by context
		// generation. A task-owned reply inherits that from its task; this row
		// has none, so it carries the generation current when it was written.
		//
		// Taken with the same row lock the channel's own appends and /new take
		// (after chat_session, the order they use), so a concurrent route switch
		// cannot retire the generation between this read and the insert. No row
		// means no live route: a web-only chat, or one whose route was retired.
		contextRevision := pgtype.Int8{}
		binding, err := qtx.LockCurrentChannelChatSessionBindingBySession(ctx, session.ID)
		switch {
		case err == nil:
			out.Binding = &binding
			contextRevision = pgtype.Int8{Int64: binding.ContextRevision, Valid: true}
		case errors.Is(err, pgx.ErrNoRows):
		default:
			return fmt.Errorf("lock chat channel binding: %w", err)
		}

		msg, err := qtx.CreateChatMessage(ctx, db.CreateChatMessageParams{
			ID:                     dbid.NewV7(),
			ChatSessionID:          session.ID,
			Role:                   "assistant",
			Content:                content,
			MessageKind:            pgtype.Text{String: protocol.ChatMessageKindMessage, Valid: true},
			ChannelContextRevision: contextRevision,
			AuthorType:             pgtype.Text{String: ChatAuthorTypeAgent, Valid: true},
			AuthorID:               authorAgentID,
		})
		if err != nil {
			return fmt.Errorf("create agent chat message: %w", err)
		}
		out.Message = msg

		if err := qtx.TouchChatSession(ctx, session.ID); err != nil {
			return fmt.Errorf("touch chat session: %w", err)
		}
		return nil
	}); err != nil {
		var limited *AgentChatRateLimitedError
		if !errors.As(err, &limited) && !errors.Is(err, ErrChatSessionArchived) {
			slog.Error("agent chat post failed",
				"chat_session_id", util.UUIDToString(session.ID),
				"author_agent_id", util.UUIDToString(authorAgentID),
				"error", err)
		}
		return nil, err
	}

	slog.Info("agent chat message posted",
		"message_id", util.UUIDToString(out.Message.ID),
		"chat_session_id", util.UUIDToString(session.ID),
		"author_agent_id", util.UUIDToString(authorAgentID),
		"channel_bound", out.Binding != nil)
	return &out, nil
}
