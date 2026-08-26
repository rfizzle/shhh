package chat

// The approved plan as a live checklist (S-104, DESIGN-TUI.md §13a, §15a).
//
// Plan mode is the one place a step list is authoritative. Everywhere else the
// outline infers a step from the prose that precedes a batch of tool calls
// (S-090); once a plan is approved the steps it declared are the steps, and
// the transcript outline, the inspector rail's PLAN block and /plan all read
// one list.
//
// The join between the declared list and the transcript is made once, when an
// assistant announcement is appended ahead of a batch of calls: the line is
// matched against the steps still unclaimed, and the entry is stamped with the
// number of the step it carries out — or with offPlanStep when it matches
// nothing the plan declared. Stamping at append time is what keeps every later
// reader a pure function of the transcript, which is what lets the render
// cache freeze a block and never look at it again.

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/rfizzle/shhh/internal/plan"
)

// offPlanStep marks an announcement that matched no declared step: work the
// agent went and did that the plan never named. It is drift, and it is shown
// as such rather than renumbered into the plan.
const offPlanStep = -1

// planMatchFloor is how much of a declared step's title an announcement has to
// restate before it counts as carrying that step out. Half is deliberately
// forgiving — a model announcing step 2 rarely repeats its title verbatim —
// and the floor is measured against the *step's* words, not the
// announcement's, so a long sentence that contains the step still matches
// while a short one that shares a single word with it does not.
const planMatchFloor = 0.5

// planRun is an approved plan for as long as it is being carried out. It holds
// no rendering state: the checklist's glyphs and durations are read off the
// transcript every frame, so the rail and the outline cannot disagree about a
// step. What it does hold is the assignment — which announcement carried out
// which step — because that is a decision, made once, in the order the
// transcript was written.
type planRun struct {
	doc plan.Plan
	// start is the transcript index the execution turn began at, so the
	// checklist reads only the entries this plan produced.
	start int
	// claimed maps a step's index to the order it was claimed in: 1 for the
	// first step the agent actually carried out. The order is what says a step
	// ran before an earlier one.
	claimed map[int]int
	claims  int
	// offPlan are the announcements that matched no step, in the order they
	// were made.
	offPlan []string
}

// newPlanRun starts tracking an approved plan. A plan that never adopted the
// step shape has no steps to track and returns nil — the prose the card fell
// back to is not a checklist.
func newPlanRun(doc plan.Plan, start int) *planRun {
	if !doc.Structured() {
		return nil
	}
	return &planRun{doc: doc, start: start, claimed: map[int]int{}}
}

// claim assigns one announcement to the step it carries out and returns that
// step's number, or offPlanStep. A step is claimed once: an announcement that
// best matches a step already carried out is not a second copy of that step,
// it is the agent doing something else, and the plan says so.
func (r *planRun) claim(title string) int {
	best, score := -1, 0.0
	for i, s := range r.doc.Steps {
		if _, taken := r.claimed[i]; taken {
			continue
		}
		if v := planTitleScore(title, s.Title); v > score {
			best, score = i, v
		}
	}
	if best < 0 || score < planMatchFloor {
		r.offPlan = append(r.offPlan, title)
		return offPlanStep
	}
	r.claims++
	r.claimed[best] = r.claims
	return r.doc.Steps[best].Number
}

// complete reports whether every declared step has been claimed — the plan is
// through its list, whatever the outcome of the steps.
func (r *planRun) complete() bool { return len(r.claimed) == len(r.doc.Steps) }

// title is the plan's own title, for the heading /plan prints.
func (r *planRun) title() string {
	if r.doc.Title != "" {
		return "Plan · " + r.doc.Title
	}
	return "Plan"
}

// outOfOrder reports the first pair of steps carried out back to front: the
// later step's number, the earlier one's, and whether there was such a pair.
func (r *planRun) outOfOrder() (ran, before int, ok bool) {
	for i := range r.doc.Steps {
		oi, taken := r.claimed[i]
		if !taken {
			continue
		}
		for j := i + 1; j < len(r.doc.Steps); j++ {
			if oj, taken := r.claimed[j]; taken && oj < oi {
				return r.doc.Steps[j].Number, r.doc.Steps[i].Number, true
			}
		}
	}
	return 0, 0, false
}

