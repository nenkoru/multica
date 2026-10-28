package daemon

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/redact"
)

// Synthetic values only.
const (
	secretValuesTestHex  = "fedcba9876543210fedcba9876543210"
	secretValuesTestPass = "Synthetic-Pass-Word-77"
)

func useSecretValuesFixture(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "ru-exec", "ru-exec.env")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("export RU_EXEC_TOKEN=\""+secretValuesTestHex+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	restore := redact.SetSecretValuesSourceForTesting(dir, []string{"SVC_PASSWORD=" + secretValuesTestPass})
	t.Cleanup(restore)
	t.Setenv(redact.SecretValuesEnv, "")
}

func TestScrubTaskMessagesSecretValues(t *testing.T) {
	useSecretValuesFixture(t)
	in := []TaskMessageData{
		{Seq: 1, Type: "text", Content: "token is " + secretValuesTestHex},
		{Seq: 2, Type: "thinking", Content: "pw " + secretValuesTestPass},
		{Seq: 3, Type: "tool_use", Tool: "Bash", Input: map[string]any{
			"command": "curl -H 'Authorization: x " + secretValuesTestHex + "'",
			"changes": []any{map[string]any{"diff": "+PW=" + secretValuesTestPass}},
		}},
		{Seq: 4, Type: "tool_result", Output: "{\"bearer\":\"" + secretValuesTestHex + "\"}"},
		{Seq: 5, Type: "error", Content: "failed with " + secretValuesTestHex},
	}
	out := scrubTaskMessagesSecretValues(in)
	for _, m := range out {
		blob := m.Content + m.Output + flattenForTest(m.Input)
		if strings.Contains(blob, secretValuesTestHex) || strings.Contains(blob, secretValuesTestPass) {
			t.Fatalf("seq %d still carries a secret value: %q", m.Seq, blob)
		}
	}
	if !strings.Contains(out[0].Content, redact.SecretValuePlaceholder) {
		t.Fatalf("placeholder missing: %q", out[0].Content)
	}
	if !strings.Contains(in[0].Content, secretValuesTestHex) || !strings.Contains(flattenForTest(in[2].Input), secretValuesTestHex) {
		t.Fatal("scrub must not mutate the caller's slice")
	}

	t.Setenv(redact.SecretValuesEnv, "0")
	if got := scrubTaskMessagesSecretValues(in); got[0].Content != in[0].Content {
		t.Fatal("MULTICA_REDACT_SECRET_VALUES=0 must disable masking")
	}
}

