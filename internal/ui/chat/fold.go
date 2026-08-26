package chat

// Verbosity folding (S-091, DESIGN-TUI.md §13c): inside a step, a run of
// consecutive read-only calls collapses into one counted row — `▸ ⚙ 6 reads ·
// 2 searches` — so the rows that matter are not buried under eight `read`
// lines. The fold obeys invariant 4: the group row always states what it
// swallowed and what that cost, and expanding restores the individual rows in
// place. Mutations, failures, refusals, commands and sub-agent rows are never
// folded into a group, because the whole point of the fold is that it only
// ever hides chrome.

import (
	"fmt"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/ui/components"
)

// minGroupRun is the shortest run worth folding. A fold trades every target
// it swallows for one count, so a pair gives up two paths to save one line —
// a bad bargain. Three is where the fold starts paying for itself.
const minGroupRun = 3

// slot is one rendered position inside a step: a single entry, or — when
// group is set — the run of consecutive read-only calls starting at idx that
// the transcript is currently showing as one group row.
type slot struct {
	idx   int
	span  int
	group bool
}

// groupNouns names a verb in a counted label. A verb with no entry here
// pluralizes by suffix, which is the signal that this table has fallen behind
// the §6c verb table rather than a wrong word in the feed.
var groupNouns = map[string][2]string{
	"read":   {"read", "reads"},
	"search": {"search", "searches"},
	"glob":   {"glob", "globs"},
	"lsp":    {"lookup", "lookups"},
	"web":    {"fetch", "fetches"},
}

func groupNoun(verb string, n int) string {
	forms, ok := groupNouns[verb]
	if !ok {
		forms = [2]string{verb, verb + "s"}
	}
	if n == 1 {
		return forms[0]
	}
	return forms[1]
}

// groupLabel counts what the group swallowed, by verb, in the order the calls
// came: `6 reads · 2 searches`.
func groupLabel(es []entry) string {
	var order []string
	counts := map[string]int{}
	for _, e := range es {
		v := activityVerb(e.toolName)
		if counts[v] == 0 {
			order = append(order, v)
		}
		counts[v]++
	}
	parts := make([]string, 0, len(order))
	for _, v := range order {
		parts = append(parts, fmt.Sprintf("%d %s", counts[v], groupNoun(v, counts[v])))
	}
	return strings.Join(parts, " · ")
}

// foldableRow reports whether an entry may be swallowed by a group row: a
// read-only tool call that ran and came back. Everything that changed the
// machine, broke, was refused, or belongs to a child stays a row of its own.
func (m Model) foldableRow(e entry) bool {
	if e.kind != entryTool {
		return false
	}
	row := m.activityRowFor(e)
	return row.Kind == components.ActivityTool && row.State == components.ActivityDone
}

// foldRun measures the run of consecutive foldable rows starting at i.
func (m Model) foldRun(es []entry, i, end int) int {
	n := 0
	for i+n < end && m.foldableRow(es[i+n]) {
		n++
	}
	return n
}

// groupFolded decides whether a run shows as one group row. Folding is the
// verbosity's call — normal and low fold, high shows every row — and your own
// fold, recorded on the entry the run starts at, overrides it.
func (m Model) groupFolded(e entry) bool {
	switch e.groupFold {
	case foldOpen:
		return false
	case foldClosed:
		return true
	}
	return m.verbosity != verbosityHigh
}

// stepSlots walks a step's members and reports what the transcript renders
// for each: one slot per entry, or one group slot per folded run. Both the
// renderer and focus mode read the step through this, so what is on screen
// and what can be selected can never disagree.
func (m Model) stepSlots(es []entry, start, end int) []slot {
	var slots []slot
	for i := start; i < end; {
		run := m.foldRun(es, i, end)
		if run >= minGroupRun && m.groupFolded(es[i]) {
			slots = append(slots, slot{idx: i, span: run, group: true})
			i += run
			continue
		}
		if run == 0 {
			run = 1
		}
		// The whole run advances together, folded or not: an opened group
		// gives every one of its rows back, rather than peeling off the head
		// and re-folding the tail behind it.
		for j := 0; j < run; j++ {
			slots = append(slots, slot{idx: i + j, span: 1})
		}
		i += run
	}
	return slots
}

// groupRowFor builds the counted row for a folded run: what it swallowed, and
// the summed duration of it.
func (m Model) groupRowFor(es []entry, s slot) components.ActivityGroup {
	members := es[s.idx : s.idx+s.span]
	var d time.Duration
	for _, e := range members {
		d += e.duration
	}
	return components.ActivityGroup{
		Label:    groupLabel(members),
		Duration: activityDuration(d),
	}
}

// groupAnchor reports whether enter on idx toggles a group: it heads a run
// that is folded right now, or one you opened by hand. A run left open by the
// verbosity is not a group you can close row by row — /ui verbosity owns that
// — so its first row keeps the ordinary expand behaviour.
func (m Model) groupAnchor(es []entry, idx int) bool {
	for _, blk := range m.blocksOf(es) {
		if blk.step == nil || blk.step.queued() {
			continue
		}
		for _, s := range m.stepSlots(es, blk.step.start, blk.step.end) {
			if s.idx != idx {
				continue
			}
			return s.group || (es[idx].groupFold == foldOpen && m.foldRun(es, idx, blk.step.end) >= minGroupRun)
		}
	}
	return false
}

// toggleGroupFold flips a group between folded and expanded, recording the
// choice on the entry the run starts at so groups, like steps, hold no layout
// state of their own and re-render from raw entries on resize.
func (m *Model) toggleGroupFold(idx int) {
	es := *m.entries()
	if idx < 0 || idx >= len(es) {
		return
	}
	if m.groupFolded(es[idx]) {
		es[idx].groupFold = foldOpen
	} else {
		es[idx].groupFold = foldClosed
	}
}
