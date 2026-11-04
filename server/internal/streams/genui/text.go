package genui

import (
	"fmt"
	"sort"
	"strings"
)

// Text renders a screen as plain text for Telegram (§10.5: in the MVP a
// text version plus a link to open it live). Buttons are not offered there:
// screen actions run from the web.
func Text(d Doc) string {
	var b strings.Builder
	seen := map[string]bool{}
	var walk func(id string, depth int)
	walk = func(id string, depth int) {
		if seen[id] {
			return
		}
		seen[id] = true
		n, ok := d.Node(id)
		if !ok {
			return
		}
		p := n.Props()
		switch n.Name() {
		case "Stack":
			for _, c := range strs(p["children"]) {
				walk(c, depth)
			}
		case "Text":
			line(&b, str(p["text"]))
		case "KpiStrip":
			var parts []string
			for _, c := range strs(p["items"]) {
				seen[c] = true
				if k, ok := d.Node(c); ok {
					parts = append(parts, kpiText(k.Props()))
				}
			}
			line(&b, strings.Join(parts, " · "))
		case "Kpi":
			line(&b, kpiText(p))
		case "TrendChart", "StackedBars":
			line(&b, chartText(p))
		case "MilestoneCard":
			s := "✓ " + str(p["text"])
			if at := str(p["at"]); at != "" {
				s += " (" + at + ")"
			}
			if ev := str(p["evidence"]); ev != "" {
				s += " — " + ev
			}
			line(&b, s)
		case "AskCard":
			s := "? " + str(p["question"])
			for i, o := range strs(p["options"]) {
				s += fmt.Sprintf("\n   %c) %s", 'а'+rune(i), o)
			}
			line(&b, s)
		case "StreamCard":
			s := fmt.Sprintf("%s — %s", str(p["title"]), StateWord(str(p["state"])))
			if g := str(p["goal"]); g != "" {
				s += "\nЦель: " + g
			}
			line(&b, s)
			if k := str(p["kpis"]); k != "" {
				walk(k, depth+1)
			}
			if t := str(p["trend"]); t != "" {
				walk(t, depth+1)
			}
			if now := str(p["now"]); now != "" {
				line(&b, "Сейчас: "+now)
			}
			if next := strs(p["next"]); len(next) > 0 {
				line(&b, "Дальше: "+strings.Join(next, "; "))
			}
		case "Table":
			line(&b, strings.Join(strs(p["columns"]), " | "))
			if rows, ok := p["rows"].([][]string); ok {
				for _, r := range rows {
					line(&b, strings.Join(r, " | "))
				}
			}
		case "IssueRow":
			line(&b, fmt.Sprintf("%s %s", str(p["identifier"]), str(p["title"])))
		case "AgentStack":
			line(&b, firstNonEmpty(str(p["label"]), "Исполнители")+": "+strings.Join(strs(p["agents"]), ", "))
		case "Actions":
			var labels []string
			for _, bt := range buttons(p) {
				labels = append(labels, "«"+str(bt["label"])+"»")
			}
			if len(labels) > 0 {
				line(&b, "Кнопки в вебе: "+strings.Join(labels, ", "))
			}
		}
	}
	walk(d.Root, 0)
	return strings.TrimSpace(b.String())
}

// StateWord names a StreamCard state in Russian (§5.1).
func StateWord(s string) string {
	switch s {
	case "running":
		return "идёт"
	case "waiting":
		return "ждёт условия"
	case "asking":
		return "ждёт тебя"
	case "budget":
		return "бюджет"
	case "paused":
		return "пауза"
	case "broken":
		return "авария"
	case "milestone":
		return "веха"
	}
	return s
}

func kpiText(p map[string]any) string {
	s := str(p["label"]) + ": " + str(p["value"])
	if d := str(p["delta"]); d != "" {
		s += " (" + d + ")"
	}
	return s
}

// chartText sums each series: the totals are what a reader takes from a
// chart in a text message.
func chartText(p map[string]any) string {
	unit := str(p["unit"])
	type tot struct {
		name string
		sum  float64
	}
	var tots []tot
	all := 0.0
	series, _ := p["series"].([]map[string]any)
	for _, s := range series {
		vals, _ := s["values"].([]float64)
		sum := 0.0
		for _, v := range vals {
			sum += v
		}
		tots = append(tots, tot{str(s["name"]), sum})
		all += sum
	}
	sort.SliceStable(tots, func(i, j int) bool { return tots[i].sum > tots[j].sum })
	var parts []string
	for _, t := range tots {
		part := t.name + " " + fmtAmount(t.sum, unit)
		if all > 0 && len(tots) > 1 {
			part += fmt.Sprintf(" (%.0f%%)", t.sum/all*100)
		}
		parts = append(parts, part)
	}
	labels := strs(p["labels"])
	period := ""
	if len(labels) > 1 {
		period = fmt.Sprintf(" (%s–%s)", labels[0], labels[len(labels)-1])
	}
	return str(p["title"]) + period + ": " + strings.Join(parts, ", ")
}

func fmtAmount(v float64, unit string) string {
	s := fmt.Sprintf("%.2f", v)
	if v == float64(int64(v)) {
		s = fmt.Sprintf("%d", int64(v))
	}
	if unit == "$" || unit == "USD" {
		return "$" + s
	}
	if unit != "" {
		return s + " " + unit
	}
	return s
}

func line(b *strings.Builder, s string) {
	if s = strings.TrimSpace(s); s != "" {
		b.WriteString(s + "\n")
	}
}

func str(v any) string { s, _ := v.(string); return s }

func strs(v any) []string { s, _ := v.([]string); return s }

func buttons(p map[string]any) []map[string]any {
	bs, _ := p["buttons"].([]map[string]any)
	return bs
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
