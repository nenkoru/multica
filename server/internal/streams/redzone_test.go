package streams

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/streams/sdb"
)

func TestValidateAskRedTopicNeedsRedZone(t *testing.T) {
	t0 := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	def := 0
	normal := HumanAction{
		Text:     "RUSL-2171: одобрить запуск места work.ruslan. Мандат: люди, личный доступ.",
		Options:  []string{"да, запускать", "пока нет"},
		Default:  &def,
		Zone:     "normal",
		Deadline: t0.Add(72 * time.Hour).Format(time.RFC3339),
	}
	if _, err := ValidateAsk(normal, t0); err == nil || !strings.Contains(err.Error(), "red zone") {
		t.Fatalf("normal-zone mandate question accepted: %v", err)
	}
	red := normal
	red.Zone, red.Default = "red", nil
	if _, err := ValidateAsk(red, t0); err != nil {
		t.Fatalf("red version rejected: %v", err)
	}
	// Each category on its own, without the others in the same text.
	for text, want := range map[string]string{
		"Ответить рекрутеру? Мандат: люди.":           "people outside the circle",
		"Мандат: люди, зона":                          "people outside the circle",
		"Потратить больше лимита на API в ноябре?":    "money over budget",
		"Поднять потолок до 120 $?":                   "money over budget",
		"Удалить старые снапшоты без копии?":          "irreversible without a backup",
		"Стереть ветку безвозвратно?":                 "irreversible without a backup",
		"Ответить покупателю? Мандат: зона Наташи.":   "Natasha's zone",
		"Нужен ключ от почты — личный доступ Руслана": "personal access",
	} {
		if got := redTopic(text); got != want {
			t.Errorf("%q: got %q, want %q", text, got, want)
		}
	}
	for _, text := range []string{"Продлить домен за 1200 ₽?", "Какой заголовок у сводки?", "Шорт-лист из 15 позиций — ок?"} {
		if why := redTopic(text); why != "" {
			t.Fatalf("%q flagged as %s", text, why)
		}
	}
}

func TestRedTopicAskNeverDefaults(t *testing.T) {
	h := newHarness(t, "redask")
	ctx := context.Background()
	h.tick()
	mg := h.only(KindManage, StepRunning)
	// An ask stored before the screen existed: normal zone with a default.
	h.f.Exec(t, `INSERT INTO ask (stream_id, origin_step_id, kind, text, options, default_option, zone, deadline, status)
		VALUES ($1, $2, 'choice', 'Одобрить запуск? Мандат: люди, личный доступ.', '["да","нет"]', 0, 'normal', now() - interval '1 hour', 'shown')`,
		uuidString(h.stream.ID), uuidString(mg.ID))
	if err := h.sup.settleAsks(ctx, h.reload(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if n := h.journalCount("ask_defaulted"); n != 0 {
		t.Fatalf("mandate question answered by silence: %d", n)
	}
	if n := h.journalCount("ask_reminded"); n != 1 {
		t.Fatalf("want one reminder, got %d", n)
	}
}

// RUSL-2729: the pulse of 06.11 showed RUSL-2171 (mandate: people, personal
// access) as a normal question with "(по умолчанию)". The tick turns such a
// row red without a default, its buttons get the red confirmation, it never
// settles by silence and stays in the next pulse.
func TestMandateAskStoredNormalTurnsRed(t *testing.T) {
	h := newHarness(t, "redrow")
	ctx := context.Background()
	h.tick()
	mg := h.only(KindManage, StepRunning)
	text := "RUSL-2171: одобрить запуск места work.ruslan. Мандат: люди, личный доступ."
	var askID string
	h.f.QueryRow(t, `INSERT INTO ask (stream_id, origin_step_id, kind, text, options, default_option, zone, deadline, status, shown_at)
		VALUES ($1, $2, 'choice', $3, '["да, запускать","пока нет"]', 0, 'normal', now() + interval '1 day', 'shown', now()) RETURNING id::text`,
		uuidString(h.stream.ID), uuidString(mg.ID), text).Scan(&askID)
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), "DELETE FROM ui_action WHERE ask_id = $1", askID) })
	h.f.Exec(t, `INSERT INTO ui_action (ask_id, token, action, zone) VALUES ($1, $2, '{"kind":"ask_reply","choice":0}', 'normal')`, askID, "tok-"+askID)

	// Before the deadline: the row is fixed, nothing is resolved.
	if err := h.sup.settleAsks(ctx, h.reload(), time.Now()); err != nil {
		t.Fatal(err)
	}
	var zone, status string
	var def *int32
	h.f.QueryRow(t, `SELECT zone, default_option, status FROM ask WHERE id = $1`, askID).Scan(&zone, &def, &status)
	if zone != "red" || def != nil || status != "shown" {
		t.Fatalf("ask after tick: zone=%s default=%v status=%s, want red/nil/shown", zone, def, status)
	}
	if z := h.f.Count(t, `SELECT count(*) FROM ui_action WHERE ask_id = $1 AND zone = 'red'`, askID); z != 1 {
		t.Fatalf("pending button not red: %d", z)
	}
	if n := h.journalCount("ask_reddened"); n != 1 {
		t.Fatalf("ask_reddened: %d", n)
	}

	// Past the deadline, twice: still open, one reminder, never defaulted.
	later := time.Now().Add(48 * time.Hour)
	for i := 0; i < 2; i++ {
		if err := h.sup.settleAsks(ctx, h.reload(), later); err != nil {
			t.Fatal(err)
		}
	}
	h.f.QueryRow(t, `SELECT status FROM ask WHERE id = $1`, askID).Scan(&status)
	if status != "shown" || h.journalCount("ask_defaulted") != 0 || h.journalCount("ask_reminded") != 1 || h.journalCount("ask_reddened") != 1 {
		t.Fatalf("status=%s defaulted=%d reminded=%d reddened=%d", status, h.journalCount("ask_defaulted"), h.journalCount("ask_reminded"), h.journalCount("ask_reddened"))
	}

	// The next pulse still asks it, red, with no default promised.
	pulse, err := h.sup.Digest(ctx, h.stream.WorkspaceID, SlotPreview)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pulse, "🔴 Поток redrow: "+text) || strings.Contains(pulse, "(по умолчанию)") || strings.Contains(pulse, "Молчание = вариант по умолчанию") {
		t.Fatalf("pulse:\n%s", pulse)
	}
}

