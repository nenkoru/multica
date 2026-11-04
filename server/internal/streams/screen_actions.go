package streams

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/streams/genui"
	"github.com/multica-ai/multica/server/internal/streams/sdb"
)

// Screen buttons (§10.4). A press names a ui_action record created when
// the screen was finalized, so nothing works before ui.final, every action
// has its own id, and a repeated press returns the first result. Red-zone
// actions wait for the app's own confirmation window, built here from the
// record — never from the generated screen.

// Press outcomes.
const (
	PressDone      = "done"
	PressConfirm   = "confirm"   // red zone: show the confirmation window
	PressAlready   = "already"   // the action ran before; result repeated
	PressCancelled = "cancelled" // the confirmation was declined
)

// ErrActionStale is a press on an action that can no longer run.
var ErrActionStale = errors.New("кнопка устарела")

// ConfirmView is the confirmation window of a red action. Window names it:
// the confirming press must name the window it answers.
type ConfirmView struct {
	Window string `json:"window"`
	Title  string `json:"title"`
	Text   string `json:"text"`
}

// PressResult is what a press did.
type PressResult struct {
	Status  string         `json:"status"`
	Confirm *ConfirmView   `json:"confirm,omitempty"`
	Result  map[string]any `json:"result,omitempty"`
}

// confirmWindow is a red action's open window, kept in ui_action.result.
type confirmWindow struct {
	Window string `json:"window"`
	Text   string `json:"text"`
}

// PressScreenAction runs a screen button of the user's own chat. A red
// action runs only when the press confirms the window the server opened
// last and the window's text still describes the stream as stored now;
// otherwise a new window opens.
func (s *Supervisor) PressScreenAction(ctx context.Context, ws, user, actionID pgtype.UUID, confirm bool, window string) (PressResult, error) {
	ua, act, err := s.screenAction(ctx, ws, user, actionID)
	if err != nil {
		return PressResult{}, err
	}
	switch ua.Status {
	case "done":
		return alreadyResult(ua.Result), nil
	case "pending", "confirming":
	default:
		return PressResult{}, ErrActionStale
	}
	if ua.Zone == "red" {
		text, err := s.describeAction(ctx, ws, act.Action)
		if err != nil {
			return PressResult{}, err
		}
		var open confirmWindow
		if ua.Status == "confirming" {
			_ = json.Unmarshal(ua.Result, &open)
		}
		if !confirm || window == "" || open.Window != window || open.Text != text {
			return s.openConfirmWindow(ctx, ua.ID, text)
		}
	}
	switch act.Action.Name {
	case "set_share", "raise_budget":
		return s.applyShareAction(ctx, ws, ua, act.Action)
	}
	// The other actions set a state or are guarded by their record (an ask
	// is answered once), so they apply first and the action closes after:
	// a crash in between leaves the button pressable, never done-but-not-run.
	res, err := s.applyScreenAction(ctx, ws, ua, act.Action)
	if err != nil {
		return PressResult{}, err
	}
	raw, _ := json.Marshal(res)
	claimed, err := s.Q.FinishUIActionAny(ctx, sdb.FinishUIActionAnyParams{ID: ua.ID, Status: "done", Result: raw})
	if err != nil {
		return PressResult{}, err
	}
	if claimed == 0 {
		return s.alreadyPressed(ctx, ua.ID)
	}
	if act.Action.Name == "answer_ask" {
		// The question's other buttons (Telegram, other screens) end now.
		_ = s.Q.ExpireAskActions(ctx, ua.AskID)
		s.Kick()
	}
	return PressResult{Status: PressDone, Result: res}, nil
}

func (s *Supervisor) openConfirmWindow(ctx context.Context, id pgtype.UUID, text string) (PressResult, error) {
	tok, err := newAskToken()
	if err != nil {
		return PressResult{}, err
	}
	raw, _ := json.Marshal(confirmWindow{Window: tok, Text: text})
	n, err := s.Q.ConfirmUIAction(ctx, sdb.ConfirmUIActionParams{ID: id, ConfirmWindow: raw})
	if err != nil {
		return PressResult{}, err
	}
	if n == 0 {
		return s.alreadyPressed(ctx, id)
	}
	return PressResult{Status: PressConfirm, Confirm: &ConfirmView{Window: tok, Title: "Красная зона: подтверди", Text: text}}, nil
}

