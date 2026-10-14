package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/testutil"
)

func tvSignal(t *testing.T, resp TVHealthResponse, key string) TVHealthSignal {
	t.Helper()
	for _, s := range resp.Signals {
		if s.Key == key {
			return s
		}
	}
	t.Fatalf("signal %q missing in %+v", key, resp.Signals)
	return TVHealthSignal{}
}

func TestTvProbeNeedsTwoFailuresAndCaches(t *testing.T) {
	calls := 0
	reason := "unreachable"
	probe := func(context.Context, string, string) string { calls++; return reason }
	p := &tvProbe{}
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	if got := p.check(ctx, "llm", "http://llm.test", "", now, probe); got.Status != tvHealthUnknown || got.Reason != "unreachable" {
		t.Fatalf("first failure must stay unknown, got %+v", got)
	}
	// Inside the TTL the cached result is reused and the counter does not move.
	if got := p.check(ctx, "llm", "http://llm.test", "", now.Add(10*time.Second), probe); got.Status != tvHealthUnknown || calls != 1 {
		t.Fatalf("cached check: %+v calls=%d", got, calls)
	}
	got := p.check(ctx, "llm", "http://llm.test", "", now.Add(tvProbeCacheTTL), probe)
	if got.Status != tvHealthWarning || got.Count != 2 || calls != 2 {
		t.Fatalf("second failure must warn, got %+v calls=%d", got, calls)
	}
	if got.Since == nil || *got.Since != "2026-10-14T12:00:00Z" {
		t.Fatalf("since must be the first failure, got %v", got.Since)
	}
	reason = ""
	if got := p.check(ctx, "llm", "http://llm.test", "", now.Add(2*tvProbeCacheTTL), probe); got.Status != tvHealthOK || got.Since != nil {
		t.Fatalf("recovery must reset, got %+v", got)
	}
}

func TestTvProbeNotConfiguredIsUnknown(t *testing.T) {
	got := (&tvProbe{}).check(context.Background(), "site", "", "", time.Now(), func(context.Context, string, string) string {
		t.Fatal("probe must not run without a URL")
		return ""
	})
	if got.Status != tvHealthUnknown || got.Reason != "not_configured" {
		t.Fatalf("got %+v", got)
	}
}

func TestTvProbeOnceClassifiesResponses(t *testing.T) {
	status := http.StatusOK
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.WriteHeader(status)
	}))
	defer srv.Close()
	ctx := context.Background()

	if got := tvProbeOnce(ctx, srv.URL, "tok"); got != "" || auth != "Bearer tok" {
		t.Fatalf("200: reason=%q auth=%q", got, auth)
	}
	status = http.StatusInternalServerError
	if got := tvProbeOnce(ctx, srv.URL, ""); got != "http_5xx" {
		t.Fatalf("500: %q", got)
	}
	status = http.StatusFound
	// A redirect (to a login page, say) is not followed and is not healthy.
	if got := tvProbeOnce(ctx, srv.URL, ""); got != "http_status" {
		t.Fatalf("302: %q", got)
	}
	srv.Close()
	if got := tvProbeOnce(ctx, srv.URL, ""); got != "unreachable" {
		t.Fatalf("closed: %q", got)
	}
}

