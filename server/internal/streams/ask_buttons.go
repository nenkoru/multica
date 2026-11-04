package streams

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/streams/sdb"
)

// Inline buttons for shown questions (§7, §10.5). Each button is a short
// server-held token in ui_action; the press comes back through the Telegram
// polling loop that already serves Мультика's bot, so no webhook or second
// getUpdates consumer is involved.

// AskButton is one inline button: a label and the token it carries.
type AskButton struct {
	Text string
	Data string
}

// AskButtonSender posts a message with inline buttons to a Telegram chat of
// a channel installation. The wiring adapts telegram.ButtonSender.
type AskButtonSender interface {
	SendButtons(ctx context.Context, installationID pgtype.UUID, chatID int64, text string, rows [][]AskButton) error
}

// askTokenPrefix marks Fabrika's callback data so other buttons are left alone.
const askTokenPrefix = "fa1."

const (
	pressNotOurs   = "Эта кнопка не отсюда."
	pressStale     = "Кнопка устарела."
	pressAnswered  = "Уже отвечено."
	pressClosed    = "Вопрос уже закрыт."
	pressFailed    = "Не получилось — ответьте текстом, например «1 а»."
	askButtonsChan = "telegram_button"
)

type askButtonAction struct {
	Kind   string `json:"kind"`
	AskID  string `json:"ask_id"`
	Choice *int   `json:"choice,omitempty"`
	Yes    *bool  `json:"yes,omitempty"`
	Label  string `json:"label"`
}

func newAskToken() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return askTokenPrefix + hex.EncodeToString(b[:]), nil
}

// askButtonsFor lists the answers a question offers as buttons: one per
// option for a choice, да/нет for a batch of approvals.
func askButtonsFor(a sdb.Ask) []askButtonAction {
	id := uuidString(a.ID)
	switch a.Kind {
	case "choice":
		var opts []string
		_ = json.Unmarshal(a.Options, &opts)
		out := make([]askButtonAction, 0, len(opts))
		for i, o := range opts {
			i := i
			out = append(out, askButtonAction{Kind: "ask_reply", AskID: id, Choice: &i, Label: OptionLabel(i) + ") " + o})
		}
		return out
	case "batch":
		yes, no := true, false
		return []askButtonAction{
			{Kind: "ask_reply", AskID: id, Yes: &yes, Label: "да"},
			{Kind: "ask_reply", AskID: id, Yes: &no, Label: "нет"},
		}
	}
	return nil
}

