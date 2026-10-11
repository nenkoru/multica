package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/integrations/channel"
	"github.com/multica-ai/multica/server/internal/integrations/telegram"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// agentPostReq builds a POST .../agent-messages request carrying the agent
// identity pair an agent run's task token resolves to.
func agentPostReq(t *testing.T, sessionID, agentID, taskID, content string) *http.Request {
	t.Helper()
	req := newRequest("POST", "/api/chat/sessions/"+sessionID+"/agent-messages", map[string]any{"content": content})
	if agentID != "" {
		req.Header.Set("X-Agent-ID", agentID)
	}
	if taskID != "" {
		req.Header.Set("X-Task-ID", taskID)
	}
	return withChatTestWorkspaceCtx(t, withURLParam(req, "sessionId", sessionID))
}

// setAgentChatRateForTest pins the rate limit for one test and restores the
// environment-driven default afterwards.
func setAgentChatRateForTest(t *testing.T, limit service.AgentChatRateLimit) {
	t.Helper()
	previous := testHandler.TaskService.AgentChatRate
	testHandler.TaskService.AgentChatRate = limit
	t.Cleanup(func() { testHandler.TaskService.AgentChatRate = previous })
}

func loadChatMessageForTest(t *testing.T, messageID string) db.ChatMessage {
	t.Helper()
	msg, err := testHandler.Queries.GetChatMessage(context.Background(), parseUUID(messageID))
	if err != nil {
		t.Fatalf("load chat message %s: %v", messageID, err)
	}
	return msg
}

func TestPostAgentChatMessage_StoresAssistantRowStampedWithAuthor(t *testing.T) {
	if testHandler == nil {
		t.Skip("requires test database")
	}
	ctx := context.Background()
	sessionAgentID := createHandlerTestAgent(t, "AgentPostSessionAgent", []byte("[]"))
	sessionID := createHandlerTestChatSession(t, sessionAgentID)
	authorID := createHandlerTestAgent(t, "AgentPostAuthor", []byte("[]"))
	taskID := createHandlerTestTaskForAgent(t, authorID)

	var resp PostAgentChatMessageResponse
	testutil.Call(t, testHandler.PostAgentChatMessage, agentPostReq(t, sessionID, authorID, taskID, "  build finished, PR is up  ")).
		Want(http.StatusCreated).JSON(&resp)

	if resp.AuthorType != service.ChatAuthorTypeAgent || resp.AuthorID != authorID {
		t.Fatalf("response author = (%q, %q), want (agent, %s)", resp.AuthorType, resp.AuthorID, authorID)
	}
	if resp.Delivery.Status != agentChatDeliveryNotBound {
		t.Fatalf("delivery status = %q, want %q for a web-only session", resp.Delivery.Status, agentChatDeliveryNotBound)
	}

	msg := loadChatMessageForTest(t, resp.MessageID)
	if msg.Role != "assistant" || msg.Content != "build finished, PR is up" {
		t.Fatalf("stored row = (%q, %q), want trimmed assistant row", msg.Role, msg.Content)
	}
	if msg.TaskID.Valid {
		t.Fatalf("a proactive post must not be owned by a task, got task_id %s", uuidToString(msg.TaskID))
	}
	if msg.AuthorType.String != service.ChatAuthorTypeAgent || uuidToString(msg.AuthorID) != authorID {
		t.Fatalf("stored author = (%q, %s), want (agent, %s)", msg.AuthorType.String, uuidToString(msg.AuthorID), authorID)
	}

	// The message is the whole event: no turn of the session agent is queued.
	var queued int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM agent_task_queue WHERE chat_session_id = $1`, sessionID).Scan(&queued); err != nil {
		t.Fatalf("count session tasks: %v", err)
	}
	if queued != 0 {
		t.Fatalf("a proactive post queued %d task(s), want 0", queued)
	}

	// The feed API names the author, so the UI can show who wrote the bubble.
	api := chatMessageToResponse(msg, nil)
	if api.AuthorType == nil || *api.AuthorType != service.ChatAuthorTypeAgent || api.AuthorID == nil || *api.AuthorID != authorID {
		t.Fatalf("API author = (%v, %v), want (agent, %s)", api.AuthorType, api.AuthorID, authorID)
	}
}

func TestPostAgentChatMessage_RejectsCallersThatAreNotALiveAgentRun(t *testing.T) {
	if testHandler == nil {
		t.Skip("requires test database")
	}
	sessionAgentID := createHandlerTestAgent(t, "AgentPostGateSessionAgent", []byte("[]"))
	sessionID := createHandlerTestChatSession(t, sessionAgentID)
	authorID := createHandlerTestAgent(t, "AgentPostGateAuthor", []byte("[]"))
	taskID := createHandlerTestTaskForAgent(t, authorID)

	t.Run("member", func(t *testing.T) {
		testutil.Call(t, testHandler.PostAgentChatMessage, agentPostReq(t, sessionID, "", "", "hello")).Want(http.StatusForbidden)
	})
	t.Run("finished run", func(t *testing.T) {
		finishedTaskID := createHandlerTestTaskForAgent(t, authorID)
		if _, err := testPool.Exec(context.Background(), `UPDATE agent_task_queue SET status = 'completed', completed_at = now() WHERE id = $1`, finishedTaskID); err != nil {
			t.Fatalf("finish task: %v", err)
		}
		testutil.Call(t, testHandler.PostAgentChatMessage, agentPostReq(t, sessionID, authorID, finishedTaskID, "hello")).Want(http.StatusForbidden)
	})
	t.Run("empty content", func(t *testing.T) {
		testutil.Call(t, testHandler.PostAgentChatMessage, agentPostReq(t, sessionID, authorID, taskID, "   ")).Want(http.StatusBadRequest)
	})
	t.Run("oversized content", func(t *testing.T) {
		testutil.Call(t, testHandler.PostAgentChatMessage, agentPostReq(t, sessionID, authorID, taskID, strings.Repeat("я", maxAgentChatPostChars+1))).Want(http.StatusBadRequest)
	})
	t.Run("archived session", func(t *testing.T) {
		archivedID := createHandlerTestChatSession(t, sessionAgentID)
		if _, err := testPool.Exec(context.Background(), `UPDATE chat_session SET status = 'archived' WHERE id = $1`, archivedID); err != nil {
			t.Fatalf("archive session: %v", err)
		}
		testutil.Call(t, testHandler.PostAgentChatMessage, agentPostReq(t, archivedID, authorID, taskID, "hello")).Want(http.StatusConflict)
	})

	var stored int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM chat_message WHERE author_id = $1`, authorID).Scan(&stored); err != nil {
		t.Fatalf("count agent rows: %v", err)
	}
	if stored != 0 {
		t.Fatalf("rejected posts stored %d row(s), want 0", stored)
	}
}

