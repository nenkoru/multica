package streams

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/streams/sdb"
)

// HTTP serves /api/streams. Routes are registered individually by the fork's
// router (cmd/server/router.go) so the public health probe and the
// member-only routes can live in different middleware groups.
type HTTP struct {
	Sup *Supervisor
	// WorkspaceID returns the workspace resolved by upstream middleware.
	WorkspaceID func(r *http.Request) string
}

// HealthStaleAfter is how old the last tick may be before /health says 503.
const HealthStaleAfter = 2 * time.Minute

// Health is unauthenticated and returns only timestamps: the external
// watchdog polls it every minute (§6.6).
func (h *HTTP) Health(w http.ResponseWriter, r *http.Request) {
	now := h.Sup.now()
	out := map[string]any{"ok": false}
	st, err := h.Sup.Q.GetSupervisorState(r.Context())
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		out["error"] = "supervisor has not ticked yet"
	case err != nil:
		out["error"] = "state unavailable"
	default:
		age := now.Sub(st.LastTickAt.Time)
		out["last_tick_at"] = st.LastTickAt.Time.UTC()
		out["tick_age_seconds"] = int(age.Seconds())
		out["ticks"] = st.Ticks
		out["ok"] = age < HealthStaleAfter
	}
	if hb, err := h.Sup.Q.LastStepHeartbeat(r.Context()); err == nil && hb.Valid {
		out["last_step_heartbeat_at"] = hb.Time.UTC()
	}
	status := http.StatusOK
	if out["ok"] != true {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, out)
}

type streamView struct {
	ID            string     `json:"id"`
	Slug          string     `json:"slug"`
	Title         string     `json:"title"`
	State         string     `json:"state"`
	StateLabel    string     `json:"state_label"`
	Reason        string     `json:"reason"`
	Until         *time.Time `json:"until,omitempty"`
	Summary       string     `json:"summary"`
	MaxParallel   int32      `json:"max_parallel"`
	Active        bool       `json:"active"`
	LastMilestone *time.Time `json:"last_milestone_at,omitempty"`
	Charter       Charter    `json:"charter"`
}

func view(st sdb.Stream) streamView {
	c, _ := ParseCharter(st.Charter)
	v := streamView{ID: uuidString(st.ID), Slug: st.Slug, Title: st.Title, State: st.State, StateLabel: stateWords[st.State], Reason: st.Reason, Summary: st.Summary, MaxParallel: st.MaxParallel, Active: st.Active, Charter: c}
	if st.Until.Valid {
		t := st.Until.Time
		v.Until = &t
	}
	if st.LastMilestoneAt.Valid {
		t := st.LastMilestoneAt.Time
		v.LastMilestone = &t
	}
	return v
}

func (h *HTTP) workspace(w http.ResponseWriter, r *http.Request) (pgtype.UUID, bool) {
	id, err := parseUUID(h.WorkspaceID(r))
	if err != nil {
		writeError(w, http.StatusBadRequest, "workspace required")
		return pgtype.UUID{}, false
	}
	return id, true
}

func (h *HTTP) stream(w http.ResponseWriter, r *http.Request) (sdb.Stream, bool) {
	ws, ok := h.workspace(w, r)
	if !ok {
		return sdb.Stream{}, false
	}
	st, err := h.Sup.Q.GetStreamBySlug(r.Context(), sdb.GetStreamBySlugParams{WorkspaceID: ws, Slug: chi.URLParam(r, "slug")})
	if err != nil {
		writeError(w, http.StatusNotFound, "stream not found")
		return sdb.Stream{}, false
	}
	return st, true
}

// List: GET /api/streams
func (h *HTTP) List(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspace(w, r)
	if !ok {
		return
	}
	rows, err := h.Sup.Q.ListStreams(r.Context(), ws)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]streamView, 0, len(rows))
	for _, st := range rows {
		out = append(out, view(st))
	}
	writeJSON(w, http.StatusOK, map[string]any{"streams": out})
}

