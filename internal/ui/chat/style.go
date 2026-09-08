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
// set and nothing else. It replaced a set of functions that each mutated
// another file's globals: a group is a value now, and newStyles composes
// them, so a surface cannot be left out of a rebuild by forgetting to call
// one more function.
//
// Every group and every constructor lives in this file rather than beside the
// code that draws with it. That is the point: a style is only correct if it
// is rebuilt when the palette changes, and applyPalette below is the one
// place that happens. A table declared in a renderer is a table someone can
// build at init or per call without noticing which of the two they did — the
// first goes stale on /theme and /mono, the second allocates on every frame.
// A `lipgloss.NewStyle()` outside this file is therefore a thing to explain,
// and the three that remain each carry the explanation: the transcript's two
// search marks and the drag selection are structural rather than coloured
// (viewport.go, select.go), and the syntax segment takes the tone its lexer
// chose (highlight.go).
type Styles struct {
	User       lipgloss.Style
	Assistant  lipgloss.Style
	Error      lipgloss.Style
	SystemMsg  lipgloss.Style
	Header     lipgloss.Style
	HeaderHint lipgloss.Style
	Welcome    lipgloss.Style
	// CompactSummary is the summary a compaction produced, quoted under the
	// receipt row that announced it (context.go). It is the one italic this
	// package sets, because the slant means quoted model output and nothing
	// else: hints, fold markers, notices, viewer bars and the welcome line are
	// the product's own voice, so they stay upright and say what they are with
	// their grey. The rule is the design system's *Type* rule
	// (docs/interface/README.md names where it is normative).
	CompactSummary lipgloss.Style
	Tool           lipgloss.Style
	ToolArgs       lipgloss.Style
	StatusBar      lipgloss.Style
	// Divider is the faint rule under the header and above the bottom
	// panel; the width is the caller's, the colour is the palette's.
	Divider lipgloss.Style
	// Viewport is the transcript pane's own box, given its width and height
	// at the moment it renders so every row is the same length
	// (viewport.go). It carries no token yet and is here anyway: it is the
	// pane's style, and a pane that gains a ground has one place to gain it
	// rather than a NewStyle() call in the middle of a render.
	Viewport lipgloss.Style
	// Permission-mode segment (docs/interface/surfaces.md#the-input-frame):
	// permissive vs gated modes (the orchestrator's bar renders through
	// components.Cockpit; these back the child-scoped bar).
	ModePermissive lipgloss.Style
	ModeGated      lipgloss.Style
	CtxAlert       lipgloss.Style
	UpdateNotice   lipgloss.Style
	// Focus-mode gutter pointer on the selected transcript row.
	FocusMarker lipgloss.Style

	// The reading rail under the header, which says the transcript has the
	// keyboard rather than the input — navigate.go.
	Reading readingStyles
	// Step outline — the header's title, ordinal, faint rule
	// and stats, plus one style per state glyph.
	Step stepStyles
	// The input frame and its rails — frame.go.
	Frame frameStyles
	// The slash-command menu — complete.go.
	Complete completeStyles
	// The input history search's row — historysearch.go.
	Search searchStyles
	// The reading-mode hint line and the mutation rail —
	// readinghint.go.
	Hint hintStyles
	// The two-pane cockpit — inspector.go.
	Pane paneStyles
}

// stepStyles is the step outline's own group.
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

// readingStyles is the reading rail's own group.
type readingStyles struct {
	Label lipgloss.Style
	Rule  lipgloss.Style
}

func newReadingStyles(p components.ColorTokens) readingStyles {
	return readingStyles{
		// The label is info and bold, as DRAFT, DECISION and READING all are
		// in guidelines/invariant-inert-keys; the rule it sits on is chrome,
		// so it is dim like every other divider. The accent belongs to the
		// rows.
		Label: lipgloss.NewStyle().Bold(true).Foreground(p.Info.Color()),
		Rule:  lipgloss.NewStyle().Foreground(p.Dim.Color()),
	}
}

// frameStyles is the input frame's own group, built by newFrameStyles.
type frameStyles struct {
	AccentPermissive lipgloss.Style
	AccentGated      lipgloss.Style
	AccentChecking   lipgloss.Style
	Idle             lipgloss.Style
	Hint             lipgloss.Style
	GutterIdle       lipgloss.Style
	GutterWork       lipgloss.Style
	GutterBang       lipgloss.Style
	NoticeInfo       lipgloss.Style
	NoticeAlert      lipgloss.Style
	// The undressed draft and the waiting chip a decision puts on the frame
	//: the chrome goes dim, the characters stay legible.
	DraftHeld   lipgloss.Style
	WaitingChip lipgloss.Style
	// The attached breadcrumb on the top rail: the path and the session's
	// name in Status, and each agent along the path in Info, which is the
	// token a sub-agent wears wherever one is named.
	Identity      lipgloss.Style
	IdentityChild lipgloss.Style
}

