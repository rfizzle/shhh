package components

// The plan card (S-103, docs/interface/surfaces.md#selectors). Plan approval
// is the cheapest place in the product to disagree with an agent, and it used
// to be the vaguest: a list of option rows under a paragraph the reader
// skimmed. The card states the plan as priced steps — each one naming the
// files it intends to touch — computes the whole radius once on a line of its
// own, and then asks, explaining the consequence of the option under the
// pointer and no other.
//
// Only the focused option explains itself on purpose. Four consequences
// stacked at once is a wall, not a choice, and a card that tall would push
// the plan it is about out of the panel.

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
)

// PlanFact is one clause of the computed summary line: `3 files touched`,
// `no deletes`, `reversible`. The words carry the meaning; the tone only
// makes the clause that matters findable.
type PlanFact struct {
	Text string
	Tone FieldTone
}

// PlanStep is one step of the plan as the card draws it: the numbered title
// with its intent on the right, and — where the model supplied one — the
// paths beneath it.
type PlanStep struct {
	Number int
	Title  string
	// Detail is the paths the step named, joined for display, with any note
	// after them. Empty when the model did not say, which the card renders as
	// a missing row rather than as "unknown".
	Detail string
	// Kind is the right-hand intent label, e.g. "read only" or "✎ 2 files".
	Kind     string
	KindTone FieldTone
}

// PlanCard is the plan-approval surface.
type PlanCard struct {
	// Title is the border title, e.g. "Plan · make the round limit
	// recoverable"; Chip rides the top border with the step count.
	Title string
	Chip  string
	// Steps is the structured body. When it is empty the card falls back to
	// Prose, which is how a plan the model wrote without structure still
	// reaches the reader instead of failing to parse.
	Steps []PlanStep
	Prose []string
	// Summary is the computed radius line and SummaryDetail the clause that
	// qualifies it, dropped first when the terminal cannot carry both.
	Summary       []PlanFact
	SummaryDetail string
	// Options are the decisions, each naming the mode it enters; the focused
	// one shows its Desc beneath it and no other does.
	Options []SelectOption
	Focus   int
	Hint    string
	// MaxLines bounds the card's height, frame included; the step list is
	// what shrinks, and what it drops is counted rather than lost.
	MaxLines int
	// NotYetLive says the card is on screen beside a draft that still holds
	// the keyboard (S-117): its keys render as not-yet-live, and
	// Handover is the one that hands the keyboard over.
	NotYetLive bool
	Handover   string
}

// View renders the card at the given width.
func (c *PlanCard) View(width int) string {
	inner := width - cardFrameWidth
	// FocusDesc is the plan card's rule and this card's alone: elsewhere a
	// description is a property of the option and rides its row, but here it is
	// the consequence of taking the option, and four consequences stacked at
	// once is a wall rather than a choice.
	sel := Select{Options: c.Options, Focus: c.Focus, FocusDesc: true}
	// The plan card's options are its three or four decisions and never
	// scroll; here it is the step list that shrinks, so the options
	// render whole rather than through a window (S-116).
	tail := c.tailRows(width, inner, sel.optionRows(width, true, 0, len(c.Options)))
	rows := c.bodyRows(inner, c.bodyBudget(len(tail)))
	return renderChromeCard(cardChrome{title: c.Title, chips: c.chips()}, append(rows, tail...), width)
}

// chips is the step count on the title rail, which is the one thing about the
// plan that is worth knowing before reading it.
func (c *PlanCard) chips() []string {
	if c.Chip == "" {
		return nil
	}
	return []string{c.Chip}
}

// tailRows is everything below the steps: the summary, the rule, the options
// and the keys. It is fixed height, so it is measured first and the body gets
// what is left.
func (c *PlanCard) tailRows(width, inner int, options []string) []string {
	var rows []string
	if line := c.summaryRow(inner); line != "" {
		rows = append(rows, "", line)
	}
	rows = append(rows, cardRule)
	rows = append(rows, options...)
	switch {
	case c.NotYetLive:
		// A plan that arrived while a sentence was half-typed offers its keys
		// the same way an approval card does: dimmed, said to be
		// waiting, with the one key that hands the keyboard over under them.
		rows = append(rows, notYetLiveRows(c.Hint, c.Handover, width)...)
	case c.Hint != "":
		rows = append(rows, hintRows([]string{c.Hint}, width)...)
	}
	return rows
}

