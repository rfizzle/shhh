package chat

// /ui mono (S-095): the session-level half of the mono invariant — the switch
// itself, what it reports, and that the chat TUI's own derived styles follow
// the shared palette instead of holding stale colours.

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// monoRestore puts the palette back after a test flips it.
func monoRestore(t *testing.T) {
	t.Helper()
	was := components.Mono()
	t.Cleanup(func() { components.SetMono(was) })
}

func TestUICommand_MonoTogglesThePalette(t *testing.T) {
	monoRestore(t)
	m := readyModel(t)

	handled, result := m.handleSlashCommand("/ui mono on")
	if !handled {
		t.Fatal("/ui mono should be handled")
	}
	if !components.Mono() {
		t.Fatal("mono should be on")
	}
	if !strings.Contains(result, "two greys") {
		t.Fatalf("the reply should say what changed, got %q", result)
	}

	if _, result = m.handleSlashCommand("/ui mono"); !strings.Contains(result, "Monochrome: on") {
		t.Fatalf("bare /ui mono should report the state, got %q", result)
	}
	if _, result = m.handleSlashCommand("/ui"); !strings.Contains(result, "Monochrome: on") {
		t.Fatalf("bare /ui should report mono alongside verbosity, got %q", result)
	}

	if _, result = m.handleSlashCommand("/ui mono off"); components.Mono() {
		t.Fatalf("mono should be off again, reply %q", result)
	}
	if _, result = m.handleSlashCommand("/ui mono grey"); !strings.Contains(result, "unknown mono setting") {
		t.Fatalf("an unknown setting should be an error, got %q", result)
	}
}

func TestUICommand_VerbositySurvivesTheMonoArgument(t *testing.T) {
	m := readyModel(t)
	if _, result := m.handleSlashCommand("/ui verbosity high"); !strings.Contains(result, "set to high") {
		t.Fatalf("verbosity should still be settable, got %q", result)
	}
	if _, result := m.handleSlashCommand("/ui verbosity"); !strings.Contains(result, "verbosity: high") {
		t.Fatalf("bare /ui verbosity should report the level, got %q", result)
	}
	if _, result := m.handleSlashCommand("/ui density low"); !strings.Contains(result, "Usage:") {
		t.Fatalf("an unknown subcommand should show usage, got %q", result)
	}
}

// TestMonoReachesTheChatStyles is the reason the styles are rebuilt rather
// than initialized in place: a package-level style captured at init would
// keep its colour forever, and every surface in this package would ignore the
// swap.
func TestMonoReachesTheChatStyles(t *testing.T) {
	// A profile with colour to give: the one detected from a test binary's
	// non-terminal stdout resolves every token to no colour at all, which
	// would make the two palettes indistinguishable (S-155).
	was := components.Profile()
	components.SetProfile(colorprofile.ANSI256)
	t.Cleanup(func() { components.SetProfile(was) })
	monoRestore(t)
	components.SetMono(false)
	full := []lipgloss.Style{sty.Assistant, sty.Error, sty.Step.Done, sty.Frame.AccentGated, sty.Complete.Args, sty.Pane.Divider, sty.Hint.Key, sty.Reading.Label}

	components.SetMono(true)
	got := []lipgloss.Style{sty.Assistant, sty.Error, sty.Step.Done, sty.Frame.AccentGated, sty.Complete.Args, sty.Pane.Divider, sty.Hint.Key, sty.Reading.Label}
	for i := range got {
		if got[i].GetForeground() == full[i].GetForeground() {
			t.Errorf("style %d kept its full-palette foreground %v after the mono swap", i, full[i].GetForeground())
		}
	}
	for _, s := range got {
		fg := s.GetForeground()
		if fg != components.MonoFg.Color() && fg != components.MonoDim.Color() {
			t.Errorf("mono style has foreground %v, which is not one of the two greys", fg)
		}
	}
}

// TestMonoDeclinesSyntaxAndMarkdownColour covers the two colour sources this
// package owns that do not come from the palette.
func TestMonoDeclinesSyntaxAndMarkdownColour(t *testing.T) {
	monoRestore(t)
	components.SetMono(false)
	if diffSyntax("main.go") == nil {
		t.Fatal("a Go file should have a highlighter with the full palette")
	}
	if markdownStyle() != "dark" {
		t.Fatalf("markdown should use the dark theme, got %q", markdownStyle())
	}

	components.SetMono(true)
	if diffSyntax("main.go") != nil {
		t.Fatal("mono should decline syntax highlighting outright")
	}
	if markdownStyle() != "ascii" {
		t.Fatalf("mono markdown should use the ascii theme, got %q", markdownStyle())
	}
	// The ascii theme is the invariant applied to prose: emphasis survives as
	// characters once the colour is gone.
	if out := renderMarkdown("a **bold** word", 40); !strings.Contains(out, "**bold**") {
		t.Fatalf("mono markdown should mark emphasis with characters, got %q", out)
	}
}

func TestArgCompletion_MonoValuesAreGatedOnTheMonoToken(t *testing.T) {
	m := typeChars(t, readyModel(t), "/ui mono o")
	got := completionNames(m)
	if len(got) != 2 || got[0] != "on" || got[1] != "off" {
		t.Fatalf("the mono position should offer on and off, got %v", got)
	}

	// The same position after "verbosity" is still the level list, so the two
	// alternatives do not bleed into each other.
	m = typeChars(t, readyModel(t), "/ui verbosity l")
	if got := completionNames(m); len(got) != 1 || got[0] != "low" {
		t.Fatalf("the verbosity position should be unchanged, got %v", got)
	}
}

// The document margin is layout, not decoration, so it has to survive the
// mono swap — invariant 1 runs in that direction too.
//
// It did not, until S-155. renderMarkdown finished a document with a
// TrimSpace, and glamour v1 opened each line with its style escape before the
// margin, so the trim stopped at the escape and the margin survived — in
// colour. In mono there was no escape in front of it, so the first line of
// every assistant paragraph sat two columns left of the rest of itself.
func TestMonoKeepsTheDocumentMargin(t *testing.T) {
	monoRestore(t)
	const prose = "Round exhaustion is fatal in Agent.runRound: the loop returns " +
		"ErrRoundLimit, and the chat model treats any error from a round as terminal.\n"
	for _, on := range []bool{false, true} {
		components.SetMono(on)
		rows := strings.Split(ansi.Strip(renderMarkdown(prose, 60)), "\n")
		if len(rows) < 2 {
			t.Fatalf("mono=%v: the fixture must wrap, got %d row(s)", on, len(rows))
		}
		first := len(rows[0]) - len(strings.TrimLeft(rows[0], " "))
		rest := len(rows[1]) - len(strings.TrimLeft(rows[1], " "))
		if first != rest {
			t.Errorf("mono=%v: the opening line is indented %d and the rest %d",
				on, first, rest)
		}
	}
}