func TestPostAgentChatMessage_RefusesAnotherMembersSession(t *testing.T) {
	if testHandler == nil {
		t.Skip("requires test database")
	}
	ctx := context.Background()
	sessionAgentID := createHandlerTestAgent(t, "AgentPostForeignSessionAgent", []byte("[]"))
	authorID := createHandlerTestAgent(t, "AgentPostForeignAuthor", []byte("[]"))
	taskID := createHandlerTestTaskForAgent(t, authorID)

	var otherUserID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email) VALUES ('Agent Post Other Member', 'agent-post-other@multica.test')
		RETURNING id
	`).Scan(&otherUserID); err != nil {
		t.Fatalf("create other user: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, otherUserID) })
	var foreignSessionID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO chat_session (workspace_id, agent_id, creator_id, title, status, explicitly_created_at)
		VALUES ($1, $2, $3, 'someone else''s chat', 'active', now())
		RETURNING id
	`, testWorkspaceID, sessionAgentID, otherUserID).Scan(&foreignSessionID); err != nil {
		t.Fatalf("create foreign session: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM chat_session WHERE id = $1`, foreignSessionID)
	})

	testutil.Call(t, testHandler.PostAgentChatMessage, agentPostReq(t, foreignSessionID, authorID, taskID, "hello")).Want(http.StatusForbidden)

	var stored int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM chat_message WHERE chat_session_id = $1`, foreignSessionID).Scan(&stored); err != nil {
		t.Fatalf("count foreign session rows: %v", err)
	}
	if stored != 0 {
		t.Fatalf("a refused post stored %d row(s) in the foreign session", stored)
	}
}

// The task token acts as the runtime's owner, so passing the creator-only gate
// is not enough: the session's member must also be tied to this run.
func TestAgentRunMayWriteToSession(t *testing.T) {
	member := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	stranger := pgtype.UUID{Bytes: [16]byte{2}, Valid: true}
	session := db.ChatSession{CreatorID: member}

	cases := []struct {
		name   string
		author db.Agent
		task   db.AgentTaskQueue
		want   bool
	}{
		{"agent owned by the session member", db.Agent{OwnerID: member}, db.AgentTaskQueue{}, true},
		{"run started for the session member", db.Agent{OwnerID: stranger}, db.AgentTaskQueue{OriginatorUserID: member}, true},
		{"run accountable to the session member", db.Agent{OwnerID: stranger}, db.AgentTaskQueue{AccountableUserID: member}, true},
		{"run queued by someone else for someone else's agent", db.Agent{OwnerID: stranger}, db.AgentTaskQueue{OriginatorUserID: stranger}, false},
		{"no attribution at all", db.Agent{}, db.AgentTaskQueue{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := agentRunMayWriteToSession(session, tc.author, tc.task); got != tc.want {
				t.Fatalf("agentRunMayWriteToSession = %v, want %v", got, tc.want)
			}
		})
	}
	if agentRunMayWriteToSession(db.ChatSession{}, db.Agent{}, db.AgentTaskQueue{}) {
		t.Fatal("a session with no creator must never match an unset owner")
	}
}

func TestPostAgentChatMessage_RateLimitsPerSessionAndPerAgent(t *testing.T) {
	if testHandler == nil {
		t.Skip("requires test database")
	}
	setAgentChatRateForTest(t, service.AgentChatRateLimit{Window: time.Hour, PerSession: 2, PerAgent: 3})
	sessionAgentID := createHandlerTestAgent(t, "AgentPostRateSessionAgent", []byte("[]"))
	firstSessionID := createHandlerTestChatSession(t, sessionAgentID)
	secondSessionID := createHandlerTestChatSession(t, sessionAgentID)
	authorID := createHandlerTestAgent(t, "AgentPostRateAuthor", []byte("[]"))
	taskID := createHandlerTestTaskForAgent(t, authorID)

	post := func(sessionID string) *testutil.Response {
		return testutil.Call(t, testHandler.PostAgentChatMessage, agentPostReq(t, sessionID, authorID, taskID, "ping"))
	}
	post(firstSessionID).Want(http.StatusCreated)
	post(firstSessionID).Want(http.StatusCreated)

	var limited agentChatRateLimitedResponse
	sessionLimited := post(firstSessionID).Want(http.StatusTooManyRequests)
	sessionLimited.JSON(&limited)
	if limited.Scope != "session" || limited.Limit != 2 || limited.RetryAfterSeconds < 1 {
		t.Fatalf("session limit response = %+v, want scope=session limit=2 with a retry hint", limited)
	}
	if sessionLimited.Header().Get("Retry-After") == "" {
		t.Fatal("a rate-limited post must carry Retry-After")
	}

	// The other session still has room, until the per-agent cap is reached.
	post(secondSessionID).Want(http.StatusCreated)
	post(secondSessionID).Want(http.StatusTooManyRequests).JSON(&limited)
	if limited.Scope != "agent" || limited.Limit != 3 {
		t.Fatalf("agent limit response = %+v, want scope=agent limit=3", limited)
	}

	// Another agent has its own budget in the same session.
	otherAuthorID := createHandlerTestAgent(t, "AgentPostRateOtherAuthor", []byte("[]"))
	otherTaskID := createHandlerTestTaskForAgent(t, otherAuthorID)
	testutil.Call(t, testHandler.PostAgentChatMessage, agentPostReq(t, firstSessionID, otherAuthorID, otherTaskID, "ping")).Want(http.StatusCreated)

	var stored int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM chat_message WHERE author_id = $1`, authorID).Scan(&stored); err != nil {
		t.Fatalf("count agent rows: %v", err)
	}
	if stored != 3 {
		t.Fatalf("stored %d rows for the limited agent, want exactly the 3 admitted", stored)
	}
}

