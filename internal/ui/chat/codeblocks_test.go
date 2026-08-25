package chat

import (
	"strings"
	"testing"

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

func TestSlashCopy_CopiesLastResponse(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "how do I list files?"},
		{Role: provider.RoleAssistant, Content: "Use this:\n\n```bash\nls -la\n```"},
	}
	m := New(msgs, mockStream)
	var copied string
	m.copyFn = func(s string) clipboard.Result {
		copied = s
		return clipboard.Result{OK: true}
	}

	handled, result := m.handleSlashCommand("/copy")
	if !handled || !strings.Contains(result, "Copied") {
		t.Fatalf("expected copy confirmation, got handled=%v result=%q", handled, result)
	}
	if !strings.Contains(copied, "ls -la") {
		t.Fatalf("expected full response copied, got %q", copied)
	}

	handled, result = m.handleSlashCommand("/copy code")
	if !handled || !strings.Contains(result, "Copied") {
		t.Fatalf("expected copy confirmation, got handled=%v result=%q", handled, result)
	}
	if copied != "ls -la" {
		t.Fatalf("expected only code copied, got %q", copied)
	}
}

func TestSlashCopy_NothingToCopy(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)
	m.copyFn = func(s string) clipboard.Result { return clipboard.Result{OK: true} }

	handled, result := m.handleSlashCommand("/copy")
	if !handled || !strings.Contains(result, "Nothing to copy") {
		t.Fatalf("expected 'Nothing to copy', got handled=%v result=%q", handled, result)
	}
}
