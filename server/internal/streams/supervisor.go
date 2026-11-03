package streams

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/streams/sdb"
)

// supervisorLockKey serialises scheduler ticks and bus-triggered kicks across
// instances (pg advisory lock). Arbitrary constant, "FABR".
const supervisorLockKey int64 = 0x46414252

// Supervisor keeps the invariant of §5: every active stream holds exactly one
// live reason. It decides nothing of substance; it reconciles state.
type Supervisor struct {
	Pool    *pgxpool.Pool
	Q       *sdb.Queries
	Carrier Carrier
	Now     func() time.Time
	// AskDailyCap overrides DefaultAskDailyCap (mandate: 1).
	AskDailyCap int

	kick chan struct{}
}

func NewSupervisor(pool *pgxpool.Pool, carrier Carrier) *Supervisor {
	return &Supervisor{Pool: pool, Q: sdb.New(pool), Carrier: carrier, Now: time.Now, AskDailyCap: DefaultAskDailyCap, kick: make(chan struct{}, 1)}
}

// Kick asks for an early reconcile (task:completed / task:failed on the bus).
// The event only speeds the reconcile up; the 30 s tick does not depend on it.
func (s *Supervisor) Kick() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// RunKicks serves Kick until ctx ends.
func (s *Supervisor) RunKicks(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.kick:
			// Let the completing transaction land before reading it.
			time.Sleep(2 * time.Second)
			if err := s.Tick(ctx); err != nil {
				slog.Warn("streams: kick reconcile failed", "error", err)
			}
		}
	}
}

// Tick reconciles every active stream once.
func (s *Supervisor) Tick(ctx context.Context) error {
	conn, err := s.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", supervisorLockKey).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return nil
	}
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", supervisorLockKey)
	}()

	streams, err := s.Q.ListActiveStreams(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, st := range streams {
		if err := s.reconcile(ctx, st); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", st.Slug, err))
			s.journal(ctx, st.ID, pgtype.UUID{}, "reconcile_error", map[string]any{"error": err.Error()})
		}
	}
	joined := errors.Join(errs...)
	lastErr := ""
	if joined != nil {
		lastErr = firstLine(joined.Error(), 500)
	}
	if err := s.Q.RecordSupervisorTick(ctx, lastErr); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (s *Supervisor) now() time.Time { return s.Now() }

func (s *Supervisor) reconcile(ctx context.Context, st sdb.Stream) error {
	now := s.now()
	charter, err := ParseCharter(st.Charter)
	if err != nil {
		return s.setState(ctx, st, StateBroken, "устав не читается: "+err.Error(), time.Time{})
	}

	if err := s.settleAsks(ctx, st, now); err != nil {
		return err
	}
	if st.State == StatePaused {
		return nil
	}
	if st.State == StateBroken {
		open, err := s.Q.ListOpenAsks(ctx, st.ID)
		if err != nil {
			return err
		}
		if len(open) > 0 {
			return nil
		}
		// Ruslan answered the crash question: back to running (§5.1).
		if err := s.Q.SetStreamFailStreak(ctx, sdb.SetStreamFailStreakParams{ID: st.ID, FailStreak: 0}); err != nil {
			return err
		}
		if err := s.setState(ctx, st, StateRunning, "ответ на аварию получен", time.Time{}); err != nil {
			return err
		}
		st.State, st.Reason, st.FailStreak = StateRunning, "ответ на аварию получен", 0
	}

	expired, err := s.settleWaits(ctx, st, now)
	if err != nil {
		return err
	}

	execStates, err := s.executorStates(ctx)
	if err != nil {
		return err
	}

	steps, err := s.Q.ListOpenSteps(ctx, st.ID)
	if err != nil {
		return err
	}
	for _, step := range steps {
		if step.Status == StepRunning {
			if err := s.observe(ctx, st, charter, step, execStates, now); err != nil {
				return fmt.Errorf("observe step %d: %w", step.Seq, err)
			}
		}
	}
	// Re-read: observation may have finished, requeued or created steps.
	if st, err = s.Q.GetStream(ctx, st.ID); err != nil {
		return err
	}
	if st.State == StateBroken || st.State == StatePaused {
		return nil
	}
	steps, err = s.Q.ListOpenSteps(ctx, st.ID)
	if err != nil {
		return err
	}

	budgetUntil, err := s.dispatchQueued(ctx, st, charter, steps, execStates, now)
	if err != nil {
		return err
	}
	if steps, err = s.Q.ListOpenSteps(ctx, st.ID); err != nil {
		return err
	}

	// The invariant (§5): exactly one live reason.
	running := 0
	queued := 0
	for _, step := range steps {
		switch step.Status {
		case StepRunning, StepVerifying:
			running++
		case StepQueued:
			queued++
		}
	}
	switch {
	case running > 0:
		return s.setState(ctx, st, StateRunning, fmt.Sprintf("идёт шагов: %d", running), time.Time{})
	case queued > 0 && !budgetUntil.IsZero():
		return s.setState(ctx, st, StateBudget, "цепочка исполнителей исчерпана", budgetUntil)
	case queued > 0:
		// Dispatch failed for a non-budget reason this tick; the error is
		// journaled and the next tick retries.
		return s.setState(ctx, st, StateRunning, "шаг в очереди", time.Time{})
	}

	waits, err := s.Q.ListOpenWaits(ctx, st.ID)
	if err != nil {
		return err
	}
	if len(waits) > 0 {
		w := waits[0]
		return s.setState(ctx, st, StateWaiting, fmt.Sprintf("%s: %s", w.Kind, firstLine(w.Check, 120)), w.Deadline.Time)
	}
	asks, err := s.Q.ListOpenAsks(ctx, st.ID)
	if err != nil {
		return err
	}
	if len(asks) > 0 && len(expired) == 0 && !s.hasFreshAnswers(ctx, st) {
		return s.setState(ctx, st, StateAsking, firstLine(asks[0].Text, 120), asks[0].Deadline.Time)
	}

	// Orphan: put the manager's turn, idempotently by (stream_id, seq).
	trigger := s.orphanTrigger(ctx, st, expired)
	gap := 0.0
	if end, err := s.Q.LastReasonEnd(ctx, st.ID); err == nil && end.Valid {
		gap = now.Sub(end.Time).Seconds()
	}
	s.journal(ctx, st.ID, pgtype.UUID{}, "orphan", map[string]any{"trigger": trigger, "gap_seconds": gap})
	if _, err := s.enqueueManage(ctx, st, trigger, false); err != nil {
		return err
	}
	if steps, err = s.Q.ListOpenSteps(ctx, st.ID); err != nil {
		return err
	}
	if budgetUntil, err = s.dispatchQueued(ctx, st, charter, steps, execStates, now); err != nil {
		return err
	}
	if !budgetUntil.IsZero() {
		return s.setState(ctx, st, StateBudget, "цепочка управляющего исчерпана", budgetUntil)
	}
	return s.setState(ctx, st, StateRunning, "ход управляющего", time.Time{})
}

