package components

// One key row, one renderer (
// docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
//
// Every key row the product draws — a card's run under its rule, a screen's
// footer, the frame's bottom rail, reading mode's bar, the one-shot's action
// bar — is a run of the same segment painted by the same code. The rows used
// to be laid out by five painters, each with its own idea of which offer a
// narrow terminal gives up first, and the one they disagreed about most was
// esc: one row cut its words to a bare `[esc]`, another let the frame cut it
// off the end. A segment carries its own priority now, so the order a row
// gives things up in is written where the row is built, and the renderer
// holds the one rule every row keeps: the safe answer is the last thing to go
// (docs/interface/principles.md#esc-is-always-the-safe-answer).

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// KeyOffer is one segment of a key row: a key, the gloss for what it does,
// and the priority it holds its place on the row with. A key row has three
// states and this carries the two a live row can be in: an offer is info, and
// the answer that costs nothing is add. A key in any other colour is not an
// offer — the row is not live, or the key is not
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
//
// A segment with no key is a fact the row states rather than an offer it
// makes — what is waiting elsewhere on the screen, the name of the screen the
// row belongs to — and is painted in the row's own tone for facts.
type KeyOffer struct {
	Key, Label string
	// Chord is the same offer spelled as the chord that reaches it while the
	// row's letters are not live — from the draft, where `r` under `[r]
	// try again` is a letter of the sentence being typed. A row draws whichever
	// of the two is true where it stands, and an offer with no chord is one
	// whose surface holds the keyboard, so its key is live as it is
	// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
	Chord string
	// Safe marks the answer that changes nothing. It is esc, everywhere, and
	// it is set off the register's spelling rather than at each row
	// (keyoffers.go), so a surface cannot forget it
	// (docs/interface/principles.md#esc-is-always-the-safe-answer).
	Safe bool
	// Reason is why a key that is on screen cannot act, said in words rather
	// than left to the colour (invariant 1). The whole segment goes grey.
	Reason string
	// Priority is the order a row too narrow for its whole run gives
	// segments up in, the way a ticket's is: zero is the floor and the
	// largest number goes first, the rightmost of two equal ones before the
	// other. The safe answer outranks every number — it is the last segment
	// any row drops, whatever priority it was built with.
	Priority int
	// Tone is the key's weight where a row has more than one kind of
	// offer on it: the one-shot's bar draws its way in, its run and its
	// way out in three.
	Tone OfferTone
	// inert marks an offer that cannot act where it stands — the turns
	// screen's review on a turn that changed nothing. It keeps its place in
	// the row, grey with its words, the treatment a key that is not live
	// takes everywhere (inertOffers), so the row does not change shape under
	// a pointer walking a list.
	inert bool
}

// At is the segment with its place in the order a row gives things up in.
func (o KeyOffer) At(priority int) KeyOffer { o.Priority = priority; return o }

// OfferTone is the weight a key is drawn at, where a row has more than one.
type OfferTone int

const (
	// OfferPlain is every key that is simply offered.
	OfferPlain OfferTone = iota
	// OfferPrimary is the key the row leads with.
	OfferPrimary
	// OfferDanger is a key whose act cannot be taken back.
	OfferDanger
	// OfferQuiet is a key drawn in its gloss's tone, the way out of a row
	// whose other keys all act.
	OfferQuiet
)

// TurnKey is the turn close's name for the same thing; the two were
// separate structs until recovery rows needed the third.
type TurnKey = KeyOffer

// HintTones are the styles one kind of row paints its segments in. The
// renderer is one; what differs between a card's run and a screen's footer
// is only which grey the words are in and whether the safe answer's words
// take its colour, so that is all a row says about itself.
type HintTones struct {
	// Key is an offered key; Gloss the words after it; Sep the separator
	// between two segments.
	Key, Gloss, Sep lipgloss.Style
	// Safe is the safe answer's key; with SafeWhole set its words wear it
	// too, which is the card's grammar: what makes it the safe answer is the
	// whole clause and not the letter.
	Safe      lipgloss.Style
	SafeWhole bool
	// Whole paints every segment as one run in its key's tone, for a row
	// drawn in a single grey because none of its keys is live.
	Whole bool
	// Inert is a key that cannot act where it stands; Absent a key stated
	// absent with its reason, painted whole; Fact a segment with no key.
	Inert, Absent, Fact lipgloss.Style
	// Primary, Danger and Quiet are the keys of a row that weighs them.
	Primary, Danger, Quiet lipgloss.Style
	// Gap is the separator's text, " · " unless a row says otherwise.
	Gap string
}

// gap is the separator's text.
func (t HintTones) gap() string {
	if t.Gap == "" {
		return " · "
	}
	return t.Gap
}

