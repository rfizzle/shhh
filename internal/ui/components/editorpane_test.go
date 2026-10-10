package components

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/golden"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// editorLoop is the artboard's file: eight declarations, the round limit's
// comment over runRound, and lines long enough to wrap at sixty columns.
func editorLoop() string {
	var b strings.Builder
	b.WriteString("package agent\n\nimport (\n\t\"context\"\n\t\"errors\"\n\t\"fmt\"\n)\n\n")
	b.WriteString("const defaultMaxRounds = 25\n\n")
	b.WriteString("// ErrRoundLimit is returned when the session has spent its rounds.\n")
	b.WriteString("var ErrRoundLimit = errors.New(\"round limit\")\n\n")
	b.WriteString("// Loop runs one conversation against one provider.\n")
	b.WriteString("type Loop struct {\n\tprovider  Provider\n\trounds    int\n\tmaxRounds int\n}\n\n")
	b.WriteString("// New is a loop with the default round limit.\n")
	b.WriteString("func New(p Provider) *Loop {\n\treturn &Loop{provider: p, maxRounds: defaultMaxRounds}\n}\n\n")
	b.WriteString("// Run runs rounds until the model stops asking for calls.\n")
	b.WriteString("func (l *Loop) Run(ctx context.Context) error {\n\tfor {\n\t\tif err := l.runRound(ctx); err != nil {\n\t\t\treturn err\n\t\t}\n\t}\n}\n\n")
	b.WriteString("// runRound sends one request and runs the calls it asks for. It returns\n")
	b.WriteString("// ErrRoundLimit when the session has spent its rounds, and the caller\n")
	b.WriteString("// decides whether to offer more.\n")
	b.WriteString("func (l *Loop) runRound(ctx context.Context) error {\n")
	b.WriteString("\tif l.rounds >= l.maxRounds {\n\t\treturn ErrRoundLimit\n\t}\n")
	b.WriteString("\tl.rounds++\n")
	b.WriteString("\tresp, err := l.provider.Send(ctx, l.request())\n")
	b.WriteString("\tif err != nil {\n\t\treturn fmt.Errorf(\"round %d: %w\", l.rounds, err)\n\t}\n")
	b.WriteString("\tfor _, call := range resp.Calls {\n\t\tif err := l.run(ctx, call); err != nil {\n\t\t\treturn err\n\t\t}\n\t}\n")
	b.WriteString("\treturn nil\n}\n\n")
	b.WriteString("func (l *Loop) run(ctx context.Context, c Call) error { return c.Do(ctx) }\n\n")
	b.WriteString("func (l *Loop) request() Request { return Request{} }\n")
	return b.String()
}

// editorLine is the index of the first line that opens with prefix.
func editorLine(t *testing.T, p *EditorPane, prefix string) int {
	t.Helper()
	for i, l := range p.lines {
		if strings.HasPrefix(string(l), prefix) {
			return i
		}
	}
	t.Fatalf("no line opens with %q", prefix)
	return 0
}

// editorAt is the pane over the artboard's file with the cursor on a line,
// one-based, and the column given in runes.
func editorAt(t *testing.T, line, col int) *EditorPane {
	t.Helper()
	p := NewEditorPane("internal/agent/loop.go", editorLoop())
	p.cur = editorPos{line - 1, col}
	return p
}

func pressEditor(t *testing.T, p *EditorPane, msgs ...tea.KeyPressMsg) (bool, EditorResult) {
	t.Helper()
	var done bool
	var res EditorResult
	for _, m := range msgs {
		done, res = p.Update(m)
	}
	return done, res
}