func (s *Supervisor) setState(ctx context.Context, st sdb.Stream, state, reason string, until time.Time) error {
	u := pgtype.Timestamptz{}
	if !until.IsZero() {
		u = pgtype.Timestamptz{Time: until, Valid: true}
	}
	if st.State == state && st.Reason == reason && st.Until.Valid == u.Valid && (!u.Valid || st.Until.Time.Equal(u.Time)) {
		return nil
	}
	if st.State != state {
		s.journal(ctx, st.ID, pgtype.UUID{}, "state", map[string]any{"from": st.State, "to": state, "reason": reason})
	}
	return s.Q.SetStreamState(ctx, sdb.SetStreamStateParams{ID: st.ID, State: state, Reason: reason, Until: u})
}

// hasFreshAnswers reports asks answered or defaulted after the last manage turn.
func (s *Supervisor) hasFreshAnswers(ctx context.Context, st sdb.Stream) bool {
	since := s.lastManageAt(ctx, st)
	resolved, err := s.Q.ListAsksResolvedSince(ctx, sdb.ListAsksResolvedSinceParams{StreamID: st.ID, ResolvedAt: pgtype.Timestamptz{Time: since, Valid: true}})
	return err == nil && len(resolved) > 0
}

func (s *Supervisor) lastManageAt(ctx context.Context, st sdb.Stream) time.Time {
	last, err := s.Q.LastStepOfKind(ctx, sdb.LastStepOfKindParams{StreamID: st.ID, Kind: KindManage})
	if err != nil {
		return time.Time{}
	}
	return last.CreatedAt.Time
}

func (s *Supervisor) orphanTrigger(ctx context.Context, st sdb.Stream, expired []sdb.StreamWait) string {
	if len(expired) > 0 {
		return fmt.Sprintf("Вышел срок ожидания %s (%s) → %s.", expired[0].Kind, firstLine(expired[0].Check, 120), expired[0].OnDeadline)
	}
	if s.hasFreshAnswers(ctx, st) {
		return "Руслан ответил на вопрос (или сработал вариант по умолчанию)."
	}
	recent, err := s.Q.ListRecentSteps(ctx, sdb.ListRecentStepsParams{StreamID: st.ID, Limit: 1})
	if err != nil || len(recent) == 0 {
		return "Первый ход потока: составь план от устава."
	}
	r := recent[0]
	return fmt.Sprintf("Сирота: последний шаг #%d (%s) — %s.", r.Seq, r.Kind, r.Status)
}

// EnqueueManage queues a manager turn. reserved=true is Ruslan's message in
// focus: it runs above max_parallel and does not interrupt current work.
func (s *Supervisor) EnqueueManage(ctx context.Context, streamID pgtype.UUID, trigger string, reserved bool) (sdb.StreamStep, error) {
	st, err := s.Q.GetStream(ctx, streamID)
	if err != nil {
		return sdb.StreamStep{}, err
	}
	step, err := s.enqueueManage(ctx, st, trigger, reserved)
	if err == nil {
		s.Kick()
	}
	return step, err
}

func (s *Supervisor) enqueueManage(ctx context.Context, st sdb.Stream, trigger string, reserved bool) (sdb.StreamStep, error) {
	kind := KindManage
	if n, err := s.Q.CountStepsSinceSummarize(ctx, st.ID); err == nil && n >= SummarizeEvery {
		kind = KindSummarize
	}
	return s.insertStep(ctx, st.ID, kind, trigger, "", "S", "supervisor", reserved)
}

func (s *Supervisor) insertStep(ctx context.Context, streamID pgtype.UUID, kind, brief, moves, cost, origin string, reserved bool) (sdb.StreamStep, error) {
	var step sdb.StreamStep
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := s.Q.WithTx(tx)
		if _, err := q.LockStream(ctx, streamID); err != nil {
			return err
		}
		seq, err := q.AllocStepSeq(ctx, streamID)
		if err != nil {
			return err
		}
		step, err = q.InsertStep(ctx, sdb.InsertStepParams{
			StreamID: streamID, Seq: seq, Kind: kind, Brief: brief, Moves: moves, CostClass: cost,
			Origin: origin, ReservedSlot: reserved, MaxAttempts: MaxStepAttempts,
		})
		if err != nil {
			return err
		}
		return appendJournal(ctx, q, streamID, step.ID, "step_queued", map[string]any{"seq": seq, "kind": kind, "brief": firstLine(brief, 200)})
	})
	return step, err
}

