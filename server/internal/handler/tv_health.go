package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/logger"
)

// TV health is the read-only aggregate behind the TV screen's alarms
// (RUSL-1786). Every signal is "ok", "warning" (raised) or "unknown"; the
// screen decides from its sound table whether a raised signal is a warning or
// a caution, and never sounds "unknown", so an unconfigured or
// not-yet-confirmed probe stays silent instead of raising a false alarm. The
// response carries counts and coarse reason codes only — no URLs, hostnames,
// runtime names or error text. Thresholds come from TVConfig.
const (
	tvHealthOK      = "ok"
	tvHealthWarning = "warning"
	tvHealthUnknown = "unknown"

	tvHealthKeyDaemon       = "daemon"
	tvHealthKeyLLM          = "llm"
	tvHealthKeySite         = "site"
	tvHealthKeyStall        = "stall"
	tvHealthKeyBudget       = "budget"
	tvHealthKeyOOM          = "oom"
	tvHealthKeyAutopilot    = "autopilot"
	tvHealthKeyFailedBurst  = "failed_burst"
	tvHealthKeyDuplicateRun = "duplicate_run"
	tvHealthKeyLLMSlow      = "llm_slow"
	tvHealthKeyMinimums     = "minimums"

	// A probe result is reused for this long, so a wall of TVs polling once a
	// minute still costs one upstream request per interval.
	tvProbeCacheTTL = 30 * time.Second
	tvProbeTimeout  = 5 * time.Second
	// Consecutive failed probes before a signal turns into a warning. One
	// dropped request is noise; two in a row, at least 30 s apart, is not.
	tvProbeFailThreshold = 2

	// A daemon restart flips the runtime offline for a few seconds; only an
	// outage longer than this is reported.
	tvDaemonGrace = 2 * time.Minute
)

type TVHealthSignal struct {
	Key    string `json:"key"`
	Status string `json:"status"`
	// Reason is a stable code ("offline", "http_5xx", "http_status",
	// "unreachable", "timeout", "not_configured", "oom_kill", "queue_idle",
	// "failed_runs", "autopilot_failed", "same_issue", "sprint_behind");
	// the screen maps it to copy.
	Reason string `json:"reason,omitempty"`
	Count  int64  `json:"count"`
	// Since is when the current non-ok state was first observed (RFC3339).
	Since *string `json:"since,omitempty"`
}

type TVHealthResponse struct {
	Signals   []TVHealthSignal `json:"signals"`
	CheckedAt string           `json:"checked_at"`
}

// tvProbe tracks one HTTP endpoint across requests.
type tvProbe struct {
	mu        sync.Mutex
	checkedAt time.Time
	failures  int
	reason    string
	since     time.Time
}

var (
	tvProbes     sync.Map // url -> *tvProbe
	tvHTTPClient = &http.Client{
		Timeout: tvProbeTimeout,
		// A redirect to a login page would read as healthy; report it as is.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
)

func tvProbeFor(url string) *tvProbe {
	p, _ := tvProbes.LoadOrStore(url, &tvProbe{})
	return p.(*tvProbe)
}

// tvProbeOnce returns "" for a healthy response or a reason code.
func tvProbeOnce(ctx context.Context, url, bearer string) string {
	ctx, cancel := context.WithTimeout(ctx, tvProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "unreachable"
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := tvHTTPClient.Do(req)
	if err != nil {
		var netErr net.Error
		if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
			return "timeout"
		}
		return "unreachable"
	}
	resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return ""
	}
	if resp.StatusCode >= 500 {
		return "http_5xx"
	}
	return "http_status"
}

