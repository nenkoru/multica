package streams

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/streams/sdb"
)

// The digest is assembled by code from milestones, questions, budget and
// pulse, so it arrives even when no LLM is available (§7.4).

var stateWords = map[string]string{
	StateRunning: "идёт",
	StateWaiting: "ждёт условия",
	StateAsking:  "ждёт тебя",
	StateBudget:  "бюджет",
	StatePaused:  "пауза",
	StateBroken:  "авария",
}

// SilentStopGap is how long a stream may sit without a live reason before
// the orphan counts as a silent stop in the digest.
const SilentStopGap = 5 * time.Minute

// DigestSlot selects what Digest includes.
type DigestSlot string

const (
	// SlotMorning (10:00) always sends: pulse + questions.
	SlotMorning DigestSlot = "morning"
	// SlotEvening (18:00) sends only if there are questions to show.
	SlotEvening DigestSlot = "evening"
	// SlotPreview renders without marking questions shown.
	SlotPreview DigestSlot = "preview"
)

// Digest renders the pulse and, for real slots, moves the next questions
// into the shown batch within the daily cap. Empty text means "send nothing".
func (s *Supervisor) Digest(ctx context.Context, workspaceID pgtype.UUID, slot DigestSlot) (string, error) {
	now := s.now()
	streams, err := s.Q.ListStreams(ctx, workspaceID)
	if err != nil {
		return "", err
	}
	if slot != SlotPreview {
		if err := s.promoteAsks(ctx, workspaceID, now); err != nil {
			return "", err
		}
	}
	shown, err := s.Q.ListShownAsks(ctx, workspaceID)
	if err != nil {
		return "", err
	}
	if slot == SlotEvening && len(shown) == 0 {
		return "", nil
	}

	var b strings.Builder
	title := "Фабрика · пульс"
	if slot == SlotEvening {
		title = "Фабрика · вопросы"
	}
	fmt.Fprintf(&b, "%s %s\n", title, fmtMSK(now))

	if slot != SlotEvening {
		since := now.Add(-24 * time.Hour)
		silent := 0
		for _, st := range streams {
			if !st.Active {
				continue
			}
			line, n, err := s.streamPulse(ctx, st, since, now)
			if err != nil {
				return "", err
			}
			silent += n
			b.WriteString("\n" + line)
		}
		if ex := s.exhaustedLine(ctx, now); ex != "" {
			b.WriteString("\n" + ex + "\n")
		}
		fmt.Fprintf(&b, "\nТихих остановок за сутки: %d\n", silent)
		if sup, err := s.Q.GetSupervisorState(ctx); err == nil {
			age := now.Sub(sup.LastTickAt.Time).Round(time.Second)
			fmt.Fprintf(&b, "Супервизор: последний тик %s назад", age)
			if sup.LastError != "" {
				fmt.Fprintf(&b, ", ошибка: %s", firstLine(sup.LastError, 160))
			}
			b.WriteString("\n")
		}
	}

	if len(shown) > 0 {
		b.WriteString("\nВопросы:\n")
		names := map[string]string{}
		for _, st := range streams {
			names[uuidString(st.ID)] = st.Title
		}
		for i, a := range shown {
			b.WriteString(renderAsk(i+1, a, names[uuidString(a.StreamID)]))
		}
		b.WriteString("Ответ одной строкой: «1 а, 2 да». Молчание = вариант по умолчанию (кроме 🔴).\n")
	}
	return b.String(), nil
}

func renderAsk(n int, a sdb.Ask, stream string) string {
	var b strings.Builder
	red := ""
	if a.Zone == "red" {
		red = "🔴 "
	}
	fmt.Fprintf(&b, "%d. %s%s: %s\n", n, red, stream, a.Text)
	var opts []string
	_ = json.Unmarshal(a.Options, &opts)
	for i, o := range opts {
		def := ""
		if a.DefaultOption.Valid && int(a.DefaultOption.Int32) == i {
			def = " (по умолчанию)"
		}
		fmt.Fprintf(&b, "   %s) %s%s\n", OptionLabel(i), o, def)
	}
	var items []string
	_ = json.Unmarshal(a.Items, &items)
	for i, it := range items {
		fmt.Fprintf(&b, "   %d.%d %s — да/нет\n", n, i+1, it)
	}
	fmt.Fprintf(&b, "   до %s\n", fmtMSK(a.Deadline.Time))
	return b.String()
}