// applyShareAction changes a stream's money share and closes the action in
// one transaction: either both happened or neither.
func (s *Supervisor) applyShareAction(ctx context.Context, ws pgtype.UUID, ua sdb.UiAction, a genui.Action) (PressResult, error) {
	var (
		res     map[string]any
		st      sdb.Stream
		journal string
		details map[string]any
		lost    bool
	)
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := s.Q.WithTx(tx)
		n, err := q.FinishUIActionAny(ctx, sdb.FinishUIActionAnyParams{ID: ua.ID, Status: "done", Result: []byte("{}")})
		if err != nil {
			return err
		}
		if n == 0 {
			lost = true
			return nil
		}
		slug, _ := a.Args["stream"].(string)
		st, err = q.GetStreamBySlug(ctx, sdb.GetStreamBySlugParams{WorkspaceID: ws, Slug: slug})
		if err != nil {
			return fmt.Errorf("потока %q нет", slug)
		}
		var share float64
		if a.Name == "set_share" {
			f, _ := a.Args["factor"].(float64)
			share, details, err = s.shareTo(ctx, q, st, f)
			journal = "share_set"
			res = map[string]any{"message": fmt.Sprintf("Доля «%s»: %s потолка", st.Title, fmtShare(share)), "budget_share": share}
		} else {
			usd, _ := a.Args["usd"].(float64)
			share, details, err = s.raiseTo(ctx, q, st, usd)
			journal = "budget_raised"
			res = map[string]any{"message": fmt.Sprintf("Бюджет «%s»: $%.2f в месяц", st.Title, share*s.APIMonthlyUSD), "budget_share": share}
		}
		if err != nil {
			return err
		}
		raw, _ := json.Marshal(res)
		return q.SetUIActionResult(ctx, sdb.SetUIActionResultParams{ID: ua.ID, Result: raw})
	})
	if err != nil {
		return PressResult{}, err
	}
	if lost {
		return s.alreadyPressed(ctx, ua.ID)
	}
	details["via"] = "экран"
	s.journal(ctx, st.ID, pgtype.UUID{}, journal, details)
	return PressResult{Status: PressDone, Result: res}, nil
}

func (s *Supervisor) alreadyPressed(ctx context.Context, id pgtype.UUID) (PressResult, error) {
	cur, err := s.Q.GetUIAction(ctx, id)
	if err != nil {
		return PressResult{}, err
	}
	if cur.Status != "done" {
		return PressResult{}, ErrActionStale
	}
	return alreadyResult(cur.Result), nil
}

func alreadyResult(raw []byte) PressResult {
	var res map[string]any
	_ = json.Unmarshal(raw, &res)
	return PressResult{Status: PressAlready, Result: res}
}

// CancelScreenAction declines a red action's confirmation; the button can
// be pressed again and the closed window no longer confirms.
func (s *Supervisor) CancelScreenAction(ctx context.Context, ws, user, actionID pgtype.UUID) (PressResult, error) {
	ua, _, err := s.screenAction(ctx, ws, user, actionID)
	if err != nil {
		return PressResult{}, err
	}
	if _, err := s.Q.CancelUIActionConfirm(ctx, ua.ID); err != nil {
		return PressResult{}, err
	}
	return PressResult{Status: PressCancelled}, nil
}

// screenAction loads a screen button of the user's own chat; anything else
// is stale, so a foreign chat's buttons are indistinguishable from none.
func (s *Supervisor) screenAction(ctx context.Context, ws, user, actionID pgtype.UUID) (sdb.UiAction, screenAction, error) {
	ua, err := s.Q.GetUIAction(ctx, actionID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !ua.UiBlockID.Valid) {
		return sdb.UiAction{}, screenAction{}, ErrActionStale
	}
	if err != nil {
		return sdb.UiAction{}, screenAction{}, err
	}
	b, err := s.Q.GetUIBlock(ctx, ua.UiBlockID)
	if err != nil || b.WorkspaceID != ws || !s.ownsChat(ctx, ws, user, b.ChatSessionID) {
		return sdb.UiAction{}, screenAction{}, ErrActionStale
	}
	var act screenAction
	if err := json.Unmarshal(ua.Action, &act); err != nil || act.Kind != "screen" {
		return sdb.UiAction{}, screenAction{}, ErrActionStale
	}
	return ua, act, nil
}