// PushAskButtons sends every shown question that has no live buttons yet to
// Мультика's Telegram chat, one message per question, numbered as in the
// digest so text answers ("1 а") keep working. It returns how many were sent.
// Without the Мультика flag, a sender or a Telegram chat it does nothing.
func (s *Supervisor) PushAskButtons(ctx context.Context, workspaceID pgtype.UUID) (int, error) {
	if !s.MultikaEnabled() || s.AskButtons == nil {
		return 0, nil
	}
	target, err := s.Q.MultikaTelegramTarget(ctx, sdb.MultikaTelegramTargetParams{AgentID: s.MultikaAgentID, WorkspaceID: workspaceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	chatID, err := strconv.ParseInt(target.ChannelChatID, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("multika telegram chat id: %w", err)
	}
	shown, err := s.Q.ListShownAsks(ctx, workspaceID)
	if err != nil {
		return 0, err
	}
	sent := 0
	for i, a := range shown {
		n, err := s.Q.CountAskActions(ctx, a.ID)
		if err != nil {
			return sent, err
		}
		acts := askButtonsFor(a)
		if n > 0 || len(acts) == 0 {
			continue
		}
		var row []AskButton
		var ids []pgtype.UUID
		for _, act := range acts {
			tok, err := newAskToken()
			if err != nil {
				return sent, err
			}
			raw, _ := json.Marshal(act)
			ua, err := s.Q.InsertUIAction(ctx, sdb.InsertUIActionParams{AskID: a.ID, Token: tok, Action: raw, Zone: a.Zone})
			if err != nil {
				return sent, err
			}
			ids = append(ids, ua.ID)
			row = append(row, AskButton{Text: act.Label, Data: tok})
		}
		text := fmt.Sprintf("%d. %s", i+1, a.Text)
		if err := s.AskButtons.SendButtons(ctx, target.InstallationID, chatID, text, askButtonRows(row)); err != nil {
			// Failed rows do not count as live buttons: the next push retries.
			for _, id := range ids {
				_ = s.Q.FailUIAction(ctx, id)
			}
			return sent, err
		}
		sent++
	}
	return sent, nil
}

// askButtonRows puts short labels side by side and long ones one per row.
func askButtonRows(bs []AskButton) [][]AskButton {
	long := false
	for _, b := range bs {
		if len([]rune(b.Text)) > 12 {
			long = true
		}
	}
	if !long {
		return [][]AskButton{bs}
	}
	rows := make([][]AskButton, 0, len(bs))
	for _, b := range bs {
		rows = append(rows, []AskButton{b})
	}
	return rows
}

// PressAskButton applies a button press from Мультика's Telegram chat. It
// returns the toast for the pressing client and, when the press settled the
// question, the line that replaces the buttons. Data that is not Fabrika's
// returns both empty so the button is merely acknowledged.
func (s *Supervisor) PressAskButton(ctx context.Context, installationID pgtype.UUID, chatID, fromID int64, data string) (toast, done string) {
	if !strings.HasPrefix(data, askTokenPrefix) || !s.MultikaEnabled() {
		return "", ""
	}
	// A direct chat's id is the member's own id; anything else is a group or
	// someone else's chat.
	if chatID != fromID {
		return pressNotOurs, ""
	}
	ws, err := s.Q.MultikaTelegramChatWorkspace(ctx, sdb.MultikaTelegramChatWorkspaceParams{
		InstallationID: installationID, ChannelChatID: strconv.FormatInt(chatID, 10), AgentID: s.MultikaAgentID,
	})
	if err != nil {
		return pressNotOurs, ""
	}
	ua, err := s.Q.GetUIActionByToken(ctx, data)
	if err != nil || !ua.AskID.Valid {
		return pressStale, ""
	}
	a, err := s.Q.GetAsk(ctx, ua.AskID)
	if err != nil {
		return pressStale, ""
	}
	st, err := s.Q.GetStream(ctx, a.StreamID)
	if err != nil || st.WorkspaceID != ws {
		return pressNotOurs, ""
	}
	if a.Status != "shown" && a.Status != "queued" {
		_ = s.Q.ExpireAskActions(ctx, a.ID)
		return pressClosed, "— " + pressClosed
	}
	if ua.Status != "pending" {
		return pressAnswered, ""
	}
	var act askButtonAction
	if err := json.Unmarshal(ua.Action, &act); err != nil || act.Kind != "ask_reply" {
		return pressStale, ""
	}
	r := Reply{Choice: -1, Yes: act.Yes}
	if act.Choice != nil {
		r.Choice = *act.Choice
	}
	result, _ := json.Marshal(map[string]any{"label": act.Label})
	n, err := s.Q.FinishUIAction(ctx, sdb.FinishUIActionParams{ID: ua.ID, Status: "done", Result: result})
	if err != nil {
		return pressFailed, ""
	}
	if n == 0 {
		return pressAnswered, ""
	}
	out, err := s.applyReply(ctx, a, r, askButtonsChan, s.now())
	if err == nil && out == replyClosed {
		// Another button or a text answer resolved it first: this press
		// changed nothing, so its action ends expired, not done.
		_ = s.Q.ReleaseUIAction(ctx, ua.ID)
		_ = s.Q.ExpireAskActions(ctx, a.ID)
		return pressClosed, "— " + pressClosed
	}
	if err != nil || out != replyApplied {
		_ = s.Q.ReleaseUIAction(ctx, ua.ID)
		return pressFailed, ""
	}
	_ = s.Q.ExpireAskActions(ctx, a.ID)
	s.Kick()
	return "Принято: " + act.Label, "✓ " + act.Label
}
