package components

// The rail's SUMMARY block: the reading a cheap model gives a finished turn,
// and the vocabulary its verdict is drawn in. It is a file of its own because
// it is the one block that carries prose rather than fields, so the wrapping
// rules and the words a verdict may say are decided here and nowhere else.

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// InspectorSummary is the SUMMARY block: a cheap model's read of what
// the session is doing and whether it is still doing what was asked. It is
// the one block that is not a count — the numbers under it say how much has
// happened, and this says what.
type InspectorSummary struct {
	// Text is the reading itself, in the model's own words. It is wrapped to
	// the rail rather than clipped, because a sentence cut at 44 columns is a
	// sentence nobody can finish.
	Text string
	// State is the reading's judgement, drawn as its own row.
	State SummaryTone
	// Reason qualifies a state that is not on target, and is empty otherwise
	// — a departure the reader can see is worth a row, and "on target
	// because…" is the model narrating.
	Reason string
	// Round is the tool round the reading was taken at, which the heading
	// states. A summary without it is a claim about now that nobody can
	// check.
	Round int
	// Stale marks a reading the session has outrun — a refresh that failed,
	// or one still in flight past its interval. The heading says so rather
	// than letting an old sentence pass for a current one.
	Stale bool
}

// SummaryTone is the rail's rendering of a summary's state. It mirrors
// agent.SummaryState; the component keeps its own type for the same reason
// PlanStepState is not the transcript's own — the rail draws, it does not
// import the session's vocabulary.
type SummaryTone int

const (
	// SummaryUnclear is the reading that could not tell.
	SummaryUnclear SummaryTone = iota
	// SummaryOnTarget is the run still serving the instruction it started
	// from.
	SummaryOnTarget
	// SummaryOffTarget is the run that has departed from it.
	SummaryOffTarget
	// SummarySufficient is the run still on its instruction that has found
	// what it needs and has not started acting on it.
	SummarySufficient
)

// summaryLines is how many wrapped rows the reading may take. Three is what
// two short sentences need at this width; a model that wrote more has written
// more than the block asked for, and the rest folds.
const summaryLines = 3

// summaryReasonLines is how many rows a departure's reason may take under the
// state row. Two, because a reason that needs three is not a reason, it is a
// second summary.
const summaryReasonLines = 2

// summaryBlock is the rail's one prose block. It sits first because
// it is the answer the rest of the rail is the detail of: SUMMARY says what
// is happening, THIS TURN says how far through, CHANGES says what it cost the
// workspace.
//
// The state row is drawn in every state, including on target. PLAN's drift
// line is not — "no drift is not news" — and the difference is where the two
// come from: PLAN's drift is computed from the plan and the steps taken, so
// its absence is a fact, while this is a model's judgement, and a block that
// went quiet when the judgement was "fine" would be indistinguishable from
// one whose reading failed.
func (r InspectorRail) summaryBlock(width int) (railBlock, bool) {
	s := r.Summary
	if s == nil || strings.TrimSpace(s.Text) == "" {
		return railBlock{}, false
	}
	var fields []string
	if s.Round > 0 {
		fields = append(fields, fmt.Sprintf("as of round %d", s.Round))
	}
	metaStyle := sty.Dim
	if s.Stale {
		// An old reading is still the best reading there is — it is just not
		// a current one, and the heading is where that is said.
		fields, metaStyle = append(fields, "stale"), sty.Accent
	}
	meta := strings.Join(fields, " · ")
	b := railBlock{heading: railHeading("SUMMARY", meta, metaStyle, width)}

	lines := wrapPlain(s.Text, width-inspectorIndent)
	if len(lines) > summaryLines {
		lines = lines[:summaryLines]
		lines[summaryLines-1] = Clip(lines[summaryLines-1], width-inspectorIndent-1) + "…"
	}
	for i, line := range lines {
		// The first line is pinned: a block truncated to its heading would
		// leave the rail with a word and no sentence, and the sentence is the
		// whole block. The rest fold from the bottom like any other rows.
		row := indentRow(sty.Body.Render(line), width)
		if i == 0 {
			b.pin(row)
			continue
		}
		b.add(row)
	}

	b.pin(indentRow(SummaryLabel(s.State), width))
	// The reason gets its own rows rather than a suffix on the state row: it
	// is the whole content of a departure, and at 46 columns a suffix is a
	// reason clipped mid-word. It follows its state the way an alert's note
	// follows its alert in CHANGES — one indent further in, dim, bounded.
	for i, line := range wrapPlain(s.Reason, width-inspectorIndent-2) {
		if s.Reason == "" || i >= summaryReasonLines {
			break
		}
		b.pin(railRow(sty.Dim.Render(line), "", width, inspectorIndent+2))
	}
	return b, true
}

// SummaryLabel is a reading's judgement as one clause: the glyph, then the
// words. The rail draws it as a row of its own and the input frame's status
// row leads a line with it, so a terminal too narrow for the rail reads the
// same verdict in the same marks
// (docs/interface/surfaces.md#the-inspector-rail).
func SummaryLabel(s SummaryTone) string {
	glyph, label, style := summaryTone(s)
	return glyph + " " + style.Render(label)
}

// summaryTone is the state row's glyph, its words and its weight. The glyph
// carries the distinction so a monochrome terminal reads the same as a colour
// one: ▸ for a run still on its instruction, ◆ for one that has what it needs
// and is still looking, ⚠ for one that has left the instruction, · for a
// reading that could not tell.
//
// Only the departure is drawn in the accent: a run that has found what it
// needs is not a warning, it is news, so it takes the reading weight and the
// healthy glyph colour.
func summaryTone(s SummaryTone) (string, string, lipgloss.Style) {
	glyph, word := SummaryGlyph(s), SummaryWord(s)
	switch s {
	case SummaryOnTarget:
		return sty.SpinText.Render(glyph), word, sty.Dim
	case SummarySufficient:
		return sty.SpinText.Render(glyph), word, sty.Body
	case SummaryOffTarget:
		return sty.Accent.Render(glyph), word, sty.Body
	}
	return sty.Dim.Render(glyph), word, sty.Dim
}

// SummaryGlyph and SummaryWord are the same verdict unpainted, for the
// callers that render it into a field of their own — the transcript's summary
// row states its verdict in the outcome column, and a colour set there would
// be a second wrapper around a string the row is about to paint itself
// (activityrow.go). The glyph goes first wherever both are used: it is what
// carries the distinction on a terminal with no colour at all.
func SummaryGlyph(s SummaryTone) string {
	switch s {
	case SummaryOnTarget:
		return "▸"
	case SummarySufficient:
		return "◆"
	case SummaryOffTarget:
		return "⚠"
	}
	return "·"
}

func SummaryWord(s SummaryTone) string {
	switch s {
	case SummaryOnTarget:
		return "on target"
	case SummarySufficient:
		return "has enough"
	case SummaryOffTarget:
		return "off target"
	}
	return "target unclear"
}
