package streams

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/streams/sdb"
	"github.com/multica-ai/multica/server/internal/testutil"
)

var (
	testPool        *pgxpool.Pool
	testWorkspaceID string
	testUserID      string
)

const fixtureSlug = "fabrika-streams-tests"

func TestMain(m *testing.M) {
	ctx := context.Background()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}
	pool, err := pgxpool.New(ctx, dbURL)
	if err == nil {
		err = pool.Ping(ctx)
	}
	if err != nil {
		fmt.Printf("Skipping streams DB tests: %v\n", err)
		os.Exit(m.Run())
	}
	_, _ = pool.Exec(ctx, `DELETE FROM workspace WHERE slug = $1`, fixtureSlug)
	_, _ = pool.Exec(ctx, `DELETE FROM "user" WHERE email = $1`, fixtureSlug+"@example.com")
	if err := pool.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ('Fabrika Test', $1) RETURNING id`, fixtureSlug+"@example.com").Scan(&testUserID); err != nil {
		fmt.Printf("Skipping streams DB tests: seed user: %v\n", err)
		os.Exit(m.Run())
	}
	if err := pool.QueryRow(ctx, `INSERT INTO workspace (name, slug, issue_prefix) VALUES ('Fabrika Tests', $1, 'FAB') RETURNING id`, fixtureSlug).Scan(&testWorkspaceID); err != nil {
		fmt.Printf("Skipping streams DB tests: seed workspace: %v\n", err)
		os.Exit(m.Run())
	}
	_, _ = pool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`, testWorkspaceID, testUserID)
	testPool = pool
	code := m.Run()
	_, _ = pool.Exec(ctx, `DELETE FROM workspace WHERE slug = $1`, fixtureSlug)
	_, _ = pool.Exec(ctx, `DELETE FROM "user" WHERE email = $1`, fixtureSlug+"@example.com")
	pool.Close()
	os.Exit(code)
}

// fakeCarrier stands in for IssueService/TaskService: it writes the same
// rows (issue + agent_task_queue) the real carrier produces.
type fakeCarrier struct {
	t  *testing.T
	f  *testutil.Fixture
	rt string
	mu sync.Mutex

	dispatched []DispatchRequest
	cancelled  []string
	closed     map[string]bool
	notReady   map[string]bool
	comments   map[string][]string // task id → bodies, newest first
}

func (c *fakeCarrier) Dispatch(ctx context.Context, req DispatchRequest) (DispatchResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	agent := uuidString(req.AgentID)
	if c.notReady[agent] {
		return DispatchResult{}, fmt.Errorf("agent %s not ready", agent)
	}
	c.dispatched = append(c.dispatched, req)
	issueID := c.f.Issue(c.t, req.Title, testutil.Cols{"assignee_type": "agent", "assignee_id": agent, "description": req.Body})
	taskID := c.f.Task(c.t, agent, testutil.Cols{"issue_id": issueID, "runtime_id": c.rt})
	iid, _ := parseUUID(issueID)
	tid, _ := parseUUID(taskID)
	return DispatchResult{IssueID: iid, TaskID: tid}, nil
}

func (c *fakeCarrier) Cancel(ctx context.Context, taskID pgtype.UUID, reason string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cancelled = append(c.cancelled, uuidString(taskID))
	_, err := testPool.Exec(ctx, `UPDATE agent_task_queue SET status = 'cancelled', error = $2, completed_at = now() WHERE id = $1`, taskID, reason)
	return err
}

func (c *fakeCarrier) Close(ctx context.Context, issueID pgtype.UUID, done bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed[uuidString(issueID)] = done
	return nil
}

func (c *fakeCarrier) TaskComments(ctx context.Context, issueID, taskID pgtype.UUID) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.comments[uuidString(taskID)], nil
}

type harness struct {
	t       *testing.T
	f       *testutil.Fixture
	sup     *Supervisor
	carrier *fakeCarrier
	stream  sdb.Stream
	agents  map[string]string // executor name → agent id
}

