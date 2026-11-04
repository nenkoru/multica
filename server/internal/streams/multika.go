package streams

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/streams/sdb"
)

// Мультика: one lifelong chat for web and Telegram (§2 #8, §9.2, §13 day 6).
//
// The chat itself is an ordinary Multica chat_session of the agent named by
// Supervisor.MultikaAgentID. The fork hooks the daemon claim of that agent's
// chat turns (OnMultikaClaim): code applies what it can read without a model —
// a focus switch, a one-line answer to shown questions, routing a remark to
// the focused stream's manager — and hands Мультика a short context block
// with the streams and the focus. The model only talks.

// What the chat says when Мультика cannot answer (§3.6): never silence.
const (
	MultikaUnavailableText     = "Мультика недоступна, потоки идут. Сообщение сохранено в чате — повтори его чуть позже. Сводка в 10:00 придёт в любом случае."
	MultikaOfflineText         = "Мультика недоступна: её исполнитель сейчас не в сети. Потоки идут, сообщение сохранено и будет обработано, когда она вернётся."
	MultikaNewChatDisabledText = "Чат с Мультикой один, /new отключён. Пиши как обычно: «давай про <поток>» — нырнуть в поток, «всё» — вынырнуть."
)

// FocusIdle ends the focus after this much silence (§9.2).
const FocusIdle = 30 * time.Minute

// MultikaEnabled reports whether the fork's chat hooks are on (feature flag:
// FABRIKA_MULTIKA_AGENT_ID is set).
func (s *Supervisor) MultikaEnabled() bool { return s.MultikaAgentID.Valid }

// IsMultika reports whether agentID is Мультика.
func (s *Supervisor) IsMultika(agentID pgtype.UUID) bool {
	return s.MultikaAgentID.Valid && agentID.Valid && agentID.Bytes == s.MultikaAgentID.Bytes
}

// Focus intents read from a remark.
const (
	FocusNone  = ""
	FocusEnter = "enter"
	FocusExit  = "exit"
)

// FocusIntent is what ParseFocus read from one remark.
type FocusIntent struct {
	Kind   string
	Stream *sdb.Stream
	// Ambiguous lists the candidates when "давай про X" matched more than one
	// stream; Мультика asks back at once (§9.2).
	Ambiguous []sdb.Stream
	// Target is the raw name after "давай про", kept when nothing matched.
	Target string
}

var exitWords = map[string]bool{
	"всё": true, "все": true, "назад": true, "хватит": true, "выйди": true,
	"выход": true, "вынырни": true, "вынырнуть": true, "всё, спасибо": true,
	"все, спасибо": true, "всё спасибо": true, "все спасибо": true,
}

// explicitPrefixes name a stream for sure, so a miss is reported back to
// Мультика ("потока X нет"). After softer ones («давай по порядку»,
// «перейдём к делу») a miss is plain speech.
var explicitPrefixes = map[string]bool{
	"давай поговорим про": true, "давай про": true, "поговорим про": true,
	"переключись на": true, "переключимся на": true, "что с потоком": true, "что по потоку": true,
}

// enterPrefixes open a focus; longest first so "давай про" wins over "про".
var enterPrefixes = []string{
	"давай поговорим про", "давай поговорим о", "давай про", "давай о", "давай по",
	"поговорим про", "поговорим о", "переключись на", "переключимся на",
	"перейдём к", "перейдем к", "что с потоком", "что по потоку", "про",
}