// Get: GET /api/streams/{slug} — stream, open steps, recent journal.
func (h *HTTP) Get(w http.ResponseWriter, r *http.Request) {
	st, ok := h.stream(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	steps, _ := h.Sup.Q.ListRecentSteps(ctx, sdb.ListRecentStepsParams{StreamID: st.ID, Limit: 20})
	journal, _ := h.Sup.Q.ListJournal(ctx, sdb.ListJournalParams{StreamID: st.ID, Limit: 50})
	ms, _ := h.Sup.Q.ListRecentMilestones(ctx, sdb.ListRecentMilestonesParams{StreamID: st.ID, Limit: 10})
	asks, _ := h.Sup.Q.ListOpenAsks(ctx, st.ID)
	waits, _ := h.Sup.Q.ListOpenWaits(ctx, st.ID)
	writeJSON(w, http.StatusOK, map[string]any{"stream": view(st), "steps": steps, "journal": journal, "milestones": ms, "asks": asks, "waits": waits})
}

type upsertRequest struct {
	Title            string  `json:"title"`
	Charter          Charter `json:"charter"`
	Summary          string  `json:"summary"`
	Weight           int32   `json:"weight"`
	MaxParallel      int32   `json:"max_parallel"`
	ManagerAgentID   string  `json:"manager_agent_id"`
	CarrierProjectID string  `json:"carrier_project_id"`
	// Migrate: old board issues (in_progress, blocked) for the first turn to
	// fold into summary and a step queue (§12). Empty: a plain first turn.
	Migrate []string `json:"migrate"`
}

// reservedSlugs are fixed routes under /api/streams that a stream slug
// would shadow.
var reservedSlugs = map[string]bool{"health": true, "digest": true, "answer": true, "waits": true, "asks": true, "multika": true}

var streamSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// Upsert: PUT /api/streams/{slug} — charter written by hand in week 1,
// by the interview from week 2. Summary is only seeded on create.
func (h *HTTP) Upsert(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspace(w, r)
	if !ok {
		return
	}
	if !streamSlug.MatchString(chi.URLParam(r, "slug")) || reservedSlugs[chi.URLParam(r, "slug")] {
		writeError(w, http.StatusBadRequest, "slug must be lowercase latin, digits and dashes, and not a reserved route")
		return
	}
	var req upsertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Title) == "" || req.Charter.Goal == "" || req.Charter.Metric == "" || req.Charter.Done == "" {
		// A stream without "done" is not started (§3 p.2).
		writeError(w, http.StatusBadRequest, "title, charter.goal, charter.metric and charter.done are required")
		return
	}
	migrate, err := NormalizeMigrate(req.Migrate)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	charter, _ := json.Marshal(req.Charter)
	if req.Weight <= 0 {
		req.Weight = 1
	}
	if req.MaxParallel <= 0 {
		req.MaxParallel = 1
	}
	mgr, _ := parseUUID(req.ManagerAgentID)
	proj, _ := parseUUID(req.CarrierProjectID)
	var share pgtype.Numeric
	_ = share.Scan("0")
	params := sdb.UpsertStreamParams{
		WorkspaceID: ws, Slug: chi.URLParam(r, "slug"), Title: req.Title, Charter: charter, Summary: req.Summary,
		Weight: req.Weight, MinShare: share, MaxParallel: req.MaxParallel, ManagerAgentID: mgr, CarrierProjectID: proj,
	}
	var st sdb.Stream
	// One transaction: a tick between the stream and its migration turn
	// would see an empty stream and queue a plain first turn as well.
	err = pgx.BeginFunc(r.Context(), h.Sup.Pool, func(tx pgx.Tx) error {
		q := h.Sup.Q.WithTx(tx)
		var err error
		if st, err = q.UpsertStream(r.Context(), params); err != nil {
			return err
		}
		if len(migrate) > 0 {
			// A second migration turn on a live stream would queue the old
			// tails twice; after a failed one a new PUT is fine.
			open, lerr := q.ListOpenSteps(r.Context(), st.ID)
			if lerr != nil {
				return lerr
			}
			for _, x := range open {
				if x.Origin == OriginMigration {
					return errMigrationOpen
				}
			}
			_, err = insertStepTx(r.Context(), q, st.ID, KindSummarize, MigrationBrief(migrate), "", "S", OriginMigration, false)
		}
		return err
	})
	if errors.Is(err, errMigrationOpen) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.Sup.journal(r.Context(), st.ID, pgtype.UUID{}, "charter", map[string]any{"title": st.Title, "migrate": migrate})
	h.Sup.Kick()
	writeJSON(w, http.StatusOK, view(st))
}

