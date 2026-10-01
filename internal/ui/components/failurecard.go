package components

// The failure card (docs/interface/surfaces.md#the-recovery-row). A provider
// failure is drawn the way a step is: a card on the band whose header is the
// model and what the turn had done when it broke, whose body is what the
// failure means for the reader, and whose footer is the provider's own words
// and the ways out. An error is a card like any other, so a reader scanning a
// turn reads it in the grammar the steps around it use.
//
// It is drawn from the recovery row's facts — the state, the model, the
// class, the provider's words and the offers — so the one-shot, which prints
// the row, and the session, which draws the card, cannot disagree about what
// a failure offers.

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// failureKeysShareWidth is the narrowest pane the card's ways out share a
// line with the provider's words at. Below it they take a line of their own,
// right-aligned under the words, because the words are the provider's and
// cutting them to make room for the keys would cut the one thing that
// explains an unclassified failure.
const failureKeysShareWidth = 120

// FailureCard is one provider failure as a card.
type FailureCard struct {
	// Row is the failure's facts: its state, the model, the class it was
	// read as, how long the turn had run, the provider's words and the
	// offers in the state the keyboard puts them in.
	Row RecoveryRow
	// After is what the turn had done before it broke, in the receipt's
	// words; empty where it had done nothing.
	After string
	// Sentence is the body: what the failure means for the reader, in the
	// session's words.
	Sentence string
	// Selected puts the reading cursor on the header and lights it.
	Selected bool
}

// View draws the card at the pane's width. The class stands where a step's
// outcome does, and a stall or a stop keeps its own mark rather than the
// break's, because which of the three it is decides what to do next.
// See docs/interface/departures.md#a-failure-cards-header-carries-the-class.
func (c FailureCard) View(width int) string {
	r := c.Row
	card := StepCard{
		Verb:     r.Verb,
		Subject:  r.Subject,
		Outcome:  r.Qualifier,
		Duration: r.Duration,
		Body:     c.Sentence,
		Selected: c.Selected,
	}
	if c.After != "" {
		card.Rollup = "after " + c.After
	}
	switch r.State {
	case RecoveryStalled:
		card.Mark, card.OutcomeAccent = sty.Accent.Render("⚠"), true
	case RecoveryStopped:
		card.State, card.OutcomeState = ActivityDenied, ActivityDenied
	default:
		card.State, card.OutcomeState = ActivityFailed, ActivityFailed
	}
	lines := card.top(width)
	lines = append(lines, c.footer(width)...)
	lines = append(lines, cardPad(width))
	return strings.Join(lines, "\n")
}

// footer is the provider's words, one line each, cut to the pane, and the
// ways out right-aligned: on the last line of words where the pane is wide
// enough for both, and on lines of their own under them where it is not,
// wrapped between two offers rather than inside one.
func (c FailureCard) footer(width int) []string {
	r := c.Row
	inner := max(width-CardBodyIndent-cardMargin, 1)
	detail := r.Detail
	if r.MaxDetail > 0 && len(detail) > r.MaxDetail {
		detail = detail[:r.MaxDetail]
	}
	words := make([]string, 0, len(detail))
	for _, d := range detail {
		words = append(words, sty.Dimmer.Render(Clip(d, inner)))
	}
	keys := c.keyRows(inner)
	var lines []string
	if n := len(words); n > 0 && len(keys) == 1 && width >= failureKeysShareWidth &&
		lipgloss.Width(words[n-1])+1+lipgloss.Width(keys[0]) <= inner {
		words[n-1] = rightOf(words[n-1], keys[0], inner)
		keys = nil
	}
	for _, w := range words {
		lines = append(lines, onBand(strings.Repeat(" ", CardBodyIndent)+w, width))
	}
	for _, k := range keys {
		lines = append(lines, onBand(strings.Repeat(" ", CardBodyIndent)+rightOf("", k, inner), width))
	}
	return lines
}

// rightOf sets right at the right edge of a run inner columns wide, after
// left.
func rightOf(left, right string, inner int) string {
	return left + strings.Repeat(" ", max(inner-lipgloss.Width(left)-lipgloss.Width(right), 0)) + right
}

// keyRows are the offers in the state the keyboard puts them in, packed
// into as few rows as the room allows with ` · ` between two offers. Where
// the card does not hold the keyboard the offers are grey and the key that
// hands it over is one more offer at the end of the run, live
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
func (c FailureCard) keyRows(room int) []string {
	r := c.Row
	if len(r.Keys) == 0 {
		return nil
	}
	var pieces []string
	switch {
	case !r.KeysWaiting:
		for _, k := range r.Keys {
			pieces = append(pieces, keyOffers([]TurnKey{k}))
		}
	case chorded(r.Keys):
		for _, k := range asChords(r.Keys) {
			pieces = append(pieces, keyOffers([]TurnKey{k}))
		}
	default:
		for _, k := range r.Keys {
			pieces = append(pieces, inertOffers([]TurnKey{k}))
		}
		if r.Handover != "" {
			pieces = append(pieces, handoverOffer(r.Handover, handoverWords))
		}
	}
	sep := sty.Dim.Render(" · ")
	var rows []string
	line := ""
	for _, p := range pieces {
		if line != "" && lipgloss.Width(line+sep+p) > room {
			rows = append(rows, line)
			line = ""
		}
		if line == "" {
			line = Clip(p, room)
			continue
		}
		line += sep + p
	}
	return append(rows, line)
}

// RetryLine is the row a retry leaves where the reader's words would stand:
// at the prompt's column, dim and with no band, saying what it asks again.
// NewModel names the model where the retry is on another one than the
// failure was; empty, it is the same model.
type RetryLine struct {
	NewModel string
}

// View draws the line at the pane's width.
func (r RetryLine) View(width int) string {
	what := "same prompt, same model"
	if r.NewModel != "" {
		what = "same prompt, now on " + r.NewModel
	}
	return Clip(sty.Dim.Render("↻ try again · "+what), width)
}