// keyStyle is the tone the segment's key is drawn in.
func (t HintTones) keyStyle(o KeyOffer) lipgloss.Style {
	switch {
	case o.inert:
		return t.Inert
	case o.Safe:
		return t.Safe
	}
	switch o.Tone {
	case OfferPrimary:
		return t.Primary
	case OfferDanger:
		return t.Danger
	case OfferQuiet:
		return t.Quiet
	}
	return t.Key
}

// Paint renders one segment.
func (t HintTones) Paint(o KeyOffer) string {
	switch {
	case o.Key == "":
		return t.Fact.Render(o.Label)
	case o.Reason != "":
		return t.Absent.Render(o.Key + " " + o.Label + " — " + o.Reason)
	case o.Label == "":
		// A key offered bare — the tone carrying what it does — gets no
		// trailing space, since that space is a column the row is short of.
		return t.keyStyle(o).Render(o.Key)
	case t.Whole || (o.Safe && t.SafeWhole && !o.inert):
		return t.keyStyle(o).Render(o.Key + " " + o.Label)
	}
	return t.keyStyle(o).Render(o.Key) + t.Gloss.Render(" "+o.Label)
}

// Join renders a run of segments on one row, separated, and nothing else: a
// caller that has already decided the run fits.
func (t HintTones) Join(segs []KeyOffer) string {
	parts := make([]string, 0, len(segs))
	for _, s := range segs {
		parts = append(parts, t.Paint(s))
	}
	return strings.Join(parts, t.Sep.Render(t.gap()))
}

// Width is the columns the run takes on one row.
func (t HintTones) Width(segs []KeyOffer) int { return lipgloss.Width(t.Join(segs)) }

// Shed is the run in every form a row narrows through, whole first, each
// form one segment shorter than the one before: the largest priority goes
// first, the rightmost of two equal ones before the other, and the safe
// answer only once nothing else is left to give. The last form holds one
// segment. A row with something beside its run — a position field, a
// count — walks the forms itself and asks of each whether the field still
// fits; a row with nothing beside it calls Fit.
func Shed(segs []KeyOffer) [][]KeyOffer {
	forms := [][]KeyOffer{segs}
	cur := segs
	for len(cur) > 1 {
		drop := -1
		for i, s := range cur {
			if s.Safe {
				continue
			}
			if drop < 0 || s.Priority >= cur[drop].Priority {
				drop = i
			}
		}
		if drop < 0 {
			// Nothing left but safe answers: the rightmost of them goes, so
			// the row still ends on one.
			drop = 0
		}
		cur = append(cur[:drop:drop], cur[drop+1:]...)
		forms = append(forms, cur)
	}
	return forms
}

// Fit is the run on one row no wider than width: segments given up whole in
// the order Shed gives, and where even the last one standing is too wide,
// its gloss cut at the edge with `…` — the one place a key row ends on an
// ellipsis, because the key is still whole beside it. A row is never cut
// anywhere else (docs/interface/principles.md#fold-never-hide).
func (t HintTones) Fit(segs []KeyOffer, width int) string {
	if len(segs) == 0 || width <= 0 {
		return ""
	}
	forms := Shed(segs)
	for _, form := range forms {
		if row := t.Join(form); lipgloss.Width(row) <= width {
			return row
		}
	}
	return t.cut(forms[len(forms)-1][0], width)
}

// cut is one segment too wide for its row, its gloss ended at the edge.
func (t HintTones) cut(o KeyOffer, width int) string {
	row := t.Paint(o)
	if o.Key == "" || o.Label == "" || lipgloss.Width(o.Key)+2 > width {
		return Clip(row, width)
	}
	return ansi.Truncate(row, width, "…")
}

// Pack lays the run out in as few rows as width allows, breaking between one
// segment and the next and never inside one: half of `[esc] back` on a row
// is not an offer anybody can take. It is the other answer to a row that will
// not fit, for the surfaces that can spend a second row rather than lose an
// offer — every segment is something the surface can do, and a terminal that
// cannot carry them side by side gets more rows rather than fewer offers
// (docs/interface/principles.md#fold-never-hide).
//
// The safe answer is the last segment of a run that packs as well as one
// that sheds: a row built in the card's order ends on it, and where it will
// not fit beside the row before it, it takes a row of its own with its words
// whole rather than giving them up.
//
// Only a segment alone on its row can be wider than the room, since a
// second one would have started a row of its own. It is folded at word
// boundaries onto the rows under it, the continuation indented under its
// words rather than under its bracket, so the key column reads down the block
// as keys and nothing else.
func (t HintTones) Pack(segs []KeyOffer, width int) []string {
	var rows []string
	var line []KeyOffer
	flush := func() {
		if len(line) == 0 {
			return
		}
		row := t.Join(line)
		// Below the width a card is framed at the rows are drawn bare and
		// nothing is readable, and a fold there would spend the body's rows
		// one word at a time.
		if len(line) == 1 && width >= minCardWidth-cardFrameWidth && lipgloss.Width(row) > width {
			rows = append(rows, foldSegment(row, line[0], width)...)
			line = nil
			return
		}
		line = nil
		rows = append(rows, row)
	}
	for _, s := range segs {
		next := append(append([]KeyOffer{}, line...), s)
		if len(line) > 0 && lipgloss.Width(t.Join(next)) > width {
			flush()
		}
		line = append(line, s)
	}
	flush()
	return rows
}

