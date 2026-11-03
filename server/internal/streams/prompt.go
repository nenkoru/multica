package streams

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/streams/sdb"
)

// Prompts are ordered stable → volatile for the prompt cache (§11):
// charter → skills → summary → brief.

const contractInstructions = "## Контракт выхода\n\n" +
	"Закончи ход **одним** JSON-блоком в ```json … ```. Без блока шаг не принят и уйдёт на повтор.\n\n" +
	"```json\n" +
	`{
  "result": "что сделано, одна строка",
  "evidence": ["pr:org/repo#412", "ci:run/123", "bench:2026-11-03-02"],
  "done_check": "какая проверка подтверждает готовность и её итог",
  "summary_patch": "что дописать в summary потока",
  "next": [{"brief": "...", "kind": "work", "cost_class": "M", "moves": "какую метрику двигает"}],
  "wait": {"kind": "ci", "check": "gh run view 123 --json conclusion", "deadline": "2026-11-03T16:00:00+03:00", "on_deadline": "replan"},
  "human_action": {"text": "≤200 знаков", "options": ["а", "б"], "default": 0, "zone": "normal", "deadline": "2026-11-04T18:00:00+03:00"}
}` + "\n```\n\n" +
	"- Необязательные поля опускай. `next` — только предложение, решает управляющий.\n" +
	"- Фоновый процесс без `wait` оставлять нельзя: либо дождись в этом ходе, либо верни `wait` с конечным сроком.\n" +
	"- Историю комментариев не читай: всё нужное — в этом брифе. Итог пиши только в JSON-блок.\n"

const managerInstructions = "## Ты — управляющий потока\n\n" +
	"Реши, что дальше. Ничего не исполняй сам: выбери **одно** из — следующий шаг (`next`), Ожидание (`wait`), Вопрос Руслану (`human_action`) или `\"no_ideas\": true`.\n\n" +
	"- Каждый шаг в `next` называет метрику в `moves`. Без метрики шаг не ставится.\n" +
	"- `summary` — новая полная версия summary (≤1500 токенов): примени `summary_patch` шагов по порядку. Пиши summary только ты.\n" +
	"- Шаг прошёл проверку (`verify` = accept) — оформи веху: `\"milestone\": {\"text\": \"…\", \"evidence\": [\"…\"], \"metric_delta\": \"…\"}`.\n" +
	"- Сам решаешь: подход и архитектуру, приоритет, `max_parallel`, исполнителя, «доработать или переделать», что делать после 402/падения/зависания, любой обратимый мердж или деплой. Такие вопросы проверка формы отклонит.\n" +
	"- Вопрос Руслану — только по пяти категориям мандата (деньги сверх бюджета, необратимое без бэкапа, личный доступ, люди вне круга, зона Наташи). У красной зоны нет варианта по умолчанию. Пока ждёшь ответа — ищи независимую работу.\n" +
	"- Создавать issue, назначенные агентам, в обход шага запрещено: исполняемая работа идёт только через `next`.\n" +
	"- Повисло дважды — разбей мельче. Нет вехи 3 дня — дай план или честное `no_ideas`.\n"

func charterBlock(st sdb.Stream, c Charter) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Поток «%s»\n\n## Устав\n\n", st.Title)
	fmt.Fprintf(&b, "- **Цель:** %s\n- **Метрика:** %s\n- **Готово =** %s\n- **Мандат:** %s\n", c.Goal, c.Metric, c.Done, c.Mandate)
	if len(c.People) > 0 {
		fmt.Fprintf(&b, "- **Круг людей:** %s\n", strings.Join(c.People, ", "))
	}
	if len(c.Repos) > 0 {
		fmt.Fprintf(&b, "- **Репозитории:** %s\n", strings.Join(c.Repos, ", "))
	}
	for _, w := range c.Windows {
		fmt.Fprintf(&b, "- **Окно %s:** %s–%s МСК\n", w.Kind, w.Start, w.End)
	}
	for _, r := range c.Rules {
		fmt.Fprintf(&b, "- %s\n", r)
	}
	if len(c.Skills) > 0 {
		fmt.Fprintf(&b, "\n## Скиллы\n\nПользуйся только этими: %s\n", strings.Join(c.Skills, ", "))
	}
	b.WriteString("\n## Summary\n\n")
	if strings.TrimSpace(st.Summary) == "" {
		b.WriteString("(пусто)\n")
	} else {
		b.WriteString(st.Summary + "\n")
	}
	return b.String()
}

// WorkPrompt is the carrier issue body for work/verify steps.
func WorkPrompt(st sdb.Stream, c Charter, step sdb.StreamStep) string {
	var b strings.Builder
	b.WriteString(charterBlock(st, c))
	fmt.Fprintf(&b, "\n## Бриф шага #%d (%s, класс %s)\n\n%s\n", step.Seq, step.Kind, step.CostClass, step.Brief)
	if step.Moves != "" {
		fmt.Fprintf(&b, "\n**Двигает метрику:** %s\n", step.Moves)
	}
	if step.RecoveryNote != "" {
		fmt.Fprintf(&b, "\n## Восстановление\n\n%s\n", step.RecoveryNote)
	}
	if step.Kind == KindVerify {
		b.WriteString("\n## Ты — проверяющий\n\nТы видишь бриф, критерий и результат, но не рассуждения автора. Сначала детерминированные проверки (тесты, CI, бенчмарк, скриншот), потом мнение. Верни `\"verdict\": \"accept\"` или `\"verdict\": \"rework\"` и в `result` — что именно не так.\n")
	}
	b.WriteString("\n" + contractInstructions)
	return b.String()
}

