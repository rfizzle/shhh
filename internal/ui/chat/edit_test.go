package chat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/scope"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/golden"
)

const editLoop = "package agent\n\n// limit is how many rounds a turn may run.\nconst limit = 25\n\nfunc rounds() int {\n\treturn limit\n}\n"

// editFixture is a workspace with loop.go in it, and the path to the file.
func editFixture(t *testing.T) string {
	t.Helper()
	return filepath.Join(fixtureDir(t, map[string]string{"loop.go": editLoop}), "loop.go")
}

// editSessionAt is a coding session over a workspace holding loop.go, sized
// to a terminal of the given width.
func editSessionAt(t *testing.T, width int) (Model, string) {
	t.Helper()
	path := editFixture(t)
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream).
		WithWorkspace(filepath.Dir(path)).
		WithChangeset(changeset.New(changeset.DefaultMaxBytes), nil)
	next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 40})
	return next.(Model), path
}

var (
	editEsc  = tea.KeyPressMsg{Code: tea.KeyEscape}
	editSave = tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}
)

// A save from the pane is written through the write tool's path into the
// changeset as the person's, under the turn the next instruction starts, and
// that turn's close row counts it as changed by you. The tree reading does
// not subtract it: the model did not make it.
func TestEdit_ASaveIsThePersonsChange(t *testing.T) {
	m, path := editSessionAt(t, 130)
	m = sendText(t, m, "/edit loop.go")
	if m.state != stateEditor {
		t.Fatalf("/edit did not open the pane: state %d, last note %q", m.state, lastNote(m))
	}
	m = typeInto(t, m, "// mine\n")
	m = pressKeys(t, m, editSave)
	if m.state != stateEditor {
		t.Fatal("the save closed the pane")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "// mine\n"+editLoop {
		t.Fatalf("the file reads %q (%v)", data, err)
	}
	if got := lastNote(m); got != "wrote 9 lines to loop.go" {
		t.Errorf("the save's receipt reads %q", got)
	}
	turn, ok := m.changes.Turn(m.turnCount + 1)
	if !ok || len(turn.Records) != 1 || turn.Records[0].Origin != changeset.ByPerson {
		t.Fatalf("the changeset holds %+v under the next turn", turn)
	}
	if paths := writtenPaths(m.changes); len(paths) != 0 {
		t.Errorf("the tree reading subtracts the person's save: %v", paths)
	}
	row := m.turnChangesFor(turn, false)
	if row == nil || row.ByYou != 1 {
		t.Fatalf("the close row counts %+v", row)
	}
	if view := (components.TurnClose{Changes: row}).View(130); !strings.Contains(view, "1 file changed +1 −0 · 1 changed by you") {
		t.Errorf("the close row reads:\n%s", view)
	}
	m = pressKeys(t, m, editEsc)
	if m.state == stateEditor {
		t.Error("esc over a saved buffer did not go back to the prompt")
	}
}

// While the pane holds the keyboard every letter is the file's, the letters
// the session spends elsewhere included, and nothing the session answers on a
// letter fires.
func TestEdit_EveryLetterIsTheFiles(t *testing.T) {
	m, path := editSessionAt(t, 110)
	m = sendText(t, m, "/edit loop.go")
	m = typeInto(t, m, "?qyjkxn/ ")
	if m.state != stateEditor {
		t.Fatalf("a letter took the pane away: state %d", m.state)
	}
	if got := m.screens.editPane().pane.Value(); got != "?qyjkxn/ "+editLoop {
		t.Errorf("the buffer reads %q", got)
	}
	if data, _ := os.ReadFile(path); string(data) != editLoop {
		t.Error("typing wrote the file")
	}
}

// esc over a modified buffer asks on the foot row and writes nothing; y
// discards and goes back with the file as it was.
func TestEdit_EscAsksAndYDiscards(t *testing.T) {
	m, path := editSessionAt(t, 130)
	m = sendText(t, m, "/edit loop.go")
	m = typeInto(t, m, "zz")
	m = pressKeys(t, m, editEsc)
	if m.state != stateEditor || !m.screens.editPane().pane.Asking() {
		t.Fatal("esc over a modified buffer did not ask")
	}
	if lines := strings.Join(m.editPaneLines(m.contentWidth(), m.viewportHeight()), "\n"); !strings.Contains(stripANSI(lines), "leave without saving?") {
		t.Errorf("the foot row does not ask:\n%s", stripANSI(lines))
	}
	m = typeInto(t, m, "y")
	if m.state == stateEditor {
		t.Fatal("y on the question did not leave")
	}
	if data, _ := os.ReadFile(path); string(data) != editLoop {
		t.Error("discarding wrote the file")
	}
	if _, ok := m.changes.Latest(); ok {
		t.Error("a discarded buffer is in the changeset")
	}
}

// The cancel chord backs out of the pane as esc does — asking over a
// modified buffer — and arms the quit rather than quitting.
func TestEdit_TheCancelChordBacksOutFirst(t *testing.T) {
	m, _ := editSessionAt(t, 130)
	m = sendText(t, m, "/edit loop.go")
	m = typeInto(t, m, "zz")
	m = pressKeys(t, m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if m.state != stateEditor || !m.screens.editPane().pane.Asking() {
		t.Fatal("the cancel chord over a modified buffer did not ask")
	}
}

// ctrl+s on the question saves and leaves.
func TestEdit_SaveAndLeave(t *testing.T) {
	m, path := editSessionAt(t, 130)
	m = sendText(t, m, "/edit loop.go")
	m = typeInto(t, m, "x")
	m = pressKeys(t, m, editEsc, editSave)
	if m.state == stateEditor {
		t.Fatal("save and leave stayed")
	}
	if data, _ := os.ReadFile(path); string(data) != "x"+editLoop {
		t.Errorf("the file reads %q", data)
	}
}

// A file that moved on disk while the pane was open is refused as the write
// tool refuses a stale overwrite, and the buffer is kept.
func TestEdit_AFileThatMovedIsNotOverwritten(t *testing.T) {
	m, path := editSessionAt(t, 130)
	m = sendText(t, m, "/edit loop.go")
	m = typeInto(t, m, "x")
	if err := os.WriteFile(path, []byte("somebody else\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m = pressKeys(t, m, editSave)
	if data, _ := os.ReadFile(path); string(data) != "somebody else\n" {
		t.Errorf("the save overwrote a file that moved: %q", data)
	}
	if m.state != stateEditor || !strings.HasPrefix(lastNote(m), "could not save loop.go") {
		t.Errorf("state %d, note %q", m.state, lastNote(m))
	}
}

// The doors that do not open: no path, a missing file, a file outside the
// working scope, and a conversation, which changes nothing.
func TestEdit_RefusesWhatItCannotOpen(t *testing.T) {
	m, path := editSessionAt(t, 110)
	for line, want := range map[string]string{
		"/edit":         "/edit opens a file in a pane: /edit <path>",
		"/edit gone.go": "cannot edit gone.go: no such file",
	} {
		got := sendText(t, m, line)
		if got.state == stateEditor || !strings.Contains(lastNote(got), want) {
			t.Errorf("%s: state %d, note %q", line, got.state, lastNote(got))
		}
	}

	sc, errs := scope.New(t.TempDir())
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	scoped := sendText(t, m.WithScope(sc), "/edit "+path)
	if scoped.state == stateEditor || !strings.Contains(lastNote(scoped), "outside the working scope — /add-dir") {
		t.Errorf("outside the scope: state %d, note %q", scoped.state, lastNote(scoped))
	}

	chat := sendText(t, m.WithConversation(), "/edit loop.go")
	if chat.state == stateEditor || !strings.Contains(lastNote(chat), "/edit is not part of this session") {
		t.Errorf("in a conversation: state %d, note %q", chat.state, lastNote(chat))
	}
}

// TestGolden_EditPane captures the pane as the session draws it over the
// feed, and the one row it leaves where the draft box was.
func TestGolden_EditPane(t *testing.T) {
	captureGolden(t, "edit-pane", "the editor pane over the feed", goldenWidths, func(width int) []golden.Panel {
		m, _ := editSessionAt(t, width)
		m = sendText(t, m, "/edit loop.go")
		opened := strings.Join(m.editPaneLines(m.contentWidth(), 16), "\n")
		m = typeInto(t, m, "// mine\n")
		m = pressKeys(t, m, editEsc)
		return []golden.Panel{
			{Label: "opened over the feed", View: opened},
			{Label: "modified, and esc asked", View: strings.Join(m.editPaneLines(m.contentWidth(), 16), "\n")},
			{Label: "the panel it leaves · the way out, and no letter", View: m.takeoverPanel(m.contentWidth())},
		}
	})
}

// The scene's route through the real program: /edit opens the file over the
// feed, a typed line marks it modified, esc asks on the foot row and esc
// keeps editing, ctrl+s saves through the write and says so, esc goes back,
// and the next turn's close counts the save as the person's.
func TestProgram_AnEditInThePaneIsCountedOnTheClose(t *testing.T) {
	path := editFixture(t)
	root := filepath.Dir(path)
	m := readingSession(root, programTurn{text: "The limit reads as you left it."}).
		WithChangeset(changeset.New(changeset.DefaultMaxBytes), nil)
	tm := runProgramAt(t, m, 130, 40)

	send(tm, "/edit loop.go")
	waitForText(t, tm, "OUTLINE 2 declarations")
	tm.Type("// mine")
	programPress(t, tm, "enter")
	waitForText(t, tm, "· modified")
	programPress(t, tm, "esc")
	waitForText(t, tm, "leave without saving?")
	programPress(t, tm, "esc")
	waitForText(t, tm, "[ctrl+s] save · [esc] back to the prompt")
	programPress(t, tm, "ctrl+s")
	waitForText(t, tm, "wrote 9 lines to loop.go")
	programPress(t, tm, "esc")
	waitForText(t, tm, "[enter] send")
	send(tm, "read the limit back")
	waitForText(t, tm, "1 changed by you")

	frameHas(t, finalFrame(t, tm), "1 file changed +1 −0 · 1 changed by you")
	if data, _ := os.ReadFile(path); string(data) != "// mine\n"+editLoop {
		t.Errorf("the file reads %q", data)
	}
}
