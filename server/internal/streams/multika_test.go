package streams

import (
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/streams/sdb"
)

func testStreams() []sdb.Stream {
	mk := func(b byte, slug, title string) sdb.Stream {
		return sdb.Stream{ID: pgtype.UUID{Bytes: [16]byte{b}, Valid: true}, Slug: slug, Title: title, Active: true, State: StateRunning}
	}
	return []sdb.Stream{
		mk(1, "platform", "Платформа"),
		mk(2, "buvg01", "buvg01: боевая 27B"),
		mk(3, "tern", "Терн"),
		mk(4, "rabota", "Работа"),
	}
}

func TestParseFocusEnter(t *testing.T) {
	cases := map[string]string{
		"Давай про Терн":                     "tern",
		"давай про терна, что там с нодами?": "tern",
		"поговорим о работе":                 "rabota",
		"переключись на buvg01":              "buvg01",
		"давай про buvg01: что с бенчмарком": "buvg01",
		"про платформу":                      "platform",
		"Давай про поток Платформа":          "platform",
		"что с потоком работа":               "rabota",
	}
	for text, want := range cases {
		got := ParseFocus(text, testStreams())
		if got.Kind != FocusEnter || got.Stream == nil || got.Stream.Slug != want {
			t.Errorf("%q: want enter %s, got %+v", text, want, got)
		}
	}
}

func TestParseFocusExit(t *testing.T) {
	for _, text := range []string{"всё", "Все.", "назад", "хватит!", "всё, спасибо"} {
		if got := ParseFocus(text, testStreams()); got.Kind != FocusExit {
			t.Errorf("%q: want exit, got %+v", text, got)
		}
	}
}

func TestParseFocusLeavesPlainSpeechAlone(t *testing.T) {
	for _, text := range []string{
		"всё понятно, а что с деньгами?", // not a bare exit
		"Терн сегодня как?",              // a mention does not move the focus
		"про это потом",                  // "про" + unknown word is plain speech
		"давай о том, что сделано",
		"давай по порядку",
		"перейдём к делу",
		"поговорим о погоде",
		"1 а, 2 да",
		"",
	} {
		if got := ParseFocus(text, testStreams()); got.Kind != FocusNone || got.Target != "" {
			t.Errorf("%q: want nothing, got %+v", text, got)
		}
	}
}

func TestParseFocusUnknownAndAmbiguous(t *testing.T) {
	got := ParseFocus("давай про Варден", testStreams())
	if got.Kind != FocusNone || got.Target != "варден" {
		t.Fatalf("unknown stream: %+v", got)
	}
	two := append(testStreams(), sdb.Stream{ID: pgtype.UUID{Bytes: [16]byte{5}, Valid: true}, Slug: "tern-lab", Title: "Терн-лаб"})
	// "Терн-лаб" splits into "терн" + "лаб", so both streams match and
	// Мультика must ask back.
	got = ParseFocus("давай про терн", two)
	if got.Kind != FocusNone || len(got.Ambiguous) != 2 || got.Target != "терн" {
		t.Fatalf("want ambiguous between two, got %+v", got)
	}
}

func TestMultikaContextFocusAndActions(t *testing.T) {
	sts := testStreams()
	sts[2].Summary = strings.Repeat("я", MaxSummaryInContext+50)
	sts[2].Charter = []byte(`{"goal":"тестнет живой","metric":"финальность","done":"e2e зелёный"}`)
	now := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	out := MultikaContext(sts, MultikaFocus{Stream: &sts[2], Until: now.Add(FocusIdle)}, MultikaTurnActions{Entered: "Терн", Routed: "Терн", RoutedSeq: 7}, now)
	for _, want := range []string{"Потоки (4 из 5 активны)", "Фокус: Терн до 12:30 МСК", "цель — тестнет живой", "фокус поставлен на «Терн»", "шаг 7", "…"} {
		if !strings.Contains(out, want) {
			t.Errorf("context lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, strings.Repeat("я", MaxSummaryInContext+1)) {
		t.Error("summary is not trimmed")
	}
	plain := MultikaContext(sts, MultikaFocus{}, MultikaTurnActions{Unknown: "варден"}, now)
	if !strings.Contains(plain, "Фокуса нет") || !strings.Contains(plain, "потока «варден» нет") {
		t.Errorf("no-focus context:\n%s", plain)
	}
}
