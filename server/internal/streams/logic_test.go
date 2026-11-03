package streams

import (
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

func TestExtractContractPrefersLastFencedBlock(t *testing.T) {
	out := "Сделал.\n```json\n{\"result\": \"черновик\"}\n```\nи ещё\n```json\n{\"result\": \"готово\", \"evidence\": [\"pr:x#1\"]}\n```\n"
	c, err := ExtractContract(out)
	if err != nil {
		t.Fatal(err)
	}
	if c.Result != "готово" || len(c.Evidence) != 1 {
		t.Fatalf("got %+v", c)
	}
}

func TestExtractContractBareTrailingObject(t *testing.T) {
	c, err := ExtractContract("итог ниже\n{\"result\": \"ok\", \"next\": [{\"brief\": \"b\", \"moves\": \"m\"}]}")
	if err != nil || c.Result != "ok" || len(c.Next) != 1 {
		t.Fatalf("got %+v %v", c, err)
	}
}

func TestExtractContractMissing(t *testing.T) {
	for _, out := range []string{"", "просто текст", "```json\n{\"evidence\": []}\n```", "```json\n{broken\n```"} {
		if _, err := ExtractContract(out); err != ErrNoContract {
			t.Errorf("%q: want ErrNoContract, got %v", out, err)
		}
	}
}

func TestValidateContractManage(t *testing.T) {
	cases := []struct {
		name string
		out  StepOutput
		want string
	}{
		{"no decision", StepOutput{Result: "думал"}, "must return next"},
		{"next without metric", StepOutput{Result: "r", Next: []NextStep{{Brief: "b"}}}, "moves must name the metric"},
		{"bad cost", StepOutput{Result: "r", Next: []NextStep{{Brief: "b", Moves: "m", CostClass: "XL"}}}, "cost_class"},
		{"background without wait", StepOutput{Result: "r", NoIdeas: true, Background: true}, "background"},
		{"wait in past", StepOutput{Result: "r", Wait: &WaitSpec{Kind: "ci", Check: "c", Deadline: "2026-11-01T00:00:00Z"}}, "past"},
		{"wait too far", StepOutput{Result: "r", Wait: &WaitSpec{Kind: "time", Deadline: "2026-12-30T00:00:00Z"}}, "14 days"},
		{"milestone without evidence", StepOutput{Result: "r", NoIdeas: true, Milestone: &MilestoneSpec{Text: "x"}}, "evidence"},
		{"ok next", StepOutput{Result: "r", Next: []NextStep{{Brief: "b", Moves: "m", Kind: "work", CostClass: "M"}}}, ""},
		{"ok wait", StepOutput{Result: "r", Wait: &WaitSpec{Kind: "ci", Check: "gh run view 1", Deadline: "2026-11-03T16:00Z"}}, ""},
		{"ok no ideas", StepOutput{Result: "r", NoIdeas: true}, ""},
	}
	for _, c := range cases {
		err := ValidateContract(KindManage, c.out, t0)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: unexpected %v", c.name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: want %q, got %v", c.name, c.want, err)
		}
	}
}

func TestValidateContractVerifyNeedsVerdict(t *testing.T) {
	if err := ValidateContract(KindVerify, StepOutput{Result: "ok"}, t0); err == nil {
		t.Fatal("verify without verdict accepted")
	}
	if err := ValidateContract(KindVerify, StepOutput{Result: "ok", Verdict: "accept"}, t0); err != nil {
		t.Fatal(err)
	}
}

