package chat

// [y] in reading mode (docs/interface/surfaces.md#reading-mode): the focused
// row's content, shaped by what the row is, put on the terminal's own
// clipboard where it takes one and on this machine's where it does not.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/clipboard"
	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// copyModel is a ready session whose copyFn records what reached the
// clipboard.
func copyModel(t *testing.T, caught *[]string) Model {
	t.Helper()
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)
	m.copyFn = func(text string) clipboard.Result {
		*caught = append(*caught, text)
		return clipboard.Result{OK: true}
	}
	return m
}

// yank opens reading mode (cursor on the last row) and presses [y].
func yank(t *testing.T, m Model) Model {
	t.Helper()
	m.viewport.SetLines(m.renderHistoryLines())
	updated, _ := m.Update(readingChord())
	m = updated.(Model)
	if m.state != stateFocus {
		t.Fatalf("reading mode should open, got state %d", m.state)
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	return updated.(Model)
}

func TestCopyRow_EachKind(t *testing.T) {
	cases := []struct {
		name string
		add  func(m *Model)
		want string
	}{
		{"assistant message as markdown source", func(m *Model) {
			m.appendEntry(entry{kind: entryAssistant, text: "It fails **because** the\nround limit is fatal."})
		}, "It fails **because** the\nround limit is fatal."},
		{"command as $ cmd over its output", func(m *Model) {
			m.appendEntry(entry{kind: entryCommand, text: "go test ./...",
				toolResult: "--- FAIL: TestX\nFAIL"})
		}, "$ go test ./...\n--- FAIL: TestX\nFAIL"},
		{"read row as what the read returned", func(m *Model) {
			m.appendEntry(entry{kind: entryTool, toolName: "read_file",
				toolArgs: `{"path":"go.mod"}`, toolResult: "module shhh\n\ngo 1.24"})
		}, "module shhh\n\ngo 1.24"},
		{"edit row as the unified diff", func(m *Model) {
			m.appendEntry(entry{kind: entryDiff, diff: &components.DiffView{
				Path:  "a.go",
				Hunks: diff.Compute("old line\n", "new line\n"),
			}})
		}, "@@ -1,1 +1,1 @@\n-old line\n+new line"},
		{"think row as the reasoning text", func(m *Model) {
			m.appendEntry(entry{kind: entryThink, text: "the cap is a checkpoint"})
		}, "the cap is a checkpoint"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var caught []string
			m := copyModel(t, &caught)
			tc.add(&m)
			m = yank(t, m)
			if len(caught) != 1 {
				t.Fatalf("expected one copy, got %d", len(caught))
			}
			if caught[0] != tc.want {
				t.Fatalf("copied %q, want %q", caught[0], tc.want)
			}
			// The caption on the bar states what was copied and how far it
			// ran, in words and lines.
			if m.readingCopied == "" || !strings.Contains(m.readingCopied, "copied") {
				t.Fatalf("the rail should caption the copy, got %q", m.readingCopied)
			}
		})
	}
}

// What a program painted is not part of what it said: escape sequences are
// stripped from command output on the way to the clipboard.
func TestCopyRow_StripsANSI(t *testing.T) {
	var caught []string
	m := copyModel(t, &caught)
	m.appendEntry(entry{kind: entryCommand, text: "go test ./...",
		toolResult: "--- \x1b[31mFAIL\x1b[0m: TestX"})
	yank(t, m)
	if len(caught) != 1 {
		t.Fatalf("expected one copy, got %d", len(caught))
	}
	if want := "$ go test ./...\n--- FAIL: TestX"; caught[0] != want {
		t.Fatalf("copied %q, want %q", caught[0], want)
	}
}

