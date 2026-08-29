package chat

// The draft's trip through the reader's own editor (editor.go).
//
// The editor itself is a shell script the test writes: what shhh has to get
// right is the file — that it holds the draft going out, that whatever came
// back is what the draft becomes, and that it is gone afterwards whichever
// way the editor ended.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/subagent"
)

// fakeEditor writes an executable that does script to a draft file it is
// handed, and points $EDITOR at it.
func fakeEditor(t *testing.T, script string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-editor")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EDITOR", path)
	t.Setenv("VISUAL", "")
}

// runEditor is what tea.ExecProcess does for the model: the command shhh
// built, run against the file shhh wrote.
func runEditor(t *testing.T, path string, line, col int) error {
	t.Helper()
	argv := editorArgv(editorCommand(), path, line, col)
	return exec.Command(argv[0], argv[1:]...).Run()
}

func TestEditor_TakesBackWhatTheEditorWrote(t *testing.T) {
	fakeEditor(t, `printf '\nand a second paragraph\n' >> "$1"`)
	m := frameModel(t, 100, 40)
	m.input.SetValue("the first paragraph")

	path, err := writeDraftFile(m.input.Value())
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "the first paragraph" {
		t.Fatalf("the file the editor is handed = %q, want the draft", out)
	}
	if err := runEditor(t, path, 1, 1); err != nil {
		t.Fatal(err)
	}

	updated, _ := m.editorFinished(editorDoneMsg{path: path})
	next := updated.(Model)
	if want := "the first paragraph\nand a second paragraph"; next.input.Value() != want {
		t.Fatalf("draft = %q, want %q", next.input.Value(), want)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the temp file outlived the edit: %v", err)
	}
}

// An editor that fell over is not an instruction to throw the sentence away,
// and the file it was given is still shhh's to clean up.
func TestEditor_FailureKeepsTheDraftAndRemovesTheFile(t *testing.T) {
	fakeEditor(t, `exit 1`)
	m := frameModel(t, 100, 40)
	m.input.SetValue("the sentence that was being written")
	path, err := writeDraftFile(m.input.Value())
	if err != nil {
		t.Fatal(err)
	}
	runErr := runEditor(t, path, 1, 1)
	if runErr == nil {
		t.Fatal("the fake editor should have failed")
	}

	updated, _ := m.editorFinished(editorDoneMsg{path: path, err: runErr})
	next := updated.(Model)
	if next.input.Value() != "the sentence that was being written" {
		t.Fatalf("draft = %q, want it untouched", next.input.Value())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the temp file outlived the failure: %v", err)
	}
	if !strings.Contains(transcriptText(next), "exit status 1") {
		t.Fatalf("the failure should be said out loud:\n%s", transcriptText(next))
	}
}

func TestEditor_EmptyResultLeavesTheDraftStanding(t *testing.T) {
	fakeEditor(t, `: > "$1"`)
	m := frameModel(t, 100, 40)
	m.input.SetValue("a paragraph worth keeping")
	path, err := writeDraftFile(m.input.Value())
	if err != nil {
		t.Fatal(err)
	}
	if err := runEditor(t, path, 1, 1); err != nil {
		t.Fatal(err)
	}

	updated, _ := m.editorFinished(editorDoneMsg{path: path})
	next := updated.(Model)
	if next.input.Value() != "a paragraph worth keeping" {
		t.Fatalf("draft = %q, want it untouched", next.input.Value())
	}
	if !strings.Contains(transcriptText(next), "came back empty") {
		t.Fatalf("an empty result should say so:\n%s", transcriptText(next))
	}
}

// The chord suspends the program, so the two states that have something
// happening on this screen refuse it rather than queueing it.
func TestEditor_RefusedWhileSomethingIsHappening(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*Model)
		says  string
	}{
		{"a turn in flight", func(m *Model) { m.setTurnState(stateStreaming) }, "not while the turn is running"},
		{"a decision waiting", func(m *Model) { m.state = stateConfirmRun }, "a decision is waiting"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := frameModel(t, 100, 40)
			m.input.SetValue("half a sentence")
			c.setup(&m)

			// m.update rather than m.Update: the outer one batches the
			// spinner's tick in, and what is being asserted is that the
			// handler itself ran no process.
			updated, cmd := m.update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
			next := updated.(Model)
			if cmd != nil {
				t.Fatal("the chord should run nothing here")
			}
			if next.input.Value() != "half a sentence" {
				t.Fatalf("draft = %q, want it untouched", next.input.Value())
			}
			if !strings.Contains(transcriptText(next), c.says) {
				t.Fatalf("the refusal should say why:\n%s", transcriptText(next))
			}
		})
	}
}