// The pulse never promises a default for a mandate question, even for a row
// the tick has not fixed yet (preview right after a deploy).
func TestRenderAskMandateHasNoDefault(t *testing.T) {
	at := pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true}
	legacy := sdb.Ask{Text: "Одобрить запуск? Мандат: люди, личный доступ.", Options: []byte(`["да","нет"]`), Zone: "normal", DefaultOption: pgtype.Int4{Int32: 0, Valid: true}, Deadline: at}
	if out := renderAsk(1, legacy, "Терн", time.Now()); !strings.HasPrefix(out, "1. 🔴 Терн:") || strings.Contains(out, "по умолчанию") {
		t.Fatalf("mandate ask rendered:\n%s", out)
	}
	plain := sdb.Ask{Text: "Шорт-лист из 15 позиций — ок?", Options: []byte(`["да","нет"]`), Zone: "normal", DefaultOption: pgtype.Int4{Int32: 0, Valid: true}, Deadline: at}
	if out := renderAsk(1, plain, "Работа", time.Now()); strings.Contains(out, "🔴") || !strings.Contains(out, "а) да (по умолчанию)") {
		t.Fatalf("plain ask rendered:\n%s", out)
	}
	if askZone(legacy) != "red" || askDefault(legacy) != -1 || askDefault(plain) != 0 {
		t.Fatal("askZone/askDefault")
	}
}

