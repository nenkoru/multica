package genui

import (
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strings"
)

// The catalog is closed (§10.3, §14 "no arbitrary HTML"): a line naming
// anything else does not appear. Every component maps to Multica UI in
// packages/views/streams/genui.

type ptype int

const (
	pStr     ptype = iota // short label
	pText                 // longer text
	pNum                  // number
	pEnum                 // string from Enum
	pURL                  // link: http(s) on an allowed host or an app path
	pStrList              // [string]
	pNumList              // [number]
	pRef                  // id of a component (or an inline call of one)
	pRefList              // [id]
	pSeries               // [Series(name, [numbers])]
	pButtons              // [Button(label, action(...))]
	pRows                 // [[cell, …], …]
)

type param struct {
	Name     string
	Type     ptype
	Optional bool
	Enum     []string
	// Allowed restricts pRef / pRefList to these components; empty: any.
	Allowed []string
}

// Component describes one catalog entry.
type Component struct {
	Name   string
	Params []param
}

func req(name string, t ptype) param { return param{Name: name, Type: t} }
func opt(name string, t ptype) param { return param{Name: name, Type: t, Optional: true} }

// Catalog is every component a screen may use.
var Catalog = map[string]Component{}

func init() {
	for _, c := range []Component{
		{"Stack", []param{req("children", pRefList), {Name: "direction", Type: pEnum, Optional: true, Enum: []string{"column", "row"}}}},
		{"Text", []param{req("text", pText), {Name: "tone", Type: pEnum, Optional: true, Enum: []string{"default", "muted", "heading"}}}},
		{"KpiStrip", []param{{Name: "items", Type: pRefList, Allowed: []string{"Kpi"}}}},
		{"Kpi", []param{req("label", pStr), req("value", pStr), opt("delta", pStr), opt("hint", pStr)}},
		{"TrendChart", []param{req("title", pStr), req("labels", pStrList), req("series", pSeries), opt("unit", pStr)}},
		{"StackedBars", []param{req("title", pStr), req("labels", pStrList), req("series", pSeries), opt("unit", pStr)}},
		{"MilestoneCard", []param{req("text", pText), opt("evidence", pURL), opt("at", pStr), opt("stream", pStr)}},
		{"AskCard", []param{req("question", pText), req("options", pStrList), opt("ask_id", pStr), opt("default", pNum),
			{Name: "zone", Type: pEnum, Optional: true, Enum: []string{"normal", "red"}}}},
		{"StreamCard", []param{req("stream", pStr), req("title", pStr), req("state", pEnum), opt("goal", pText),
			{Name: "kpis", Type: pRef, Optional: true, Allowed: []string{"KpiStrip"}},
			{Name: "trend", Type: pRef, Optional: true, Allowed: []string{"TrendChart", "StackedBars"}},
			opt("now", pText), opt("next", pStrList), opt("link", pURL)}},
		{"Table", []param{req("columns", pStrList), req("rows", pRows)}},
		{"IssueRow", []param{req("identifier", pStr), req("title", pStr), {Name: "status", Type: pEnum, Optional: true,
			Enum: []string{"backlog", "todo", "in_progress", "in_review", "done", "blocked", "cancelled"}}, opt("link", pURL)}},
		{"AgentStack", []param{req("agents", pStrList), opt("label", pStr)}},
		{"Actions", []param{req("buttons", pButtons)}},
	} {
		Catalog[c.Name] = c
	}
	sc := Catalog["StreamCard"]
	sc.Params[2].Enum = StreamStates
	Catalog["StreamCard"] = sc
}

// StreamStates are the six stream states as stored plus "веха" (§5.1, §9.1 icons).
var StreamStates = []string{"running", "waiting", "asking", "budget", "paused", "broken", "milestone"}

// Action is a typed button action (§10.4). The list is closed.
type Action struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

type actionSpec struct {
	Params []param
}

var actions = map[string]actionSpec{
	"answer_ask":    {[]param{req("ask_id", pStr), req("choice", pNum)}},
	"focus_stream":  {[]param{req("stream", pStr)}},
	"open":          {[]param{req("url", pURL)}},
	"followup":      {[]param{req("text", pText)}},
	"pause_stream":  {[]param{req("stream", pStr)}},
	"resume_stream": {[]param{req("stream", pStr)}},
	"set_share":     {[]param{req("stream", pStr), req("factor", pNum)}},
	"raise_budget":  {[]param{req("stream", pStr), req("usd", pNum)}},
}

// ActionNames lists the closed action set, for prompts and tests.
func ActionNames() []string {
	return []string{"answer_ask", "focus_stream", "open", "followup", "pause_stream", "resume_stream", "set_share", "raise_budget"}
}

// Zone of an action as far as the screen alone can tell (§10.4). The
// server adds what it knows: answer_ask takes the question's zone.
func (a Action) Zone() string {
	switch a.Name {
	case "raise_budget":
		return "red"
	case "set_share":
		if f, _ := a.Args["factor"].(float64); f >= 1 {
			return "red"
		}
	}
	return "normal"
}

