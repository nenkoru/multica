package streams

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// StepOutput is the JSON block every step must end with (§6.2).
type StepOutput struct {
	Result       string       `json:"result"`
	Evidence     []string     `json:"evidence,omitempty"`
	DoneCheck    string       `json:"done_check,omitempty"`
	SummaryPatch string       `json:"summary_patch,omitempty"`
	Next         []NextStep   `json:"next,omitempty"`
	Wait         *WaitSpec    `json:"wait,omitempty"`
	HumanAction  *HumanAction `json:"human_action,omitempty"`
	// Manage turns only: "no ideas" parks the stream for 24 h (§6.5).
	NoIdeas bool `json:"no_ideas,omitempty"`
	// Verify turns only: accept or send back for rework (§8).
	Verdict string `json:"verdict,omitempty"`
	// Manage turns only: milestone accepted from the previous step (§8).
	Milestone *MilestoneSpec `json:"milestone,omitempty"`
	// Manage turns only: full replacement summary (≤1500 tokens). Only the
	// manager writes the summary; work steps propose summary_patch.
	Summary string `json:"summary,omitempty"`
	// Steps must not leave a background process without a wait.
	Background bool `json:"background,omitempty"`
}

type NextStep struct {
	Brief     string `json:"brief"`
	Kind      string `json:"kind"`
	CostClass string `json:"cost_class"`
	Moves     string `json:"moves"`
	Executor  string `json:"executor,omitempty"`
}

type WaitSpec struct {
	Kind       string `json:"kind"`
	Check      string `json:"check"`
	Deadline   string `json:"deadline"`
	OnDeadline string `json:"on_deadline"`
}

type HumanAction struct {
	Text     string   `json:"text"`
	Options  []string `json:"options,omitempty"`
	Items    []string `json:"items,omitempty"`
	Default  *int     `json:"default,omitempty"`
	Zone     string   `json:"zone"`
	Deadline string   `json:"deadline"`
	Value    float64  `json:"value,omitempty"`
	Cost     float64  `json:"cost_of_delay,omitempty"`
}

type MilestoneSpec struct {
	Text        string   `json:"text"`
	Evidence    []string `json:"evidence"`
	MetricDelta string   `json:"metric_delta,omitempty"`
}

// ErrNoContract is returned when the output carries no contract block.
var ErrNoContract = errors.New("step did not return the output contract")

var fencedJSON = regexp.MustCompile("(?s)```(?i:json)?\\s*(\\{.*?\\})\\s*```")

// ExtractContract finds the last JSON object in the agent's final output
// that decodes into a StepOutput with a non-empty result. Fenced blocks are
// preferred; a bare trailing object is accepted as a fallback.
func ExtractContract(output string) (StepOutput, error) {
	matches := fencedJSON.FindAllStringSubmatch(output, -1)
	for i := len(matches) - 1; i >= 0; i-- {
		if out, ok := decodeContract(matches[i][1]); ok {
			return out, nil
		}
	}
	// Fallback: the last '{' that starts a decodable object running to the end.
	trimmed := strings.TrimSpace(output)
	if strings.HasSuffix(trimmed, "}") {
		for i := strings.LastIndex(trimmed, "{"); i >= 0; i = strings.LastIndex(trimmed[:i], "{") {
			if out, ok := decodeContract(trimmed[i:]); ok {
				return out, nil
			}
			if i == 0 {
				break
			}
		}
	}
	return StepOutput{}, ErrNoContract
}

func decodeContract(s string) (StepOutput, bool) {
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(strings.NewReader(s)).Decode(&raw); err != nil {
		return StepOutput{}, false
	}
	norm, _ := json.Marshal(normalizeContract(raw))
	var out StepOutput
	if err := json.Unmarshal(norm, &out); err != nil {
		return StepOutput{}, false
	}
	if strings.TrimSpace(out.Result) == "" && !out.NoIdeas {
		return StepOutput{}, false
	}
	return out, true
}

// normalizeContract accepts the report shape the 27B worker writes out of
// habit ({"done", "links", "next": ["..."]}, RUSL-2621) as the contract:
// done → result, links → evidence, plain-string next → briefs. A good
// result must not be thrown away over field names.
func normalizeContract(raw map[string]json.RawMessage) map[string]json.RawMessage {
	var str string
	if _, ok := raw["result"]; !ok {
		if v, ok := raw["done"]; ok && json.Unmarshal(v, &str) == nil {
			raw["result"] = v
		}
	}
	var links []string
	if v, ok := raw["links"]; ok && json.Unmarshal(v, &links) == nil && len(links) > 0 {
		var ev []string
		if e, ok := raw["evidence"]; ok {
			_ = json.Unmarshal(e, &ev)
		}
		raw["evidence"], _ = json.Marshal(append(ev, links...))
	}
	var items []json.RawMessage
	if v, ok := raw["next"]; ok && json.Unmarshal(v, &items) == nil {
		for i, it := range items {
			if json.Unmarshal(it, &str) == nil {
				items[i], _ = json.Marshal(NextStep{Brief: str})
			}
		}
		raw["next"], _ = json.Marshal(items)
	}
	return raw
}

var verdictWord = regexp.MustCompile(`(?i)(?:verdict|вердикт)["'*\s:=—–-]{0,8}(accept|rework)\b`)
var verdictPrefix = regexp.MustCompile(`(?i)^[\s*"'«(\[]{0,3}(accept|rework)\b`)

