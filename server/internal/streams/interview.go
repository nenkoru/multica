package streams

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/streams/sdb"
)

// A new stream from the interview (§9.3, §13 day 8). Мультика asks all the
// taste and scope questions at once; once Руслан says yes to the assembled
// charter she ends her reply with one fenced fabrika-charter block. Code, not
// the model, creates the stream from it: the block is validated, executors
// and the manager come from a template stream (FABRIKA_STREAM_TEMPLATE), and
// the outcome reaches Мультика's next context so she reports what happened.

// MaxActiveStreams is how many streams run at once (§3, §9.3).
const MaxActiveStreams = 5

// CharterDraft is the fabrika-charter block.
type CharterDraft struct {
	Slug    string  `json:"slug"`
	Title   string  `json:"title"`
	Charter Charter `json:"charter"`
	// Pause names an active stream to deactivate when five are already
	// active; Мультика fills it only after Руслан chose whom to pause.
	Pause string `json:"pause,omitempty"`
}

var charterFence = regexp.MustCompile("(?s)```fabrika-charter\\s*(\\{.*?\\})\\s*```")

// ParseCharterBlock returns the last fabrika-charter block of a reply, or nil
// when there is none.
func ParseCharterBlock(content string) (*CharterDraft, error) {
	m := charterFence.FindAllStringSubmatch(content, -1)
	if len(m) == 0 {
		return nil, nil
	}
	var d CharterDraft
	dec := json.NewDecoder(strings.NewReader(m[len(m)-1][1]))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return &d, fmt.Errorf("блок устава не читается: %v", err)
	}
	return &d, nil
}

