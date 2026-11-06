package streams

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/metrics"
	"github.com/multica-ai/multica/server/internal/streams/genui"
	"github.com/multica-ai/multica/server/internal/streams/sdb"
	"github.com/multica-ai/multica/server/pkg/llm"
)

// ScreenRules teach Мультика the screen language and the catalog (§10.1,
// §10.3, §10.4). The same text is the repair prompt's reference.
const ScreenRules = "Экраны. Когда ответ лучше показать, чем рассказать (деньги, состояние потоков, сравнение, вехи), добавь в ответ один блок:\n" +
	"```openui\nroot = Stack([kpis, spend, acts])\nkpis = KpiStrip([Kpi(\"За 14 дней\", \"$41.20\", \"-8%\"), Kpi(\"Больше всех\", \"Терн\")])\nspend = StackedBars(\"Расход API по дням\", [\"22.10\", \"23.10\"], [Series(\"Терн\", [1.5, 3]), Series(\"buvg01\", [2, 0.5])], \"$\")\nacts = Actions([Button(\"Урезать долю Терна вдвое\", set_share(\"tern\", 0.5))])\n```\n" +
	"Правила: строка = один компонент «id = Компонент(аргументы)», первая строка — root, порядок строк — порядок появления у Руслана: сначала root и короткие KPI, потом графики, кнопки в конце. Аргументы позиционные, необязательные можно опустить или поставить null; строки в двойных кавычках; на id можно сослаться до его строки.\n" +
	"Каталог: Stack([id…], \"column\"|\"row\"); Text(\"текст\", \"default\"|\"muted\"|\"heading\"); KpiStrip([Kpi(…)…]); Kpi(\"подпись\", \"значение\", \"дельта\"?, \"пояснение\"?); " +
	"TrendChart(\"заголовок\", [\"метки\"], [Series(\"имя\", [числа])…], \"ед.\"?); StackedBars(те же аргументы; столбцы с накоплением); MilestoneCard(\"текст\", \"ссылка-доказательство\"?, \"когда\"?, \"поток\"?); " +
	"AskCard(\"вопрос\", [\"варианты\"], \"ask_id\"?, номер-по-умолчанию?, \"normal\"|\"red\"); StreamCard(\"slug\", \"название\", состояние, \"цель\"?, id-KpiStrip?, id-графика?, \"сейчас\"?, [\"дальше\"]?, \"ссылка\"?) — состояния running, waiting, asking, budget, paused, broken, milestone; " +
	"Table([\"колонки\"], [[\"ячейки\"]…]); IssueRow(\"RUSL-1\", \"название\", статус?, \"ссылка\"?); AgentStack([\"имена\"], \"подпись\"?); Actions([Button(\"надпись\", действие)…]) — до 6 кнопок.\n" +
	"Действия кнопок (других нет): focus_stream(\"slug\"), open(\"ссылка\"), followup(\"текст реплики\"), pause_stream(\"slug\"), resume_stream(\"slug\"), set_share(\"slug\", множитель) — меньше 1 урезает долю, 1 и больше — красная зона, raise_budget(\"slug\", usd) — красная зона, answer_ask(\"ask_id\", номер варианта с 0). Красную зону Руслан подтверждает в окне приложения, ты её не обещаешь.\n" +
	"Нельзя: HTML, ссылки в надписях (только open и поле ссылки), чужой текст (письма, тексты из upstream) на кнопках и в ссылках, цифры не из контекста. Текст вокруг блока — короткий вывод, цифры экрана не повторяй.\n"

// multikaContextEnd closes Мультика's context block.
const multikaContextEnd = "[Конец контекста. Ниже — сообщение Руслана.]"

// SpendDays is the window of the money screen (§9.1: 14 days).
const SpendDays = 14

// spendTable is per-stream API spend by Moscow day.
type spendTable struct {
	Days   []string             // "02.01"
	By     map[string][]float64 // stream id → USD per day
	Total  float64              // all workspace agents over the window
	Stream float64              // the streams' part of Total
}

