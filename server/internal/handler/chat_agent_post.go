package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/integrations/channel"
	"github.com/multica-ai/multica/server/internal/integrations/telegram"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// maxAgentChatPostChars bounds one proactive agent message. 4096 is Telegram's
// hard cap for a single sendMessage; the adapter chunks below it on its own.
const maxAgentChatPostChars = 4096

// agentChatDeliveryTimeout bounds the one-shot channel send. The message is
// already committed to the feed by then, so a slow platform only delays the
// response that reports the delivery outcome.
const agentChatDeliveryTimeout = 20 * time.Second

// Delivery outcomes of a proactive agent message, reported to the agent so it
// knows whether the member was actually reached outside Multica.
const (
	agentChatDeliveryDelivered   = "delivered"
	agentChatDeliveryNotBound    = "not_bound"
	agentChatDeliveryUnsupported = "unsupported_channel"
	agentChatDeliveryUnavailable = "unavailable"
	agentChatDeliveryFailed      = "failed"
)

type PostAgentChatMessageRequest struct {
	Content string `json:"content"`
}

type AgentChatDelivery struct {
	Status      string `json:"status"`
	ChannelType string `json:"channel_type,omitempty"`
}

type PostAgentChatMessageResponse struct {
	MessageID     string            `json:"message_id"`
	ChatSessionID string            `json:"chat_session_id"`
	AuthorType    string            `json:"author_type"`
	AuthorID      string            `json:"author_id"`
	CreatedAt     string            `json:"created_at"`
	Delivery      AgentChatDelivery `json:"delivery"`
}

type agentChatRateLimitedResponse struct {
	Error             string `json:"error"`
	Code              string `json:"code"`
	Scope             string `json:"scope"`
	Limit             int    `json:"limit"`
	WindowSeconds     int64  `json:"window_seconds"`
	RetryAfterSeconds int64  `json:"retry_after_seconds"`
}

// writeAgentChatRateLimited answers 429 for either agent-authored chat path.
func writeAgentChatRateLimited(w http.ResponseWriter, limited *service.AgentChatRateLimitedError) {
	retryAfter := int64(math.Ceil(limited.RetryAfter.Seconds()))
	if retryAfter < 1 {
		retryAfter = 1
	}
	w.Header().Set("Retry-After", strconv.FormatInt(retryAfter, 10))
	writeJSON(w, http.StatusTooManyRequests, agentChatRateLimitedResponse{
		Error:             limited.Error(),
		Code:              "agent_chat_rate_limited",
		Scope:             limited.Scope,
		Limit:             limited.Limit,
		WindowSeconds:     int64(limited.Window.Seconds()),
		RetryAfterSeconds: retryAfter,
	})
}

// PostAgentChatMessage — POST /api/chat/sessions/{sessionId}/agent-messages
//
// Lets a running agent write into a chat on its own initiative. The message is
// stored on the assistant side of the feed, stamped with the authoring agent,
// and — when the session is linked to Telegram — sent to that conversation.
// It queues no turn: the session agent runs only when the member answers.
//
// Only an agent run may call this, and only while its task is live. The session
// must belong to the member the run's token acts for (the existing creator-only
// gate) and that member must also be the agent's owner or the person the run is
// for, so a task queued by someone else cannot write into the runtime owner's
// chats. A per-agent, per-session rate limit applies (service.AgentChatRateLimit).
func (h *Handler) PostAgentChatMessage(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	sessionID := chi.URLParam(r, "sessionId")

	actorType, actorID := h.resolveActor(r, userID, workspaceID)
	if actorType != "agent" {
		writeError(w, http.StatusForbidden, "only an agent run can post a chat message as an agent")
		return
	}
	taskUUID, err := util.ParseUUID(r.Header.Get("X-Task-ID"))
	if err != nil {
		writeError(w, http.StatusForbidden, "only an agent run can post a chat message as an agent")
		return
	}
	task, err := h.Queries.GetAgentTask(r.Context(), taskUUID)
	if err != nil || uuidToString(task.AgentID) != actorID {
		writeError(w, http.StatusForbidden, "only an agent run can post a chat message as an agent")
		return
	}
	if isTerminalTaskStatus(task.Status) {
		writeError(w, http.StatusForbidden, "the agent run has already finished")
		return
	}

	var req PostAgentChatMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	content := strings.TrimSpace(sanitizeNullBytes(req.Content))
	if content == "" {
		writeError(w, http.StatusBadRequest, "content is required")
		return
	}
	if utf8.RuneCountInString(content) > maxAgentChatPostChars {
		writeError(w, http.StatusBadRequest, "content is too long")
		return
	}

	session, ok := h.gatePublicChatSessionForUser(w, r, userID, workspaceID, sessionID)
	if !ok {
		return
	}
	if session.Status != "active" {
		writeError(w, http.StatusConflict, "chat session is archived")
		return
	}
	author, err := h.Queries.GetAgent(r.Context(), task.AgentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load the authoring agent")
		return
	}
	if !agentRunMayWriteToSession(session, author, task) {
		writeError(w, http.StatusForbidden, "not your chat session")
		return
	}

	posted, err := h.TaskService.PostAgentChatMessage(r.Context(), session, author.ID, content)
	if err != nil {
		var limited *service.AgentChatRateLimitedError
		switch {
		case errors.As(err, &limited):
			writeAgentChatRateLimited(w, limited)
		case errors.Is(err, service.ErrChatSessionArchived):
			writeError(w, http.StatusConflict, "chat session is archived")
		default:
			writeError(w, http.StatusInternalServerError, "failed to post chat message")
		}
		return
	}
	msg := posted.Message

	resolvedSessionID := uuidToString(session.ID)
	h.publishChat(protocol.EventChatMessage, workspaceID, "agent", actorID, resolvedSessionID, protocol.ChatMessagePayload{
		ChatSessionID: resolvedSessionID,
		MessageID:     uuidToString(msg.ID),
		Role:          msg.Role,
		Content:       msg.Content,
		CreatedAt:     timestampToString(msg.CreatedAt),
		AuthorType:    service.ChatAuthorTypeAgent,
		AuthorID:      actorID,
	})

	delivery := h.deliverAgentChatPost(r.Context(), session, msg, posted.Binding, author)

	writeJSON(w, http.StatusCreated, PostAgentChatMessageResponse{
		MessageID:     uuidToString(msg.ID),
		ChatSessionID: resolvedSessionID,
		AuthorType:    service.ChatAuthorTypeAgent,
		AuthorID:      actorID,
		CreatedAt:     timestampToString(msg.CreatedAt),
		Delivery:      delivery,
	})
}

