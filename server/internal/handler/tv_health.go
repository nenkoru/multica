package handler

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// TV health is the read-only infrastructure aggregate behind the TV screen's
// warning tier (RUSL-1786). Every signal is either "ok", "warning" or
// "unknown"; the screen sounds a master warning only for "warning", so an
// unconfigured or not-yet-confirmed probe stays silent instead of raising a
// false alarm. The response carries counts and coarse reason codes only —
// no URLs, hostnames, runtime names or error text.
const (
	tvHealthOK      = "ok"
	tvHealthWarning = "warning"
	tvHealthUnknown = "unknown"

	tvHealthKeyDaemon = "daemon"
	tvHealthKeyLLM    = "llm"
	tvHealthKeySite   = "site"
	tvHealthKeyOOM    = "oom"

	// A probe result is reused for this long, so a wall of TVs polling once a
	// minute still costs one upstream request per interval.
	tvProbeCacheTTL = 30 * time.Second
	tvProbeTimeout  = 5 * time.Second
	// Consecutive failed probes before a signal turns into a warning. One
	// dropped request is noise; two in a row, at least 30 s apart, is not.
	tvProbeFailThreshold = 2

	// A runtime that heartbeated within this window and is now offline counts
	// as a daemon that went down. Older ones are retired machines, not alarms.
	tvDaemonRecentWindow = 24 * time.Hour
	// A daemon restart flips the runtime offline for a few seconds; only an
	// outage longer than this is reported.
	tvDaemonGrace = 2 * time.Minute
	// OOM-killed runs inside this window keep the oom signal raised.
	tvOOMWindow = 15 * time.Minute
)

type TVHealthSignal struct {
	Key    string `json:"key"`
	Status string `json:"status"`
	// Reason is a stable code ("offline", "http_5xx", "http_status",
	// "unreachable", "timeout", "not_configured", "oom_kill"); the screen maps it to copy.
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

// tvDaemonSignal reports local daemons that heartbeated recently and are now
// offline, grouped by daemon so one machine with three providers counts once.
// A daemon that still has any online runtime is not down.
func (h *Handler) tvDaemonSignal(ctx context.Context, ws pgtype.UUID, now time.Time) (TVHealthSignal, error) {
	var count int64
	var since pgtype.Timestamptz
	err := h.DB.QueryRow(ctx, `
SELECT count(*), min(last_seen)
FROM (
  SELECT r.daemon_id, max(r.last_seen_at) AS last_seen
  FROM agent_runtime r
  WHERE r.workspace_id = $1
    AND r.runtime_mode = 'local'
    AND r.daemon_id IS NOT NULL
  GROUP BY r.daemon_id
  HAVING bool_and(r.status = 'offline')
     AND max(r.last_seen_at) > $2
     AND max(r.last_seen_at) < $3
) d`, ws, now.Add(-tvDaemonRecentWindow), now.Add(-tvDaemonGrace)).Scan(&count, &since)
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
func (h *Handler) tvOOMSignal(ctx context.Context, ws pgtype.UUID, now time.Time) (TVHealthSignal, error) {
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
    t.error ~* '(oom[_ -]?kill|out of memory|signal: killed|exit status 137)'
    OR t.failure_reason ~* 'oom'
  )`, ws, now.Add(-tvOOMWindow)).Scan(&count, &since)
	if err != nil {
		return TVHealthSignal{}, err
	}
	if count == 0 {
		return TVHealthSignal{Key: tvHealthKeyOOM, Status: tvHealthOK}, nil
	}
	return TVHealthSignal{Key: tvHealthKeyOOM, Status: tvHealthWarning, Reason: "oom_kill", Count: count, Since: tvTimestamptz(since)}, nil
}

// GetTVHealth serves GET /api/tv/health with the same kiosk credential as
// the TV overview; the token's workspace scopes the database signals.
func (h *Handler) GetTVHealth(w http.ResponseWriter, r *http.Request) {
	session, ok := h.requireKiosk(w, r)
	if !ok {
		return
	}
	wsUUID := session.WorkspaceID
	ctx := r.Context()
	now := time.Now()

	daemon, err := h.tvDaemonSignal(ctx, wsUUID, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load health")
		return
	}
	oom, err := h.tvOOMSignal(ctx, wsUUID, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load health")
		return
	}

	var llm, site TVHealthSignal
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		llm = tvProbeFor(h.cfg.TVHealthLLMURL).check(ctx, tvHealthKeyLLM, h.cfg.TVHealthLLMURL, h.cfg.TVHealthLLMToken, now, tvProbeOnce)
	}()
	go func() {
		defer wg.Done()
		site = tvProbeFor(h.cfg.TVHealthSiteURL).check(ctx, tvHealthKeySite, h.cfg.TVHealthSiteURL, "", now, tvProbeOnce)
	}()
	wg.Wait()

	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, TVHealthResponse{
		Signals:   []TVHealthSignal{daemon, llm, oom, site},
		CheckedAt: now.UTC().Format(time.RFC3339),
	})
}