// ManageInput is everything a manage turn reads (§6.5).
type ManageInput struct {
	Milestones    []sdb.Milestone
	OpenAsks      []sdb.Ask
	ResolvedAsks  []sdb.Ask
	RecentSteps   []sdb.StreamStep
	ExpiredWaits  []sdb.StreamWait
	Failures      []sdb.StreamJournal
	Summarize     bool
	StallDays     int
	Trigger       string
	ExhaustedExec []string
}

// ManagePrompt is the carrier issue body for manage/summarize turns.
func ManagePrompt(st sdb.Stream, c Charter, step sdb.StreamStep, in ManageInput) string {
	var b strings.Builder
	b.WriteString(charterBlock(st, c))
	b.WriteString("\n" + managerInstructions)
	if in.Summarize {
		b.WriteString("\n**Это ход сжатия (`summarize`):** перепиши summary короче, сохранив решения, открытые хвосты и метрику. `next` можно не давать.\n")
	}
	fmt.Fprintf(&b, "\n## Почему ход\n\n%s\n", step.Brief)
	if step.RecoveryNote != "" {
		fmt.Fprintf(&b, "\n%s\n", step.RecoveryNote)
	}
	if in.StallDays >= 3 {
		fmt.Fprintf(&b, "\n**Затухание:** вехи нет %d дн. Дай план или честное `no_ideas`.\n", in.StallDays)
	}
	if len(in.Milestones) > 0 {
		b.WriteString("\n## Последние вехи\n\n")
		for _, m := range in.Milestones {
			fmt.Fprintf(&b, "- %s — %s %s\n", fmtMSK(m.CreatedAt.Time), m.Text, string(m.Evidence))
		}
	}
	if len(in.RecentSteps) > 0 {
		b.WriteString("\n## Шаги с прошлого хода\n\n")
		for _, s := range in.RecentSteps {
			fmt.Fprintf(&b, "- #%d %s [%s] %s\n", s.Seq, s.Kind, s.Status, firstLine(s.Brief, 160))
			if out, err := decodeOutput(s.Output); err == nil {
				fmt.Fprintf(&b, "  - итог: %s\n", out.Result)
				if len(out.Evidence) > 0 {
					fmt.Fprintf(&b, "  - доказательства: %s\n", strings.Join(out.Evidence, ", "))
				}
				if out.Verdict != "" {
					fmt.Fprintf(&b, "  - вердикт проверки: %s\n", out.Verdict)
				}
				if out.SummaryPatch != "" {
					fmt.Fprintf(&b, "  - summary_patch: %s\n", out.SummaryPatch)
				}
				for _, n := range out.Next {
					fmt.Fprintf(&b, "  - предлагает: %s (%s, %s) → %s\n", firstLine(n.Brief, 160), n.Kind, n.CostClass, n.Moves)
				}
			}
			if s.LastError != "" {
				fmt.Fprintf(&b, "  - ошибка: %s\n", firstLine(s.LastError, 300))
			}
		}
	}
	if len(in.Failures) > 0 {
		b.WriteString("\n## Журнал провалов\n\n")
		for _, j := range in.Failures {
			fmt.Fprintf(&b, "- %s %s %s\n", fmtMSK(j.CreatedAt.Time), j.Event, firstLine(string(j.Detail), 300))
		}
	}
	if len(in.ExpiredWaits) > 0 {
		b.WriteString("\n## Ожидания, у которых вышел срок\n\n")
		for _, w := range in.ExpiredWaits {
			fmt.Fprintf(&b, "- %s `%s`, срок %s, при сроке: %s\n", w.Kind, w.Check, fmtMSK(w.Deadline.Time), w.OnDeadline)
		}
	}
	if len(in.ResolvedAsks) > 0 {
		b.WriteString("\n## Ответы Руслана\n\n")
		for _, a := range in.ResolvedAsks {
			fmt.Fprintf(&b, "- «%s» → %s\n", a.Text, answerText(a))
		}
	}
	if len(in.OpenAsks) > 0 {
		b.WriteString("\n## Открытые вопросы (ждут пачки или ответа)\n\n")
		for _, a := range in.OpenAsks {
			fmt.Fprintf(&b, "- «%s» (%s, до %s)\n", a.Text, a.Zone, fmtMSK(a.Deadline.Time))
		}
	}
	if len(in.ExhaustedExec) > 0 {
		fmt.Fprintf(&b, "\n**Исчерпаны:** %s\n", strings.Join(in.ExhaustedExec, ", "))
	}
	b.WriteString("\n" + contractInstructions)
	return b.String()
}

func decodeOutput(raw []byte) (StepOutput, error) {
	var out StepOutput
	if len(raw) == 0 {
		return out, fmt.Errorf("no output")
	}
	err := json.Unmarshal(raw, &out)
	return out, err
}

func firstLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	r := []rune(s)
	if len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}

func fmtMSK(t time.Time) string {
	return t.In(Moscow).Format("02.01 15:04") + " МСК"
}

// answerText renders a resolved ask for the manager: the chosen option in
// words, not the raw answer JSON.
func answerText(a sdb.Ask) string {
	var ans struct {
		Choice  *int   `json:"choice"`
		Items   *bool  `json:"items"`
		Channel string `json:"channel"`
	}
	_ = json.Unmarshal(a.Answer, &ans)
	var opts []string
	_ = json.Unmarshal(a.Options, &opts)
	how := "ответ Руслана"
	if a.Status == "defaulted" {
		how = "молчание, взят вариант по умолчанию"
	}
	switch {
	case ans.Choice != nil && *ans.Choice >= 0 && *ans.Choice < len(opts):
		return fmt.Sprintf("«%s» (%s)", opts[*ans.Choice], how)
	case ans.Items != nil && *ans.Items:
		return "да на все пункты (" + how + ")"
	case ans.Items != nil:
		return "нет на все пункты (" + how + ")"
	}
	return a.Status
}