// agentRunMayWriteToSession is the "no foreign sessions" rule. A task token acts
// as the runtime's owner, so the creator-only session gate alone would let a run
// that somebody else queued write into the runtime owner's chats. The session's
// member must therefore also be tied to this run: the agent's owner, or the
// person the run is attributed to.
func agentRunMayWriteToSession(session db.ChatSession, author db.Agent, task db.AgentTaskQueue) bool {
	creator := session.CreatorID
	if !creator.Valid {
		return false
	}
	for _, candidate := range []pgtype.UUID{author.OwnerID, task.OriginatorUserID, task.AccountableUserID} {
		if candidate.Valid && candidate == creator {
			return true
		}
	}
	return false
}

// deliverAgentChatPost sends a committed proactive message to the conversation
// the session is linked to. It reuses the channel adapters' own Send through
// the registry — outbound only, no inbound handler, Connect is never called —
// so the bot token never leaves the adapter. Every failure is reported, never
// retried: the first request may already have reached the platform.
func (h *Handler) deliverAgentChatPost(ctx context.Context, session db.ChatSession, msg db.ChatMessage, binding *db.ChannelChatSessionBinding, author db.Agent) AgentChatDelivery {
	if binding == nil {
		return AgentChatDelivery{Status: agentChatDeliveryNotBound}
	}
	delivery := AgentChatDelivery{ChannelType: binding.ChannelType}
	channelType := channel.Type(binding.ChannelType)
	// Only Telegram addresses a conversation from the session binding alone;
	// the other adapters need per-task reply state this send does not have.
	if channelType != telegram.TypeTelegram {
		delivery.Status = agentChatDeliveryUnsupported
		return delivery
	}
	if h.ChannelRegistry == nil {
		delivery.Status = agentChatDeliveryUnavailable
		return delivery
	}
	if _, ok := h.ChannelRegistry.Lookup(channelType); !ok {
		delivery.Status = agentChatDeliveryUnavailable
		return delivery
	}
	installation, err := h.Queries.GetChannelInstallation(ctx, db.GetChannelInstallationParams{
		ID:          binding.InstallationID,
		ChannelType: binding.ChannelType,
	})
	if err != nil || installation.Status != "active" {
		delivery.Status = agentChatDeliveryUnavailable
		return delivery
	}
	ch, err := h.ChannelRegistry.Build(channel.Config{
		Type: channelType,
		ID:   installation.ID,
		Raw:  installation.Config,
	})
	if err != nil {
		slog.WarnContext(ctx, "agent chat post: build channel adapter failed",
			"channel_type", binding.ChannelType, "installation_id", uuidToString(installation.ID), "error", err)
		delivery.Status = agentChatDeliveryUnavailable
		return delivery
	}

	// The bot speaks as the session's agent. A different author is named, so
	// the member can tell who is writing.
	text := msg.Content
	if author.ID != session.AgentID {
		text = "**" + author.Name + "**\n\n" + text
	}
	sendCtx, cancel := context.WithTimeout(ctx, agentChatDeliveryTimeout)
	defer cancel()
	result, err := ch.Send(sendCtx, telegram.SessionOutboundMessage(*binding, text))
	if err != nil {
		slog.WarnContext(ctx, "agent chat post: channel delivery failed",
			"channel_type", binding.ChannelType, "installation_id", uuidToString(installation.ID),
			"message_id", uuidToString(msg.ID), "error", err)
		delivery.Status = agentChatDeliveryFailed
		return delivery
	}
	delivery.Status = agentChatDeliveryDelivered

	messageIDs := result.MessageIDs
	if len(messageIDs) == 0 && result.MessageID != "" {
		messageIDs = []string{result.MessageID}
	}
	if len(messageIDs) > 0 {
		if err := h.Queries.SetAgentPostedChatMessageOutbound(ctx, db.SetAgentPostedChatMessageOutboundParams{
			ID:             msg.ID,
			ChannelType:    pgtype.Text{String: binding.ChannelType, Valid: true},
			InstallationID: installation.ID,
			ChannelChatID:  pgtype.Text{String: binding.ChannelChatID, Valid: true},
			MessageIds:     messageIDs,
		}); err != nil {
			slog.WarnContext(ctx, "agent chat post: record channel provenance failed",
				"message_id", uuidToString(msg.ID), "error", err)
		}
	}
	return delivery
}
