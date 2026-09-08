package components

// Foreign output (docs/interface/principles.md#one-grid). A detail
// body is the one place in the transcript where bytes shhh did not write
// reach the screen: a failed command's output, a running one's live tail, a
// provider's error body. Programs emit \x1b[31m and trust the terminal to
// pick a red. Inside shhh that red is whatever the reader's theme decided —
// frequently illegible against the terminal's own background, and in every
// case a colour the palette does not own, sitting one indent away from
// rows that were built to give one job per token.
//
// So the line is read before it is drawn. It is re-painted the way every
// other surface is painted — as runs of text carrying a lipgloss style — with
// every colour a program can name arriving as one of the fifteen tokens: the
// sixteen a terminal theme owns by what they mean, the cube, the greyscale
// ramp and a truecolor triple by what they look like. Bold, faint, underline
// and strikethrough are kept, because they are emphasis rather than colour
// and cost the palette nothing. Nothing else survives.
//
// Ported from Crush's internal/ui/common/ansi16.go (RemapANSI16 and
// StripCursorControl), with four places where shhh's semantics win:
//
//   - It re-paints runs rather than rewriting SGR parameters. Crush edits the
//     parameter list in place and lets the bytes through; painting through
//     lipgloss puts foreign output behind the same renderer as everything
//     else, so the colour profile, NO_COLOR and the mono swap reach it
//     without this file knowing they exist.
//   - Background colours are dropped rather than remapped, and reverse video
//     with them. The palette allows exactly three background tints, and all
//     three collapse onto --mono-bg, which means selection. A program
//     painting a block of a detail body would be drawing the reading cursor.
//   - Under mono no foreign colour survives at all, the way the diff renderer
//     drops chroma highlighting rather than recolouring it. A grey
//     step is still a distinction, and a detail body is exactly where the
//     words are already carrying one.
//   - The sequence vocabulary is closed, not filtered. Crush strips the
//     cursor and screen controls it has seen corrupt a viewport; here the
//     only sequence a detail body carries is the one that colours text, so
//     cursor moves, erases, mode changes, window titles and OSC 52 clipboard
//     writes all leave by the same door rather than by name.

import (
	"image/color"
	"math"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// ansiPalette maps the sixteen colours a terminal theme owns onto the tokens
// that mean the same thing in shhh. Black is dim rather than black
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
	fg                color.Color
	bold, faint       bool
	underline, strike bool
}

// style resolves the run against the ground: the token the program's colour
// arrived as where there is one and the palette is showing colour at all, the
// ground otherwise, plus the four attributes a program keeps.
func (r foreignRun) style(ground Token) lipgloss.Style {
	fg := ground.Color()
	if r.fg != nil && !mono {
		fg = r.fg
	}
	return lipgloss.NewStyle().Foreground(fg).
		Bold(r.bold).Faint(r.faint).
		Underline(r.underline).Strikethrough(r.strike)
}

// repaint re-paints one line of a program's own output in shhh's materials.
// ground is the token the body around it is drawn in — dimmer for a detail
// body — so a run the program left alone comes back the colour the body
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
// to a token; background and underline colours are read only far enough to
// skip their arguments, so they can never be misread as attributes of their
// own.
//
// Three attributes are read and dropped rather than left to fall through the
// switch, because a reader of this table is owed the reason each one is not
// on the row (docs/interface/departures.md#a-foreign-colour-arrives-as-a-token-and-a-foreign-ground-does-not-arrive).
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
		case p == 3 || p == 23:
			// Italic is the mark on text the model said, and a detail body
			// is the one place on the screen that is nobody's words but a
			// program's. A linter emphasising a rule name in italic would
			// be quoting the model.
		case p == 4:
			r.underline = true
		case p == 5 || p == 6 || p == 25:
			// Blink is not in the type system and never was.
		case p == 7 || p == 27:
			// Reverse video paints the ground with the foreground, which is
			// exactly what the reading cursor does to the row it is on. A
			// program does not get to draw the cursor, for the reason a
			// background is dropped.
		case p == 9:
			r.strike = true
		case p == 22:
			r.bold, r.faint = false, false
		case p == 24:
			r.underline = false
		case p == 29:
			r.strike = false
		case p >= 30 && p <= 37:
			r.fg = ansiPalette[p-30].Color()
		case p >= 90 && p <= 97:
			r.fg = ansiPalette[8+p-90].Color()
		case p == 39:
			r.fg = nil
		case p == 38:
			// An explicit 256-colour or truecolor foreground. It arrives as
			// the token nearest it, the way the sixteen arrive as the token
			// that means what they mean — never as itself, which is the one
			// route by which a colour the palette did not issue could reach
			// the screen.
			tok, ok, next := extendedColor(params, i)
			r.fg, i = nil, next
			if ok {
				r.fg = tok.Color()
			}
		case p == 48 || p == 58:
			// A background or an underline colour. Skipped, arguments and
			// all.
			_, _, next := extendedColor(params, i)
			i = next
		}
	}
}

// extendedColor reads one explicit-colour introducer and its arguments —
// `38;5;n` or `38;2;r;g;b` — starting at params[i]. It returns the token that
// colour arrives as and the index of the last parameter it consumed; a
// truncated introducer yields no token rather than a guess.
func extendedColor(params ansi.Params, i int) (Token, bool, int) {
	if i+1 >= len(params) {
		return Token{}, false, i
	}
	switch params[i+1].Param(0) {
	case 5:
		if i+2 >= len(params) {
			return Token{}, false, i + 1
		}
		return indexToken(params[i+2].Param(0)), true, i + 2
	case 2:
		if i+4 >= len(params) {
			return Token{}, false, len(params) - 1
		}
		return nearestToken(
			clampByte(params[i+2].Param(0)),
			clampByte(params[i+3].Param(0)),
			clampByte(params[i+4].Param(0))), true, i + 4
	}
	return Token{}, false, i + 1
}