// The chord's own path, which the refusal tests only ever see say no: it
// runs the editor and leaves the draft alone until the file comes back.
func TestEditor_ChordRunsTheEditorAndHoldsTheDraft(t *testing.T) {
	fakeEditor(t, `:`)
	m := frameModel(t, 100, 40)
	m.input.SetValue("half a sentence")

	updated, cmd := m.update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("the chord should run the editor")
	}
	next := updated.(Model)
	if next.input.Value() != "half a sentence" {
		t.Fatalf("draft = %q, want it held until the editor comes back", next.input.Value())
	}
	if transcriptText(next) != "" {
		t.Fatalf("nothing to say until the editor is done:\n%s", transcriptText(next))
	}
}

// A draft recalled with ↑ and then rewritten is a fresh draft, not the
// history entry it started as: without that, the next ↑ would replace the
// paragraph that was just written.
func TestEditor_ResultLeavesTheHistory(t *testing.T) {
	fakeEditor(t, `printf 'rewritten' > "$1"`)
	m := frameModel(t, 100, 40)
	m.inputHistory = []string{"the first ask", "the second ask"}
	m.historyIdx = 0
	m.input.SetValue(m.inputHistory[0])

	path, err := writeDraftFile(m.input.Value())
	if err != nil {
		t.Fatal(err)
	}
	if err := runEditor(t, path, 1, 1); err != nil {
		t.Fatal(err)
	}
	updated, _ := m.editorFinished(editorDoneMsg{path: path})
	next := updated.(Model)
	if next.browsingHistory() {
		t.Fatal("what came back from the editor is a fresh draft, not a recalled one")
	}

	updated, _ = next.update(tea.KeyPressMsg{Code: tea.KeyUp})
	if got := updated.(Model).input.Value(); got != "rewritten" {
		t.Fatalf("↑ replaced the edited draft with %q", got)
	}
}

// Attached, the turn that must not be suspended is the child's, and the
// orchestrator's own state says nothing about it.
func TestEditor_RefusedWhileTheAttachedChildWorks(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	spawnBlockedChild(t, sup)
	m.attachedTo = "researcher-1"
	m.input.SetValue("half a sentence")
	if m.working() {
		t.Fatal("the orchestrator's own turn is not what is running here")
	}

	updated, cmd := m.update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	next := updated.(Model)
	if cmd != nil {
		t.Fatal("the chord should run nothing while the child works")
	}
	if next.input.Value() != "half a sentence" {
		t.Fatalf("draft = %q, want it untouched", next.input.Value())
	}
}

// $EDITOR first, $VISUAL for a machine that only set that one, and vi last
// because POSIX promises it is there.
func TestEditorCommand_FallsBackInOrder(t *testing.T) {
	t.Setenv("EDITOR", "hx --vsplit")
	t.Setenv("VISUAL", "gedit")
	if got := editorCommand(); len(got) != 2 || got[0] != "hx" || got[1] != "--vsplit" {
		t.Fatalf("editorCommand() = %v, want the command and its arguments", got)
	}
	t.Setenv("EDITOR", "")
	if got := editorCommand(); len(got) != 1 || got[0] != "gedit" {
		t.Fatalf("editorCommand() = %v, want $VISUAL", got)
	}
	t.Setenv("VISUAL", "")
	if got := editorCommand(); len(got) != 1 || got[0] != "vi" {
		t.Fatalf("editorCommand() = %v, want vi", got)
	}
}

// An editor is only told where the cursor was in a spelling it understands:
// one that does not would open a second file named after the argument.
func TestEditorArgv_PositionsOnlyWhereItIsUnderstood(t *testing.T) {
	cases := []struct {
		command []string
		want    []string
	}{
		{[]string{"vim"}, []string{"vim", "+12", "/tmp/draft.md"}},
		{[]string{"/usr/bin/nvim"}, []string{"/usr/bin/nvim", "+12", "/tmp/draft.md"}},
		{[]string{"nano"}, []string{"nano", "+12,5", "/tmp/draft.md"}},
		{[]string{"emacsclient", "-t"}, []string{"emacsclient", "-t", "+12:5", "/tmp/draft.md"}},
		{[]string{"code", "-w"}, []string{"code", "-w", "/tmp/draft.md"}},
	}
	for _, c := range cases {
		got := editorArgv(c.command, "/tmp/draft.md", 12, 5)
		if strings.Join(got, " ") != strings.Join(c.want, " ") {
			t.Errorf("editorArgv(%v) = %v, want %v", c.command, got, c.want)
		}
	}
}
