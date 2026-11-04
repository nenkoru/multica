package streams

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/streams/sdb"
)

func TestParseSlots(t *testing.T) {
	got, err := ParseSlots(" claude-sub=3, qwen-gpu=2 ,")
	if err != nil || got["claude-sub"] != 3 || got["qwen-gpu"] != 2 || len(got) != 2 {
		t.Fatalf("%v %v", got, err)
	}
	for _, bad := range []string{"x", "x=0", "=2", "x=a"} {
		if _, err := ParseSlots(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestWindowOpenAcrossMidnight(t *testing.T) {
	w := Window{Kind: "gpu", Start: "23:00", End: "07:00"}
	at := func(h, m int) time.Time { return time.Date(2026, 11, 5, h, m, 0, 0, Moscow) }
	if end, ok := windowOpen(w, at(2, 0)); !ok || !end.Equal(at(7, 0)) {
		t.Fatalf("02:00: %v %v", end, ok)
	}
	if end, ok := windowOpen(w, at(23, 30)); !ok || !end.Equal(at(7, 0).AddDate(0, 0, 1)) {
		t.Fatalf("23:30: %v %v", end, ok)
	}
	if _, ok := windowOpen(w, at(12, 0)); ok {
		t.Fatal("12:00 inside 23–07")
	}
	if _, ok := windowOpen(Window{Start: "02:00", End: "07:00"}, at(7, 0)); ok {
		t.Fatal("end is exclusive")
	}
}

func sid(b byte) pgtype.UUID { return pgtype.UUID{Bytes: [16]byte{b}, Valid: true} }

func streamWithShare(id pgtype.UUID, share string, weight int32) sdb.Stream {
	var n pgtype.Numeric
	_ = n.Scan(share)
	return sdb.Stream{ID: id, MinShare: n, Weight: weight}
}

// §6.7: min_share is guaranteed; above it a stream uses only what other
// streams' unmet guarantees leave free.
func TestSlotAllocGuaranteesMinShare(t *testing.T) {
	a, b := sid(1), sid(2)
	streams := []sdb.Stream{streamWithShare(a, "0", 1), streamWithShare(b, "0.5", 1)}
	rows := []sdb.RunningStepsByExecutorRow{
		{StreamID: a, Status: StepRunning, Executor: "sub", N: 2},
		{StreamID: b, Status: StepQueued, Executor: "", N: 1},
	}
	al := newSlotAlloc(map[string]int{"sub": 4}, streams, rows)
	// 4 slots, A runs 2, B is owed ceil(0.5×4)=2 and waits: A must not take more.
	if al.canTake(a.Bytes, "sub") {
		t.Fatal("A took B's guaranteed slot")
	}
	if !al.canTake(b.Bytes, "sub") {
		t.Fatal("B denied its guarantee")
	}
	al.take(b.Bytes, "sub")
	al.take(b.Bytes, "sub")
	if al.canTake(a.Bytes, "sub") || al.canTake(b.Bytes, "sub") {
		t.Fatal("over capacity")
	}
	if !al.canTake(a.Bytes, "other") {
		t.Fatal("unbounded executor limited")
	}
}

func TestOrderByLoadFollowsWeight(t *testing.T) {
	a, b := sid(1), sid(2)
	streams := []sdb.Stream{streamWithShare(a, "0", 1), streamWithShare(b, "0", 3)}
	rows := []sdb.RunningStepsByExecutorRow{
		{StreamID: a, Status: StepRunning, Executor: "sub", N: 1},
		{StreamID: b, Status: StepRunning, Executor: "sub", N: 2},
	}
	al := newSlotAlloc(map[string]int{"sub": 5}, streams, rows)
	orderByLoad(streams, al)
	if streams[0].ID != b {
		t.Fatal("heavier stream with less load per weight goes first")
	}
}

func TestGPUWindowTakesGPUExecutorsFromOthers(t *testing.T) {
	now := time.Date(2026, 11, 5, 3, 0, 0, 0, Moscow)
	raw, _ := json.Marshal(Charter{Windows: []Window{{Kind: "gpu", Start: "02:00", End: "07:00"}}})
	owner := sdb.Stream{ID: sid(1), Charter: raw}
	other := sdb.Stream{ID: sid(2)}
	until, who := gpuWindowUntil([]sdb.Stream{other, owner}, now)
	if who != owner.ID || until.In(Moscow).Hour() != 7 {
		t.Fatalf("window %v %v", until, who)
	}
	s := &Supervisor{GPUExecutors: map[string]bool{"qwen-gpu": true}, gpuUntil: until, gpuOwner: who}
	chain := []Executor{{Name: "qwen-gpu"}, {Name: "sub"}}
	_, view, _ := s.resourceView(other, chain, map[string]ExecutorState{})
	ex, _, ok := PickExecutor(chain, view, nil, now)
	if !ok || ex.Name != "sub" {
		t.Fatalf("other stream got %v", ex)
	}
	_, view, _ = s.resourceView(owner, chain, map[string]ExecutorState{})
	if ex, _, _ := PickExecutor(chain, view, nil, now); ex.Name != "qwen-gpu" {
		t.Fatalf("window owner lost the GPU: %v", ex)
	}
	// Only the GPU in the chain: an honest "budget until the window ends".
	_, view, _ = s.resourceView(other, chain[:1], map[string]ExecutorState{})
	if _, u, ok := PickExecutor(chain[:1], view, nil, now); ok || !u.Equal(until) {
		t.Fatalf("want wait until %v, got %v %v", until, u, ok)
	}
}

// Two streams share one capped executor: the second waits for the slot
// with a slot reason, not a budget state, and runs once it frees.
func TestSlotCapAcrossStreams(t *testing.T) {
	a := newHarness(t, "slota")
	b := newHarness(t, "slotb")
	only := `{"M": [{"name": "slota-claude", "agent_id": "` + a.agents["claude"] + `"}]}`
	for _, h := range []*harness{a, b} {
		h.f.Exec(t, `UPDATE stream SET charter = jsonb_set(charter, '{chains,M}', ($2::jsonb)->'M') WHERE id = $1`, uuidString(h.stream.ID), only)
		h.sup.Slots = map[string]int{"slota-claude": 1}
	}
	a.planAndRunWork("M")
	a.only(KindWork, StepRunning)
	b.planAndRunWork("M")
	b.only(KindWork, StepQueued)
	if st := b.reload(); st.State != StateRunning || !strings.Contains(st.Reason, "слот") {
		t.Fatalf("want slot wait, got %s (%s)", st.State, st.Reason)
	}
	a.finish(a.only(KindWork, StepRunning), contract(map[string]any{"result": "готово", "evidence": []string{"x"}}))
	// The slot view is taken at the start of a tick: the freed slot is seen
	// on the next one.
	b.tick()
	b.tick()
	b.only(KindWork, StepRunning)
}