// `multica chat send` from an agent run writes the member-side row of the
// runtime owner's chat and spends a run of the session agent. It shares the
// same budget and is stamped with who really wrote it.
func TestSendDirectChatMessage_AgentSenderIsStampedAndRateLimited(t *testing.T) {
	if testHandler == nil {
		t.Skip("requires test database")
	}
	ctx := context.Background()
	setAgentChatRateForTest(t, service.AgentChatRateLimit{Window: time.Hour, PerSession: 1, PerAgent: 5})
	sessionAgentID, sessionID, _, _ := setupDirectChatSession(t, ctx, "agent send chat")
	authorID := createHandlerTestAgent(t, "AgentSendAuthor", []byte("[]"))

	session, err := testHandler.Queries.GetChatSession(ctx, parseUUID(sessionID))
	if err != nil {
		t.Fatalf("load chat session: %v", err)
	}
	sessionAgent, err := testHandler.Queries.GetAgent(ctx, parseUUID(sessionAgentID))
	if err != nil {
		t.Fatalf("load session agent: %v", err)
	}
	send := func(actorType, actorID string) (*service.DirectChatSendResult, error) {
		return testHandler.TaskService.SendDirectChatMessage(ctx, session, sessionAgent, parseUUID(testUserID), "status?", nil, actorType, parseUUID(actorID))
	}

	sent, err := send("agent", authorID)
	if err != nil {
		t.Fatalf("first agent send: %v", err)
	}
	if sent.Message.Role != "user" || sent.Message.AuthorType.String != service.ChatAuthorTypeAgent || uuidToString(sent.Message.AuthorID) != authorID {
		t.Fatalf("agent-sent row = role %q author (%q, %s), want user row stamped with the agent",
			sent.Message.Role, sent.Message.AuthorType.String, uuidToString(sent.Message.AuthorID))
	}

	_, err = send("agent", authorID)
	var limited *service.AgentChatRateLimitedError
	if !errors.As(err, &limited) || limited.Scope != "session" {
		t.Fatalf("second agent send error = %v, want a session rate limit", err)
	}

	// The member's own sends are neither stamped nor limited.
	own, err := send("member", testUserID)
	if err != nil {
		t.Fatalf("member send after the agent hit its limit: %v", err)
	}
	if own.Message.AuthorType.Valid || own.Message.AuthorID.Valid {
		t.Fatalf("a member's own message must not carry an author stamp, got (%q, %s)",
			own.Message.AuthorType.String, uuidToString(own.Message.AuthorID))
	}
}

