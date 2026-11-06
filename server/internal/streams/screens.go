package streams

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/streams/genui"
	"github.com/multica-ai/multica/server/internal/streams/sdb"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Screens in chat (§10, §13 week 3). Мультика writes a screen as an
// ```openui block in her reply. While the reply streams in, every finished
// line inside the block is checked against the catalog and sent to the web
// as chat:ui_line, so the screen grows as she writes it. When the reply is
// done, the block is compiled to A2UI, fully checked, repaired by the model
// at most twice, stored in ui_block and sent as chat:ui_final; only then do
// its buttons work. The chat:* prefix keeps both events private to the
// chat's owner in the realtime fan-out, like every chat event.

// Realtime events of the screen pipeline (§10.2: ui.line / ui.final).
const (
	EventUILine  = "chat:ui_line"
	EventUIFinal = "chat:ui_final"
)

// Block seq ranges within one reply: code-built screens (StreamCard on a
// focus switch) first, then the model's blocks in reply order.
const (
	codeBlockSeq  = 0
	modelBlockSeq = 10
)

// Screens holds the live state of the pipeline. Nil disables it.
type Screens struct {
	// LinkHosts are the hosts a screen may link to (FABRIKA_GENUI_LINK_HOSTS).
	LinkHosts []string
	// Repair asks a model to fix a screen; nil keeps the first build.
	Repair genui.Repairer
	// AppURL is the web app's base for "открыть вживую" links (MULTICA_APP_URL).
	AppURL string

	bus  *events.Bus
	mu   sync.Mutex
	live map[string]*liveScreen // task id → reply being streamed
}

type liveScreen struct {
	multika bool
	lastSeq int
	stream  genui.Stream
	seen    time.Time
}

// liveTTL drops the state of a reply that never finished.
const liveTTL = time.Hour

// LineEvent is the chat:ui_line payload: the nodes of one finished line.
type LineEvent struct {
	TaskID        string       `json:"task_id"`
	ChatSessionID string       `json:"chat_session_id"`
	Block         int          `json:"block"`
	Line          int          `json:"line"`
	Nodes         []genui.Node `json:"nodes"`
	// Root is set on the line that defines the screen's root.
	Root string `json:"root,omitempty"`
}

// BlockView is a stored screen as the web reads it.
type BlockView struct {
	ID            string          `json:"id"`
	TaskID        string          `json:"task_id"`
	ChatSessionID string          `json:"chat_session_id"`
	MessageID     string          `json:"message_id,omitempty"`
	Seq           int32           `json:"seq"`
	Kind          string          `json:"kind"`
	Status        string          `json:"status"`
	A2UI          json.RawMessage `json:"a2ui"`
	Issues        int             `json:"issues"`
	CreatedAt     time.Time       `json:"created_at"`
}

func blockView(b sdb.UiBlock) BlockView {
	var issues []genui.Issue
	_ = json.Unmarshal(b.Issues, &issues)
	return BlockView{
		ID: uuidString(b.ID), TaskID: uuidString(b.TaskID), ChatSessionID: uuidString(b.ChatSessionID),
		MessageID: uuidString(b.MessageID), Seq: b.Seq, Kind: b.Kind, Status: b.Status,
		A2UI: json.RawMessage(b.A2ui), Issues: len(issues), CreatedAt: b.CreatedAt.Time,
	}
}

// FinalEvent is the chat:ui_final payload.
type FinalEvent struct {
	TaskID        string    `json:"task_id"`
	ChatSessionID string    `json:"chat_session_id"`
	MessageID     string    `json:"message_id,omitempty"`
	Block         BlockView `json:"block"`
}

// subscribeScreens wires the pipeline to the bus. Both handlers return
// quickly: the bus is synchronous and also delivers the reply itself.
func (s *Supervisor) subscribeScreens(bus *events.Bus) {
	if s.Screens == nil || !s.MultikaEnabled() {
		return
	}
	s.Screens.bus = bus
	bus.Subscribe(protocol.EventTaskMessage, s.onScreenTaskMessage)
	bus.Subscribe(protocol.EventChatDone, func(e events.Event) {
		p, ok := e.Payload.(protocol.ChatDonePayload)
		if !ok {
			return
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			s.finishScreens(ctx, e, p)
		}()
	})
}

