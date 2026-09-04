package chat

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/provider"
)

func readyModel(t *testing.T) Model {
	t.Helper()
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	return updated.(Model)
}

func typeChars(t *testing.T, m Model, s string) Model {
	t.Helper()
	for _, r := range s {
		updated, _ := m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = updated.(Model)
	}
	return m
}

func TestCompletion_OpensOnSlashPrefix(t *testing.T) {
	m := typeChars(t, readyModel(t), "/mo")

	if !m.completionActive() {
		t.Fatal("typing /mo should open the completion menu")
	}
	names := make([]string, len(m.complete.items))
	for i, c := range m.complete.items {
		names[i] = c.name
	}
	joined := strings.Join(names, " ")
	if !strings.Contains(joined, "/model") || !strings.Contains(joined, "/mode") {
		t.Fatalf("expected /model and /mode candidates, got %v", names)
	}
}

func TestCompletion_NoMenuForPlainText(t *testing.T) {
	m := typeChars(t, readyModel(t), "hello")
	if m.completionActive() {
		t.Fatal("plain text should not open the completion menu")
	}
}

func TestCompletion_NoMenuForUnknownCommand(t *testing.T) {
	m := typeChars(t, readyModel(t), "/zzz")
	if m.completionActive() {
		t.Fatal("an unknown prefix should not keep the menu open")
	}
}

func TestCompletion_HiddenForFreeFormArgument(t *testing.T) {
	// /plan's second position is a free-form file name: no spec, no menu.
	m := typeChars(t, readyModel(t), "/plan save nam")
	if m.completionActive() {
		t.Fatal("a free-form argument should not open the menu")
	}
}

func TestCompletion_ExactMatchRanksFirst(t *testing.T) {
	m := typeChars(t, readyModel(t), "/permissions")
	if !m.completionActive() {
		t.Fatal("typing /permissions should keep the menu open")
	}
	if got := m.complete.items[m.complete.idx].name; got != "/permissions" {
		t.Fatalf("exact match /permissions should be focused, got %s", got)
	}
}

// /mode was this command's name until it sat one letter from /model on the
// same menu. It still answers, and typing it in full still names a command
// exactly — the menu just shows what the command is called now.
func TestCompletion_TheOldNameStillNamesTheCommand(t *testing.T) {
	m := typeChars(t, readyModel(t), "/mode")
	if !m.completionActive() {
		t.Fatal("typing the old name should keep the menu open")
	}
	if got := m.complete.items[m.complete.idx].name; got != "/permissions" {
		t.Fatalf("/mode should focus the command it renamed to, got %s", got)
	}
}

func TestCompletion_ArrowsMoveFocus(t *testing.T) {
	m := typeChars(t, readyModel(t), "/mo")
	first := m.complete.items[0].name

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = updated.(Model)
	if m.complete.idx != 1 {
		t.Fatalf("down should move focus to 1, got %d", m.complete.idx)
	}
	if m.input.Value() != "/mo" {
		t.Fatalf("moving focus should not touch the input, got %q", m.input.Value())
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	m = updated.(Model)
	if m.complete.idx != 0 || m.complete.items[0].name != first {
		t.Fatalf("up should move focus back to 0, got %d", m.complete.idx)
	}
}

func TestCompletion_TabCompletes(t *testing.T) {
	m := typeChars(t, readyModel(t), "/comp")

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = updated.(Model)
	if m.input.Value() != "/compact" {
		t.Fatalf("tab should complete to /compact, got %q", m.input.Value())
	}
}

func TestCompletion_TabAddsSpaceForArgCommands(t *testing.T) {
	m := typeChars(t, readyModel(t), "/rewi")

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = updated.(Model)
	if m.input.Value() != "/rewind " {
		t.Fatalf("tab should complete to %q, got %q", "/rewind ", m.input.Value())
	}
	if m.completionActive() {
		t.Fatal("the menu should hide after completing into the argument position")
	}
}

func TestCompletion_EnterRunsHighlighted(t *testing.T) {
	m := typeChars(t, readyModel(t), "/cle")

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if m.input.Value() != "" {
		t.Fatalf("enter should consume the input, got %q", m.input.Value())
	}
	last := m.transcript[len(m.transcript)-1]
	if !strings.Contains(last.text, "new session") {
		t.Fatalf("enter on /clear should run it, transcript: %q", last.text)
	}
}

func TestCompletion_EscDismissesKeepsDraft(t *testing.T) {
	m := typeChars(t, readyModel(t), "/mo")

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if m.completionActive() {
		t.Fatal("esc should dismiss the menu")
	}
	if m.input.Value() != "/mo" {
		t.Fatalf("esc with the menu open should keep the draft, got %q", m.input.Value())
	}

	// Typing again re-opens the menu.
	m = typeChars(t, m, "d")
	if !m.completionActive() {
		t.Fatal("typing after dismissal should re-open the menu")
	}
}

func TestCompletion_HidesUnavailableCommands(t *testing.T) {
	// No DB wired: /save must not be offered; /sandbox still matches /sa.
	m := typeChars(t, readyModel(t), "/sa")
	if !m.completionActive() {
		t.Fatal("typing /sa should open the menu")
	}
	for _, c := range m.complete.items {
		if c.name == "/save" {
			t.Fatal("/save should be hidden without chat persistence")
		}
	}
}

func TestCompletion_AliasMatches(t *testing.T) {
	m := typeChars(t, readyModel(t), "/q")
	if !m.completionActive() {
		t.Fatal("typing /q should match the /exit aliases")
	}
	if m.complete.items[m.complete.idx].name != "/exit" {
		t.Fatalf("alias /q should surface /exit, got %s", m.complete.items[m.complete.idx].name)
	}
}

func TestCompletion_MenuInView(t *testing.T) {
	m := typeChars(t, readyModel(t), "/mo")
	view := m.View().Content
	if !strings.Contains(view, "/model") || !strings.Contains(view, "tab complete") {
		t.Fatal("the view should render the completion menu and its hint line")
	}
}

func TestCompletion_ShrinksViewport(t *testing.T) {
	m := readyModel(t)
	base := m.viewport.Height()
	m = typeChars(t, m, "/mo")
	if m.viewport.Height() >= base {
		t.Fatalf("the open menu should shrink the viewport (%d -> %d)", base, m.viewport.Height())
	}
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if m.viewport.Height() != base {
		t.Fatalf("dismissing the menu should restore the viewport (%d != %d)", m.viewport.Height(), base)
	}
}
