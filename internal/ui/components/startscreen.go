package components

// The first-contact screen (
// docs/interface/surfaces.md#the-start-screen). An empty session used to be
// one italic sentence in the middle of a blank viewport, which told a new
// reader nothing and a returning one less. The screen states what shhh
// already knows about the checkout it opened in — the path, the toolchain,
// the branch and its dirty count, the package count — names the files it read
// into the system prompt and the gate that will run without asking, and then
// offers three concrete pieces of work rather than a blinking cursor.
//
// It is a passive renderer like every other component here: the host computes
// the facts once at session start and feeds them in. Nothing on this surface
// is derived per frame.

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// StartFact is one clause of the header line: `~/src/shhh`, `go 1.24`,
// `3 files changed`. The tone only makes the clause that matters findable —
// the words carry the meaning, as everywhere else.
type StartFact struct {
	Text string
	Tone FieldTone
	// Lead marks the clause that opens the line (the path), which is bright
	// rather than dim because it is the one thing a reader checks first.
	Lead bool
}

// StartNote is one labelled line under the header: what was read for context,
// which quality gate is in effect. The label column is aligned across notes,
// so the values read as a column rather than as prose.
type StartNote struct {
	Label string
	Value string
	// Detail qualifies the value in dim text on the same line.
	Detail string
}

// StartSuggestion is one offered piece of work. Glyph distinguishes picking
// something up (`▸`) from starting something new (`⚙`); Detail says what it
// will cost in permission, which is the thing a reader wants before pressing
// enter.
type StartSuggestion struct {
	Glyph  string
	Title  string
	Detail string
}

// StartScreen is the empty session's surface.
type StartScreen struct {
	// Facts is the header line, joined with · and clipped as one line.
	Facts []StartFact
	Notes []StartNote
	// Lead introduces the suggestion list; empty hides the list's heading.
	Lead        string
	Suggestions []StartSuggestion
	Focus       int
	// Hint is the key line under the list. It is dropped along with the
	// suggestions once the reader starts typing, because a key nothing
	// accepts is not an offer.
	Hint string
	// Nav is the second key line: how to move between the prompt and the
	// transcript. It outlives the typing dismissal that takes
	// Hint, because those keys outlive it too — the wheel, pgup and ctrl+o
	// work with a half-written draft in the box, which is the whole point of
	// them.
	Nav string
	// Height is how many rows the pane this is drawn in has, which decides
	// how much of a face the screen wears. Zero is a host that has not said —
	// a bare model, a test — and it gets the screen with no face at all
	// rather than a guess about a terminal nobody measured.
	Height int
}

// suggestionGutter is the two columns the ❯ pointer occupies. Focus is a
// pointer and not only a highlight: a background survives no monochrome
// terminal, and the row's own glyph already means something else.
const suggestionGutter = 2

// View renders the screen at the given width.
func (s StartScreen) View(width int) string {
	if width <= 0 {
		return ""
	}
	rows := s.faceRows(width)
	if line := s.factLine(width); line != "" {
		rows = append(rows, line)
	}
	if notes := s.noteRows(width); len(notes) > 0 {
		rows = append(rows, "")
		rows = append(rows, notes...)
	}
	if len(s.Suggestions) > 0 {
		rows = append(rows, "")
		if s.Lead != "" {
			rows = append(rows, sty.Dim.Render(Clip(s.Lead, width)))
		}
		rows = append(rows, s.suggestionRows(width)...)
	}
	if s.Hint != "" {
		rows = append(rows, "", sty.Hint.Render(Clip(s.Hint, width)))
	}
	if s.Nav != "" {
		if s.Hint == "" {
			rows = append(rows, "")
		}
		rows = append(rows, sty.Hint.Render(Clip(s.Nav, width)))
	}
	return strings.Join(rows, "\n")
}

// The product's name as this screen wears it
// (docs/interface/surfaces.md#the-start-screen). The letters are drawn from
// the half blocks rather than from a font, because a font is a dependency and
// four letters are three rows of table:
//
//	▄▀▀▀ █    █    █
//	 ▀▀▄ █▀▀▄ █▀▀▄ █▀▀▄
//	▄▄▄▀ █  █ █  █ █  █
//
// The rows are one string each rather than a per-letter table for the same
// reason the anim's frames are prerendered: there is one word to draw and it
// never changes, so a letterform table would be machinery in front of a
// constant.
var startWordmark = [3]string{
	"▄▀▀▀ █    █    █",
	" ▀▀▄ █▀▀▄ █▀▀▄ █▀▀▄",
	"▄▄▄▀ █  █ █  █ █  █",
}

