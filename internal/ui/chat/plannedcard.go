package chat

// The approved plan in the transcript
// (docs/interface/surfaces.md#the-progress-checkpoint): one card where the
// plan was approved, drawn once — the plan's receipt as its header, its own
// sentence as the body, a footer row per step — and a flat line under each
// step's card as the run finishes it. The card is read off the run every
// frame the run is live, so its rows tick in place; the lines are stamped on
// the step's title when the run claims it, so a line already drawn says what
// was true when its step was taken.

import (
	"path"

	"github.com/rfizzle/shhh/internal/plan"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// planCard is the approved plan behind an entryPlan card: the run, and what
// the approval card said about putting its files back, which is a claim made
// once, before the first edit — the same claim the approval card made.
type planCard struct {
	run        *planRun
	reversible components.PlanFact
}

// planTick is what a step's flat line says once the step is finished: how
// many steps the plan had, the step's own title as the plan named it, and how
// many of the plan's writes the run had still to reach when it took this one.
type planTick struct {
	of, writesLeft int
	title          string
}

// beginPlanRun starts carrying out an approved plan: the run the outline,
// the rail and /plan read, and the card the transcript draws it as, filed
// before the run's own entries begin. A plan that never adopted the step
// shape has no list to keep and no card to draw.
func (m *Model) beginPlanRun(doc plan.Plan) {
	m.planRun = newPlanRun(doc, len(m.transcript))
	if m.planRun == nil {
		return
	}
	fact, _ := m.planReversibility(doc.WritePaths())
	m.appendEntry(entry{kind: entryPlan, plan: &planCard{run: m.planRun, reversible: fact}})
	m.planRun.start = len(m.transcript)
}

// tickFor is the line a claimed step will say once it is finished.
func (r *planRun) tickFor(number int) planTick {
	t := planTick{of: len(r.doc.Steps)}
	for i, s := range r.doc.Steps {
		if s.Number == number {
			t.title = s.Title
			continue
		}
		if _, taken := r.claimed[i]; !taken && s.Action.Writes() {
			t.writesLeft++
		}
	}
	return t
}

// plannedCardFor is the plan's card: each declared step with its state as
// the checklist reads it — the reading the rail's PLAN block takes — and
// what it said it would write.
func (m Model) plannedCardFor(e entry, sel rowSel) components.PlannedCard {
	run := e.plan.run
	states := m.planChecklistOf(run)
	card := components.PlannedCard{
		Body:           run.doc.Title,
		Reversible:     e.plan.reversible.Text,
		ReversibleTone: e.plan.reversible.Tone,
		Folded:         e.stepFold == foldClosed,
		Selected:       sel != rowUnselected,
	}
	files := map[string]bool{}
	for i, s := range run.doc.Steps {
		for _, p := range s.Paths {
			files[p] = true
		}
		row := components.PlannedStep{Number: s.Number, Title: s.Title,
			Writes: s.Action.Writes(), Delete: s.Action == plan.Delete}
		if i < len(states) {
			row.State = states[i].State
		}
		switch {
		case row.Writes && len(s.Paths) > 0:
			row.File, row.More = path.Base(s.Paths[0]), len(s.Paths)-1
		case !row.Writes:
			row.Does, _ = planStepKind(s)
		}
		card.Steps = append(card.Steps, row)
	}
	card.Files = len(files)
	return card
}

// planTickFor is the flat line under a step of the plan the run has
// finished, or false for a step still going, one the plan never named, or a
// card no plan's step titled.
func (m Model) planTickFor(blk transcriptBlock, es []entry) (components.PlanTick, bool) {
	g := blk.step
	if g == nil || g.titleIdx < 0 || g.titleIdx >= len(es) {
		return components.PlanTick{}, false
	}
	e := es[g.titleIdx]
	if e.planStep <= 0 || e.planTick.of == 0 {
		return components.PlanTick{}, false
	}
	state := m.stepStateFor(blk, es)
	if state != stepDone && state != stepFailed {
		return components.PlanTick{}, false
	}
	return components.PlanTick{Number: e.planStep, Of: e.planTick.of, Title: e.planTick.title,
		WritesLeft: e.planTick.writesLeft, Failed: state == stepFailed}, true
}

// livePlanBlock is the index of the block holding the running plan's card,
// which changes as the run reaches each step without a row landing in it,
// so nothing from there on can be frozen into the render cache.
func (m Model) livePlanBlock(blocks []transcriptBlock) int {
	if m.planRun == nil {
		return len(blocks)
	}
	for i, blk := range blocks {
		for j := blk.start; j < blk.end && j < len(m.transcript); j++ {
			if e := m.transcript[j]; e.kind == entryPlan && e.plan != nil && e.plan.run == m.planRun {
				return i
			}
		}
	}
	return len(blocks)
}
