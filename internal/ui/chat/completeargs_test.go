package chat

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/storage"
)

// completionNames is the menu's rows, in order.
func completionNames(m Model) []string {
	names := make([]string, len(m.completions))
	for i, c := range m.completions {
		names[i] = c.name
	}
	return names
}

// withTestCommand installs an extra registry row for one test.
func withTestCommand(t *testing.T, c slashCommand) {
	t.Helper()
	old := slashCommands
	slashCommands = append(append([]slashCommand{}, old...), c)
	t.Cleanup(func() { slashCommands = old })
}

func TestArgCompletion_StaticSubcommands(t *testing.T) {
	m := typeChars(t, readyModel(t), "/ui ")
	if !m.completionActive() {
		t.Fatal("the menu should stay open past the command name")
	}
	if got := completionNames(m); len(got) != 2 || got[0] != "verbosity" || got[1] != "mono" {
		t.Fatalf("expected the verbosity and mono subcommands, got %v", got)
	}

	m = typeChars(t, m, "verbosity l")
	if got := completionNames(m); len(got) != 1 || got[0] != "low" {
		t.Fatalf("expected low for the second position, got %v", got)
	}
}

func TestArgCompletion_PositionGatedOnPreviousToken(t *testing.T) {
	// The low|med|high position only applies after "verbosity".
	m := typeChars(t, readyModel(t), "/ui density l")
	if m.completionActive() {
		t.Fatal("the value position should be gated on the verbosity token")
	}
}

func TestArgCompletion_TabCompletesCurrentTokenOnly(t *testing.T) {
	m := typeChars(t, readyModel(t), "/ui verbosity h")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(Model)
	if m.input.Value() != "/ui verbosity high" {
		t.Fatalf("tab should complete the argument token, got %q", m.input.Value())
	}
}

func TestArgCompletion_TabKeepsTextAfterTheCursor(t *testing.T) {
	m := readyModel(t)
	m.input.SetValue("/ui verb low")
	m.input.SetCursor(len("/ui verb"))
	m.syncCompletions()
	if !m.completionActive() {
		t.Fatalf("the token under the cursor should open the menu, input %q", m.input.Value())
	}
	if got := completionNames(m); len(got) != 1 || got[0] != "verbosity" {
		t.Fatalf("expected the token under the cursor to filter, got %v", got)
	}

	m.acceptCompletion()
	if m.input.Value() != "/ui verbosity low" {
		t.Fatalf("tab should replace only the token under the cursor, got %q", m.input.Value())
	}
}

func TestArgCompletion_NoTrailingSpaceOnLastPosition(t *testing.T) {
	m := typeChars(t, readyModel(t), "/plan sa")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(Model)
	if m.input.Value() != "/plan save" {
		t.Fatalf("a final argument should not gain a trailing space, got %q", m.input.Value())
	}
}

func TestArgCompletion_EnterRunsCompletedLine(t *testing.T) {
	m := typeChars(t, readyModel(t), "/ui verbosity l")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.input.Value() != "" {
		t.Fatalf("enter should consume the input, got %q", m.input.Value())
	}
	if m.verbosity != verbosityLow {
		t.Fatalf("enter on the low row should apply it, verbosity is %s", m.verbosity)
	}
	last := m.transcript[len(m.transcript)-1]
	if !strings.Contains(last.text, "low") {
		t.Fatalf("expected the /ui reply in the transcript, got %q", last.text)
	}
}

func TestArgCompletion_UnavailableCommandOffersNothing(t *testing.T) {
	// No memory manager wired: /memory is not in this session's registry, so
	// its subcommands are not offered either.
	m := typeChars(t, readyModel(t), "/memory li")
	if m.completionActive() {
		t.Fatal("an unavailable command should not complete its arguments")
	}
}

func TestArgCompletion_ModeCycle(t *testing.T) {
	m := readyModel(t)
	m.modeCycle = []agent.Mode{agent.ModeManual, agent.ModeAcceptEdits, agent.ModeAuto}
	m = typeChars(t, m, "/mode a")

	got := completionNames(m)
	if len(got) != 2 || got[0] != "accept-edits" || got[1] != "auto" {
		t.Fatalf("expected the a-prefixed modes, got %v", got)
	}

	m = typeChars(t, readyModel(t), "/mode wh")
	if got := completionNames(m); len(got) != 1 || got[0] != "why" {
		t.Fatalf("/mode why should complete, got %v", got)
	}
}