func newHarness(t *testing.T, slug string) *harness {
	t.Helper()
	if testPool == nil {
		t.Skip("database not available")
	}
	f := testutil.New(testPool, testWorkspaceID, testUserID)
	rt := f.Runtime(t, "rt-"+slug)
	agents := map[string]string{}
	for _, name := range []string{"manager", "qwen", "claude", "api"} {
		agents[name] = f.Agent(t, slug+"-"+name, rt)
	}
	carrier := &fakeCarrier{t: t, f: f, rt: rt, closed: map[string]bool{}, notReady: map[string]bool{}, comments: map[string][]string{}}
	sup := NewSupervisor(testPool, carrier)
	charter := Charter{
		Goal: "форк не стоит", Metric: "зелёные выкатки в неделю", Done: "тесты зелёные, verify ОК",
		Chains: map[string][]Executor{
			"manage": {{Name: slug + "-mgr", AgentID: agents["manager"]}},
			"M":      {{Name: slug + "-claude", AgentID: agents["claude"]}, {Name: slug + "-api", AgentID: agents["api"]}},
			"S":      {{Name: slug + "-qwen", AgentID: agents["qwen"]}, {Name: slug + "-claude", AgentID: agents["claude"]}},
		},
	}
	raw, _ := json.Marshal(charter)
	ws, _ := parseUUID(testWorkspaceID)
	mgr, _ := parseUUID(agents["manager"])
	var share pgtype.Numeric
	_ = share.Scan("0")
	st, err := sup.Q.UpsertStream(context.Background(), sdb.UpsertStreamParams{
		WorkspaceID: ws, Slug: slug, Title: "Поток " + slug, Charter: raw, Weight: 1, MinShare: share, MaxParallel: 1, ManagerAgentID: mgr,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		for _, table := range []string{"stream_step", "stream_wait", "ask", "milestone", "stream_journal"} {
			_, _ = testPool.Exec(ctx, "DELETE FROM "+table+" WHERE stream_id = $1", st.ID)
		}
		_, _ = testPool.Exec(ctx, "DELETE FROM executor_status WHERE executor LIKE $1", slug+"-%")
		_, _ = testPool.Exec(ctx, "DELETE FROM stream WHERE id = $1", st.ID)
	})
	return &harness{t: t, f: f, sup: sup, carrier: carrier, stream: st, agents: agents}
}

func (h *harness) tick() {
	h.t.Helper()
	if err := h.sup.Tick(context.Background()); err != nil {
		h.t.Fatalf("tick: %v", err)
	}
}

func (h *harness) reload() sdb.Stream {
	h.t.Helper()
	st, err := h.sup.Q.GetStream(context.Background(), h.stream.ID)
	if err != nil {
		h.t.Fatal(err)
	}
	return st
}

func (h *harness) open() []sdb.StreamStep {
	h.t.Helper()
	steps, err := h.sup.Q.ListOpenSteps(context.Background(), h.stream.ID)
	if err != nil {
		h.t.Fatal(err)
	}
	return steps
}

func (h *harness) only(kind, status string) sdb.StreamStep {
	h.t.Helper()
	var found []sdb.StreamStep
	for _, s := range h.open() {
		if s.Kind == kind && s.Status == status {
			found = append(found, s)
		}
	}
	if len(found) != 1 {
		h.t.Fatalf("want one %s/%s step, have %+v", kind, status, h.open())
	}
	return found[0]
}

// finish completes the step's current task with the given agent output.
func (h *harness) finish(step sdb.StreamStep, output string) {
	h.t.Helper()
	res, _ := json.Marshal(map[string]string{"output": output})
	h.f.Exec(h.t, `UPDATE agent_task_queue SET status = 'completed', result = $2, started_at = now(), completed_at = now() WHERE id = $1`, uuidString(step.TaskID), string(res))
}

func (h *harness) fail(step sdb.StreamStep, reason, msg string) {
	h.t.Helper()
	h.f.Exec(h.t, `UPDATE agent_task_queue SET status = 'failed', failure_reason = $2, error = $3, completed_at = now() WHERE id = $1`, uuidString(step.TaskID), reason, msg)
}

func (h *harness) journalCount(event string) int {
	h.t.Helper()
	return h.f.Count(h.t, `SELECT count(*) FROM stream_journal WHERE stream_id = $1 AND event = $2`, uuidString(h.stream.ID), event)
}

func contract(v map[string]any) string {
	raw, _ := json.Marshal(v)
	return "Готово.\n```json\n" + string(raw) + "\n```"
}

