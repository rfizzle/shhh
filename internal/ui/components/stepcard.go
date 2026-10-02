package components

// The step card (docs/interface/surfaces.md#the-step): one padded block on
// the band for each step a turn took — a header that is the step's receipt,
// a body that is what the model said when it took the step, and a footer of
// evidence where the step has some. Live and after are the same card; a
// running step keeps its kind's glyph and its duration ticks.
//
// The card is drawn from facts the caller has already read: what the step
// did comes from the step's receipt and is handed over as words, so this
// file knows no tool and computes no rollup. What it owns is the shape —
// which of its fields a narrow pane gives up, and in what order — and the
// band it rests on.

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// CardDensity is how much of a card is drawn: the rung of the density ladder
// (docs/interface/principles.md#density-is-one-ladder) after the reader's own
// open or close has been read over it. The zero value is the default rung.
type CardDensity int

const (
	// CardNormal is the header, the body and the footer.
	CardNormal CardDensity = iota
	// CardLow is the header alone, with no padding rows: the low rung's
	// closed card. The header keeps its pointer, rail and glyph columns, so
	// its text stands in from the band's edge as a padded card's does.
	CardLow
	// CardHigh is the card open: the header, the body and the calls under
	// it, in place of the footer that summarised them.
	CardHigh
)

// StripCell is one call in a step's strip: the kind of act it was, and how
// it stands.
type StripCell struct {
	Kind  ActivityKind
	State ActivityState
}

// StepCard is one step, as its card draws it.
type StepCard struct {
	// Kind is the lead kind of act, which picks the glyph of a finished
	// step; State overrides it the way it overrides a row's.
	Kind  ActivityKind
	State ActivityState
	// Rail is the mutation rail: the step wrote, ran a command or called a
	// server nobody marked read-only. A failed step's rail is del.
	Rail bool
	// Verb leads the receipt in body colour. It is never cut and never
	// dropped: the header is the step's answer and the verb is half of it.
	Verb string
	// Subject is what the lead kind's one call was about, where it had one
	// call. A narrow pane cuts it with … and never drops it.
	Subject string
	// Rollup is the rest of the receipt, dim; Bare is the same without the
	// reads' directory clause. A narrowing pane gives up the directory
	// clause first and then the rollup whole.
	Rollup, Bare string
	// Outcome is the step's answer on the right — `exit 1`, `+88 −5`, `ok`
	// — painted in the tone OutcomeState gives it. It never drops.
	Outcome      string
	OutcomeState ActivityState
	// Mark, where set, is the glyph already painted, standing in for the
	// one the state would draw, and OutcomeAccent paints the outcome in the
	// accent with it: a failure the session will come back from is a stall,
	// `⚠`, and not a break (FailureCard).
	Mark          string
	OutcomeAccent bool
	// OutcomePainted marks an outcome already painted run by run: a
	// fan-out's tally, whose clauses each carry their own tone.
	OutcomePainted bool
	// Verdict is the reading's verdict or the gate's word after the
	// outcome, and VerdictAlert paints it in the accent. It drops second.
	Verdict      string
	VerdictAlert bool
	// Duration is right-most and the first field a narrow pane drops.
	Duration string
	// Matches is what a live search finds behind a card that is not drawing
	// its calls, stated after the outcome. Like the outcome it never drops:
	// a fold that hid the answer to the reader's question without saying so
	// would be hiding rather than folding
	// (docs/interface/principles.md#fold-never-hide).
	Matches string
	// Body is what the model said that titled the step, whole; Reading
	// marks a reading's sentence standing in for it, drawn dimmer and
	// italic. Empty, the card has no body and the footer follows the
	// header.
	Body    string
	Reading bool
	// Tail is a running command's last line of output, under the body while
	// the step runs.
	Tail string
	// ByRule marks a step a rule refused rather than the reader: its ⊘ and
	// its outcome go del, as a refused row's do
	// (docs/interface/principles.md#two-denials-are-not-one-denial).
	ByRule bool
	// Evidence is the footer's line, cut to the pane with …; EvidenceRight
	// is what stands right of it — the verdict, what the call counted.
	Evidence      string
	EvidenceRight string
	// EvidenceAlert paints what stands right of the evidence in del: a rule's
	// refusal stated there rather than on the header.
	EvidenceAlert bool
	// Keys is a live chord the card offers — a refusal's way to the rule's
	// own answer — drawn last in the footer in the key colour. It is never
	// the key enter is: the hint bar says that
	// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
	Keys string
	// Strip is every call in order, which ends the footer of a large step.
	Strip []StripCell
	// Rows are footer rows the host drew under the evidence line, already on
	// the band: a picture a call returned (CardPictureRow), a notice that
	// landed mid-step (CardNoteRow). A card drawn as its header alone or
	// open onto its calls draws none, as it draws no footer.
	Rows []string
	// Calls are the open card's rows, already drawn at the pane's width.
	// They stand where the footer stood, on the band.
	Calls []string
	// Density is how much of the card is drawn. No card has a fold of the
	// reader's: its header alone is the low rung's closed card and nothing
	// else, so the pointer column carries no fold mark.
	Density CardDensity
	// Selected puts the reading cursor on the header and lights it.
	Selected bool
}