// ParseFocus reads a focus switch from a remark. Only the start of the
// message counts: "давай про Терн, что там с нодами" enters Терн; a remark
// that merely mentions a stream does not move the focus.
func ParseFocus(text string, streams []sdb.Stream) FocusIntent {
	norm := normalizeRemark(text)
	if norm == "" {
		return FocusIntent{}
	}
	if exitWords[norm] || exitWords[strings.TrimRight(norm, " .!")] {
		return FocusIntent{Kind: FocusExit}
	}
	for _, p := range enterPrefixes {
		if !strings.HasPrefix(norm, p+" ") {
			continue
		}
		rest := strings.TrimSpace(norm[len(p):])
		words := strings.FieldsFunc(rest, func(r rune) bool { return unicode.IsSpace(r) || strings.ContainsRune(",.!?:;—-«»\"", r) })
		if len(words) == 0 {
			return FocusIntent{}
		}
		explicit := explicitPrefixes[p]
		if words[0] == "поток" || words[0] == "потоку" || words[0] == "потоке" {
			words, explicit = words[1:], true
		}
		if len(words) == 0 {
			return FocusIntent{}
		}
		var hits []sdb.Stream
		for _, st := range streams {
			if streamMatches(st, words[0]) {
				hits = append(hits, st)
			}
		}
		switch len(hits) {
		case 1:
			st := hits[0]
			return FocusIntent{Kind: FocusEnter, Stream: &st}
		case 0:
			if !explicit {
				return FocusIntent{}
			}
			return FocusIntent{Target: words[0]}
		default:
			return FocusIntent{Ambiguous: hits, Target: words[0]}
		}
	}
	return FocusIntent{}
}

func normalizeRemark(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "ё", "е")
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimRight(s, " .!?")
}

// streamMatches compares one word with the stream's slug and title words,
// tolerating Russian case endings: "терна", "терну" → "Терн"; "работу" →
// "Работа".
func streamMatches(st sdb.Stream, word string) bool {
	word = strings.ReplaceAll(strings.ToLower(word), "ё", "е")
	names := []string{st.Slug}
	for _, w := range strings.FieldsFunc(st.Title, func(r rune) bool { return unicode.IsSpace(r) || strings.ContainsRune(",.!?:;—-«»\"()", r) }) {
		names = append(names, w)
	}
	for _, n := range names {
		n = strings.ReplaceAll(strings.ToLower(n), "ё", "е")
		if n == "" {
			continue
		}
		if word == n {
			return true
		}
		stem := russianStem(n)
		if len([]rune(stem)) >= 3 && strings.HasPrefix(word, stem) && len([]rune(word))-len([]rune(stem)) <= 3 {
			return true
		}
	}
	return false
}

// russianStem drops one trailing vowel or soft sign.
func russianStem(n string) string {
	r := []rune(n)
	if len(r) > 3 && strings.ContainsRune("аяоеуюыиьй", r[len(r)-1]) {
		return string(r[:len(r)-1])
	}
	return n
}

// MultikaTurnActions is what code did with one chat turn; it is stored in
// multika_turn and shown to Мультика.
type MultikaTurnActions struct {
	Entered   string   `json:"entered,omitempty"`
	Exited    string   `json:"exited,omitempty"`
	IdleExit  string   `json:"idle_exit,omitempty"`
	Routed    string   `json:"routed,omitempty"`
	RoutedSeq int32    `json:"routed_seq,omitempty"`
	Answered  []string `json:"answered,omitempty"`
	Ambiguous []string `json:"ambiguous,omitempty"`
	Unknown   string   `json:"unknown,omitempty"`
}

// OnMultikaClaim runs when the daemon claims a chat turn. It returns the
// context block to put in front of the user's text, or "" when the turn is
// not Мультика's. Errors only degrade the context: the turn always runs.
func (s *Supervisor) OnMultikaClaim(ctx context.Context, taskID, sessionID, agentID pgtype.UUID, text string) string {
	if !s.IsMultika(agentID) || !sessionID.Valid {
		return ""
	}
	owner, err := s.Q.GetChatSessionOwner(ctx, sessionID)
	if err != nil {
		slog.Warn("streams: multika session lookup failed", "error", err)
		return ""
	}
	streams, err := s.Q.ListStreams(ctx, owner.WorkspaceID)
	if err != nil {
		slog.Warn("streams: multika streams lookup failed", "error", err)
		return ""
	}
	// The focus belongs to the one chat, not to whichever session the turn
	// came from: a web session without the Telegram binding must see the
	// same focus as the header and Telegram.
	focusKey := sessionID
	if canon, err := s.MultikaSession(ctx, owner.WorkspaceID); err == nil && canon.Valid {
		focusKey = canon
	}
	now := s.now()
	acts, focus := s.applyMultikaTurn(ctx, taskID, sessionID, focusKey, owner.WorkspaceID, text, streams, now)
	return MultikaContext(streams, focus, acts, now)
}

// MultikaFocus is the current focus of the chat, if any.
type MultikaFocus struct {
	Stream *sdb.Stream
	Until  time.Time
}

