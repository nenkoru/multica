package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// shutdownTestServer records every /fail body and /cancel-ack call, and serves
// /status with the given value. Everything else answers 200.
type shutdownTestServer struct {
	srv       *httptest.Server
	mu        sync.Mutex
	failBody  []map[string]any
	ackCalls  atomic.Int32
	statusVal atomic.Value
}

func newShutdownTestServer(t *testing.T, status string) *shutdownTestServer {
	t.Helper()
	s := &shutdownTestServer{}
	s.statusVal.Store(status)
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/fail"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			s.mu.Lock()
			s.failBody = append(s.failBody, body)
			s.mu.Unlock()
		case strings.HasSuffix(r.URL.Path, "/cancel-ack"):
			s.ackCalls.Add(1)
		case strings.HasSuffix(r.URL.Path, "/status"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"` + s.statusVal.Load().(string) + `"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *shutdownTestServer) fails() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any(nil), s.failBody...)
}

func newShutdownTestDaemon(url string, pollInterval time.Duration, runner taskRunnerFunc) *Daemon {
	d := &Daemon{
		client:             NewClient(url),
		logger:             slog.New(slog.NewTextHandler(io.Discard, nil)),
		runtimeIndex:       map[string]Runtime{"rt-1": {ID: "rt-1", Provider: "claude"}},
		cancelPollInterval: pollInterval,
	}
	d.runner = runner
	return d
}

