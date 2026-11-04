// Package genui is the screens-in-chat pipeline of the Fabrika harness
// (harness-design.md §10): a model writes OpenUI Lang one component per line,
// the server checks every finished line against a closed catalog and streams
// it to the web, and at the end compiles the whole screen to A2UI — the only
// form that is stored and sent on.
//
// The language is the subset of OpenUI Lang 1.0 (Thesys, MIT) the Tern
// parser understands (§10.2, §15 #4):
//
//	root = Stack([kpis, spend, acts])
//	kpis = KpiStrip([Kpi("За 14 дней", "$41.20", "-8%"), Kpi("Терн", "$18.00")])
//	spend = StackedBars("Расход по дням, $", ["22.10", "23.10"], [Series("Терн", [1.2, 3.4])])
//	acts = Actions([Button("Урезать долю Терна вдвое", set_share("tern", 0.5))])
//
// A statement is `id = expression` on one line. Expressions are strings
// (JSON escapes), numbers, true / false / null, arrays, objects
// `{key: value}`, calls `Name(arg, …)` and bare identifiers, which refer to
// another statement and may point forward.
package genui

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Kind is the type of a parsed value.
type Kind int

const (
	KString Kind = iota
	KNumber
	KBool
	KNull
	KArray
	KObject
	KCall
	KRef
)

// Value is one parsed expression.
type Value struct {
	Kind   Kind
	Str    string
	Num    float64
	Bool   bool
	Items  []Value
	Keys   []string // object keys in source order
	Fields map[string]Value
	Name   string // call or reference name
	Args   []Value
}

// Statement is one finished line: `ID = Expr`.
type Statement struct {
	Line int
	ID   string
	Expr Value
}

// MaxLineBytes bounds one statement; longer lines are rejected unparsed.
const MaxLineBytes = 16 << 10

// ParseLine parses one statement. Blank lines and `#` / `//` comments return
// ok == false with no error.
func ParseLine(line string) (st Statement, ok bool, err error) {
	s := strings.TrimSpace(line)
	if s == "" || strings.HasPrefix(s, "#") || strings.HasPrefix(s, "//") {
		return Statement{}, false, nil
	}
	if len(s) > MaxLineBytes {
		return Statement{}, false, fmt.Errorf("строка длиннее %d байт", MaxLineBytes)
	}
	p := &parser{src: s}
	id := p.ident()
	if id == "" {
		return Statement{}, false, fmt.Errorf("ожидалось «id = Компонент(…)»")
	}
	p.space()
	if !p.eat('=') {
		return Statement{}, false, fmt.Errorf("после %q ожидалось «=»", id)
	}
	v, err := p.value(0)
	if err != nil {
		return Statement{}, false, err
	}
	p.space()
	if p.pos < len(p.src) {
		return Statement{}, false, fmt.Errorf("лишнее в конце строки: %q", trimTo(p.src[p.pos:], 24))
	}
	return Statement{ID: id, Expr: v}, true, nil
}

// maxDepth bounds nesting so a hostile line cannot exhaust the stack.
const maxDepth = 32

type parser struct {
	src string
	pos int
}

func (p *parser) space() {
	for p.pos < len(p.src) {
		r, n := utf8.DecodeRuneInString(p.src[p.pos:])
		if !unicode.IsSpace(r) {
			return
		}
		p.pos += n
	}
}

func (p *parser) peek() byte {
	if p.pos >= len(p.src) {
		return 0
	}
	return p.src[p.pos]
}

func (p *parser) eat(c byte) bool {
	p.space()
	if p.peek() == c {
		p.pos++
		return true
	}
	return false
}

func isIdentStart(c byte) bool { return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }
func isIdentPart(c byte) bool  { return isIdentStart(c) || (c >= '0' && c <= '9') }

func (p *parser) ident() string {
	p.space()
	start := p.pos
	if p.pos >= len(p.src) || !isIdentStart(p.src[p.pos]) {
		return ""
	}
	for p.pos < len(p.src) && isIdentPart(p.src[p.pos]) {
		p.pos++
	}
	return p.src[start:p.pos]
}