func (s *Supervisor) applyMultikaTurn(ctx context.Context, taskID, sessionID, focusKey, ws pgtype.UUID, text string, streams []sdb.Stream, now time.Time) (MultikaTurnActions, MultikaFocus) {
	var acts MultikaTurnActions
	focus := s.currentFocus(ctx, focusKey, streams)
	// A redelivered claim reuses what the first claim did.
	prev, err := s.Q.GetMultikaTurn(ctx, taskID)
	if err == nil {
		_ = json.Unmarshal(prev.Actions, &acts)
		return acts, s.liveFocus(focus, now)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		// Unknown whether this turn was applied: act on nothing rather than twice.
		slog.Warn("streams: multika turn lookup failed", "error", err)
		return acts, s.liveFocus(focus, now)
	}
	if focus.Stream != nil && !focus.Until.After(now) {
		acts.IdleExit = focus.Stream.Title
		s.leaveFocus(ctx, focusKey, *focus.Stream, "30 мин тишины")
		focus = MultikaFocus{}
	}

	answered := false
	if replies, rest := ParseReply(text); len(replies) > 0 && rest == "" {
		if shown, err := s.Q.ListShownAsks(ctx, ws); err == nil && len(shown) > 0 {
			res, err := s.Answer(ctx, ws, text, "chat")
			if err != nil {
				slog.Warn("streams: multika answer failed", "error", err)
			} else {
				acts.Answered = res.Applied
				answered = len(res.Applied) > 0
			}
		}
	}

	intent := FocusIntent{}
	if !answered {
		intent = ParseFocus(text, streams)
	}
	switch {
	case intent.Kind == FocusExit:
		if focus.Stream != nil {
			acts.Exited = focus.Stream.Title
			s.leaveFocus(ctx, focusKey, *focus.Stream, "Руслан вынырнул")
		}
		focus = MultikaFocus{}
	case intent.Kind == FocusEnter:
		if focus.Stream != nil && focus.Stream.ID.Bytes != intent.Stream.ID.Bytes {
			s.leaveFocus(ctx, focusKey, *focus.Stream, "фокус перешёл на "+intent.Stream.Title)
		}
		focus = MultikaFocus{Stream: intent.Stream, Until: now.Add(FocusIdle)}
		acts.Entered = intent.Stream.Title
	case len(intent.Ambiguous) > 0:
		for _, st := range intent.Ambiguous {
			acts.Ambiguous = append(acts.Ambiguous, st.Title)
		}
		acts.Unknown = intent.Target
	case intent.Target != "":
		acts.Unknown = intent.Target
	}

	if focus.Stream != nil && intent.Kind != FocusExit {
		focus.Until = now.Add(FocusIdle)
		if !answered {
			seq, err := s.routeRemark(ctx, focus.Stream.ID, text)
			if err != nil {
				slog.Warn("streams: multika route to manager failed", "stream", focus.Stream.Slug, "error", err)
			} else {
				acts.Routed, acts.RoutedSeq = focus.Stream.Title, seq
			}
		}
	}
	if focus.Stream != nil {
		if err := s.Q.SetChatFocus(ctx, sdb.SetChatFocusParams{ChatSessionID: focusKey, FocusStreamID: focus.Stream.ID, FocusUntil: ts(focus.Until)}); err != nil {
			slog.Warn("streams: multika set focus failed", "error", err)
		}
	}

	raw, _ := json.Marshal(acts)
	var focusID pgtype.UUID
	if focus.Stream != nil {
		focusID = focus.Stream.ID
	}
	if _, err := s.Q.InsertMultikaTurn(ctx, sdb.InsertMultikaTurnParams{TaskID: taskID, ChatSessionID: sessionID, FocusStreamID: focusID, Actions: raw}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		slog.Warn("streams: multika turn record failed", "error", err)
	}
	return acts, focus
}

// chatRemarkBrief opens a manager turn that carries Руслан's chat remarks.
const chatRemarkBrief = "Сообщения Руслана из чата (фокус на потоке):"