func (s *Supervisor) executorStates(ctx context.Context) (map[string]ExecutorState, error) {
	rows, err := s.Q.ListExecutorStatus(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]ExecutorState, len(rows))
	for _, r := range rows {
		out[r.Executor] = ExecutorState{Executor: r.Executor, ExhaustedUntil: r.ExhaustedUntil.Time, ProbeLevel: int(r.ProbeLevel)}
	}
	return out, nil
}

// chainFor resolves the executor chain of a step (§6.7).
func chainFor(st sdb.Stream, c Charter, step sdb.StreamStep) []Executor {
	switch step.Kind {
	case KindManage, KindSummarize:
		if ch := c.Chains["manage"]; len(ch) > 0 {
			return ch
		}
		if st.ManagerAgentID.Valid {
			return []Executor{{Name: "manager", AgentID: uuidString(st.ManagerAgentID)}}
		}
	case KindVerify:
		if ch := c.Chains["verify"]; len(ch) > 0 {
			return ch
		}
	}
	return c.Chain(step.CostClass)
}

// dispatchQueued starts queued steps within the parallelism limits. It
// returns a non-zero time when some queued step found its chain exhausted.
func (s *Supervisor) dispatchQueued(ctx context.Context, st sdb.Stream, c Charter, steps []sdb.StreamStep, states map[string]ExecutorState, now time.Time) (time.Time, error) {
	runningWork, runningManage := 0, 0
	for _, step := range steps {
		if step.Status != StepRunning {
			continue
		}
		if step.Kind == KindManage || step.Kind == KindSummarize {
			if !step.ReservedSlot {
				runningManage++
			}
		} else {
			runningWork++
		}
	}
	var budgetUntil time.Time
	for _, step := range steps {
		if step.Status != StepQueued {
			continue
		}
		isManage := step.Kind == KindManage || step.Kind == KindSummarize
		switch {
		case isManage && !step.ReservedSlot && runningManage > 0:
			continue
		case !isManage && runningWork >= int(st.MaxParallel):
			continue
		}
		avoid := s.avoidFor(ctx, step)
		ex, until, ok := PickExecutor(chainFor(st, c, step), states, avoid, now)
		if !ok {
			if until.IsZero() {
				// No chain at all is a charter defect, not a budget state.
				s.journal(ctx, st.ID, step.ID, "no_chain", map[string]any{"cost_class": step.CostClass, "kind": step.Kind})
				until = now.Add(OfflineReroute)
			}
			if budgetUntil.IsZero() || until.Before(budgetUntil) {
				budgetUntil = until
			}
			continue
		}
		if err := s.dispatch(ctx, st, c, step, ex, now); err != nil {
			// Agent not ready / create failed: mark the executor briefly
			// exhausted so the next tick moves to the next link.
			s.journal(ctx, st.ID, step.ID, "dispatch_failed", map[string]any{"executor": ex.Name, "error": err.Error()})
			until, level := ExhaustUntil(now, time.Time{}, states[ex.Name].ProbeLevel)
			_ = s.Q.UpsertExecutorStatus(ctx, sdb.UpsertExecutorStatusParams{Executor: ex.Name, ExhaustedUntil: ts(until), Reason: "dispatch: " + firstLine(err.Error(), 200), ProbeLevel: int32(level)})
			states[ex.Name] = ExecutorState{Executor: ex.Name, ExhaustedUntil: until, ProbeLevel: level}
			continue
		}
		if isManage && !step.ReservedSlot {
			runningManage++
		} else if !isManage {
			runningWork++
		}
	}
	return budgetUntil, nil
}

// avoidFor returns executors a step should not land on: the author of the
// work under verification (§8 "another model or provider") and the executor
// that returned garbage twice (§6.4).
func (s *Supervisor) avoidFor(ctx context.Context, step sdb.StreamStep) map[string]bool {
	avoid := map[string]bool{}
	if step.Kind == KindVerify {
		if workID, ok := strings.CutPrefix(step.Origin, "verify:"); ok {
			if id, err := parseUUID(workID); err == nil {
				if work, err := s.Q.GetStep(ctx, id); err == nil && work.Executor != "" {
					avoid[work.Executor] = true
				}
			}
		}
	}
	if step.Executor != "" && strings.HasPrefix(step.LastError, "contract") && step.Attempt >= 2 {
		avoid[step.Executor] = true
	}
	return avoid
}