// Validate checks what a stream cannot start without (§3 p.2, §9.3).
func (d CharterDraft) Validate() error {
	var missing []string
	if strings.TrimSpace(d.Title) == "" {
		missing = append(missing, "title")
	}
	c := d.Charter
	for _, f := range []struct{ name, v string }{{"goal", c.Goal}, {"metric", c.Metric}, {"done", c.Done}, {"mandate", c.Mandate}} {
		if strings.TrimSpace(f.v) == "" {
			missing = append(missing, f.name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("в уставе не заполнено: %s", strings.Join(missing, ", "))
	}
	if !streamSlug.MatchString(d.Slug) || reservedSlugs[d.Slug] {
		return fmt.Errorf("slug «%s» не подходит: латиница, цифры и дефис, не служебное имя", d.Slug)
	}
	if len(c.Chains) > 0 {
		// Executors are the fleet's business, not the interview's.
		return errors.New("цепочки исполнителей в уставе из интервью не задаются")
	}
	return nil
}

// OnMultikaReply runs when Мультика's chat turn finishes. A reply without a
// charter block is ignored. Errors are recorded for Мультика, never returned:
// the chat is not on the critical path.
func (s *Supervisor) OnMultikaReply(ctx context.Context, sessionID, taskID pgtype.UUID, content string) {
	if !s.MultikaEnabled() || !sessionID.Valid || !taskID.Valid || !strings.Contains(content, "```fabrika-charter") {
		return
	}
	owner, err := s.Q.GetChatSessionOwner(ctx, sessionID)
	if err != nil || !s.IsMultika(owner.AgentID) {
		return
	}
	draft, perr := ParseCharterBlock(content)
	if draft == nil && perr == nil {
		return
	}
	var created *sdb.Stream
	err = pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := s.Q.WithTx(tx)
		if _, err := q.ClaimMultikaCharter(ctx, sdb.ClaimMultikaCharterParams{TaskID: taskID, ChatSessionID: sessionID, WorkspaceID: owner.WorkspaceID}); err != nil {
			// ErrNoRows: this turn was handled already.
			return err
		}
		st, why, err := s.createFromDraft(ctx, q, owner.WorkspaceID, draft, perr)
		if err != nil {
			return err
		}
		var id pgtype.UUID
		if st != nil {
			id, created = st.ID, st
		}
		return q.FinishMultikaCharter(ctx, sdb.FinishMultikaCharterParams{TaskID: taskID, Slug: draft.Slug, StreamID: id, Error: why})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return
	}
	if err != nil {
		slog.Warn("streams: interview charter failed", "error", err)
		return
	}
	if created == nil {
		return
	}
	s.journal(ctx, created.ID, pgtype.UUID{}, "interview_created", map[string]any{"title": created.Title})
	focusKey := sessionID
	if canon, err := s.MultikaSession(ctx, owner.WorkspaceID); err == nil && canon.Valid {
		focusKey = canon
	}
	if _, err := s.SetMultikaFocus(ctx, owner.WorkspaceID, focusKey, created.Slug); err != nil {
		slog.Warn("streams: interview focus failed", "error", err)
	}
	// §9.3: the manager's first turn is queued at once.
	s.Kick()
}

// createFromDraft creates the stream or returns why not (why is for Мультика;
// err is a database failure).
func (s *Supervisor) createFromDraft(ctx context.Context, q *sdb.Queries, ws pgtype.UUID, d *CharterDraft, perr error) (*sdb.Stream, string, error) {
	if perr != nil {
		return nil, perr.Error(), nil
	}
	if err := d.Validate(); err != nil {
		return nil, err.Error(), nil
	}
	if s.StreamTemplate == "" {
		return nil, "не задан шаблон исполнителей (FABRIKA_STREAM_TEMPLATE) — поток не заведён, это вопрос к Mika", nil
	}
	tpl, err := q.GetStreamBySlug(ctx, sdb.GetStreamBySlugParams{WorkspaceID: ws, Slug: s.StreamTemplate})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "шаблон исполнителей «" + s.StreamTemplate + "» не найден — поток не заведён, это вопрос к Mika", nil
	}
	if err != nil {
		return nil, "", err
	}
	tc, err := ParseCharter(tpl.Charter)
	if err != nil {
		return nil, "", fmt.Errorf("template charter: %w", err)
	}
	streams, err := q.ListStreams(ctx, ws)
	if err != nil {
		return nil, "", err
	}
	active := 0
	var pause *sdb.Stream
	for i, st := range streams {
		if st.Slug == d.Slug {
			return nil, "поток «" + d.Slug + "» уже есть — правка устава идёт отдельно", nil
		}
		if st.Active {
			active++
			if d.Pause != "" && st.Slug == d.Pause {
				pause = &streams[i]
			}
		}
	}
	if d.Pause != "" && pause == nil {
		return nil, "активного потока «" + d.Pause + "» нет — уточни у Руслана, кого на паузу", nil
	}
	if pause != nil {
		if err := q.SetStreamActive(ctx, sdb.SetStreamActiveParams{ID: pause.ID, Active: false}); err != nil {
			return nil, "", err
		}
		if err := appendJournal(ctx, q, pause.ID, pgtype.UUID{}, "active", map[string]any{"active": false, "why": "место для «" + d.Title + "»"}); err != nil {
			return nil, "", err
		}
		active--
	}
	if active >= MaxActiveStreams {
		return nil, fmt.Sprintf("активных уже %d — спроси Руслана, кого на паузу, и повтори блок устава с полем pause", MaxActiveStreams), nil
	}
	c := d.Charter
	c.Chains = tc.Chains
	raw, _ := json.Marshal(c)
	st, err := q.CreateStream(ctx, sdb.CreateStreamParams{
		WorkspaceID: ws, Slug: d.Slug, Title: strings.TrimSpace(d.Title), Charter: raw,
		Weight: tpl.Weight, MinShare: tpl.MinShare, MaxParallel: 1,
		ManagerAgentID: tpl.ManagerAgentID, CarrierProjectID: tpl.CarrierProjectID,
		Active: true,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "поток «" + d.Slug + "» уже есть — правка устава идёт отдельно", nil
	}
	if err != nil {
		return nil, "", err
	}
	if err := appendJournal(ctx, q, st.ID, pgtype.UUID{}, "charter", map[string]any{"title": st.Title, "from": "interview"}); err != nil {
		return nil, "", err
	}
	return &st, "", nil
}

// takeCharterOutcomes returns the interview outcomes Мультика has not
// reported yet, as lines for her context.
func (s *Supervisor) takeCharterOutcomes(ctx context.Context, ws pgtype.UUID) []string {
	rows, err := s.Q.TakeMultikaCharters(ctx, ws)
	if err != nil {
		slog.Warn("streams: interview outcomes lookup failed", "error", err)
		return nil
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].CreatedAt.Time.Before(rows[j].CreatedAt.Time) })
	var out []string
	for _, r := range rows {
		switch {
		case r.Error != "":
			out = append(out, "поток из интервью НЕ заведён: "+r.Error+" — скажи Руслану честно и поправь")
		case r.StreamID.Valid:
			st, err := s.Q.GetStream(ctx, r.StreamID)
			if err != nil {
				continue
			}
			out = append(out, "поток «"+st.Title+"» ("+st.Slug+") заведён по уставу, первый ход управляющего поставлен, фокус на нём")
		}
	}
	return out
}