func TestTvHealthDaemonAndOOM(t *testing.T) {
	// A daemon that went offline 10 minutes ago, with two providers.
	for _, provider := range []string{"tv_claude", "tv_codex"} {
		dbfx.Runtime(t, "tv down "+provider, testutil.Cols{
			"runtime_mode": "local", "daemon_id": "tv-daemon-down", "provider": provider,
			"status": "offline", "last_seen_at": time.Now().Add(-10 * time.Minute),
		})
	}
	// Restarting right now (inside the grace period): not an alarm.
	dbfx.Runtime(t, "tv restarting", testutil.Cols{
		"runtime_mode": "local", "daemon_id": "tv-daemon-restart", "provider": "tv_claude",
		"status": "offline", "last_seen_at": time.Now().Add(-20 * time.Second),
	})
	// A laptop closed for the night: not watched, not an alarm.
	dbfx.Runtime(t, "tv laptop", testutil.Cols{
		"runtime_mode": "local", "daemon_id": "tv-daemon-laptop", "provider": "tv_claude",
		"status": "offline", "last_seen_at": time.Now().Add(-3 * time.Hour),
	})
	// One provider offline but the same daemon still online: not an alarm.
	dbfx.Runtime(t, "tv half a", testutil.Cols{
		"runtime_mode": "local", "daemon_id": "tv-daemon-half", "provider": "tv_claude",
		"status": "offline", "last_seen_at": time.Now().Add(-10 * time.Minute),
	})
	online := dbfx.Runtime(t, "tv half b", testutil.Cols{
		"runtime_mode": "local", "daemon_id": "tv-daemon-half", "provider": "tv_codex",
	})

	agent := dbfx.Agent(t, "tv oom agent", online)
	issue := dbfx.Issue(t, "tv oom issue")
	failed := func(errText string, at time.Time, reason ...string) {
		cols := testutil.Cols{"issue_id": issue, "status": "failed", "error": errText, "completed_at": at}
		if len(reason) > 0 {
			cols["failure_reason"] = reason[0]
		}
		dbfx.Task(t, agent, cols)
	}
	failed("worker exited: signal: killed (oom_kill)", time.Now().Add(-3*time.Minute))
	failed("exit status 137", time.Now().Add(-5*time.Minute))
	failed("model returned 429", time.Now().Add(-1*time.Minute))
	failed("Out of memory", time.Now().Add(-2*time.Hour))
	// Killed by its own timeout or idle watchdog: SIGKILL, but not memory.
	failed("worker exited: signal: killed", time.Now().Add(-2*time.Minute))
	failed("exit status 137", time.Now().Add(-2*time.Minute), "timeout")
	failed("exit status 137", time.Now().Add(-2*time.Minute), "agent_error.idle_watchdog")

	previous := testHandler.cfg.TV
	testHandler.cfg.TV = DefaultTVConfig()
	testHandler.cfg.TV.WatchedDaemons = []string{"tv-daemon-down", "tv-daemon-restart", "tv-daemon-half"}
	t.Cleanup(func() { testHandler.cfg.TV = previous })

	var created CreateKioskTokenResponse
	testutil.Call(t, testHandler.CreateKioskToken, newRequest("POST", "/api/kiosk-tokens", map[string]string{
		"name": "TV health", "owner_label": "Owner",
	})).Want(http.StatusCreated).JSON(&created)
	dbfx.Cleanup(t, `DELETE FROM kiosk_token WHERE id = $1`, created.ID)

	// Without a kiosk token the aggregate is closed.
	testutil.Call(t, testHandler.GetTVHealth, tvRequest("GET", "/api/tv/health", "", nil)).Want(http.StatusUnauthorized)

	var got TVHealthResponse
	testutil.Call(t, testHandler.GetTVHealth, tvRequest("GET", "/api/tv/health", created.Token, nil)).Want(http.StatusOK).JSON(&got)

	daemon := tvSignal(t, got, tvHealthKeyDaemon)
	if daemon.Status != tvHealthWarning || daemon.Count != 1 || daemon.Since == nil {
		t.Fatalf("daemon: %+v", daemon)
	}
	// Without a watch list the daemon signal never sounds.
	testHandler.cfg.TV.WatchedDaemons = nil
	var unwatched TVHealthResponse
	testutil.Call(t, testHandler.GetTVHealth, tvRequest("GET", "/api/tv/health", created.Token, nil)).Want(http.StatusOK).JSON(&unwatched)
	if s := tvSignal(t, unwatched, tvHealthKeyDaemon); s.Status != tvHealthUnknown || s.Reason != "not_configured" {
		t.Fatalf("unwatched daemon: %+v", s)
	}
	oom := tvSignal(t, got, tvHealthKeyOOM)
	if oom.Status != tvHealthWarning || oom.Count != 2 {
		t.Fatalf("oom: %+v", oom)
	}
	// Order follows the sound table's priority.
	keys := []string{}
	for _, s := range got.Signals {
		keys = append(keys, s.Key)
	}
	if strings.Join(keys, ",") != "daemon,llm,site,stall,budget,oom,autopilot,failed_burst,duplicate_run,llm_slow,minimums" {
		t.Fatalf("signal order: %v", keys)
	}
	for _, key := range []string{tvHealthKeyLLM, tvHealthKeySite, tvHealthKeyBudget, tvHealthKeyLLMSlow} {
		if s := tvSignal(t, got, key); s.Status != tvHealthUnknown || s.Reason != "not_configured" {
			t.Fatalf("%s: %+v", key, s)
		}
	}
}