func (s *Supervisor) dispatch(ctx context.Context, st sdb.Stream, c Charter, step sdb.StreamStep, ex Executor, now time.Time) error {
	agentID, err := parseUUID(ex.AgentID)
	if err != nil {
		return fmt.Errorf("executor %s: bad agent_id: %w", ex.Name, err)
	}
	var body string
	if step.Kind == KindManage || step.Kind == KindSummarize {
		in, err := s.manageInput(ctx, st, step, now)
		if err != nil {
			return err
		}
		body = ManagePrompt(st, c, step, in)
	} else {
		body = WorkPrompt(st, c, step)
	}
	creator := st.ManagerAgentID
	if !creator.Valid {
		creator = agentID
	}
	res, err := s.Carrier.Dispatch(ctx, DispatchRequest{
		WorkspaceID: st.WorkspaceID, ProjectID: st.CarrierProjectID, CreatorAgentID: creator, AgentID: agentID,
		Title: fmt.Sprintf("[%s #%d %s] %s", st.Slug, step.Seq, step.Kind, firstLine(step.Brief, 80)),
		Body:  body,
	})
	if err != nil {
		return err
	}
	if !res.TaskID.Valid {
		if res.IssueID.Valid {
			_ = s.Carrier.Close(ctx, res.IssueID, false)
		}
		return errors.New("agent is not ready: no task enqueued")
	}
	tags, _ := json.Marshal(map[string]string{"stream_id": uuidString(st.ID), "step_id": uuidString(step.ID), "stream_step_kind": step.Kind})
	if err := s.Q.TagTaskContext(ctx, sdb.TagTaskContextParams{ID: res.TaskID, Tags: tags}); err != nil {
		return err
	}
	if err := s.Q.MarkStepDispatched(ctx, sdb.MarkStepDispatchedParams{
		ID: step.ID, Executor: ex.Name, ExecutorAgentID: agentID, CarrierIssueID: res.IssueID, TaskID: res.TaskID, LeaseUntil: ts(now.Add(LeaseDuration)),
	}); err != nil {
		return err
	}
	s.journal(ctx, st.ID, step.ID, "step_dispatched", map[string]any{"seq": step.Seq, "executor": ex.Name, "task_id": uuidString(res.TaskID), "issue_id": uuidString(res.IssueID)})
	return nil
}

func (s *Supervisor) manageInput(ctx context.Context, st sdb.Stream, step sdb.StreamStep, now time.Time) (ManageInput, error) {
	in := ManageInput{Summarize: step.Kind == KindSummarize}
	var err error
	if in.Milestones, err = s.Q.ListRecentMilestones(ctx, sdb.ListRecentMilestonesParams{StreamID: st.ID, Limit: 5}); err != nil {
		return in, err
	}
	if in.OpenAsks, err = s.Q.ListOpenAsks(ctx, st.ID); err != nil {
		return in, err
	}
	// Steps since the previous manage turn.
	var sinceSeq int32
	var sinceAt time.Time
	if prev, err := s.Q.ListRecentSteps(ctx, sdb.ListRecentStepsParams{StreamID: st.ID, Limit: 50}); err == nil {
		for _, p := range prev {
			if p.Seq < step.Seq && (p.Kind == KindManage || p.Kind == KindSummarize) && p.Status == StepDone {
				sinceSeq, sinceAt = p.Seq, p.CreatedAt.Time
				break
			}
		}
	}
	all, err := s.Q.ListStepsSince(ctx, sdb.ListStepsSinceParams{StreamID: st.ID, Seq: sinceSeq})
	if err != nil {
		return in, err
	}
	for _, x := range all {
		if x.ID != step.ID && x.Kind != KindManage && x.Kind != KindSummarize {
			in.RecentSteps = append(in.RecentSteps, x)
		}
	}
	if in.ResolvedAsks, err = s.Q.ListAsksResolvedSince(ctx, sdb.ListAsksResolvedSinceParams{StreamID: st.ID, ResolvedAt: ts(sinceAt)}); err != nil {
		return in, err
	}
	journal, err := s.Q.ListJournalSince(ctx, sdb.ListJournalSinceParams{StreamID: st.ID, CreatedAt: ts(sinceAt)})
	if err != nil {
		return in, err
	}
	for _, j := range journal {
		switch j.Event {
		case "step_failed", "step_retry", "hang", "loop", "wait_expired", "ask_rejected", "dispatch_failed", "quota":
			in.Failures = append(in.Failures, j)
		}
	}
	if len(in.Failures) > 20 {
		in.Failures = in.Failures[len(in.Failures)-20:]
	}
	for _, j := range journal {
		if j.Event == "wait_expired" {
			var w struct {
				WaitID string `json:"wait_id"`
			}
			if json.Unmarshal(j.Detail, &w) == nil {
				if id, err := parseUUID(w.WaitID); err == nil {
					if wt, err := s.Q.GetWait(ctx, id); err == nil {
						in.ExpiredWaits = append(in.ExpiredWaits, wt)
					}
				}
			}
		}
	}
	if st.LastMilestoneAt.Valid {
		in.StallDays = int(now.Sub(st.LastMilestoneAt.Time) / (24 * time.Hour))
	} else {
		in.StallDays = int(now.Sub(st.CreatedAt.Time) / (24 * time.Hour))
	}
	if states, err := s.executorStates(ctx); err == nil {
		for name, x := range states {
			if x.ExhaustedUntil.After(now) {
				in.ExhaustedExec = append(in.ExhaustedExec, name+" до "+fmtMSK(x.ExhaustedUntil))
			}
		}
	}
	return in, nil
}

