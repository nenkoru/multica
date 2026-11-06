package streams

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/streams/sdb"
)

// §8, §13: a check never lands on the author's model, even when every other
// verifier is out of budget; the step waits for one instead.
func TestVerifyWaitsRatherThanSelfReview(t *testing.T) {
	h := newHarness(t, "strictv")
	h.planAndRunWork("M")
	work := h.only(KindWork, StepRunning)
	if work.Executor != "strictv-claude" {
		t.Fatalf("work executor %s", work.Executor)
	}
	// The S chain is qwen, claude: with qwen exhausted only the author is left.
	if err := h.sup.Q.UpsertExecutorStatus(context.Background(), sdb.UpsertExecutorStatusParams{
		Executor: "strictv-qwen", ExhaustedUntil: ts(time.Now().Add(time.Hour)), Reason: "test quota",
	}); err != nil {
		t.Fatal(err)
	}
	h.finish(work, contract(map[string]any{"result": "ветка собрана", "evidence": []string{"ci:run/1"}}))
	h.tick()
	h.tick()
	v := h.only(KindVerify, StepQueued)
	if v.Executor != "" {
		t.Fatalf("verify dispatched to %s", v.Executor)
	}
	if st := h.reload(); st.State == StateBroken {
		t.Fatalf("an exhausted verifier is a budget wait, not a defect: %s", st.Reason)
	}

	h.f.Exec(t, `DELETE FROM executor_status WHERE executor = 'strictv-qwen'`)
	h.tick()
	if v := h.only(KindVerify, StepRunning); v.Executor != "strictv-qwen" {
		t.Fatalf("verify executor %s", v.Executor)
	}
}

func TestVerifyChainOfOnlyTheAuthorBreaksStream(t *testing.T) {
	h := newHarness(t, "selfv")
	h.f.Exec(t, `UPDATE stream SET charter = jsonb_set(charter, '{chains,verify}', jsonb_build_array(jsonb_build_object('name', 'selfv-claude', 'agent_id', $2::text))) WHERE id = $1`,
		uuidString(h.stream.ID), h.agents["claude"])
	h.planAndRunWork("M")
	h.finish(h.only(KindWork, StepRunning), contract(map[string]any{"result": "ветка собрана", "evidence": []string{"ci:run/1"}}))
	h.tick()
	h.tick()
	st := h.reload()
	if st.State != StateBroken || !strings.Contains(st.Reason, "нет проверяющего на другой модели") {
		t.Fatalf("want broken, got %s (%s)", st.State, st.Reason)
	}
	for _, s := range h.open() {
		if s.Kind == KindVerify && s.Executor != "" {
			t.Fatalf("verify ran on %s", s.Executor)
		}
	}
}

// The same agent under another provider key is still the author.
func TestWithoutExecutorDropsSameAgentUnderAnotherKey(t *testing.T) {
	chain := []Executor{{Name: "a", AgentID: "AA"}, {Name: "b", AgentID: "aa"}, {Name: "c", AgentID: "cc"}}
	got := withoutExecutor(chain, Executor{Name: "a", AgentID: "aa"})
	if len(got) != 1 || got[0].Name != "c" {
		t.Fatalf("got %+v", got)
	}
	if got := withoutExecutor(chain, Executor{}); len(got) != 3 {
		t.Fatalf("unknown author filtered %+v", got)
	}
}

// §8 p.3: a milestone needs a verify accepted after the previous milestone;
// the accepted check joins its evidence.
func TestMilestoneNeedsAcceptedVerify(t *testing.T) {
	h := newHarness(t, "msgate")
	milestone := map[string]any{"text": "ветка собрана", "evidence": []string{"ci:run/1"}}
	countMs := func() int {
		return h.f.Count(t, `SELECT count(*) FROM milestone WHERE stream_id = $1`, uuidString(h.stream.ID))
	}

	// A first manager turn claims a milestone with nothing verified.
	h.tick()
	h.finish(h.only(KindManage, StepRunning), contract(map[string]any{
		"result": "план", "milestone": milestone,
		"next": []map[string]any{{"brief": "Собрать ветку", "kind": "work", "cost_class": "M", "moves": "зелёные выкатки"}},
	}))
	h.tick()
	if countMs() != 0 || h.journalCount("milestone_rejected") != 1 {
		t.Fatalf("milestones %d, rejected %d", countMs(), h.journalCount("milestone_rejected"))
	}
	// The rest of the turn still applied.
	work := h.only(KindWork, StepRunning)

	h.finish(work, contract(map[string]any{"result": "ветка собрана", "evidence": []string{"ci:run/1"}}))
	h.tick()
	v := h.only(KindVerify, StepRunning)
	h.finish(v, contract(map[string]any{"result": "ок", "verdict": "accept"}))
	h.tick()
	mg := h.only(KindManage, StepRunning)
	h.finish(mg, contract(map[string]any{
		"result": "веха", "milestone": milestone,
		"wait": map[string]any{"kind": "time", "check": "пауза", "deadline": time.Now().Add(time.Hour).Format(time.RFC3339)},
	}))
	h.tick()
	if countMs() != 1 {
		t.Fatalf("milestones %d after an accepted verify", countMs())
	}
	var ev string
	h.f.QueryRow(t, `SELECT evidence::text FROM milestone WHERE stream_id = $1`, uuidString(h.stream.ID)).Scan(&ev)
	if !strings.Contains(ev, "ci:run/1") || !strings.Contains(ev, "проверка #") || !strings.Contains(ev, v.Executor) {
		t.Fatalf("evidence %s", ev)
	}

	// The same acceptance does not back a second milestone.
	ok, err := gateMilestone(context.Background(), h.sup.Q, h.reload(), mg, &MilestoneSpec{Text: "ещё веха", Evidence: []string{"x"}})
	if err != nil || ok || h.journalCount("milestone_rejected") != 2 {
		t.Fatalf("second milestone on one acceptance: ok=%v err=%v rejected=%d", ok, err, h.journalCount("milestone_rejected"))
	}
}