// InferVerdict fills a missing verdict of a verify turn from the result
// prefix ("rework: …") or a "verdict: rework" line in the text around the
// block (RUSL-2625, RUSL-2626). It only sets the verdict when the text
// names exactly one; otherwise validation rejects the turn as before.
func InferVerdict(out *StepOutput, text string) {
	if v := strings.ToLower(strings.TrimSpace(out.Verdict)); v == "accept" || v == "rework" {
		out.Verdict = v
		return
	}
	if out.Verdict != "" {
		return
	}
	if m := verdictPrefix.FindStringSubmatch(strings.TrimSpace(out.Result)); m != nil {
		out.Verdict = strings.ToLower(m[1])
		return
	}
	found := map[string]bool{}
	for _, t := range []string{out.Result, out.DoneCheck, text} {
		for _, m := range verdictWord.FindAllStringSubmatch(t, -1) {
			found[strings.ToLower(m[1])] = true
		}
	}
	if len(found) == 1 {
		for v := range found {
			out.Verdict = v
		}
	}
}

var validWaitKinds = map[string]bool{"ci": true, "git-tag": true, "http": true, "process": true, "time": true, "gpu-window": true, "issue": true}
var validOnDeadline = map[string]bool{"replan": true, "retry": true, "ask": true}
var validKinds = map[string]bool{KindManage: true, KindWork: true, KindVerify: true, KindSummarize: true}
var validCost = map[string]bool{"S": true, "M": true, "L": true}

// ValidateContract checks the shape rules of §6.2 and §6.5 for a step of the
// given kind. The returned error text is fed back to the retry verbatim.
func ValidateContract(kind string, out StepOutput, now time.Time) error {
	var problems []string
	if strings.TrimSpace(out.Result) == "" && !out.NoIdeas {
		problems = append(problems, "result is empty")
	}
	if out.Background && out.Wait == nil {
		problems = append(problems, "background process left running without wait")
	}
	if out.Wait != nil {
		if _, err := ParseWait(*out.Wait, now); err != nil {
			problems = append(problems, err.Error())
		}
	}
	for i, n := range out.Next {
		if strings.TrimSpace(n.Brief) == "" {
			problems = append(problems, fmt.Sprintf("next[%d].brief is empty", i))
		}
		if n.Kind != "" && !validKinds[n.Kind] {
			problems = append(problems, fmt.Sprintf("next[%d].kind %q is unknown", i, n.Kind))
		}
		if n.CostClass != "" && !validCost[n.CostClass] {
			problems = append(problems, fmt.Sprintf("next[%d].cost_class %q is not S/M/L", i, n.CostClass))
		}
		// Against work for work's sake: a step that moves no metric is not queued (§6.5).
		if kind == KindManage && strings.TrimSpace(n.Moves) == "" {
			problems = append(problems, fmt.Sprintf("next[%d].moves must name the metric it moves", i))
		}
	}
	if kind == KindManage {
		decisions := 0
		if len(out.Next) > 0 {
			decisions++
		}
		if out.Wait != nil {
			decisions++
		}
		if out.HumanAction != nil {
			decisions++
		}
		if out.NoIdeas {
			decisions++
		}
		if decisions == 0 {
			problems = append(problems, "manage turn must return next, wait, human_action or no_ideas")
		}
		if len(out.Summary) > 1500*4 {
			problems = append(problems, "summary is longer than ~1500 tokens")
		}
	}
	if kind == KindVerify && out.Verdict != "accept" && out.Verdict != "rework" {
		problems = append(problems, `verify turn must return verdict "accept" or "rework"`)
	}
	if out.Milestone != nil && len(out.Milestone.Evidence) == 0 {
		problems = append(problems, "milestone needs evidence")
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// Wait is a validated wait.
type Wait struct {
	Kind       string
	Check      string
	Deadline   time.Time
	OnDeadline string
}

// ParseWait validates a wait spec. A wait must end: the deadline is required
// and must lie in the future, at most 14 days ahead.
func ParseWait(w WaitSpec, now time.Time) (Wait, error) {
	if !validWaitKinds[w.Kind] {
		return Wait{}, fmt.Errorf("wait.kind %q is unknown", w.Kind)
	}
	on := w.OnDeadline
	if on == "" {
		on = "replan"
	}
	if !validOnDeadline[on] {
		return Wait{}, fmt.Errorf("wait.on_deadline %q is unknown", w.OnDeadline)
	}
	d, err := parseTime(w.Deadline)
	if err != nil {
		return Wait{}, fmt.Errorf("wait.deadline: %v", err)
	}
	if !d.After(now) {
		return Wait{}, errors.New("wait.deadline is in the past")
	}
	if d.After(now.Add(14 * 24 * time.Hour)) {
		return Wait{}, errors.New("wait.deadline is more than 14 days away")
	}
	if w.Kind != "time" && strings.TrimSpace(w.Check) == "" {
		return Wait{}, errors.New("wait.check is empty")
	}
	return Wait{Kind: w.Kind, Check: w.Check, Deadline: d, OnDeadline: on}, nil
}

func parseTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04Z07:00", "2006-01-02T15:04Z", "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	// Bare date-time without zone is Moscow time.
	if t, err := time.ParseInLocation("2006-01-02 15:04", s, Moscow); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("cannot parse time %q", s)
}