// check runs the probe at most once per TTL and folds the result into the
// consecutive-failure counter.
func (p *tvProbe) check(ctx context.Context, key, url, bearer string, now time.Time, probe func(context.Context, string, string) string) TVHealthSignal {
	if url == "" {
		return TVHealthSignal{Key: key, Status: tvHealthUnknown, Reason: "not_configured"}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.checkedAt.IsZero() || now.Sub(p.checkedAt) >= tvProbeCacheTTL {
		// The cached result outlives this request: a TV that disconnects
		// mid-probe must not record a failure for everyone else.
		reason := probe(context.WithoutCancel(ctx), url, bearer)
		p.checkedAt = now
		if reason == "" {
			p.failures, p.reason, p.since = 0, "", time.Time{}
		} else {
			if p.failures == 0 {
				p.since = now
			}
			p.failures++
			p.reason = reason
		}
	}
	switch {
	case p.failures == 0:
		return TVHealthSignal{Key: key, Status: tvHealthOK}
	case p.failures < tvProbeFailThreshold:
		return TVHealthSignal{Key: key, Status: tvHealthUnknown, Reason: p.reason, Count: int64(p.failures), Since: tvTime(p.since)}
	default:
		return TVHealthSignal{Key: key, Status: tvHealthWarning, Reason: p.reason, Count: int64(p.failures), Since: tvTime(p.since)}
	}
}

func tvTime(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

func tvTimestamptz(t pgtype.Timestamptz) *string {
	if !t.Valid {
		return nil
	}
	return tvTime(t.Time)
}

// tvDaemonSignal reports watched daemons (TVConfig.WatchedDaemons, by
// daemon id) that have had every runtime offline for longer than the grace
// period. Only servers belong on that list: a laptop that is closed for the
// night would otherwise raise a master warning every evening. With no list
// the signal is "unknown" and never sounds.
func (h *Handler) tvDaemonSignal(ctx context.Context, ws pgtype.UUID, now time.Time, watched []string) (TVHealthSignal, error) {
	if len(watched) == 0 {
		return TVHealthSignal{Key: tvHealthKeyDaemon, Status: tvHealthUnknown, Reason: "not_configured"}, nil
	}
	var count int64
	var since pgtype.Timestamptz
	err := h.DB.QueryRow(ctx, `
SELECT count(*), min(last_seen)
FROM (
  SELECT r.daemon_id, max(r.last_seen_at) AS last_seen
  FROM agent_runtime r
  WHERE r.workspace_id = $1
    AND r.daemon_id = ANY($2::text[])
  GROUP BY r.daemon_id
  HAVING bool_and(r.status = 'offline')
     AND max(r.last_seen_at) < $3
) d`, ws, watched, now.Add(-tvDaemonGrace)).Scan(&count, &since)
	if err != nil {
		return TVHealthSignal{}, err
	}
	if count == 0 {
		return TVHealthSignal{Key: tvHealthKeyDaemon, Status: tvHealthOK}, nil
	}
	return TVHealthSignal{Key: tvHealthKeyDaemon, Status: tvHealthWarning, Reason: "offline", Count: count, Since: tvTimestamptz(since)}, nil
}

// tvOOMSignal counts runs in this workspace that failed with an
// out-of-memory kill inside the window. The daemon reports the child's exit,
// so the kernel's wording or exit status 137 is what reaches `error`.
func (h *Handler) tvOOMSignal(ctx context.Context, ws pgtype.UUID, now time.Time, window time.Duration) (TVHealthSignal, error) {
	var count int64
	var since pgtype.Timestamptz
	err := h.DB.QueryRow(ctx, `
SELECT count(*), min(t.completed_at)
FROM agent_task_queue t
JOIN agent a ON a.id = t.agent_id
WHERE a.workspace_id = $1
  AND t.status = 'failed'
  AND t.completed_at > $2
  AND (
    t.error ~* '(oom[_ -]?kill|out of memory|exit status 137)'
    OR t.failure_reason ~* 'oom'
  )
  -- A run killed by its own timeout or idle watchdog also exits on SIGKILL.
  AND COALESCE(t.failure_reason, '') !~* '(timeout|idle)'`, ws, now.Add(-window)).Scan(&count, &since)
	if err != nil {
		return TVHealthSignal{}, err
	}
	if count == 0 {
		return TVHealthSignal{Key: tvHealthKeyOOM, Status: tvHealthOK}, nil
	}
	return TVHealthSignal{Key: tvHealthKeyOOM, Status: tvHealthWarning, Reason: "oom_kill", Count: count, Since: tvTimestamptz(since)}, nil
}

// tvCountSignal turns a count and its oldest timestamp into a signal that is
// raised when count >= threshold.
func tvCountSignal(key, reason string, count int64, since pgtype.Timestamptz, threshold int64) TVHealthSignal {
	if count < threshold || count == 0 {
		return TVHealthSignal{Key: key, Status: tvHealthOK, Count: count}
	}
	return TVHealthSignal{Key: key, Status: tvHealthWarning, Reason: reason, Count: count, Since: tvTimestamptz(since)}
}

// tvWorkSignals reads the run-queue signals in one round trip: stall (no run
// going while a task has waited past the threshold), failed_burst and
// duplicate_run (two active runs on the same issue for longer than
// DuplicateRunAfter). A run on a parent and one on its sub-issue — the usual
// work and review pair — are not a duplicate, and neither is a hand-off where
// the next agent starts seconds before the previous run ends.
func (h *Handler) tvWorkSignals(ctx context.Context, ws pgtype.UUID, now time.Time, cfg TVConfig) ([]TVHealthSignal, error) {
	var running, waiting, failed, duplicates int64
	var oldestWaiting, firstFailed pgtype.Timestamptz
	err := h.DB.QueryRow(ctx, `
WITH q AS (
  SELECT t.status, t.created_at, t.completed_at, t.issue_id,
    COALESCE(t.started_at, t.dispatched_at, t.created_at) AS active_since
  FROM agent_task_queue t
  JOIN agent a ON a.id = t.agent_id
  WHERE a.workspace_id = $1
    AND (t.status IN ('queued', 'dispatched', 'running') OR (t.status = 'failed' AND t.completed_at > $3))
)
SELECT
  count(*) FILTER (WHERE status IN ('dispatched', 'running')),
  count(*) FILTER (WHERE status = 'queued' AND created_at < $2),
  min(created_at) FILTER (WHERE status = 'queued'),
  count(*) FILTER (WHERE status = 'failed'),
  min(completed_at) FILTER (WHERE status = 'failed'),
  (SELECT count(*) FROM (
     SELECT issue_id FROM q
     -- Chat runs have no issue; GROUP BY would lump them together. Two runs
     -- have overlapped past the threshold once both were active before $4.
     WHERE status IN ('dispatched', 'running') AND issue_id IS NOT NULL AND active_since < $4
     GROUP BY issue_id HAVING count(*) > 1
   ) d)
FROM q`, ws, now.Add(-cfg.StallAfter), now.Add(-cfg.FailedBurstWindow), now.Add(-cfg.DuplicateRunAfter)).Scan(
		&running, &waiting, &oldestWaiting, &failed, &firstFailed, &duplicates)
	if err != nil {
		return nil, err
	}
	stall := TVHealthSignal{Key: tvHealthKeyStall, Status: tvHealthOK}
	if running == 0 && waiting > 0 {
		stall = TVHealthSignal{Key: tvHealthKeyStall, Status: tvHealthWarning, Reason: "queue_idle", Count: waiting, Since: tvTimestamptz(oldestWaiting)}
	}
	return []TVHealthSignal{
		stall,
		tvCountSignal(tvHealthKeyFailedBurst, "failed_runs", failed, firstFailed, int64(cfg.FailedBurst)),
		tvCountSignal(tvHealthKeyDuplicateRun, "same_issue", duplicates, pgtype.Timestamptz{}, 1),
	}, nil
}

func (h *Handler) tvAutopilotSignal(ctx context.Context, ws pgtype.UUID, now time.Time, window time.Duration) (TVHealthSignal, error) {
	var count int64
	var since pgtype.Timestamptz
	err := h.DB.QueryRow(ctx, `
SELECT count(*), min(r.completed_at)
FROM autopilot_run r
JOIN autopilot a ON a.id = r.autopilot_id
WHERE a.workspace_id = $1 AND a.status = 'active' AND r.status = 'failed' AND r.completed_at > $2`,
		ws, now.Add(-window)).Scan(&count, &since)
	if err != nil {
		return TVHealthSignal{}, err
	}
	return tvCountSignal(tvHealthKeyAutopilot, "autopilot_failed", count, since, 1), nil
}

// tvMinimumsSignal: the active sprint ends (end of its last day, Moscow time)
// within the threshold with too little done.
func tvMinimumsSignal(sprint *TVSprint, now time.Time, cfg TVConfig) TVHealthSignal {
	ok := TVHealthSignal{Key: tvHealthKeyMinimums, Status: tvHealthOK}
	if sprint == nil || sprint.Total == 0 {
		return ok
	}
	end, err := time.ParseInLocation("2006-01-02", sprint.EndDate, tvMoscow)
	if err != nil {
		return ok
	}
	end = end.Add(24 * time.Hour)
	left := end.Sub(now)
	donePercent := sprint.Done * 100 / sprint.Total
	if left <= 0 || left > time.Duration(cfg.MinimumsHours)*time.Hour || donePercent >= int64(cfg.MinimumsPercent) {
		return ok
	}
	return TVHealthSignal{Key: tvHealthKeyMinimums, Status: tvHealthWarning, Reason: "sprint_behind", Count: donePercent}
}

var tvMoscow = time.FixedZone("MSK", 3*60*60)

// GetTVHealth serves GET /api/tv/health with the same kiosk credential as
// the TV overview; the token's workspace scopes the database signals.
func (h *Handler) GetTVHealth(w http.ResponseWriter, r *http.Request) {
	session, ok := h.requireKiosk(w, r)
	if !ok {
		return
	}
	resp, err := h.loadTVHealth(r.Context(), session.WorkspaceID, time.Now())
	if err != nil {
		slog.Error("tv health failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to load health")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) loadTVHealth(ctx context.Context, ws pgtype.UUID, now time.Time) (TVHealthResponse, error) {
	cfg := h.cfg.TV
	if cfg.HostCeiling == 0 {
		cfg = DefaultTVConfig()
	}
	daemon, err := h.tvDaemonSignal(ctx, ws, now, cfg.WatchedDaemons)
	if err != nil {
		return TVHealthResponse{}, fmt.Errorf("daemon: %w", err)
	}
	work, err := h.tvWorkSignals(ctx, ws, now, cfg)
	if err != nil {
		return TVHealthResponse{}, fmt.Errorf("work: %w", err)
	}
	oom, err := h.tvOOMSignal(ctx, ws, now, cfg.OOMWindow)
	if err != nil {
		return TVHealthResponse{}, fmt.Errorf("oom: %w", err)
	}
	autopilot, err := h.tvAutopilotSignal(ctx, ws, now, cfg.AutopilotWindow)
	if err != nil {
		return TVHealthResponse{}, fmt.Errorf("autopilot: %w", err)
	}
	sprint, err := h.loadTVSprint(ctx, ws)
	if err != nil {
		return TVHealthResponse{}, fmt.Errorf("sprint: %w", err)
	}

	probed := make([]TVHealthSignal, len(tvProbeKeys))
	var wg sync.WaitGroup
	for n, key := range tvProbeKeys {
		probe := cfg.Probes[key]
		wg.Add(1)
		go func() {
			defer wg.Done()
			probed[n] = tvProbeFor(key+"|"+probe.URL).check(ctx, key, probe.URL, probe.Bearer, now, tvProbeOnce)
		}()
	}
	wg.Wait()
	byKey := map[string]TVHealthSignal{}
	for _, s := range probed {
		byKey[s.Key] = s
	}

	// Order follows the sound table's priority, most important first.
	signals := []TVHealthSignal{
		daemon,
		byKey[tvHealthKeyLLM],
		byKey[tvHealthKeySite],
		work[0],
		byKey[tvHealthKeyBudget],
		oom,
		autopilot,
		work[1],
		work[2],
		byKey[tvHealthKeyLLMSlow],
		tvMinimumsSignal(sprint, now, cfg),
	}
	return TVHealthResponse{Signals: signals, CheckedAt: now.UTC().Format(time.RFC3339)}, nil
}