// Floor is how many of the run's segments a row gives up only once
// nothing else is left: the zero priorities and the safe answer.
func Floor(segs []KeyOffer) int {
	n := 0
	for _, s := range segs {
		if s.Safe || s.Priority == 0 {
			n++
		}
	}
	return n
}

// CloseOn puts one more segment at the end of rows already laid out, by the
// rule Pack lays every segment by: it joins the last row where it fits there,
// and otherwise takes a row of its own, whole. It is how a run drawn in one
// treatment ends on an offer drawn in another — a dimmed run whose way out is
// still live.
func (t HintTones) CloseOn(rows []string, o KeyOffer, width int) []string {
	seg := t.Paint(o)
	if n := len(rows); n > 0 {
		if joined := rows[n-1] + t.Sep.Render(t.gap()) + seg; lipgloss.Width(joined) <= width {
			rows[n-1] = joined
			return rows
		}
	}
	return append(rows, t.Pack([]KeyOffer{o}, width)...)
}

// foldSegment breaks one painted segment at word boundaries into rows no
// wider than width. The key stays in front of the first row and every
// continuation is indented under the words rather than under the bracket, so
// the key column reads down the block as keys and nothing else. It never ends
// on an ellipsis, which is the frame's mark for a clipped target: an offer's
// tail is the part that states the consequence
// (docs/interface/principles.md#fold-never-hide).
func foldSegment(seg string, o KeyOffer, width int) []string {
	plain := ansi.Strip(seg)
	indent := 0
	if o.Key != "" {
		indent = lipgloss.Width(o.Key) + 1
	}
	// A key so wide that little is left beside it gives the continuation the
	// whole row instead.
	if indent > width/2 {
		indent = 0
	}
	var rows []string
	lo, col, room := 0, 0, width
	end := 0 // the column the current row's last word ends at
	for _, word := range strings.Split(plain, " ") {
		w := ansi.StringWidth(word)
		if end > lo && col+w-lo > room {
			rows = append(rows, ansi.Cut(seg, lo, end))
			lo, room = col, width-indent
		}
		end = col + w
		col = end + 1
	}
	rows = append(rows, ansi.Cut(seg, lo, end))
	pad := strings.Repeat(" ", indent)
	for i := 1; i < len(rows); i++ {
		rows[i] = pad + rows[i]
	}
	return rows
}

// liveTones are a run whose surface holds the keyboard: every key it offers
// in Key, the words for it dim, the safe answer's key in add.
func liveTones() HintTones {
	return HintTones{
		Key: sty.key, Gloss: sty.dim, Sep: sty.dim,
		Safe: sty.add, Inert: sty.dimmer, Absent: sty.dim, Fact: sty.dim,
		Primary: sty.key, Danger: sty.key, Quiet: sty.dim,
	}
}

// inertTones are the same run on a surface that does not hold the keyboard.
// The keys drop out of info — which is the colour that means "you can press
// this" — and go grey with their words, the same treatment the card's
// not-yet-live key row takes.
func inertTones() HintTones {
	t := liveTones()
	t.Key, t.Safe, t.Primary, t.Danger, t.Quiet = sty.dimmer, sty.dimmer, sty.dimmer, sty.dimmer, sty.dimmer
	return t
}

// cardTones are a decision card's run: the key in Key, the imperative after
// it in body text, and the answer that costs nothing in add, words and all.
func cardTones() HintTones {
	t := liveTones()
	t.Gloss, t.SafeWhole = sty.body, true
	return t
}

// toneTones are a run drawn wholly in one tone — a footnote, a line whose
// keys are not offers yet.
func toneTones(s lipgloss.Style) HintTones {
	return HintTones{
		Key: s, Gloss: s, Sep: s, Safe: s, Inert: s, Absent: s, Fact: s,
		Primary: s, Danger: s, Quiet: s, Whole: true,
	}
}

// LiveTones are liveTones for a host outside this package that lays a row
// of its own in this package's palette.
func LiveTones() HintTones { return liveTones() }

// ToneTones are toneTones for a host that draws a run in one style of its
// own.
func ToneTones(s lipgloss.Style) HintTones { return toneTones(s) }