func TestSupervisorFullCycle(t *testing.T) {
	h := newHarness(t, "cycle")

	// Orphan → manager turn, tagged and idempotent.
	h.tick()
	mg := h.only(KindManage, StepRunning)
	if st := h.reload(); st.State != StateRunning {
		t.Fatalf("state %s", st.State)
	}
	var ctxJSON string
	h.f.QueryRow(t, `SELECT context::text FROM agent_task_queue WHERE id = $1`, uuidString(mg.TaskID)).Scan(&ctxJSON)
	if !strings.Contains(ctxJSON, uuidString(h.stream.ID)) || !strings.Contains(ctxJSON, uuidString(mg.ID)) {
		t.Fatalf("task context not tagged: %s", ctxJSON)
	}
	if !strings.Contains(h.carrier.dispatched[0].Body, "Ты — управляющий потока") {
		t.Fatal("manager prompt missing")
	}
	h.tick()
	if n := len(h.carrier.dispatched); n != 1 {
		t.Fatalf("second tick re-dispatched: %d", n)
	}

	// Manager plans a work step.
	h.finish(mg, contract(map[string]any{
		"result": "план", "summary": "Поток начат.",
		"next": []map[string]any{{"brief": "Собрать ветку", "kind": "work", "cost_class": "M", "moves": "зелёные выкатки"}},
	}))
	h.tick()
	work := h.only(KindWork, StepRunning)
	if work.Executor != "cycle-claude" {
		t.Fatalf("work executor %s", work.Executor)
	}
	if st := h.reload(); st.Summary != "Поток начат." {
		t.Fatalf("summary %q", st.Summary)
	}

	// Work done → verifying + verify on another executor than the author.
	h.finish(work, contract(map[string]any{"result": "ветка собрана", "evidence": []string{"ci:run/1"}, "done_check": "go test ok"}))
	h.tick()
	h.only(KindWork, StepVerifying)
	verify := h.only(KindVerify, StepRunning)
	if verify.Executor == "cycle-claude" {
		t.Fatal("verify ran on the author's executor")
	}

	// Verify accepts → work done → orphan → manager turn with the result.
	h.finish(verify, contract(map[string]any{"result": "тесты зелёные", "verdict": "accept"}))
	h.tick()
	mg2 := h.only(KindManage, StepRunning)
	last := h.carrier.dispatched[len(h.carrier.dispatched)-1]
	if !strings.Contains(last.Body, "ветка собрана") || !strings.Contains(last.Body, "вердикт проверки: accept") {
		t.Fatalf("manager does not see the verified result:\n%s", last.Body)
	}

	// Manager accepts a milestone and waits on CI.
	h.finish(mg2, contract(map[string]any{
		"result": "веха", "milestone": map[string]any{"text": "ветка fabrika собрана", "evidence": []string{"ci:run/1"}},
		"wait": map[string]any{"kind": "ci", "check": "gh run view 2", "deadline": time.Now().Add(time.Hour).Format(time.RFC3339)},
	}))
	h.tick()
	st := h.reload()
	if st.State != StateWaiting || !st.LastMilestoneAt.Valid {
		t.Fatalf("want waiting with milestone, got %s %v", st.State, st.LastMilestoneAt)
	}
	if n := h.f.Count(t, `SELECT count(*) FROM milestone WHERE stream_id = $1`, uuidString(h.stream.ID)); n != 1 {
		t.Fatalf("milestones %d", n)
	}
	if h.journalCount("orphan") != 2 {
		t.Fatalf("orphans %d", h.journalCount("orphan"))
	}
}

