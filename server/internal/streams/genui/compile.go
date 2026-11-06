package genui

import (
	"fmt"
	"sort"
	"strings"
)

// A2UIVersion names the stored form: the A2UI 0.8 component list (flat,
// id-addressed, children by id) with the Fabrika catalog as its vocabulary.
const A2UIVersion = "a2ui-0.8+fabrika-1"

// Node is one A2UI component: {"id": "kpis", "component": {"KpiStrip": {...}}}.
type Node struct {
	ID        string                    `json:"id"`
	Component map[string]map[string]any `json:"component"`
}

// Name is the catalog component of the node.
func (n Node) Name() string {
	for k := range n.Component {
		return k
	}
	return ""
}

// Props are the node's checked properties.
func (n Node) Props() map[string]any { return n.Component[n.Name()] }

// Doc is a compiled screen: what ui_block stores and ui.final sends.
type Doc struct {
	Version    string `json:"version"`
	Root       string `json:"root"`
	Components []Node `json:"components"`
}

// Node finds a component by id.
func (d Doc) Node(id string) (Node, bool) {
	for _, n := range d.Components {
		if n.ID == id {
			return n, true
		}
	}
	return Node{}, false
}

// Issue is one problem the checker found.
type Issue struct {
	Line int    `json:"line"`
	ID   string `json:"id,omitempty"`
	Msg  string `json:"msg"`
}

func (i Issue) String() string {
	if i.ID != "" {
		return fmt.Sprintf("строка %d (%s): %s", i.Line, i.ID, i.Msg)
	}
	return fmt.Sprintf("строка %d: %s", i.Line, i.Msg)
}

type refUse struct {
	id      string
	allowed []string
}

type converter struct {
	opts   Options
	id     string
	inline int
	extra  []Node
	refs   []refUse
}

func (c *converter) node(id string, v Value) (Node, error) {
	if v.Kind != KCall {
		return Node{}, fmt.Errorf("ожидался компонент, например Text(\"…\")")
	}
	comp, ok := Catalog[v.Name]
	if !ok {
		return Node{}, fmt.Errorf("компонента %q нет в каталоге", v.Name)
	}
	if len(v.Args) > len(comp.Params) {
		return Node{}, fmt.Errorf("%s: аргументов больше, чем %d", v.Name, len(comp.Params))
	}
	props := map[string]any{}
	for i, p := range comp.Params {
		if i >= len(v.Args) || v.Args[i].Kind == KNull {
			if !p.Optional {
				return Node{}, fmt.Errorf("%s: нет обязательного %s", v.Name, p.Name)
			}
			continue
		}
		val, err := c.convert(p, v.Args[i])
		if err != nil {
			return Node{}, fmt.Errorf("%s: %w", v.Name, err)
		}
		props[p.Name] = val
	}
	return Node{ID: id, Component: map[string]map[string]any{v.Name: props}}, nil
}

// Line is one statement converted on its own, as the stream sees it:
// references are not resolved yet (a forward reference is an empty place
// until its line arrives, §10.2).
type Line struct {
	Statement
	Nodes []Node // the statement's node first, then its inline children
	refs  []refUse
}

// CheckLine converts one statement against the catalog.
func CheckLine(st Statement, opts Options) (Line, error) {
	c := &converter{opts: opts, id: st.ID}
	n, err := c.node(st.ID, st.Expr)
	if err != nil {
		return Line{}, err
	}
	return Line{Statement: st, Nodes: append([]Node{n}, c.extra...), refs: c.refs}, nil
}

// Parse splits source into statements; lines that do not parse come back as
// issues.
func Parse(source string) ([]Statement, []Issue) {
	var out []Statement
	var issues []Issue
	for i, raw := range strings.Split(source, "\n") {
		st, ok, err := ParseLine(raw)
		if err != nil {
			issues = append(issues, Issue{Line: i + 1, Msg: err.Error()})
			continue
		}
		if ok {
			st.Line = i + 1
			out = append(out, st)
		}
	}
	return out, issues
}