// routeRemark hands a remark to the stream's manager in the reserved slot.
// Remarks that arrive before that turn starts join its brief instead of
// queueing one manager turn per message.
func (s *Supervisor) routeRemark(ctx context.Context, streamID pgtype.UUID, text string) (int32, error) {
	line := "- " + strings.TrimSpace(text)
	if open, err := s.Q.ListOpenSteps(ctx, streamID); err == nil {
		for _, st := range open {
			if st.Status == StepQueued && strings.HasPrefix(st.Brief, chatRemarkBrief) {
				n, err := s.Q.AppendQueuedStepBrief(ctx, sdb.AppendQueuedStepBriefParams{ID: st.ID, Line: line})
				if err == nil && n == 1 {
					return st.Seq, nil
				}
			}
		}
	}
	step, err := s.EnqueueManage(ctx, streamID, chatRemarkBrief+"\n"+line, true)
	return step.Seq, err
}

func (s *Supervisor) liveFocus(f MultikaFocus, now time.Time) MultikaFocus {
	if f.Stream == nil || !f.Until.After(now) {
		return MultikaFocus{}
	}
	return f
}

func (s *Supervisor) currentFocus(ctx context.Context, sessionID pgtype.UUID, streams []sdb.Stream) MultikaFocus {
	row, err := s.Q.GetChatFocus(ctx, sessionID)
	if err != nil || !row.FocusStreamID.Valid {
		return MultikaFocus{}
	}
	for i := range streams {
		if streams[i].ID.Bytes == row.FocusStreamID.Bytes {
			st := streams[i]
			return MultikaFocus{Stream: &st, Until: row.FocusUntil.Time}
		}
	}
	return MultikaFocus{}
}

// leaveFocus clears the focus and asks the manager to fold the decisions
// of the conversation into the summary (§9.2 "вынырнуть").
func (s *Supervisor) leaveFocus(ctx context.Context, sessionID pgtype.UUID, st sdb.Stream, why string) {
	if err := s.Q.ClearChatFocus(ctx, sessionID); err != nil {
		slog.Warn("streams: multika clear focus failed", "error", err)
	}
	if _, err := s.EnqueueManage(ctx, st.ID, "Разговор с Русланом в чате закончился ("+why+"). Запиши принятые в нём решения в summary_patch; новых вопросов не задавай.", false); err != nil {
		slog.Warn("streams: multika leave-focus manage failed", "stream", st.Slug, "error", err)
	}
}

// SetMultikaFocus sets or clears the focus from the web header ("сейчас про:
// buvg01 ×"). slug == "" clears.
func (s *Supervisor) SetMultikaFocus(ctx context.Context, ws, sessionID pgtype.UUID, slug string) (MultikaFocus, error) {
	streams, err := s.Q.ListStreams(ctx, ws)
	if err != nil {
		return MultikaFocus{}, err
	}
	cur := s.liveFocus(s.currentFocus(ctx, sessionID, streams), s.now())
	if slug == "" {
		if cur.Stream != nil {
			s.leaveFocus(ctx, sessionID, *cur.Stream, "Руслан снял фокус")
		}
		return MultikaFocus{}, nil
	}
	for i := range streams {
		if streams[i].Slug == slug {
			st := streams[i]
			if cur.Stream != nil && cur.Stream.ID.Bytes != st.ID.Bytes {
				s.leaveFocus(ctx, sessionID, *cur.Stream, "фокус перешёл на "+st.Title)
			}
			f := MultikaFocus{Stream: &st, Until: s.now().Add(FocusIdle)}
			return f, s.Q.SetChatFocus(ctx, sdb.SetChatFocusParams{ChatSessionID: sessionID, FocusStreamID: st.ID, FocusUntil: ts(f.Until)})
		}
	}
	return MultikaFocus{}, fmt.Errorf("stream %q not found", slug)
}

// MultikaSession resolves the one chat: the session Telegram routes the
// direct chat to, else Мультика's latest web session.
func (s *Supervisor) MultikaSession(ctx context.Context, ws pgtype.UUID) (pgtype.UUID, error) {
	id, err := s.Q.MultikaChannelSession(ctx, sdb.MultikaChannelSessionParams{AgentID: s.MultikaAgentID, WorkspaceID: ws})
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, err
	}
	return s.Q.LatestAgentChatSession(ctx, sdb.LatestAgentChatSessionParams{AgentID: s.MultikaAgentID, WorkspaceID: ws})
}