// cardMargin is the run held back at the band's right end, so the
// duration does not sit against the edge of the pane.
const cardMargin = 2

// cardVerbColumn is where the header's verb starts: the pointer, the rail
// and the glyph before it, one column, one column and two. It is the body
// column too, so the verb and the sentence under it start in one place.
const cardVerbColumn = 4

// CardBodyIndent is where a card's body, tail and footer start: the verb's
// column.
const CardBodyIndent = cardVerbColumn

// View draws the card at the pane's width.
func (c StepCard) View(width int) string {
	if c.Density == CardLow {
		return c.header(width)
	}
	lines := c.top(width)
	switch c.Density {
	case CardHigh:
		lines = append(lines, cardPad(width))
		for _, call := range c.Calls {
			for _, l := range strings.Split(strings.TrimRight(call, "\n"), "\n") {
				lines = append(lines, onBand(Clip(l, width), width))
			}
		}
	default:
		if f, ok := c.footer(width); ok {
			lines = append(lines, f)
		}
		for _, r := range c.Rows {
			lines = append(lines, strings.Split(strings.TrimRight(r, "\n"), "\n")...)
		}
	}
	lines = append(lines, cardPad(width))
	return strings.Join(lines, "\n")
}

// CardPictureRow is a picture a step's call returned, as a footer row of its
// card: the picture mark, the file's name, its facts, and on the right that
// it opens the way an attachment the reader sent does — on the attachment
// card (docs/interface/surfaces.md#the-step). The facts give way to the name
// on a narrow pane, and the right-hand words before either.
type CardPictureRow struct {
	Name string
	// Facts are the picture's size and dimensions, `1440×900 · 212 KB`.
	Facts string
	// Selected puts the reading cursor on the row.
	Selected bool
}

// cardPictureOpens is what the row says on its right: not a key — the hint
// bar names enter — but what pressing it will do.
const cardPictureOpens = "opens like an attachment"

// View draws the row at the pane's width.
func (p CardPictureRow) View(width int) string {
	inner := max(width-cardMargin-CardBodyIndent, 1)
	mark := sty.Dim.Render(ChipImage.mark())
	said := Clip(p.Name, max(inner-2, 1))
	facts := ""
	if p.Facts != "" && lipgloss.Width(said)+2+3+lipgloss.Width(p.Facts) <= inner {
		facts = sty.Dim.Render(" · " + p.Facts)
	}
	left := mark + " " + sty.Dimmer.Render(said) + facts
	right := ""
	if lipgloss.Width(left)+closeMinNoteGap+lipgloss.Width(cardPictureOpens) <= inner {
		right = sty.Dim.Render(cardPictureOpens)
	}
	gap := max(inner-lipgloss.Width(left)-lipgloss.Width(right), 1)
	line := strings.Repeat(" ", CardBodyIndent) + left + strings.Repeat(" ", gap) + right
	if p.Selected {
		return cardLit(line, CardBodyIndent, width)
	}
	return onBand(Clip(line, width), width)
}

// CardNoteRow is a line the session wrote about the step rather than one of
// the step's own — the tree moving under it — as a dim footer row of the
// card it landed in. It wraps rather than clips: it is the only place the
// card says what moved, and a fact cut off the edge is a fact hidden
// (docs/interface/principles.md#fold-never-hide).
func CardNoteRow(text string, width int) string {
	inner := max(width-cardMargin-CardBodyIndent, 1)
	var lines []string
	for _, l := range strings.Split(lipgloss.Wrap(text, inner, ""), "\n") {
		lines = append(lines, onBand(strings.Repeat(" ", CardBodyIndent)+
			sty.Dimmer.Render(Clip(strings.TrimRight(l, " "), inner)), width))
	}
	return strings.Join(lines, "\n")
}

