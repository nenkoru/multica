package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestParseTVConfig(t *testing.T) {
	env := map[string]string{"TV_LLM_TOKEN": " secret-value "}
	getenv := func(k string) string { return env[k] }

	cfg, err := ParseTVConfig("", getenv)
	if err != nil || cfg.HostCeiling != 12 || cfg.StallAfter != 10*time.Minute || cfg.FailedBurst != 5 || cfg.DuplicateRunAfter != 2*time.Minute || cfg.MinimumsPercent != 70 {
		t.Fatalf("defaults: %+v %v", cfg, err)
	}

	cfg, err = ParseTVConfig(`{
		"host_ceiling": 8, "stall_minutes": 15, "failed_burst": 3, "duplicate_run_minutes": 5, "minimums_percent": 60,
		"watched_daemons": [" srv-a ", "", "srv-b"],
		"probes": {"llm": {"url": " http://llm.test/health ", "bearer_env": "TV_LLM_TOKEN"}, "site": {"url": ""}}
	}`, getenv)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HostCeiling != 8 || cfg.StallAfter != 15*time.Minute || cfg.FailedBurst != 3 || cfg.MinimumsPercent != 60 || cfg.DuplicateRunAfter != 5*time.Minute || cfg.OOMWindow != 15*time.Minute {
		t.Fatalf("overrides: %+v", cfg)
	}
	if strings.Join(cfg.WatchedDaemons, ",") != "srv-a,srv-b" {
		t.Fatalf("watched daemons: %v", cfg.WatchedDaemons)
	}
	if p := cfg.Probes["llm"]; p.URL != "http://llm.test/health" || p.Bearer != "secret-value" {
		t.Fatalf("llm probe: %+v", p)
	}
	if _, ok := cfg.Probes["site"]; ok {
		t.Fatal("an empty URL leaves the probe unconfigured")
	}

	for _, bad := range []string{
		`{"stall_minutes": 0}`,
		`{"minimums_percent": 120}`,
		`{"probes": {"gpu": {"url": "http://x"}}}`,
		`{"unknown_field": 1}`,
		`not json`,
	} {
		cfg, err := ParseTVConfig(bad, getenv)
		if err == nil {
			t.Fatalf("%s: want error", bad)
		}
		if cfg.HostCeiling != 12 {
			t.Fatalf("%s: an invalid config falls back to defaults, got %+v", bad, cfg)
		}
	}
}

func TestTvMinimumsSignal(t *testing.T) {
	cfg := DefaultTVConfig()
	// 14.10 18:00 MSK; a sprint ending 14.10 ends at 15.10 00:00 MSK, 6 h left.
	now := time.Date(2026, 10, 14, 15, 0, 0, 0, time.UTC)
	behind := &TVSprint{EndDate: "2026-10-14", Total: 10, Done: 6}
	if s := tvMinimumsSignal(behind, now, cfg); s.Status != tvHealthWarning || s.Count != 60 {
		t.Fatalf("behind: %+v", s)
	}
	onTrack := &TVSprint{EndDate: "2026-10-14", Total: 10, Done: 7}
	if s := tvMinimumsSignal(onTrack, now, cfg); s.Status != tvHealthOK {
		t.Fatalf("on track: %+v", s)
	}
	farAway := &TVSprint{EndDate: "2026-10-17", Total: 10, Done: 1}
	if s := tvMinimumsSignal(farAway, now, cfg); s.Status != tvHealthOK {
		t.Fatalf("far away: %+v", s)
	}
	if s := tvMinimumsSignal(nil, now, cfg); s.Status != tvHealthOK {
		t.Fatalf("no sprint: %+v", s)
	}
}