func TestParseWaitMoscowLocal(t *testing.T) {
	w, err := ParseWait(WaitSpec{Kind: "time", Deadline: "2026-11-03 18:00"}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if !w.Deadline.Equal(time.Date(2026, 11, 3, 15, 0, 0, 0, time.UTC)) || w.OnDeadline != "replan" {
		t.Fatalf("got %+v", w)
	}
}

func intp(i int) *int { return &i }

func TestValidateAskForm(t *testing.T) {
	deadline := "2026-11-04T18:00:00+03:00"
	cases := []struct {
		name string
		h    HumanAction
		want string
	}{
		{"ok choice", HumanAction{Text: "Продлевать домен vergel.ru за 1200 ₽?", Options: []string{"да", "нет"}, Default: intp(1), Zone: "normal", Deadline: deadline}, ""},
		{"ok red", HumanAction{Text: "Удалить старую БД без бэкапа?", Options: []string{"да", "нет"}, Zone: "red", Deadline: deadline}, ""},
		{"ok batch", HumanAction{Text: "Отклики на вакансии", Items: []string{"A", "B"}, Zone: "normal", Deadline: deadline}, ""},
		{"no options", HumanAction{Text: "Что делать?", Zone: "normal", Deadline: deadline}, "2–4 options"},
		{"too many", HumanAction{Text: "x", Options: []string{"1", "2", "3", "4", "5"}, Default: intp(0), Deadline: deadline}, "2–4 options"},
		{"no deadline", HumanAction{Text: "x", Options: []string{"1", "2"}, Default: intp(0)}, "no deadline"},
		{"too long", HumanAction{Text: strings.Repeat("я", 201), Options: []string{"1", "2"}, Default: intp(0), Deadline: deadline}, "200"},
		{"red default", HumanAction{Text: "x", Options: []string{"1", "2"}, Default: intp(0), Zone: "red", Deadline: deadline}, "must not have a default"},
		{"normal without default", HumanAction{Text: "x", Options: []string{"1", "2"}, Zone: "normal", Deadline: deadline}, "needs a default"},
		{"self decide architecture", HumanAction{Text: "Какую библиотеку взять для парсера?", Options: []string{"a", "b"}, Default: intp(0), Deadline: deadline}, "decide it yourself"},
		{"self decide 402", HumanAction{Text: "Получили 402, что делать?", Options: []string{"a", "b"}, Default: intp(0), Deadline: deadline}, "decide it yourself"},
		{"self decide merge", HumanAction{Text: "Можно ли смерджить PR #12?", Options: []string{"да", "нет"}, Default: intp(0), Deadline: deadline}, "decide it yourself"},
	}
	for _, c := range cases {
		_, err := ValidateAsk(c.h, t0)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: unexpected %v", c.name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: want %q, got %v", c.name, c.want, err)
		}
	}
}

func TestParseReply(t *testing.T) {
	replies, rest := ParseReply("1 да, 2 б; 3 покажи, 4) 2, привет")
	if len(replies) != 4 {
		t.Fatalf("got %+v rest=%q", replies, rest)
	}
	if replies[0].N != 1 || replies[0].Yes == nil || !*replies[0].Yes {
		t.Errorf("1: %+v", replies[0])
	}
	if replies[1].N != 2 || replies[1].Choice != 1 {
		t.Errorf("2: %+v", replies[1])
	}
	if replies[2].N != 3 || !replies[2].Show {
		t.Errorf("3: %+v", replies[2])
	}
	if replies[3].N != 4 || replies[3].Choice != 1 {
		t.Errorf("4: %+v", replies[3])
	}
	if rest != "привет" {
		t.Errorf("rest = %q", rest)
	}
	if _, rest := ParseReply("5 может быть"); rest != "5 может быть" {
		t.Errorf("unparsed answer must go to Мультика, rest = %q", rest)
	}
}

func TestTopAsks(t *testing.T) {
	got := TopAsks([]RankedAsk{
		{ID: "low", Value: 1, Cost: 1, Deadline: t0},
		{ID: "high", Value: 5, Cost: 2, Deadline: t0.Add(time.Hour)},
		{ID: "mid-late", Value: 2, Cost: 1, Deadline: t0.Add(2 * time.Hour)},
		{ID: "mid-early", Value: 1, Cost: 2, Deadline: t0.Add(time.Hour)},
	}, 3)
	ids := []string{got[0].ID, got[1].ID, got[2].ID}
	if strings.Join(ids, ",") != "high,mid-early,mid-late" {
		t.Fatalf("got %v", ids)
	}
}

