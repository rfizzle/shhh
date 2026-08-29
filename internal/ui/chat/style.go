package chat

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// All colors come from the shared components.Palette (DESIGN-TUI.md §10) so
// the chat and generate UIs stay visually consistent; no new colors without
// adding a token there.
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
	// Permission-mode segment (DESIGN-TUI.md §8): permissive vs gated modes
	// (the orchestrator's bar renders through components.Cockpit; these back
	// the child-scoped bar, S-077).
	ModePermissive lipgloss.Style
	ModeGated      lipgloss.Style
	CtxAlert       lipgloss.Style
	UpdateNotice   lipgloss.Style
	// Focus-mode gutter pointer on the selected transcript row (§7).
	FocusMarker lipgloss.Style

	// The reading rail under the header, which says the transcript has the
	// keyboard rather than the input (S-115, §7a) — navigate.go.
	Reading readingStyles
	// Step outline (S-090, §13) — the header's title, ordinal, faint rule
	// and stats, plus one style per state glyph.
	Step stepStyles
	// The input frame and its rails (§12) — frame.go.
	Frame frameStyles
	// The slash-command menu (S-079) — complete.go.
	Complete completeStyles
	// The reading-mode hint line and the mutation rail (§7a, §14) —
	// readinghint.go.
	Hint hintStyles
	// The two-pane cockpit (§15) — inspector.go.
	Pane paneStyles
}

// stepStyles is the step outline's own group (S-090, §13).
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
		User:       lipgloss.NewStyle().Bold(true).Foreground(p.Info),
		Assistant:  lipgloss.NewStyle().Bold(true).Foreground(p.Add),
		Error:      lipgloss.NewStyle().Foreground(p.Del),
		SystemMsg:  lipgloss.NewStyle().Foreground(p.Dim).Italic(true),
		Header:     lipgloss.NewStyle().Bold(true).Foreground(p.Bright),
		HeaderHint: lipgloss.NewStyle().Foreground(p.Dim),
		Welcome:    lipgloss.NewStyle().Foreground(p.Dim).Italic(true),
		Tool:       lipgloss.NewStyle().Foreground(p.Accent),
		ToolArgs:   lipgloss.NewStyle().Foreground(p.Dim),
		StatusBar:  lipgloss.NewStyle().Foreground(p.Status),

		ModePermissive: lipgloss.NewStyle().Foreground(p.Add),
		ModeGated:      lipgloss.NewStyle().Foreground(p.Accent),
		CtxAlert:       lipgloss.NewStyle().Bold(true).Foreground(p.Del),
		UpdateNotice:   lipgloss.NewStyle().Foreground(p.Accent),

		// The reading cursor is info, as the pointer is on every artboard
		// that draws one; the accent belongs to the mutation rail beside it
		// (§14).
		FocusMarker: lipgloss.NewStyle().Foreground(p.Info),

		Step: stepStyles{
			Title:     lipgloss.NewStyle().Foreground(p.Body),
			LiveTitle: lipgloss.NewStyle().Foreground(p.Bright),
			Rule:      lipgloss.NewStyle().Foreground(p.Dim),
			Stats:     lipgloss.NewStyle().Foreground(p.Dim),
			Dim:       lipgloss.NewStyle().Foreground(p.Dim),
			Done:      lipgloss.NewStyle().Foreground(p.Add),
			Fail:      lipgloss.NewStyle().Foreground(p.Del),
			Run:       lipgloss.NewStyle().Foreground(p.Spin),
		},

		Reading:  newReadingStyles(p),
		Frame:    newFrameStyles(p),
		Complete: newCompleteStyles(p),
		Hint:     newHintStyles(p),
		Pane:     newPaneStyles(p),
	}
}
