package chat

// The draft in the reader's own editor (
// docs/interface/surfaces.md#the-input-frame).
//
// A three-row box is the wrong place to write a paragraph, and the sentence
// worth writing carefully is exactly the one that does not fit in it. So the
// draft goes out to $EDITOR as a file and comes back as text.
//
// The file is the interface, which is what makes this work with every editor
// rather than with a list of them: shhh writes it, the editor is handed the
// path, and whatever is in the file when the editor exits is the draft. The
// only editor-specific knowledge here is where to put the cursor, and that is
// optional — an editor shhh does not recognise opens at the top of the file
// rather than being handed an argument it would read as a filename.
//
// The editor takes the terminal with it (tea.ExecProcess suspends the program
// and restores it afterwards; the alt screen and the mouse mode come back
// because View declares both on every frame). That is why work in flight
// refuses the chord instead of queueing it: a stream landing in a screen
// nobody is rendering, and a decision answered by keystrokes going to vim,
// are both worse than being told to press it again in a moment.
//
// An editor that forks — `gvim` or `code` without their wait flags — hands
// the terminal straight back and the file comes back exactly as it went out,
// which reads here as an edit nobody made. There is nothing shhh can do about
// that from this side; it is the reason those editors have a wait flag.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// editorDoneMsg is the editor's exit, carrying the file it was given. The
// path travels with the message rather than living on the Model because the
// file has exactly one owner — the handler that reads it and removes it — and
// a field would be a second place for it to be forgotten.
type editorDoneMsg struct {
	path string
	err  error
}

// openEditor writes the draft out, hands it to the editor and pauses the
// program until it exits.
func (m Model) openEditor() (tea.Model, tea.Cmd) {
	if reason, refused := m.editorRefusal(); refused {
		return m.surfaceNotice(reason)
	}
	path, err := writeDraftFile(m.input.Value())
	if err != nil {
		return m.surfaceNotice("could not write the draft out — " + err.Error())
	}
	// The cursor is one-based to an editor and zero-based to the textarea.
	argv := editorArgv(editorCommand(), path, m.input.Line()+1, m.input.Column()+1)
	// Run directly rather than through a shell. The cost is that $EDITOR is
	// split on spaces and nothing else, so a setting whose quoting matters —
	// `emacsclient -a ""`, a path with a space in it — loses it. The gain is
	// that there is no second interpreter between the reader and their own
	// setting, and no shell metacharacter in a filename to think about.
	proc := exec.Command(argv[0], argv[1:]...)
	return m, tea.ExecProcess(proc, func(err error) tea.Msg {
		return editorDoneMsg{path: path, err: err}
	})
}

// editorFinished takes the file back. Every exit the editor can make arrives
// here — clean, non-zero, or never started — which is what makes this the one
// place the temp file is removed.
func (m Model) editorFinished(msg editorDoneMsg) (tea.Model, tea.Cmd) {
	defer func() { _ = os.Remove(msg.path) }()
	if msg.err != nil {
		return m.surfaceNotice("the editor exited with an error, so the draft is as you left it — " + msg.err.Error())
	}
	edited, err := os.ReadFile(msg.path)
	if err != nil {
		return m.surfaceNotice("could not read the draft back, so it is as you left it — " + err.Error())
	}
	text := strings.TrimSpace(string(edited))
	if text == "" {
		// Emptying the file is how vi says "I changed my mind", and it is
		// also what a `:q!` on a file the editor never wrote looks like from
		// here. Neither is worth throwing a paragraph away for, so the draft
		// stands and the notice says it did.
		return m.surfaceNotice("the editor came back empty, so the draft is as you left it")
	}
	m.input.SetValue(text)
	m.input.MoveToEnd()
	// What came back is a fresh draft, not the history entry it may have
	// started as. Without this, editing a recalled message and coming back
	// leaves ↑ pointing into the history, and the next one silently replaces
	// the paragraph that was just written.
	m.historyIdx = len(m.inputHistory)
	// The draft changed without a keystroke, so the two things a keystroke
	// would have done are done here: the slash menu is re-read from it, and
	// the viewport is resized, because a draft that came back starting with
	// `/` opens a menu that takes rows from the transcript.
	m.syncCompletions()
	m.syncViewport()
	return m, nil
}

// editorRefusal is whether the chord is refused here, and why. The first two
// are the two things suspending the program would break; the third cannot be
// reached through the keyboard, because every surface that takes it is routed
// before the draft's keys are, and is here because a guard that fails closed
// costs nothing.
//
// frameWorking rather than working: attached, the turn that must not be
// suspended is the child's, and the orchestrator's own state says nothing
// about it.
func (m Model) editorRefusal() (string, bool) {
	switch {
	case m.interruptShowing():
		return "a decision is waiting — answer it first, then open the editor", true
	case m.working() || m.frameWorking():
		return "not while the turn is running — the editor takes the terminal with it", true
	case !m.inputLive():
		return "the draft does not have the keyboard", true
	}
	return "", false
}

// writeDraftFile puts the draft in the user's temp directory as markdown: it
// is prose, and an editor that guesses a mode from the extension guesses the
// right one.
func writeDraftFile(draft string) (string, error) {
	f, err := os.CreateTemp("", "shhh-draft-*.md")
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(draft); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// editorCommand is what the reader has said they edit with, as a command and
// its arguments.
//
// The convention runs the other way — $VISUAL is the full-screen editor and
// wins where there is a terminal, $EDITOR the line editor behind it — and
// this deliberately reads $EDITOR first. $EDITOR is the one that is actually
// set, and where the two differ $VISUAL is the likelier of the pair to name a
// windowed editor, which is exactly the one that forks and hands the terminal
// straight back. vi is last because POSIX guarantees it is there.
func editorCommand() []string {
	for _, name := range []string{"EDITOR", "VISUAL"} {
		if spec := strings.TrimSpace(os.Getenv(name)); spec != "" {
			if fields := strings.Fields(spec); len(fields) > 0 {
				return fields
			}
		}
	}
	return []string{"vi"}
}

// editorArgv is the whole command line: the editor with its own arguments,
// then the cursor position if it takes one, then the file.
func editorArgv(command []string, path string, line, col int) []string {
	argv := append([]string(nil), command...)
	argv = append(argv, editorPosition(command[0], line, col)...)
	return append(argv, path)
}

// editorPosition is the argument that opens the file where the draft's cursor
// was. Only the spellings shhh knows are sent: an editor handed a `+12` it
// does not understand opens a second file called `+12` and leaves the draft
// in the first one, which is a worse failure than opening at line one.
func editorPosition(command string, line, col int) []string {
	switch filepath.Base(command) {
	case "vi", "vim", "nvim", "view", "gvim", "vimdiff":
		// Vim's `+{line}` is a command, and there is no column form of it
		// that survives being one.
		return []string{fmt.Sprintf("+%d", line)}
	case "nano":
		return []string{fmt.Sprintf("+%d,%d", line, col)}
	case "emacs", "emacsclient", "micro", "kak":
		return []string{fmt.Sprintf("+%d:%d", line, col)}
	}
	return nil
}