func (s *Supervisor) spend(ctx context.Context, ws pgtype.UUID, streams []sdb.Stream, now time.Time) (spendTable, error) {
	t := now.In(Moscow)
	start := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, Moscow).AddDate(0, 0, -(SpendDays - 1))
	tab := spendTable{By: map[string][]float64{}}
	index := map[string]int{}
	for i := 0; i < SpendDays; i++ {
		d := start.AddDate(0, 0, i)
		tab.Days = append(tab.Days, d.Format("02.01"))
		index[d.Format("2006-01-02")] = i
	}
	ids := make([]string, 0, len(streams))
	for _, st := range streams {
		ids = append(ids, uuidString(st.ID))
		tab.By[uuidString(st.ID)] = make([]float64, SpendDays)
	}
	rows, err := s.Q.StreamDailyCost(ctx, sdb.StreamDailyCostParams{StreamIds: ids, Since: ts(start)})
	if err != nil {
		return tab, err
	}
	for _, r := range rows {
		if i, ok := index[r.Day]; ok && tab.By[r.StreamID] != nil {
			usd := metrics.UsageCostUSD(r.Model, r.Ticks, r.InputTokens, r.OutputTokens, r.CacheReadTokens, r.CacheWriteTokens)
			tab.By[r.StreamID][i] += usd
			tab.Stream += usd
		}
	}
	total, err := s.Q.WorkspaceCostSince(ctx, sdb.WorkspaceCostSinceParams{WorkspaceID: ws, Since: ts(start)})
	if err == nil {
		for _, r := range total {
			tab.Total += metrics.UsageCostUSD(r.Model, r.Ticks, r.InputTokens, r.OutputTokens, r.CacheReadTokens, r.CacheWriteTokens)
		}
	}
	return tab, nil
}

// screenContext gives Мультика the screen rules and the numbers a money or
// state screen needs. Code-built, a few hundred tokens.
func (s *Supervisor) screenContext(ctx context.Context, ws pgtype.UUID, streams []sdb.Stream, acts MultikaTurnActions, now time.Time) string {
	var b strings.Builder
	b.WriteString(ScreenRules)
	var active []sdb.Stream
	for _, st := range streams {
		if st.Active {
			active = append(active, st)
		}
	}
	tab, err := s.spend(ctx, ws, active, now)
	if err != nil {
		slog.Warn("streams: spend for multika failed", "error", err)
		return b.String()
	}
	fmt.Fprintf(&b, "Расход API за %d дней по МСК, $ (отчёт провайдера, без него — оценка по токенам и прайсу; дни %s–%s):\n", SpendDays, tab.Days[0], tab.Days[len(tab.Days)-1])
	for _, st := range active {
		vals := tab.By[uuidString(st.ID)]
		sum := 0.0
		cells := make([]string, len(vals))
		for i, v := range vals {
			sum += v
			cells[i] = strconv.FormatFloat(round2(v), 'f', -1, 64)
		}
		share := charterShare(st.Charter)
		line := fmt.Sprintf("- %s (%s): всего %.2f; по дням [%s]", st.Title, st.Slug, sum, strings.Join(cells, ", "))
		if share > 0 {
			line += fmt.Sprintf("; доля %s", fmtShare(share))
			if s.APIMonthlyUSD > 0 {
				line += fmt.Sprintf(" = $%.0f/мес", share*s.APIMonthlyUSD)
			}
		}
		b.WriteString(line + "\n")
	}
	if tab.Total > 0 {
		fmt.Fprintf(&b, "Всего агенты воркспейса: $%.2f, из них вне потоков $%.2f.\n", tab.Total, max(0, tab.Total-tab.Stream))
	}
	if s.APIMonthlyUSD > 0 {
		fmt.Fprintf(&b, "Месячный потолок API: $%.0f.\n", s.APIMonthlyUSD)
	}
	if acts.Entered != "" {
		b.WriteString("Код уже показал Руслану карточку потока «" + acts.Entered + "» (StreamCard): не рисуй её заново, говори по делу.\n")
	}
	return b.String()
}

func round2(f float64) float64 { return float64(int64(f*100+0.5)) / 100 }

