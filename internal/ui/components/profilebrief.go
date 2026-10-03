package components

// The profile drafter's first widget: the brief and the drafter's questions,
// each a question over a one-row text field, the brief with its starting
// points under the field. The wizard draws it and hands it the keys while
// the step is the brief or a question.

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// profileBrief is the brief and the questions. It owns what is being asked
// and the exchange so far; the text field is the wizard's, lent to it for
// the brief and the questions. See
// docs/architecture.md#the-profile-drafters-widgets.
type profileBrief struct {
	// ask is the question this step puts, in the drafter's words or the
	// surface's own.
	ask string
	// lead introduces the starting points and starts are the starting
	// points; both are empty once the flow is past the brief.
	lead   string
	starts []string
	// fieldLabel names the text field: what is wanted in it.
	fieldLabel string
	// placeholder is what the empty field says.
	placeholder string

	// asked is the exchange so far, and at/of number the question being
	// asked now. A flow whose length is not stated is one nobody can decide
	// to finish.
	asked  []profileQA
	at, of int

	// focus is -1 while the text field has it, and the starting point's
	// index otherwise.
	focus int
	field *textarea.Model
}

// open puts the widget on the brief. It drops the exchange with it: coming
// back to the brief is reconsidering the thing the questions were asked
// about, so answers to them are not still true.
func (p *profileBrief) open(ask, lead string, starts []string) {
	p.ask, p.lead, p.starts = ask, lead, starts
	p.asked, p.at, p.of = nil, 0, 0
	p.fieldLabel, p.placeholder = "in your own words", "what it is for"
	p.resetField()
}

// question puts one of the drafter's questions up, numbered.
func (p *profileBrief) question(question string, at, of int) {
	p.ask, p.lead, p.starts = question, "", nil
	p.at, p.of = at, of
	p.fieldLabel, p.placeholder = "your answer", "enter alone says you have no preference"
	p.resetField()
}

// answered records an exchange, and forget drops the last one.
func (p *profileBrief) answered(question, answer string) {
	p.asked = append(p.asked, profileQA{question: question, answer: answer})
}

func (p *profileBrief) forget() {
	if len(p.asked) > 0 {
		p.asked = p.asked[:len(p.asked)-1]
	}
}

// profileQA is one question the drafter asked and the answer it got, kept on
// screen above the question being asked now. A flow that forgot what it had
// already been told would be asking the person to hold it in their head.
type profileQA struct {
	question string
	answer   string
}

// resetField empties the field and puts the cursor back in it.
func (p *profileBrief) resetField() {
	p.field.Reset()
	p.field.Placeholder = p.placeholder
	p.focus = -1
	p.field.Focus()
}

// updateBrief answers the first step: the field has the keyboard, the
// starting points are under it, and ↑↓ is what moves between them. The
// arrows and not j/k, because everything else on this step is text.
func (p *profileBrief) updateBrief(msg tea.KeyPressMsg) (bool, profileResult) {
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Profile.Back):
		return true, profileResult{Action: ProfileBack}
	case keys.Is(pressed, keys.Profile.Take):
		// A brief is the one answer the flow cannot supply for itself, so
		// enter with nothing to take does nothing rather than starting a
		// drafting from an empty sentence.
		if text := p.taken(); text != "" {
			return true, profileResult{Action: ProfileTake, Text: text}
		}
		return false, profileResult{}
	case keys.Is(pressed, keys.Profile.Move):
		// The pointer runs from the field, at -1, through the starts, so what
		// moves is a step and not a list: the row above the first start is a
		// place the pointer lands and not an item it steps over.
		p.moveFocus(keys.Step(pressed, keys.Profile.Move))
		return false, profileResult{}
	}
	if p.focus < 0 {
		*p.field, _ = p.field.Update(msg)
	}
	return false, profileResult{}
}

// updateQuestion answers one of the drafter's questions. There is nothing to
// pick here, so every key that is not the answer or the way back is text.
func (p *profileBrief) updateQuestion(msg tea.KeyPressMsg) (bool, profileResult) {
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Profile.Back):
		return true, profileResult{Action: ProfileBack}
	case keys.Is(pressed, keys.Profile.Take):
		// An empty answer is an answer: someone who has no preference about
		// which languages a reviewer covers should not be held at the
		// question until they invent one.
		return true, profileResult{Action: ProfileTake, Text: strings.TrimSpace(p.field.Value())}
	}
	*p.field, _ = p.field.Update(msg)
	return false, profileResult{}
}

// taken is what enter takes on the brief step: what has been typed, or the
// starting point the pointer is on.
func (p *profileBrief) taken() string {
	if p.focus >= 0 && p.focus < len(p.starts) {
		return p.starts[p.focus]
	}
	return strings.TrimSpace(p.field.Value())
}

