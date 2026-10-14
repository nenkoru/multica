package daemon

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/agent"
)

// RUSL-1741: a Claude session idle past the threshold is not resumed.
func TestGateClaudeResumeToMaxIdle(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	at := func(ago time.Duration) func() (time.Time, error) {
		return func() (time.Time, error) { return now.Add(-ago), nil }
	}
	missing := func() (time.Time, error) { return time.Time{}, os.ErrNotExist }

	tests := []struct {
		name         string
		maxIdle      time.Duration
		sessionID    string
		lastActivity func() (time.Time, error)
		wantDropped  bool
	}{
		{name: "idle above threshold drops resume", maxIdle: 55 * time.Minute, sessionID: "s1", lastActivity: at(97 * time.Minute), wantDropped: true},
		{name: "idle below threshold keeps resume", maxIdle: 55 * time.Minute, sessionID: "s1", lastActivity: at(10 * time.Minute)},
		{name: "idle equal to threshold keeps resume", maxIdle: 55 * time.Minute, sessionID: "s1", lastActivity: at(55 * time.Minute)},
		{name: "switch off keeps resume", maxIdle: 0, sessionID: "s1", lastActivity: at(48 * time.Hour)},
		{name: "negative switch keeps resume", maxIdle: -time.Minute, sessionID: "s1", lastActivity: at(48 * time.Hour)},
		{name: "missing session file keeps resume", maxIdle: 55 * time.Minute, sessionID: "s1", lastActivity: missing},
		{name: "no prior session is a no-op", maxIdle: 55 * time.Minute, sessionID: "", lastActivity: at(48 * time.Hour)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := Task{PriorSessionID: tt.sessionID}
			taskCtx := execenv.TaskContextForEnv{PriorSessionResumed: tt.sessionID != ""}
			var logs bytes.Buffer
			log := slog.New(slog.NewTextHandler(&logs, nil))

			dropped := gateClaudeResumeToMaxIdle(&task, &taskCtx, tt.maxIdle, now, tt.lastActivity, log)

			if dropped != tt.wantDropped {
				t.Fatalf("dropped = %v, want %v", dropped, tt.wantDropped)
			}
			wantSession := tt.sessionID
			if tt.wantDropped {
				wantSession = ""
			}
			if task.PriorSessionID != wantSession {
				t.Fatalf("PriorSessionID = %q, want %q", task.PriorSessionID, wantSession)
			}
			if taskCtx.PriorSessionResumed != (wantSession != "") {
				t.Fatalf("PriorSessionResumed = %v, want %v", taskCtx.PriorSessionResumed, wantSession != "")
			}
			if task.PriorSessionResumeUnavailable != tt.wantDropped || taskCtx.PriorSessionResumeUnavailable != tt.wantDropped {
				t.Fatalf("PriorSessionResumeUnavailable task=%v ctx=%v, want %v",
					task.PriorSessionResumeUnavailable, taskCtx.PriorSessionResumeUnavailable, tt.wantDropped)
			}
			if got := strings.Contains(logs.String(), "resume skipped: idle 1h37m0s > 55m0s"); got != tt.wantDropped {
				t.Fatalf("skip log line present = %v, want %v; logs:\n%s", got, tt.wantDropped, logs.String())
			}
		})
	}
}

// The dropped resume must yield the cold prompt with the continuity notice,
// the same prompt a "resumed session not found" retry gets.
func TestGateClaudeResumeToMaxIdleBuildsColdPrompt(t *testing.T) {
	t.Parallel()

	now := time.Now()
	task := Task{
		ID:                    "task-1",
		IssueID:               "issue-1",
		PriorSessionID:        "s1",
		TriggerCommentID:      "comment-1",
		NewCommentsSince:      "2026-10-14T08:00:00Z",
		NewCommentCount:       0,
		NewCommentsDeltaKnown: true,
	}
	taskCtx := execenv.TaskContextForEnv{PriorSessionResumed: true}
	warm := BuildPrompt(task, "claude")

	gateClaudeResumeToMaxIdle(&task, &taskCtx, time.Hour, now,
		func() (time.Time, error) { return now.Add(-2 * time.Hour), nil }, slog.Default())
	cold := BuildPrompt(task, "claude")

	if strings.Contains(warm, "Session Continuity Notice") {
		t.Fatalf("warm prompt unexpectedly carries the continuity notice")
	}
	if !strings.Contains(cold, execenv.SessionContinuityNoticeIssue) {
		t.Fatalf("cold prompt lacks the continuity notice:\n%s", cold)
	}
	if !strings.Contains(cold, "--roots-only --summary") {
		t.Fatalf("cold prompt lacks the full comment scan:\n%s", cold)
	}
	if cold == warm {
		t.Fatalf("prompt did not change after the resume was dropped")
	}
}

