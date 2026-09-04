package todo

import (
	"fmt"
	"strings"
)

// Planning a set: which of the ready items should go next, and why those.
//
// The ready list in backlog order under a budget is a filter — it answers
// "which items may I start" and nothing about whether they belong together.
// A set is right when what ships together reads as one change, and that is a
// judgement about a dependency chain, a shared package, a theme, a bug and
// the story that closes its cause. So the proposal is a reading, answered in
// a fixed shape: the set in order with a line per item, a goal for the set,
// and every candidate it left out with a word for why.
// See docs/capabilities/todo.md#a-sprint-is-what-ships-together.

// Omission is why a ready candidate was left out of the proposed set. The
// set is closed for the reason the grooming verdicts are: a reason free to
// be a sentence is a reason that will be one, and a left-out list of
// paragraphs is one nobody reads.
type Omission string

const (
	// OmitWaits depends on work that is not in the set.
	OmitWaits Omission = "waits"
	// OmitTooBig does not fit the budget the plan was asked for.
	OmitTooBig Omission = "too big"
	// OmitUnrelated belongs to different work: shipping it here would make
	// the set read as two changes.
	OmitUnrelated Omission = "unrelated"
	// OmitStale states things about the code that are no longer true, so it
	// has to be read again before it is worked.
	OmitStale Omission = "stale"
)

// Omissions is the closed set in the order the prompt offers it and a card
// lists it. Like the verdicts, the words are written once so the prompt that
// asks for them and the parser that reads them cannot come to disagree.
func Omissions() []Omission {
	return []Omission{OmitWaits, OmitTooBig, OmitUnrelated, OmitStale}
}

// omissionOf reads one of the words, and reports whether it is one. The
// punctuation a line puts between the slug and the word comes off first —
// a dash, a colon, a full stop — because the word is what the reader sees
// on the row and the separator is the answer's own typography.
func omissionOf(s string) (Omission, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimSpace(strings.Trim(s, "-—:·.,"))
	for _, o := range Omissions() {
		if string(o) == s {
			return o, true
		}
	}
	return "", false
}

// Release is what kind of release the set reads as. It is two words because
// it answers one question — does this set add behaviour or only fix it —
// and that is the question the person answers when they tag. shhh names no
// version and makes no tag: this is a line of prose in the goal, never a
// field, because a tool that wrote a version into a file is a tool that
// will one day write the wrong one.
type Release string

const (
	// ReleasePatch is a set of bug fixes.
	ReleasePatch Release = "patch"
	// ReleaseMinor is a set with a story in it.
	ReleaseMinor Release = "minor"
)

// Releases is the closed set, in the order the prompt offers it.
func Releases() []Release { return []Release{ReleasePatch, ReleaseMinor} }

// releaseOf reads one of the words, and reports whether it is one.
func releaseOf(s string) (Release, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, r := range Releases() {
		if string(r) == s {
			return r, true
		}
	}
	return "", false
}

// PlanItem is one item of the proposed set: the slug and the one line
// saying what puts it in this set.
type PlanItem struct {
	Slug string `json:"slug"`
	// Why is the reasoning line. It is the sprint view's note and never
	// the file's prose: the file is the person's, and a sentence a model
	// wrote about an item is shhh's reading of it.
	Why string `json:"why"`
}

// PlanOmission is one candidate the reading left out, with the word.
type PlanOmission struct {
	Slug string   `json:"slug"`
	Why  Omission `json:"why"`
}

// Plan is one reading of the ready list: the set in the order it should be
// worked, the goal it is for, what kind of release it reads as, and every
// candidate that was left out.
type Plan struct {
	// Goal is the sentence the set is for, as the reading wrote it.
	Goal string `json:"goal"`
	// Release is what kind of release the set reads as, empty where the
	// reading named none.
	Release Release `json:"release,omitempty"`
	// Items are the set, in the order they should be worked.
	Items []PlanItem `json:"items"`
	// Left are the candidates that were not taken, in the order the reading
	// gave them.
	Left []PlanOmission `json:"left,omitempty"`
}

// Slugs are the proposed set's slugs, in order.
func (p Plan) Slugs() []string {
	out := make([]string, 0, len(p.Items))
	for _, it := range p.Items {
		out = append(out, it.Slug)
	}
	return out
}

// ReleaseLine is the one line saying what kind of release the set reads as,
// and "" where the reading named none. It is a line of prose rather than a
// field because it is a judgement about a set, and the sprint file's header
// states facts.
func (p Plan) ReleaseLine() string {
	if p.Release == "" {
		return ""
	}
	return fmt.Sprintf("Reads as a %s release.", p.Release)
}

// GoalText is the goal as the sprint file carries it: the sentence, then
// the release line under it.
func (p Plan) GoalText() string {
	goal, line := strings.TrimSpace(p.Goal), p.ReleaseLine()
	switch {
	case line == "":
		return goal
	case goal == "":
		return line
	}
	return goal + "\n\n" + line
}

