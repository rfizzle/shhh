package chat

// The editor pane in a session: `/edit <path>`
// (docs/interface/surfaces.md#the-editor-pane).
//
// A person who wanted to change three lines themselves had to leave the
// screen for an editor, and the change they made there reached the session
// as the tree moving under it — nobody's, as far as the turn's close could
// tell. The pane is the same three lines made here: the file opens over the
// feed, the person types, and `[ctrl+s]` writes it through the write tool's
// own path, so the changeset records it as every edit is recorded and the
// close row counts it as theirs
// (docs/capabilities/coding-agent.md#a-turn-ends-with-what-changed).
//
// It is `shhh code`'s and not `shhh chat`'s: a conversation changes nothing,
// and a door to a file write would be the one way it could.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/scope"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// editPaneMaxBytes is the largest file the pane opens. The pane holds the whole
// file and redraws from it on every key, and a file past this is one nobody
// edits three lines of by hand in a terminal pane.
const editPaneMaxBytes = 1 << 20

// editSession is one file open in the pane: the pane, the file on disk it
// writes, and the person's own record of what they opened.
//
// The record is the person's and not the model's. The save is checked
// against it the way any overwrite is checked against a read, so a file that
// moved while the pane was open is refused rather than replaced; and the
// model's own record still holds what the model read, which is what lets
// the next boundary tell the model the file changed
// (docs/capabilities/coding-agent.md#the-tree-can-move-under-a-session).
type editSession struct {
	pane *components.EditorPane
	abs  string
	seen *tools.Recorder
}

// openEditPane is `/edit <path>`: the file, opened over the feed, or the
// sentence saying why not.
func (m Model) openEditPane(parts []string) (tea.Model, tea.Cmd) {
	named := strings.TrimSpace(strings.Join(parts[1:], " "))
	if named == "" {
		return m.systemNotice("/edit opens a file in a pane: /edit <path>")
	}
	abs, err := filepath.Abs(m.inWorkspace(named))
	if err != nil {
		return m.systemNotice(failed("edit", "cannot edit "+named+": "+err.Error()))
	}
	if why := m.editRefusal(named, abs); why != "" {
		return m.systemNotice(failed("edit", why))
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return m.systemNotice(failed("edit", "cannot edit "+named+": "+err.Error()))
	}
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return m.systemNotice(failed("edit", "cannot edit "+named+": it is not text"))
	}
	seen := tools.NewRecorder()
	seen.NoteOpened(abs, data)
	pane := components.NewEditorPane(m.editPaneName(named, abs), string(data))
	m.screens = m.screens.with(stateEditor, &editSession{pane: pane, abs: abs, seen: seen})
	m.enterSurface(stateEditor)
	m.syncViewport()
	return m, nil
}

// editRefusal is why a path cannot be opened, or "". A file outside the
// working scope is refused with the scope's own sentence: the pane writes
// where the session may write, and the way to widen that is the grant every
// other write asks for.
func (m Model) editRefusal(named, abs string) string {
	if dirs := m.scope.Outside(abs); len(dirs) > 0 {
		if class, reason := scope.Classify(dirs[0]); class == scope.Refused {
			return "cannot edit " + named + ": " + reason
		}
		return fmt.Sprintf("cannot edit %s: it is outside the working scope — /add-dir %s brings it in", named, displayDir(dirs[0]))
	}
	info, err := os.Stat(abs)
	switch {
	case err != nil:
		return "cannot edit " + named + ": no such file"
	case info.IsDir():
		return "cannot edit " + named + ": it is a directory"
	case info.Size() > editPaneMaxBytes:
		return fmt.Sprintf("cannot edit %s: at %d KB it is past what the pane opens", named, info.Size()>>10)
	}
	return ""
}