// Limits on what a screen may say.
const (
	MaxLabelRunes   = 80
	MaxTextRunes    = 1200
	MaxListItems    = 60
	MaxSeries       = 8
	MaxButtons      = 6
	MaxNodes        = 80
	MaxShareFactor  = 4
	MaxRaiseUSD     = 10000
	MaxFollowupRune = 300
)

// Options carry what the checker cannot know by itself.
type Options struct {
	// LinkHosts are the hosts an open(url) or evidence link may point to.
	// App paths ("/…") are always allowed.
	LinkHosts []string
	// StreamExists and AskOpen check action targets; nil accepts any.
	StreamExists func(slug string) bool
	AskOpen      func(askID string) bool
}

var urlInText = regexp.MustCompile(`(?i)(https?://|www\.)`)

func (o Options) checkURL(s string) error {
	if strings.HasPrefix(s, "/") && !strings.HasPrefix(s, "//") {
		return nil
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return fmt.Errorf("ссылка %q: нужен http(s)-адрес или путь приложения", trimTo(s, 40))
	}
	host := strings.ToLower(u.Hostname())
	for _, h := range o.LinkHosts {
		h = strings.ToLower(strings.TrimSpace(h))
		if h != "" && (host == h || strings.HasSuffix(host, "."+h)) {
			return nil
		}
	}
	return fmt.Errorf("ссылка на %s: хост не из разрешённых", host)
}

func checkLabel(s string, max int) error {
	if n := len([]rune(s)); n > max {
		return fmt.Errorf("надпись длиннее %d символов", max)
	}
	// Links live only in link fields: foreign text never becomes a link
	// or a clickable label (§10.4).
	if urlInText.MatchString(s) {
		return fmt.Errorf("ссылка в надписи %q: ссылки — только через open(url)", trimTo(s, 30))
	}
	return nil
}

// convert checks one value against its parameter and returns its A2UI form.
// Inline component calls in pRef / pRefList come back as extra nodes.
func (c *converter) convert(p param, v Value) (any, error) {
	switch p.Type {
	case pStr, pText, pEnum, pURL:
		if v.Kind == KNumber && (p.Type == pStr || p.Type == pText) {
			return fmtNum(v.Num), nil // Kpi("Шагов", 12) is fine
		}
		if v.Kind != KString {
			return nil, fmt.Errorf("%s: нужна строка", p.Name)
		}
		switch p.Type {
		case pStr:
			return v.Str, checkLabel(v.Str, MaxLabelRunes)
		case pText:
			return v.Str, checkLabel(v.Str, MaxTextRunes)
		case pEnum:
			for _, e := range p.Enum {
				if v.Str == e {
					return v.Str, nil
				}
			}
			return nil, fmt.Errorf("%s: %q не из %v", p.Name, v.Str, p.Enum)
		default:
			return v.Str, c.opts.checkURL(v.Str)
		}
	case pNum:
		if v.Kind != KNumber || math.IsNaN(v.Num) || math.IsInf(v.Num, 0) {
			return nil, fmt.Errorf("%s: нужно число", p.Name)
		}
		return v.Num, nil
	case pStrList:
		if v.Kind != KArray || len(v.Items) > MaxListItems {
			return nil, fmt.Errorf("%s: нужен список строк (до %d)", p.Name, MaxListItems)
		}
		out := make([]string, 0, len(v.Items))
		for _, it := range v.Items {
			s, err := c.convert(param{Name: p.Name, Type: pStr}, it)
			if err != nil {
				return nil, err
			}
			out = append(out, s.(string))
		}
		return out, nil
	case pNumList:
		if v.Kind != KArray || len(v.Items) > MaxListItems {
			return nil, fmt.Errorf("%s: нужен список чисел (до %d)", p.Name, MaxListItems)
		}
		out := make([]float64, 0, len(v.Items))
		for _, it := range v.Items {
			if it.Kind == KNull {
				out = append(out, 0)
				continue
			}
			n, err := c.convert(param{Name: p.Name, Type: pNum}, it)
			if err != nil {
				return nil, err
			}
			out = append(out, n.(float64))
		}
		return out, nil
	case pRef:
		return c.ref(p, v)
	case pRefList:
		if v.Kind != KArray || len(v.Items) > MaxListItems {
			return nil, fmt.Errorf("%s: нужен список id", p.Name)
		}
		out := make([]string, 0, len(v.Items))
		for _, it := range v.Items {
			id, err := c.ref(p, it)
			if err != nil {
				return nil, err
			}
			out = append(out, id)
		}
		return out, nil
	case pSeries:
		if v.Kind != KArray || len(v.Items) == 0 || len(v.Items) > MaxSeries {
			return nil, fmt.Errorf("%s: нужно от 1 до %d Series(…)", p.Name, MaxSeries)
		}
		out := make([]map[string]any, 0, len(v.Items))
		for _, it := range v.Items {
			if it.Kind != KCall || it.Name != "Series" || len(it.Args) != 2 {
				return nil, fmt.Errorf("%s: нужен Series(\"имя\", [числа])", p.Name)
			}
			name, err := c.convert(req("name", pStr), it.Args[0])
			if err != nil {
				return nil, err
			}
			vals, err := c.convert(req("values", pNumList), it.Args[1])
			if err != nil {
				return nil, err
			}
			out = append(out, map[string]any{"name": name, "values": vals})
		}
		return out, nil
	case pButtons:
		if v.Kind != KArray || len(v.Items) == 0 || len(v.Items) > MaxButtons {
			return nil, fmt.Errorf("%s: нужно от 1 до %d Button(…)", p.Name, MaxButtons)
		}
		out := make([]map[string]any, 0, len(v.Items))
		for _, it := range v.Items {
			b, err := c.button(it)
			if err != nil {
				return nil, err
			}
			out = append(out, b)
		}
		return out, nil
	case pRows:
		if v.Kind != KArray || len(v.Items) > MaxListItems {
			return nil, fmt.Errorf("%s: нужен список строк таблицы", p.Name)
		}
		out := make([][]string, 0, len(v.Items))
		for _, row := range v.Items {
			cells, err := c.convert(req(p.Name, pStrList), row)
			if err != nil {
				return nil, err
			}
			out = append(out, cells.([]string))
		}
		return out, nil
	}
	return nil, fmt.Errorf("%s: неизвестный тип", p.Name)
}

