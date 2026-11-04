package genui

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const moneyScreen = `root = Stack([kpis, spend, acts])
kpis = KpiStrip([Kpi("За 14 дней", "$41.20", "-8%"), Kpi("Терн", "$18.00")])
spend = StackedBars("Расход по дням", ["22.10", "23.10"], [Series("Терн", [1.5, 3]), Series("buvg01", [2, 0.5])], "$")
acts = Actions([Button("Урезать долю Терна вдвое", set_share("tern", 0.5)), Button("Поднять бюджет", raise_budget("tern", 20))])`

func opts() Options {
	return Options{LinkHosts: []string{"github.com"}, StreamExists: func(s string) bool { return s == "tern" || s == "buvg01" }}
}

func TestParseLineValues(t *testing.T) {
	st, ok, err := ParseLine(`x = Kpi("a \"b\"", -1.5e2, [true, null, {k: "v", "q": [1,]}], ref_1)`)
	if err != nil || !ok {
		t.Fatalf("parse: %v %v", ok, err)
	}
	if st.ID != "x" || st.Expr.Kind != KCall || st.Expr.Name != "Kpi" || len(st.Expr.Args) != 4 {
		t.Fatalf("bad statement %+v", st)
	}
	a := st.Expr.Args
	if a[0].Str != `a "b"` || a[1].Num != -150 || a[2].Items[2].Fields["q"].Items[0].Num != 1 || a[3].Kind != KRef {
		t.Fatalf("bad args %+v", a)
	}
	for _, bad := range []string{`x Kpi()`, `x = Kpi(`, `x = "open`, `= Kpi()`, `x = Kpi() junk`, `x = @`} {
		if _, _, err := ParseLine(bad); err == nil {
			t.Errorf("%q: want error", bad)
		}
	}
	if _, ok, err := ParseLine("  # comment"); ok || err != nil {
		t.Errorf("comment: ok=%v err=%v", ok, err)
	}
	deep := "x = " + strings.Repeat("[", 40) + strings.Repeat("]", 40)
	if _, _, err := ParseLine(deep); err == nil {
		t.Error("deep nesting: want error")
	}
}

func TestBuildMoneyScreen(t *testing.T) {
	doc, issues, ok := Build(moneyScreen, opts())
	if !ok || len(issues) != 0 {
		t.Fatalf("ok=%v issues=%v", ok, issues)
	}
	if doc.Root != "root" || doc.Version != A2UIVersion {
		t.Fatalf("root %q version %q", doc.Root, doc.Version)
	}
	// Inline Kpi calls become their own nodes right after their statement.
	var ids []string
	for _, n := range doc.Components {
		ids = append(ids, n.ID)
	}
	if got := strings.Join(ids, ","); got != "root,kpis,kpis~1,kpis~2,spend,acts" {
		t.Fatalf("order %s", got)
	}
	bs := doc.Buttons()
	if len(bs) != 2 || bs[0].Action.Name != "set_share" || bs[0].Action.Zone() != "normal" || bs[1].Action.Zone() != "red" {
		t.Fatalf("buttons %+v", bs)
	}
	raw, err := json.Marshal(doc)
	if err != nil || !strings.Contains(string(raw), `"StackedBars"`) {
		t.Fatalf("json %s %v", raw, err)
	}
}

func TestBuildPrunesInvalid(t *testing.T) {
	src := `root = Stack([a, b, ghost, c])
a = Text("ok")
b = Marquee("нет такого")
c = Actions([Button("Куда-то", open("https://evil.example/x"))])
d = Text("ни к чему не привязан")`
	doc, issues, ok := Build(src, opts())
	if !ok {
		t.Fatal("root should survive")
	}
	n, _ := doc.Node("root")
	if got := strings.Join(n.Props()["children"].([]string), ","); got != "a" {
		t.Fatalf("children %s", got)
	}
	want := []string{"Marquee", "хост не из разрешённых", "неизвестный id \"ghost\"", "не связан с root"}
	all := ""
	for _, i := range issues {
		all += i.String() + "\n"
	}
	for _, w := range want {
		if !strings.Contains(all, w) {
			t.Errorf("issues miss %q:\n%s", w, all)
		}
	}
}