// Pause / Resume: POST /api/streams/{slug}/pause|resume — human only (§5.1).
func (h *HTTP) Pause(w http.ResponseWriter, r *http.Request) {
	st, ok := h.stream(w, r)
	if !ok {
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if err := h.Sup.Pause(r.Context(), st, req.Reason); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"state": StatePaused})
}

func (h *HTTP) Resume(w http.ResponseWriter, r *http.Request) {
	st, ok := h.stream(w, r)
	if !ok {
		return
	}
	if err := h.Sup.Resume(r.Context(), st); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"state": StateRunning})
}

// SetActive: POST /api/streams/{slug}/activate|deactivate — the migration
// switch (§12): a stream is supervised only once active.
func (h *HTTP) SetActive(active bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		st, ok := h.stream(w, r)
		if !ok {
			return
		}
		if err := h.Sup.Q.SetStreamActive(r.Context(), sdb.SetStreamActiveParams{ID: st.ID, Active: active}); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		h.Sup.journal(r.Context(), st.ID, pgtype.UUID{}, "active", map[string]any{"active": active})
		h.Sup.Kick()
		writeJSON(w, http.StatusOK, map[string]any{"active": active})
	}
}

// Digest: POST /api/streams/digest {"slot": "morning"|"evening"|"preview"}.
// Text is empty when there is nothing to send (evening without questions).
func (h *HTTP) Digest(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspace(w, r)
	if !ok {
		return
	}
	var req struct {
		Slot string `json:"slot"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	slot := DigestSlot(req.Slot)
	switch slot {
	case SlotMorning, SlotEvening, SlotPreview:
	default:
		slot = SlotPreview
	}
	text, err := h.Sup.Digest(r.Context(), ws, slot)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// The same digest as a screen in Мультика's chat; Telegram gets the
	// text with a link to it (§10.5). A failed screen never holds the text.
	if slot == SlotMorning && text != "" {
		if link, err := h.Sup.DigestScreen(r.Context(), ws, slot); err != nil {
			slog.Warn("streams: digest screen failed", "error", err)
		} else if link != "" {
			text += "\n\nОткрыть вживую: " + link
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"slot": slot, "text": text})
}

// PushAsks: POST /api/streams/asks/push — shown questions without live
// buttons go to Мультика's Telegram chat with inline buttons. Called after the
// digest text is sent; a no-op without the Мультика flag or a Telegram chat.
func (h *HTTP) PushAsks(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspace(w, r)
	if !ok {
		return
	}
	n, err := h.Sup.PushAskButtons(r.Context(), ws)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sent": n})
}

// Answer: POST /api/streams/answer {"line": "1 а, 2 да", "channel": "telegram"}.
func (h *HTTP) Answer(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspace(w, r)
	if !ok {
		return
	}
	var req struct {
		Line    string `json:"line"`
		Channel string `json:"channel"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Line) == "" {
		writeError(w, http.StatusBadRequest, "line is required")
		return
	}
	res, err := h.Sup.Answer(r.Context(), ws, req.Line, firstNonEmpty(req.Channel, "web"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// FireWait: POST /api/streams/waits/{id}/fire — an observed condition.
func (h *HTTP) FireWait(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspace(w, r)
	if !ok {
		return
	}
	id, err := parseUUID(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad wait id")
		return
	}
	wt, err := h.Sup.Q.GetWait(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "wait not found")
		return
	}
	if st, err := h.Sup.Q.GetStream(r.Context(), wt.StreamID); err != nil || st.WorkspaceID != ws {
		writeError(w, http.StatusNotFound, "wait not found")
		return
	}
	if err := h.Sup.FireWait(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"fired": true})
}

// Manage: POST /api/streams/{slug}/manage {"text": "..."} — Ruslan's message
// to the stream in focus: a manager turn in the reserved slot (§6.1).
func (h *HTTP) Manage(w http.ResponseWriter, r *http.Request) {
	st, ok := h.stream(w, r)
	if !ok {
		return
	}
	var req struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Text) == "" {
		writeError(w, http.StatusBadRequest, "text is required")
		return
	}
	step, err := h.Sup.EnqueueManage(r.Context(), st.ID, "Сообщение Руслана: "+req.Text, true)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"step_id": uuidString(step.ID), "seq": step.Seq})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

