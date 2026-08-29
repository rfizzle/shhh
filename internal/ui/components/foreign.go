package components

// Foreign output (S-150, DESIGN-TUI.md §10i). A detail body is the one place
// in the transcript where bytes shhh did not write reach the screen: a failed
// command's output, a running one's live tail, a provider's error body.
// Programs emit \x1b[31m and trust the terminal to pick a red. Inside shhh
// that red is whatever the reader's theme decided — frequently illegible
// against the terminal's own background, and in every case a colour the
// palette does not own (§10a), sitting one indent away from rows that spent
// S-088 getting one job per token.
//
// So the line is read before it is drawn. It is re-painted the way every
// other surface is painted — as runs of text carrying a lipgloss style — with
// the sixteen colours a terminal theme owns mapped onto the tokens that mean
// the same thing here, and everything else the program asked for kept: bold,
// faint, italic, underline, strikethrough, reverse. Nothing else survives.
//
// Ported from Crush's internal/ui/common/ansi16.go (RemapANSI16 and
// StripCursorControl), with four places where shhh's semantics win:
//
//   - It re-paints runs rather than rewriting SGR parameters. Crush edits the
//     parameter list in place and lets the bytes through; painting through
//     lipgloss puts foreign output behind the same renderer as everything
//     else, so the colour profile, NO_COLOR and the mono swap reach it
//     without this file knowing they exist.
//   - Background colours are dropped rather than remapped. §10b says exactly
//     three background tints exist, and all three collapse onto --mono-bg,
//     which means selection (§7a). A program painting a block of a detail
//     body would be drawing the reading cursor.
//   - Under mono no foreign colour survives at all, the way the diff renderer
//     drops chroma highlighting rather than recolouring it (§10f). A grey
//     step is still a distinction, and a detail body is exactly where the
//     words are already carrying one.
//   - The sequence vocabulary is closed, not filtered. Crush strips the
//     cursor and screen controls it has seen corrupt a viewport; here the
//     only sequence a detail body carries is the one that colours text, so
//     cursor moves, erases, mode changes, window titles and OSC 52 clipboard
//     writes all leave by the same door rather than by name.