// Head is an open card down to where its calls begin: the padding row, the
// header, the body and the padding row under it. A host that draws the
// calls as rows of its own — so a cursor can stand on each — lays them on
// the band with OnBand after it and closes the card with CardPad.
func (c StepCard) Head(width int) string {
	return strings.Join(append(c.top(width), cardPad(width)), "\n")
}

// top is the card's opening rows: the padding row, the header, the body,
// and a running command's tail.
func (c StepCard) top(width int) []string {
	lines := []string{cardPad(width), c.header(width)}
	lines = append(lines, c.bodyLines(width)...)
	if c.State == ActivityRunning && c.Tail != "" {
		lines = append(lines, onBand(indented(c.Tail, CardBodyIndent, width-cardMargin), width))
	}
	return lines
}

// OnBand lays one line of a card on the band (onBand).
func OnBand(line string, width int) string { return onBand(Clip(line, width), width) }

// CardPad is a card's padding row (cardPad).
func CardPad(width int) string { return cardPad(width) }

// headerFit is one way of filling the header: which of the fields that drop
// are still standing.
type headerFit struct {
	rollup            string
	verdict, duration bool
}

// header is the card's first line: pointer, rail, glyph, verb, the rest of
// the receipt dim, and the outcome, verdict and duration on the right. What
// the pane cannot hold it gives up in one order — the duration, the
// verdict, the directory clause, the rollup — and what it never gives up is
// the glyph, the rail, the verb and the outcome; a lone call's subject is
// cut with … rather than dropped, since a card that said `ran` and nothing
// else would not say what ran.
func (c StepCard) header(width int) string {
	fits := []headerFit{
		{c.Rollup, true, true},
		{c.Rollup, true, false},
		{c.Rollup, false, false},
		{c.Bare, false, false},
		{"", false, false},
	}
	fit := fits[len(fits)-1]
	for _, f := range fits {
		if c.headerWidth(f, lipgloss.Width(c.Subject)) <= width {
			fit = f
			break
		}
	}
	subject := c.Subject
	if over := c.headerWidth(fit, lipgloss.Width(subject)) - width; over > 0 && subject != "" {
		subject = Clip(subject, max(lipgloss.Width(subject)-over, 1))
	}

	verbTone := sty.Body
	if c.State == ActivityQueued {
		verbTone = sty.Dim
	}
	left := verbTone.Render(c.Verb)
	if subject != "" {
		left += " " + sty.Dimmer.Render(subject)
	}
	if fit.rollup != "" {
		sep := " "
		if subject != "" {
			sep = " · "
		}
		left += sty.Dim.Render(sep + fit.rollup)
	}
	right := c.headerRight(fit)
	// One blank column at least between the receipt and the right-hand run:
	// touching, the two would read as one word.
	// See docs/interface/departures.md#a-cards-header-keeps-a-column-between-its-sides.
	gap := max(width-cardVerbColumn-lipgloss.Width(left)-lipgloss.Width(right)-cardMargin, 1)
	rest := c.railCell() + c.glyph() + " " + left + strings.Repeat(" ", gap) + right
	rest = Clip(rest, max(width-1, 0))
	if c.Selected {
		return sty.FocusPointer.Render("❯") + LitRowKeeping(rest, 0, -1, max(width-1, 0))
	}
	return onBand(" "+rest, width)
}

// headerWidth is how many columns the header takes with the fields fit
// leaves standing and a subject that many columns wide.
func (c StepCard) headerWidth(fit headerFit, subjectW int) int {
	w := cardVerbColumn + lipgloss.Width(c.Verb)
	if subjectW > 0 {
		w += 1 + subjectW
	}
	if fit.rollup != "" {
		w += 1 + lipgloss.Width(fit.rollup)
		if subjectW > 0 {
			w += 2
		}
	}
	if r := lipgloss.Width(c.headerRight(fit)); r > 0 {
		w += 1 + r
	}
	return w + cardMargin
}

