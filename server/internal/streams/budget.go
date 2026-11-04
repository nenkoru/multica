package streams

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/streams/sdb"
)

// Budgets and resources across streams (§6.7, §13 day 9).
//
// Slots: an executor with a configured capacity (FABRIKA_EXECUTOR_SLOTS)
// runs at most that many steps at once over all streams. Each stream is
// guaranteed ceil(min_share × capacity) of them; the rest go to streams in
// order of running steps per weight, so a heavier stream gets more. The
// focused stream's reserved slot obeys the same rule: focus never eats
// another stream's min_share. Executors without a capacity are unlimited,
// as before.
//
// GPU windows: while a charter's "gpu" window is open, the GPU executors
// (FABRIKA_GPU_EXECUTORS) are taken by that stream's experiments, so every
// chain skips them until the window ends; class S falls through to the
// subscription.

// ParseSlots reads "claude-sub=3,qwen-gpu=2".
func ParseSlots(v string) (map[string]int, error) {
	out := map[string]int{}
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, n, ok := strings.Cut(part, "=")
		c, err := strconv.Atoi(strings.TrimSpace(n))
		if !ok || strings.TrimSpace(name) == "" || err != nil || c < 1 {
			return nil, fmt.Errorf("slot %q: want name=N with N ≥ 1", part)
		}
		out[strings.TrimSpace(name)] = c
	}
	return out, nil
}

// slotAlloc is one tick's view of who runs what on capacity-bound executors.
type slotAlloc struct {
	slots   map[string]int
	running map[string]int              // executor → running steps
	by      map[[16]byte]map[string]int // stream → executor → running
	share   map[[16]byte]float64        // stream → min_share
	queued  map[[16]byte]bool           // stream has queued steps
	waited  map[[16]byte]bool           // stream skipped a full executor this tick
}

func newSlotAlloc(slots map[string]int, streams []sdb.Stream, rows []sdb.RunningStepsByExecutorRow) *slotAlloc {
	a := &slotAlloc{slots: slots, running: map[string]int{}, by: map[[16]byte]map[string]int{}, share: map[[16]byte]float64{}, queued: map[[16]byte]bool{}, waited: map[[16]byte]bool{}}
	for _, st := range streams {
		f, _ := st.MinShare.Float64Value()
		a.share[st.ID.Bytes] = f.Float64
		a.by[st.ID.Bytes] = map[string]int{}
	}
	for _, r := range rows {
		if r.Status == StepQueued {
			a.queued[r.StreamID.Bytes] = true
			continue
		}
		a.running[r.Executor] += int(r.N)
		if m, ok := a.by[r.StreamID.Bytes]; ok {
			m[r.Executor] += int(r.N)
		}
	}
	return a
}

func (a *slotAlloc) guaranteed(stream [16]byte, capacity int) int {
	return int(math.Ceil(a.share[stream] * float64(capacity)))
}

// canTake reports whether stream may start a step on ex now.
func (a *slotAlloc) canTake(stream [16]byte, ex string) bool {
	capacity, bound := a.slots[ex]
	if !bound {
		return true
	}
	if a.running[ex] >= capacity {
		return false
	}
	if a.by[stream][ex] < a.guaranteed(stream, capacity) {
		return true
	}
	// Above its own guarantee a stream uses only what others' unmet
	// guarantees leave free.
	reserved := 0
	for other := range a.by {
		if other == stream || !a.queued[other] {
			continue
		}
		if d := a.guaranteed(other, capacity) - a.by[other][ex]; d > 0 {
			reserved += d
		}
	}
	return capacity-a.running[ex] > reserved
}

func (a *slotAlloc) take(stream [16]byte, ex string) {
	a.running[ex]++
	if m, ok := a.by[stream]; ok {
		m[ex]++
	}
}

