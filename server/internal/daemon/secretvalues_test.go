package daemon

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

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