func TestSupervisorQuotaReroutesWithoutAttempt(t *testing.T) {
	h := newHarness(t, "quota")
	h.tick()
	mg := h.only(KindManage, StepRunning)
	h.finish(mg, contract(map[string]any{"result": "план", "next": []map[string]any{{"brief": "код", "moves": "m"}}}))
	h.tick()
	work := h.only(KindWork, StepRunning)
	h.fail(work, "agent_error.unknown", "API error: status 402 Payment Required")
	h.tick()
	work2 := h.only(KindWork, StepRunning)
	if work2.ID != work.ID || work2.Attempt != 0 || work2.Executor != "quota-api" {
		t.Fatalf("want same step on next executor without attempt, got %+v", work2)
	}
	if !strings.Contains(work2.RecoveryNote, "исчерпан") {
		t.Fatalf("recovery note %q", work2.RecoveryNote)
	}
	var until pgtype.Timestamptz
	h.f.QueryRow(t, `SELECT exhausted_until FROM executor_status WHERE executor = 'quota-claude'`).Scan(&until)
	if !until.Valid || until.Time.Before(time.Now().Add(14*time.Minute)) {
		t.Fatalf("exhausted_until %v", until)
	}

	// Whole chain exhausted → honest "budget until T".
	h.fail(work2, "agent_error.provider_quota_limit", "quota exceeded")
	h.tick()
	st := h.reload()
	if st.State != StateBudget || !st.Until.Valid {
		t.Fatalf("want budget until T, got %s %v", st.State, st.Until)
	}
}

func TestSupervisorGarbageRetriesThenBreaks(t *testing.T) {
	h := newHarness(t, "garbage")
	h.tick()
	for i := 0; i < MaxStepAttempts; i++ {
		mg := h.only(KindManage, StepRunning)
		if int(mg.Attempt) != i {
			t.Fatalf("attempt %d, want %d", mg.Attempt, i)
		}
		h.finish(mg, "я подумал, но контракта нет")
		h.tick()
	}
	// 3 attempts spent → step failed, streak 1, a new manage turn queued.
	if n := h.f.Count(t, `SELECT count(*) FROM stream_step WHERE stream_id = $1 AND status = 'failed'`, uuidString(h.stream.ID)); n != 1 {
		t.Fatalf("failed steps %d", n)
	}
	for streak := 2; streak <= BrokenAfterFailures; streak++ {
		for i := 0; i < MaxStepAttempts; i++ {
			h.finish(h.only(KindManage, StepRunning), "мусор")
			h.tick()
		}
	}
	st := h.reload()
	if st.State != StateBroken {
		t.Fatalf("want broken, got %s (%s)", st.State, st.Reason)
	}
	if n := h.f.Count(t, `SELECT count(*) FROM ask WHERE stream_id = $1 AND zone = 'red' AND default_option IS NULL`, uuidString(h.stream.ID)); n != 1 {
		t.Fatalf("crash question %d", n)
	}
	// Broken streams do nothing until Ruslan answers.
	before := len(h.carrier.dispatched)
	h.tick()
	if len(h.carrier.dispatched) != before {
		t.Fatal("broken stream dispatched work")
	}
}

func TestSupervisorHangCancelsAndRetries(t *testing.T) {
	h := newHarness(t, "hang")
	h.tick()
	mg := h.only(KindManage, StepRunning)
	h.f.Exec(t, `UPDATE agent_task_queue SET status = 'running', started_at = now() - interval '30 minutes' WHERE id = $1`, uuidString(mg.TaskID))
	h.f.Exec(t, `UPDATE stream_step SET lease_until = now() - interval '1 minute' WHERE id = $1`, uuidString(mg.ID))
	h.tick()
	if len(h.carrier.cancelled) != 1 || h.carrier.cancelled[0] != uuidString(mg.TaskID) {
		t.Fatalf("cancelled %v", h.carrier.cancelled)
	}
	retry := h.only(KindManage, StepRunning)
	if retry.ID != mg.ID || retry.Attempt != 1 || retry.HangCount != 1 || !strings.Contains(retry.RecoveryNote, "Проверь git и GitHub") {
		t.Fatalf("retry %+v", retry)
	}

	// Progress extends the lease: a fresh task message keeps it alive.
	h.f.Exec(t, `UPDATE agent_task_queue SET status = 'running', started_at = now() - interval '30 minutes' WHERE id = $1`, uuidString(retry.TaskID))
	h.f.Exec(t, `UPDATE stream_step SET lease_until = now() - interval '1 minute' WHERE id = $1`, uuidString(retry.ID))
	h.f.Exec(t, `INSERT INTO task_message (task_id, seq, type, content) VALUES ($1, 1, 'text', 'работаю')`, uuidString(retry.TaskID))
	h.tick()
	if len(h.carrier.cancelled) != 1 {
		t.Fatal("progressing task was cancelled")
	}
}