// PlanPrompt is the planning turn's instruction: the candidates with what
// the backlog already knows about each, and the shape the answer has to
// take. It lives beside the reading it is parsed by rather than with the
// runner's stage prompts, because planning a set is not a stage of working
// one — nothing here is gated, continued or committed.
//
// The candidates carry their accepted readings where they have one. A
// planner that read every item against the tree itself would take the
// reading the person already agreed to and pay for it again, and would
// answer with a different one.
// See docs/capabilities/todo.md#a-sprint-is-what-ships-together.
func (s *Store) PlanPrompt(candidates []Item, budget string) string {
	var b strings.Builder
	b.WriteString("This is SPRINT PLANNING over a project backlog: read the candidates below, change nothing, and recommend the set of items that should go next.\n\n")
	b.WriteString("A set is right when what ships together reads as one change. What makes one: a dependency chain that lands together, items that touch the same packages, a theme the titles share, a bug and the story that closes its cause.\n\n")
	b.WriteString("CANDIDATES — every item that is ready, in backlog order:\n")
	for _, it := range candidates {
		b.WriteString(s.planCandidate(it))
	}
	if budget != "" {
		fmt.Fprintf(&b, "\nBUDGET: %s. The set you recommend has to fit inside it.\n", budget)
	}
	b.WriteString("\nRead the item files named above and the parts of the tree they touch. Where an item carries a reading, that reading is beside it and was accepted by the person: take the item as it stands rather than reading it against the tree again.\n\n")
	b.WriteString(`Answer in exactly this shape and nothing else:

goal: <one sentence saying what this set is for>
release: <exactly one of: ` + releaseWords() + `>
item: <slug>
why: <one line: what puts this item in this set>

— one ` + "`item:`/`why:`" + ` pair per item you recommend, in the order they should be worked — then one line per candidate you left out:

out: <slug> <exactly one of: ` + omissionWords() + `>

`)
	b.WriteString(planKey())
	b.WriteString("\n\nRecommend nothing you cannot write a line about: a set is a reading somebody has to be able to argue with, and an item with no reason beside it is one they cannot.")
	return b.String()
}

// planCandidate is one candidate as the prompt states it: what the header
// says, what the backlog computed about its place, where the file is, and
// the reading of it that stands.
func (s *Store) planCandidate(it Item) string {
	var b strings.Builder
	fmt.Fprintf(&b, "- %s — %s\n", it.Slug, it.Title)
	facts := itemFacts(it)
	if n := s.Unblocks(it.Slug); n > 0 {
		facts = append(facts, fmt.Sprintf("unblocks %d", n))
	}
	if len(it.DependsOn) > 0 {
		facts = append(facts, "depends on "+strings.Join(it.DependsOn, ", "))
	}
	fmt.Fprintf(&b, "  %s\n", strings.Join(facts, " · "))
	fmt.Fprintf(&b, "  file: %s\n", it.Path)
	if r, ok := LoadReading(s.Root, it.Slug); ok {
		fmt.Fprintf(&b, "  read %s · %s\n", r.Stamp(), readingCounts(r))
	} else {
		b.WriteString("  not read against the tree\n")
	}
	return b.String()
}

// readingCounts is an accepted reading in one line: how many claims took
// each verdict. It is counts rather than the claims themselves because the
// planner is choosing between items and a reading quoted in full for every
// candidate would bury the list it is annotating.
func readingCounts(r Reading) string {
	var parts []string
	for _, v := range Verdicts() {
		if n := r.Count(v); n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, v))
		}
	}
	if len(parts) == 0 {
		return "nothing read"
	}
	return strings.Join(parts, " · ")
}

// itemFacts is the header as the planner reads it: every field the profile
// declares, in its order. The grade carries its field's name because a bare
// word off a scale is not a fact, and an ungraded item says so rather than
// showing a dash — the budget spends by grade, and an item with none cannot
// be spent, which is what the planner has to be able to see.
func itemFacts(it Item) []string {
	facts := make([]string, 0, len(it.Profile.Fields))
	for _, f := range it.Profile.Fields {
		switch f.Name {
		case keyPriority:
			facts = append(facts, "priority "+string(it.Priority))
		case it.Profile.Grade:
			if grade := it.Fields[f.Name]; grade != "" {
				facts = append(facts, f.Name+" "+grade)
			} else {
				facts = append(facts, "ungraded")
			}
		default:
			facts = append(facts, orDash(it.Fields[f.Name]))
		}
	}
	return facts
}

// orDash is the word for a field the item left empty.
func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// releaseWords and omissionWords put each closed set on one line for the
// prompt, built from the set rather than written out so a word added to the
// vocabulary is one the prompt offers without anybody remembering to.
func releaseWords() string {
	words := make([]string, 0, len(Releases()))
	for _, r := range Releases() {
		words = append(words, string(r))
	}
	return strings.Join(words, ", ")
}

func omissionWords() string {
	words := make([]string, 0, len(Omissions()))
	for _, o := range Omissions() {
		words = append(words, string(o))
	}
	return strings.Join(words, ", ")
}

