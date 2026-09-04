package chat

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/clipboard"
	"github.com/rfizzle/shhh/internal/provider"
)

func TestExtractCodeBlocks(t *testing.T) {
	text := "Here you go:\n\n```bash\nls -la\n```\n\nand also:\n\n```\necho hi\necho bye\n```\ndone"
	blocks := extractCodeBlocks(text)
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d: %v", len(blocks), blocks)
	}
	if blocks[0] != "ls -la" {
		t.Errorf("expected 'ls -la', got %q", blocks[0])
	}
	if blocks[1] != "echo hi\necho bye" {
		t.Errorf("expected multi-line block, got %q", blocks[1])
	}
}

func TestExtractCodeBlocks_None(t *testing.T) {
	if blocks := extractCodeBlocks("no code here"); len(blocks) != 0 {
		t.Fatalf("expected no blocks, got %v", blocks)
	}
}

func TestExtractCodeBlockInfo_LanguageTags(t *testing.T) {
	text := "```bash\nls -la\n```\n```\necho hi\n```\n~~~python title=x\nprint(1)\n~~~"
	blocks := extractCodeBlockInfo(text)
	if len(blocks) != 3 {
		t.Fatalf("expected 3 blocks, got %d: %v", len(blocks), blocks)
	}
	want := []codeBlock{
		{lang: "bash", body: "ls -la"},
		{lang: "", body: "echo hi"},
		{lang: "python", body: "print(1)"},
	}
	for i, w := range want {
		if blocks[i] != w {
			t.Errorf("block %d: got %+v, want %+v", i, blocks[i], w)
		}
	}
}

func TestBlockHeadAndLines(t *testing.T) {
	cases := []struct {
		body string
		head string
		n    int
	}{
		{"", "", 0},
		{"\n\n", "", 0},
		{"ls -la", "ls -la", 1},
		{"  \n  echo hi\necho bye\n", "echo hi", 3},
	}
	for _, c := range cases {
		if got := blockHead(c.body); got != c.head {
			t.Errorf("blockHead(%q) = %q, want %q", c.body, got, c.head)
		}
		if got := blockLines(c.body); got != c.n {
			t.Errorf("blockLines(%q) = %d, want %d", c.body, got, c.n)
		}
	}
}

func TestExtractCodeBlocks_UnclosedFence(t *testing.T) {
	blocks := extractCodeBlocks("```bash\nls -la")
	if len(blocks) != 0 {
		t.Fatalf("unclosed fence should yield no blocks, got %v", blocks)
	}
}

// copyCommandModel is a sized session whose clipboard records what reached
// it. The size matters: /copy answers in the transcript, which is rendered.
func copyCommandModel(t *testing.T, msgs []provider.Message, copied *string) Model {
	t.Helper()
	m := New(msgs, mockStream)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)
	m.copyFn = func(s string) clipboard.Result {
		*copied = s
		return clipboard.Result{OK: true}
	}
	return m
}

// runCopy types /copy and hands back the session and whatever command the
// copy left for the program to run.
func runCopy(t *testing.T, m Model, text string) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.runCommand(text, "/copy")
	return next.(Model), cmd
}

func TestSlashCopy_CopiesLastResponse(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "how do I list files?"},
		{Role: provider.RoleAssistant, Content: "Use this:\n\n```bash\nls -la\n```"},
	}
	var copied string
	m := copyCommandModel(t, msgs, &copied)

	m, _ = runCopy(t, m, "/copy")
	if note := lastSystemText(m); !strings.Contains(note, "Copied") {
		t.Fatalf("expected copy confirmation, got %q", note)
	}
	if !strings.Contains(copied, "ls -la") {
		t.Fatalf("expected full response copied, got %q", copied)
	}

	m, _ = runCopy(t, m, "/copy code")
	if note := lastSystemText(m); !strings.Contains(note, "Copied") {
		t.Fatalf("expected copy confirmation, got %q", note)
	}
	if copied != "ls -la" {
		t.Fatalf("expected only code copied, got %q", copied)
	}
}

func TestSlashCopy_NothingToCopy(t *testing.T) {
	var copied string
	m := copyCommandModel(t, []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, &copied)

	m, _ = runCopy(t, m, "/copy")
	if note := lastSystemText(m); !strings.Contains(note, "Nothing to copy") {
		t.Fatalf("expected 'Nothing to copy', got %q", note)
	}
}

// Over ssh a tool on this machine copies to a clipboard nobody can paste
// from, and on a server with no display there is no tool at all. /copy asks
// the terminal first, so the copy goes through with nothing on PATH to do it
// — the same path the copy key takes.
func TestSlashCopy_TheTerminalTakesItWithNoToolOnPath(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "how do I list files?"},
		{Role: provider.RoleAssistant, Content: "ls -la"},
	}
	m := New(msgs, mockStream)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)
	m.copyFn = func(string) clipboard.Result {
		t.Error("the terminal takes the copy before any tool is looked for")
		return clipboard.Result{Warning: "no clipboard tool found"}
	}
	m.caps.Clipboard = true

	m, cmd := runCopy(t, m, "/copy")

	want, ok := clipboard.OSC52("ls -la")
	if !ok {
		t.Fatal("the response should fit one clipboard write")
	}
	if got := notifyRaw(t, cmd); got != want {
		t.Errorf("wrote %q, want %q", got, want)
	}
	if note := lastSystemText(m); !strings.Contains(note, "Copied") {
		t.Fatalf("expected copy confirmation, got %q", note)
	}
}

// A session built without a clipboard at all says so rather than panicking
// on a copy function that is not there.
func TestSlashCopy_NoClipboardInThisSession(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleAssistant, Content: "ls -la"},
	}
	m := New(msgs, mockStream)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)
	m.copyFn = nil

	m, cmd := runCopy(t, m, "/copy")
	if cmd != nil {
		t.Fatal("no clipboard means nothing to write")
	}
	if note := lastSystemText(m); !strings.Contains(note, "not available in this session") {
		t.Fatalf("expected the unavailable notice, got %q", note)
	}
}
