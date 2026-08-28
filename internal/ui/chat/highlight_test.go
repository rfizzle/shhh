package chat

import (
	"regexp"
	"strings"
	"testing"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}

func TestRenderMarkdown_FencedBlock(t *testing.T) {
	input := "before\n```bash\necho hello\n```\nafter"
	result := stripANSI(renderMarkdown(input, 80))

	if !strings.Contains(result, "echo hello") {
		t.Fatalf("code block content should be present, got: %s", result)
	}
	if !strings.Contains(result, "before") {
		t.Fatal("text before block should be present")
	}
	if !strings.Contains(result, "after") {
		t.Fatal("text after block should be present")
	}
}

func TestRenderMarkdown_NoLanguage(t *testing.T) {
	input := "text\n```\nls -la\n```\nmore"
	result := stripANSI(renderMarkdown(input, 80))

	if !strings.Contains(result, "ls -la") {
		t.Fatal("code content should be present")
	}
	if !strings.Contains(result, "text") {
		t.Fatal("surrounding text should be present")
	}
}

func TestRenderMarkdown_InlineCode(t *testing.T) {
	input := "use `ls -la` to list files"
	result := stripANSI(renderMarkdown(input, 80))

	if !strings.Contains(result, "ls -la") {
		t.Fatal("inline code content should be present")
	}
}

func TestRenderMarkdown_Bold(t *testing.T) {
	input := "this is **bold** text"
	result := stripANSI(renderMarkdown(input, 80))

	if strings.Contains(result, "**") {
		t.Fatal("asterisks should be stripped from bold text")
	}
	if !strings.Contains(result, "bold") {
		t.Fatal("bold content should be present")
	}
}

func TestRenderMarkdown_Italic(t *testing.T) {
	input := "this is *italic* text"
	result := stripANSI(renderMarkdown(input, 80))

	if !strings.Contains(result, "italic") {
		t.Fatal("italic content should be present")
	}
}

func TestRenderMarkdown_NoMarkdown(t *testing.T) {
	input := "plain text with no formatting at all"
	result := stripANSI(renderMarkdown(input, 80))

	if !strings.Contains(result, "plain text with no formatting at all") {
		t.Fatalf("plain text should pass through, got: %s", result)
	}
}

func TestRenderMarkdown_MultipleFences(t *testing.T) {
	input := "first:\n```\ncmd1\n```\nsecond:\n```\ncmd2\n```"
	result := stripANSI(renderMarkdown(input, 80))

	if !strings.Contains(result, "cmd1") {
		t.Fatal("first block content should be present")
	}
	if !strings.Contains(result, "cmd2") {
		t.Fatal("second block content should be present")
	}
}

func TestDiffSyntax_GoFile(t *testing.T) {
	syntax := diffSyntax("internal/agent/loop.go")
	if syntax == nil {
		t.Fatal("a .go path should get a highlighter")
	}
	line := "func main() {\treturn}"
	segs := syntax(line)
	if segs == nil {
		t.Fatal("expected segments for a Go line")
	}
	var b strings.Builder
	for _, s := range segs {
		b.WriteString(s.Text)
	}
	if b.String() != line {
		t.Fatalf("segments must reconstruct the line exactly: %q != %q", b.String(), line)
	}
	colored := false
	for _, s := range segs {
		if s.Color != "" {
			colored = true
		}
	}
	if !colored {
		t.Fatal("a Go keyword line should get at least one colored segment")
	}
}

func TestDiffSyntax_UnknownExtension(t *testing.T) {
	if syntax := diffSyntax("notes.unknownext"); syntax != nil {
		t.Fatal("an unrecognized extension should disable highlighting")
	}
}

func TestMatchLexer_Memoized(t *testing.T) {
	first := matchLexer("loop.go")
	if first == nil {
		t.Fatal("a .go basename should match a lexer")
	}
	if second := matchLexer("loop.go"); second != first {
		t.Fatal("a repeat lookup should return the cached lexer, not a new one")
	}
	if _, ok := lexerCache.Load("loop.go"); !ok {
		t.Fatal("the hit should be cached")
	}
}

func TestMatchLexer_CachesMisses(t *testing.T) {
	if lexer := matchLexer("notes.unknownext"); lexer != nil {
		t.Fatal("an unrecognized extension should not match a lexer")
	}
	// A miss costs the same full-registry walk as a hit, so it is cached too.
	v, ok := lexerCache.Load("notes.unknownext")
	if !ok {
		t.Fatal("the miss should be cached")
	}
	if v != nil {
		t.Fatalf("a cached miss should be nil, got %v", v)
	}
	if lexer := matchLexer("notes.unknownext"); lexer != nil {
		t.Fatal("a repeat lookup of a miss should still be nil")
	}
}
