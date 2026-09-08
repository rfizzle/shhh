package chat

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
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
	updated, _ := m.Update(ctrlSlash)
	m = updated.(Model)
	if m.palette == nil || m.state != statePick {
		t.Fatal("the palette chord should open the palette on the picker surface")
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

// The query row is what is being typed into, so the panel puts the
// terminal's cursor on it rather than on the draft the palette covered.
func TestPalette_QueryRowTakesTheCursor(t *testing.T) {
	m := openPaletteWith(t, paletteModel(t), "cl")

	var cur cursorSink
	screen := strings.Split(ansi.Strip(m.paint(&cur)), "\n")
	if cur.at == nil {
		t.Fatal("the query row owns the keyboard, so it owns the cursor")
	}
	row := -1
	for i, line := range screen {
		if strings.Contains(line, "▸ cl") {
			row = i
		}
	}
	if row < 0 {
		t.Fatalf("fixture: the query row should be on screen:\n%s", strings.Join(screen, "\n"))
	}
	if cur.at.Y != row {
		t.Fatalf("cursor on row %d, want the query row at %d", cur.at.Y, row)
	}
	// The cell the cursor stands on is the space where the card used to paint
	// its own block, so the column is the one after the query.
	if got := []rune(screen[row])[cur.at.X-1]; got != 'l' {
		t.Fatalf("cursor column %d stands after %q, want the end of the query", cur.at.X, got)
	}
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
	if !strings.Contains(ansi.Strip(m.picker.View(70)), "▸ ") {
		t.Fatalf("the palette should carry its query line:\n%s", m.picker.View(70))
	}
	// The panel places the terminal's own cursor on that row, so the card
	// paints no block there and reports the coordinate instead.
	if m.picker.Cursor(70) == nil {
		t.Fatal("the query row is where the terminal's cursor goes, so the card should say where")
	}
	if got := strings.Join(m.picker.Chips, ""); !strings.Contains(got, "matches") {
		t.Fatalf("the title rail should count the matches, got %q", got)
	}
	if m.picker.Title != keys.Shown(keys.Draft.Palette) {
		t.Fatalf("the card is titled with the chord that opened it, got %q", m.picker.Title)
	}
	if m.palette.rows[m.picker.Focus].header {
		t.Fatal("the pointer should open on a row a key can land on, not on a rail")
	}
}

// TestPalette_CountsMatchesAgainstTheWholeReach pins the chip's two readings:
// a query that kept everything says how much there is, and one that narrowed
// says how much of it survived.
func TestPalette_CountsMatchesAgainstTheWholeReach(t *testing.T) {
	all := openPaletteWith(t, paletteModel(t), "")
	total := len(all.palette.all)
	if got, want := strings.Join(all.picker.Chips, ""), fmt.Sprintf("%d matches", total); got != want {
		t.Fatalf("an unfiltered palette should count what it holds, got %q want %q", got, want)
	}

	some := openPaletteWith(t, paletteModel(t), "permissions")
	got := strings.Join(some.picker.Chips, "")
	if !strings.Contains(got, " of ") || !strings.HasSuffix(got, fmt.Sprintf("of %d matches", total)) {
		t.Fatalf("a narrowed palette should count against the whole reach, got %q", got)
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

// The chord the palette gave up to the hold still moves inside the list. The
// two are not in competition: the palette is a takeover, so its keys are
// routed before the draft's are ever consulted.
func TestPalette_TheOldChordStillMovesInsideTheList(t *testing.T) {
	m := openPaletteWith(t, paletteModel(t), "")
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = updated.(Model)
	moved := m.picker.Focus

	updated, _ = m.Update(ctrlP)
	m = updated.(Model)
	if m.palette == nil || m.state != statePick {
		t.Fatal("the old chord closed the palette instead of moving in it")
	}
	if m.picker.Focus >= moved {
		t.Errorf("the old chord did not move the cursor back: %d, was %d", m.picker.Focus, moved)
	}
	if m.palette.query != "" {
		t.Errorf("the chord typed into the query: %q", m.palette.query)
	}
}

func TestPalette_IdleOnlyCommandsDimRatherThanDrop(t *testing.T) {
	m := paletteModel(t)
	m.setTurnState(stateStreaming)
	m = openPaletteWith(t, m, "compact")

	row, ok := m.paletteFocus()
	if !ok || !strings.HasPrefix(row.label, "/compact") {
		t.Fatalf("an idle-only command should still be offered while the agent works, got %q", row.label)
	}
	if row.dim == "" {
		t.Fatal("it should be dimmed rather than offered as runnable")
	}
	if row.meta != idleOnlyMeta {
		t.Fatalf("the reason belongs in the field at the row's end, got %q", row.meta)
	}
	if !strings.Contains(row.desc, "summary") {
		t.Fatalf("a greyed row keeps the command's own words, got %q", row.desc)
	}
	view := ansi.Strip(m.picker.View(110))
	if !strings.Contains(view, "⊘ /compact") || !strings.Contains(view, idleOnlyMeta) {
		t.Fatalf("the card should draw the glyph and the reason:\n%s", view)
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

	updated, _ := m.Update(ctrlSlash)
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

	updated, _ := m.Update(ctrlSlash)
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
	if last.header || last.label != "" {
		t.Fatalf("a fold marker is not a rail and writes no label of its own, got %+v", last)
	}
	if last.fold != 2 {
		t.Fatalf("the count should include the row it gave up for itself, got %d", last.fold)
	}

	// It draws as the fold marker every windowed list on the screen draws.
	card := components.Select{Options: []components.SelectOption{
		{Label: "/a"}, {Fold: last.fold},
	}}
	if view := ansi.Strip(card.View(40)); !strings.Contains(view, "↓ 2 more") {
		t.Fatalf("the marker should count what is behind it:\n%s", view)
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
