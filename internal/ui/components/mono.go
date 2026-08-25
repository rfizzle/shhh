package components

// Monochrome conformance (S-095, DESIGN-TUI.md invariant 1). Colour never
// carries meaning alone: every state pairs its colour with a glyph or a word,
// so a monochrome terminal loses decoration and never information. Mono mode
// is how that invariant is enforced rather than asserted — it strips the
// palette to the two greys of tokens/colors.css (`--mono-fg`, `--mono-dim`,
// plus `--mono-bg` for selection) and leaves bold, glyphs and layout intact.
//
// The swap happens in one place because every surface in both TUIs reads its
// colours from Palette: this package rebuilds its own styles, and hosts with
// derived styles of their own (internal/ui/chat, internal/ui, and the saved-
// chat browser) register a callback with OnPaletteChange.

import (
	"os"

	"github.com/charmbracelet/lipgloss"
)

// The mono palette's three shades, from tokens/colors.css. Two greys carry
// everything: the foreground grey for anything that is content or state, the
// dim grey for chrome. The third is a selection background, which is not a
// colour distinction — the ❯ pointer and [x] box say the same thing.
const (
	MonoFg  = lipgloss.Color("254") // --mono-fg  #e2e2e2
	MonoDim = lipgloss.Color("244") // --mono-dim #7d7d7d
	MonoBg  = lipgloss.Color("237") // --mono-bg  #32363f
)

// MonoPalette is the two-grey token set. Every token that means content,
// state or emphasis collapses onto MonoFg; every token that means chrome
// collapses onto MonoDim. Nothing is left that could distinguish two states
// by hue.
var MonoPalette = ColorTokens{
	Add:     MonoFg,
	Del:     MonoFg,
	AddBg:   MonoBg,
	DelBg:   MonoBg,
	Hunk:    MonoFg,
	Accent:  MonoFg,
	Info:    MonoFg,
	FocusBg: MonoBg,
	Dim:     MonoDim,
	Dimmer:  MonoDim,
	Spin:    MonoFg,
	Status:  MonoDim,
	Bright:  MonoFg,
	Subtle:  MonoDim,
	Body:    MonoFg,
}

var (
	mono          bool
	paletteHooks  []func()
	monoEnvForced bool
)

// Mono reports whether the mono palette is active. Renderers that would
// otherwise emit colours the palette does not own — syntax highlighting,
// markdown styling — consult it and fall back to plain output.
func Mono() bool { return mono }

// MonoForced reports whether the environment (NO_COLOR, TERM=dumb) turned
// mono on, in which case it cannot be turned off from inside the session.
func MonoForced() bool { return monoEnvForced }

// SetMono swaps the palette and rebuilds every derived style, in this package
// and in every host that registered with OnPaletteChange. It is a no-op when
// the mode is already what was asked for.
func SetMono(on bool) {
	if on == mono {
		return
	}
	mono = on
	if on {
		Palette = MonoPalette
	} else {
		Palette = FullPalette
	}
	applyPalette()
	for _, fn := range paletteHooks {
		fn()
	}
}

// OnPaletteChange registers a callback that rebuilds a host package's derived
// styles after a palette swap. Hosts register from their own init, which runs
// after this package is fully initialized, so the callback never fires before
// the host is ready.
func OnPaletteChange(fn func()) { paletteHooks = append(paletteHooks, fn) }

// init settles the palette before any dependent package builds its styles:
// Go initializes imported packages first, so a host reading Palette at var
// time already sees the mono tokens when the environment asked for them.
//
// NO_COLOR and TERM=dumb additionally make the generate UI drop the terminal
// to termenv.Ascii (internal/ui/style.go), which flattens the two greys to
// none — a stricter reading of the same invariant, and the one those
// conventions ask for.
func init() {
	applyPalette()
	if monoFromEnv(os.Getenv) {
		monoEnvForced = true
		SetMono(true)
	}
}

// monoFromEnv reports whether the environment asks for monochrome: NO_COLOR
// set to anything, or a terminal that cannot render attributes at all.
func monoFromEnv(getenv func(string) string) bool {
	return getenv("NO_COLOR") != "" || getenv("TERM") == "dumb"
}
