package streams

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/streams/genui"
	"github.com/multica-ai/multica/server/internal/streams/sdb"
)

// The morning digest in the chat (§9.1 screen 1, §13 day 15): "Смотри, что
// получилось" — milestone cards with evidence — then "Нужен ты" — the shown
// questions as AskCards whose options answer them, then the pulse of the
// streams. Telegram keeps the text digest with a link to this screen.

// digestBlockID is one screen per workspace, Moscow day and slot.
func digestBlockID(ws pgtype.UUID, day string, slot DigestSlot) pgtype.UUID {
	h := sha1.Sum([]byte(fmt.Sprintf("fabrika-digest:%x:%s:%s", ws.Bytes, day, slot)))
	var u pgtype.UUID
	copy(u.Bytes[:], h[:16])
	u.Bytes[6] = (u.Bytes[6] & 0x0f) | 0x50
	u.Bytes[8] = (u.Bytes[8] & 0x3f) | 0x80
	u.Valid = true
	return u
}

// digestScreenSource builds the digest screen from the streams' records.
func (s *Supervisor) digestScreenSource(ctx context.Context, ws pgtype.UUID, now time.Time) (string, error) {
	streams, err := s.Q.ListStreams(ctx, ws)
	if err != nil {
		return "", err
	}
	q := strconv.Quote
	var lines, top []string
	add := func(id, expr string) {
		lines = append(lines, id+" = "+expr)
		top = append(top, id)
	}
	add("head", fmt.Sprintf("Text(%s, %q)", q("Фабрика · пульс "+now.In(Moscow).Format("02.01 15:04")+" МСК"), "heading"))
	var ms []string
	for _, st := range streams {
		if !st.Active {
			continue
		}
		rows, err := s.Q.ListMilestonesSince(ctx, sdb.ListMilestonesSinceParams{StreamID: st.ID, CreatedAt: ts(now.Add(-24 * time.Hour))})
		if err != nil {
			return "", err
		}
		for _, m := range rows {
			ev := ""
			var links []string
			if json.Unmarshal(m.Evidence, &links) == nil {
				for _, l := range links {
					if s.screenLinkOK(l) {
						ev = l
						break
					}
				}
			}
			evArg := "null"
			if ev != "" {
				evArg = q(ev)
			}
			ms = append(ms, fmt.Sprintf("MilestoneCard(%s, %s, %s, %s)", q(firstLine(m.Text, 300)), evArg, q(fmtMSK(m.CreatedAt.Time)), q(st.Title)))
		}
	}
	if len(ms) > 0 {
		add("seen", fmt.Sprintf("Text(%s, %q)", q("Смотри, что получилось"), "heading"))
		for i, m := range ms {
			add(fmt.Sprintf("m%d", i+1), m)
		}
	}
	shown, err := s.Q.ListShownAsks(ctx, ws)
	if err != nil {
		return "", err
	}
	if len(shown) > 0 {
		add("need", fmt.Sprintf("Text(%s, %q)", q(fmt.Sprintf("Нужен ты: %d %s", len(shown), plural(len(shown), "вопрос", "вопроса", "вопросов"))), "heading"))
		for i, a := range shown {
			var opts []string
			_ = json.Unmarshal(a.Options, &opts)
			if a.Kind == "batch" || len(opts) == 0 {
				opts = []string{"да", "нет"}
			}
			qs := make([]string, len(opts))
			for j, o := range opts {
				qs[j] = q(firstLine(o, 70))
			}
			def := "null"
			if d := askDefault(a); d >= 0 {
				def = strconv.Itoa(d)
			}
			add(fmt.Sprintf("a%d", i+1), fmt.Sprintf("AskCard(%s, [%s], %s, %s, %s)", q(firstLine(a.Text, 600)), strings.Join(qs, ", "), q(uuidString(a.ID)), def, q(askZone(a))))
		}
	}
	var rows []string
	for _, st := range streams {
		if !st.Active {
			continue
		}
		rows = append(rows, fmt.Sprintf("[%s, %s, %s]", q(st.Title), q(stateWords[st.State]), q(firstLine(st.Reason, 70))))
	}
	if len(rows) > 0 {
		add("pulse", fmt.Sprintf("Table([%s, %s, %s], [%s])", q("Поток"), q("Состояние"), q("Почему"), strings.Join(rows, ", ")))
	}
	return "root = Stack([" + strings.Join(top, ", ") + "])\n" + strings.Join(lines, "\n"), nil
}

func plural(n int, one, few, many string) string {
	n10, n100 := n%10, n%100
	switch {
	case n10 == 1 && n100 != 11:
		return one
	case n10 >= 2 && n10 <= 4 && (n100 < 12 || n100 > 14):
		return few
	}
	return many
}

func (s *Supervisor) screenLinkOK(l string) bool {
	return genui.Options{LinkHosts: s.Screens.LinkHosts}.LinkOK(l)
}

// DigestScreen stores the digest as a screen of Мультика's chat and
// announces it (chat:ui_final without a task). It returns the chat link
// for the Telegram text, "" when there is no chat yet.
func (s *Supervisor) DigestScreen(ctx context.Context, ws pgtype.UUID, slot DigestSlot) (string, error) {
	if s.Screens == nil || !s.MultikaEnabled() {
		return "", nil
	}
	sid, err := s.MultikaSession(ctx, ws)
	if err != nil {
		return "", nil // no chat with Мультика yet
	}
	now := s.now()
	id := digestBlockID(ws, now.In(Moscow).Format("2006-01-02"), slot)
	b, err := s.Q.GetUIBlock(ctx, id)
	if err != nil {
		src, err := s.digestScreenSource(ctx, ws, now)
		if err != nil {
			return "", err
		}
		res := genui.Finalize(src, s.screenOptions(ctx, ws), nil)
		if b, err = s.storeScreen(ctx, id, ws, sid, pgtype.UUID{}, pgtype.UUID{}, 0, "code", res); err != nil {
			return "", err
		}
		creator, err := s.Q.ChatSessionCreator(ctx, sid)
		if err == nil && s.Screens.bus != nil && b.Status != genui.StatusFailed {
			s.Screens.bus.Publish(events.Event{
				Type: EventUIFinal, WorkspaceID: uuidString(ws), ActorType: "system",
				Payload:       FinalEvent{ChatSessionID: uuidString(sid), Block: blockView(b)},
				ChatSessionID: uuidString(sid), ChatSessionOwnerID: uuidString(creator),
			})
		}
	}
	return s.chatLink(ctx, uuidString(sid)), nil
}