// streamCardSource builds the StreamCard screen of a stream (§9.1 screen
// 2): state, goal, four KPIs, 14-day spend, now and next.
func (s *Supervisor) streamCardSource(ctx context.Context, ws pgtype.UUID, st sdb.Stream, now time.Time) (string, error) {
	c, _ := ParseCharter(st.Charter)
	tab, err := s.spend(ctx, ws, []sdb.Stream{st}, now)
	if err != nil {
		return "", err
	}
	vals := tab.By[uuidString(st.ID)]
	sum := 0.0
	nums := make([]string, len(vals))
	for i, v := range vals {
		sum += v
		nums[i] = strconv.FormatFloat(round2(v), 'f', -1, 64)
	}
	since := now.AddDate(0, 0, -SpendDays)
	stats, err := s.Q.StreamCardStats(ctx, sdb.StreamCardStatsParams{StreamID: st.ID, Since: ts(since)})
	if err != nil {
		return "", err
	}
	nowText, next := "", []string{}
	if steps, err := s.Q.ListOpenSteps(ctx, st.ID); err == nil {
		for _, sp := range steps {
			switch {
			case sp.Status != StepQueued && nowText == "":
				nowText = fmt.Sprintf("шаг %d (%s): %s", sp.Seq, sp.Kind, firstLine(sp.Brief, 100))
				if sp.Executor != "" {
					nowText += "; исполнитель " + sp.Executor
				}
				nowText += "; пульс " + sp.UpdatedAt.Time.In(Moscow).Format("15:04")
				if sp.LeaseUntil.Valid {
					nowText += ", lease до " + sp.LeaseUntil.Time.In(Moscow).Format("15:04")
				}
				nowText += " МСК"
			case sp.Status == StepQueued && len(next) < 3:
				next = append(next, fmt.Sprintf("%s: %s", sp.Kind, firstLine(sp.Brief, 80)))
			}
		}
	}
	if nowText == "" {
		nowText = stateWords[st.State]
		if st.Reason != "" {
			nowText += ": " + firstLine(st.Reason, 120)
		}
	}
	q := strconv.Quote
	list := func(xs []string) string {
		out := make([]string, len(xs))
		for i, x := range xs {
			out[i] = q(x)
		}
		return "[" + strings.Join(out, ", ") + "]"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "root = Stack([card, acts])\n")
	fmt.Fprintf(&b, "card = StreamCard(%s, %s, %s, %s, kpis, trend, %s, %s)\n", q(st.Slug), q(st.Title), q(st.State), q(firstLine(c.Goal, 300)), q(nowText), list(next))
	fmt.Fprintf(&b, "kpis = KpiStrip([Kpi(%s, %s), Kpi(%s, %s), Kpi(%s, %s), Kpi(%s, %s)])\n",
		q("API за 14 дней"), q(fmt.Sprintf("$%.2f", sum)), q("Шагов готово"), q(strconv.Itoa(int(stats.StepsDone))),
		q("Вех"), q(strconv.Itoa(int(stats.Milestones))), q("Открытых вопросов"), q(strconv.Itoa(int(stats.OpenAsks))))
	fmt.Fprintf(&b, "trend = TrendChart(%s, %s, [Series(%s, [%s])], %s)\n", q("Расход API за 14 дней"), list(tab.Days), q(st.Title), strings.Join(nums, ", "), q("$"))
	pause := fmt.Sprintf("Button(%s, pause_stream(%s))", q("Пауза"), q(st.Slug))
	if st.State == StatePaused {
		pause = fmt.Sprintf("Button(%s, resume_stream(%s))", q("Продолжить"), q(st.Slug))
	}
	fmt.Fprintf(&b, "acts = Actions([Button(%s, set_share(%s, 0.5)), %s])\n", q("Урезать долю вдвое"), q(st.Slug), pause)
	return b.String(), nil
}

// showStreamCard stores the StreamCard of a stream Руслан just dove into as
// a code-built screen of this turn; it is announced with the reply.
func (s *Supervisor) showStreamCard(ctx context.Context, ws, sessionID, taskID pgtype.UUID, st sdb.Stream) {
	id := blockID(taskID, codeBlockSeq)
	if _, err := s.Q.GetUIBlock(ctx, id); err == nil {
		return
	}
	src, err := s.streamCardSource(ctx, ws, st, s.now())
	if err != nil {
		slog.Warn("streams: stream card failed", "stream", st.Slug, "error", err)
		return
	}
	res := genui.Finalize(src, s.screenOptions(ctx, ws), nil)
	if _, err := s.storeScreen(ctx, id, ws, sessionID, taskID, pgtype.UUID{}, codeBlockSeq, "code", res); err != nil {
		slog.Warn("streams: stream card store failed", "stream", st.Slug, "error", err)
	}
}

// LLMRepairer fixes a screen with one completion of the server's LLM
// (MULTICA_LLM_*). Nil when the client is not configured.
func LLMRepairer(c *llm.Client, model string) genui.Repairer {
	if !c.Enabled() {
		return nil
	}
	return func(source string, issues []genui.Issue) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		var b strings.Builder
		b.WriteString("Экран:\n```openui\n" + source + "\n```\nОшибки проверки:\n")
		for _, i := range issues {
			b.WriteString("- " + i.String() + "\n")
		}
		b.WriteString("Верни исправленный экран целиком одним блоком ```openui, без пояснений. Убери то, что нельзя исправить.")
		return c.GenerateText(ctx, model, "Ты чинишь экран на языке OpenUI Lang по каталогу Фабрики.\n"+ScreenRules, b.String())
	}
}
