package components

// The rail's STEPS block: the session's own working list, as a count and the
// step it is on. It is a file of its own because it is not PLAN and must not
// read as it: PLAN is a checklist somebody approved, drawn step by step with
// a glyph each; STEPS is the agent's own account of where it is, drawn as the
// reading a child's lane gives — `n of m` and the step it is on — and nothing
// in it says a task is finished.

import (
	"fmt"
	"strings"
)

// InspectorSteps is the STEPS block: how far the session is through the list
// it declared itself.
type InspectorSteps struct {
	// Done and Total are the list's count. Total zero is no list, which the
	// host hands over as a nil block rather than as zero of zero.
	Done, Total int
	// Current is the first step not yet marked; empty once every step is.
	Current string
}

// stepsBlock is the STEPS reading. It takes PLAN's place in the rail's order
// because the two are never up together — while an approved plan is being
// executed the plan is the checklist, and the host sends no STEPS — and it
// answers PLAN's question, through what, one step at a time rather than as
// the whole list: the agent's list is its own and can change, so the row is
// where it is now, not a promise of what is left.
// See docs/interface/surfaces.md#the-inspector-rail.
func (r InspectorRail) stepsBlock(width int) (railBlock, bool) {
	s := r.Steps
	if s == nil || s.Total <= 0 {
		return railBlock{}, false
	}
	b := railBlock{heading: railHeading("STEPS", "", sty.Dim, width)}
	parts := []string{sty.Info.Render(fmt.Sprintf("%d of %d", min(max(s.Done, 0), s.Total), s.Total))}
	if s.Current != "" {
		parts = append(parts, sty.Body.Render(s.Current))
	}
	b.add(indentRow(strings.Join(parts, sty.Dim.Render(" · ")), width))
	return b, true
}