// MultikaFocusOf returns the live focus of a chat session.
func (s *Supervisor) MultikaFocusOf(ctx context.Context, ws, sessionID pgtype.UUID) MultikaFocus {
	streams, err := s.Q.ListStreams(ctx, ws)
	if err != nil {
		return MultikaFocus{}
	}
	return s.liveFocus(s.currentFocus(ctx, sessionID, streams), s.now())
}

// MaxSummaryInContext bounds the summary handed to Мультика (§11: ≤1500
// tokens is the manager's budget; Мультика gets a slice).
const MaxSummaryInContext = 1500

// MultikaContext renders the block Мультика reads before the user's text.
// Code-built and short: the streams, the focus, what code already did.
func MultikaContext(streams []sdb.Stream, focus MultikaFocus, acts MultikaTurnActions, now time.Time) string {
	var b strings.Builder
	b.WriteString("[Фабрика — служебный контекст для Мультики. Руслан его не видит, не цитируй.]\n")
	b.WriteString("Ты — Мультика, единственный чат Руслана в вебе и Telegram. Новых чатов нет, /new отключён. Доску Руслану не предлагай: всё нужное говори здесь.\n")
	active := 0
	for _, st := range streams {
		if st.Active {
			active++
		}
	}
	fmt.Fprintf(&b, "Потоки (%d из 5 активны):\n", active)
	for _, st := range streams {
		if !st.Active {
			continue
		}
		line := fmt.Sprintf("- %s (%s) — %s", st.Title, st.Slug, stateWords[st.State])
		if st.Reason != "" {
			line += ": " + firstLine(st.Reason, 120)
		}
		if st.Until.Valid {
			line += " (до " + st.Until.Time.In(Moscow).Format("02.01 15:04") + " МСК)"
		}
		b.WriteString(line + "\n")
	}
	if focus.Stream != nil {
		st := focus.Stream
		c, _ := ParseCharter(st.Charter)
		fmt.Fprintf(&b, "Фокус: %s до %s МСК (30 мин тишины или «всё» — выход). Отвечай за управляющего этого потока: по уставу и summary, можешь возразить с аргументом.\n", st.Title, focus.Until.In(Moscow).Format("15:04"))
		fmt.Fprintf(&b, "Устав: цель — %s; метрика — %s; готово — %s.\n", c.Goal, c.Metric, c.Done)
		if sum := strings.TrimSpace(st.Summary); sum != "" {
			r := []rune(sum)
			if len(r) > MaxSummaryInContext {
				sum = string(r[:MaxSummaryInContext]) + "…"
			}
			b.WriteString("Summary потока:\n" + sum + "\n")
		}
	} else {
		b.WriteString("Фокуса нет. Если реплика явно про один поток, отвечай по нему; если непонятно, про какой, — сразу переспроси одним вопросом.\n")
	}
	var done []string
	if acts.IdleExit != "" {
		done = append(done, "фокус на «"+acts.IdleExit+"» снят после 30 мин тишины")
	}
	if acts.Exited != "" {
		done = append(done, "Руслан вынырнул из «"+acts.Exited+"», управляющий запишет решения в summary")
	}
	if acts.Entered != "" {
		done = append(done, "фокус поставлен на «"+acts.Entered+"»")
	}
	if acts.Routed != "" {
		done = append(done, fmt.Sprintf("реплика передана управляющему «%s» (шаг %d, резервный слот)", acts.Routed, acts.RoutedSeq))
	}
	if len(acts.Answered) > 0 {
		done = append(done, "ответы на вопросы приняты: "+strings.Join(acts.Answered, "; "))
	}
	if len(acts.Ambiguous) > 0 {
		done = append(done, "«"+acts.Unknown+"» подходит к нескольким потокам ("+strings.Join(acts.Ambiguous, ", ")+") — переспроси, про какой")
	} else if acts.Unknown != "" {
		done = append(done, "потока «"+acts.Unknown+"» нет — уточни, о каком речь, или предложи завести новый")
	}
	if len(done) > 0 {
		b.WriteString("Код уже сделал: " + strings.Join(done, "; ") + ". Подтверди Руслану коротко.\n")
	}
	b.WriteString("[Конец контекста. Ниже — сообщение Руслана.]")
	return b.String()
}