func TestManagerPatchFillsEmptySummary(t *testing.T) {
	h := newHarness(t, "patch")
	h.tick()
	mg := h.only(KindManage, StepRunning)
	h.finish(mg, contract(map[string]any{
		"result": "план", "summary_patch": "Переезд: 15 позиций ждут «ок».",
		"next": []map[string]any{{"brief": "шорт-лист", "kind": "work", "cost_class": "M", "moves": "отклики"}},
	}))
	h.tick()
	if st := h.reload(); st.Summary != "Переезд: 15 позиций ждут «ок»." {
		t.Fatalf("summary %q", st.Summary)
	}
	// Empty again (as on streams migrated before this fix): the next patch
	// rebuilds it from the earlier done steps' patches.
	h.f.Exec(t, `UPDATE stream SET summary = '' WHERE id = $1`, uuidString(h.stream.ID))
	work := h.only(KindWork, StepRunning)
	step, err := h.sup.Q.GetStep(context.Background(), work.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := patchedSummary(context.Background(), h.sup.Q, h.reload(), step, "Шаг 2: без «ок».")
	if err != nil || got != "Переезд: 15 позиций ждут «ок».\nШаг 2: без «ок»." {
		t.Fatalf("%q %v", got, err)
	}
}

// RUSL-2699: Терн и Работа had their only manager turn before patchedSummary
// shipped and wait days for the next one. The tick rebuilds the summary.
func TestEmptySummaryBackfilledOnTick(t *testing.T) {
	h := newHarness(t, "backfill")
	h.tick()
	h.finish(h.only(KindManage, StepRunning), contract(map[string]any{
		"result": "план", "summary_patch": "Переезд: 15 позиций ждут «ок».",
		"next": []map[string]any{{"brief": "шорт-лист", "kind": "work", "cost_class": "M", "moves": "отклики"}},
	}))
	h.tick()
	h.f.Exec(t, `UPDATE stream SET summary = '' WHERE id = $1`, uuidString(h.stream.ID))
	h.tick()
	if st := h.reload(); st.Summary != "Переезд: 15 позиций ждут «ок»." {
		t.Fatalf("summary %q", st.Summary)
	}
	h.tick()
	if n := h.journalCount("summary_rebuilt"); n != 1 {
		t.Fatalf("summary_rebuilt %d", n)
	}
}

// An expired replan wait reaches the manager even while another wait is
// open (Терн, 05.11: RUSL-2269 expired, RUSL-2171 still open, no turn).
func TestExpiredWaitReplansDespiteOpenWait(t *testing.T) {
	h := newHarness(t, "replan")
	h.tick()
	mg := h.only(KindManage, StepRunning)
	h.finish(mg, contract(map[string]any{
		"result": "ждём", "summary_patch": "ждём RUSL-2171",
		"wait": map[string]any{"kind": "issue", "check": "RUSL-2171", "deadline": time.Now().Add(48 * time.Hour).Format(time.RFC3339)},
	}))
	h.tick()
	if st := h.reload(); st.State != StateWaiting {
		t.Fatalf("state %s", st.State)
	}
	if _, err := h.sup.Q.InsertWait(context.Background(), sdb.InsertWaitParams{
		StreamID: h.stream.ID, StepID: mg.ID, Kind: "issue", Check: "RUSL-2269",
		Deadline: ts(time.Now().Add(-time.Minute)), OnDeadline: "replan",
	}); err != nil {
		t.Fatal(err)
	}
	h.tick()
	next := h.only(KindManage, StepRunning)
	if !strings.Contains(next.Brief, "RUSL-2269") {
		t.Fatalf("brief %q", next.Brief)
	}
}

// Review round 1 of RUSL-2729: a button sent while the ask was normal, on a
// stream the tick does not reach (inactive), still needs the red confirmation
// once the ask is about the mandate.
func TestOldButtonOfMandateAskNeedsRedConfirm(t *testing.T) {
	h := newHarness(t, "redbtn")
	h.cleanupActions()
	ctx := context.Background()
	ws, _ := parseUUID(testWorkspaceID)
	inst, chat := h.multikaTelegram(434343)
	fake := &fakeButtonSender{}
	h.sup.AskButtons = fake
	h.askShown("Продлить домен за 1200 ₽?")
	if n, err := h.sup.PushAskButtons(ctx, ws); err != nil || n != 1 {
		t.Fatalf("push n=%d err=%v", n, err)
	}
	h.f.Exec(t, `UPDATE ask SET text = 'RUSL-2171: одобрить запуск. Мандат: люди, личный доступ.' WHERE stream_id = $1`, h.stream.ID)
	h.f.Exec(t, `UPDATE stream SET active = false WHERE id = $1`, h.stream.ID)
	h.tick()
	if z := h.f.Count(t, `SELECT count(*) FROM ui_action WHERE ask_id IN (SELECT id FROM ask WHERE stream_id = $1) AND zone = 'normal'`, h.stream.ID); z == 0 {
		t.Fatal("scenario needs a normal-zone button left over")
	}
	toast, done := h.sup.PressAskButton(ctx, inst, chat, chat, fake.sent[0].rows[0][0].Data)
	if toast != pressConfirmSent || done != "" || h.askStatus() != "shown" {
		t.Fatalf("press: %q %q ask=%s", toast, done, h.askStatus())
	}
}
