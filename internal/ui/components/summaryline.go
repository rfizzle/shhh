package components

// The summary row (docs/interface/surfaces.md#the-session-summary): a
// reading the session took of its own run, where the round it read said
// something of its own and so the reading cannot be the card's body. It is
// flat, on bare screen like the model's prose, because it is the model
// talking about the round; dimmer and italic, which is the register that
// talk is drawn in; `≡` in the glyph slot; and its verdict on the right in
// the verdict's colour, the word carrying it so mono loses nothing.

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// SummaryLine is one reading on its own row.
type SummaryLine struct {
	// Text is the reading's sentence, whole.
	Text string
	Tone SummaryTone
	// Offer is the one key a reading offers: a reading that cannot tell is
	// the one that asks the reader to step in. Zero offers nothing.
	Offer KeyOffer
	// Detail is the opened row's furniture under the sentence — the reason
	// behind the verdict, what it was read against — already worded.
	Detail []string
}

// summaryVerdict paints a verdict word: the two that say the run is going
// where it was sent dim, the two that ask for a look in the accent.
func summaryVerdict(t SummaryTone) string {
	switch t {
	case SummaryOffTarget, SummaryUnclear:
		return sty.Accent.Render(SummaryWord(t))
	}
	return sty.Dim.Render(SummaryWord(t))
}

// View draws the row at the pane's width: the sentence wrapped at the body
// column, its first line leaving the verdict its place on the right.
func (s SummaryLine) View(width int) string {
	right := summaryVerdict(s.Tone)
	if s.Offer.Key != "" {
		right += sty.Dim.Render(" · ") + keyOffers([]TurnKey{s.Offer})
	}
	lead := " " + " " + sty.Dim.Render("≡") + " "
	inner := max(width-CardBodyIndent-cardMargin, 1)
	first := max(inner-lipgloss.Width(right)-1, 1)
	tone := sty.Dimmer.Italic(true)
	head, rest := wrapFirst(strings.TrimSpace(s.Text), first, inner)
	line := lead + tone.Render(head)
	gap := max(CardBodyIndent+inner-lipgloss.Width(line)-lipgloss.Width(right), 1)
	lines := []string{Clip(line+strings.Repeat(" ", gap)+right, width)}
	pad := strings.Repeat(" ", CardBodyIndent)
	for _, l := range rest {
		lines = append(lines, pad+tone.Render(l))
	}
	for _, d := range s.Detail {
		lines = append(lines, pad+sty.Dim.Render(Clip(d, inner)))
	}
	return strings.Join(lines, "\n")
}

// wrapFirst wraps text into a first line first columns wide and the rest
// inner columns wide.
func wrapFirst(text string, first, inner int) (string, []string) {
	words := strings.Fields(text)
	head := ""
	for len(words) > 0 {
		next := strings.TrimSpace(head + " " + words[0])
		if lipgloss.Width(next) > first && head != "" {
			break
		}
		head, words = next, words[1:]
	}
	head = Clip(head, first)
	if len(words) == 0 {
		return head, nil
	}
	var rest []string
	for _, l := range strings.Split(lipgloss.Wrap(strings.Join(words, " "), inner, ""), "\n") {
		rest = append(rest, Clip(strings.TrimRight(l, " "), inner))
	}
	return head, rest
}