func (s *Supervisor) onScreenTaskMessage(e events.Event) {
	p, ok := e.Payload.(protocol.TaskMessagePayload)
	if !ok || p.Type != "text" || p.Content == "" || e.ChatSessionID == "" {
		return
	}
	sc := s.Screens
	now := s.now()
	sc.mu.Lock()
	if sc.live == nil {
		sc.live = map[string]*liveScreen{}
	}
	ls, known := sc.live[p.TaskID]
	sc.mu.Unlock()
	if !known {
		ls = &liveScreen{lastSeq: -1}
		if taskID, err := parseUUID(p.TaskID); err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			row, err := s.Q.TaskChatAgent(ctx, taskID)
			cancel()
			ls.multika = err == nil && s.IsMultika(row.AgentID)
		}
		sc.mu.Lock()
		for id, old := range sc.live {
			if now.Sub(old.seen) > liveTTL {
				delete(sc.live, id)
			}
		}
		if cur, raced := sc.live[p.TaskID]; raced {
			ls = cur
		} else {
			sc.live[p.TaskID] = ls
		}
		sc.mu.Unlock()
	}
	if !ls.multika {
		return
	}
	sc.mu.Lock()
	if p.Seq <= ls.lastSeq {
		sc.mu.Unlock()
		return // a redelivered frame
	}
	ls.lastSeq, ls.seen = p.Seq, now
	lines := ls.stream.Feed(p.Content)
	sc.mu.Unlock()
	for _, l := range lines {
		st, ok, err := genui.ParseLine(l.Text)
		if err != nil || !ok {
			continue // an invalid line simply does not appear (§10.2)
		}
		cl, err := genui.CheckLine(st, genui.Options{LinkHosts: sc.LinkHosts})
		if err != nil {
			continue
		}
		ev := LineEvent{TaskID: p.TaskID, ChatSessionID: e.ChatSessionID, Block: l.Block, Line: l.Line, Nodes: cl.Nodes}
		if st.ID == "root" {
			ev.Root = st.ID
		}
		sc.bus.Publish(events.Event{
			Type: EventUILine, WorkspaceID: e.WorkspaceID, ActorType: "system", Payload: ev,
			TaskID: p.TaskID, ChatSessionID: e.ChatSessionID, ChatSessionOwnerID: e.ChatSessionOwnerID,
		})
	}
}

// finishScreens compiles, stores and announces the screens of a finished
// reply, including code-built ones stored when the turn was claimed.
func (s *Supervisor) finishScreens(ctx context.Context, e events.Event, p protocol.ChatDonePayload) {
	sc := s.Screens
	sc.mu.Lock()
	ls := sc.live[p.TaskID]
	delete(sc.live, p.TaskID)
	sc.mu.Unlock()
	taskID, err1 := parseUUID(p.TaskID)
	sessionID, err2 := parseUUID(p.ChatSessionID)
	if err1 != nil || err2 != nil {
		return
	}
	if ls == nil {
		row, err := s.Q.TaskChatAgent(ctx, taskID)
		if err != nil || !s.IsMultika(row.AgentID) {
			return
		}
	} else if !ls.multika {
		return
	}
	owner, err := s.Q.GetChatSessionOwner(ctx, sessionID)
	if err != nil {
		slog.Warn("streams: screens session lookup failed", "error", err)
		return
	}
	messageID, _ := parseUUID(p.MessageID)
	if messageID.Valid {
		_ = s.Q.SetUIBlocksMessage(ctx, sdb.SetUIBlocksMessageParams{TaskID: taskID, MessageID: messageID})
	}
	sources, _ := genui.Extract(p.Content)
	for i, src := range sources {
		if _, err := s.storeModelScreen(ctx, owner.WorkspaceID, sessionID, taskID, messageID, int32(modelBlockSeq+i), src); err != nil {
			slog.Warn("streams: screen store failed", "task", p.TaskID, "error", err)
		}
	}
	blocks, err := s.Q.ListUIBlocksByTask(ctx, taskID)
	if err != nil {
		return
	}
	for _, b := range blocks {
		sc.bus.Publish(events.Event{
			Type: EventUIFinal, WorkspaceID: e.WorkspaceID, ActorType: "system",
			Payload: FinalEvent{TaskID: p.TaskID, ChatSessionID: p.ChatSessionID, MessageID: p.MessageID, Block: blockView(b)},
			TaskID:  p.TaskID, ChatSessionID: p.ChatSessionID, ChatSessionOwnerID: e.ChatSessionOwnerID,
		})
	}
}