// A folded group copies each member in order — the fold hides the rows,
// never what they returned.
func TestCopyRow_FoldedGroupCopiesEachMember(t *testing.T) {
	var caught []string
	m := copyModel(t, &caught)
	// Three consecutive read-only calls under an announced step fold into
	// one counted row at normal verbosity (fold_test's shape).
	m.appendEntry(entry{kind: entryUser, text: "read the loop"})
	m.appendEntry(entry{kind: entryAssistant, text: "Reading the loop"})
	for _, r := range []string{"first result", "second result", "third result"} {
		m.appendEntry(entry{kind: entryTool, toolName: "read_file",
			toolArgs: `{"path":"a.go"}`, toolResult: r})
	}
	m.viewport.SetLines(m.renderHistoryLines())
	updated, _ := m.Update(readingChord())
	m = updated.(Model)
	// The finished step arrives folded to its header; [enter] gives its rows
	// back — as the counted group row — and the cursor steps onto it.
	updated, _ = m.updateFocus(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	m.moveFocus(1)
	if es := *m.entries(); !m.groupAnchor(es, m.focusIdx) {
		t.Fatalf("the cursor should stand on the folded group row, got %d", m.focusIdx)
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = updated.(Model)
	if len(caught) != 1 {
		t.Fatalf("expected one copy, got %d", len(caught))
	}
	if want := "first result\nsecond result\nthird result"; caught[0] != want {
		t.Fatalf("copied %q, want %q", caught[0], want)
	}
	if !strings.Contains(m.readingCopied, "3 rows") {
		t.Fatalf("the caption should count the members, got %q", m.readingCopied)
	}
}

// A row with nothing to copy hands the letter back to the draft, the way [-]
// does with nothing open.
func TestCopyRow_NothingToCopyIsALetter(t *testing.T) {
	var caught []string
	m := copyModel(t, &caught)
	m.appendEntry(entry{kind: entryTool, toolName: "read_file",
		toolArgs: `{"path":"a.go"}`, toolResult: pendingToolResult})
	m = yank(t, m)
	if len(caught) != 0 {
		t.Fatalf("a pending call has no result to copy, got %v", caught)
	}
	if m.state == stateFocus || m.input.Value() != "y" {
		t.Fatalf("the letter should land in the draft, state %d draft %q", m.state, m.input.Value())
	}
}

// The caption is the bar's right-hand field while it stands, and the next
// key clears it.
func TestCopyRow_CaptionStandsUntilTheNextKey(t *testing.T) {
	var caught []string
	m := copyModel(t, &caught)
	m.appendEntry(entry{kind: entryCommand, text: "go build", toolResult: "ok"})
	m = yank(t, m)
	line := ansi.Strip(m.readingKeyLine(m.contentWidth()))
	if !strings.Contains(line, "✂ copied command · 2 lines") {
		t.Fatalf("the bar should carry the caption, got %q", line)
	}
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	m = updated.(Model)
	if m.readingCopied != "" {
		t.Fatalf("the next key should clear the caption, got %q", m.readingCopied)
	}
}

// The failure lands in the transcript, where /copy's already goes.
func TestCopyRow_FailureIsATranscriptRow(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)
	m.copyFn = func(string) clipboard.Result {
		return clipboard.Result{Warning: "no clipboard tool found"}
	}
	m.appendEntry(entry{kind: entryCommand, text: "go build", toolResult: "ok"})
	m = yank(t, m)
	es := *m.entries()
	last := es[len(es)-1]
	if last.kind != entrySystem || !strings.Contains(last.text, "no clipboard tool found") {
		t.Fatalf("the warning should land in the transcript, got %q", last.text)
	}
	if m.readingCopied != "" {
		t.Fatal("a failed copy earns no caption")
	}
}

// [y] is offered on the bar only while the row can honour it.
func TestReadingHint_CopyOfferFollowsTheRow(t *testing.T) {
	var caught []string
	m := copyModel(t, &caught)
	m.appendEntry(entry{kind: entryCommand, text: "go build", toolResult: "ok"})
	m.viewport.SetLines(m.renderHistoryLines())
	updated, _ := m.Update(readingChord())
	m = updated.(Model)
	if line := ansi.Strip(m.readingKeyLine(m.contentWidth())); !strings.Contains(line, "["+keys.Shown(keys.Reading.Copy)+"]") {
		t.Fatalf("a command row should offer [y], got %q", line)
	}
}

// Over ssh the machine running shhh is not the machine the reader is sitting
// at: a tool there copies to a clipboard nobody can paste from, and on a
// server with no display there is no tool at all. A terminal that takes a
// clipboard write is asked first, and the copy goes through with nothing on
// PATH to do it.
func TestCopyRow_TheTerminalTakesItWithNoToolOnPath(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)
	m.copyFn = func(string) clipboard.Result {
		t.Error("the terminal takes the copy before any tool is looked for")
		return clipboard.Result{Warning: "no clipboard tool found"}
	}
	m.caps.Clipboard = true
	m.appendEntry(entry{kind: entryCommand, text: "go build", toolResult: "ok"})
	m.viewport.SetLines(m.renderHistoryLines())
	updated, _ = m.Update(readingChord())
	m = updated.(Model)
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = updated.(Model)

	want, ok := clipboard.OSC52("$ go build\nok")
	if !ok {
		t.Fatal("the row's text should fit one clipboard write")
	}
	if got := notifyRaw(t, cmd); got != want {
		t.Errorf("wrote %q, want %q", got, want)
	}
	// The caption stands, and no failure lands in the transcript: the write
	// has no reply, so "it went" is the whole of what can be known.
	if !strings.Contains(m.readingCopied, "copied command") {
		t.Errorf("the rail should caption the copy, got %q", m.readingCopied)
	}
	es := *m.entries()
	if last := es[len(es)-1]; last.kind == entrySystem {
		t.Errorf("a copy that went should say nothing in the transcript, got %q", last.text)
	}
}

// A terminal that never said it takes a write leaves the copy to the tools,
// which is where it was before.
func TestCopyRow_AnUnlistedTerminalLeavesItToTheTools(t *testing.T) {
	var caught []string
	m := copyModel(t, &caught)
	m.appendEntry(entry{kind: entryCommand, text: "go build", toolResult: "ok"})
	m = yank(t, m)
	if len(caught) != 1 {
		t.Fatalf("the tool should have had the copy, got %d", len(caught))
	}
	if m.readingCopied == "" {
		t.Error("the rail should caption the copy")
	}
}