func typeEditor(t *testing.T, p *EditorPane, text string) {
	t.Helper()
	for _, r := range text {
		pressEditor(t, p, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

var (
	editorEsc   = tea.KeyPressMsg{Code: tea.KeyEscape}
	editorSave  = tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}
	editorEnter = tea.KeyPressMsg{Code: tea.KeyEnter}
)

// TestGolden_EditorPane captures the pane's states at every width: open on
// the artboard's file with the outline beside it, the person's two lines
// marked in the gutter with a selection on the cursor's line, the question
// esc asks over them, and the receipt a save leaves on the foot row. Below
// the rail's rung the outline folds into the header's second line and the
// code wraps under a blank gutter.
func TestGolden_EditorPane(t *testing.T) {
	captureBoundedGolden(t, "editor-pane", "the editor pane", goldenWidths, func(width int) []golden.Panel {
		// The artboard's window: from the comment over runRound, with the
		// cursor in the call to the provider.
		view := func(p *EditorPane) string {
			p.SetSize(width, 23)
			p.top = editorLine(t, p, "// runRound sends")
			return p.View(width)
		}
		open := NewEditorPane("internal/agent/loop.go", editorLoop())
		open.cur = editorPos{editorLine(t, open, "\tresp, err"), 1}

		// The person's two lines: a comment above the return, and the return
		// itself rewritten, with three runes of it selected.
		person := func() *EditorPane {
			p := NewEditorPane("internal/agent/loop.go", editorLoop())
			p.cur = editorPos{editorLine(t, p, "\tif l.rounds >= l.maxRounds {"), 0}
			pressEditor(t, p, tea.KeyPressMsg{Code: tea.KeyEnd}, editorEnter)
			typeEditor(t, p, "\t\t// The chat model offers more rounds on this error.")
			pressEditor(t, p, tea.KeyPressMsg{Code: tea.KeyDown}, tea.KeyPressMsg{Code: tea.KeyEnd})
			for range len("ErrRoundLimit") {
				pressEditor(t, p, tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModShift})
			}
			typeEditor(t, p, `fmt.Errorf("%w: %d of %d", ErrRoundLimit, l.rounds, l.maxRounds)`)
			p.cur.col = 9
			return p
		}
		modified := person()
		for range 3 {
			pressEditor(t, modified, tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModShift})
		}

		asking := person()
		pressEditor(t, asking, editorEsc)

		saved := person()
		saved.Saved(saved.Value(), WriteReceipt(plural(saved.LineCount(), "line"), saved.Path))

		return []golden.Panel{
			{Label: "open · the cursor's line bright, the outline on its declaration", View: view(open)},
			{Label: "modified · the person's lines marked, a selection on the cursor's line", View: view(modified)},
			{Label: "esc on a modified buffer · the foot row asks", View: view(asking)},
			{Label: "saved · the receipt on the foot row, the marks gone", View: view(saved)},
			// Back at the prompt, the turn's close counts the save as the
			// person's among the files it changed.
			{Label: "the close row · the save counted as yours", View: TurnClose{
				Steps: 3, Tools: 9, Elapsed: "41.2s", Spend: "$0.22",
				Changes: &TurnChanges{Files: 2, Added: 14, Removed: 3, ByYou: 1,
					Note: "all tracked in git", Back: "/undo 1 takes it back"},
			}.View(width)},
		}
	})
}

// Every keystroke a sentence produces is the file's, `?` and the letters the
// rest of the product spends included, and a tab is a tab.
func TestEditorPane_EveryLetterIsText(t *testing.T) {
	p := NewEditorPane("notes.txt", "a\n")
	typeEditor(t, p, "?yq/ ")
	pressEditor(t, p, tea.KeyPressMsg{Code: tea.KeyTab})
	if got := p.Value(); got != "?yq/ \ta\n" {
		t.Fatalf("the buffer reads %q", got)
	}
	if !p.Modified() {
		t.Error("a typed buffer is not modified")
	}
}