// TestClientRedactsSecretValuesOnTheWire checks the actual request bodies the
// daemon sends: transcript batch, completion output, failure error, progress.
func TestClientRedactsSecretValuesOnTheWire(t *testing.T) {
	useSecretValuesFixture(t)
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, r.URL.Path+" "+string(b))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	ctx := context.Background()

	if err := c.ReportTaskMessages(ctx, "t1", []TaskMessageData{
		{Seq: 1, Type: "text", Content: secretValuesTestHex},
		{Seq: 2, Type: "tool_result", Output: secretValuesTestPass},
		{Seq: 3, Type: "tool_use", Input: map[string]any{"a": []any{secretValuesTestHex}}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.completeTaskWithRetrySchedule(ctx, "t1", "done: "+secretValuesTestHex, "", "", "", false, "", "", nil); err != nil {
		t.Fatal(err)
	}
	if err := c.failTaskWithRetrySchedule(ctx, "t1", "err: "+secretValuesTestPass, "", "", "", "", false, "", "", nil); err != nil {
		t.Fatal(err)
	}
	if err := c.ReportProgress(ctx, "t1", "step "+secretValuesTestHex, 1, 2); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 4 {
		t.Fatalf("requests = %d, want 4", len(bodies))
	}
	for _, b := range bodies {
		if strings.Contains(b, secretValuesTestHex) || strings.Contains(b, secretValuesTestPass) {
			t.Fatalf("secret value on the wire: %s", b)
		}
		if !strings.Contains(b, redact.SecretValuePlaceholder) {
			t.Fatalf("placeholder missing: %s", b)
		}
	}
}

// TestTaskSecretValuesRegistration: a task's MULTICA_TOKEN and secret-named
// custom_env values are masked while it is registered and not after; a
// custom_env model name is never masked.
func TestTaskSecretValuesRegistration(t *testing.T) {
	useSecretValuesFixture(t)
	const taskTok = "mat_synthetic_task_token_abcdef"
	const ghTok = "gh-custom-env-synthetic-01"
	const model = "custom-env-synthetic-model"
	task := Task{ID: "task-sv", AuthToken: taskTok, Agent: &AgentData{CustomEnv: map[string]string{
		"GH_TOKEN":        ghTok,
		"ANTHROPIC_MODEL": model,
	}}}
	in := "tok=" + taskTok + " gh=" + ghTok + " model=" + model
	if got := scrubSecretValues(in); got != in {
		t.Fatalf("masked before registration: %q", got)
	}
	unregister := registerTaskSecretValues(task)
	want := "tok=" + redact.SecretValuePlaceholder + " gh=" + redact.SecretValuePlaceholder + " model=" + model
	if got := scrubSecretValues(in); got != want {
		t.Fatalf("registered task values:\n got %q\nwant %q", got, want)
	}
	unregister()
	if got := scrubSecretValues(in); got != in {
		t.Fatalf("still masked after unregister: %q", got)
	}
	if registerTaskSecretValues(Task{ID: "empty"}) == nil {
		t.Fatal("unregister func must never be nil")
	}
}

// TestClientSetTokenMasksDaemonToken: the daemon's own token (set from the
// profile config via SetToken) is masked on the wire; a new token replaces it.
func TestClientSetTokenMasksDaemonToken(t *testing.T) {
	useSecretValuesFixture(t)
	const tok1 = "mul_synthetic_daemon_token_1"
	const tok2 = "mul_synthetic_daemon_token_2"
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	t.Cleanup(func() { c.SetToken("") })
	c.SetToken(tok1)
	if err := c.ReportProgress(context.Background(), "t1", "a "+tok1, 1, 2); err != nil {
		t.Fatal(err)
	}
	c.SetToken(tok2)
	if err := c.ReportProgress(context.Background(), "t1", "b "+tok1+" "+tok2, 1, 2); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("requests = %d", len(bodies))
	}
	if strings.Contains(bodies[0], tok1) {
		t.Fatalf("daemon token on the wire: %s", bodies[0])
	}
	if strings.Contains(bodies[1], tok2) || !strings.Contains(bodies[1], tok1) {
		t.Fatalf("token replacement not applied: %s", bodies[1])
	}
	if c.Token() != tok2 {
		t.Fatal("SetToken must still set the token")
	}
}

// TestTerminalReportMaskedBeforePersist: a terminal report that fails to
// send is persisted already masked, so its replay after the task's values
// were unregistered still carries no secret.
func TestTerminalReportMaskedBeforePersist(t *testing.T) {
	useSecretValuesFixture(t)
	const taskTok = "mat_synthetic_terminal_token_1"
	cfg := Config{
		ServerBaseURL:  "https://api.example.test",
		WorkspacesRoot: t.TempDir(),
		Profile:        "secret-values",
		DaemonID:       "daemon-secret-values",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	unregister := registerTaskSecretValues(Task{ID: "task-sv-term", AuthToken: taskTok})
	d := New(cfg, logger)
	d.terminalReportSend = func(context.Context, terminalTaskReport, []time.Duration) error {
		return errors.New("network unavailable")
	}
	report := terminalTaskReport{
		kind:          terminalTaskReportFail,
		taskID:        "task-sv-term",
		errorMessage:  "failed: " + taskTok,
		failureReason: "agent_error.process_failure",
	}
	if err := d.reportTerminalTask(context.Background(), report); err == nil {
		t.Fatal("report unexpectedly succeeded")
	}
	unregister()

	after := New(cfg, logger)
	var replayed terminalTaskReport
	after.terminalReportSend = func(_ context.Context, got terminalTaskReport, _ []time.Duration) error {
		replayed = got
		return nil
	}
	if _, delivered := after.replayPendingTerminalReports(context.Background()); delivered != 1 {
		t.Fatalf("delivered = %d, want 1", delivered)
	}
	if strings.Contains(replayed.errorMessage, taskTok) || !strings.Contains(replayed.errorMessage, redact.SecretValuePlaceholder) {
		t.Fatalf("replayed report not masked: %q", replayed.errorMessage)
	}
}

func flattenForTest(v any) string {
	var b strings.Builder
	var walk func(any)
	walk = func(v any) {
		switch t := v.(type) {
		case string:
			b.WriteString(t + "|")
		case map[string]any:
			for _, e := range t {
				walk(e)
			}
		case []any:
			for _, e := range t {
				walk(e)
			}
		case []string:
			for _, e := range t {
				walk(e)
			}
		}
	}
	walk(v)
	return b.String()
}