// Build compiles a whole screen (§10.2 step 3). It returns the screen made
// of what is valid — invalid lines, dangling references and unreachable
// components are dropped — and every issue found, for the repair round.
// ok is false when nothing renderable is left.
func Build(source string, opts Options) (doc Doc, issues []Issue, ok bool) {
	stmts, issues := Parse(source)
	lines := map[string]Line{}
	var order []string
	for _, st := range stmts {
		if _, dup := lines[st.ID]; dup {
			issues = append(issues, Issue{Line: st.Line, ID: st.ID, Msg: "id уже занят"})
			continue
		}
		l, err := CheckLine(st, opts)
		if err != nil {
			issues = append(issues, Issue{Line: st.Line, ID: st.ID, Msg: err.Error()})
			continue
		}
		lines[st.ID] = l
		order = append(order, st.ID)
	}
	if len(stmts) == 0 {
		issues = append(issues, Issue{Line: 1, Msg: "экран пуст"})
		return Doc{}, issues, false
	}
	root := "root"
	if _, has := lines[root]; !has {
		if hasStmt(stmts, root) {
			// root was written but is invalid: nothing to hang the rest on.
			return Doc{}, issues, false
		}
		if len(order) == 0 {
			return Doc{}, issues, false
		}
		root = order[0]
		issues = append(issues, Issue{Line: lines[root].Line, ID: root, Msg: "нет строки root = …; корнем взят первый компонент"})
	}

	// All nodes by id: statement nodes and their inline children.
	nodes := map[string]Node{}
	owner := map[string]string{}
	for _, id := range order {
		for _, n := range lines[id].Nodes {
			nodes[n.ID] = n
			owner[n.ID] = id
		}
	}
	// Reference type checks (Allowed) need the target's component.
	for _, id := range order {
		for _, r := range lines[id].refs {
			t, ok := nodes[r.id]
			if !ok {
				issues = append(issues, Issue{Line: lines[id].Line, ID: id, Msg: fmt.Sprintf("ссылка на неизвестный id %q", r.id)})
				continue
			}
			if err := allowed(param{Name: "ссылка", Allowed: r.allowed}, t.Name()); err != nil {
				issues = append(issues, Issue{Line: lines[id].Line, ID: id, Msg: err.Error()})
				delete(nodes, r.id) // a wrong-type target is dropped everywhere
			}
		}
	}

	// Walk from the root; cycles and dangling references are cut, required
	// single references that dangle drop their node.
	keep := map[string]bool{}
	var walk func(id string, path map[string]bool) bool
	walk = func(id string, path map[string]bool) bool {
		n, ok := nodes[id]
		if !ok {
			return false
		}
		if path[id] {
			issues = append(issues, Issue{Line: lines[owner[id]].Line, ID: id, Msg: "цикл ссылок"})
			return false
		}
		if keep[id] {
			return true
		}
		path[id] = true
		defer delete(path, id)
		props := n.Props()
		comp := Catalog[n.Name()]
		for _, p := range comp.Params {
			switch p.Type {
			case pRef:
				ref, has := props[p.Name].(string)
				if has && !walk(ref, path) {
					if !p.Optional {
						return false
					}
					delete(props, p.Name)
				}
			case pRefList:
				refs, _ := props[p.Name].([]string)
				kept := make([]string, 0, len(refs))
				for _, r := range refs {
					if walk(r, path) {
						kept = append(kept, r)
					}
				}
				props[p.Name] = kept
			}
		}
		keep[id] = true
		return true
	}
	if !walk(root, map[string]bool{}) {
		return Doc{}, issues, false
	}
	if len(keep) > MaxNodes {
		issues = append(issues, Issue{Line: 1, Msg: fmt.Sprintf("компонентов больше %d", MaxNodes)})
		return Doc{}, issues, false
	}
	doc = Doc{Version: A2UIVersion, Root: root}
	// Source order, inline children right after their statement.
	for _, id := range order {
		for _, n := range lines[id].Nodes {
			if keep[n.ID] {
				doc.Components = append(doc.Components, nodes[n.ID])
			}
		}
	}
	for _, id := range order {
		if !keep[id] {
			if _, valid := nodes[id]; valid {
				issues = append(issues, Issue{Line: lines[id].Line, ID: id, Msg: "компонент не связан с root и не показан"})
			}
		}
	}
	sort.SliceStable(issues, func(i, j int) bool { return issues[i].Line < issues[j].Line })
	return doc, issues, true
}