import (
	"fmt"
	"image/color"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// ansiPalette maps the sixteen colours a terminal theme owns onto the tokens
// that mean the same thing in shhh (§10i). Black is dim rather than black
// because the terminal's black is the background on half the terminals there
// are.
//
// The bright half is not a second palette: shhh has one token per meaning, so
// a program's red and its bright red are both del — a failure is a failure.
// Bold is what still says which of the two the program was emphasising, and
// bold passes through untouched.
//
// It reads FullPalette rather than the live Palette and is not rebuilt on a
// swap, because mono declines foreign colour outright instead of recolouring
// it: with mono on, foreignRun never reaches this table.
var ansiPalette = ansiTable(FullPalette)

func ansiTable(p ColorTokens) [16]Token {
	return [16]Token{
		p.Dim, p.Del, p.Add, p.Accent, p.Info, p.Spin, p.Hunk, p.Body,
		p.Dim, p.Del, p.Add, p.Accent, p.Info, p.Spin, p.Hunk, p.Bright,
	}
}

// foreignRun is the state one run of foreign text carries: what the program
// last asked for, resolved against the body's own ground. A run with nothing
// asked of it is the ground and only the ground, which is what makes the
// uncoloured majority of tool output render identically to the way it did
// before this file existed.
type foreignRun struct {
	fg                                color.Color
	bold, faint, italic               bool
	underline, strike, reverse, blink bool
}

// style resolves the run against the ground: the program's foreground where
// there is one and the palette is showing colour at all, the ground
// otherwise, plus whatever attributes the program set.
func (r foreignRun) style(ground Token) lipgloss.Style {
	fg := ground.Color()
	if r.fg != nil && !mono {
		fg = r.fg
	}
	return lipgloss.NewStyle().Foreground(fg).
		Bold(r.bold).Faint(r.faint).Italic(r.italic).
		Underline(r.underline).Strikethrough(r.strike).
		Reverse(r.reverse).Blink(r.blink)
}

// repaint re-paints one line of a program's own output in shhh's materials.
// ground is the token the body around it is drawn in — dimmer for a detail
// body (§6a) — so a run the program left alone comes back the colour the body
// would have been anyway, and the caller has nothing left to add.
//
// It reports false, and returns the line untouched, where there was nothing
// to re-paint. That is every line shhh wrote itself, which is nearly all of
// them: the caller styles those the way it always did.
func repaint(s string, ground Token) (string, bool) {
	if !strings.ContainsAny(s, "\x1b\r") {
		return s, false
	}

	var out, run strings.Builder
	out.Grow(len(s))
	var state foreignRun

	flush := func() {
		if run.Len() == 0 {
			return
		}
		out.WriteString(state.style(ground).Render(run.String()))
		run.Reset()
	}

	parser := ansi.GetParser()
	defer ansi.PutParser(parser)

	var pstate byte
	for len(s) > 0 {
		parser.Reset()
		seq, _, n, next := ansi.DecodeSequence(s, pstate, parser)
		rest := s[n:]

		switch {
		case ansi.HasCsiPrefix(seq) && parser.Command() == 'm':
			// The one sequence a detail body carries. Runs are flushed
			// before the state moves, so what was already written keeps the
			// look it was written with.
			flush()
			state.apply(parser.Params())
		case isSequence(seq):
			// Everything else — cursor moves, erases, mode changes, OSC —
			// is not text and not colour, so it is not a detail body's.
		case seq == "\r" && rest != "" && rest[0] != '\n':
			// A bare carriage return is a progress bar starting the line
			// again: what is on the line is overwritten, so the line so far
			// is dropped and the attributes it was written with stay. A \r
			// that ends the line, or that leads a \n, is a line terminator
			// and means nothing here.
			run.Reset()
			out.Reset()
		case seq == "\r":
		default:
			run.WriteString(seq)
		}

		s, pstate = rest, next
	}
	flush()
	return out.String(), true
}

// isSequence reports whether the decoded token is a control sequence rather
// than text: ESC-introduced, or one of the 8-bit C1 introducers. A UTF-8
// continuation byte never leads a decoded token, so the second test is safe.
func isSequence(seq string) bool {
	if seq == "" {
		return false
	}
	return seq[0] == 0x1b || (seq[0] >= 0x80 && seq[0] <= 0x9f)
}

// apply moves the run state by one SGR sequence. Colour parameters resolve
// through ansiPalette; background and underline colours are read only far
// enough to skip their arguments, so they can never be misread as attributes
// of their own.
func (r *foreignRun) apply(params ansi.Params) {
	if len(params) == 0 {
		// \x1b[m is \x1b[0m.
		*r = foreignRun{}
		return
	}
	for i := 0; i < len(params); i++ {
		p := params[i].Param(0)
		switch {
		case p == 0:
			*r = foreignRun{}
		case p == 1:
			r.bold = true
		case p == 2:
			r.faint = true
		case p == 3:
			r.italic = true
		case p == 4:
			r.underline = true
		case p == 5 || p == 6:
			r.blink = true
		case p == 7:
			r.reverse = true
		case p == 9:
			r.strike = true
		case p == 22:
			r.bold, r.faint = false, false
		case p == 23:
			r.italic = false
		case p == 24:
			r.underline = false
		case p == 25:
			r.blink = false
		case p == 27:
			r.reverse = false
		case p == 29:
			r.strike = false
		case p >= 30 && p <= 37:
			r.fg = ansiPalette[p-30].Color()
		case p >= 90 && p <= 97:
			r.fg = ansiPalette[8+p-90].Color()
		case p == 39:
			r.fg = nil
		case p == 38:
			// An explicit 256-colour or truecolor foreground: a colour the
			// program could see when it chose it, and one the palette has no
			// token to stand in for. It is kept as it was asked for and
			// degrades through the renderer like any other.
			c, next := extendedColor(params, i)
			r.fg, i = c, next
		case p == 48 || p == 58:
			// A background or an underline colour. Skipped, arguments and
			// all (§10i).
			_, next := extendedColor(params, i)
			i = next
		}
	}
}

// extendedColor reads one explicit-colour introducer and its arguments —
// `38;5;n` or `38;2;r;g;b` — starting at params[i]. It returns the colour and
// the index of the last parameter it consumed; a truncated introducer yields
// no colour rather than a guess.
func extendedColor(params ansi.Params, i int) (color.Color, int) {
	if i+1 >= len(params) {
		return nil, i
	}
	switch params[i+1].Param(0) {
	case 5:
		if i+2 >= len(params) {
			return nil, i + 1
		}
		return lipgloss.Color(strconv.Itoa(params[i+2].Param(0))), i + 2
	case 2:
		if i+4 >= len(params) {
			return nil, len(params) - 1
		}
		return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x",
			clampByte(params[i+2].Param(0)),
			clampByte(params[i+3].Param(0)),
			clampByte(params[i+4].Param(0)))), i + 4
	}
	return nil, i + 1
}

// clampByte keeps a malformed channel inside the byte the %02x expects.
func clampByte(v int) int { return min(max(v, 0), 255) }