// A paste is text whatever it spells, and the draft's newline chord is a
// newline here too.
func TestEditorPane_APasteIsTextAndCtrlJIsALine(t *testing.T) {
	p := NewEditorPane("f.txt", "a\n")
	for _, run := range []string{"esc", "delete", "ctrl+s"} {
		if done, res := pressEditor(t, p, tea.KeyPressMsg{Code: []rune(run)[0], Text: run}); done || res != EditorTyping {
			t.Errorf("a paste of %q acted as a key: done %v, %v", run, done, res)
		}
	}
	pressEditor(t, p, tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
	if got := p.Value(); got != "escdeletectrl+s\na\n" {
		t.Errorf("the buffer reads %q", got)
	}
	pressEditor(t, p, editorEsc)
	if done, _ := pressEditor(t, p, tea.KeyPressMsg{Code: 'y', Text: "yes"}); done || !p.Asking() {
		t.Error("a paste under the question answered it")
	}
}

// A file is written back as it was read: tabs, a final newline and carriage
// returns are the file's, not the pane's to tidy.
func TestEditorPane_AFileComesBackAsItWasRead(t *testing.T) {
	for _, content := range []string{editorLoop(), "no newline", "crlf\r\nlines\r\n", "", "\n\n"} {
		p := NewEditorPane("f.go", content)
		if got := p.Value(); got != content {
			t.Errorf("%q came back as %q", content, got)
		}
		if p.Modified() {
			t.Errorf("%q reads as modified before a key", content)
		}
	}
}

// esc over the disk's own buffer hands the keyboard back; over a modified one
// it asks, the typing stops, and each answer does what it says.
func TestEditorPane_EscAsksOverAModifiedBuffer(t *testing.T) {
	p := NewEditorPane("f.txt", "one\n")
	if done, res := pressEditor(t, p, editorEsc); !done || res != EditorLeave {
		t.Fatalf("esc over an unmodified buffer: done %v, %v", done, res)
	}

	p = NewEditorPane("f.txt", "one\n")
	typeEditor(t, p, "x")
	if done, _ := pressEditor(t, p, editorEsc); done || !p.Asking() {
		t.Fatal("esc over a modified buffer left without asking")
	}
	typeEditor(t, p, "abc")
	if p.Value() != "xone\n" {
		t.Errorf("a letter typed under the question reached the file: %q", p.Value())
	}
	if done, _ := pressEditor(t, p, editorEsc); done || p.Asking() {
		t.Error("esc on the question did not keep editing")
	}
	typeEditor(t, p, "y")
	if p.Value() != "xyone\n" {
		t.Errorf("y while editing is a letter, the buffer reads %q", p.Value())
	}
	pressEditor(t, p, editorEsc)
	if done, res := pressEditor(t, p, editorSave); done || res != EditorSaveLeave {
		t.Errorf("ctrl+s on the question: done %v, %v", done, res)
	}
	// A save that did not land leaves the buffer to be edited, and esc asks
	// again.
	pressEditor(t, p, editorEsc)
	if done, res := pressEditor(t, p, tea.KeyPressMsg{Code: 'y', Text: "y"}); !done || res != EditorLeave {
		t.Errorf("y on the question: done %v, %v", done, res)
	}
}

// ctrl+c is the draft's alone: over a modified buffer it neither asks nor
// leaves, where esc asks and keeps editing on the question.
func TestEditorPane_CtrlCDoesNotBackOutWhereEscDoes(t *testing.T) {
	ctrlC := tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	p := NewEditorPane("f.txt", "one\n")
	typeEditor(t, p, "x")
	if done, _ := pressEditor(t, p, ctrlC); done || p.Asking() || p.Value() != "xone\n" {
		t.Fatalf("ctrl+c over a modified buffer: done %v asking %v buffer %q", done, p.Asking(), p.Value())
	}
	pressEditor(t, p, tea.KeyPressMsg{Code: tea.KeyEscape})
	if !p.Asking() {
		t.Fatal("esc over a modified buffer did not ask")
	}
	pressEditor(t, p, tea.KeyPressMsg{Code: tea.KeyEscape})
	if p.Asking() || p.Value() != "xone\n" {
		t.Errorf("esc on the question: asking %v, buffer %q", p.Asking(), p.Value())
	}
}

// ctrl+s asks the host to save and keeps the pane; the save the host reports
// takes the marks and the modified word away.
func TestEditorPane_SaveIsAskedOfTheHost(t *testing.T) {
	p := NewEditorPane("f.txt", "one\n")
	typeEditor(t, p, "x")
	if done, res := pressEditor(t, p, editorSave); done || res != EditorSave {
		t.Fatalf("ctrl+s: done %v, %v", done, res)
	}
	p.Saved(p.Value(), "wrote 1 line to f.txt")
	if p.Modified() {
		t.Error("a saved buffer is still modified")
	}
	if done, res := pressEditor(t, p, tea.KeyPressMsg{Code: ']', Mod: tea.ModCtrl}); done || res != EditorKeys {
		t.Errorf("the key list chord: done %v, %v", done, res)
	}
	if !strings.Contains(keys.Shown(keys.Editor.Save), "ctrl+s") {
		t.Errorf("the save is spelled %q", keys.Shown(keys.Editor.Save))
	}
}

// A selection is replaced by what is typed over it and taken out by
// backspace.
func TestEditorPane_TypingReplacesTheSelection(t *testing.T) {
	p := NewEditorPane("f.txt", "hello world\n")
	shiftRight := tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModShift}
	pressEditor(t, p, shiftRight, shiftRight, shiftRight, shiftRight, shiftRight)
	typeEditor(t, p, "HELLO")
	if p.Value() != "HELLO world\n" {
		t.Errorf("typing over a selection reads %q", p.Value())
	}
	pressEditor(t, p, tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModShift}, tea.KeyPressMsg{Code: tea.KeyBackspace})
	if p.Value() != "HELL world\n" {
		t.Errorf("backspace over a selection reads %q", p.Value())
	}
	pressEditor(t, p, editorEnter)
	if p.Value() != "HELL\n world\n" {
		t.Errorf("enter splits the line, the buffer reads %q", p.Value())
	}
}

// The outline is a Go file's top-level declarations and a document's
// headings, and a click on an entry's row puts the cursor on its line.
func TestEditorPane_TheOutlineAndAClickOnIt(t *testing.T) {
	p := editorAt(t, 1, 0)
	p.read()
	var labels []string
	for _, e := range p.outline {
		labels = append(labels, e.label)
	}
	want := []string{"const defaultMaxRounds", "var ErrRoundLimit", "type Loop", "func New",
		"func (l *Loop) Run", "func (l *Loop) runRound", "func (l *Loop) run", "func (l *Loop) request"}
	if strings.Join(labels, "|") != strings.Join(want, "|") {
		t.Errorf("the outline reads %q, want %q", labels, want)
	}

	p.SetSize(130, 23)
	p.View(130)
	line, ok := p.OutlineAt(p.outlineX+2, 3+5)
	if !ok {
		t.Fatal("no outline entry under the sixth entry's row")
	}
	p.GoTo(line)
	if e, _ := p.current(); e.name != "runRound" {
		t.Errorf("the click put the cursor in %q", e.name)
	}
	if _, ok := p.OutlineAt(2, 3+5); ok {
		t.Error("a click on the code found an outline entry")
	}

	md := NewEditorPane("README.md", "# shhh\n\n```\n# not a heading\n```\n## Install\n")
	md.read()
	if len(md.outline) != 2 || md.outline[1].label != "  Install" {
		t.Errorf("the document's outline reads %+v", md.outline)
	}
}