// observe follows a running step's carrier task (§5.2, §6.3, §6.4).
func (s *Supervisor) observe(ctx context.Context, st sdb.Stream, c Charter, step sdb.StreamStep, states map[string]ExecutorState, now time.Time) error {
	if !step.CarrierIssueID.Valid {
		return s.Q.RequeueStep(ctx, sdb.RequeueStepParams{ID: step.ID, Attempt: step.Attempt, HangCount: step.HangCount, Rejections: step.Rejections, RecoveryNote: step.RecoveryNote, LastError: "carrier lost"})
	}
	task, err := s.Q.LatestTaskForIssue(ctx, step.CarrierIssueID)
	if errors.Is(err, pgx.ErrNoRows) {
		return s.failStep(ctx, st, step, "carrier issue has no task", false, states, now)
	}
	if err != nil {
		return err
	}
	if task.ID != step.TaskID {
		// Upstream auto-retry created a child; it inherited our context tags.
		if err := s.Q.SetStepTask(ctx, sdb.SetStepTaskParams{ID: step.ID, TaskID: task.ID}); err != nil {
			return err
		}
		step.TaskID = task.ID
	}
	switch task.Status {
	case "completed":
		return s.handleCompleted(ctx, st, c, step, task.Result, states, now)
	case "failed":
		reason := task.FailureReason.String
		errText := task.Error.String
		if IsQuotaFailure(reason, errText) {
			return s.reroute(ctx, st, step, ParseQuotaReset(errText, now), reason+": "+firstLine(errText, 300), states, now)
		}
		return s.failStep(ctx, st, step, firstNonEmpty(reason, "failed")+": "+firstLine(errText, 400), false, states, now)
	case "cancelled":
		return s.failStep(ctx, st, step, "task cancelled: "+firstLine(task.Error.String, 300), false, states, now)
	}

	// Still alive: progress extends the lease (§6.3).
	base := task.StartedAt.Time
	if p, err := s.Q.TaskLastProgress(ctx, task.ID); err == nil && p.Time.After(base) {
		base = p.Time
	}
	lease := step.LeaseUntil.Time
	if !base.IsZero() && base.Add(LeaseDuration).After(lease) {
		lease = base.Add(LeaseDuration)
		if err := s.Q.ExtendStepLease(ctx, sdb.ExtendStepLeaseParams{ID: step.ID, LeaseUntil: ts(lease)}); err != nil {
			return err
		}
	}
	switch task.Status {
	case "running":
		if now.After(lease) {
			// Hung on a live daemon: cancel through upstream and retry (§6.4).
			if err := s.Carrier.Cancel(ctx, task.ID, "fabrika: step lease expired without progress"); err != nil {
				return fmt.Errorf("cancel hung task: %w", err)
			}
			step.HangCount++
			s.journal(ctx, st.ID, step.ID, "hang", map[string]any{"seq": step.Seq, "hang_count": step.HangCount, "lease_until": lease})
			return s.failStep(ctx, st, step, fmt.Sprintf("hang: no progress for %s (повисло %d раз)", LeaseDuration, step.HangCount), false, states, now)
		}
	case "queued", "dispatched", "deferred", "waiting_local_directory":
		// Waiting for a runtime. Upstream's runtime_offline path handles a
		// dead daemon; we only move the step after 10 min without a runtime.
		if task.RuntimeID.Valid && now.Sub(task.CreatedAt.Time) > OfflineReroute {
			seen, err := s.Q.RuntimeLastSeen(ctx, task.RuntimeID)
			if err == nil && (!seen.Valid || now.Sub(seen.Time) > OfflineReroute) {
				if err := s.Carrier.Cancel(ctx, task.ID, "fabrika: runtime offline, rerouting step"); err != nil {
					return err
				}
				return s.reroute(ctx, st, step, now.Add(OfflineReroute), "runtime offline", states, now)
			}
		}
	}
	return nil
}

// reroute sends the step to the next executor without spending an attempt
// and marks the current one exhausted until T (§6.4: 402 / quota).
func (s *Supervisor) reroute(ctx context.Context, st sdb.Stream, step sdb.StreamStep, reset time.Time, why string, states map[string]ExecutorState, now time.Time) error {
	prev := states[step.Executor]
	until, level := ExhaustUntil(now, reset, prev.ProbeLevel)
	if err := s.Q.UpsertExecutorStatus(ctx, sdb.UpsertExecutorStatusParams{Executor: step.Executor, ExhaustedUntil: ts(until), Reason: firstLine(why, 300), ProbeLevel: int32(level)}); err != nil {
		return err
	}
	states[step.Executor] = ExecutorState{Executor: step.Executor, ExhaustedUntil: until, ProbeLevel: level}
	s.journal(ctx, st.ID, step.ID, "quota", map[string]any{"executor": step.Executor, "until": until, "why": firstLine(why, 300)})
	if step.CarrierIssueID.Valid {
		_ = s.Carrier.Close(ctx, step.CarrierIssueID, false)
	}
	note := fmt.Sprintf("Предыдущий исполнитель (%s) исчерпан до %s; шаг передан дальше по цепочке. Проверь git и GitHub, прежде чем повторять внешние действия.", step.Executor, fmtMSK(until))
	return s.Q.RequeueStep(ctx, sdb.RequeueStepParams{ID: step.ID, Attempt: step.Attempt, HangCount: step.HangCount, Rejections: step.Rejections, RecoveryNote: note, LastError: firstLine(why, 500)})
}

// failStep retries up to MaxStepAttempts with a recovery note, then hands the
// failure log to the manager; three failed steps in a row break the stream.
func (s *Supervisor) failStep(ctx context.Context, st sdb.Stream, step sdb.StreamStep, why string, contract bool, _ map[string]ExecutorState, now time.Time) error {
	if step.CarrierIssueID.Valid {
		_ = s.Carrier.Close(ctx, step.CarrierIssueID, false)
	}
	attempt := step.Attempt + 1
	lastErr := why
	if contract {
		lastErr = "contract: " + why
	}
	if attempt < step.MaxAttempts {
		note := fmt.Sprintf("Повтор %d из %d. Прошлый запуск: %s\nПроверь git и GitHub, прежде чем повторять внешние действия: часть работы могла уже случиться.", attempt+1, step.MaxAttempts, firstLine(why, 400))
		if contract {
			note += "\nПрошлый запуск не вернул корректный JSON-блок контракта — верни его в конце."
		}
		if step.HangCount >= 2 {
			note += "\nШаг повисал дважды: сделай меньший кусок и верни остаток в `next`."
		}
		s.journal(ctx, st.ID, step.ID, "step_retry", map[string]any{"seq": step.Seq, "attempt": attempt, "why": firstLine(why, 300)})
		return s.Q.RequeueStep(ctx, sdb.RequeueStepParams{ID: step.ID, Attempt: attempt, HangCount: step.HangCount, Rejections: step.Rejections, RecoveryNote: note, LastError: firstLine(lastErr, 500)})
	}
	if err := s.Q.FinishStep(ctx, sdb.FinishStepParams{ID: step.ID, Status: StepFailed, Output: step.Output, LastError: firstLine(lastErr, 1000)}); err != nil {
		return err
	}
	s.journal(ctx, st.ID, step.ID, "step_failed", map[string]any{"seq": step.Seq, "kind": step.Kind, "why": firstLine(why, 300)})
	streak := st.FailStreak + 1
	if err := s.Q.SetStreamFailStreak(ctx, sdb.SetStreamFailStreakParams{ID: st.ID, FailStreak: streak}); err != nil {
		return err
	}
	if streak >= BrokenAfterFailures {
		return s.breakStream(ctx, st, step, why, now)
	}
	// Orphaned now; the reconcile below queues the manager with the log.
	return nil
}