type multikaView struct {
	Enabled       bool       `json:"enabled"`
	AgentID       string     `json:"agent_id,omitempty"`
	ChatSessionID string     `json:"chat_session_id,omitempty"`
	FocusSlug     string     `json:"focus_slug,omitempty"`
	FocusTitle    string     `json:"focus_title,omitempty"`
	FocusUntil    *time.Time `json:"focus_until,omitempty"`
	// Voice: the composer may offer a microphone (POST /multika/voice).
	Voice bool `json:"voice"`
	// Screens: replies may carry screens (chat:ui_line / chat:ui_final).
	Screens bool `json:"screens"`
}

func (h *HTTP) multikaView(r *http.Request, ws pgtype.UUID) (multikaView, error) {
	if !h.Sup.MultikaEnabled() {
		return multikaView{}, nil
	}
	v := multikaView{Enabled: true, AgentID: uuidString(h.Sup.MultikaAgentID), Voice: h.Sup.Voice != nil, Screens: h.Sup.Screens != nil}
	sid, err := h.Sup.MultikaSession(r.Context(), ws)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, nil
	}
	if err != nil {
		return v, err
	}
	v.ChatSessionID = uuidString(sid)
	if f := h.Sup.MultikaFocusOf(r.Context(), ws, sid); f.Stream != nil {
		until := f.Until
		v.FocusSlug, v.FocusTitle, v.FocusUntil = f.Stream.Slug, f.Stream.Title, &until
	}
	return v, nil
}

