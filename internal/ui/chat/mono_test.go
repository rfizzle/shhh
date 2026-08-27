package chat

// /ui mono (S-095): the session-level half of the mono invariant — the switch
// itself, what it reports, and that the chat TUI's own derived styles follow
// the shared palette instead of holding stale colours.

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
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
	monoRestore(t)
	components.SetMono(false)
	full := []lipgloss.Style{assistantStyle, errorStyle, stepDoneStyle, frameAccentGated, completeArgsStyle, paneDividerStyle, hintKeyStyle, readingLabelStyle}

	components.SetMono(true)
	got := []lipgloss.Style{assistantStyle, errorStyle, stepDoneStyle, frameAccentGated, completeArgsStyle, paneDividerStyle, hintKeyStyle, readingLabelStyle}
	for i := range got {
		if got[i].GetForeground() == full[i].GetForeground() {
			t.Errorf("style %d kept its full-palette foreground %v after the mono swap", i, full[i].GetForeground())
		}
	}
	for _, s := range got {
		fg := s.GetForeground()
		if fg != components.MonoFg && fg != components.MonoDim {
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