// bodyBudget is how many rows the step list may have once the tail and the
// frame are paid for. A card with no bound gets all of them.
func (c *PlanCard) bodyBudget(tail int) int {
	if c.MaxLines <= 0 {
		return 0
	}
	return max(c.MaxLines-2-tail, 1)
}

// bodyRows is the step list, or the prose fallback when there are no steps.
func (c *PlanCard) bodyRows(inner, budget int) []string {
	if len(c.Steps) == 0 {
		return boundProse(c.Prose, inner, budget)
	}
	groups := make([][]string, len(c.Steps))
	total := 0
	for i, s := range c.Steps {
		groups[i] = s.rows(inner)
		total += len(groups[i])
	}
	var rows []string
	if budget <= 0 || total <= budget {
		for _, g := range groups {
			rows = append(rows, g...)
		}
		return rows
	}
	// Steps are dropped whole — half a step, a title with the files it
	// touches cut off, says less than a counted remainder — and the last row
	// is reserved for that count.
	for i, g := range groups {
		if len(rows)+len(g) > budget-1 {
			return append(rows, sty.Hint.Render(clip(remainder(len(c.Steps)-i, len(rows) > 0), inner)))
		}
		rows = append(rows, g...)
	}
	return rows
}

// remainder counts the steps the card could not fit. A terminal too short for
// even one of them says so rather than counting "more" than nothing.
func remainder(n int, shown bool) string {
	switch {
	case !shown:
		return "… " + strconv.Itoa(n) + " steps, no room to show them"
	case n == 1:
		return "… 1 more step"
	}
	return "… " + strconv.Itoa(n) + " more steps"
}

// boundProse renders the unstructured fallback, counting what it drops for
// the same reason the step list does.
func boundProse(prose []string, inner, budget int) []string {
	truncated := budget > 0 && len(prose) > budget
	if truncated {
		prose = prose[:max(budget-1, 0)]
	}
	var rows []string
	for _, line := range prose {
		rows = append(rows, sty.Body.Render(clip(line, inner)))
	}
	if truncated {
		rows = append(rows, sty.Hint.Render("…"))
	}
	return rows
}

// rows renders one step: the number and title with the intent right-aligned,
// then the paths beneath. The intent is dropped before the title is clipped —
// a title cut in half says less than a missing label does.
func (s PlanStep) rows(inner int) []string {
	head := sty.Dim.Render(padRight(strconv.Itoa(s.Number), 2)) + sty.Body.Render(s.Title)
	if s.Kind != "" {
		kind := s.KindTone.style().Render(s.Kind)
		if gap := inner - lipgloss.Width(head) - lipgloss.Width(kind); gap >= 1 {
			head += strings.Repeat(" ", gap) + kind
		}
	}
	rows := []string{clip(head, inner)}
	if s.Detail != "" {
		rows = append(rows, sty.Dimmer.Render(clip("  "+s.Detail, inner)))
	}
	return rows
}

// summaryRow is the computed radius: the clauses joined, then the detail that
// qualifies the last of them. The detail goes rather than being clipped, so
// what is left is a whole statement.
func (c *PlanCard) summaryRow(inner int) string {
	if len(c.Summary) == 0 {
		return ""
	}
	var parts []string
	for _, f := range c.Summary {
		parts = append(parts, f.Tone.style().Render(f.Text))
	}
	line := strings.Join(parts, sty.Dim.Render(" · "))
	if c.SummaryDetail != "" {
		detail := " — " + c.SummaryDetail
		if lipgloss.Width(line)+lipgloss.Width(detail) <= inner {
			line += sty.Dimmer.Render(detail)
		}
	}
	return clip(line, inner)
}