// planKey is what each word means. The wording is here rather than beside
// the constants because it is an instruction to a model, and the constants
// are a vocabulary the code shares with a card and a file.
func planKey() string {
	return `What the words mean:
- patch: every item in the set is a bug fix. minor: a story is in it.
- waits: it depends on work that is not in this set.
- too big: the budget has no room for it.
- unrelated: it belongs to different work, and shipping it here would make the set read as two changes.
- stale: what it says about the code is no longer true, so it has to be read again before it is worked.`
}

// The answer's markers, read by prefix the way a grooming answer's are.
const (
	goalMarker    = "goal:"
	releaseMarker = "release:"
	itemMarker    = "item:"
	whyMarker     = "why:"
	outMarker     = "out:"
)

// ParsePlan reads a planning answer against the candidates it was asked
// about, and against the budget it was asked for.
//
// A slug the candidate list does not hold is dropped rather than proposed:
// the set is written to a file and worked, and an item invented by a
// misread line would be a sprint naming something that is not ready — or
// not there at all. The same rule takes an item the budget has no room for
// out of the set and into the left-out list as `too big`, because a budget
// the answer may overrun is not a budget.
func ParsePlan(answer string, candidates []Item, budget SprintBudget) Plan {
	grade := map[string]string{}
	for _, it := range candidates {
		grade[it.Slug] = it.Grade()
	}
	var p Plan
	seen := map[string]bool{}
	var cur *PlanItem
	flush := func() {
		if cur != nil && !seen[cur.Slug] {
			seen[cur.Slug] = true
			p.Items = append(p.Items, *cur)
		}
		cur = nil
	}
	for _, raw := range strings.Split(answer, "\n") {
		line := strings.TrimSpace(raw)
		line = strings.TrimPrefix(line, "- ")
		line = strings.TrimPrefix(line, "* ")
		switch {
		case hasMarker(line, goalMarker):
			flush()
			p.Goal = value(line, goalMarker)
		case hasMarker(line, releaseMarker):
			flush()
			if r, ok := releaseOf(value(line, releaseMarker)); ok {
				p.Release = r
			}
		case hasMarker(line, itemMarker):
			flush()
			if slug, ok := candidateSlug(grade, value(line, itemMarker)); ok {
				cur = &PlanItem{Slug: slug}
			}
		case hasMarker(line, whyMarker):
			if cur != nil {
				cur.Why = value(line, whyMarker)
			}
		case hasMarker(line, outMarker):
			flush()
			if slug, why, ok := parseOmission(value(line, outMarker), grade); ok && !seen[slug] {
				seen[slug] = true
				p.Left = append(p.Left, PlanOmission{Slug: slug, Why: why})
			}
		}
	}
	flush()
	return p.underBudget(budget, grade)
}

// known reports the slug is one of the candidates, including an ungraded
// one whose grade is the empty string.
func known(grade map[string]string, slug string) bool {
	_, ok := grade[slug]
	return ok
}

// candidateSlug is the candidate a marker line names, and whether it names
// one at all. The whole value is tried before its first word because a line
// asked for as `item: <slug>` comes back as `item: cache-ttl — Give the
// cache a lifetime` often enough to matter: an item dropped for the title
// beside it is one the reading meant to propose and the person never sees.
func candidateSlug(grade map[string]string, text string) (string, bool) {
	whole := unfence(strings.TrimSpace(text))
	if known(grade, whole) {
		return whole, true
	}
	if first, _, ok := strings.Cut(whole, " "); ok {
		if first = unfence(first); known(grade, first) {
			return first, true
		}
	}
	return "", false
}

// parseOmission reads `<slug> <word>`, with whatever punctuation the answer
// put between them. A line naming no candidate or no word from the set is
// dropped: "left out for a reason nobody wrote down" is the row this list
// exists to make impossible.
func parseOmission(line string, grade map[string]string) (string, Omission, bool) {
	slug, ok := candidateSlug(grade, line)
	if !ok {
		return "", "", false
	}
	rest := strings.TrimPrefix(strings.TrimSpace(line), slug)
	why, ok := omissionOf(rest)
	return slug, why, ok
}

// underBudget moves the items the budget has no room for into the left-out
// list, keeping the order the reading gave. An ungraded item has no grade
// to spend, so a stated budget leaves it out — the same rule the filter
// this replaced applied, and for the same reason: a budget counts grades,
// and an item with none cannot be counted against one.
func (p Plan) underBudget(budget SprintBudget, grade map[string]string) Plan {
	if len(budget) == 0 {
		return p
	}
	left := budget.clone()
	kept := make([]PlanItem, 0, len(p.Items))
	for _, it := range p.Items {
		if !left.spend(grade[it.Slug]) {
			p.Left = append(p.Left, PlanOmission{Slug: it.Slug, Why: OmitTooBig})
			continue
		}
		kept = append(kept, it)
	}
	p.Items = kept
	return p
}