// startWordmarkWidth is what the widest of those rows measures. A pane
// narrower than the name plus a column of air gets the one-row face instead.
// It is read off the rows rather than written down beside them, because a
// letterform edited by one cell and a number left at nineteen is a wordmark
// that clips itself on exactly one terminal width — and it is the widest of
// the three rather than whichever one is widest today, for the same reason.
var startWordmarkWidth = widestRow(startWordmark[:])

// widestRow is the display width of the longest of the rows.
func widestRow(rows []string) int {
	widest := 0
	for _, row := range rows {
		widest = max(widest, lipgloss.Width(row))
	}
	return widest
}

// startTrailGaps are the columns between the birth marks that carry the
// wordmark off its own right edge, in order. The mark is the one the working
// label stands an unarrived cell in for, so the name reads as still arriving
// — the same claim the entrance makes, made once, on the screen a session
// opens with.
//
// The trail thins in spacing rather than in shade because there is no second
// dim to thin it with: the palette holds one, and a thinning drawn in a
// colour the table does not name is a colour the mono swap cannot answer for.
var startTrailGaps = []int{1, 1, 2, 3, 5}

// startFaceHeight is the shortest pane the three-row wordmark is drawn in.
// The screen's own rows run from twelve on a wide terminal to around eighteen
// once the notes and the offers wrap their details onto lines of their own,
// and the face costs four more; twenty-four is the first height that holds
// the widest of those with the face above it and a row still to spare. Under
// it those four rows come out of the offers, which are the reason the screen
// exists, so the name goes in one row of the texture instead — which costs
// two.
const startFaceHeight = 24

// faceRows is the product's face and the blank under it, or nothing.
//
// Nothing is what a monochrome terminal gets. The face carries no fact — the
// fact line under it is where the reader is going — so it is decoration, and
// decoration is the first thing a palette with two greys to spend gives up
// (docs/interface/principles.md#colour-never-carries-meaning-alone). The
// terminals that ask for that palette by name, NO_COLOR and TERM=dumb, are
// also the ones least likely to draw half blocks at all.
func (s StartScreen) faceRows(width int) []string {
	if s.Height <= 0 || Mono() {
		return nil
	}
	if s.Height >= startFaceHeight && width >= startWordmarkWidth+1 {
		return append(wordmarkRows(width), "")
	}
	if row := nameRule(width); row != "" {
		return []string{row, ""}
	}
	return nil
}

// wordmarkRows draws the name in bright with the trail behind its middle row.
func wordmarkRows(width int) []string {
	rows := make([]string, 0, len(startWordmark))
	for i, row := range startWordmark {
		painted := brightStyle().Render(row)
		if i == 1 {
			if trail := startTrail(width - lipgloss.Width(row)); trail != "" {
				painted += sty.Dim.Render(trail)
			}
		}
		rows = append(rows, Clip(painted, width))
	}
	return rows
}

// startTrail is the run of birth marks, as much of it as the room allows. A
// mark whose gap does not fit is not drawn at all: a trail that ends in a
// clipped space is a trail that ends in nothing visible.
func startTrail(room int) string {
	var b strings.Builder
	for _, gap := range startTrailGaps {
		if room < gap+1 {
			break
		}
		b.WriteString(strings.Repeat(" ", gap) + animBirthMark)
		room -= gap + 1
	}
	return b.String()
}

// nameRule is the one-row face: the name sitting in the texture, in the
// grammar a card's top edge puts its title in, so the short screen and the
// tall one are the same product rather than two.
func nameRule(width int) string {
	// Two columns of texture, a space either side of the name, and whatever
	// the row has left after them. Under three columns left there is no rule
	// behind the name, only a glyph or two trailing it, which reads as a
	// stray character rather than as chrome — so the row is not drawn at all
	// and the screen opens on its facts, the way it does with no face.
	fill := width - 4 - lipgloss.Width(startName)
	if fill < 3 {
		return ""
	}
	return sty.Dim.Render(textureFill(2)) + " " + brightStyle().Render(startName) +
		" " + sty.Dim.Render(textureFill(fill))
}

