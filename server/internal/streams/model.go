// Package streams is the Fabrika harness (harness-design.md): streams that
// never stand still. A code supervisor keeps every active stream holding
// exactly one live reason (§5); LLM agents only decide what to do next.
//
// The package is fork-owned. It touches upstream code only through
// interfaces (Carrier) wired in cmd/server, its own sqlc package (sdb) and
// migrations numbered 9000+.
package streams

import (
	"encoding/json"
	"time"
)

// Stream states (§5.1).
const (
	StateRunning = "running"
	StateWaiting = "waiting"
	StateAsking  = "asking"
	StateBudget  = "budget"
	StatePaused  = "paused"
	StateBroken  = "broken"
)

// Step kinds and statuses (§5.2).
const (
	KindManage    = "manage"
	KindWork      = "work"
	KindVerify    = "verify"
	KindSummarize = "summarize"

	StepQueued    = "queued"
	StepRunning   = "running"
	StepVerifying = "verifying"
	StepDone      = "done"
	StepFailed    = "failed"
	StepCancelled = "cancelled"
)

// Tunables from the design. They are constants, not config, on purpose:
// changing them changes the invariant and belongs in review.
const (
	TickInterval        = 30 * time.Second
	LeaseDuration       = 10 * time.Minute
	MaxStepAttempts     = 3
	MaxRejections       = 2
	BrokenAfterFailures = 3
	// Summary compression cadence (§6.2).
	SummarizeEvery = 20
	// Stall detector: no milestone for this long forces a plan (§6.5).
	StallAfter = 72 * time.Hour
	// "No ideas" parks the stream for a review (§6.5).
	NoIdeasReview = 24 * time.Hour
	// Rerouting to another runtime after this long without one (§6.3).
	OfflineReroute = 10 * time.Minute
	// Class S runs on the local 27B, which can stay silent for more than
	// 10 min while it prefills behind other sessions or writes a long
	// answer in one call (RUSL-2620, RUSL-2636).
	LeaseDurationS = 25 * time.Minute
)

// LeaseFor is how long a step may go without progress before it counts
// as hung.
func LeaseFor(costClass string) time.Duration {
	if costClass == "S" {
		return LeaseDurationS
	}
	return LeaseDuration
}

// Charter is the stream's constitution (§4.2, §11). Stored as JSONB.
type Charter struct {
	Goal    string   `json:"goal"`
	Metric  string   `json:"metric"`
	Done    string   `json:"done"`
	Mandate string   `json:"mandate"`
	People  []string `json:"people,omitempty"`
	// Budget share of the monthly API ceiling, 0..1.
	BudgetShare float64  `json:"budget_share"`
	Repos       []string `json:"repos,omitempty"`
	// Executor chains by cost class; overrides DefaultChains.
	Chains map[string][]Executor `json:"chains,omitempty"`
	Skills []string              `json:"skills,omitempty"`
	// Resource windows, e.g. a GPU host 02:00–07:00 MSK.
	Windows []Window `json:"windows,omitempty"`
	// Free-form rules the manager must keep (boundaries, stop rules).
	Rules []string `json:"rules,omitempty"`
}

// Executor is one link of an executor chain: a Multica agent plus the
// provider key used for "exhausted until T" bookkeeping.
type Executor struct {
	// Name is the executor_status key, e.g. "claude-sub", "deepseek-api",
	// "qwen-gpu". Several agents may share one provider key.
	Name    string `json:"name"`
	AgentID string `json:"agent_id"`
}

// Window is a recurring daily resource window in Europe/Moscow.
type Window struct {
	Kind  string `json:"kind"`
	Start string `json:"start"` // "02:00"
	End   string `json:"end"`   // "07:00"
}

// ParseCharter decodes a charter, tolerating an empty document.
func ParseCharter(raw []byte) (Charter, error) {
	var c Charter
	if len(raw) == 0 {
		return c, nil
	}
	err := json.Unmarshal(raw, &c)
	return c, err
}

// Chain returns the executor chain for a cost class.
func (c Charter) Chain(costClass string) []Executor {
	if ch, ok := c.Chains[costClass]; ok && len(ch) > 0 {
		return ch
	}
	if ch, ok := c.Chains["*"]; ok {
		return ch
	}
	return nil
}

// Moscow is the only time zone the harness speaks to people in.
var Moscow = func() *time.Location {
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		return time.FixedZone("MSK", 3*60*60)
	}
	return loc
}()
