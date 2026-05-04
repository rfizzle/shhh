package chat

import (
	"strings"
	"testing"
)

func TestHighlightCode_FencedBlock(t *testing.T) {
	input := "before\n```bash\necho hello\n```\nafter"
	result := highlightCode(input)

	if !strings.Contains(result, "echo hello") {
		t.Fatal("code block content should be present")
	}
	if !strings.Contains(result, "bash") {
		t.Fatal("language label should be present")
	}
	if !strings.Contains(result, "before") {
		t.Fatal("text before block should be present")
	}
	if !strings.Contains(result, "after") {
		t.Fatal("text after block should be present")
	}
}

func TestHighlightCode_NoLanguage(t *testing.T) {
	input := "text\n```\nls -la\n```\nmore"
	result := highlightCode(input)

	if !strings.Contains(result, "ls -la") {
		t.Fatal("code content should be present")
	}
	if !strings.Contains(result, "text") {
		t.Fatal("surrounding text should be present")
	}
}

func TestHighlightCode_UnclosedFence(t *testing.T) {
	input := "start\n```python\nprint('hi')\nmore code"
	result := highlightCode(input)

	if !strings.Contains(result, "print('hi')") {
		t.Fatal("unclosed fence content should still render")
	}
	if !strings.Contains(result, "python") {
		t.Fatal("language label should render for unclosed fence")
	}
}

func TestHighlightCode_InlineCode(t *testing.T) {
	input := "use `ls -la` to list files"
	result := highlightCode(input)

	if !strings.Contains(result, "ls -la") {
		t.Fatal("inline code content should be present")
	}
	if strings.Contains(result, "`") {
		t.Fatal("backticks should be stripped from output")
	}
}

func TestHighlightCode_MultipleInline(t *testing.T) {
	input := "run `cd /tmp` then `ls`"
	result := highlightCode(input)

	if !strings.Contains(result, "cd /tmp") {
		t.Fatal("first inline code should be present")
	}
	if !strings.Contains(result, "ls") {
		t.Fatal("second inline code should be present")
	}
}

func TestHighlightCode_NoCode(t *testing.T) {
	input := "plain text with no code at all"
	result := highlightCode(input)

	if !strings.Contains(result, "plain text with no code at all") {
		t.Fatalf("plain text should pass through unchanged, got: %s", result)
	}
}

func TestHighlightCode_MultipleFences(t *testing.T) {
	input := "first:\n```\ncmd1\n```\nsecond:\n```\ncmd2\n```"
	result := highlightCode(input)

	if !strings.Contains(result, "cmd1") {
		t.Fatal("first block content should be present")
	}
	if !strings.Contains(result, "cmd2") {
		t.Fatal("second block content should be present")
	}
}

func TestHighlightInlineCode_UnmatchedBacktick(t *testing.T) {
	input := "some `unclosed code"
	result := highlightInlineCode(input)

	if result != input {
		t.Fatalf("unmatched backtick should leave text unchanged, got: %s", result)
	}
}