// promoteAsks moves the best queued questions into the shown batch, within
// the daily cap counted from Moscow midnight. A batch of approvals counts as
// one question (§7.1).
func (s *Supervisor) promoteAsks(ctx context.Context, workspaceID pgtype.UUID, now time.Time) error {
	perDay := s.AskDailyCap
	if perDay < 0 {
		perDay = 0
	}
	local := now.In(Moscow)
	midnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, Moscow)
	used, err := s.Q.CountAsksShownSince(ctx, sdb.CountAsksShownSinceParams{WorkspaceID: workspaceID, ShownAt: ts(midnight)})
	if err != nil {
		return err
	}
	left := perDay - int(used)
	if left <= 0 {
		return nil
	}
	open, err := s.Q.ListAllOpenAsks(ctx, workspaceID)
	if err != nil {
		return err
	}
	var ranked []RankedAsk
	byID := map[string]sdb.Ask{}
	for _, a := range open {
		if a.Status != "queued" {
			continue
		}
		id := uuidString(a.ID)
		byID[id] = a
		ranked = append(ranked, RankedAsk{ID: id, Value: a.Value, Cost: a.CostOfDelay, Deadline: a.Deadline.Time})
	}
	limit := MaxAsksPerBatch
	if left < limit {
		limit = left
	}
	for _, r := range TopAsks(ranked, limit) {
		a := byID[r.ID]
		if err := s.Q.MarkAskShown(ctx, a.ID); err != nil {
			return err
		}
		s.journal(ctx, a.StreamID, a.OriginStepID, "ask_shown", map[string]any{"text": a.Text})
	}
	return nil
}

func (s *Supervisor) streamPulse(ctx context.Context, st sdb.Stream, since, now time.Time) (string, int, error) {
	var b strings.Builder
	state := stateWords[st.State]
	fmt.Fprintf(&b, "%s — %s", st.Title, state)
	if st.Reason != "" {
		fmt.Fprintf(&b, ": %s", st.Reason)
	}
	if st.Until.Valid {
		fmt.Fprintf(&b, " (до %s)", fmtMSK(st.Until.Time))
	}
	b.WriteString("\n")
	ms, err := s.Q.ListMilestonesSince(ctx, sdb.ListMilestonesSinceParams{StreamID: st.ID, CreatedAt: ts(since)})
	if err != nil {
		return "", 0, err
	}
	for _, m := range ms {
		var ev []string
		_ = json.Unmarshal(m.Evidence, &ev)
		fmt.Fprintf(&b, "  ✓ %s", m.Text)
		if len(ev) > 0 {
			fmt.Fprintf(&b, " [%s]", strings.Join(ev, ", "))
		}
		b.WriteString("\n")
	}
	if len(ms) == 0 {
		b.WriteString("  " + s.lastMilestoneLine(ctx, st) + "\n")
	}
	journal, err := s.Q.ListJournalSince(ctx, sdb.ListJournalSinceParams{StreamID: st.ID, CreatedAt: ts(since)})
	if err != nil {
		return "", 0, err
	}
	counts := map[string]int{}
	silent := 0
	for _, j := range journal {
		counts[j.Event]++
		if j.Event == "orphan" {
			var d struct {
				GapSeconds float64 `json:"gap_seconds"`
			}
			if json.Unmarshal(j.Detail, &d) == nil && d.GapSeconds > SilentStopGap.Seconds() {
				silent++
			}
		}
	}
	var parts []string
	if n := counts["step_dispatched"]; n > 0 {
		parts = append(parts, fmt.Sprintf("запусков %d", n))
	}
	if n := counts["step_retry"] + counts["hang"]; n > 0 {
		parts = append(parts, fmt.Sprintf("повторов %d (починено само)", n))
	}
	if n := counts["quota"]; n > 0 {
		parts = append(parts, fmt.Sprintf("переездов по квоте %d", n))
	}
	if n := counts["step_failed"]; n > 0 {
		parts = append(parts, fmt.Sprintf("провалов %d", n))
	}
	if len(parts) > 0 {
		fmt.Fprintf(&b, "  за сутки: %s\n", strings.Join(parts, ", "))
	}
	if counts["no_ideas"] > 0 {
		fmt.Fprintf(&b, "  %s: нет идей, поговорим?\n", st.Title)
	}
	last := st.CreatedAt.Time
	if st.LastMilestoneAt.Valid {
		last = st.LastMilestoneAt.Time
	}
	if d := now.Sub(last); d >= StallAfter {
		fmt.Fprintf(&b, "  вехи нет %d дн.\n", int(d/(24*time.Hour)))
	}
	return b.String(), silent, nil
}