func (p *parser) value(depth int) (Value, error) {
	if depth > maxDepth {
		return Value{}, fmt.Errorf("слишком глубокая вложенность")
	}
	p.space()
	switch c := p.peek(); {
	case c == 0:
		return Value{}, fmt.Errorf("строка оборвалась")
	case c == '"':
		s, err := p.str()
		return Value{Kind: KString, Str: s}, err
	case c == '[':
		p.pos++
		items, err := p.list(']', depth)
		return Value{Kind: KArray, Items: items}, err
	case c == '{':
		return p.object(depth)
	case c == '-' || c == '+' || (c >= '0' && c <= '9'):
		return p.number()
	case isIdentStart(c):
		name := p.ident()
		switch name {
		case "true", "false":
			return Value{Kind: KBool, Bool: name == "true"}, nil
		case "null":
			return Value{Kind: KNull}, nil
		}
		if p.eat('(') {
			args, err := p.list(')', depth)
			return Value{Kind: KCall, Name: name, Args: args}, err
		}
		return Value{Kind: KRef, Name: name}, nil
	default:
		return Value{}, fmt.Errorf("неожиданный символ %q", string(rune(c)))
	}
}

func (p *parser) list(end byte, depth int) ([]Value, error) {
	var out []Value
	if p.eat(end) {
		return out, nil
	}
	for {
		v, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		if p.eat(',') {
			if p.eat(end) { // trailing comma
				return out, nil
			}
			continue
		}
		if p.eat(end) {
			return out, nil
		}
		return nil, fmt.Errorf("ожидалось «,» или %q", string(end))
	}
}

func (p *parser) object(depth int) (Value, error) {
	p.pos++ // {
	v := Value{Kind: KObject, Fields: map[string]Value{}}
	if p.eat('}') {
		return v, nil
	}
	for {
		p.space()
		var key string
		if p.peek() == '"' {
			k, err := p.str()
			if err != nil {
				return Value{}, err
			}
			key = k
		} else if key = p.ident(); key == "" {
			return Value{}, fmt.Errorf("ожидался ключ объекта")
		}
		if !p.eat(':') {
			return Value{}, fmt.Errorf("после ключа %q ожидалось «:»", key)
		}
		val, err := p.value(depth + 1)
		if err != nil {
			return Value{}, err
		}
		if _, dup := v.Fields[key]; !dup {
			v.Keys = append(v.Keys, key)
		}
		v.Fields[key] = val
		if p.eat(',') {
			if p.eat('}') {
				return v, nil
			}
			continue
		}
		if p.eat('}') {
			return v, nil
		}
		return Value{}, fmt.Errorf("ожидалось «,» или «}»")
	}
}

func (p *parser) str() (string, error) {
	start := p.pos
	p.pos++ // opening quote
	for p.pos < len(p.src) {
		switch p.src[p.pos] {
		case '\\':
			p.pos += 2
		case '"':
			p.pos++
			s, err := strconv.Unquote(p.src[start:p.pos])
			if err != nil {
				return "", fmt.Errorf("плохая строка %s", trimTo(p.src[start:p.pos], 24))
			}
			return s, nil
		default:
			p.pos++
		}
	}
	return "", fmt.Errorf("незакрытая строка")
}

func (p *parser) number() (Value, error) {
	start := p.pos
	if c := p.peek(); c == '-' || c == '+' {
		p.pos++
	}
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if (c >= '0' && c <= '9') || c == '.' || c == 'e' || c == 'E' || ((c == '-' || c == '+') && (p.src[p.pos-1] == 'e' || p.src[p.pos-1] == 'E')) {
			p.pos++
			continue
		}
		break
	}
	f, err := strconv.ParseFloat(p.src[start:p.pos], 64)
	if err != nil {
		return Value{}, fmt.Errorf("плохое число %q", p.src[start:p.pos])
	}
	return Value{Kind: KNumber, Num: f}, nil
}

func trimTo(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
