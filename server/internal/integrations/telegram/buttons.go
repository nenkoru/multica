package telegram

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Button is one inline keyboard button. Data travels back in the press and
// must fit Telegram's 64-byte callback_data limit.
type Button struct {
	Text string
	Data string
}

// CallbackFunc handles a button press seen by one installation's polling
// loop. toast is shown on the pressing client (empty: none); done, when set,
// replaces the keyboard: the buttons are removed and done is appended to the
// message text. A press the handler does not recognise returns both empty.
type CallbackFunc func(ctx context.Context, installationID pgtype.UUID, chatID, fromID int64, data string) (toast, done string)

const maxCallbackData = 64

func inlineKeyboard(rows [][]Button) (*InlineKeyboardMarkup, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	kb := &InlineKeyboardMarkup{InlineKeyboard: make([][]InlineKeyboardButton, 0, len(rows))}
	for _, row := range rows {
		out := make([]InlineKeyboardButton, 0, len(row))
		for _, b := range row {
			if b.Data == "" || len(b.Data) > maxCallbackData {
				return nil, fmt.Errorf("telegram: callback data must be 1..%d bytes", maxCallbackData)
			}
			out = append(out, InlineKeyboardButton{Text: b.Text, CallbackData: b.Data})
		}
		kb.InlineKeyboard = append(kb.InlineKeyboard, out)
	}
	return kb, nil
}

// buttonQueries is the slice of generated queries ButtonSender needs.
type buttonQueries interface {
	GetChannelInstallation(ctx context.Context, arg db.GetChannelInstallationParams) (db.ChannelInstallation, error)
}

// ButtonSender posts a message with an inline keyboard through an
// installation's bot. It only calls sendMessage; the presses come back
// through that installation's existing polling loop (ChannelDeps.Callbacks).
type ButtonSender struct {
	q       buttonQueries
	decrypt Decrypter
	apiBase string
	client  *http.Client
}

// NewButtonSender builds a sender. apiBase empty uses production.
func NewButtonSender(q buttonQueries, decrypt Decrypter, apiBase string) *ButtonSender {
	return &ButtonSender{q: q, decrypt: decrypt, apiBase: apiBase, client: &http.Client{Timeout: 15 * time.Second}}
}

// Send posts text (plain, no parse mode) with rows of buttons and returns
// the Telegram message id.
func (b *ButtonSender) Send(ctx context.Context, installationID pgtype.UUID, chatID int64, text string, rows [][]Button) (int64, error) {
	kb, err := inlineKeyboard(rows)
	if err != nil {
		return 0, err
	}
	inst, err := b.q.GetChannelInstallation(ctx, db.GetChannelInstallationParams{ID: installationID, ChannelType: string(TypeTelegram)})
	if err != nil {
		return 0, fmt.Errorf("load telegram installation: %w", err)
	}
	if inst.Status != "active" {
		return 0, errors.New("telegram: installation is not active")
	}
	creds, err := decodeCredentials(inst.Config, b.decrypt)
	if err != nil {
		return 0, err
	}
	m, err := sendMessageWithRetryAfter(ctx, newBotAPI(b.apiBase, creds.BotToken, b.client), sendMessageParams{
		ChatID: chatID, Text: text, ReplyMarkup: kb,
	})
	if err != nil {
		return 0, err
	}
	return m.MessageID, nil
}

// handleCallback answers one press: the toast first (Telegram keeps the
// button spinning until answerCallbackQuery), then, when the handler says
// the press settled something, the keyboard comes off and the result is
// appended to the message. Best-effort: a failure is logged, never fatal to
// the polling loop.
func (c *telegramChannel) handleCallback(ctx context.Context, q *CallbackQuery) {
	var toast, done string
	if c.callbacks != nil && q.Message != nil && q.From != nil {
		toast, done = c.callbacks(ctx, c.installationID, q.Message.Chat.ID, q.From.ID, q.Data)
	}
	if err := c.api.AnswerCallbackQuery(ctx, q.ID, toast); err != nil {
		c.logger.WarnContext(ctx, "telegram: answerCallbackQuery failed", "error", err)
	}
	if done == "" || q.Message == nil {
		return
	}
	if err := c.api.EditMessageText(ctx, editMessageTextParams{
		ChatID:    q.Message.Chat.ID,
		MessageID: q.Message.MessageID,
		Text:      q.Message.Text + "\n\n" + done,
	}); err != nil {
		c.logger.WarnContext(ctx, "telegram: closing pressed keyboard failed", "error", err)
	}
}
