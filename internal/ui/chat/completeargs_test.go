package chat

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/changeset"
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
	if got := strings.Join(completionNames(m), " "); got != "verbosity mono mouse notify title window rail terminal" {
		t.Fatalf("expected every /ui subcommand in registry order, got %q", got)
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

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = updated.(Model)
	if m.input.Value() != "/ui verbosity high" {
		t.Fatalf("tab should complete the argument token, got %q", m.input.Value())
	}
}

func TestArgCompletion_TabKeepsTextAfterTheCursor(t *testing.T) {
	m := readyModel(t)
	m.input.SetValue("/ui verb low")
	m.input.SetCursorColumn(len("/ui verb"))
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

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = updated.(Model)
	if m.input.Value() != "/plan save" {
		t.Fatalf("a final argument should not gain a trailing space, got %q", m.input.Value())
	}
}

func TestArgCompletion_EnterRunsCompletedLine(t *testing.T) {
	m := typeChars(t, readyModel(t), "/ui verbosity l")

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
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
	m = typeChars(t, m, "/permissions a")

	// The a-prefixed modes, and the a-prefixed subcommand beside them: the
	// position offers everything /permissions takes there.
	got := completionNames(m)
	if len(got) != 3 || got[0] != "accept-edits" || got[1] != "auto" || got[2] != "allow" {
		t.Fatalf("expected the a-prefixed modes and allow, got %v", got)
	}

	m = typeChars(t, readyModel(t), "/permissions wh")
	if got := completionNames(m); len(got) != 1 || got[0] != "why" {
		t.Fatalf("/permissions why should complete, got %v", got)
	}

	// The grants the key hands out are reachable from the menu, both halves.
	m = typeChars(t, readyModel(t), "/permissions revoke ")
	if got := completionNames(m); len(got) != 2 || got[0] != "edits" || got[1] != "commands" {
		t.Fatalf("/permissions revoke should offer its scopes, got %v", got)
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

	// Long dynamic lists also match as a subsequence.
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
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = updated.(Model)
	m = typeChars(t, m, "p")
	if calls != 1 {
		t.Fatalf("keystrokes should not re-read the source, got %d reads", calls)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
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
	view := m.View().Content
	if !strings.Contains(view, "low") || !strings.Contains(view, "tab complete") {
		t.Fatal("the view should render the argument menu and its hint line")
	}
}

// Tab-completing a command name leaves the whole argument catalog under the
// cursor, and enter there means the line — bare /model is the picker. The
// menu only claims enter once it is a choice: a typed prefix or an arrowed-to
// row.
func TestArgCompletion_EnterOnAnUnfilteredMenuRunsTheLine(t *testing.T) {
	m := readyModel(t).
		WithModelSwitcher(func(string) {}).
		WithPricing(nil, "m1").
		WithModelOptions([]string{"m1", "m2", "m3"})
	m = typeChars(t, m, "/mo")

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = updated.(Model)
	if m.input.Value() != "/model " {
		t.Fatalf("tab should complete the command name, got %q", m.input.Value())
	}
	if !m.completionActive() || !m.completeArg {
		t.Fatal("the argument menu should be open over the catalog")
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if m.state != statePick || m.picker == nil {
		t.Fatalf("enter on the unfiltered menu should open the picker, state is %v", m.state)
	}
	if m.modelName != "m1" {
		t.Fatalf("enter should not have taken a row, model is %q", m.modelName)
	}
}

func TestArgCompletion_ArrowMakesTheMenuTheChoiceAgain(t *testing.T) {
	var switched string
	m := readyModel(t).
		WithModelSwitcher(func(name string) { switched = name }).
		WithPricing(nil, "m1").
		WithModelOptions([]string{"m1", "m2", "m3"})
	m = typeChars(t, m, "/model ")

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)

	if m.picker != nil {
		t.Fatal("a row that was arrowed onto is a choice, not the bare command")
	}
	if switched != "m2" {
		t.Fatalf("enter should have taken the arrowed-to row, switched to %q", switched)
	}
}

func TestArgCompletion_TypedPrefixStillRunsTheRow(t *testing.T) {
	m := typeChars(t, readyModel(t), "/ui verbosity l")

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if m.verbosity != verbosityLow {
		t.Fatalf("a filtered menu keeps enter, verbosity is %s", m.verbosity)
	}
}

// Backspacing out of a typed prefix hands enter back to the line: the menu is
// showing the whole catalog again, so it is a list again.
func TestArgCompletion_BackspaceToAnEmptyTokenGivesEnterBack(t *testing.T) {
	m := readyModel(t).
		WithModelSwitcher(func(string) {}).
		WithPricing(nil, "m1").
		WithModelOptions([]string{"m1", "m2", "m3"})
	m = typeChars(t, m, "/model m")

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)

	if m.state != statePick {
		t.Fatalf("enter should open the picker again, state is %v", m.state)
	}
}

// The hint says which line enter runs, so the reader is never guessing which
// of the two readings the menu is offering.
func TestArgCompletion_HintNamesTheLineEnterRuns(t *testing.T) {
	m := readyModel(t).
		WithModelSwitcher(func(string) {}).
		WithPricing(nil, "m1").
		WithModelOptions([]string{"m1", "m2", "m3"})
	m = typeChars(t, m, "/model ")

	menu := strings.Join(m.completionMenuLines(), "\n")
	if !strings.Contains(menu, "enter run /model") {
		t.Fatalf("the hint should name the line enter runs:\n%s", menu)
	}
}

// TestArgCompletion_RailOffersWhatThisTerminalAllows: the useful third offer
// is the ladder's own width here, so the menu never proposes a number the
// layout is about to narrow. On a terminal too narrow to split there is no
// such number, and the menu is the word and the floor.
func TestArgCompletion_RailOffersWhatThisTerminalAllows(t *testing.T) {
	narrow := typeChars(t, readyModel(t), "/ui rail ")
	if got := strings.Join(completionNames(narrow), " "); got != "auto 46" {
		t.Errorf("a narrow terminal offers the word and the floor, got %q", got)
	}

	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 40})
	wide := typeChars(t, updated.(Model), "/ui rail ")
	if got := strings.Join(completionNames(wide), " "); got != "auto 62 46" {
		t.Errorf("a 200-column terminal offers its own 62 between them, got %q", got)
	}
}