func TestBuildRejects(t *testing.T) {
	cases := map[string]string{
		"cycle":         "root = Stack([a])\na = Stack([root])",
		"bad action":    `root = Actions([Button("x", rm_rf("/"))])`,
		"unknown slug":  `root = Actions([Button("x", pause_stream("nope"))])`,
		"url in label":  `root = Text("смотри https://github.com/x")`,
		"share too big": `root = Actions([Button("x", set_share("tern", 9))])`,
		"wrong ref":     "root = KpiStrip([t])\nt = Text(\"x\")",
	}
	for name, src := range cases {
		_, issues, _ := Build(src, opts())
		if len(issues) == 0 {
			t.Errorf("%s: want issues", name)
		}
	}
	if _, _, ok := Build("root = Marquee()\na = Text(\"x\")", opts()); ok {
		t.Error("invalid root: want not ok")
	}
	if _, _, ok := Build("", opts()); ok {
		t.Error("empty: want not ok")
	}
}

func TestFinalizeRepairs(t *testing.T) {
	bad := "root = Stack([a])\na = Txt(\"x\")"
	calls := 0
	res := Finalize(bad, opts(), func(src string, issues []Issue) (string, error) {
		calls++
		if len(issues) == 0 || !strings.Contains(src, "Txt") {
			t.Errorf("repair input: %q %v", src, issues)
		}
		return "```openui\nroot = Stack([a])\na = Text(\"x\")\n```", nil
	})
	if res.Status != StatusRepaired || calls != 1 || len(res.Issues) != 0 {
		t.Fatalf("res %+v calls %d", res, calls)
	}
	calls = 0
	res = Finalize(bad, opts(), func(string, []Issue) (string, error) { calls++; return "", errors.New("down") })
	if res.Status != StatusPruned || calls != 1 {
		t.Fatalf("failed repair: %+v calls %d", res, calls)
	}
	calls = 0
	res = Finalize(bad, opts(), func(src string, _ []Issue) (string, error) { calls++; return src, nil })
	if calls != MaxRepairs {
		t.Fatalf("repairs capped at %d, got %d", MaxRepairs, calls)
	}
}

func TestStreamYieldsFinishedLines(t *testing.T) {
	reply := "Вот куда ушли деньги:\n```openui\nroot = Stack([kpis])\nkpis = KpiStrip([Kpi(\"a\", \"1\")])\n```\nИтого — Терн."
	var s Stream
	var got []StreamLine
	for i := 0; i < len(reply); i += 7 { // arbitrary chunking
		end := min(i+7, len(reply))
		got = append(got, s.Feed(reply[i:end])...)
	}
	if len(got) != 2 || got[0].Line != 1 || !strings.HasPrefix(got[1].Text, "kpis =") {
		t.Fatalf("lines %+v", got)
	}
	srcs, rest := Extract(reply)
	if len(srcs) != 1 || rest != "Вот куда ушли деньги:\n\nИтого — Терн." {
		t.Fatalf("extract %q %q", srcs, rest)
	}
	// Cut off mid-block: the lines are kept, the text has no fence.
	srcs, rest = Extract("Смотри:\n```openui\nroot = Text(\"x\")\n")
	if len(srcs) != 1 || strings.Contains(rest, "```") {
		t.Fatalf("open block %q %q", srcs, rest)
	}
}

func TestTextVersion(t *testing.T) {
	doc, _, _ := Build(moneyScreen, opts())
	txt := Text(doc)
	for _, w := range []string{"За 14 дней: $41.20 (-8%)", "Расход по дням (22.10–23.10): Терн $4.50 (64%), buvg01 $2.50 (36%)", "Кнопки в вебе: «Урезать долю Терна вдвое»"} {
		if !strings.Contains(txt, w) {
			t.Errorf("text misses %q:\n%s", w, txt)
		}
	}
}
