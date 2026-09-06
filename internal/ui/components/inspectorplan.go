package components

// The rail's PLAN block: an approved plan as a checklist, one row per step.
// It is a file of its own because a step's state, its glyph and the weight
// that glyph is drawn at are one decision and nothing outside the block
// makes it.

import (
	"fmt"

	"charm.land/lipgloss/v2"
)

// PlanStepState is one checklist step's state in the PLAN block. It is the
// same four states the step outline draws, because an approved plan's
// step and the transcript's step are the same step.
type PlanStepState int

const (
	// PlanStepQueued is declared and not started; its duration is —.
	PlanStepQueued PlanStepState = iota
	// PlanStepRunning is the step in flight, the one the reader is looking
	// for, so it is the one the block emphasises.
	PlanStepRunning
	// PlanStepDone finished with nothing to report.
	PlanStepDone
	// PlanStepFailed finished and contained a failure.
	PlanStepFailed
)

// InspectorPlanStep is one step of the approved plan in the PLAN block.
type InspectorPlanStep struct {
	Number int
	Title  string
	State  PlanStepState
	// Elapsed is how long the step took; blank for one that has not finished,
	// so the column carries a number only where there is one.
	Elapsed string
}

// InspectorPlan is the PLAN block: an approved plan as a live checklist
// . An approved plan is not a message that scrolls away — it is the
// answer to "where are we", and the rail is where that answer belongs.
type InspectorPlan struct {
	Steps []InspectorPlanStep
	// Done counts the steps that finished, which is what the heading states.
	// A failed step finished.
	Done int
	// Drift is the one-line note when the run has departed from the plan —
	// work the plan never named, steps taken out of order, steps skipped.
	// Empty while the run is following it, because "no drift" is not news.
	Drift string
	// Hint is the row under the list naming how to read the whole plan.
	Hint string
}

// planBlock is the PLAN checklist. It sits under THIS TURN because it
// is that block's detail: THIS TURN says how far through, PLAN says through
// what. The keys it prints are the host's, like [v] and [u] on CHANGES.
func (r InspectorRail) planBlock(width int) (railBlock, bool) {
	p := r.Plan
	if p == nil || len(p.Steps) == 0 {
		return railBlock{}, false
	}
	b := railBlock{heading: railHeading("PLAN",
		fmt.Sprintf("%d of %d done", p.Done, len(p.Steps)), sty.Dim, width)}
	for _, s := range p.Steps {
		glyph, style := planStepTone(s.State)
		// A step with no duration yet gets no right-hand field at all, so the
		// title has the whole row rather than a column reserved for nothing.
		elapsed := ""
		if s.Elapsed != "" {
			elapsed = sty.Dim.Render(s.Elapsed)
		}
		b.add(railRow(glyph+" "+style.Render(s.Title), elapsed, width, inspectorIndent))
	}
	if p.Drift != "" {
		b.add(railRow(sty.Accent.Render("⚠")+" "+sty.Dim.Render(p.Drift),
			"", width, inspectorIndent))
	}
	if p.Hint != "" {
		b.add(indentRow(sty.Hint.Render(p.Hint), width))
	}
	return b, true
}

// planStepTone is a checklist step's glyph and the weight its title carries.
// The running step is the one being looked for, so it is the bright one; a
// finished step recedes to chrome, having nothing left to ask of anyone.
func planStepTone(s PlanStepState) (string, lipgloss.Style) {
	switch s {
	case PlanStepRunning:
		return sty.SpinText.Render("▸"), brightStyle()
	case PlanStepDone:
		return sty.Add.Render("✓"), sty.Dim
	case PlanStepFailed:
		return sty.Err.Render("✗"), sty.Body
	}
	return sty.Dim.Render("·"), sty.Dim
}
