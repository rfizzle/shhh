package chat

import (
	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// All colors come from the shared components.Palette
// (docs/interface/README.md) so the chat and generate UIs stay visually
// consistent; no new colors without adding a token there.
//
// Styles is this package's whole style set, built by newStyles from a token
// set and nothing else. It replaced seven applyXStyles functions that each
// mutated another file's globals (Finding 2): a group is now a value its own
// file returns, and newStyles composes them, so a surface cannot be left out
// of a rebuild by forgetting to call one more function.
type Styles struct {
	User       lipgloss.Style
	Assistant  lipgloss.Style
	Error      lipgloss.Style
	SystemMsg  lipgloss.Style
	Header     lipgloss.Style
	HeaderHint lipgloss.Style
	Welcome    lipgloss.Style
	Tool       lipgloss.Style
	ToolArgs   lipgloss.Style
	StatusBar  lipgloss.Style
	// Permission-mode segment (docs/interface/surfaces.md#the-input-frame):
	// permissive vs gated modes (the orchestrator's bar renders through
	// components.Cockpit; these back the child-scoped bar, S-077).
	ModePermissive lipgloss.Style
	ModeGated      lipgloss.Style
	CtxAlert       lipgloss.Style
	UpdateNotice   lipgloss.Style
	// Focus-mode gutter pointer on the selected transcript row.
	FocusMarker lipgloss.Style

	// The reading rail under the header, which says the transcript has the
	// keyboard rather than the input (S-115) — navigate.go.
	Reading readingStyles
	// Step outline (S-090) — the header's title, ordinal, faint rule
	// and stats, plus one style per state glyph.
	Step stepStyles
	// The input frame and its rails — frame.go.
	Frame frameStyles
	// The slash-command menu (S-079) — complete.go.
	Complete completeStyles
	// The reading-mode hint line and the mutation rail —
	// readinghint.go.
	Hint hintStyles
	// The two-pane cockpit — inspector.go.
	Pane paneStyles
}

// stepStyles is the step outline's own group (S-090).
type stepStyles struct {
	Title     lipgloss.Style
	LiveTitle lipgloss.Style
	Rule      lipgloss.Style
	Stats     lipgloss.Style
	Dim       lipgloss.Style
	Done      lipgloss.Style
	Fail      lipgloss.Style
	Run       lipgloss.Style
}

// sty is the live style set. init builds it and keeps it current across a
// palette swap (/ui mono, NO_COLOR — S-095); it runs after
// internal/ui/components is fully initialized, so the environment's mono
// decision is already settled.
var sty Styles

func init() {
	applyPalette()
	components.OnPaletteChange(applyPalette)
}

// applyPalette rebuilds this package's styles from the current palette, and
// the diff body's syntax register with them (highlight.go) — it is the same
// palette read in a different register, so it swaps on the same signal.
func applyPalette() {
	sty = newStyles(components.Palette)
	applySyntaxTones(components.Palette)
}

// newStyles builds the whole set from one token set, composing the groups the
// files that draw with them own. It reads its argument and no global, so a
// theme can be rendered in a test without swapping the session's.
func newStyles(p components.ColorTokens) Styles {
	return Styles{
		User:       lipgloss.NewStyle().Bold(true).Foreground(p.Info.Color()),
		Assistant:  lipgloss.NewStyle().Bold(true).Foreground(p.Add.Color()),
		Error:      lipgloss.NewStyle().Foreground(p.Del.Color()),
		SystemMsg:  lipgloss.NewStyle().Foreground(p.Dim.Color()).Italic(true),
		Header:     lipgloss.NewStyle().Bold(true).Foreground(p.Bright.Color()),
		HeaderHint: lipgloss.NewStyle().Foreground(p.Dim.Color()),
		Welcome:    lipgloss.NewStyle().Foreground(p.Dim.Color()).Italic(true),
		Tool:       lipgloss.NewStyle().Foreground(p.Accent.Color()),
		ToolArgs:   lipgloss.NewStyle().Foreground(p.Dim.Color()),
		StatusBar:  lipgloss.NewStyle().Foreground(p.Status.Color()),

		ModePermissive: lipgloss.NewStyle().Foreground(p.Add.Color()),
		ModeGated:      lipgloss.NewStyle().Foreground(p.Accent.Color()),
		CtxAlert:       lipgloss.NewStyle().Bold(true).Foreground(p.Del.Color()),
		UpdateNotice:   lipgloss.NewStyle().Foreground(p.Accent.Color()),

		// The reading cursor is info, as the pointer is on every artboard
		// that draws one; the accent belongs to the mutation rail beside it.
		FocusMarker: lipgloss.NewStyle().Foreground(p.Info.Color()),

		Step: stepStyles{
			Title:     lipgloss.NewStyle().Foreground(p.Body.Color()),
			LiveTitle: lipgloss.NewStyle().Foreground(p.Bright.Color()),
			Rule:      lipgloss.NewStyle().Foreground(p.Dim.Color()),
			Stats:     lipgloss.NewStyle().Foreground(p.Dim.Color()),
			Dim:       lipgloss.NewStyle().Foreground(p.Dim.Color()),
			Done:      lipgloss.NewStyle().Foreground(p.Add.Color()),
			Fail:      lipgloss.NewStyle().Foreground(p.Del.Color()),
			Run:       lipgloss.NewStyle().Foreground(p.Spin.Color()),
		},

		Reading:  newReadingStyles(p),
		Frame:    newFrameStyles(p),
		Complete: newCompleteStyles(p),
		Hint:     newHintStyles(p),
		Pane:     newPaneStyles(p),
	}
}