// /diff's argument is a path, so the menu offers the files this session has
// changed — the rail's CHANGES block as a list, in the order the rail draws
// it — and matches on any part of a path, because the part that tells two
// files apart is at the end of it.
func TestArgCompletion_SessionFiles(t *testing.T) {
	m := readyModel(t)
	m.changes.Add(1, changeset.Record{Path: "internal/agent/loop.go",
		Before: "a\n", After: "a\nb\n", BeforeExists: true, AfterExists: true})
	m.changes.Add(2, changeset.Record{Path: "internal/ui/chat/model.go",
		Before: "x\n", After: "y\n", BeforeExists: true, AfterExists: true})

	menu := typeChars(t, m, "/diff ")
	got := completionNames(menu)
	if len(got) != 2 || got[0] != "internal/agent/loop.go" || got[1] != "internal/ui/chat/model.go" {
		t.Fatalf("expected the session's files in the rail's order, got %v", got)
	}
	if desc := menu.completions[0].desc; !strings.Contains(desc, "+1 −0") {
		t.Fatalf("expected what the file cost as the description, got %q", desc)
	}

	menu = typeChars(t, m, "/diff model.go")
	if got := completionNames(menu); len(got) != 1 || got[0] != "internal/ui/chat/model.go" {
		t.Fatalf("expected a path to match on its own name, got %v", got)
	}
}

// A file the session made executable and left otherwise alone is one of the
// files /diff can be given, and its description says the change it has rather
// than the lines it did not move.
func TestArgCompletion_SessionFilesOfferAModeOnlyChange(t *testing.T) {
	m := readyModel(t)
	m.changes.Add(1, changeset.Record{Path: "scripts/build.sh",
		Before: "a\n", After: "a\n", BeforeExists: true, AfterExists: true,
		BeforeMode: 0o644, AfterMode: 0o755})

	menu := typeChars(t, m, "/diff ")
	got := completionNames(menu)
	if len(got) != 1 || got[0] != "scripts/build.sh" {
		t.Fatalf("expected the file the session chmod'd, got %v", got)
	}
	if desc := menu.completions[0].desc; !strings.Contains(desc, "mode 0644 → 0755") {
		t.Fatalf("expected the mode as the description, got %q", desc)
	}
	if desc := menu.completions[0].desc; strings.Contains(desc, "+0 −0") {
		t.Fatalf("nothing counted this change, got %q", desc)
	}
}