// startName is what the product calls itself. It is here rather than borrowed
// from the binary's own name because a renamed executable is still shhh, and
// a face that reads `./shhh-linux-amd64` is not a face.
const startName = "shhh"

// factLine renders the header clauses joined with ·, dropping clauses from
// the right until the line fits. The path is never dropped: a header that
// cannot say where it is has nothing left to say.
func (s StartScreen) factLine(width int) string {
	facts := s.Facts
	for {
		line := joinFacts(facts)
		if len(facts) <= 1 || lipgloss.Width(line) <= width {
			return Clip(line, width)
		}
		facts = facts[:len(facts)-1]
	}
}

func joinFacts(facts []StartFact) string {
	var b strings.Builder
	for i, f := range facts {
		if i > 0 {
			b.WriteString(sty.Dim.Render(" · "))
		}
		style := f.Tone.style()
		if f.Lead {
			style = brightStyle()
		}
		b.WriteString(style.Render(f.Text))
	}
	return b.String()
}

// noteRows renders the labelled lines with their labels in one column. A
// detail that does not fit beside its value moves under it rather than being
// clipped mid-word: the detail is what makes the value actionable — which
// checks the gate runs, where the config was looked for.
func (s StartScreen) noteRows(width int) []string {
	label := 0
	for _, n := range s.Notes {
		label = max(label, lipgloss.Width(n.Label))
	}
	indent := strings.Repeat(" ", label+2)
	rows := make([]string, 0, len(s.Notes))
	for _, n := range s.Notes {
		head := sty.Dim.Render(padRight(n.Label, label)) + "  " + sty.Body.Render(n.Value)
		if n.Detail == "" {
			rows = append(rows, Clip(head, width))
			continue
		}
		if full := head + sty.Dim.Render(" — "+n.Detail); lipgloss.Width(full) <= width {
			rows = append(rows, full)
			continue
		}
		rows = append(rows, Clip(head, width), Clip(indent+sty.Dim.Render(n.Detail), width))
	}
	return rows
}

// suggestionRows renders the offered work. A row that fits keeps its detail
// beside the title; one that does not drops the detail onto its own indented
// line rather than losing it to a clip, because the detail is the permission
// the row costs.
func (s StartScreen) suggestionRows(width int) []string {
	var rows []string
	for i, sg := range s.Suggestions {
		focused := i == s.Focus
		head := strings.Repeat(" ", suggestionGutter) + sg.Glyph + " " + sg.Title
		if focused {
			head = "❯ " + sg.Glyph + " " + sg.Title
		}
		if sg.Detail == "" {
			rows = append(rows, s.row(focused, head, "", width))
			continue
		}
		if lipgloss.Width(head+" — "+sg.Detail) <= width {
			rows = append(rows, s.row(focused, head, sg.Detail, width))
			continue
		}
		rows = append(rows, s.row(focused, head, "", width),
			Clip(strings.Repeat(" ", suggestionGutter+2)+sty.Dim.Render(sg.Detail), width))
	}
	return rows
}

// row styles one suggestion line. The focused row is highlighted whole, the
// way a selected option is everywhere else; the rest carry the glyph in
// accent, the title in body text and the detail dim.
func (s StartScreen) row(focused bool, head, detail string, width int) string {
	if focused {
		line := head
		if detail != "" {
			line += " — " + detail
		}
		return sty.FocusRow.Render(Clip(line, width))
	}
	glyph, title, _ := strings.Cut(strings.TrimLeft(head, " "), " ")
	line := strings.Repeat(" ", suggestionGutter) + sty.Accent.Render(glyph) + " " + sty.Body.Render(title)
	if detail != "" {
		line += sty.Dim.Render(" — " + detail)
	}
	return Clip(line, width)
}

// FocusAfter moves the pointer by delta and returns the new index, bounded to
// the list. The screen has no Update of its own — the input textarea owns the
// keys — so the host asks for the arithmetic rather than repeating it.
func (s StartScreen) FocusAfter(delta int) int {
	if len(s.Suggestions) == 0 {
		return 0
	}
	next := s.Focus + delta
	return min(max(next, 0), len(s.Suggestions)-1)
}
