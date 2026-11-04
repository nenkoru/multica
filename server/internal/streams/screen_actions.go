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

// ConfirmView is the confirmation window of a red action.
type ConfirmView struct {
	Title string `json:"title"`
	Text  string `json:"text"`
}

// PressResult is what a press did.
type PressResult struct {
	Status  string         `json:"status"`
	Confirm *ConfirmView   `json:"confirm,omitempty"`
	Result  map[string]any `json:"result,omitempty"`
}

// PressScreenAction runs a screen button. confirm is the answer of the
// confirmation window; a red action without it only opens the window.
func (s *Supervisor) PressScreenAction(ctx context.Context, ws, actionID pgtype.UUID, confirm bool) (PressResult, error) {
	ua, act, err := s.screenAction(ctx, ws, actionID)
	if err != nil {
		return PressResult{}, err
	}
	switch ua.Status {
	case "done":
		var res map[string]any
		_ = json.Unmarshal(ua.Result, &res)
		return PressResult{Status: PressAlready, Result: res}, nil
	case "pending", "confirming":
	default:
		return PressResult{}, ErrActionStale
	}
	if ua.Zone == "red" {
		if !confirm {
			text, err := s.describeAction(ctx, ws, act.Action)
			if err != nil {
				return PressResult{}, err
			}
			if ua.Status == "pending" {
				if _, err := s.Q.ConfirmUIAction(ctx, ua.ID); err != nil {
					return PressResult{}, err
				}
			}
			return PressResult{Status: PressConfirm, Confirm: &ConfirmView{Title: "Красная зона: подтверди", Text: text}}, nil
		}
		if ua.Status != "confirming" {
			// Confirmation only through the window the server opened.
			return PressResult{}, fmt.Errorf("сначала нажми кнопку, затем подтверди в окне")
		}
	}
	claimed, err := s.Q.FinishUIActionAny(ctx, sdb.FinishUIActionAnyParams{ID: ua.ID, Status: "done", Result: []byte("{}")})
	if err != nil {
		return PressResult{}, err
	}
	if claimed == 0 {
		cur, err := s.Q.GetUIAction(ctx, ua.ID)
		if err != nil {
			return PressResult{}, err
		}
		var res map[string]any
		_ = json.Unmarshal(cur.Result, &res)
		return PressResult{Status: PressAlready, Result: res}, nil
	}
	res, err := s.applyScreenAction(ctx, ws, ua, act.Action)
	if err != nil {
		_ = s.Q.ReleaseUIAction(ctx, ua.ID)
		return PressResult{}, err
	}
	raw, _ := json.Marshal(res)
	_ = s.Q.SetUIActionResult(ctx, sdb.SetUIActionResultParams{ID: ua.ID, Result: raw})
	return PressResult{Status: PressDone, Result: res}, nil
}

// CancelScreenAction declines a red action's confirmation; the button can
// be pressed again.
func (s *Supervisor) CancelScreenAction(ctx context.Context, ws, actionID pgtype.UUID) (PressResult, error) {
	ua, _, err := s.screenAction(ctx, ws, actionID)
	if err != nil {
		return PressResult{}, err
	}
	if _, err := s.Q.CancelUIActionConfirm(ctx, ua.ID); err != nil {
		return PressResult{}, err
	}
	return PressResult{Status: PressCancelled}, nil
}