// telegramSendRecorder is a fake Bot API that records sendMessage calls.
type telegramSendRecorder struct {
	mu     sync.Mutex
	reject bool
	sends  []map[string]any
}

func (rec *telegramSendRecorder) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !strings.HasSuffix(r.URL.Path, "/sendMessage") {
		_, _ = w.Write([]byte(`{"ok":false,"error_code":404,"description":"unexpected method"}`))
		return
	}
	body, _ := io.ReadAll(r.Body)
	var params map[string]any
	_ = json.Unmarshal(body, &params)
	rec.mu.Lock()
	rec.sends = append(rec.sends, params)
	n := len(rec.sends)
	reject := rec.reject
	rec.mu.Unlock()
	if reject {
		_, _ = w.Write([]byte(`{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`))
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"message_id": 900 + n, "chat": map[string]any{"id": 424242}}})
}

func (rec *telegramSendRecorder) recorded() []map[string]any {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return append([]map[string]any(nil), rec.sends...)
}

// bindSessionToFakeTelegram links a chat session to a Telegram conversation
// served by a fake Bot API and wires the channel registry to it.
func bindSessionToFakeTelegram(t *testing.T, sessionID, sessionAgentID, botID string, contextRevision int64) *telegramSendRecorder {
	t.Helper()
	ctx := context.Background()
	rec := &telegramSendRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(rec.serve))
	t.Cleanup(srv.Close)

	registry := channel.NewRegistry()
	telegram.RegisterTelegram(registry, telegram.ChannelDeps{APIBase: srv.URL, HTTPClient: srv.Client()})
	previous := testHandler.ChannelRegistry
	testHandler.ChannelRegistry = registry
	t.Cleanup(func() { testHandler.ChannelRegistry = previous })

	config, err := json.Marshal(map[string]string{
		"app_id":              botID,
		"bot_username":        "agent_post_test_bot",
		"bot_token_encrypted": base64.StdEncoding.EncodeToString([]byte(botID + ":test-token")),
	})
	if err != nil {
		t.Fatalf("marshal installation config: %v", err)
	}
	var installationID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel_installation (workspace_id, agent_id, channel_type, config, status, installer_user_id)
		VALUES ($1, $2, 'telegram', $3::jsonb, 'active', $4)
		RETURNING id
	`, testWorkspaceID, sessionAgentID, string(config), testUserID).Scan(&installationID); err != nil {
		t.Fatalf("create telegram installation: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM channel_chat_session_binding WHERE chat_session_id = $1`, sessionID)
		testPool.Exec(context.Background(), `DELETE FROM channel_installation WHERE id = $1`, installationID)
	})
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_chat_session_binding
			(chat_session_id, installation_id, channel_type, channel_chat_id, chat_type, config, context_revision)
		VALUES ($1, $2, 'telegram', '424242', 'p2p', '{"chat_id":"424242"}'::jsonb, $3)
	`, sessionID, installationID, contextRevision); err != nil {
		t.Fatalf("bind session to telegram: %v", err)
	}
	return rec
}

func TestPostAgentChatMessage_DeliversToTheSessionsTelegramChat(t *testing.T) {
	if testHandler == nil {
		t.Skip("requires test database")
	}
	ctx := context.Background()
	sessionAgentID := createHandlerTestAgent(t, "AgentPostTelegramSessionAgent", []byte("[]"))
	sessionID := createHandlerTestChatSession(t, sessionAgentID)
	authorID := createHandlerTestAgent(t, "AgentPostTelegramAuthor", []byte("[]"))
	taskID := createHandlerTestTaskForAgent(t, authorID)
	rec := bindSessionToFakeTelegram(t, sessionID, sessionAgentID, "155701", 3)

	var resp PostAgentChatMessageResponse
	testutil.Call(t, testHandler.PostAgentChatMessage, agentPostReq(t, sessionID, authorID, taskID, "deploy is green")).
		Want(http.StatusCreated).JSON(&resp)
	if resp.Delivery.Status != agentChatDeliveryDelivered || resp.Delivery.ChannelType != "telegram" {
		t.Fatalf("delivery = %+v, want delivered over telegram", resp.Delivery)
	}

	sends := rec.recorded()
	if len(sends) != 1 {
		t.Fatalf("telegram received %d sendMessage call(s), want 1", len(sends))
	}
	if chatID, _ := sends[0]["chat_id"].(float64); int64(chatID) != 424242 {
		t.Fatalf("sendMessage chat_id = %v, want the bound chat 424242", sends[0]["chat_id"])
	}
	text, _ := sends[0]["text"].(string)
	if !strings.Contains(text, "deploy is green") || !strings.Contains(text, "AgentPostTelegramAuthor") {
		t.Fatalf("sendMessage text = %q, want the content and the name of the authoring agent", text)
	}
	if _, quoted := sends[0]["reply_parameters"]; quoted {
		t.Fatalf("a proactive post must not quote a message, got %v", sends[0]["reply_parameters"])
	}

	msg := loadChatMessageForTest(t, resp.MessageID)
	if !msg.ChannelContextRevision.Valid || msg.ChannelContextRevision.Int64 != 3 {
		t.Fatalf("stored context revision = %+v, want the binding's generation 3", msg.ChannelContextRevision)
	}
	if msg.ChannelOutboundType.String != "telegram" || len(msg.ChannelOutboundMessageIds) != 1 {
		t.Fatalf("stored outbound provenance = (%q, %v), want one telegram message id",
			msg.ChannelOutboundType.String, msg.ChannelOutboundMessageIds)
	}

	// The session agent's own channel transcript shows the post in the
	// generation it was written in, and not in any other.
	for revision, wantVisible := range map[int64]bool{3: true, 1: false, 2: false} {
		rows, err := testHandler.Queries.ListChatMessagesPageForChannelContext(ctx, db.ListChatMessagesPageForChannelContextParams{
			ChatSessionID:          parseUUID(sessionID),
			ChannelContextRevision: pgtype.Int8{Int64: revision, Valid: true},
			PageLimit:              10,
		})
		if err != nil {
			t.Fatalf("channel context transcript (revision %d): %v", revision, err)
		}
		if visible := len(rows) == 1; visible != wantVisible {
			t.Fatalf("revision %d sees the post = %v, want %v", revision, visible, wantVisible)
		}
	}
}

func TestPostAgentChatMessage_KeepsTheMessageWhenTelegramRejectsIt(t *testing.T) {
	if testHandler == nil {
		t.Skip("requires test database")
	}
	sessionAgentID := createHandlerTestAgent(t, "AgentPostTelegramRejectAgent", []byte("[]"))
	sessionID := createHandlerTestChatSession(t, sessionAgentID)
	taskID := createHandlerTestTaskForAgent(t, sessionAgentID)
	rec := bindSessionToFakeTelegram(t, sessionID, sessionAgentID, "155702", 1)
	rec.reject = true

	var resp PostAgentChatMessageResponse
	testutil.Call(t, testHandler.PostAgentChatMessage, agentPostReq(t, sessionID, sessionAgentID, taskID, "are you there?")).
		Want(http.StatusCreated).JSON(&resp)
	if resp.Delivery.Status != agentChatDeliveryFailed {
		t.Fatalf("delivery status = %q, want %q", resp.Delivery.Status, agentChatDeliveryFailed)
	}

	sends := rec.recorded()
	if len(sends) != 1 {
		t.Fatalf("telegram received %d sendMessage call(s), want exactly 1 (no retry)", len(sends))
	}
	// The session's own agent is the bot's voice, so it is not named again.
	if text, _ := sends[0]["text"].(string); strings.Contains(text, "AgentPostTelegramRejectAgent") {
		t.Fatalf("sendMessage text = %q, must not prefix the session agent's own name", text)
	}
	msg := loadChatMessageForTest(t, resp.MessageID)
	if msg.Content != "are you there?" || len(msg.ChannelOutboundMessageIds) != 0 {
		t.Fatalf("stored row = (%q, %v), want the message kept with no outbound ids", msg.Content, msg.ChannelOutboundMessageIds)
	}
}

// The session agent has no memory of a proactive post: it is in the transcript
// but in no provider session. The next turn must be told about it, once.
func TestClaim_HandsProactiveAgentPostsToTheNextTurnOnce(t *testing.T) {
	if testHandler == nil {
		t.Skip("requires test database")
	}
	ctx := context.Background()
	sessionAgentID, sessionID, runtimeID, daemonID := setupDirectChatSession(t, ctx, "proactive context chat")
	authorID := createHandlerTestAgent(t, "ProactiveContextAuthor", []byte("[]"))
	authorTaskID := createHandlerTestTaskForAgent(t, authorID)

	testutil.Call(t, testHandler.PostAgentChatMessage, agentPostReq(t, sessionID, authorID, authorTaskID, "nightly import failed on step 3")).
		Want(http.StatusCreated)

	first := sendDirectChat(t, ctx, sessionAgentID, sessionID, "what exactly failed?")
	claimed := claimTaskForRuntimeGuard(t, runtimeID, daemonID)
	if !strings.HasSuffix(claimed.ChatMessage, "what exactly failed?") {
		t.Fatalf("the member's message must stay last in the prompt, got %q", claimed.ChatMessage)
	}
	for _, want := range []string{"nightly import failed on step 3", "ProactiveContextAuthor", "not a message from the user"} {
		if !strings.Contains(claimed.ChatMessage, want) {
			t.Fatalf("claim prompt is missing %q: %q", want, claimed.ChatMessage)
		}
	}

	markTaskRunning(t, ctx, first)
	if _, err := testHandler.TaskService.CompleteTask(ctx, parseUUID(first), completeResult(t, "step 3 timed out"), "", "", "", false, "", ""); err != nil {
		t.Fatalf("complete first turn: %v", err)
	}

	sendDirectChat(t, ctx, sessionAgentID, sessionID, "retry it")
	next := claimTaskForRuntimeGuard(t, runtimeID, daemonID)
	if next.ChatMessage != "retry it" {
		t.Fatalf("a post already shown to a completed turn must not be repeated, got %q", next.ChatMessage)
	}
}

func TestWithoutAgentPostedMessages_KeepsLegacyTrailingInputIntact(t *testing.T) {
	task := pgtype.UUID{Bytes: [16]byte{7}, Valid: true}
	agent := pgtype.UUID{Bytes: [16]byte{8}, Valid: true}
	history := []db.ChatMessage{
		{Role: "user", Content: "old question", TaskID: task},
		{Role: "assistant", Content: "old answer", TaskID: task},
		{Role: "user", Content: "new question"},
		{Role: "assistant", Content: "proactive post", AuthorType: pgtype.Text{String: service.ChatAuthorTypeAgent, Valid: true}, AuthorID: agent},
	}
	trailing := trailingUserMessages(withoutAgentPostedMessages(history))
	if len(trailing) != 1 || trailing[0].Content != "new question" {
		t.Fatalf("trailing input = %+v, want only the unanswered user message", trailing)
	}
}
