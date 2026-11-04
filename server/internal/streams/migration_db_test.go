package streams

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestNormalizeMigrate(t *testing.T) {
	got, err := NormalizeMigrate([]string{" rusl-1768", "RUSL-2269", "RUSL-1768", ""})
	if err != nil || strings.Join(got, ",") != "RUSL-1768,RUSL-2269" {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := NormalizeMigrate([]string{"RUSL-1; drop"}); err == nil {
		t.Fatal("garbage accepted")
	}
	many := make([]string, MaxMigrateIssues+1)
	for i := range many {
		many[i] = "RUSL-" + strings.Repeat("1", i+1)
	}
	if _, err := NormalizeMigrate(many); err == nil {
		t.Fatal("over the cap accepted")
	}
}

func (h *harness) upsert(body map[string]any) *httptest.ResponseRecorder {
	h.t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/streams/"+h.stream.Slug, bytes.NewReader(raw))
	rc := chi.NewRouteContext()
	rc.URLParams.Add("slug", h.stream.Slug)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))
	rec := httptest.NewRecorder()
	api := &HTTP{Sup: h.sup, WorkspaceID: func(*http.Request) string { return testWorkspaceID }}
	api.Upsert(rec, req)
	return rec
}

func TestMigrationTurnIsTheFirstAndOnlyTurn(t *testing.T) {
	h := newHarness(t, "migrate")
	var charter Charter
	_ = json.Unmarshal(h.stream.Charter, &charter)
	body := map[string]any{
		"title": h.stream.Title, "charter": charter, "manager_agent_id": h.agents["manager"],
		"migrate": []string{"rusl-1768", "RUSL-2269", "RUSL-1768"},
	}
	if rec := h.upsert(map[string]any{"title": "x", "charter": charter, "migrate": []string{"нет"}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad identifier: %d %s", rec.Code, rec.Body)
	}
	if n := len(h.open()); n != 0 {
		t.Fatalf("rejected request queued %d steps", n)
	}
	if rec := h.upsert(body); rec.Code != http.StatusOK {
		t.Fatalf("upsert: %d %s", rec.Code, rec.Body)
	}
	h.tick()
	h.tick()
	open := h.open()
	if len(open) != 1 {
		t.Fatalf("want only the migration turn, got %d steps", len(open))
	}
	mg := open[0]
	if mg.Kind != KindSummarize || mg.Origin != OriginMigration || mg.Status != StepRunning {
		t.Fatalf("step %s/%s/%s", mg.Kind, mg.Origin, mg.Status)
	}
	if len(h.carrier.dispatched) != 1 {
		t.Fatalf("dispatched %d", len(h.carrier.dispatched))
	}
	prompt := h.carrier.dispatched[0].Body
	for _, want := range []string{"Переезд с доски (§12)", "RUSL-1768, RUSL-2269.", "migrated: …"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt lacks %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "Это ход сжатия") {
		t.Fatal("migration turn told to compact an empty summary")
	}
	if h.journalCount("charter") != 1 {
		t.Fatalf("charter journal %d", h.journalCount("charter"))
	}

	// The turn's output lands like any manager turn: summary and queue.
	h.finish(mg, contract(map[string]any{
		"result":  "перенесены: RUSL-1768; migrated: RUSL-2269",
		"summary": "Поиск работы: шорт-лист от 24.10, откликов 0.",
		"next":    []map[string]any{{"brief": "шорт-лист недели", "kind": "work", "cost_class": "S", "moves": "отклики в неделю"}},
	}))
	h.tick()
	if st := h.reload(); !strings.Contains(st.Summary, "шорт-лист от 24.10") {
		t.Fatalf("summary %q", st.Summary)
	}
	if w := h.only(KindWork, StepRunning); !strings.Contains(w.Brief, "шорт-лист недели") {
		t.Fatalf("work step %q", w.Brief)
	}
}
