package streams

import (
	"context"
	"strings"
	"testing"
	"time"
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