func (s *Supervisor) exhaustedLine(ctx context.Context, now time.Time) string {
	rows, err := s.Q.ListExecutorStatus(ctx)
	if err != nil {
		return ""
	}
	var parts []string
	for _, r := range rows {
		if r.ExhaustedUntil.Valid && r.ExhaustedUntil.Time.After(now) {
			parts = append(parts, fmt.Sprintf("%s до %s", r.Executor, r.ExhaustedUntil.Time.In(Moscow).Format("15:04")))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	sort.Strings(parts)
	return "Исчерпаны: " + strings.Join(parts, ", ")
}

// AnswerResult reports what a one-line reply did.
type AnswerResult struct {
	Applied []string `json:"applied"`
	// Rest is what the parser did not understand; it goes to Мультика.
	Rest string `json:"rest"`
}

// Answer applies a one-line reply ("1 а, 2 да, 3 покажи") to the shown batch.
func (s *Supervisor) Answer(ctx context.Context, workspaceID pgtype.UUID, line, channel string) (AnswerResult, error) {
	now := s.now()
	shown, err := s.Q.ListShownAsks(ctx, workspaceID)
	if err != nil {
		return AnswerResult{}, err
	}
	replies, rest := ParseReply(line)
	res := AnswerResult{Rest: rest}
	var unknown []string
	for _, r := range replies {
		if r.N < 1 || r.N > len(shown) {
			unknown = append(unknown, fmt.Sprintf("%d: нет такого вопроса", r.N))
			continue
		}
		a := shown[r.N-1]
		if r.Show {
			unknown = append(unknown, fmt.Sprintf("покажи вопрос %d", r.N))
			continue
		}
		var opts []string
		_ = json.Unmarshal(a.Options, &opts)
		answer := map[string]any{"channel": channel, "at": now}
		switch {
		case a.Kind == "choice" && r.Choice >= 0 && r.Choice < len(opts):
			answer["choice"] = r.Choice
		case a.Kind == "choice" && r.Yes != nil && len(opts) == 2:
			// "да/нет" on a two-way choice: да = first option.
			if *r.Yes {
				answer["choice"] = 0
			} else {
				answer["choice"] = 1
			}
		case a.Kind == "batch" && r.Yes != nil:
			answer["items"] = *r.Yes
		default:
			unknown = append(unknown, fmt.Sprintf("%d: не понял ответ", r.N))
			continue
		}
		raw, _ := json.Marshal(answer)
		if err := s.Q.ResolveAsk(ctx, sdb.ResolveAskParams{ID: a.ID, Status: "answered", Answer: raw}); err != nil {
			return res, err
		}
		s.journal(ctx, a.StreamID, a.OriginStepID, "ask_answered", map[string]any{"text": a.Text, "answer": answer})
		res.Applied = append(res.Applied, fmt.Sprintf("%d → %s", r.N, a.Text))
		// The crash question's second option is "pause the stream" (§5.1).
		if st, err := s.Q.GetStream(ctx, a.StreamID); err == nil && st.State == StateBroken && a.Zone == "red" && answer["choice"] == 1 {
			_ = s.setState(ctx, st, StatePaused, "пауза после аварии (ответ Руслана)", time.Time{})
		}
	}
	if len(unknown) > 0 {
		if res.Rest != "" {
			res.Rest += ", "
		}
		res.Rest += strings.Join(unknown, ", ")
	}
	if len(res.Applied) > 0 {
		s.Kick()
	}
	return res, nil
}

// Pause and Resume are Ruslan's one-phrase controls (§5.1). Agents must not
// call them; the HTTP layer only accepts a human actor.
func (s *Supervisor) Pause(ctx context.Context, st sdb.Stream, why string) error {
	return s.setState(ctx, st, StatePaused, firstNonEmpty(why, "пауза Руслана"), time.Time{})
}

func (s *Supervisor) Resume(ctx context.Context, st sdb.Stream) error {
	if err := s.Q.SetStreamFailStreak(ctx, sdb.SetStreamFailStreakParams{ID: st.ID, FailStreak: 0}); err != nil {
		return err
	}
	err := s.setState(ctx, st, StateRunning, "продолжить (Руслан)", time.Time{})
	s.Kick()
	return err
}

// lastMilestoneLine names the latest milestone when none landed in the
// digest window, so every stream line carries one (§7.4).
func (s *Supervisor) lastMilestoneLine(ctx context.Context, st sdb.Stream) string {
	recent, err := s.Q.ListRecentMilestones(ctx, sdb.ListRecentMilestonesParams{StreamID: st.ID, Limit: 1})
	if err != nil || len(recent) == 0 {
		return "последняя веха: пока нет"
	}
	m := recent[0]
	return fmt.Sprintf("последняя веха: %s (%s)", firstLine(m.Text, 160), fmtMSK(m.CreatedAt.Time))
}