// moveFocus walks the pointer between the field and the starting points. The
// field is above the list rather than a row in it, so leaving the top of the
// list is how you get back to typing.
func (p *profileBrief) moveFocus(delta int) {
	next := min(max(p.focus+delta, -1), len(p.starts)-1)
	if len(p.starts) == 0 {
		next = -1
	}
	p.focus = next
	if p.focus < 0 {
		p.field.Focus()
	} else {
		p.field.Blur()
	}
}

// briefRows is the first step: the question, the field, the starting points,
// and what the session already has.
func (p *profileBrief) briefRows(width int) []string {
	rows := p.askRows(width)
	rows = append(rows, p.fieldRows(width)...)
	if len(p.starts) > 0 {
		rows = append(rows, "")
		if p.lead != "" {
			rows = append(rows, Clip(indent(sty.dim.Render(p.lead)), width))
		}
		rows = append(rows, p.startRows(width)...)
	}
	return append(rows, "")
}

// briefHint is the first step's key row. It leads with what enter takes,
// because which of the two the pointer is on is the one thing about this step
// that is not obvious from looking at it.
func (p *profileBrief) briefHint() []KeyOffer {
	take := keyOfferAs(keys.Profile.Take, "draft from what you typed")
	if p.focus >= 0 {
		take = keyOfferAs(keys.Profile.Take, "draft from this one")
	}
	segments := []KeyOffer{take}
	if len(p.starts) > 0 {
		segments = append(segments, keyOfferAs(keys.Profile.Move, "the field or a starting point"))
	}
	return append(segments, keyOfferAs(keys.Profile.Back, "nothing is drafted"))
}

// questionRows is one question with the answers already given above it. The
// answered run is dim and the question is not: what is being asked now is
// what the eye should land on.
func (p *profileBrief) questionRows(width int) []string {
	var rows []string
	for _, qa := range p.asked {
		answer := qa.answer
		if answer == "" {
			answer = "no preference"
		}
		rows = append(rows,
			Clip(indent(sty.dim.Render("✓ "+qa.question)), width),
			Clip(indent(sty.dimmer.Render("  "+answer)), width))
	}
	if len(rows) > 0 {
		rows = append(rows, "")
	}
	if p.of > 0 {
		rows = append(rows, Clip(indent(sty.dim.Render(fmt.Sprintf("question %d of %d", p.at, p.of))), width))
	}
	rows = append(rows, p.askRows(width)...)
	rows = append(rows, p.fieldRows(width)...)
	return append(rows, "")
}

// backWords is what esc does from where the flow is standing. It says which
// of the two it is, because "back" on the first question and "back" on the
// third are a cancelled drafting and a corrected answer.
func (p *profileBrief) backWords() string {
	if len(p.asked) == 0 {
		return "nothing is drafted"
	}
	return "back to the last answer"
}

// askRows is the step's question, wrapped by the caller and drawn as the one
// bright thing on the step.
func (p *profileBrief) askRows(width int) []string {
	if p.ask == "" {
		return nil
	}
	var rows []string
	for _, line := range wrapBlock(p.ask, width-profileIndent) {
		rows = append(rows, Clip(indent(sty.body.Render(line)), width))
	}
	return append(rows, "")
}

// fieldRows is the text field: the label that names what is wanted, and the
// field under it. It is the note field's shape rather than a box of its own,
// because a second kind of text input would be a second thing to learn.
func (p *profileBrief) fieldRows(width int) []string {
	inner := max(width-profileIndent-2, 8)
	p.field.SetWidth(inner)
	StyleTextArea(p.field)
	view := p.field.View()
	if p.focus >= 0 {
		// Unfocused, the field echoes as plain text: a blurred textarea
		// still draws cursor artifacts, and the pointer is in the list.
		text := strings.TrimSpace(p.field.Value())
		if text == "" {
			text = "(nothing typed)"
		}
		view = sty.dimmer.Render(Clip(text, inner))
	}
	rows := []string{Clip(indent(sty.dim.Render("┄ "+p.fieldLabel)), width)}
	for _, line := range strings.Split(view, "\n") {
		rows = append(rows, Clip(indent("  "+line), width))
	}
	return rows
}

// startRows is the starting points, drawn as the start screen draws its
// offers: a pointer outside the highlight, and the focused row lit whole.
func (p *profileBrief) startRows(width int) []string {
	rows := make([]string, 0, len(p.starts))
	for i, start := range p.starts {
		if i == p.focus {
			rows = append(rows, LitOption(start, width))
			continue
		}
		rows = append(rows, Clip(PointerColumn()+sty.status.Render(start), width))
	}
	return rows
}
