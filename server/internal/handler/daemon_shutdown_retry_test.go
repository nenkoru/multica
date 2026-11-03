package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/multica-ai/multica/server/internal/testutil"
)

type claimRetryLineageTask struct {
	ID                   string  `json:"id"`
	Attempt              int32   `json:"attempt"`
	MaxAttempts          int32   `json:"max_attempts"`
	ParentTaskID         *string `json:"parent_task_id"`
	RetryOfFailureReason string  `json:"retry_of_failure_reason"`
	PriorSessionID       string  `json:"prior_session_id"`
	PriorWorkDir         string  `json:"prior_work_dir"`
}

func claimRetryLineageForRuntime(t *testing.T, runtimeID, daemonID string) *claimRetryLineageTask {
	t.Helper()
	w := httptest.NewRecorder()
	req := newDaemonTokenRequest("POST", "/api/daemon/runtimes/"+runtimeID+"/claim", nil, testWorkspaceID, daemonID)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("runtimeId", runtimeID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	testHandler.ClaimTaskByRuntime(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ClaimTaskByRuntime: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Task *claimRetryLineageTask `json:"task"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode claim: %v", err)
	}
	return resp.Task
}

// TestDaemonShutdown_RetriesWithSessionAndCapsAtThreeAttempts walks the whole
// RUSL-2266 chain on a real database: a running issue task fails with
// daemon_shutdown, the server creates a retry child, and the claim hands that
// child the parent's session and workdir plus the lineage the daemon needs to
// render its continuation notice. A second shutdown still retries (attempt 3);
// a third does not — the daemon_shutdown ceiling is 3 attempts.
func TestDaemonShutdown_RetriesWithSessionAndCapsAtThreeAttempts(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID, runtimeID, daemonID := createRuntimeGuardAgent(t, ctx)
	issueID := dbfx.Issue(t, "daemon shutdown retry fixture", testutil.Cols{
		"status":        "in_progress",
		"assignee_type": "agent",
		"assignee_id":   agentID,
	})
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE issue_id = $1`, issueID)
	})

	firstID := dbfx.Task(t, agentID, testutil.Cols{
		"issue_id":     issueID,
		"runtime_id":   runtimeID,
		"status":       "running",
		"attempt":      1,
		"max_attempts": 2,
		"started_at":   testutil.Raw("now()"),
	})

	if _, err := testHandler.TaskService.FailTask(ctx, parseUUID(firstID),
		"agent run interrupted by daemon shutdown (graceful stop or restart)",
		"shutdown-session-1", "/tmp/shutdown-workdir-1", "", "daemon_shutdown", false, "", ""); err != nil {
		t.Fatalf("FailTask(daemon_shutdown): %v", err)
	}

	var reason string
	if err := testPool.QueryRow(ctx, `SELECT failure_reason FROM agent_task_queue WHERE id = $1`, firstID).Scan(&reason); err != nil {
		t.Fatalf("read failed parent: %v", err)
	}
	if reason != "daemon_shutdown" {
		t.Fatalf("parent failure_reason = %q, want daemon_shutdown", reason)
	}

	second := claimRetryLineageForRuntime(t, runtimeID, daemonID)
	if second == nil {
		t.Fatal("no retry child was claimable after a daemon_shutdown failure")
	}
	if second.Attempt != 2 || second.MaxAttempts != 3 {
		t.Errorf("retry attempt/max = %d/%d, want 2/3", second.Attempt, second.MaxAttempts)
	}
	if second.ParentTaskID == nil || *second.ParentTaskID != firstID {
		t.Errorf("parent_task_id = %v, want %s", second.ParentTaskID, firstID)
	}
	if second.RetryOfFailureReason != "daemon_shutdown" {
		t.Errorf("retry_of_failure_reason = %q, want daemon_shutdown", second.RetryOfFailureReason)
	}
	if second.PriorSessionID != "shutdown-session-1" {
		t.Errorf("PriorSessionID = %q, want shutdown-session-1 (the retry resumes the interrupted session)", second.PriorSessionID)
	}
	if second.PriorWorkDir != "/tmp/shutdown-workdir-1" {
		t.Errorf("PriorWorkDir = %q, want /tmp/shutdown-workdir-1", second.PriorWorkDir)
	}

	// Second shutdown during the retry: still within the 3-attempt ceiling.
	if _, err := testHandler.TaskService.FailTask(ctx, parseUUID(second.ID),
		"agent run interrupted by daemon shutdown", "", "", "", "daemon_shutdown", false, "", ""); err != nil {
		t.Fatalf("FailTask(attempt 2): %v", err)
	}
	third := claimRetryLineageForRuntime(t, runtimeID, daemonID)
	if third == nil {
		t.Fatal("attempt 2 interrupted by a shutdown must still get attempt 3")
	}
	if third.Attempt != 3 {
		t.Errorf("third attempt = %d, want 3", third.Attempt)
	}
	if third.PriorSessionID != "shutdown-session-1" {
		t.Errorf("attempt 3 PriorSessionID = %q, want the session inherited through the chain", third.PriorSessionID)
	}

	// Third shutdown: the ceiling is reached, no further retry.
	if _, err := testHandler.TaskService.FailTask(ctx, parseUUID(third.ID),
		"agent run interrupted by daemon shutdown", "", "", "", "daemon_shutdown", false, "", ""); err != nil {
		t.Fatalf("FailTask(attempt 3): %v", err)
	}
	var pending int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM agent_task_queue
		WHERE issue_id = $1 AND status IN ('queued', 'deferred', 'dispatched', 'running')
	`, issueID).Scan(&pending); err != nil {
		t.Fatalf("count pending: %v", err)
	}
	if pending != 0 {
		t.Fatalf("pending tasks after the 3rd shutdown = %d, want 0 (ceiling is 3 attempts)", pending)
	}
}

// A first attempt carries no retry lineage, so the claim omits the field.
func TestClaim_FirstAttemptHasNoRetryOfFailureReason(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID, runtimeID, daemonID := createRuntimeGuardAgent(t, ctx)
	issueID := dbfx.Issue(t, "first attempt lineage fixture", testutil.Cols{
		"status":        "in_progress",
		"assignee_type": "agent",
		"assignee_id":   agentID,
	})
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE issue_id = $1`, issueID)
	})
	dbfx.Task(t, agentID, testutil.Cols{
		"issue_id":   issueID,
		"runtime_id": runtimeID,
		"status":     "queued",
	})

	got := claimRetryLineageForRuntime(t, runtimeID, daemonID)
	if got == nil {
		t.Fatal("expected a claimable task")
	}
	if got.RetryOfFailureReason != "" || got.ParentTaskID != nil {
		t.Fatalf("first attempt lineage = %q / %v, want empty", got.RetryOfFailureReason, got.ParentTaskID)
	}
}
