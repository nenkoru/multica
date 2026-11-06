package streams

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/multica-ai/multica/server/internal/streams/sdb"
)

// DefaultAskDailyCap is the per-day ceiling of asynchronous questions to
// the owner. The design allows 5 (§2 #5); the owner's mandate is stricter
// and wins: zero by default, at most one a day. Configurable per deployment.
const DefaultAskDailyCap = 1

// MaxAsksPerBatch is how many questions a batch shows (§7.2).
const MaxAsksPerBatch = 5

// AskDraft is a question proposed by a step (from human_action).
type AskDraft struct {
	Kind     string
	Text     string
	Options  []string
	Items    []string
	Default  *int
	Zone     string
	Deadline time.Time
	Value    float64
	Cost     float64
}

// selfDecideTopics are questions the agent must answer itself (§7.3). The
// match is a cheap keyword screen; the manager prompt carries the full rule.
// RE2's \b is ASCII-only, so Cyrillic stems are matched without it.
var selfDecideTopics = []struct {
	re  *regexp.Regexp
	why string
}{
	{regexp.MustCompile(`(?i)(библиотек|архитектур|подход|\blibrary|\barchitecture|\bframework)`), "approach, library or architecture inside the stream's repos"},
	{regexp.MustCompile(`(?i)(приоритет|max_parallel|какого исполнител|какую модель|which executor|which model)`), "priority, parallelism or executor choice"},
	{regexp.MustCompile(`(?i)(доработать или переделать|переделать или|rework or redo)`), "rework vs redo after a failed check"},
	{regexp.MustCompile(`(?i)(\b402\b|квот|quota|завис|упал|crash)`), "what to do after a 402, crash or hang"},
	{regexp.MustCompile(`(?i)(непонятн\S* бриф|что имел\S* в виду|unclear brief)`), "an unclear brief is a manager turn"},
	{regexp.MustCompile(`(?i)(можно ли (смерджить|слить|задеплоить|выкатить)|merge\?|deploy\?)`), "a reversible merge or deploy"},
}

// redTopics are the mandate's categories kept for Ruslan (RUSL-2514): a
// question about them is red, never answered by silence.
var redTopics = []struct {
	re  *regexp.Regexp
	why string
}{
	// No \b here: in RE2 it is ASCII-only and never matches before Cyrillic.
	{regexp.MustCompile(`(?i)(личн\S* доступ|personal access)`), "personal access"},
	{regexp.MustCompile(`(?i)(люди вне круга|мандат:[^.]*люд|от (его|имени руслана|своего) имени|on his behalf)`), "people outside the circle"},
	{regexp.MustCompile(`(?i)((сверх|больше|выше|поднять) (бюджет|лимит|потол)|мандат:[^.]*деньг|over budget)`), "money over budget"},
	{regexp.MustCompile(`(?i)(необратим|безвозвратн|без (бэкапа|копии|резервн)|мандат:[^.]*необрат|irreversible)`), "irreversible without a backup"},
	{regexp.MustCompile(`(?i)(зон\S* наташ|мандат:[^.]*наташ|natasha's zone)`), "Natasha's zone"},
}

// redTopic names the mandate category a question text touches, or "".
func redTopic(text string) string {
	for _, t := range redTopics {
		if t.re.MatchString(text) {
			return t.why
		}
	}
	return ""
}

// askIsRed: a red-zone question, or one whose text touches a mandate
// category even if it was stored as normal (RUSL-2729). Such a question has
// no default and is never answered by silence.
func askIsRed(zone, text string) bool {
	return zone == "red" || redTopic(text) != ""
}

// askZone is the zone a question is shown and pressed with.
func askZone(a sdb.Ask) string {
	if askIsRed(a.Zone, a.Text) {
		return "red"
	}
	return a.Zone
}

// askDefault is the option silence picks, or -1: a red question has none.
func askDefault(a sdb.Ask) int {
	if !a.DefaultOption.Valid || askIsRed(a.Zone, a.Text) {
		return -1
	}
	return int(a.DefaultOption.Int32)
}