func TestPickExecutorSkipsExhaustedAndAvoided(t *testing.T) {
	chain := []Executor{{Name: "qwen"}, {Name: "claude"}, {Name: "api"}}
	states := map[string]ExecutorState{"qwen": {ExhaustedUntil: t0.Add(time.Hour)}}
	ex, _, ok := PickExecutor(chain, states, nil, t0)
	if !ok || ex.Name != "claude" {
		t.Fatalf("got %v %v", ex, ok)
	}
	ex, _, ok = PickExecutor(chain, states, map[string]bool{"claude": true}, t0)
	if !ok || ex.Name != "api" {
		t.Fatalf("avoid: got %v %v", ex, ok)
	}
	// Avoidance never causes a stop when it is the only live link.
	ex, _, ok = PickExecutor(chain[:2], states, map[string]bool{"claude": true}, t0)
	if !ok || ex.Name != "claude" {
		t.Fatalf("avoid fallback: got %v %v", ex, ok)
	}
	all := map[string]ExecutorState{
		"qwen": {ExhaustedUntil: t0.Add(2 * time.Hour)}, "claude": {ExhaustedUntil: t0.Add(time.Hour)}, "api": {ExhaustedUntil: t0.Add(3 * time.Hour)},
	}
	_, until, ok := PickExecutor(chain, all, nil, t0)
	if ok || !until.Equal(t0.Add(time.Hour)) {
		t.Fatalf("exhausted chain: ok=%v until=%v", ok, until)
	}
}

func TestExhaustUntilBackoff(t *testing.T) {
	u, l := ExhaustUntil(t0, time.Time{}, 0)
	if !u.Equal(t0.Add(15*time.Minute)) || l != 1 {
		t.Fatal(u, l)
	}
	u, l = ExhaustUntil(t0, time.Time{}, 1)
	if !u.Equal(t0.Add(30*time.Minute)) || l != 2 {
		t.Fatal(u, l)
	}
	u, _ = ExhaustUntil(t0, time.Time{}, 7)
	if !u.Equal(t0.Add(time.Hour)) {
		t.Fatal(u)
	}
	reset := t0.Add(5 * time.Hour)
	if u, l := ExhaustUntil(t0, reset, 2); !u.Equal(reset) || l != 0 {
		t.Fatal(u, l)
	}
}

func TestQuotaDetection(t *testing.T) {
	yes := []string{
		"API error: status 402 Payment Required",
		"Claude AI usage limit reached|1762160400",
		"Your credit balance is too low to access the Anthropic API",
		"insufficient credits",
		"error: 402",
	}
	for _, s := range yes {
		if !IsQuotaFailure("agent_error.unknown", s) {
			t.Errorf("not detected: %q", s)
		}
	}
	no := []string{"build failed at main.go line 402", "TypeError: x is undefined", ""}
	for _, s := range no {
		if IsQuotaFailure("agent_error.process_failure", s) {
			t.Errorf("false positive: %q", s)
		}
	}
	if !IsQuotaFailure("agent_error.provider_quota_limit", "") {
		t.Error("reason code ignored")
	}
}

func TestParseQuotaReset(t *testing.T) {
	if got := ParseQuotaReset("Claude AI usage limit reached|1762160400", t0); !got.Equal(time.Unix(1762160400, 0)) {
		t.Errorf("unix: %v", got)
	}
	if got := ParseQuotaReset("quota exceeded, resets at 2026-11-03T15:00:00+0300", t0); !got.Equal(time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("rfc3339: %v", got)
	}
	if got := ParseQuotaReset("429 retry-after: 120", t0); !got.Equal(t0.Add(2 * time.Minute)) {
		t.Errorf("retry-after: %v", got)
	}
	if got := ParseQuotaReset("402", t0); !got.IsZero() {
		t.Errorf("no reset: %v", got)
	}
}