// headerRight is the right-hand run: the outcome, the verdict and the
// duration that fit leaves standing, joined the way every outcome field is.
func (c StepCard) headerRight(fit headerFit) string {
	var parts []string
	if c.Outcome != "" {
		parts = append(parts, c.outcome())
	}
	if c.Matches != "" {
		parts = append(parts, sty.Dim.Render(c.Matches))
	}
	if fit.verdict && c.Verdict != "" {
		parts = append(parts, c.verdict(c.Verdict))
	}
	if fit.duration && c.Duration != "" {
		parts = append(parts, sty.Dim.Render(c.Duration))
	}
	return strings.Join(parts, sty.Dim.Render(" · "))
}

// outcome paints the step's answer: a write's line counts in the diff's two
// tokens, and anything else in the tone of how the step stands.
func (c StepCard) outcome() string {
	if c.OutcomePainted {
		return c.Outcome
	}
	if c.OutcomeAccent {
		return sty.Accent.Render(c.Outcome)
	}
	if painted, ok := paintLineCounts(c.Outcome); ok {
		return painted
	}
	switch c.OutcomeState {
	case ActivityFailed:
		return sty.Del.Render(c.Outcome)
	case ActivityDenied:
		if c.ByRule {
			return sty.Del.Render(c.Outcome)
		}
		return sty.Dim.Render(c.Outcome)
	case ActivityQueued:
		return sty.Dim.Render(c.Outcome)
	case ActivityRunning:
		return sty.SpinText.Render(c.Outcome)
	}
	return sty.Add.Render(c.Outcome)
}

// verdict paints a verdict or a gate's word: dim, a person's `approved` in
// add as it is on a row, and a reading that departed in the accent.
func (c StepCard) verdict(v string) string {
	if c.VerdictAlert {
		return sty.Accent.Render(v)
	}
	return paintAccount(v, sty.Dim)
}

// railCell is the mutation rail's column: accent, or del on a step that
// broke, and blank on a step that only read.
func (c StepCard) railCell() string {
	switch {
	case !c.Rail:
		return " "
	case c.State == ActivityFailed, c.State == ActivityDenied && c.ByRule:
		return sty.Del.Render("▎")
	}
	return sty.Accent.Render("▎")
}

// glyph is the step's mark: the state where it overrides, the lead kind of
// act otherwise. A read keeps the accent on a card's header, where a read
// row's glyph is dim: the card is the step's answer, and its mark is what
// the eye runs down the transcript for.
//
// A running step keeps its kind's mark, still: the frame's status already
// says the turn is working, so a spinner here would be a second animation
// telling the same fact, and the ticking duration is what says this step is
// the live one. See docs/interface/surfaces.md#the-step.
func (c StepCard) glyph() string {
	if c.Mark != "" {
		return c.Mark
	}
	switch c.State {
	case ActivityDenied:
		if c.ByRule {
			return sty.Del.Render(stateGlyphs[ActivityDenied].mark)
		}
		return stateGlyphs[ActivityDenied].render()
	case ActivityFailed, ActivityQueued, ActivityChecking:
		return stateGlyphs[c.State].render()
	}
	if c.Kind == ActivityTool {
		return sty.Accent.Render(kindGlyphs[ActivityTool].mark)
	}
	if int(c.Kind) < len(kindGlyphs) {
		return kindGlyphs[c.Kind].render()
	}
	return sty.Accent.Render(kindGlyphs[ActivityTool].mark)
}

// bodyLines is the sentence that titled the step, whole, wrapped at the
// body column and held two columns short of the band's edge. A reading's
// sentence standing in for it is dimmer and italic, which is the register
// the model talking about the work is drawn in.
func (c StepCard) bodyLines(width int) []string {
	text := strings.TrimSpace(ansi.Strip(c.Body))
	if text == "" {
		return nil
	}
	tone := sty.Body
	if c.Reading {
		tone = sty.Dimmer.Italic(true)
	}
	inner := max(width-CardBodyIndent-cardMargin, 1)
	var lines []string
	for _, para := range strings.Split(text, "\n") {
		for _, l := range strings.Split(lipgloss.Wrap(strings.TrimSpace(para), inner, ""), "\n") {
			l = Clip(strings.TrimRight(l, " "), inner)
			if l == "" {
				lines = append(lines, cardPad(width))
				continue
			}
			lines = append(lines, onBand(strings.Repeat(" ", CardBodyIndent)+tone.Render(l), width))
		}
	}
	return lines
}