// skipped are the steps still unclaimed with a later step already carried out.
// A step nobody has reached yet is queued, not skipped — the difference is
// whether the run has moved past it.
func (r *planRun) skipped() []int {
	last := -1
	for i := range r.doc.Steps {
		if _, taken := r.claimed[i]; taken {
			last = i
		}
	}
	var out []int
	for i := 0; i < last; i++ {
		if _, taken := r.claimed[i]; !taken {
			out = append(out, r.doc.Steps[i].Number)
		}
	}
	return out
}

// drift is everything the run has done that the plan did not say, one clause
// each. An empty result is a run following its plan, which is not news and is
// reported as such by the callers rather than as an absence.
func (r *planRun) drift() []string {
	var out []string
	if n := len(r.offPlan); n > 0 {
		out = append(out, fmt.Sprintf("%s off the plan (%s)", stepsWord(n), quoteFirst(r.offPlan)))
	}
	if ran, before, ok := r.outOfOrder(); ok {
		out = append(out, fmt.Sprintf("step %d ran before step %d", ran, before))
	}
	if s := r.skipped(); len(s) > 0 {
		out = append(out, fmt.Sprintf("%s skipped so far (%s)", stepsWord(len(s)), numberList(s)))
	}
	return out
}

// driftLabel is the rail's one line: the same drift in as few words as 46
// columns allow, with /plan carrying the rest. Empty means no drift.
func (r *planRun) driftLabel() string {
	var out []string
	if n := len(r.offPlan); n > 0 {
		out = append(out, fmt.Sprintf("%d off plan", n))
	}
	if _, _, ok := r.outOfOrder(); ok {
		out = append(out, "out of order")
	}
	if n := len(r.skipped()); n > 0 {
		out = append(out, fmt.Sprintf("%d skipped", n))
	}
	return strings.Join(out, " · ")
}

// planTitleScore is the share of a declared step's significant words that an
// announcement restates. It is word overlap and nothing cleverer: the two
// strings being compared are a step title and a sentence announcing that step,
// so the words they share are the whole of the signal.
func planTitleScore(announced, declared string) float64 {
	want := planWords(declared)
	if len(want) == 0 {
		return 0
	}
	have := map[string]bool{}
	for _, w := range planWords(announced) {
		have[w] = true
	}
	hit := 0
	for _, w := range want {
		if have[w] {
			hit++
		}
	}
	return float64(hit) / float64(len(want))
}

// planWords reduces a title to the words worth comparing: lowercase, letters
// and digits only, each counted once. A word under three characters carries no
// signal at this length.
func planWords(s string) []string {
	var out []string
	seen := map[string]bool{}
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for _, field := range fields {
		if len([]rune(field)) < 3 || planFiller[field] || seen[field] {
			continue
		}
		seen[field] = true
		out = append(out, field)
	}
	return out
}

// planFiller is the padding a model wraps an announcement in — "now let me
// update the loop" against a step titled "Update the loop". Only a word that
// is padding in *both* directions belongs here: a word that could be the
// substance of a step title (add, run, fix, read) never does, because dropping
// it would make two different steps look alike.
var planFiller = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "into": true,
	"from": true, "that": true, "this": true, "then": true, "next": true,
	"now": true, "let": true, "will": true, "step": true, "its": true,
	"our": true, "them": true, "they": true, "than": true, "have": true,
	"has": true, "are": true, "was": true,
}

// stepsWord renders "1 step" / "3 steps".
func stepsWord(n int) string {
	if n == 1 {
		return "1 step"
	}
	return fmt.Sprintf("%d steps", n)
}

// quoteFirst names the first of a list and counts the rest, so a drift note
// stays one line however far the agent wandered.
func quoteFirst(items []string) string {
	if len(items) == 0 {
		return ""
	}
	first := `"` + items[0] + `"`
	if len(items) == 1 {
		return first
	}
	return fmt.Sprintf("%s and %d more", first, len(items)-1)
}

// numberList renders step numbers as "2, 4".
func numberList(ns []int) string {
	parts := make([]string, len(ns))
	for i, n := range ns {
		parts[i] = fmt.Sprint(n)
	}
	return strings.Join(parts, ", ")
}