// breakStream puts the stream into "авария" and opens the red crash question
// (§5.1): the only state Ruslan learns about by question.
func (s *Supervisor) breakStream(ctx context.Context, st sdb.Stream, step sdb.StreamStep, why string, now time.Time) error {
	text := firstLine(fmt.Sprintf("%s: %d провала подряд, последний на шаге #%d: %s", st.Title, BrokenAfterFailures, step.Seq, why), 200)
	opts, _ := json.Marshal([]string{"повторить с ходом управляющего", "пауза потока", "дам доступ или подскажу (ответь текстом)"})
	if _, err := s.Q.InsertAsk(ctx, sdb.InsertAskParams{
		StreamID: st.ID, OriginStepID: step.ID, Kind: "choice", Text: text, Options: opts, Items: []byte("[]"),
		Zone: "red", Deadline: ts(now.Add(72 * time.Hour)), Value: 10, CostOfDelay: 10,
	}); err != nil {
		return err
	}
	s.journal(ctx, st.ID, step.ID, "broken", map[string]any{"why": firstLine(why, 300)})
	return s.setState(ctx, st, StateBroken, text, time.Time{})
}

func (s *Supervisor) handleCompleted(ctx context.Context, st sdb.Stream, c Charter, step sdb.StreamStep, result []byte, states map[string]ExecutorState, now time.Time) error {
	var res struct {
		Output string `json:"output"`
	}
	_ = json.Unmarshal(result, &res)
	out, err := ExtractContract(res.Output)
	if err == nil {
		err = ValidateContract(step.Kind, out, now)
	}
	var ask *AskDraft
	if err == nil && out.HumanAction != nil {
		d, aerr := ValidateAsk(*out.HumanAction, now)
		if aerr != nil {
			s.journal(ctx, st.ID, step.ID, "ask_rejected", map[string]any{"text": out.HumanAction.Text, "why": aerr.Error()})
			err = fmt.Errorf("human_action rejected: %w", aerr)
		} else {
			ask = &d
		}
	}
	if err != nil {
		return s.failStep(ctx, st, step, err.Error(), true, states, now)
	}
	_ = s.Q.ClearExecutorStatus(ctx, step.Executor)
	raw, _ := json.Marshal(out)

	switch step.Kind {
	case KindManage, KindSummarize:
		err = s.applyManage(ctx, st, step, out, ask, raw, now)
	case KindWork:
		err = s.applyWork(ctx, st, c, step, out, ask, raw, now)
	case KindVerify:
		err = s.applyVerify(ctx, st, step, out, raw)
	}
	if err != nil {
		return err
	}
	if step.CarrierIssueID.Valid {
		_ = s.Carrier.Close(ctx, step.CarrierIssueID, true)
	}
	return nil
}

func (s *Supervisor) applyManage(ctx context.Context, st sdb.Stream, step sdb.StreamStep, out StepOutput, ask *AskDraft, raw []byte, now time.Time) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := s.Q.WithTx(tx)
		if _, err := q.LockStream(ctx, st.ID); err != nil {
			return err
		}
		if strings.TrimSpace(out.Summary) != "" {
			if err := q.SetStreamSummary(ctx, sdb.SetStreamSummaryParams{ID: st.ID, Summary: out.Summary}); err != nil {
				return err
			}
		}
		if out.Milestone != nil {
			ev, _ := json.Marshal(out.Milestone.Evidence)
			if _, err := q.InsertMilestone(ctx, sdb.InsertMilestoneParams{StreamID: st.ID, StepID: step.ID, Text: out.Milestone.Text, Evidence: ev, MetricDelta: out.Milestone.MetricDelta}); err != nil {
				return err
			}
			if err := q.TouchStreamMilestone(ctx, st.ID); err != nil {
				return err
			}
			if err := appendJournal(ctx, q, st.ID, step.ID, "milestone", map[string]any{"text": out.Milestone.Text}); err != nil {
				return err
			}
		}
		for i, n := range out.Next {
			if i >= 3 {
				break
			}
			kind, cost := n.Kind, n.CostClass
			if kind == "" || kind == KindManage || kind == KindSummarize {
				kind = KindWork
			}
			if cost == "" {
				cost = "M"
			}
			// Loop detector (§6.4): the same brief a third time is not queued.
			if cnt, err := q.CountRecentBrief(ctx, sdb.CountRecentBriefParams{StreamID: st.ID, Brief: n.Brief}); err == nil && cnt >= 2 {
				if err := appendJournal(ctx, q, st.ID, step.ID, "loop", map[string]any{"brief": firstLine(n.Brief, 200)}); err != nil {
					return err
				}
				if err := q.SetStreamFailStreak(ctx, sdb.SetStreamFailStreakParams{ID: st.ID, FailStreak: st.FailStreak + 1}); err != nil {
					return err
				}
				continue
			}
			seq, err := q.AllocStepSeq(ctx, st.ID)
			if err != nil {
				return err
			}
			if _, err := q.InsertStep(ctx, sdb.InsertStepParams{StreamID: st.ID, Seq: seq, Kind: kind, Brief: n.Brief, Moves: n.Moves, CostClass: cost, Origin: "manage:" + uuidString(step.ID), MaxAttempts: MaxStepAttempts}); err != nil {
				return err
			}
		}
		if out.Wait != nil {
			if err := insertWait(ctx, q, st.ID, step.ID, *out.Wait, now); err != nil {
				return err
			}
		}
		if out.NoIdeas {
			// Not a question and not counted against the cap (§6.5).
			if _, err := q.InsertWait(ctx, sdb.InsertWaitParams{StreamID: st.ID, StepID: step.ID, Kind: "time", Check: "нет идей: пересмотр", Deadline: ts(now.Add(NoIdeasReview)), OnDeadline: "replan"}); err != nil {
				return err
			}
			if err := appendJournal(ctx, q, st.ID, step.ID, "no_ideas", map[string]any{"result": out.Result}); err != nil {
				return err
			}
		}
		if ask != nil {
			if err := insertAsk(ctx, q, st.ID, step.ID, *ask); err != nil {
				return err
			}
		}
		return q.FinishStep(ctx, sdb.FinishStepParams{ID: step.ID, Status: StepDone, Output: raw})
	})
}