func hasStmt(stmts []Statement, id string) bool {
	for _, s := range stmts {
		if s.ID == id {
			return true
		}
	}
	return false
}

// Buttons lists every button of the screen with a stable address
// (node id + index) so the server can bind action records to them.
type ButtonRef struct {
	NodeID string
	Index  int
	Label  string
	Action Action
}

// Buttons returns the screen's buttons in order.
func (d Doc) Buttons() []ButtonRef {
	var out []ButtonRef
	for _, n := range d.Components {
		if n.Name() != "Actions" {
			continue
		}
		bs, _ := n.Props()["buttons"].([]map[string]any)
		for i, b := range bs {
			a, _ := b["action"].(Action)
			label, _ := b["label"].(string)
			out = append(out, ButtonRef{NodeID: n.ID, Index: i, Label: label, Action: a})
		}
	}
	return out
}

// BindButton sets the server-side action id and zone on a button.
func (d Doc) BindButton(nodeID string, index int, actionID, zone string) {
	for _, n := range d.Components {
		if n.ID != nodeID {
			continue
		}
		bs, _ := n.Props()["buttons"].([]map[string]any)
		if index < len(bs) {
			bs[index]["action_id"] = actionID
			bs[index]["zone"] = zone
		}
	}
}

// Repairer asks the model to fix a screen: the source and what is wrong
// with it in, corrected source out (§10.2 step 3; separate calls after the
// stream).
type Repairer func(source string, issues []Issue) (string, error)

// MaxRepairs bounds the repair rounds (§10.2: no more than two).
const MaxRepairs = 2

// Block statuses as stored in ui_block.status.
const (
	StatusValid    = "valid"
	StatusRepaired = "repaired"
	StatusPruned   = "pruned" // shown without the parts that stayed invalid
	StatusFailed   = "failed"
)

// Result is a finished screen.
type Result struct {
	Doc     Doc
	Source  string
	Issues  []Issue // what is still wrong (dropped from Doc)
	Status  string
	Repairs int
}

// Finalize compiles, checks and, while issues remain, repairs up to
// MaxRepairs times. A nil repair keeps the first build.
func Finalize(source string, opts Options, repair Repairer) Result {
	doc, issues, ok := Build(source, opts)
	res := Result{Doc: doc, Source: source, Issues: issues}
	for res.Repairs < MaxRepairs && len(res.Issues) > 0 && repair != nil {
		fixed, err := repair(res.Source, res.Issues)
		res.Repairs++
		if err != nil || strings.TrimSpace(fixed) == "" {
			break
		}
		if srcs, _ := Extract(fixed); len(srcs) > 0 {
			fixed = srcs[0]
		}
		d2, i2, ok2 := Build(fixed, opts)
		// A repair is taken only when it is no worse than what we have.
		if ok2 && (!ok || len(i2) <= len(res.Issues)) {
			res.Doc, res.Source, res.Issues, ok = d2, fixed, i2, true
		}
	}
	switch {
	case !ok:
		res.Status = StatusFailed
	case len(res.Issues) > 0:
		res.Status = StatusPruned
	case res.Repairs > 0:
		res.Status = StatusRepaired
	default:
		res.Status = StatusValid
	}
	return res
}