// ownsChat: the chat is the user's own in this workspace.
func (s *Supervisor) ownsChat(ctx context.Context, ws, user, sessionID pgtype.UUID) bool {
	if !user.Valid || !sessionID.Valid {
		return false
	}
	_, err := s.Q.ChatSessionOfUser(ctx, sdb.ChatSessionOfUserParams{ID: sessionID, WorkspaceID: ws, UserID: user})
	return err == nil
}

func (s *Supervisor) streamArg(ctx context.Context, ws pgtype.UUID, a genui.Action) (sdb.Stream, error) {
	slug, _ := a.Args["stream"].(string)
	st, err := s.Q.GetStreamBySlug(ctx, sdb.GetStreamBySlugParams{WorkspaceID: ws, Slug: slug})
	if err != nil {
		return sdb.Stream{}, fmt.Errorf("потока %q нет", slug)
	}
	return st, nil
}

// describeAction is the confirmation text, from the action record and the
// stream as stored now.
func (s *Supervisor) describeAction(ctx context.Context, ws pgtype.UUID, a genui.Action) (string, error) {
	switch a.Name {
	case "raise_budget", "set_share":
		st, err := s.streamArg(ctx, ws, a)
		if err != nil {
			return "", err
		}
		share := charterShare(st.Charter)
		if a.Name == "set_share" {
			f, _ := a.Args["factor"].(float64)
			return fmt.Sprintf("Умножить долю потока «%s» на %s: %s → %s месячного потолка API.", st.Title, fmtFactor(f), fmtShare(share), fmtShare(clampShare(share*f))), nil
		}
		usd, _ := a.Args["usd"].(float64)
		if s.APIMonthlyUSD <= 0 {
			return "", fmt.Errorf("месячный потолок API не задан — поднимать нечего")
		}
		next := share + usd/s.APIMonthlyUSD
		if next > 1 {
			return "", fmt.Errorf("доля потока превысила бы весь потолок API")
		}
		return fmt.Sprintf("Поднять бюджет потока «%s» на $%.2f в месяц: $%.2f → $%.2f из потолка $%.0f.", st.Title, usd, share*s.APIMonthlyUSD, next*s.APIMonthlyUSD, s.APIMonthlyUSD), nil
	case "answer_ask":
		id, _ := a.Args["ask_id"].(string)
		ask, err := s.askInWorkspace(ctx, ws, id)
		if err != nil {
			return "", err
		}
		var opts []string
		_ = json.Unmarshal(ask.Options, &opts)
		n := int(a.Args["choice"].(float64))
		if n < 0 || n >= len(opts) {
			return "", fmt.Errorf("варианта %d нет", n)
		}
		return fmt.Sprintf("Ответ на вопрос «%s»: %s) %s.", firstLine(ask.Text, 120), OptionLabel(n), opts[n]), nil
	}
	return fmt.Sprintf("Действие %s.", a.Name), nil
}

func (s *Supervisor) applyScreenAction(ctx context.Context, ws pgtype.UUID, ua sdb.UiAction, a genui.Action) (map[string]any, error) {
	switch a.Name {
	case "open":
		return map[string]any{"open": a.Args["url"]}, nil
	case "followup":
		// The web sends it as Руслан's remark marked as coming from a screen.
		return map[string]any{"followup": a.Args["text"]}, nil
	case "focus_stream":
		sid, err := s.MultikaSession(ctx, ws)
		if err != nil {
			return nil, fmt.Errorf("чата с Мультикой ещё нет")
		}
		slug, _ := a.Args["stream"].(string)
		f, err := s.SetMultikaFocus(ctx, ws, sid, slug)
		if err != nil {
			return nil, err
		}
		return map[string]any{"message": "Сейчас про: " + f.Stream.Title}, nil
	case "pause_stream", "resume_stream":
		st, err := s.streamArg(ctx, ws, a)
		if err != nil {
			return nil, err
		}
		if a.Name == "pause_stream" {
			if err := s.Pause(ctx, st, "пауза с экрана (Руслан)"); err != nil {
				return nil, err
			}
			return map[string]any{"message": "«" + st.Title + "» на паузе"}, nil
		}
		if err := s.Resume(ctx, st); err != nil {
			return nil, err
		}
		return map[string]any{"message": "«" + st.Title + "» продолжает"}, nil
	case "answer_ask":
		if !ua.AskID.Valid {
			return nil, ErrActionStale
		}
		ask, err := s.Q.GetAsk(ctx, ua.AskID)
		if err != nil {
			return nil, err
		}
		n := int(a.Args["choice"].(float64))
		out, err := s.applyReply(ctx, ask, Reply{Choice: n}, "screen", s.now())
		if err != nil {
			return nil, err
		}
		if out != replyApplied {
			return nil, fmt.Errorf("вопрос уже закрыт")
		}
		return map[string]any{"message": "Ответ принят"}, nil
	}
	return nil, fmt.Errorf("действия %q нет", a.Name)
}