// screenOptions are the checks that need the workspace: action targets must
// be its streams and its open questions.
func (s *Supervisor) screenOptions(ctx context.Context, ws pgtype.UUID) genui.Options {
	slugs := map[string]bool{}
	if streams, err := s.Q.ListStreams(ctx, ws); err == nil {
		for _, st := range streams {
			slugs[st.Slug] = true
		}
	}
	asks := map[string]bool{}
	if shown, err := s.Q.ListShownAsks(ctx, ws); err == nil {
		for _, a := range shown {
			asks[uuidString(a.ID)] = true
		}
	}
	return genui.Options{
		LinkHosts:    s.Screens.LinkHosts,
		StreamExists: func(slug string) bool { return slugs[slug] },
		AskOpen:      func(id string) bool { return asks[id] },
	}
}

// blockID derives a screen's id from its reply and position.
func blockID(taskID pgtype.UUID, seq int32) pgtype.UUID {
	h := sha1.Sum([]byte(fmt.Sprintf("fabrika-ui-block:%x:%d", taskID.Bytes, seq)))
	var u pgtype.UUID
	copy(u.Bytes[:], h[:16])
	u.Bytes[6] = (u.Bytes[6] & 0x0f) | 0x50 // version 5 layout
	u.Bytes[8] = (u.Bytes[8] & 0x3f) | 0x80
	u.Valid = true
	return u
}

func (s *Supervisor) storeModelScreen(ctx context.Context, ws, sessionID, taskID, messageID pgtype.UUID, seq int32, src string) (sdb.UiBlock, error) {
	id := blockID(taskID, seq)
	if b, err := s.Q.GetUIBlock(ctx, id); err == nil {
		return b, nil // a redelivered reply: stored once, repaired once
	}
	res := genui.Finalize(src, s.screenOptions(ctx, ws), s.Screens.Repair)
	return s.storeScreen(ctx, id, ws, sessionID, taskID, messageID, seq, "model", res)
}