func TestArgCompletion_CheckpointNumbers(t *testing.T) {
	m := readyModel(t)
	m.checkpoints = []checkpoint{{index: 1, preview: "first question"}, {index: 3, preview: "second question"}}
	m = typeChars(t, m, "/rewind ")

	got := completionNames(m)
	if len(got) != 2 || got[0] != "2" || got[1] != "1" {
		t.Fatalf("expected checkpoint numbers latest first, got %v", got)
	}
	if m.completions[0].desc != "second question" {
		t.Fatalf("expected the turn preview as the description, got %q", m.completions[0].desc)
	}
}

func TestArgCompletion_SavedChatNames(t *testing.T) {
	db, err := storage.OpenPath(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	msgs := []provider.Message{{Role: provider.RoleUser, Content: "hi"}}
	for _, name := range []string{"alpha-notes", "beta-notes"} {
		if err := db.SaveChat(name, msgs); err != nil {
			t.Fatal(err)
		}
	}

	m := readyModel(t).WithDB(db)
	m = typeChars(t, m, "/load al")
	if got := completionNames(m); len(got) != 1 || got[0] != "alpha-notes" {
		t.Fatalf("expected the prefix-matching chat, got %v", got)
	}

	// Long dynamic lists also match as a subsequence (S-079).
	m = typeChars(t, readyModel(t).WithDB(db), "/load btn")
	if got := completionNames(m); len(got) != 1 || got[0] != "beta-notes" {
		t.Fatalf("expected the fuzzy match, got %v", got)
	}
}

func TestArgCompletion_BranchNames(t *testing.T) {
	db, err := storage.OpenPath(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.SaveChat("main", []provider.Message{{Role: provider.RoleUser, Content: "hi"}}); err != nil {
		t.Fatal(err)
	}

	m := readyModel(t).WithDB(db)
	m.sessionName = "main"
	m = typeChars(t, m, "/branches ma")
	if got := completionNames(m); len(got) != 1 || got[0] != "main" {
		t.Fatalf("expected the branch family, got %v", got)
	}
	if !strings.HasPrefix(m.completions[0].desc, "current") {
		t.Fatalf("the current branch should say so, got %q", m.completions[0].desc)
	}
}

func TestArgCompletion_ModelCatalogFuzzy(t *testing.T) {
	m := readyModel(t).WithModelOptions([]string{"claude-opus-5", "claude-sonnet-5"})
	m.modelName = "claude-opus-5"
	m = typeChars(t, m, "/model son")

	if got := completionNames(m); len(got) != 1 || got[0] != "claude-sonnet-5" {
		t.Fatalf("expected the fuzzy model match, got %v", got)
	}
}

func TestArgCompletion_CommandNamesStayPrefixOnly(t *testing.T) {
	// Fuzzy matching is for argument values; command names keep prefix
	// matching so "/mdl" stays a typo rather than silently meaning /model.
	m := typeChars(t, readyModel(t), "/mdl")
	if m.completionActive() {
		t.Fatal("command names should not match as a subsequence")
	}
}

func TestArgCompletion_DynamicSourceReadOncePerMenu(t *testing.T) {
	calls := 0
	withTestCommand(t, slashCommand{
		name: "/zztest", args: "[x]", desc: "test command",
		argSpecs: []argSpec{{dynamic: func(*Model) []argOption {
			calls++
			return []argOption{{value: "alpha"}, {value: "alberta"}, {value: "beta"}}
		}}},
	})

	m := typeChars(t, readyModel(t), "/zztest al")
	if got := completionNames(m); len(got) != 2 {
		t.Fatalf("expected both al-prefixed values, got %v", got)
	}
	if calls != 1 {
		t.Fatalf("the dynamic source should be read once per menu, got %d reads", calls)
	}

	// Arrowing and typing reuse the cache; closing the menu drops it.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	m = typeChars(t, m, "p")
	if calls != 1 {
		t.Fatalf("keystrokes should not re-read the source, got %d reads", calls)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	m = typeChars(t, m, "h")
	if !m.completionActive() {
		t.Fatal("typing after dismissal should re-open the argument menu")
	}
	if calls != 2 {
		t.Fatalf("a new menu should re-read the source, got %d reads", calls)
	}
}

func TestArgCompletion_MenuInView(t *testing.T) {
	m := typeChars(t, readyModel(t), "/ui verbosity ")
	view := m.View()
	if !strings.Contains(view, "low") || !strings.Contains(view, "tab complete") {
		t.Fatal("the view should render the argument menu and its hint line")
	}
}
