package streams

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestMultikaPrivateProjectBoundary(t *testing.T) {
	h := newHarness(t, "mkprivate")
	ctx := context.Background()
	outsider := h.f.User(t, "Outsider", "multika-outsider@example.test")
	h.f.Member(t, testWorkspaceID, outsider, "member")
	project := h.f.Project(t, "Private project", testutil.Cols{"visibility": "private"})
	const marker = "PRIVATE-SUMMARY-MULTIKA"
	h.f.Exec(t, `UPDATE stream SET carrier_project_id = $1, summary = $2 WHERE id = $3`, project, marker, h.stream.ID)
	runtimeID := h.f.Runtime(t, "mkprivate-runtime")
	multika := h.f.Agent(t, "Shared Multika", runtimeID, testutil.Cols{"visibility": "workspace", "permission_mode": "public_to"})
	h.f.Insert(t, "agent_invocation_target", testutil.Cols{"agent_id": multika, "target_type": "workspace", "target_id": testWorkspaceID, "created_by": testUserID})
	h.sup.MultikaAgentID, _ = parseUUID(multika)
	sid, _ := parseUUID(h.f.ChatSession(t, multika, testutil.Cols{"creator_id": outsider}))
	h.sup.Screens = &Screens{}
	bus := events.New()
	h.sup.subscribeScreens(bus)
	finals := 0
	bus.Subscribe(EventUIFinal, func(events.Event) { finals++ })
	h.sup.StreamTemplate = h.stream.Slug
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM chat_focus WHERE chat_session_id = $1`, sid)
		_, _ = testPool.Exec(ctx, `DELETE FROM multika_turn WHERE chat_session_id = $1`, sid)
		_, _ = testPool.Exec(ctx, `DELETE FROM multika_charter WHERE chat_session_id = $1`, sid)
		_, _ = testPool.Exec(ctx, `DELETE FROM ui_action WHERE ui_block_id IN (SELECT id FROM ui_block WHERE chat_session_id = $1)`, sid)
		_, _ = testPool.Exec(ctx, `DELETE FROM ui_block WHERE chat_session_id = $1`, sid)
	})
	newTask := func(t *testing.T, cols testutil.Cols) string {
		t.Helper()
		cols["runtime_id"] = runtimeID
		cols["chat_session_id"] = uuidString(sid)
		if user, ok := cols["originator_user_id"]; ok {
			cols["accountable_user_id"] = user
		}
		return h.f.Task(t, multika, cols)
	}
	assertDenied := func(t *testing.T, task string) {
		t.Helper()
		taskID, _ := parseUUID(task)
		before := h.chatSteps()
		out := h.sup.OnMultikaClaim(ctx, taskID, sid, h.sup.MultikaAgentID, "давай про mkprivate")
		if strings.Contains(out, marker) || h.chatSteps() != before || out == "" {
			t.Fatalf("claim must explicitly deny without leaking or routing: context=%q steps=%d->%d", out, before, h.chatSteps())
		}
		if n := h.f.Count(t, `SELECT count(*) FROM multika_turn WHERE task_id = $1`, task); n != 0 {
			t.Fatal("denied claim recorded turn actions")
		}
		if n := h.f.Count(t, `SELECT count(*) FROM ui_block WHERE task_id = $1`, task); n != 0 {
			t.Fatal("denied claim generated a screen")
		}
		// Even a model reply must not apply an interview charter after denial.
		h.sup.OnMultikaReply(ctx, sid, taskID, charterReply(goodCharter))
		if n := h.f.Count(t, `SELECT count(*) FROM multika_charter WHERE task_id = $1`, task); n != 0 {
			t.Fatal("denied reply applied a charter")
		}
	}
	t.Run("originator bounds owner", func(t *testing.T) {
		assertDenied(t, newTask(t, testutil.Cols{"originator_user_id": outsider}))
	})
	t.Run("accountable fallback", func(t *testing.T) {
		assertDenied(t, newTask(t, testutil.Cols{"accountable_user_id": outsider}))
	})
	t.Run("missing task", func(t *testing.T) {
		assertDenied(t, uuidString(newUUID(t)))
	})
	t.Run("agent owner bounds originator", func(t *testing.T) {
		h.f.Exec(t, `UPDATE agent SET owner_id = $1 WHERE id = $2`, outsider, multika)
		defer h.f.Exec(t, `UPDATE agent SET owner_id = $1 WHERE id = $2`, testUserID, multika)
		assertDenied(t, newTask(t, testutil.Cols{"originator_user_id": testUserID}))
	})
	t.Run("foreign session task", func(t *testing.T) {
		task := newTask(t, testutil.Cols{"originator_user_id": testUserID})
		other := h.f.ChatSession(t, multika)
		h.f.Exec(t, `UPDATE agent_task_queue SET chat_session_id = $1 WHERE id = $2`, other, task)
		assertDenied(t, task)
	})
	t.Run("shown ask remains unanswered", func(t *testing.T) {
		ask := h.f.Insert(t, "ask", testutil.Cols{"stream_id": uuidString(h.stream.ID), "kind": "choice", "text": "Private choice", "options": `["yes","no"]`, "status": "shown", "shown_at": time.Now(), "deadline": time.Now().Add(time.Hour)})
		taskID, _ := parseUUID(newTask(t, testutil.Cols{"originator_user_id": outsider}))
		h.sup.OnMultikaClaim(ctx, taskID, sid, h.sup.MultikaAgentID, "1 а")
		if n := h.f.Count(t, `SELECT count(*) FROM ask WHERE id = $1 AND status = 'shown' AND answer IS NULL`, ask); n != 1 {
			t.Fatal("denied claim answered a private ask")
		}
	})
	t.Run("access rechecked on redelivery", func(t *testing.T) {
		task := newTask(t, testutil.Cols{"originator_user_id": outsider})
		taskID, _ := parseUUID(task)
		h.f.Exec(t, `INSERT INTO project_member (workspace_id, project_id, user_id) VALUES ($1, $2, $3)`, testWorkspaceID, project, outsider)
		out := h.sup.OnMultikaClaim(ctx, taskID, sid, h.sup.MultikaAgentID, "давай про mkprivate")
		if !strings.Contains(out, marker) || h.chatSteps() == 0 {
			t.Fatalf("fully authorized run lost Multika behavior: %s", out)
		}
		h.f.Exec(t, `DELETE FROM project_member WHERE project_id = $1 AND user_id = $2`, project, outsider)
		before := h.chatSteps()
		out = h.sup.OnMultikaClaim(ctx, taskID, sid, h.sup.MultikaAgentID, "всё")
		if strings.Contains(out, marker) || h.chatSteps() != before {
			t.Fatal("redelivery reused actions or changed focus after access revocation")
		}
		if f := h.sup.MultikaFocusOf(ctx, h.stream.WorkspaceID, sid); f.Stream == nil {
			t.Fatal("denied claim cleared shared focus")
		}
		h.sup.finishScreens(ctx, events.Event{}, protocol.ChatDonePayload{TaskID: task, ChatSessionID: uuidString(sid)})
		if finals != 0 {
			t.Fatal("screen final disclosed a stored card after access revocation")
		}
	})
}