func (c *converter) ref(p param, v Value) (string, error) {
	switch v.Kind {
	case KRef:
		c.refs = append(c.refs, refUse{id: v.Name, allowed: p.Allowed})
		return v.Name, nil
	case KCall:
		if err := allowed(p, v.Name); err != nil {
			return "", err
		}
		c.inline++
		id := fmt.Sprintf("%s~%d", c.id, c.inline)
		node, err := c.node(id, v)
		if err != nil {
			return "", err
		}
		c.extra = append(c.extra, node)
		return id, nil
	}
	return "", fmt.Errorf("%s: нужен id компонента", p.Name)
}

func allowed(p param, name string) error {
	if len(p.Allowed) == 0 {
		return nil
	}
	for _, a := range p.Allowed {
		if a == name {
			return nil
		}
	}
	return fmt.Errorf("%s: %s здесь нельзя, только %v", p.Name, name, p.Allowed)
}

func (c *converter) button(v Value) (map[string]any, error) {
	if v.Kind != KCall || v.Name != "Button" || len(v.Args) != 2 {
		return nil, fmt.Errorf("кнопка: нужен Button(\"надпись\", действие(…))")
	}
	label, err := c.convert(req("label", pStr), v.Args[0])
	if err != nil {
		return nil, err
	}
	act, err := c.action(v.Args[1])
	if err != nil {
		return nil, err
	}
	return map[string]any{"label": label, "action": act, "zone": act.Zone()}, nil
}

func (c *converter) action(v Value) (Action, error) {
	if v.Kind != KCall {
		return Action{}, fmt.Errorf("действие: нужен вызов, например focus_stream(\"tern\")")
	}
	spec, ok := actions[v.Name]
	if !ok {
		return Action{}, fmt.Errorf("действия %q нет в списке %v", v.Name, ActionNames())
	}
	if len(v.Args) != len(spec.Params) {
		return Action{}, fmt.Errorf("%s: нужно аргументов — %d", v.Name, len(spec.Params))
	}
	a := Action{Name: v.Name, Args: map[string]any{}}
	for i, p := range spec.Params {
		val, err := c.convert(p, v.Args[i])
		if err != nil {
			return Action{}, fmt.Errorf("%s: %w", v.Name, err)
		}
		a.Args[p.Name] = val
	}
	if slug, ok := a.Args["stream"].(string); ok && c.opts.StreamExists != nil && !c.opts.StreamExists(slug) {
		return Action{}, fmt.Errorf("%s: потока %q нет", v.Name, slug)
	}
	switch v.Name {
	case "answer_ask":
		if c.opts.AskOpen != nil && !c.opts.AskOpen(a.Args["ask_id"].(string)) {
			return Action{}, fmt.Errorf("answer_ask: вопрос %v не открыт", a.Args["ask_id"])
		}
		if n := a.Args["choice"].(float64); n < 0 || n != math.Trunc(n) {
			return Action{}, fmt.Errorf("answer_ask: choice — номер варианта с 0")
		}
	case "set_share":
		if f := a.Args["factor"].(float64); f <= 0 || f > MaxShareFactor {
			return Action{}, fmt.Errorf("set_share: factor от 0 до %d", MaxShareFactor)
		}
	case "raise_budget":
		if u := a.Args["usd"].(float64); u <= 0 || u > MaxRaiseUSD {
			return Action{}, fmt.Errorf("raise_budget: usd от 0 до %d", MaxRaiseUSD)
		}
	case "followup":
		if len([]rune(a.Args["text"].(string))) > MaxFollowupRune {
			return Action{}, fmt.Errorf("followup: текст длиннее %d символов", MaxFollowupRune)
		}
	}
	return a, nil
}

func fmtNum(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return fmt.Sprintf("%d", int64(f))
	}
	return fmt.Sprintf("%g", f)
}

// LinkOK reports whether a screen may link to s.
func (o Options) LinkOK(s string) bool { return o.checkURL(s) == nil }
