package chat

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/storage"
)

// paletteModel is a ready session whose FILES group comes from a fixed list
// rather than from whatever directory the suite runs in.
func paletteModel(t *testing.T) Model {
	t.Helper()
	m := readyModel(t)
	m.recentFiles = func() []project.RecentFile {
		return []project.RecentFile{
			{Path: "internal/agent/loop.go", Mod: time.Now().Add(-4 * time.Minute)},
			{Path: "README.md", Mod: time.Now().Add(-2 * time.Hour)},
		}
	}
	return m
}

// openPaletteWith opens the palette and types query into it.
func openPaletteWith(t *testing.T, m Model, query string) Model {
	t.Helper()
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	m = updated.(Model)
	if m.palette == nil || m.state != statePick {
		t.Fatal("ctrl+p should open the palette on the picker surface")
	}
	return typeChars(t, m, query)
}

// paletteLabels is what the palette is showing, rails included.
func paletteLabels(m Model) []string {
	out := make([]string, len(m.palette.rows))
	for i, r := range m.palette.rows {
		out[i] = r.label
	}
	return out
}

func TestPalette_CtrlKOpensGroupedResults(t *testing.T) {
	m := openPaletteWith(t, paletteModel(t), "")

	labels := strings.Join(paletteLabels(m), "\n")
	if !strings.Contains(labels, "COMMANDS") {
		t.Fatalf("the palette should rail its groups, got:\n%s", labels)
	}
	if !strings.Contains(labels, "FILES") {
		t.Fatalf("the recent files should be offered, got:\n%s", labels)
	}
	if !m.picker.Filtering {
		t.Fatal("the palette is the shared filter row always open, so the card should be filtering")
	}
	if !strings.Contains(m.picker.View(70), "▸ █") {
		t.Fatalf("the palette should carry its query line:\n%s", m.picker.View(70))
	}
	if got := strings.Join(m.picker.Chips, ""); !strings.Contains(got, "results") {
		t.Fatalf("the title rail should count the results, got %q", got)
	}
	if m.palette.rows[m.picker.Focus].header {
		t.Fatal("the pointer should open on a row a key can land on, not on a rail")
	}
}

func TestPalette_SessionsAndFilesAreSearchedToo(t *testing.T) {
	m := paletteModel(t)
	store := changeset.New(1 << 20)
	store.Add(4, changeset.Record{
		Path: "internal/ui/chat/palette.go", After: "package chat\n", AfterExists: true,
	})
	m = m.WithChangeset(store, nil)

	m = openPaletteWith(t, m, "palette")
	labels := paletteLabels(m)
	joined := strings.Join(labels, "\n")
	if !strings.Contains(joined, "internal/ui/chat/palette.go") {
		t.Fatalf("a file this session changed should match, got:\n%s", joined)
	}
	for _, r := range m.palette.rows {
		if r.header {
			continue
		}
		if r.group == paletteFiles && r.desc != "" && !strings.Contains(r.desc, "changed this session") {
			t.Fatalf("a changed file should say what it did, got %q", r.desc)
		}
	}
}

func TestPalette_ExactCommandNameRanksFirst(t *testing.T) {
	m := openPaletteWith(t, paletteModel(t), "permissions")

	first, ok := m.paletteFocus()
	if !ok {
		t.Fatal("a query that matches should focus something")
	}
	if !strings.HasPrefix(first.label, "/permissions") {
		t.Fatalf("an exact command name should rank first, got %q", first.label)
	}
}

func TestPalette_FuzzyMatchesAcrossGroups(t *testing.T) {
	m := paletteModel(t)
	m.recentFiles = func() []project.RecentFile {
		return []project.RecentFile{{Path: "internal/agent/loop.go", Mod: time.Now()}}
	}
	// Not a prefix of anything: only a subsequence finds it.
	m = openPaletteWith(t, m, "aglp")

	joined := strings.Join(paletteLabels(m), "\n")
	if !strings.Contains(joined, "internal/agent/loop.go") {
		t.Fatalf("a subsequence should still find the path, got:\n%s", joined)
	}
}

func TestPalette_AliasFindsItsCommand(t *testing.T) {
	m := openPaletteWith(t, paletteModel(t), "quit")

	first, ok := m.paletteFocus()
	if !ok || !strings.HasPrefix(first.label, "/exit") {
		t.Fatalf("an alias should surface its primary command, got %q", first.label)
	}
	// The binding is the row's meta field, right-aligned by the
	// card rather than padded into the label by this package.
	if first.meta != "ctrl+d" {
		t.Fatalf("a command with a key binding should show it, got %q", first.meta)
	}
}

func TestPalette_EnterRunsTheFocusedCommand(t *testing.T) {
	m := openPaletteWith(t, paletteModel(t), "stats")

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)

	if m.palette != nil || m.picker != nil || m.state != stateInput {
		t.Fatal("running from the palette should dismiss it")
	}
	last := m.transcript[len(m.transcript)-1]
	if !strings.Contains(last.text, "Context") && !strings.Contains(last.text, "tokens") {
		t.Fatalf("/stats should have run, got %q", last.text)
	}
}

func TestPalette_TabWritesIntoTheInput(t *testing.T) {
	m := openPaletteWith(t, paletteModel(t), "model")

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = updated.(Model)

	if m.palette != nil {
		t.Fatal("tab should dismiss the palette")
	}
	if got := m.input.Value(); got != "/model " {
		t.Fatalf("tab should complete the command into the input, got %q", got)
	}
}