func (s *Supervisor) applyWork(ctx context.Context, st sdb.Stream, c Charter, step sdb.StreamStep, out StepOutput, ask *AskDraft, raw []byte, now time.Time) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := s.Q.WithTx(tx)
		if _, err := q.LockStream(ctx, st.ID); err != nil {
			return err
		}
		if out.Wait != nil {
			if err := insertWait(ctx, q, st.ID, step.ID, *out.Wait, now); err != nil {
				return err
			}
		}
		if ask != nil {
			if err := insertAsk(ctx, q, st.ID, step.ID, *ask); err != nil {
				return err
			}
		}
		if out.Wait != nil {
			// The result is not final until the wait resolves; the manager
			// decides after it fires whether to verify.
			return q.FinishStep(ctx, sdb.FinishStepParams{ID: step.ID, Status: StepDone, Output: raw})
		}
		if err := q.SetStepVerifying(ctx, sdb.SetStepVerifyingParams{ID: step.ID, Output: raw}); err != nil {
			return err
		}
		seq, err := q.AllocStepSeq(ctx, st.ID)
		if err != nil {
			return err
		}
		brief := fmt.Sprintf("Проверь результат шага #%d.\n\n**Бриф автора:**\n%s\n\n**Критерий «готово» потока:** %s\n\n**Результат автора:** %s\n**Доказательства:** %s\n**Проверка автора:** %s",
			step.Seq, step.Brief, c.Done, out.Result, strings.Join(out.Evidence, ", "), out.DoneCheck)
		_, err = q.InsertStep(ctx, sdb.InsertStepParams{StreamID: st.ID, Seq: seq, Kind: KindVerify, Brief: brief, Moves: step.Moves, CostClass: "S", Origin: "verify:" + uuidString(step.ID), MaxAttempts: MaxStepAttempts})
		return err
	})
}

func (s *Supervisor) applyVerify(ctx context.Context, st sdb.Stream, step sdb.StreamStep, out StepOutput, raw []byte) error {
	workID, _ := strings.CutPrefix(step.Origin, "verify:")
	id, err := parseUUID(workID)
	if err != nil {
		return s.Q.FinishStep(ctx, sdb.FinishStepParams{ID: step.ID, Status: StepDone, Output: raw})
	}
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := s.Q.WithTx(tx)
		work, err := q.GetStep(ctx, id)
		if err != nil {
			return err
		}
		if err := q.FinishStep(ctx, sdb.FinishStepParams{ID: step.ID, Status: StepDone, Output: raw}); err != nil {
			return err
		}
		if out.Verdict == "accept" {
			// The manager's next turn turns it into a milestone (§8 p.3).
			return q.FinishStep(ctx, sdb.FinishStepParams{ID: work.ID, Status: StepDone, Output: work.Output})
		}
		rej := work.Rejections + 1
		if rej > MaxRejections {
			if err := appendJournal(ctx, q, st.ID, work.ID, "step_failed", map[string]any{"seq": work.Seq, "why": "проверка отклонила трижды"}); err != nil {
				return err
			}
			return q.FinishStep(ctx, sdb.FinishStepParams{ID: work.ID, Status: StepFailed, Output: work.Output, LastError: "verify rejected: " + firstLine(out.Result, 400)})
		}
		note := fmt.Sprintf("Проверка вернула на доработку (%d из %d): %s", rej, MaxRejections, out.Result)
		if err := appendJournal(ctx, q, st.ID, work.ID, "rework", map[string]any{"seq": work.Seq, "why": firstLine(out.Result, 300)}); err != nil {
			return err
		}
		return q.RequeueStep(ctx, sdb.RequeueStepParams{ID: work.ID, Attempt: work.Attempt, HangCount: work.HangCount, Rejections: rej, RecoveryNote: note, LastError: ""})
	})
}