// ValidateAsk applies the form check of §7.2 and the self-decide screen of
// §7.3. A rejected question goes back to the step as a contract error.
func ValidateAsk(h HumanAction, now time.Time) (AskDraft, error) {
	var problems []string
	d := AskDraft{Text: strings.TrimSpace(h.Text), Options: h.Options, Items: h.Items, Default: h.Default, Zone: h.Zone, Value: h.Value, Cost: h.Cost}
	if d.Zone == "" {
		d.Zone = "normal"
	}
	if d.Zone != "normal" && d.Zone != "red" {
		problems = append(problems, fmt.Sprintf("zone %q is not normal/red", h.Zone))
	}
	switch {
	case len(h.Items) > 0 && len(h.Options) == 0:
		d.Kind = "batch"
	case len(h.Options) >= 2 && len(h.Options) <= 4:
		d.Kind = "choice"
	default:
		problems = append(problems, "question needs 2–4 options or a list of yes/no items")
	}
	if d.Text == "" {
		problems = append(problems, "question text is empty")
	}
	if utf8.RuneCountInString(d.Text) > 200 {
		problems = append(problems, "question is longer than 200 characters")
	}
	if strings.TrimSpace(h.Deadline) == "" {
		problems = append(problems, "question has no deadline")
	} else if t, err := parseTime(h.Deadline); err != nil {
		problems = append(problems, "deadline: "+err.Error())
	} else if !t.After(now) {
		problems = append(problems, "deadline is in the past")
	} else {
		d.Deadline = t
	}
	if d.Zone == "normal" {
		if why := redTopic(d.Text); why != "" {
			problems = append(problems, "red zone (mandate: "+why+"): set \"zone\": \"red\" and no default")
		}
	}
	if d.Zone == "red" && h.Default != nil {
		problems = append(problems, "red-zone question must not have a default")
	}
	if d.Zone == "normal" && d.Kind == "choice" && h.Default == nil {
		problems = append(problems, "normal-zone question needs a default (silence = default)")
	}
	if h.Default != nil && (*h.Default < 0 || *h.Default >= len(h.Options)) {
		problems = append(problems, "default is out of range")
	}
	// Red-zone questions are by definition Ruslan's; skip the screen there.
	if d.Zone == "normal" {
		for _, t := range selfDecideTopics {
			if t.re.MatchString(d.Text) {
				problems = append(problems, "decide it yourself (§7.3): "+t.why)
				break
			}
		}
	}
	if d.Value <= 0 {
		d.Value = 1
	}
	if d.Cost <= 0 {
		d.Cost = 1
	}
	if len(problems) > 0 {
		return AskDraft{}, errors.New(strings.Join(problems, "; "))
	}
	return d, nil
}

// RankedAsk is the minimum the batcher needs.
type RankedAsk struct {
	ID          string
	Value, Cost float64
	Deadline    time.Time
}

// TopAsks returns at most limit asks by value × cost_of_delay, earliest
// deadline first on ties (§7.2).
func TopAsks(asks []RankedAsk, limit int) []RankedAsk {
	out := append([]RankedAsk(nil), asks...)
	sort.SliceStable(out, func(i, j int) bool {
		si, sj := out[i].Value*out[i].Cost, out[j].Value*out[j].Cost
		if si != sj {
			return si > sj
		}
		return out[i].Deadline.Before(out[j].Deadline)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Reply is one parsed answer from a one-line reply like "1 да, 2 б, 3 покажи".
type Reply struct {
	// Position of the question in the shown batch, 1-based.
	N int
	// Choice is the 0-based option index for choice questions, -1 if none.
	Choice int
	// Yes is set for yes/no answers.
	Yes *bool
	// Show asks to expand the question instead of answering.
	Show bool
}

var replyPart = regexp.MustCompile(`^\s*(\d+)\s*[.):-]?\s*(.+?)\s*$`)

// ParseReply parses a one-line batch answer. Anything it cannot read is
// returned in rest and goes to Мультика as an ordinary message (§7.2).
func ParseReply(line string) (replies []Reply, rest string) {
	parts := strings.FieldsFunc(line, func(r rune) bool { return r == ',' || r == ';' || r == '\n' })
	var unknown []string
	for _, p := range parts {
		m := replyPart.FindStringSubmatch(p)
		if m == nil {
			if strings.TrimSpace(p) != "" {
				unknown = append(unknown, strings.TrimSpace(p))
			}
			continue
		}
		n, _ := strconv.Atoi(m[1])
		r := Reply{N: n, Choice: -1}
		word := strings.ToLower(strings.Trim(m[2], ".!"))
		switch {
		case word == "да" || word == "yes" || word == "+" || word == "ок" || word == "ok":
			t := true
			r.Yes = &t
		case word == "нет" || word == "no" || word == "-":
			f := false
			r.Yes = &f
		case word == "покажи" || word == "show" || word == "?":
			r.Show = true
		case optionLetter(word) >= 0:
			r.Choice = optionLetter(word)
		default:
			if c, err := strconv.Atoi(word); err == nil && c >= 1 && c <= 4 {
				r.Choice = c - 1
			} else {
				unknown = append(unknown, strings.TrimSpace(p))
				continue
			}
		}
		replies = append(replies, r)
	}
	return replies, strings.Join(unknown, ", ")
}

// optionLetter maps а/б/в/г and a/b/c/d to 0..3.
func optionLetter(w string) int {
	for i, set := range [][]string{{"а", "a"}, {"б", "b"}, {"в", "c", "v"}, {"г", "d", "g"}} {
		for _, s := range set {
			if w == s {
				return i
			}
		}
	}
	return -1
}

// OptionLabel renders the i-th option letter for batch text.
func OptionLabel(i int) string {
	return []string{"а", "б", "в", "г"}[i%4]
}
