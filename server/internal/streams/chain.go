package streams

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ExecutorState is one executor_status row.
type ExecutorState struct {
	Executor       string
	ExhaustedUntil time.Time
	ProbeLevel     int
}

// PickExecutor returns the first executor in the chain that is not
// exhausted at now, skipping the ones in avoid (e.g. "change executor after
// two garbage outputs"). ok=false means the chain is exhausted; until is the
// earliest moment any link reopens (§6.4: "budget until T").
func PickExecutor(chain []Executor, states map[string]ExecutorState, avoid map[string]bool, now time.Time) (ex Executor, until time.Time, ok bool) {
	pass := func(skipAvoided bool) (Executor, bool) {
		for _, e := range chain {
			if skipAvoided && avoid[e.Name] {
				continue
			}
			st, has := states[e.Name]
			if has && st.ExhaustedUntil.After(now) {
				if until.IsZero() || st.ExhaustedUntil.Before(until) {
					until = st.ExhaustedUntil
				}
				continue
			}
			return e, true
		}
		return Executor{}, false
	}
	if e, found := pass(true); found {
		return e, time.Time{}, true
	}
	// Avoidance is a preference, not a reason to stand still.
	if len(avoid) > 0 {
		if e, found := pass(false); found {
			return e, time.Time{}, true
		}
	}
	return Executor{}, until, false
}

// probeBackoff is the probe schedule when the provider gives no reset time:
// 15 min, 30 min, 1 h, then hourly (§6.4).
var probeBackoff = []time.Duration{15 * time.Minute, 30 * time.Minute, time.Hour}

// ExhaustUntil computes "exhausted until T" for a quota failure. reset is
// the provider's reset time if the error text carried one.
func ExhaustUntil(now time.Time, reset time.Time, probeLevel int) (time.Time, int) {
	if !reset.IsZero() && reset.After(now) {
		return reset, 0
	}
	if probeLevel >= len(probeBackoff) {
		probeLevel = len(probeBackoff) - 1
	}
	return now.Add(probeBackoff[probeLevel]), probeLevel + 1
}

// Quota detection. Upstream classifies most CLIs into
// agent_error.provider_quota_limit; the DSH bridge only sends a generic
// {code, message}, so 402 is recognised by text until the bridge grows
// codes (§12 "Исполнители").
var quotaText = regexp.MustCompile(`(?i)((status|code|error|http)\W{0,3}402\b|^402\b|payment required|insufficient (credits|balance|quota)|quota (exceeded|exhausted)|usage limit|credit balance is too low|out of credits|rate limit reached for .* per day|hit your limit)`)

// IsQuotaFailure reports whether a task failure means the executor is
// exhausted rather than the step being wrong.
func IsQuotaFailure(failureReason, errText string) bool {
	if failureReason == "agent_error.provider_quota_limit" {
		return true
	}
	return quotaText.MatchString(errText)
}

var resetsAt = regexp.MustCompile(`(?i)(?:resets?|reset_at|retry[- ]after|until)\D{0,12}(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(?::\d{2})?(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2}))`)
var retryAfterSecs = regexp.MustCompile(`(?i)retry[- ]after[:= ]+(\d{1,6})\s*s?\b`)
var unixReset = regexp.MustCompile(`(?i)(?:limit|resets?)\D{0,20}\|(\d{10})\b`)

// ParseQuotaReset extracts a reset moment from provider error text, if any.
func ParseQuotaReset(errText string, now time.Time) time.Time {
	if m := resetsAt.FindStringSubmatch(errText); m != nil {
		if t, err := time.Parse(time.RFC3339Nano, normalizeOffset(m[1])); err == nil {
			return t
		}
	}
	if m := retryAfterSecs.FindStringSubmatch(errText); m != nil {
		if s, err := strconv.Atoi(m[1]); err == nil && s > 0 {
			return now.Add(time.Duration(s) * time.Second)
		}
	}
	// Claude Code prints "Claude AI usage limit reached|1762160400".
	if m := unixReset.FindStringSubmatch(errText); m != nil {
		if s, err := strconv.ParseInt(m[1], 10, 64); err == nil {
			return time.Unix(s, 0)
		}
	}
	return time.Time{}
}

func normalizeOffset(s string) string {
	// +0300 → +03:00 for RFC3339.
	if n := len(s); n >= 5 && (s[n-5] == '+' || s[n-5] == '-') && !strings.Contains(s[n-5:], ":") {
		return s[:n-2] + ":" + s[n-2:]
	}
	return s
}