// footer is the evidence line and what stands right of it, drawn only where
// there is some. The line is the tool's own text and is cut with … to leave
// the right-hand run its place, which never moves.
func (c StepCard) footer(width int) (string, bool) {
	if c.Evidence == "" && c.EvidenceRight == "" && len(c.Strip) == 0 && c.Keys == "" {
		return "", false
	}
	inner := max(width-CardBodyIndent-cardMargin, 1)
	var right []string
	switch {
	case c.EvidenceRight != "" && c.EvidenceAlert:
		right = append(right, sty.Del.Render(c.EvidenceRight))
	case c.EvidenceRight != "":
		right = append(right, paintCounts(c.EvidenceRight, sty.Dim))
	}
	if c.Keys != "" {
		right = append(right, sty.Key.Render(Clip(c.Keys, inner)))
	}
	if len(c.Strip) > 0 {
		used := lipgloss.Width(strings.Join(right, " "))
		if used > 0 {
			used++
		}
		right = append(right, stepStripRun(c.Strip, inner-used))
	}
	rightRun := strings.Join(right, " ")
	rightW := lipgloss.Width(rightRun)
	room := inner - rightW
	if rightW > 0 {
		room--
	}
	ev := ""
	if c.Evidence != "" && room > 0 {
		if painted, ok := repaint(c.Evidence, Palette.Dimmer); ok {
			ev = Clip(painted, room)
		} else {
			ev = sty.Dimmer.Render(Clip(c.Evidence, room))
		}
	}
	gap := max(inner-lipgloss.Width(ev)-rightW, 0)
	line := strings.Repeat(" ", CardBodyIndent) + ev + strings.Repeat(" ", gap) + rightRun
	return onBand(Clip(line, width), width), true
}

// stepStripRun is the strip as one run of glyphs, each call's own: a read dim,
// anything that acted in the accent, a break in del, a refusal dim. Where
// the pane cannot hold every call it keeps the latest, behind a …, because
// the end of a step is where it stands.
func stepStripRun(cells []StripCell, room int) string {
	if room <= 0 {
		return ""
	}
	if len(cells) > room {
		cells = cells[len(cells)-(room-1):]
		return sty.Dim.Render("…") + stepStripRun(cells, room-1)
	}
	var b strings.Builder
	for _, cell := range cells {
		b.WriteString(stripCell(cell))
	}
	return b.String()
}

// stripCell is one call's glyph in the strip.
func stripCell(cell StripCell) string {
	switch cell.State {
	case ActivityFailed:
		return sty.Del.Render(stateGlyphs[ActivityFailed].mark)
	case ActivityDenied:
		return stateGlyphs[ActivityDenied].render()
	case ActivityRunning:
		return sty.SpinText.Render(stateGlyphs[ActivityRunning].mark)
	}
	if int(cell.Kind) < len(kindGlyphs) && cell.Kind != ActivityTool {
		return kindGlyphs[cell.Kind].render()
	}
	return kindGlyphs[ActivityTool].render()
}

// cardPad is a padding row: the band and nothing on it. Where the palette
// has no band it is an empty row, and the padding rows alone are the card.
func cardPad(width int) string { return onBand("", width) }

// onBand lays a line on the band, carried to the pane's edge so the band
// does not stop where the words do, and re-armed after every reset inside
// it so a painted word does not punch a hole in the ground. Where the
// palette has no band — mono, sixteen colours — the line is left as it was
// and its trailing blanks go, as they do on every other row.
func onBand(line string, width int) string {
	bg := backgroundSeq(CardBand())
	if bg == "" {
		return strings.TrimRight(line, " ")
	}
	pad := max(width-lipgloss.Width(line), 0)
	return bg + strings.ReplaceAll(line, ansiReset, ansiReset+bg) + strings.Repeat(" ", pad) + ansiReset
}

// CardBand is the ground a card is drawn on: the palette's band, the same
// whether or not the screen under it is painted. The dark band is half of
// the catalogue's pair with the dark ground, so stepping it off whatever
// ground stood under it would draw a card the catalogue does not.
// See docs/interface/departures.md#a-card-on-a-painted-ground-steps-its-band-up.
func CardBand() Token { return Palette.Band }
