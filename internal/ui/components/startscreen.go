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
	var rows []string
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
			rows = append(rows, sty.Dim.Render(clip(s.Lead, width)))
		}
		rows = append(rows, s.suggestionRows(width)...)
	}
	if s.Hint != "" {
		rows = append(rows, "", sty.Hint.Render(clip(s.Hint, width)))
	}
	if s.Nav != "" {
		if s.Hint == "" {
			rows = append(rows, "")
		}
		rows = append(rows, sty.Hint.Render(clip(s.Nav, width)))
	}
	return strings.Join(rows, "\n")
}

// factLine renders the header clauses joined with ·, dropping clauses from
// the right until the line fits. The path is never dropped: a header that
// cannot say where it is has nothing left to say.
func (s StartScreen) factLine(width int) string {
	facts := s.Facts
	for {
		line := joinFacts(facts)
		if len(facts) <= 1 || lipgloss.Width(line) <= width {
			return clip(line, width)
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
			rows = append(rows, clip(head, width))
			continue
		}
		if full := head + sty.Dim.Render(" — "+n.Detail); lipgloss.Width(full) <= width {
			rows = append(rows, full)
			continue
		}
		rows = append(rows, clip(head, width), clip(indent+sty.Dim.Render(n.Detail), width))
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
			clip(strings.Repeat(" ", suggestionGutter+2)+sty.Dim.Render(sg.Detail), width))
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
		return sty.FocusRow.Render(clip(line, width))
	}
	glyph, title, _ := strings.Cut(strings.TrimLeft(head, " "), " ")
	line := strings.Repeat(" ", suggestionGutter) + sty.Accent.Render(glyph) + " " + sty.Body.Render(title)
	if detail != "" {
		line += sty.Dim.Render(" — " + detail)
	}
	return clip(line, width)
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