// editPaneName is the file as the header names it: relative to the workspace
// where it is inside it, as it was typed otherwise.
func (m Model) editPaneName(named, abs string) string {
	if m.workspace != "" {
		if rel, err := filepath.Rel(m.workspace, abs); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(filepath.Clean(named))
}

func (h heldScreens) editPane() *editSession {
	return heldAs[editSession](h, stateEditor)
}

// answerEditPane routes one key while the pane is up. The pane answers every
// key itself; what it asks of the session is a write, a way out, or its key
// list.
func (m *Model) answerEditPane(msg tea.KeyPressMsg) (bool, overlayAction) {
	s := m.screens.editPane()
	if s == nil {
		return true, m.closeEditPane("")
	}
	done, res := s.pane.Update(msg)
	switch res {
	case components.EditorKeys:
		next, _ := m.openKeyList(keys.OnEditor, nil)
		*m = next.(Model)
		return false, overlayAction{}
	case components.EditorSave:
		note, _ := m.saveEditPane(s)
		return false, overlayAction{note: note}
	case components.EditorSaveLeave:
		note, ok := m.saveEditPane(s)
		if !ok {
			return false, overlayAction{note: note}
		}
		return true, m.closeEditPane(note)
	}
	if done {
		return true, m.closeEditPane("")
	}
	return false, overlayAction{}
}

// saveEditPane writes the buffer through the write tool's own path and records
// it in the changeset as the person's, under the turn that is open or, with
// none open, the turn the next instruction starts — the close row that turn
// draws is the one that counts it. What it returns is the receipt, or the
// sentence saying the write did not land, which the pane's foot row carries
// as well; the buffer is kept either way.
func (m *Model) saveEditPane(s *editSession) (string, bool) {
	content := s.pane.Value()
	args, err := json.Marshal(map[string]string{"path": s.abs, "content": content})
	if err != nil {
		return m.editPaneFailed(s, err), false
	}
	turn := m.turnCount
	if !m.turnOpen {
		turn++
	}
	record := changeRecording{store: m.changes, tracker: m.tracker, turn: turn, path: s.abs, origin: changeset.ByPerson}
	before := record.before()
	if _, err := s.seen.ExecuteMutating(tools.WriteFileName, args); err != nil {
		return m.editPaneFailed(s, err), false
	}
	m.noteEvictedTurns(record.after(before))
	receipt := components.WriteReceipt(plural(s.pane.LineCount(), "line"), s.pane.Path)
	s.pane.Saved(content, receipt)
	return receipt, true
}

func (m *Model) editPaneFailed(s *editSession, err error) string {
	note := "could not save " + s.pane.Path + ": " + err.Error()
	s.pane.Notice = note
	return note
}

// closeEditPane hands the screen back to the turn.
func (m *Model) closeEditPane(note string) overlayAction {
	m.screens = m.screens.without(stateEditor)
	return overlayAction{close: true, note: note}
}

// editPaneLines draws the pane into the rectangle the feed has.
func (m Model) editPaneLines(width, height int) []string {
	s := m.screens.editPane()
	if s == nil {
		return nil
	}
	s.pane.SetSize(width, height)
	return strings.Split(s.pane.View(width), "\n")
}

// renderEditPaneHint is the one line the pane leaves where the draft box was.
// The pane states its own keys on its foot row, so the panel names the
// surface and the way out and nothing else — and no letter, because every
// letter is the file's.
func (m Model) renderEditPaneHint() string {
	hint := segAs(keys.Editor.Back, "back to the prompt")
	if s := m.screens.editPane(); s != nil && s.pane.Asking() {
		hint = segAs(keys.Editor.Keep, "keep editing")
	}
	return sty.SystemMsg.Render("edit · ") + hint.render()
}

// clickEditPane is a click on the pane's outline, which puts the cursor on the
// entry's line. The outline never takes the keyboard; the pane already has
// it.
func (m Model) clickEditPane(x, y int) (tea.Model, tea.Cmd, bool) {
	s := m.screens.editPane()
	if s == nil || m.state != stateEditor {
		return m, nil, false
	}
	sl := m.surface()
	view := sl.in(sl.view, sl.pane)
	line, ok := s.pane.OutlineAt(x-view.Min.X, y-view.Min.Y)
	if !ok {
		return m, nil, false
	}
	s.pane.GoTo(line)
	return m, nil, true
}