// (a) A graceful stop cancels the daemon's root context; the agent comes back
// with status "cancelled". That used to be reported as failure_reason
// "cancelled" (not retryable) and the run was lost. It must now be the
// retryable daemon_shutdown, carrying the session and workdir so the retry
// resumes in place (RUSL-2266).
func TestHandleTask_RootCancelWithCancelledResultReportsDaemonShutdown(t *testing.T) {
	t.Parallel()

	srv := newShutdownTestServer(t, "running")
	d := newShutdownTestDaemon(srv.srv.URL, time.Hour, func(runCtx context.Context, _ Task, _ string, _ int, _ *slog.Logger) (TaskResult, error) {
		if !errors.Is(runCtx.Err(), context.Canceled) {
			t.Errorf("runner context error = %v, want context.Canceled", runCtx.Err())
		}
		return TaskResult{
			Status:    "cancelled",
			Comment:   "task cancelled by server",
			SessionID: "sess-1",
			WorkDir:   "/ws/task-1/workdir",
		}, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d.handleTask(ctx, Task{ID: "task-shutdown-cancelled", RuntimeID: "rt-1"}, 0)

	fails := srv.fails()
	if len(fails) != 1 {
		t.Fatalf("fail callbacks = %d, want 1", len(fails))
	}
	body := fails[0]
	if got := body["failure_reason"]; got != "daemon_shutdown" {
		t.Errorf("failure_reason = %v, want daemon_shutdown", got)
	}
	if got := body["session_id"]; got != "sess-1" {
		t.Errorf("session_id = %v, want sess-1 (the retry resumes it)", got)
	}
	if got := body["work_dir"]; got != "/ws/task-1/workdir" {
		t.Errorf("work_dir = %v, want the run's workdir", got)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "daemon shutdown") || strings.Contains(msg, "cancelled by server") {
		t.Errorf("error = %q, want a daemon-shutdown message without the misleading server-cancel text", msg)
	}
	if got := srv.ackCalls.Load(); got != 0 {
		t.Errorf("cancel-ack calls = %d, want 0", got)
	}
}

// (a') Under KillMode=control-group the agent gets the same SIGTERM and dies
// on its own; runTask classifies the exit as a process failure on the blocked
// path. With the root context cancelled that is still a shutdown.
func TestHandleTask_RootCancelOverridesProcessFailureClassification(t *testing.T) {
	t.Parallel()

	srv := newShutdownTestServer(t, "running")
	d := newShutdownTestDaemon(srv.srv.URL, time.Hour, func(context.Context, Task, string, int, *slog.Logger) (TaskResult, error) {
		return TaskResult{
			Status:        "blocked",
			Comment:       "claude exited with error: signal: terminated",
			SessionID:     "sess-2",
			WorkDir:       "/ws/task-2/workdir",
			FailureReason: "agent_error.process_failure",
		}, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d.handleTask(ctx, Task{ID: "task-shutdown-sigterm", RuntimeID: "rt-1"}, 0)

	fails := srv.fails()
	if len(fails) != 1 {
		t.Fatalf("fail callbacks = %d, want 1", len(fails))
	}
	if got := fails[0]["failure_reason"]; got != "daemon_shutdown" {
		t.Errorf("failure_reason = %v, want daemon_shutdown", got)
	}
	if msg, _ := fails[0]["error"].(string); !strings.Contains(msg, "signal: terminated") {
		t.Errorf("error = %q, want the original error text kept for the server's resume guards", msg)
	}
	if got := fails[0]["session_id"]; got != "sess-2" {
		t.Errorf("session_id = %v, want sess-2", got)
	}
}

// A poisoned conversation keeps its resume-unsafe reason even if the daemon is
// stopping: overriding it would make the retry resume the poisoned session.
func TestHandleTask_RootCancelKeepsResumeUnsafeReason(t *testing.T) {
	t.Parallel()

	srv := newShutdownTestServer(t, "running")
	d := newShutdownTestDaemon(srv.srv.URL, time.Hour, func(context.Context, Task, string, int, *slog.Logger) (TaskResult, error) {
		return TaskResult{
			Status:        "blocked",
			Comment:       "prompt is too long",
			SessionID:     "sess-3",
			FailureReason: "agent_error.context_overflow",
		}, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d.handleTask(ctx, Task{ID: "task-shutdown-poisoned", RuntimeID: "rt-1"}, 0)

	fails := srv.fails()
	if len(fails) != 1 {
		t.Fatalf("fail callbacks = %d, want 1", len(fails))
	}
	if got := fails[0]["failure_reason"]; got != "agent_error.context_overflow" {
		t.Errorf("failure_reason = %v, want agent_error.context_overflow preserved", got)
	}
}

// (b) The agent returned an ERROR (not a result) while the root context was
// cancelled. The classifier must not be trusted: the reason is daemon_shutdown,
// and the workdir (and session, when known) still travel with the report.
func TestHandleTask_RootCancelWithRunErrorReportsDaemonShutdown(t *testing.T) {
	t.Parallel()

	srv := newShutdownTestServer(t, "running")
	d := newShutdownTestDaemon(srv.srv.URL, time.Hour, func(context.Context, Task, string, int, *slog.Logger) (TaskResult, error) {
		return TaskResult{WorkDir: "/ws/task-4/workdir", SessionID: "sess-4"}, errors.New("claude exited with error: exit status 143")
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d.handleTask(ctx, Task{ID: "task-shutdown-error", RuntimeID: "rt-1"}, 0)

	fails := srv.fails()
	if len(fails) != 1 {
		t.Fatalf("fail callbacks = %d, want 1", len(fails))
	}
	body := fails[0]
	if got := body["failure_reason"]; got != "daemon_shutdown" {
		t.Errorf("failure_reason = %v, want daemon_shutdown (not the classifier's agent_error.*)", got)
	}
	if got := body["work_dir"]; got != "/ws/task-4/workdir" {
		t.Errorf("work_dir = %v, want the run's workdir", got)
	}
	if got := body["session_id"]; got != "sess-4" {
		t.Errorf("session_id = %v, want sess-4", got)
	}
}

// Control for (b): with a live root context the same error is classified as
// before.
func TestHandleTask_RunErrorWithoutShutdownIsClassified(t *testing.T) {
	t.Parallel()

	srv := newShutdownTestServer(t, "running")
	d := newShutdownTestDaemon(srv.srv.URL, time.Hour, func(context.Context, Task, string, int, *slog.Logger) (TaskResult, error) {
		return TaskResult{}, errors.New("claude exited with error: exit status 143")
	})

	d.handleTask(context.Background(), Task{ID: "task-plain-error", RuntimeID: "rt-1"}, 0)

	fails := srv.fails()
	if len(fails) != 1 {
		t.Fatalf("fail callbacks = %d, want 1", len(fails))
	}
	if got := fails[0]["failure_reason"]; got == "daemon_shutdown" {
		t.Errorf("failure_reason = %v; a live daemon must keep the classifier's reason", got)
	}
}

// (c) A cancelled result while the daemon keeps running is a server/user
// cancel shape and keeps the historical "cancelled" reason.
func TestHandleTask_CancelledResultWithoutShutdownKeepsCancelled(t *testing.T) {
	t.Parallel()

	srv := newShutdownTestServer(t, "running")
	d := newShutdownTestDaemon(srv.srv.URL, time.Hour, func(context.Context, Task, string, int, *slog.Logger) (TaskResult, error) {
		return TaskResult{Status: "cancelled", Comment: "task cancelled by server", SessionID: "sess-5"}, nil
	})

	d.handleTask(context.Background(), Task{ID: "task-cancel-live", RuntimeID: "rt-1"}, 0)

	fails := srv.fails()
	if len(fails) != 1 {
		t.Fatalf("fail callbacks = %d, want 1", len(fails))
	}
	if got := fails[0]["failure_reason"]; got != "cancelled" {
		t.Errorf("failure_reason = %v, want cancelled", got)
	}
}

// (c') A server-side cancel observed by the poller is discarded through the
// cancel-ack path exactly as before — no fail callback, no daemon_shutdown —
// even if the daemon then starts shutting down before the run returns.
func TestHandleTask_ServerCancelIsNotDaemonShutdown(t *testing.T) {
	t.Parallel()

	srv := newShutdownTestServer(t, "cancelled")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := newShutdownTestDaemon(srv.srv.URL, 10*time.Millisecond, func(runCtx context.Context, _ Task, _ string, _ int, _ *slog.Logger) (TaskResult, error) {
		select {
		case <-runCtx.Done():
		case <-time.After(5 * time.Second):
			t.Error("runner was never interrupted by the server-side cancel")
		}
		// The daemon begins a shutdown right after the server cancel landed.
		cancel()
		return TaskResult{Status: "cancelled", SessionID: "sess-6"}, nil
	})

	d.handleTask(ctx, Task{ID: "task-server-cancel", RuntimeID: "rt-1"}, 0)

	// The ack itself rides the (now cancelled) root context, so whether it
	// reaches the server is not this test's concern; the server sweeper
	// finalizes a lost ack. What matters is that no fail — and in particular
	// no retryable daemon_shutdown — is reported for a server-side cancel.
	if got := len(srv.fails()); got != 0 {
		t.Fatalf("fail callbacks = %d, want 0 (server cancel is acked, not failed): %v", got, srv.fails())
	}
}

func TestDaemonShutdownFailureReason(t *testing.T) {
	t.Parallel()

	live := context.Background()
	stopped, cancel := context.WithCancel(context.Background())
	cancel()

	cases := []struct {
		name   string
		ctx    context.Context
		status string
		reason string
		want   bool
	}{
		{"live daemon never overrides", live, "cancelled", "", false},
		{"completed run is never rewritten", stopped, "completed", "", false},
		{"cancelled result", stopped, "cancelled", "", true},
		{"error path", stopped, "", "agent_error.unknown", true},
		{"process failure", stopped, "blocked", "agent_error.process_failure", true},
		{"idle watchdog", stopped, "blocked", "idle_watchdog", true},
		{"poisoned session kept", stopped, "blocked", "api_invalid_request", false},
		{"context overflow kept", stopped, "blocked", "agent_error.context_overflow", false},
		{"invalid identity kept", stopped, "", "invalid_task_identity", false},
	}
	for _, tc := range cases {
		got, ok := daemonShutdownFailureReason(tc.ctx, tc.status, tc.reason)
		if ok != tc.want {
			t.Errorf("%s: ok = %v, want %v", tc.name, ok, tc.want)
		}
		if ok && got != "daemon_shutdown" {
			t.Errorf("%s: reason = %q, want daemon_shutdown", tc.name, got)
		}
	}
}

// (e) The retry knows it is a retry: attempt > 1 with an interruption parent
// reason renders the continuation block; anything else does not.
func TestBuildPrompt_InterruptedRetryBlock(t *testing.T) {
	t.Parallel()

	base := Task{IssueID: "issue-1", TriggerCommentID: "c-1", TriggerCommentContent: "please do X"}

	for _, reason := range []string{"daemon_shutdown", "runtime_recovery"} {
		task := base
		task.Attempt = 2
		task.ParentTaskID = "parent-1"
		task.RetryOfFailureReason = reason
		prompt := BuildPrompt(task, "claude")
		if !strings.Contains(prompt, "[CONTINUATION AFTER INTERRUPTION]") {
			t.Fatalf("%s: prompt lacks the continuation block:\n%s", reason, prompt)
		}
		if !strings.Contains(prompt, "attempt 2") || !strings.Contains(prompt, reason) {
			t.Errorf("%s: block does not name the attempt and reason", reason)
		}
		if !strings.Contains(prompt, "Do not repeat external actions") {
			t.Errorf("%s: block does not forbid repeating external actions", reason)
		}
	}

	noBlock := []struct {
		name string
		task Task
	}{
		{"first attempt", func() Task { t := base; t.Attempt = 1; t.RetryOfFailureReason = "daemon_shutdown"; return t }()},
		{"old server: attempt only", func() Task { t := base; t.Attempt = 2; return t }()},
		{"retry for another reason", func() Task {
			t := base
			t.Attempt = 2
			t.RetryOfFailureReason = "agent_error.provider_network"
			return t
		}()},
	}
	for _, tc := range noBlock {
		if prompt := BuildPrompt(tc.task, "claude"); strings.Contains(prompt, "CONTINUATION AFTER INTERRUPTION") {
			t.Errorf("%s: unexpected continuation block", tc.name)
		}
	}
}

// Wire compatibility: the daemon reads attempt / parent_task_id /
// retry_of_failure_reason from the claim payload, and an old server's payload
// (no retry_of_failure_reason, parent_task_id null) still decodes.
func TestTaskDecodesRetryLineage(t *testing.T) {
	t.Parallel()

	var got Task
	if err := json.Unmarshal([]byte(`{"id":"t","attempt":2,"parent_task_id":"p","retry_of_failure_reason":"daemon_shutdown"}`), &got); err != nil {
		t.Fatal(err)
	}
	if got.Attempt != 2 || got.ParentTaskID != "p" || got.RetryOfFailureReason != "daemon_shutdown" {
		t.Fatalf("decoded = %+v", got)
	}
	var old Task
	if err := json.Unmarshal([]byte(`{"id":"t","attempt":1,"parent_task_id":null}`), &old); err != nil {
		t.Fatal(err)
	}
	if old.Attempt != 1 || old.ParentTaskID != "" || old.RetryOfFailureReason != "" {
		t.Fatalf("decoded old payload = %+v", old)
	}
}
