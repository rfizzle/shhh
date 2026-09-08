package ui

import (
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"
	"github.com/rfizzle/shhh/internal/radius"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// Styles is the generate UI's style set, built by newStyles from a token set
// and nothing else. Colors come from the shared components.Palette so
// the generate and chat UIs use identical tokens.
type Styles struct {
	// The generated command, drawn as two runs: the glyph that says the line
	// is a command, and the line itself. The line is the one bold bright run
	// on the screen — cover the colour and it is still the thing the one-shot
	// was asked for — and the glyph is what says so with no colour at all.
	CommandGlyph lipgloss.Style
	Command      lipgloss.Style
	Error        lipgloss.Style
	// Label is a field's own name — `edit: `, `feedback: `, `explanation:`.
	// One style rather than three, because they are the same thing said in
	// three places: a word the reader reads past on the way to the field,
	// which is what Status is for.
	Label       lipgloss.Style
	ExplainBody lipgloss.Style

	// The result surface: the key row, the containment line, the risk
	// line, and the dimmed command a revise is being compared against.
	Key        lipgloss.Style
	KeyLabel   lipgloss.Style
	PrimaryKey lipgloss.Style
	DangerKey  lipgloss.Style
	Reach      lipgloss.Style
	// Risk is the top of the ladder and Caution the rest of it: a HIGH
	// reading is shouted in Del, a low or medium one stated in Accent. One
	// colour for both makes an argument shhh could not resolve the same red
	// as a recursive forced deletion.
	Risk        lipgloss.Style
	Caution     lipgloss.Style
	Dim         lipgloss.Style
	PastCommand lipgloss.Style
}

var (
	sty Styles

	Narrow bool
)

// newStyles builds the whole set from one token set, reading no global.
func newStyles(p components.ColorTokens) Styles {
	return Styles{
		// Add used to carry the command, which made the answer the same
		// green as the key that runs it and left the screen with two
		// primaries. The command is now the screen's only bold bright run
		// and Add is the key row's alone.
		// See docs/interface/surfaces.md#the-one-shot-result.
		CommandGlyph: lipgloss.NewStyle().Foreground(p.Accent.Color()),
		Command:      lipgloss.NewStyle().Bold(true).Foreground(p.Bright.Color()),
		Error:        lipgloss.NewStyle().Foreground(p.Del.Color()),

		Label:       lipgloss.NewStyle().Foreground(p.Status.Color()).MarginTop(1),
		ExplainBody: lipgloss.NewStyle().Foreground(p.Body.Color()),

		// Every key the interface offers is Info; the default and the
		// deliberate one carry their tone as well, and both say it in words
		// too.
		Key:         lipgloss.NewStyle().Foreground(p.Info.Color()),
		KeyLabel:    lipgloss.NewStyle().Foreground(p.Dim.Color()),
		PrimaryKey:  lipgloss.NewStyle().Foreground(p.Add.Color()),
		DangerKey:   lipgloss.NewStyle().Foreground(p.Del.Color()),
		Reach:       lipgloss.NewStyle().Foreground(p.Status.Color()),
		Risk:        lipgloss.NewStyle().Foreground(p.Del.Color()),
		Caution:     lipgloss.NewStyle().Foreground(p.Accent.Color()),
		Dim:         lipgloss.NewStyle().Foreground(p.Dim.Color()),
		PastCommand: lipgloss.NewStyle().Foreground(p.Dim.Color()),
	}
}

// commandLine is a generated command as every surface in the one-shot draws
// one: the glyph that says the line is a command, then the line. The two runs
// are what keep the command readable with the colour covered — the glyph is
// the word the invariant asks for beside the tone.
// See docs/interface/principles.md#colour-never-carries-meaning-alone.
func commandLine(command string) string {
	return sty.CommandGlyph.Render("$ ") + sty.Command.Render(command)
}

// renderLines draws a block one line at a time. A style handed the whole
// block pads every short line out to the widest one — lipgloss aligns a
// multi-line render, and left-aligned is still aligned — which on a surface
// that draws inline is a paragraph with a ragged margin of trailing spaces
// behind it.
func renderLines(style lipgloss.Style, s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = style.Render(line)
	}
	return strings.Join(lines, "\n")
}

// riskStyle is the ladder the warning is drawn on: Del at HIGH, Accent below
// it. The level comes from the same resolver the containment line under it
// comes from, so the two cannot disagree about what the command is.
// See docs/interface/principles.md#weight-tracks-risk.
func riskStyle(level radius.Level) lipgloss.Style {
	if level == radius.High {
		return sty.Risk
	}
	return sty.Caution
}

// applyPalette rebuilds the styles, and settles the width class the surface
// lays out against.
//
// It used to settle the colour profile too, dropping the terminal to Ascii
// under NO_COLOR and TERM=dumb. v2 has no global profile to set — a Style
// carries a resolved colour and nothing degrades it on the way out — so that
// rule moved to where the profile is now decided, beside the palette it
// belongs to (components.detectProfile). It reads the same and it
// reaches every surface rather than only the ones that import this package.
func applyPalette() {
	width, _, err := term.GetSize(os.Stdout.Fd())
	if err != nil {
		width = 80
	}
	Narrow = width < 40

	sty = newStyles(components.Palette)
}

func init() {
	applyPalette()
	// The one-shot generate UI honours the mono swap through the same shared
	// palette the chat TUI uses.
	components.OnPaletteChange(applyPalette)
}