func newFrameStyles(p components.ColorTokens) frameStyles {
	return frameStyles{
		AccentPermissive: lipgloss.NewStyle().Foreground(p.Add.Color()),
		AccentGated:      lipgloss.NewStyle().Foreground(p.Accent.Color()),
		AccentChecking:   lipgloss.NewStyle().Foreground(p.Spin.Color()),
		Idle:             lipgloss.NewStyle().Foreground(p.Dim.Color()),
		Hint:             lipgloss.NewStyle().Foreground(p.Dim.Color()),
		GutterIdle:       lipgloss.NewStyle().Bold(true).Foreground(p.Info.Color()),
		GutterWork:       lipgloss.NewStyle().Bold(true).Foreground(p.Spin.Color()),
		// The bang draft's glyph carries the gated accent: what enter does
		// next is ask, on the confirm card.
		GutterBang:    lipgloss.NewStyle().Bold(true).Foreground(p.Accent.Color()),
		NoticeInfo:    lipgloss.NewStyle().Foreground(p.Info.Color()),
		NoticeAlert:   lipgloss.NewStyle().Foreground(p.Del.Color()),
		DraftHeld:     lipgloss.NewStyle().Foreground(p.Body.Color()),
		WaitingChip:   lipgloss.NewStyle().Bold(true).Foreground(p.Accent.Color()),
		Identity:      lipgloss.NewStyle().Foreground(p.Status.Color()),
		IdentityChild: lipgloss.NewStyle().Foreground(p.Info.Color()),
	}
}

// completeStyles is the slash-command menu's own group.
type completeStyles struct {
	Focus lipgloss.Style
	Name  lipgloss.Style
	Args  lipgloss.Style
	Desc  lipgloss.Style
	Off   lipgloss.Style
	Hint  lipgloss.Style
}

func newCompleteStyles(p components.ColorTokens) completeStyles {
	return completeStyles{
		Focus: lipgloss.NewStyle().Bold(true).Background(p.FocusBg.Color()),
		// The unlit row's command name. It went out unpainted until the menu
		// started greying what it cannot offer: the terminal's own foreground
		// is a colour the palette never issued and differs between two
		// terminals side by side, so a grey row beside it was a difference
		// from nothing rather than a state
		// (docs/interface/principles.md#one-grid).
		Name: lipgloss.NewStyle().Foreground(p.Body.Color()),
		Args: lipgloss.NewStyle().Foreground(p.Dim.Color()),
		Desc: lipgloss.NewStyle().Foreground(p.Dim.Color()),
		// A row the running turn has put out of reach, in the grey the
		// selector greys an unavailable option in — the two menus are one
		// answer to "why is /compact missing" and say it the same way. The ⊘
		// in front of the name is what carries it on a terminal with no
		// colour at all (invariant 1).
		Off:  lipgloss.NewStyle().Foreground(p.Dimmer.Color()),
		Hint: lipgloss.NewStyle().Foreground(p.Dim.Color()),
	}
}

// searchStyles is the history-search row's own group.
type searchStyles struct {
	Label lipgloss.Style
	Query lipgloss.Style
	State lipgloss.Style
	Hint  lipgloss.Style
}

func newSearchStyles(p components.ColorTokens) searchStyles {
	return searchStyles{
		Label: lipgloss.NewStyle().Bold(true).Foreground(p.Info.Color()),
		Query: lipgloss.NewStyle().Foreground(p.Body.Color()),
		State: lipgloss.NewStyle().Foreground(p.Dim.Color()),
		Hint:  lipgloss.NewStyle().Foreground(p.Dim.Color()),
	}
}

// hintStyles is the reading-mode hint line's own group, with the
// mutation rail that shares its file.
type hintStyles struct {
	Key          lipgloss.Style
	Safe         lipgloss.Style
	Dim          lipgloss.Style
	MutationRail lipgloss.Style
}

func newHintStyles(p components.ColorTokens) hintStyles {
	return hintStyles{
		Key:          lipgloss.NewStyle().Foreground(p.Info.Color()),
		Safe:         lipgloss.NewStyle().Foreground(p.Add.Color()),
		Dim:          lipgloss.NewStyle().Foreground(p.Dim.Color()),
		MutationRail: lipgloss.NewStyle().Foreground(p.Accent.Color()),
	}
}

// paneStyles is the two-pane cockpit's own group.
type paneStyles struct {
	Divider lipgloss.Style
}

func newPaneStyles(p components.ColorTokens) paneStyles {
	return paneStyles{Divider: lipgloss.NewStyle().Foreground(p.Dim.Color())}
}

// sty is the live style set. init builds it and keeps it current across a
// palette swap (/ui mono, NO_COLOR); it runs after
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
		SystemMsg:  lipgloss.NewStyle().Foreground(p.Dim.Color()),
		Header:     lipgloss.NewStyle().Bold(true).Foreground(p.Bright.Color()),
		HeaderHint: lipgloss.NewStyle().Foreground(p.Dim.Color()),
		Welcome:    lipgloss.NewStyle().Foreground(p.Dim.Color()),

		// On its own, because it is the only italic in the file.
		CompactSummary: lipgloss.NewStyle().Foreground(p.Dimmer.Color()).Italic(true),

		Tool:      lipgloss.NewStyle().Foreground(p.Accent.Color()),
		ToolArgs:  lipgloss.NewStyle().Foreground(p.Dim.Color()),
		StatusBar: lipgloss.NewStyle().Foreground(p.Status.Color()),
		Divider:   lipgloss.NewStyle().Foreground(p.Dim.Color()),
		Viewport:  lipgloss.NewStyle(),

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
		Search:   newSearchStyles(p),
		Hint:     newHintStyles(p),
		Pane:     newPaneStyles(p),
	}
}