// storeScreen binds the screen's buttons to action records and stores it.
func (s *Supervisor) storeScreen(ctx context.Context, id, ws, sessionID, taskID, messageID pgtype.UUID, seq int32, kind string, res genui.Result) (sdb.UiBlock, error) {
	var raw []byte
	text := ""
	if res.Status != genui.StatusFailed {
		if err := s.bindScreenActions(ctx, id, ws, res.Doc); err != nil {
			return sdb.UiBlock{}, err
		}
		raw, _ = json.Marshal(res.Doc)
		text = genui.Text(res.Doc)
	}
	issues, _ := json.Marshal(res.Issues)
	if res.Issues == nil {
		issues = []byte("[]")
	}
	b, err := s.Q.InsertUIBlock(ctx, sdb.InsertUIBlockParams{
		ID: id, MessageID: messageID, WorkspaceID: ws, ChatSessionID: sessionID, TaskID: taskID, Seq: seq, Kind: kind,
		OpenuiSource: res.Source, A2ui: raw, Status: res.Status, Issues: issues, Repairs: int32(res.Repairs), TextVersion: text,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return s.Q.GetUIBlock(ctx, id)
	}
	return b, err
}

// screenAction is what a ui_action row of a screen button holds.
type screenAction struct {
	Kind   string       `json:"kind"` // "screen"
	Action genui.Action `json:"action"`
	Label  string       `json:"label"`
}

// bindScreenActions creates one action record per button (§10.4: each
// action has its own id) and writes the ids into the screen. AskCards of
// open questions get one answer_ask action per option.
func (s *Supervisor) bindScreenActions(ctx context.Context, blockID, ws pgtype.UUID, doc genui.Doc) error {
	bind := func(key string, act genui.Action, label string) (sdb.UiAction, error) {
		zone := act.Zone()
		var askID pgtype.UUID
		if act.Name == "answer_ask" {
			id, _ := act.Args["ask_id"].(string)
			a, err := s.askInWorkspace(ctx, ws, id)
			if err != nil {
				return sdb.UiAction{}, err
			}
			askID, zone = a.ID, askZone(a)
		}
		tok, err := newAskToken()
		if err != nil {
			return sdb.UiAction{}, err
		}
		raw, _ := json.Marshal(screenAction{Kind: "screen", Action: act, Label: label})
		k := pgtype.Text{String: "ui:" + uuidString(blockID) + ":" + key, Valid: true}
		ua, err := s.Q.InsertBlockUIAction(ctx, sdb.InsertBlockUIActionParams{UiBlockID: blockID, AskID: askID, Token: tok, Action: raw, Zone: zone, IdempotencyKey: k})
		if errors.Is(err, pgx.ErrNoRows) {
			return s.Q.GetUIActionByKey(ctx, k)
		}
		return ua, err
	}
	for _, b := range doc.Buttons() {
		ua, err := bind(fmt.Sprintf("%s:%d", b.NodeID, b.Index), b.Action, b.Label)
		if err != nil {
			return err
		}
		doc.BindButton(b.NodeID, b.Index, uuidString(ua.ID), ua.Zone)
	}
	for _, n := range doc.Components {
		if n.Name() != "AskCard" {
			continue
		}
		props := n.Props()
		askID, _ := props["ask_id"].(string)
		opts, _ := props["options"].([]string)
		a, err := s.askInWorkspace(ctx, ws, askID)
		if askID == "" || err != nil || (a.Status != "shown" && a.Status != "queued") {
			delete(props, "ask_id") // shown as text only
			continue
		}
		props["zone"] = askZone(a)
		ids := make([]string, 0, len(opts))
		for i, o := range opts {
			act := genui.Action{Name: "answer_ask", Args: map[string]any{"ask_id": askID, "choice": float64(i)}}
			ua, err := bind(fmt.Sprintf("%s:opt%d", n.ID, i), act, o)
			if err != nil {
				return err
			}
			ids = append(ids, uuidString(ua.ID))
		}
		props["option_actions"] = ids
	}
	return nil
}

func (s *Supervisor) askInWorkspace(ctx context.Context, ws pgtype.UUID, id string) (sdb.Ask, error) {
	askID, err := parseUUID(id)
	if err != nil {
		return sdb.Ask{}, fmt.Errorf("ask %q: %w", id, err)
	}
	a, err := s.Q.GetAsk(ctx, askID)
	if err != nil {
		return sdb.Ask{}, err
	}
	st, err := s.Q.GetStream(ctx, a.StreamID)
	if err != nil || st.WorkspaceID != ws {
		return sdb.Ask{}, fmt.Errorf("ask %q is not in this workspace", id)
	}
	return a, nil
}

// ScreenBlocks lists the stored screens of a chat, newest first.
func (s *Supervisor) ScreenBlocks(ctx context.Context, ws, sessionID pgtype.UUID, limit int32) ([]BlockView, error) {
	rows, err := s.Q.ListUIBlocksBySession(ctx, sdb.ListUIBlocksBySessionParams{ChatSessionID: sessionID, WorkspaceID: ws, Limit: limit})
	if err != nil {
		return nil, err
	}
	out := make([]BlockView, 0, len(rows))
	for _, b := range rows {
		out = append(out, blockView(b))
	}
	return out, nil
}

// TelegramReplyText turns screen blocks of a reply into their text version
// with a link to open them live (§10.5). Replies without a block pass
// through unchanged.
func (s *Supervisor) TelegramReplyText(ctx context.Context, sessionID, content string) string {
	if s.Screens == nil || !strings.Contains(content, "```"+genui.FenceTag) {
		return content
	}
	sources, rest := genui.Extract(content)
	parts := []string{}
	if rest != "" {
		parts = append(parts, rest)
	}
	for _, src := range sources {
		if doc, _, ok := genui.Build(src, genui.Options{LinkHosts: s.Screens.LinkHosts}); ok {
			if t := genui.Text(doc); t != "" {
				parts = append(parts, t)
			}
		}
	}
	if link := s.chatLink(ctx, sessionID); link != "" {
		parts = append(parts, "Открыть вживую: "+link)
	}
	return strings.Join(parts, "\n\n")
}

// TelegramPartialText hides screen blocks from a streaming reply: the
// partial shows the prose only, the final reply brings the text version.
func TelegramPartialText(content string) string {
	if !strings.Contains(content, "```"+genui.FenceTag) {
		return content
	}
	return genui.StripForDisplay(content)
}

func (s *Supervisor) chatLink(ctx context.Context, sessionID string) string {
	if s.Screens == nil || s.Screens.AppURL == "" {
		return ""
	}
	sid, err := parseUUID(sessionID)
	if err != nil {
		return ""
	}
	owner, err := s.Q.GetChatSessionOwner(ctx, sid)
	if err != nil {
		return ""
	}
	slug, err := s.Q.WorkspaceSlug(ctx, owner.WorkspaceID)
	if err != nil {
		return ""
	}
	return strings.TrimRight(s.Screens.AppURL, "/") + "/" + slug + "/chat?session=" + sessionID
}
