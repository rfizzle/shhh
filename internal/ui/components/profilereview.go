package components

// The profile drafter's third widget: the diff and migrate viewer. It draws
// what a profile opened from its file is measured against — the offer to
// move an older shape into the sections, the prompt as the file wrote it
// beside them, and the diff the card's save would make.

import (
	"fmt"
	"strings"

	"github.com/rfizzle/shhh/internal/ui/keys"
)

// profileReview is the diff and migrate viewer. It owns the diff's window
// and whether the wait is a migration; what it draws — the diff, the
// original, the older-shape note — is the host's, held by the wizard and
// handed in. See docs/architecture.md#the-profile-drafters-widgets.
type profileReview struct {
	// diff is the diff's window: it stands where the sections stood while
	// the card holds the keyboard, and the profile's scroll keys move it.
	diff Pager
	// migrating is the wait while a profile opened from its file is moved
	// into the sections: drawn on its own, with the file's headline.
	migrating bool
}

// scroll moves the diff by a row, held inside it against the rows the
// window last drew it in.
func (p *profileReview) scroll(by int) {
	p.diff.Offset = Pager{Offset: p.diff.Offset + by, Height: p.diff.Height, total: p.diff.total}.Held()
}

// olderRow is the offer at the head of a profile opened in the older shape:
// the mark in its glyph and words, what it means, and the key that moves it.
func (p *profileReview) olderRow(note string) string {
	row := sty.accent.Render(OlderShapeMark)
	if note != "" {
		row += sty.dim.Render(" · " + note)
	}
	return row + sty.dim.Render(" · ") + strings.Join(HintRows([]KeyOffer{keyOffer(keys.Profile.Migrate)}, 1<<10), "")
}

// originalColumn is the narrowest width the original prompt is drawn beside
// the sections at; under it the original is drawn after them.
const originalColumn = 100

// originalBeside is the sections with the original prompt readable next to
// them: a column on the right where the width allows, rows after the
// sections where it does not. The starts index the same rows either way.
func (p *profileReview) originalBeside(original string, width int, sections func(width int) ([]string, []int)) ([]string, []int) {
	if original == "" {
		return sections(width)
	}
	if width < originalColumn {
		blocks, starts := sections(width)
		blocks = append(blocks, "", Clip(indent(sty.dim.Render("as the file wrote it")), width))
		for _, line := range p.originalLines(original, width-profileBodyIndent) {
			blocks = append(blocks, Clip(bodyIndent(sty.dim.Render(line)), width))
		}
		return blocks, starts
	}
	right := min(52, width/3)
	left := width - right - 2
	blocks, starts := sections(left)
	column := append([]string{sty.dim.Render("as the file wrote it")}, p.originalLines(original, right-2)...)
	for i, line := range column {
		if i >= len(blocks) {
			blocks = append(blocks, "")
		}
		if i > 0 {
			line = sty.dim.Render(line)
		}
		blocks[i] = padRight(blocks[i], left) + sty.dimmer.Render("│ ") + line
	}
	return blocks, starts
}

// originalLines is the original prompt wrapped paragraph by paragraph.
func (p *profileReview) originalLines(original string, width int) []string {
	var out []string
	for i, para := range strings.Split(strings.TrimSpace(original), "\n\n") {
		if i > 0 {
			out = append(out, "")
		}
		out = append(out, wrapPlain(para, width)...)
	}
	return out
}

// diffWindow is the diff windowed to room rows, with a counted marker for
// the lines above and below it, paid for out of the room.
func (p *profileReview) diffWindow(lines []string, width, room int) []string {
	all := p.diffRows(lines, width)
	if room <= 0 {
		return nil
	}
	if len(all) <= room {
		p.diff = Pager{}
		return all
	}
	height := max(room-2, 1)
	p.diff.Height, p.diff.total = height, len(all)
	p.diff.Offset = p.diff.Held()
	var rows []string
	if p.diff.Offset > 0 {
		rows = append(rows, Clip(indent(sty.dim.Render(fmt.Sprintf("⋮ %s above · %s",
			plural(p.diff.Offset, "more line"), words(keys.Profile.ScrollUp, "scroll up")))), width))
	} else {
		height++
		p.diff.Height = height
	}
	window := p.diff.Window(all)
	rows = append(rows, window...)
	if below := len(all) - p.diff.Offset - len(window); below > 0 {
		rows = append(rows, Clip(indent(sty.dim.Render(fmt.Sprintf("⋮ %s · %s",
			plural(below, "more line"), words(keys.Profile.ScrollDown, "scroll the diff")))), width))
	}
	return rows
}

// diffRows is the save's diff, a line each, in the diff's own tones.
func (p *profileReview) diffRows(lines []string, width int) []string {
	rows := make([]string, 0, len(lines))
	for _, line := range lines {
		style := sty.dim
		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"), strings.HasPrefix(line, "@@"):
			style = sty.dimmer
		case strings.HasPrefix(line, "+"):
			style = sty.add
		case strings.HasPrefix(line, "-"):
			style = sty.del
		}
		rows = append(rows, Clip(indent(style.Render(line)), width))
	}
	return rows
}