func TestClaudeResumeMaxIdleForSplitsChat(t *testing.T) {
	t.Parallel()

	d := &Daemon{cfg: Config{ClaudeResumeMaxIdle: 55 * time.Minute}}
	if got := d.claudeResumeMaxIdleFor(Task{IssueID: "i"}); got != 55*time.Minute {
		t.Fatalf("issue max idle = %s, want 55m", got)
	}
	if got := d.claudeResumeMaxIdleFor(Task{ChatSessionID: "c"}); got != 0 {
		t.Fatalf("chat max idle = %s, want 0 (off unless its own switch is set)", got)
	}
	d.cfg.ClaudeResumeMaxIdleChat = 3 * time.Hour
	if got := d.claudeResumeMaxIdleFor(Task{ChatSessionID: "c"}); got != 3*time.Hour {
		t.Fatalf("chat max idle = %s, want 3h", got)
	}
}

// End to end against a real transcript layout: the mtime of
// <config>/projects/<encoded-cwd>/<id>.jsonl decides.
func TestGateClaudeResumeToMaxIdleWithTranscriptFile(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	cwd := t.TempDir()
	env := map[string]string{"CLAUDE_CONFIG_DIR": configDir}
	transcript := filepath.Join(configDir, "projects", "-encoded-cwd", "sess-1.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(transcript, old, old); err != nil {
		t.Fatal(err)
	}
	lookup := func(id string) func() (time.Time, error) {
		return func() (time.Time, error) { return agent.ClaudeSessionLastActivity(env, cwd, id) }
	}

	task := Task{PriorSessionID: "sess-1"}
	taskCtx := execenv.TaskContextForEnv{PriorSessionResumed: true}
	if !gateClaudeResumeToMaxIdle(&task, &taskCtx, 55*time.Minute, time.Now(), lookup("sess-1"), slog.Default()) {
		t.Fatalf("2h-old transcript was resumed with a 55m bound")
	}

	fresh := time.Now().Add(-5 * time.Minute)
	if err := os.Chtimes(transcript, fresh, fresh); err != nil {
		t.Fatal(err)
	}
	task = Task{PriorSessionID: "sess-1"}
	if gateClaudeResumeToMaxIdle(&task, &taskCtx, 55*time.Minute, time.Now(), lookup("sess-1"), slog.Default()) {
		t.Fatalf("5m-old transcript was dropped")
	}

	task = Task{PriorSessionID: "sess-missing"}
	if gateClaudeResumeToMaxIdle(&task, &taskCtx, 55*time.Minute, time.Now(), lookup("sess-missing"), slog.Default()) {
		t.Fatalf("missing transcript dropped the resume")
	}
	if _, err := lookup("sess-missing")(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing transcript error = %v, want ErrNotExist", err)
	}
}

func TestLoadConfig_ClaudeResumeMaxIdle(t *testing.T) {
	stageFakeAgent(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SHELL", filepath.Join(t.TempDir(), "missing-shell"))
	t.Setenv("MULTICA_CLAUDE_RESUME_MAX_IDLE", "")
	t.Setenv("MULTICA_CLAUDE_RESUME_MAX_IDLE_CHAT", "")
	overrides := Overrides{ServerURL: "http://localhost:0", WorkspacesRoot: t.TempDir()}

	cfg, err := LoadConfig(overrides)
	if err != nil {
		t.Fatalf("LoadConfig default: %v", err)
	}
	if cfg.ClaudeResumeMaxIdle != 0 || cfg.ClaudeResumeMaxIdleChat != 0 {
		t.Fatalf("defaults = %s / %s, want both off", cfg.ClaudeResumeMaxIdle, cfg.ClaudeResumeMaxIdleChat)
	}

	t.Setenv("MULTICA_CLAUDE_RESUME_MAX_IDLE", "55m")
	cfg, err = LoadConfig(overrides)
	if err != nil {
		t.Fatalf("LoadConfig 55m: %v", err)
	}
	if cfg.ClaudeResumeMaxIdle != 55*time.Minute || cfg.ClaudeResumeMaxIdleChat != 0 {
		t.Fatalf("got %s / %s, want 55m / off", cfg.ClaudeResumeMaxIdle, cfg.ClaudeResumeMaxIdleChat)
	}

	t.Setenv("MULTICA_CLAUDE_RESUME_MAX_IDLE", "0")
	t.Setenv("MULTICA_CLAUDE_RESUME_MAX_IDLE_CHAT", "2h")
	cfg, err = LoadConfig(overrides)
	if err != nil {
		t.Fatalf("LoadConfig 0 / 2h: %v", err)
	}
	if cfg.ClaudeResumeMaxIdle != 0 || cfg.ClaudeResumeMaxIdleChat != 2*time.Hour {
		t.Fatalf("got %s / %s, want off / 2h", cfg.ClaudeResumeMaxIdle, cfg.ClaudeResumeMaxIdleChat)
	}

	t.Setenv("MULTICA_CLAUDE_RESUME_MAX_IDLE", "soon")
	if _, err := LoadConfig(overrides); err == nil || !strings.Contains(err.Error(), "MULTICA_CLAUDE_RESUME_MAX_IDLE") {
		t.Fatalf("invalid value error = %v, want named validation error", err)
	}
}