// Multika: GET /api/streams/multika — the one chat and its focus (§9.2).
func (h *HTTP) Multika(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspace(w, r)
	if !ok {
		return
	}
	v, err := h.multikaView(r, ws)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// MultikaFocus: PUT /api/streams/multika/focus {"slug": "buvg01"} — the web
// header's "сейчас про: … ×". An empty slug clears the focus.
func (h *HTTP) MultikaFocus(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspace(w, r)
	if !ok {
		return
	}
	if !h.Sup.MultikaEnabled() {
		writeError(w, http.StatusNotFound, "multika is off")
		return
	}
	var req struct {
		Slug string `json:"slug"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "slug is required (empty clears)")
		return
	}
	sid, err := h.Sup.MultikaSession(r.Context(), ws)
	if err != nil {
		writeError(w, http.StatusNotFound, "no chat with Мультика yet")
		return
	}
	if _, err := h.Sup.SetMultikaFocus(r.Context(), ws, sid, strings.TrimSpace(req.Slug)); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	v, err := h.multikaView(r, ws)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// MaxVoiceBytes bounds one voice note: the transcription service's own limit,
// several minutes of opus.
const MaxVoiceBytes = 5 << 20

// MultikaVoice: POST /api/streams/multika/voice, multipart "file" — speech to
// text for Мультика's composer (§13 day 9). The text goes back to the
// composer for Руслан to read and send; nothing is posted to the chat here.
func (h *HTTP) MultikaVoice(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.workspace(w, r); !ok {
		return
	}
	if !h.Sup.MultikaEnabled() || h.Sup.Voice == nil {
		writeError(w, http.StatusNotFound, "voice is off")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxVoiceBytes+1<<16)
	if err := r.ParseMultipartForm(MaxVoiceBytes); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "voice note too large")
			return
		}
		writeError(w, http.StatusBadRequest, "voice note is not multipart")
		return
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file is required")
		return
	}
	defer f.Close()
	if hdr.Size > MaxVoiceBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "voice note too large")
		return
	}
	text, err := h.Sup.Voice.Transcribe(r.Context(), hdr.Filename, f)
	if err != nil {
		writeError(w, http.StatusBadGateway, "распознавание не удалось: "+firstLine(err.Error(), 200))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"text": text})
}

// ScreenBlocks: GET /api/streams/ui/blocks?session=<id> — the stored screens
// of a chat, newest first (§10.2: only A2UI is stored and sent on).
func (h *HTTP) ScreenBlocks(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspace(w, r)
	if !ok {
		return
	}
	sid, err := parseUUID(r.URL.Query().Get("session"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "session is required")
		return
	}
	// Screens are private to the chat, like chat:ui_* events: only the
	// chat's own creator reads them.
	if !h.Sup.ownsChat(r.Context(), ws, requestUser(r), sid) {
		writeError(w, http.StatusNotFound, "chat not found")
		return
	}
	blocks, err := h.Sup.ScreenBlocks(r.Context(), ws, sid, 200)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"blocks": blocks})
}

// PressScreenAction: POST /api/streams/ui/actions/{id} {"confirm": false} —
// a screen button of the caller's own chat. A red action answers
// {"status":"confirm"} with the app's confirmation window; the window posts
// again with {"confirm": true, "window": <its id>}.
func (h *HTTP) PressScreenAction(w http.ResponseWriter, r *http.Request) {
	h.screenAction(w, r, func(ws, id pgtype.UUID) (PressResult, error) {
		var req struct {
			Confirm bool   `json:"confirm"`
			Window  string `json:"window"`
		}
		if r.ContentLength != 0 {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				return PressResult{}, errBadRequest
			}
		}
		return h.Sup.PressScreenAction(r.Context(), ws, requestUser(r), id, req.Confirm, req.Window)
	})
}

// CancelScreenAction: POST /api/streams/ui/actions/{id}/cancel — the
// confirmation window was closed.
func (h *HTTP) CancelScreenAction(w http.ResponseWriter, r *http.Request) {
	h.screenAction(w, r, func(ws, id pgtype.UUID) (PressResult, error) {
		return h.Sup.CancelScreenAction(r.Context(), ws, requestUser(r), id)
	})
}

var errBadRequest = errors.New("bad request")

// requestUser is the authenticated user set by the auth middleware; invalid
// when absent, which no chat matches.
func requestUser(r *http.Request) pgtype.UUID {
	u, _ := parseUUID(r.Header.Get("X-User-ID"))
	return u
}

func (h *HTTP) screenAction(w http.ResponseWriter, r *http.Request, run func(ws, id pgtype.UUID) (PressResult, error)) {
	ws, ok := h.workspace(w, r)
	if !ok {
		return
	}
	if h.Sup.Screens == nil {
		writeError(w, http.StatusNotFound, "screens are off")
		return
	}
	id, err := parseUUID(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "action id is required")
		return
	}
	res, err := run(ws, id)
	switch {
	case errors.Is(err, errBadRequest):
		writeError(w, http.StatusBadRequest, "body must be {\"confirm\": bool}")
	case errors.Is(err, ErrActionStale):
		writeError(w, http.StatusGone, err.Error())
	case err != nil:
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	default:
		writeJSON(w, http.StatusOK, res)
	}
}