func TestPalette_EnterOnAFileWritesItsPath(t *testing.T) {
	m := paletteModel(t)
	m.input.SetValue("explain")
	m = openPaletteWith(t, m, "loop.go")

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)

	if got := m.input.Value(); got != "explain internal/agent/loop.go " {
		t.Fatalf("a file should join the draft, got %q", got)
	}
}

func TestPalette_EscDismissesAndKeepsTheDraft(t *testing.T) {
	m := paletteModel(t)
	m.input.SetValue("half a sentence")
	m = openPaletteWith(t, m, "mo")

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)

	if m.palette != nil || m.state != stateInput {
		t.Fatal("esc should dismiss the palette")
	}
	if m.input.Value() != "half a sentence" {
		t.Fatalf("the draft should survive, got %q", m.input.Value())
	}
}

func TestPalette_BackspaceWidensTheQuery(t *testing.T) {
	m := openPaletteWith(t, paletteModel(t), "modx")
	if len(m.palette.rows) != 0 {
		t.Fatalf("a query that matches nothing should show nothing, got %v", paletteLabels(m))
	}
	if got := strings.Join(m.picker.Chips, ""); got != "no matches" {
		t.Fatalf("the rail should say so, got %q", got)
	}

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	m = updated.(Model)
	if len(m.palette.rows) == 0 {
		t.Fatal("backspace should widen the query again")
	}
}

func TestPalette_DigitsAndJKAreQueryText(t *testing.T) {
	m := openPaletteWith(t, paletteModel(t), "j")
	if m.palette.query != "j" {
		t.Fatalf("j should type, not move, got query %q", m.palette.query)
	}
	m = typeChars(t, m, "2")
	if m.palette.query != "j2" {
		t.Fatalf("a digit should type, not jump, got query %q", m.palette.query)
	}
	if m.state != statePick {
		t.Fatal("neither key should have chosen anything")
	}
}

func TestPalette_IdleOnlyCommandsDimRatherThanDrop(t *testing.T) {
	m := paletteModel(t)
	m.setTurnState(stateStreaming)
	m = openPaletteWith(t, m, "clear")

	row, ok := m.paletteFocus()
	if !ok || !strings.HasPrefix(row.label, "/clear") {
		t.Fatalf("an idle-only command should still be offered while the agent works, got %q", row.label)
	}
	if row.dim == "" {
		t.Fatal("it should be dimmed rather than offered as runnable")
	}
	if !strings.Contains(row.desc, "starts a new conversation") {
		t.Fatalf("the reason should be stated, got %q", row.desc)
	}

	// Choosing it answers with the notice rather than doing it.
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	last := m.transcript[len(m.transcript)-1]
	if !strings.Contains(last.text, "needs the turn to be finished") {
		t.Fatalf("expected the idle-only notice, got %q", last.text)
	}
}

func TestPalette_OpensMidTurn(t *testing.T) {
	m := paletteModel(t)
	m.setTurnState(stateStreaming)

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	m = updated.(Model)

	if m.palette == nil || m.state != statePick {
		t.Fatal("the palette should open while the agent works")
	}
	if m.turnState() != stateStreaming {
		t.Fatal("the turn should keep running underneath")
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if m.state != stateStreaming {
		t.Fatalf("dismissing should hand the screen back to the turn, got state %v", m.state)
	}
}

func TestPalette_NotOpenedWhileAttached(t *testing.T) {
	m := paletteModel(t)
	m.attachedTo = "researcher-1"

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	m = updated.(Model)

	if m.palette != nil {
		t.Fatal("attached, ctrl+k belongs to the child's input, not to the orchestrator's palette")
	}
}

func TestPaletteRows_CountsWhatDidNotFit(t *testing.T) {
	all := []paletteEntry{
		{group: paletteCommands, label: "/a"},
		{group: paletteCommands, label: "/b"},
		{group: paletteCommands, label: "/c"},
		{group: paletteCommands, label: "/d"},
	}
	rows := paletteRows(all, 4)
	if len(rows) > 4 {
		t.Fatalf("the budget bounds the rows, got %d", len(rows))
	}
	last := rows[len(rows)-1]
	if !last.header || !strings.Contains(last.label, "more") {
		t.Fatalf("what did not fit should be counted, got %q", last.label)
	}
	if !strings.Contains(last.label, "2 more") {
		t.Fatalf("the count should include the row it gave up for itself, got %q", last.label)
	}
}

func TestPaletteRows_RailPerGroup(t *testing.T) {
	rows := paletteRows([]paletteEntry{
		{group: paletteCommands, label: "/a"},
		{group: paletteFiles, label: "x.go"},
	}, 10)
	want := []string{"COMMANDS", "/a", "FILES", "x.go"}
	got := make([]string, len(rows))
	for i, r := range rows {
		got[i] = r.label
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestPalette_SavedChatsAreASessionGroup(t *testing.T) {
	db, err := storage.OpenPath(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	msgs := []provider.Message{{Role: provider.RoleUser, Content: "hi"}}
	if err := db.SaveChat("loop-refactor", msgs); err != nil {
		t.Fatal(err)
	}

	m := openPaletteWith(t, paletteModel(t).WithDB(db), "loop-ref")

	joined := strings.Join(paletteLabels(m), "\n")
	if !strings.Contains(joined, "SESSIONS") || !strings.Contains(joined, "loop-refactor") {
		t.Fatalf("a saved chat should be findable, got:\n%s", joined)
	}
	row, ok := m.paletteFocus()
	if !ok || row.text != "/load loop-refactor" {
		t.Fatalf("choosing a session should load it, got %q", row.text)
	}
}