// orderByLoad sorts streams so the one with the fewest running steps per
// weight dispatches first: the remainder above min_share follows weight.
func orderByLoad(streams []sdb.Stream, a *slotAlloc) {
	load := func(st sdb.Stream) float64 {
		n := 0
		for _, c := range a.by[st.ID.Bytes] {
			n += c
		}
		w := st.Weight
		if w < 1 {
			w = 1
		}
		return float64(n) / float64(w)
	}
	sort.SliceStable(streams, func(i, j int) bool { return load(streams[i]) < load(streams[j]) })
}

// gpuWindowUntil returns the end of an open "gpu" window in any active
// stream's charter, and whose it is.
func gpuWindowUntil(streams []sdb.Stream, now time.Time) (time.Time, pgtype.UUID) {
	for _, st := range streams {
		c, err := ParseCharter(st.Charter)
		if err != nil {
			continue
		}
		for _, w := range c.Windows {
			if w.Kind != "gpu" {
				continue
			}
			if end, ok := windowOpen(w, now); ok {
				return end, st.ID
			}
		}
	}
	return time.Time{}, pgtype.UUID{}
}

// windowOpen reports whether now (any zone) is inside a daily Moscow window
// "HH:MM"–"HH:MM", which may cross midnight, and when it ends.
func windowOpen(w Window, now time.Time) (time.Time, bool) {
	start, err1 := time.Parse("15:04", w.Start)
	end, err2 := time.Parse("15:04", w.End)
	if err1 != nil || err2 != nil || start.Equal(end) {
		return time.Time{}, false
	}
	t := now.In(Moscow)
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, Moscow)
	at := func(d time.Time, hm time.Time) time.Time {
		return d.Add(time.Duration(hm.Hour())*time.Hour + time.Duration(hm.Minute())*time.Minute)
	}
	for _, d := range []time.Time{day.AddDate(0, 0, -1), day} {
		s, e := at(d, start), at(d, end)
		if !e.After(s) {
			e = e.AddDate(0, 0, 1)
		}
		if !t.Before(s) && t.Before(e) {
			return e, true
		}
	}
	return time.Time{}, false
}

// tickResources prepares the tick's slot view and GPU window. Errors only
// disable the limits for this tick: the supervisor must not stand still.
func (s *Supervisor) tickResources(ctx context.Context, streams []sdb.Stream, now time.Time) {
	s.alloc, s.gpuUntil, s.gpuOwner = nil, time.Time{}, pgtype.UUID{}
	if len(s.GPUExecutors) > 0 {
		s.gpuUntil, s.gpuOwner = gpuWindowUntil(streams, now)
	}
	if len(s.Slots) == 0 {
		return
	}
	rows, err := s.Q.RunningStepsByExecutor(ctx)
	if err != nil {
		return
	}
	s.alloc = newSlotAlloc(s.Slots, streams, rows)
	orderByLoad(streams, s.alloc)
}

// resourceView narrows a chain for one step: executors held by a GPU window
// look exhausted until it ends; executors at capacity are dropped (full
// reports whether any was).
func (s *Supervisor) resourceView(st sdb.Stream, chain []Executor, states map[string]ExecutorState) (out []Executor, view map[string]ExecutorState, full bool) {
	view = states
	if !s.gpuUntil.IsZero() && st.ID != s.gpuOwner {
		view = make(map[string]ExecutorState, len(states)+len(s.GPUExecutors))
		for k, v := range states {
			view[k] = v
		}
		for _, e := range chain {
			if s.GPUExecutors[e.Name] && view[e.Name].ExhaustedUntil.Before(s.gpuUntil) {
				view[e.Name] = ExecutorState{Executor: e.Name, ExhaustedUntil: s.gpuUntil, ProbeLevel: view[e.Name].ProbeLevel}
			}
		}
	}
	if s.alloc == nil {
		return chain, view, false
	}
	out = make([]Executor, 0, len(chain))
	for _, e := range chain {
		if s.alloc.canTake(st.ID.Bytes, e.Name) {
			out = append(out, e)
		} else {
			full = true
		}
	}
	if full {
		s.alloc.waited[st.ID.Bytes] = true
	}
	return out, view, full
}