func (s *Supervisor) screenAction(ctx context.Context, ws, actionID pgtype.UUID) (sdb.UiAction, screenAction, error) {
	ua, err := s.Q.GetUIAction(ctx, actionID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !ua.UiBlockID.Valid) {
		return sdb.UiAction{}, screenAction{}, ErrActionStale
	}
	if err != nil {
		return sdb.UiAction{}, screenAction{}, err
	}
	b, err := s.Q.GetUIBlock(ctx, ua.UiBlockID)
	if err != nil || b.WorkspaceID != ws {
		return sdb.UiAction{}, screenAction{}, ErrActionStale
	}
	var act screenAction
	if err := json.Unmarshal(ua.Action, &act); err != nil || act.Kind != "screen" {
		return sdb.UiAction{}, screenAction{}, ErrActionStale
	}
	return ua, act, nil
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
	case "set_share":
		st, err := s.streamArg(ctx, ws, a)
		if err != nil {
			return nil, err
		}
		f, _ := a.Args["factor"].(float64)
		share, err := s.SetShare(ctx, st, f, "экран")
		if err != nil {
			return nil, err
		}
		return map[string]any{"message": fmt.Sprintf("Доля «%s»: %s потолка", st.Title, fmtShare(share)), "budget_share": share}, nil
	case "raise_budget":
		st, err := s.streamArg(ctx, ws, a)
		if err != nil {
			return nil, err
		}
		usd, _ := a.Args["usd"].(float64)
		share, err := s.RaiseBudget(ctx, st, usd, "экран")
		if err != nil {
			return nil, err
		}
		return map[string]any{"message": fmt.Sprintf("Бюджет «%s»: $%.2f в месяц", st.Title, share*s.APIMonthlyUSD), "budget_share": share}, nil
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
		_ = s.Q.ExpireAskActions(ctx, ask.ID)
		s.Kick()
		return map[string]any{"message": "Ответ принят"}, nil
	}
	return nil, fmt.Errorf("действия %q нет", a.Name)
}

// SetShare multiplies a stream's share (§6.7): the money share of the
// monthly API ceiling and the slot guarantee move together. Reducing is a
// normal-zone action, growing is red (§10.4); callers check the zone.
func (s *Supervisor) SetShare(ctx context.Context, st sdb.Stream, factor float64, via string) (float64, error) {
	if factor <= 0 || factor > genui.MaxShareFactor {
		return 0, fmt.Errorf("множитель доли от 0 до %d", genui.MaxShareFactor)
	}
	old := charterShare(st.Charter)
	next := clampShare(old * factor)
	minF, _ := st.MinShare.Float64Value()
	minNext := math.Round(math.Min(1, minF.Float64*factor)*1000) / 1000
	if err := s.writeShare(ctx, st, next, minNext); err != nil {
		return 0, err
	}
	s.journal(ctx, st.ID, pgtype.UUID{}, "share_set", map[string]any{"factor": factor, "budget_share_from": old, "budget_share": next, "min_share_from": minF.Float64, "min_share": minNext, "via": via})
	return next, nil
}

// RaiseBudget adds usd a month to a stream's share of the API ceiling. Red
// zone only: never called without Руслан's confirmation (§14: no automatic
// raise of the money ceiling).
func (s *Supervisor) RaiseBudget(ctx context.Context, st sdb.Stream, usd float64, via string) (float64, error) {
	if s.APIMonthlyUSD <= 0 {
		return 0, fmt.Errorf("месячный потолок API не задан")
	}
	if usd <= 0 || usd > genui.MaxRaiseUSD {
		return 0, fmt.Errorf("сумма от 0 до %d", genui.MaxRaiseUSD)
	}
	old := charterShare(st.Charter)
	next := old + usd/s.APIMonthlyUSD
	if next > 1 {
		return 0, fmt.Errorf("доля потока превысила бы весь потолок API")
	}
	next = math.Round(next*10000) / 10000
	minF, _ := st.MinShare.Float64Value()
	if err := s.writeShare(ctx, st, next, minF.Float64); err != nil {
		return 0, err
	}
	s.journal(ctx, st.ID, pgtype.UUID{}, "budget_raised", map[string]any{"usd": usd, "budget_share_from": old, "budget_share": next, "via": via})
	return next, nil
}

// writeShare keeps every other charter field as stored.
func (s *Supervisor) writeShare(ctx context.Context, st sdb.Stream, share, minShare float64) error {
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
	return s.Q.SetStreamShare(ctx, sdb.SetStreamShareParams{ID: st.ID, Charter: raw, MinShare: n})
}

func charterShare(raw []byte) float64 {
	c, _ := ParseCharter(raw)
	return c.BudgetShare
}

func clampShare(f float64) float64 { return math.Round(math.Min(1, math.Max(0, f))*10000) / 10000 }

func fmtShare(f float64) string { return strconv.FormatFloat(f*100, 'f', -1, 64) + "%" }

func fmtFactor(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