func insertWait(ctx context.Context, q *sdb.Queries, streamID, stepID pgtype.UUID, spec WaitSpec, now time.Time) error {
	w, err := ParseWait(spec, now)
	if err != nil {
		return err
	}
	_, err = q.InsertWait(ctx, sdb.InsertWaitParams{StreamID: streamID, StepID: stepID, Kind: w.Kind, Check: w.Check, Deadline: ts(w.Deadline), OnDeadline: w.OnDeadline})
	return err
}

func insertAsk(ctx context.Context, q *sdb.Queries, streamID, stepID pgtype.UUID, d AskDraft) error {
	opts, _ := json.Marshal(nonNil(d.Options))
	items, _ := json.Marshal(nonNil(d.Items))
	def := pgtype.Int4{}
	if d.Default != nil && d.Zone != "red" {
		def = pgtype.Int4{Int32: int32(*d.Default), Valid: true}
	}
	_, err := q.InsertAsk(ctx, sdb.InsertAskParams{
		StreamID: streamID, OriginStepID: stepID, Kind: d.Kind, Text: d.Text, Options: opts, Items: items,
		DefaultOption: def, Zone: d.Zone, Deadline: ts(d.Deadline), Value: d.Value, CostOfDelay: d.Cost,
	})
	return err
}

// settleWaits closes waits whose deadline passed. time/gpu-window waits are
// satisfied by the deadline itself; others expire into on_deadline, which the
// manager's turn reads from the journal.
func (s *Supervisor) settleWaits(ctx context.Context, st sdb.Stream, now time.Time) ([]sdb.StreamWait, error) {
	waits, err := s.Q.ListOpenWaits(ctx, st.ID)
	if err != nil {
		return nil, err
	}
	var expired []sdb.StreamWait
	for _, w := range waits {
		if w.Deadline.Time.After(now) {
			continue
		}
		status, event := "expired", "wait_expired"
		if w.Kind == "time" || w.Kind == "gpu-window" {
			status, event = "fired", "wait_fired"
		}
		if err := s.Q.ResolveWait(ctx, sdb.ResolveWaitParams{ID: w.ID, Status: status}); err != nil {
			return nil, err
		}
		s.journal(ctx, st.ID, w.StepID, event, map[string]any{"wait_id": uuidString(w.ID), "kind": w.Kind, "on_deadline": w.OnDeadline})
		if status == "expired" {
			expired = append(expired, w)
		}
	}
	return expired, nil
}

// FireWait resolves a wait early when its condition is observed outside
// (CI webhook, an operator, a probe). Idempotent.
func (s *Supervisor) FireWait(ctx context.Context, waitID pgtype.UUID) error {
	w, err := s.Q.GetWait(ctx, waitID)
	if err != nil {
		return err
	}
	if err := s.Q.ResolveWait(ctx, sdb.ResolveWaitParams{ID: w.ID, Status: "fired"}); err != nil {
		return err
	}
	s.journal(ctx, w.StreamID, w.StepID, "wait_fired", map[string]any{"wait_id": uuidString(w.ID), "kind": w.Kind, "external": true})
	s.Kick()
	return nil
}

// settleAsks applies silence = default for normal-zone questions past their
// deadline, reminds red ones once, and drops stale never-shown ones (§7.2).
func (s *Supervisor) settleAsks(ctx context.Context, st sdb.Stream, now time.Time) error {
	asks, err := s.Q.ListOpenAsks(ctx, st.ID)
	if err != nil {
		return err
	}
	for _, a := range asks {
		if a.Deadline.Time.After(now) {
			continue
		}
		switch {
		case a.Status == "queued":
			if err := s.Q.ResolveAsk(ctx, sdb.ResolveAskParams{ID: a.ID, Status: "dropped", Answer: []byte(`{"reason":"устарел, не показан"}`)}); err != nil {
				return err
			}
			s.journal(ctx, st.ID, a.OriginStepID, "ask_dropped", map[string]any{"text": a.Text})
		case a.Zone == "normal" && a.DefaultOption.Valid:
			ans, _ := json.Marshal(map[string]any{"choice": a.DefaultOption.Int32, "channel": "default", "at": now})
			if err := s.Q.ResolveAsk(ctx, sdb.ResolveAskParams{ID: a.ID, Status: "defaulted", Answer: ans}); err != nil {
				return err
			}
			s.journal(ctx, st.ID, a.OriginStepID, "ask_defaulted", map[string]any{"text": a.Text, "choice": a.DefaultOption.Int32})
		case !a.RemindedAt.Valid:
			// Red zone never defaults: remind once in the next digest.
			if err := s.Q.MarkAskReminded(ctx, a.ID); err != nil {
				return err
			}
			s.journal(ctx, st.ID, a.OriginStepID, "ask_reminded", map[string]any{"text": a.Text})
		}
	}
	return nil
}

func (s *Supervisor) journal(ctx context.Context, streamID, stepID pgtype.UUID, event string, detail map[string]any) {
	if err := appendJournal(ctx, s.Q, streamID, stepID, event, detail); err != nil {
		slog.Warn("streams: journal append failed", "event", event, "error", err)
	}
}

func appendJournal(ctx context.Context, q *sdb.Queries, streamID, stepID pgtype.UUID, event string, detail map[string]any) error {
	raw, err := json.Marshal(detail)
	if err != nil {
		raw = []byte("{}")
	}
	return q.AppendJournal(ctx, sdb.AppendJournalParams{StreamID: streamID, StepID: stepID, Event: event, Detail: raw})
}

func ts(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func uuidString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	b := u.Bytes
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func parseUUID(s string) (pgtype.UUID, error) {
	var u pgtype.UUID
	err := u.Scan(strings.TrimSpace(s))
	if err == nil && !u.Valid {
		err = errors.New("empty uuid")
	}
	return u, err
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