func TestTvWorkAndAutopilotSignals(t *testing.T) {
	ctx := context.Background()
	ws := parseUUID(testWorkspaceID)
	cfg := DefaultTVConfig()
	cfg.FailedBurst = 2

	runtime := dbfx.Runtime(t, "tv signals runtime")
	agent := dbfx.Agent(t, "tv signals agent", runtime)
	issue := dbfx.Issue(t, "tv signals issue")

	// A task queued 20 minutes ago and nothing running: the queue stalls.
	queued := dbfx.Task(t, agent, testutil.Cols{"issue_id": issue, "status": "queued", "runtime_id": runtime, "created_at": time.Now().Add(-20 * time.Minute)})
	signals, err := testHandler.tvWorkSignals(ctx, ws, time.Now(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if signals[0].Key != tvHealthKeyStall || signals[0].Status != tvHealthWarning || signals[0].Count < 1 {
		t.Fatalf("stall: %+v", signals[0])
	}

	// A run on the issue and one on its sub-issue (work and its review) are
	// normal; a second run on the same issue is a duplicate.
	child := dbfx.Issue(t, "tv signals child", testutil.Cols{"parent_issue_id": issue})
	dbfx.Task(t, agent, testutil.Cols{"issue_id": issue, "status": "running", "runtime_id": runtime, "started_at": time.Now().Add(-10 * time.Minute)})
	dbfx.Task(t, agent, testutil.Cols{"issue_id": child, "status": "running", "runtime_id": runtime, "started_at": time.Now()})
	signals, err = testHandler.tvWorkSignals(ctx, ws, time.Now(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if signals[2].Key != tvHealthKeyDuplicateRun || signals[2].Status != tvHealthOK {
		t.Fatalf("parent and sub-issue runs are not a duplicate: %+v", signals[2])
	}
	// Two chat runs at once have no issue at all: not a duplicate either.
	for _, name := range []string{"tv chat a", "tv chat b"} {
		chatAgent := dbfx.Agent(t, name, runtime)
		session := dbfx.ChatSession(t, chatAgent)
		dbfx.Task(t, chatAgent, testutil.Cols{"issue_id": nil, "chat_session_id": session, "status": "running", "runtime_id": runtime, "started_at": time.Now()})
	}
	signals, err = testHandler.tvWorkSignals(ctx, ws, time.Now(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if signals[2].Status != tvHealthOK {
		t.Fatalf("chat runs are not a duplicate: %+v", signals[2])
	}
	// A second agent picks the same issue up while the first run is still
	// finishing. Overlapping for 30 s is a hand-off, not a duplicate.
	other := dbfx.Agent(t, "tv signals second agent", runtime)
	dbfx.Task(t, other, testutil.Cols{"issue_id": issue, "status": "running", "runtime_id": runtime, "started_at": time.Now().Add(-30 * time.Second)})
	signals, err = testHandler.tvWorkSignals(ctx, ws, time.Now(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if signals[2].Status != tvHealthOK {
		t.Fatalf("a 30 s hand-off is not a duplicate: %+v", signals[2])
	}
	for _, n := range []int{1, 2} {
		dbfx.Task(t, agent, testutil.Cols{"issue_id": issue, "status": "failed", "error": "boom " + strings.Repeat("x", n), "completed_at": time.Now().Add(-time.Minute)})
	}
	// Both runs are still going 2.5 min later: 3 min together is a duplicate.
	signals, err = testHandler.tvWorkSignals(ctx, ws, time.Now().Add(150*time.Second), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if signals[0].Status != tvHealthOK {
		t.Fatalf("stall with runs going: %+v", signals[0])
	}
	if signals[1].Key != tvHealthKeyFailedBurst || signals[1].Status != tvHealthWarning || signals[1].Count < 2 {
		t.Fatalf("failed burst: %+v", signals[1])
	}
	if signals[2].Key != tvHealthKeyDuplicateRun || signals[2].Status != tvHealthWarning || signals[2].Count != 1 {
		t.Fatalf("duplicate run: %+v", signals[2])
	}
	_ = queued

	autopilot := dbfx.Insert(t, "autopilot", testutil.Cols{
		"workspace_id": testWorkspaceID, "title": "tv signals autopilot", "assignee_id": agent,
		"created_by_type": "member", "created_by_id": testUserID,
	})
	if s, err := testHandler.tvAutopilotSignal(ctx, ws, time.Now(), cfg.AutopilotWindow); err != nil || s.Status != tvHealthOK {
		t.Fatalf("autopilot before failure: %+v %v", s, err)
	}
	dbfx.Insert(t, "autopilot_run", testutil.Cols{
		"autopilot_id": autopilot, "source": "schedule", "status": "failed", "completed_at": time.Now().Add(-5 * time.Minute),
	})
	if s, err := testHandler.tvAutopilotSignal(ctx, ws, time.Now(), cfg.AutopilotWindow); err != nil || s.Status != tvHealthWarning || s.Count != 1 {
		t.Fatalf("autopilot after failure: %+v %v", s, err)
	}
}

func TestTVSoundServesOnlyKnownFilesToKiosks(t *testing.T) {
	var created CreateKioskTokenResponse
	testutil.Call(t, testHandler.CreateKioskToken, newRequest("POST", "/api/kiosk-tokens", map[string]string{
		"name": "TV sounds", "owner_label": "Owner",
	})).Want(http.StatusCreated).JSON(&created)
	dbfx.Cleanup(t, `DELETE FROM kiosk_token WHERE id = $1`, created.ID)

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tcas_clear.mp3"), []byte("ID3fake"), 0o600); err != nil {
		t.Fatal(err)
	}
	previous := testHandler.cfg.TV.SoundsDir
	testHandler.cfg.TV.SoundsDir = dir
	t.Cleanup(func() { testHandler.cfg.TV.SoundsDir = previous })

	get := func(name, token string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		testHandler.GetTVSound(rec, withURLParam(tvRequest("GET", "/api/tv/sounds/"+name, token, nil), "file", name))
		return rec
	}
	if rec := get("tcas_clear.mp3", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("without kiosk token: %d", rec.Code)
	}
	if rec := get("tcas_clear.mp3", created.Token); rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "audio/mpeg" || rec.Body.String() != "ID3fake" {
		t.Fatalf("known file: %d %q %q", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
	for _, name := range []string{"missing.mp3", "..%2Fsecret.mp3", "tcas_clear.ogg", "../tv_sounds.go"} {
		if rec := get(name, created.Token); rec.Code != http.StatusNotFound {
			t.Fatalf("%s: %d", name, rec.Code)
		}
	}
}