func TestSupervisorAskBatchDefaultAndAnswer(t *testing.T) {
	h := newHarness(t, "ask")
	ctx := context.Background()
	h.tick()
	mg := h.only(KindManage, StepRunning)
	deadline := time.Now().Add(48 * time.Hour).Format(time.RFC3339)
	h.finish(mg, contract(map[string]any{
		"result":       "нужен Руслан",
		"human_action": map[string]any{"text": "Продлить домен за 1200 ₽?", "options": []string{"да", "нет"}, "default": 1, "zone": "normal", "deadline": deadline},
	}))
	h.tick()
	if st := h.reload(); st.State != StateAsking {
		t.Fatalf("want asking, got %s", st.State)
	}
	ws, _ := parseUUID(testWorkspaceID)
	text, err := h.sup.Digest(ctx, ws, SlotMorning)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "Поток ask — ждёт тебя") || !strings.Contains(text, "1. Поток ask: Продлить домен") || !strings.Contains(text, "б) нет (по умолчанию)") {
		t.Fatalf("digest:\n%s", text)
	}
	// Evening slot with nothing new still shows the open batch.
	if ev, _ := h.sup.Digest(ctx, ws, SlotEvening); !strings.Contains(ev, "Продлить домен") {
		t.Fatalf("evening:\n%s", ev)
	}

	res, err := h.sup.Answer(ctx, ws, "1 а, привет", "telegram")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) != 1 || res.Rest != "привет" {
		t.Fatalf("answer %+v", res)
	}
	h.tick()
	h.only(KindManage, StepRunning)
	last := h.carrier.dispatched[len(h.carrier.dispatched)-1]
	if !strings.Contains(last.Body, "Ответы Руслана") || !strings.Contains(last.Body, "«Продлить домен за 1200 ₽?» → «да» (ответ Руслана)") {
		t.Fatalf("manager does not see the answer:\n%s", last.Body)
	}
}

func TestSupervisorAskRejectedBySelfDecideRule(t *testing.T) {
	h := newHarness(t, "selfdecide")
	h.tick()
	mg := h.only(KindManage, StepRunning)
	h.finish(mg, contract(map[string]any{
		"result":       "вопрос",
		"human_action": map[string]any{"text": "Какую библиотеку взять?", "options": []string{"a", "b"}, "default": 0, "deadline": time.Now().Add(time.Hour).Format(time.RFC3339)},
	}))
	h.tick()
	retry := h.only(KindManage, StepRunning)
	if retry.Attempt != 1 || !strings.Contains(retry.LastError, "decide it yourself") {
		t.Fatalf("retry %+v", retry)
	}
	if h.journalCount("ask_rejected") != 1 {
		t.Fatal("rejection not journaled")
	}
}

func TestSupervisorConcurrentTicksAreIdempotent(t *testing.T) {
	h := newHarness(t, "concurrent")
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = h.sup.Tick(context.Background())
		}()
	}
	wg.Wait()
	h.tick()
	if n := h.f.Count(t, `SELECT count(*) FROM stream_step WHERE stream_id = $1`, uuidString(h.stream.ID)); n != 1 {
		t.Fatalf("steps %d", n)
	}
	if len(h.carrier.dispatched) != 1 {
		t.Fatalf("dispatched %d", len(h.carrier.dispatched))
	}
}

func TestSupervisorDispatchFailureMovesDownTheChain(t *testing.T) {
	h := newHarness(t, "notready")
	h.tick()
	mg := h.only(KindManage, StepRunning)
	h.finish(mg, contract(map[string]any{"result": "план", "next": []map[string]any{{"brief": "код", "moves": "m"}}}))
	h.carrier.notReady[h.agents["claude"]] = true
	h.tick()
	h.tick()
	work := h.only(KindWork, StepRunning)
	if work.Executor != "notready-api" {
		t.Fatalf("executor %s", work.Executor)
	}
	if h.journalCount("dispatch_failed") != 1 {
		t.Fatal("dispatch failure not journaled")
	}
}

func TestHealthReflectsTicks(t *testing.T) {
	h := newHarness(t, "health")
	h.tick()
	st, err := h.sup.Q.GetSupervisorState(context.Background())
	if err != nil || time.Since(st.LastTickAt.Time) > time.Minute {
		t.Fatalf("state %+v %v", st, err)
	}
}