// clampByte keeps a malformed channel inside a byte.
func clampByte(v int) int { return min(max(v, 0), 255) }

// indexToken is one 256-colour index as a token.
//
// The first sixteen go through the same table `31` does: a program that names
// red the long way means what a program that names it the short way means,
// and both are del. The rest are the 6×6×6 cube and the twenty-four-step
// greyscale ramp — values chosen off a chart rather than off a theme — so
// they are read for what they look like and folded onto the nearest token.
func indexToken(n int) Token {
	n = clampByte(n)
	switch {
	case n < 16:
		return ansiPalette[n]
	case n < 232:
		// The cube: three digits base six, each digit one of six levels.
		levels := [6]int{0, 95, 135, 175, 215, 255}
		n -= 16
		return nearestToken(levels[n/36], levels[(n/6)%6], levels[n%6])
	default:
		v := 8 + (n-232)*10
		return nearestToken(v, v, v)
	}
}

// nearestToken folds one colour a program named onto the token that says the
// nearest thing (docs/interface/principles.md#one-grid).
//
// It is two questions rather than one distance, because the palette is not a
// spread of colours to match against: it is six hues with one job each and a
// grey ramp carrying most of the screen, and the two halves are answered by
// different facts about a colour. A colour with hue in it is folded onto the
// token whose hue is nearest, which is the same judgement the sixteen already
// get — a program's red is del whether it wrote 31, 38;5;196 or 38;2;255;0;0.
// A colour with none is folded onto the grey whose lightness is nearest.
//
// Distance in RGB would answer neither: every token here is a mid-saturation
// colour chosen to be read for minutes at a time, so pure red is further from
// del in RGB than several greys are, and matching by distance would paint a
// failure grey.
func nearestToken(r, g, b int) Token {
	hi, lo := max(r, max(g, b)), min(r, min(g, b))

	// A colour is achromatic when what hue it has is a rounding error
	// against how light it is: an eighth of the strongest channel, which
	// makes the whole greyscale ramp grey and leaves the palest cube colour
	// a hue. Black has no strongest channel and is grey by the same test.
	if hi-lo <= hi/8 {
		return nearestGrey((hi + lo) / 2)
	}
	return nearestHue(hueOf(r, g, b, hi, lo))
}

// nearestGrey picks the grey token closest in lightness. The five of them are
// the ramp the screen is mostly built from, and a foreign grey joins it
// rather than sitting a shade off it.
func nearestGrey(light int) Token {
	best, bd := foreignGreys[0].tok, math.MaxFloat64
	for _, t := range foreignGreys {
		if d := math.Abs(float64(light) - t.at); d < bd {
			best, bd = t.tok, d
		}
	}
	return best
}

// nearestHue picks the coloured token closest in hue, measured the short way
// round the circle.
func nearestHue(h float64) Token {
	best, bd := foreignHues[0].tok, math.MaxFloat64
	for _, t := range foreignHues {
		d := math.Abs(h - t.at)
		if d > 180 {
			d = 360 - d
		}
		if d < bd {
			best, bd = t.tok, d
		}
	}
	return best
}

// foreignHues and foreignGreys are the tokens a colour a program named can
// arrive as, each beside the one number it is chosen by: a hue in degrees, or
// a lightness.
//
// The six coloured ones are the six the sixteen already map onto, so the
// extended half can reach no token the short form cannot — which is what keeps
// `38;5;n` from being a second, wider palette. The five greys are the ramp
// itself. The four the list leaves out are the three background tints, which a
// foreground may not borrow, and subtle, which belongs to the one-shot UI.
//
// Like ansiPalette they read FullPalette and are not rebuilt on a swap, for
// the same reason: mono declines foreign colour outright rather than
// recolouring it, so with mono on nothing reaches these tables.
var foreignHues, foreignGreys = foreignTargets(FullPalette)

type foreignTarget struct {
	tok Token
	at  float64
}

func foreignTargets(p ColorTokens) (hues, greys []foreignTarget) {
	at := func(t Token, hue bool) foreignTarget {
		r, g, b := rgb8(t.TrueColor)
		hi, lo := max(r, max(g, b)), min(r, min(g, b))
		if hue {
			return foreignTarget{t, hueOf(r, g, b, hi, lo)}
		}
		return foreignTarget{t, float64((hi + lo) / 2)}
	}
	for _, t := range []Token{p.Del, p.Accent, p.Add, p.Hunk, p.Info, p.Spin} {
		hues = append(hues, at(t, true))
	}
	for _, t := range []Token{p.Dim, p.Status, p.Dimmer, p.Body, p.Bright} {
		greys = append(greys, at(t, false))
	}
	return hues, greys
}

// hueOf is the hue in degrees, given the channels and their extremes — the
// textbook conversion, with the negative sixth wrapped rather than left for
// the caller to notice.
func hueOf(r, g, b, hi, lo int) float64 {
	c := float64(hi - lo)
	var h float64
	switch hi {
	case r:
		h = 60 * float64(g-b) / c
	case g:
		h = 60 * (float64(b-r)/c + 2)
	default:
		h = 60 * (float64(r-g)/c + 4)
	}
	if h < 0 {
		h += 360
	}
	return h
}

// rgb8 is a token's own colour as three bytes. Token.TrueColor is the design
// system's hex, which is what the fold is measured against: the token a
// colour is nearest is a fact about the palette and not about what rung of it
// this terminal can show.
func rgb8(c color.Color) (int, int, int) {
	r, g, b, _ := c.RGBA()
	return int(r >> 8), int(g >> 8), int(b >> 8)
}
