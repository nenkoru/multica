package genui

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestStoredTableMessage(t *testing.T) {
	reply, err := os.ReadFile("testdata/table-message.md")
	if err != nil {
		t.Fatal(err)
	}
	sources, _ := Extract(string(reply))
	if len(sources) != 1 {
		t.Fatalf("expected one screen, got %d", len(sources))
	}
	res := Finalize(sources[0], Options{}, nil)
	if path := os.Getenv("GENUI_REPRO_OUTPUT"); path != "" {
		raw, _ := json.MarshalIndent(res, "", "  ")
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if res.Status != StatusValid {
		t.Fatalf("stored message must produce a valid table: status=%s issues=%v doc=%+v", res.Status, res.Issues, res.Doc)
	}
	n, ok := res.Doc.Node("history")
	if !ok || n.Name() != "Table" {
		t.Fatal("history Table was discarded")
	}
	if rows, ok := n.Props()["rows"].([][]string); !ok || len(rows) != 4 || len(rows[0]) != 3 {
		t.Fatalf("expected 4 rows of 3 columns, got %v", n.Props()["rows"])
	}
}

func TestTableCellLimits(t *testing.T) {
	for _, tc := range []struct {
		name, cell string
		valid      bool
	}{
		{"long text", strings.Repeat("я", MaxTextRunes), true},
		{"too long", strings.Repeat("я", MaxTextRunes+1), false},
		{"foreign link", "https://evil.example", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := json.Marshal(tc.cell)
			r := Finalize(`root = Table(["column"], [[`+string(raw)+`]])`, Options{}, nil)
			if (r.Status == StatusValid) != tc.valid {
				t.Fatalf("status=%s issues=%v", r.Status, r.Issues)
			}
		})
	}
}

func TestEmptyContainersFail(t *testing.T) {
	for _, source := range []string{
		`root = Stack([])`,
		"root = Stack([missing])",
		"root = Stack([a])\na = Marquee(\"x\")",
		"root = Stack([a])\na = Stack([root])",
		"root = KpiStrip([a])\na = Text(\"wrong type\")",
	} {
		if r := Finalize(source, Options{}, nil); r.Status != StatusFailed {
			t.Errorf("%q: status=%s issues=%v", source, r.Status, r.Issues)
		}
	}
}

func TestTableForwardReferenceOrder(t *testing.T) {
	for _, source := range []string{
		"root = Stack([history])\nhistory = Table([\"c\"], [[\"value\"]])",
		"history = Table([\"c\"], [[\"value\"]])\nroot = Stack([history])",
	} {
		r := Finalize(source, Options{}, nil)
		if r.Status != StatusValid || !strings.Contains(Text(r.Doc), "value") {
			t.Fatalf("status=%s issues=%v text=%q", r.Status, r.Issues, Text(r.Doc))
		}
	}
}