// planAndRunWork drives orphan → manager → one work step of the given class.
func (h *harness) planAndRunWork(cost string) {
	h.t.Helper()
	h.tick()
	h.finish(h.only(KindManage, StepRunning), contract(map[string]any{
		"result": "план", "next": []map[string]any{{"brief": "Собрать ветку", "kind": "work", "cost_class": cost, "moves": "зелёные выкатки"}},
	}))
	h.tick()
}

func TestSupervisorFailedVerifyDoesNotStrandWork(t *testing.T) {
	h := newHarness(t, "lostverify")
	h.planAndRunWork("M")
	h.finish(h.only(KindWork, StepRunning), contract(map[string]any{"result": "ветка собрана", "evidence": []string{"ci:run/1"}}))
	h.tick()
	for i := 0; i < MaxStepAttempts; i++ {
		h.finish(h.only(KindVerify, StepRunning), "проверка без контракта")
		h.tick()
	}
	// The verify step failed for good: the work must not stay "verifying",
	// and the stream must go back to its manager instead of hanging.
	for _, s := range h.open() {
		if s.Status == StepVerifying {
			t.Fatalf("work stranded in verifying: %+v", s)
		}
	}
	h.only(KindManage, StepRunning)
	if st := h.reload(); st.State != StateRunning || st.FailStreak != 1 {
		t.Fatalf("state %s streak %d", st.State, st.FailStreak)
	}
}

func TestSupervisorProgressResetsFailStreak(t *testing.T) {
	h := newHarness(t, "streak")
	h.f.Exec(t, `UPDATE stream SET fail_streak = 2 WHERE id = $1`, uuidString(h.stream.ID))
	h.planAndRunWork("M")
	if st := h.reload(); st.FailStreak != 2 {
		t.Fatalf("a manager turn alone is not progress: streak %d", st.FailStreak)
	}
	h.finish(h.only(KindWork, StepRunning), contract(map[string]any{"result": "ветка собрана", "evidence": []string{"ci:run/1"}}))
	h.tick()
	h.finish(h.only(KindVerify, StepRunning), contract(map[string]any{"result": "ок", "verdict": "accept"}))
	h.tick()
	if st := h.reload(); st.FailStreak != 0 {
		t.Fatalf("accepted work must reset the streak, got %d", st.FailStreak)
	}
}

func TestSupervisorMissingChainBreaksInsteadOfBudget(t *testing.T) {
	h := newHarness(t, "nochain")
	h.planAndRunWork("L") // the test charter has no L and no "*" chain
	st := h.reload()
	if st.State != StateBroken || !strings.Contains(st.Reason, "нет исполнителей") {
		t.Fatalf("want broken on a charter defect, got %s (%s)", st.State, st.Reason)
	}
	if h.journalCount("no_chain") != 1 {
		t.Fatalf("no_chain journaled %d times", h.journalCount("no_chain"))
	}
}

// Multica agents deliver their result as a comment; the final output may be
// a short recap without the contract block.
func TestSupervisorContractFromCarrierComment(t *testing.T) {
	h := newHarness(t, "comment")
	h.tick()
	mg := h.only(KindManage, StepRunning)
	h.carrier.comments[uuidString(mg.TaskID)] = []string{
		"Уточнение без контракта.",
		contract(map[string]any{
			"result": "план",
			"next":   []map[string]any{{"brief": "Собрать ветку", "kind": "work", "cost_class": "M", "moves": "зелёные выкатки"}},
		}),
	}
	h.carrier.comments["00000000-0000-0000-0000-000000000000"] = []string{contract(map[string]any{"result": "чужой"})}
	h.finish(mg, "Сделано. План опубликован комментарием.")
	h.tick()
	h.only(KindWork, StepRunning)
	if n := h.journalCount("step_retry") + h.journalCount("step_failed"); n != 0 {
		t.Fatalf("step retried or failed %d times", n)
	}
}

func TestDigestCarriesLastMilestone(t *testing.T) {
	h := newHarness(t, "pulse")
	h.tick()
	text, err := h.sup.Digest(context.Background(), h.stream.WorkspaceID, SlotPreview)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "Поток pulse — идёт") || !strings.Contains(text, "последняя веха: пока нет") {
		t.Fatalf("pulse line:\n%s", text)
	}
}