// SetShare multiplies a stream's share (§6.7): the money share of the
// monthly API ceiling and the slot guarantee move together. Reducing is a
// normal-zone action, growing is red (§10.4); callers check the zone.
func (s *Supervisor) SetShare(ctx context.Context, st sdb.Stream, factor float64, via string) (float64, error) {
	next, details, err := s.shareTo(ctx, s.Q, st, factor)
	if err != nil {
		return 0, err
	}
	details["via"] = via
	s.journal(ctx, st.ID, pgtype.UUID{}, "share_set", details)
	return next, nil
}

func (s *Supervisor) shareTo(ctx context.Context, q *sdb.Queries, st sdb.Stream, factor float64) (float64, map[string]any, error) {
	if factor <= 0 || factor > genui.MaxShareFactor {
		return 0, nil, fmt.Errorf("множитель доли от 0 до %d", genui.MaxShareFactor)
	}
	old := charterShare(st.Charter)
	next := clampShare(old * factor)
	minF, _ := st.MinShare.Float64Value()
	minNext := math.Round(math.Min(1, minF.Float64*factor)*1000) / 1000
	if err := writeShare(ctx, q, st, next, minNext); err != nil {
		return 0, nil, err
	}
	return next, map[string]any{"factor": factor, "budget_share_from": old, "budget_share": next, "min_share_from": minF.Float64, "min_share": minNext}, nil
}

// RaiseBudget adds usd a month to a stream's share of the API ceiling. Red
// zone only: never called without Руслан's confirmation (§14: no automatic
// raise of the money ceiling).
func (s *Supervisor) RaiseBudget(ctx context.Context, st sdb.Stream, usd float64, via string) (float64, error) {
	next, details, err := s.raiseTo(ctx, s.Q, st, usd)
	if err != nil {
		return 0, err
	}
	details["via"] = via
	s.journal(ctx, st.ID, pgtype.UUID{}, "budget_raised", details)
	return next, nil
}

func (s *Supervisor) raiseTo(ctx context.Context, q *sdb.Queries, st sdb.Stream, usd float64) (float64, map[string]any, error) {
	if s.APIMonthlyUSD <= 0 {
		return 0, nil, fmt.Errorf("месячный потолок API не задан")
	}
	if usd <= 0 || usd > genui.MaxRaiseUSD {
		return 0, nil, fmt.Errorf("сумма от 0 до %d", genui.MaxRaiseUSD)
	}
	old := charterShare(st.Charter)
	next := old + usd/s.APIMonthlyUSD
	if next > 1 {
		return 0, nil, fmt.Errorf("доля потока превысила бы весь потолок API")
	}
	next = math.Round(next*10000) / 10000
	minF, _ := st.MinShare.Float64Value()
	if err := writeShare(ctx, q, st, next, minF.Float64); err != nil {
		return 0, nil, err
	}
	return next, map[string]any{"usd": usd, "budget_share_from": old, "budget_share": next}, nil
}

// writeShare keeps every other charter field as stored.
func writeShare(ctx context.Context, q *sdb.Queries, st sdb.Stream, share, minShare float64) error {
	doc := map[string]any{}
	if len(st.Charter) > 0 {
		if err := json.Unmarshal(st.Charter, &doc); err != nil {
			return fmt.Errorf("устав потока не читается: %w", err)
		}
	}
	doc["budget_share"] = share
	raw, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	var n pgtype.Numeric
	if err := n.Scan(strconv.FormatFloat(minShare, 'f', 3, 64)); err != nil {
		return err
	}
	return q.SetStreamShare(ctx, sdb.SetStreamShareParams{ID: st.ID, Charter: raw, MinShare: n})
}

func charterShare(raw []byte) float64 {
	c, _ := ParseCharter(raw)
	return c.BudgetShare
}

func clampShare(f float64) float64 { return math.Round(math.Min(1, math.Max(0, f))*10000) / 10000 }

func fmtShare(f float64) string { return strconv.FormatFloat(f*100, 'f', -1, 64) + "%" }

func fmtFactor(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
