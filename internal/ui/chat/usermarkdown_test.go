package chat

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/provider"
)

// A sent message is markdown in the transcript, the same as the model's. The
// draft is not (it stays a plain editor), so this is about what happens after
// enter, not before it.
func TestUserEntry_RendersMarkdown(t *testing.T) {
	m := New([]provider.Message{}, mockStream)
	const src = "use the `--flag` and **mean** it"
	row := m.renderEntry(entry{kind: entryUser, text: src}, 60)

	if strings.Contains(ansi.Strip(row), "`--flag`") {
		t.Errorf("backticks survived, so the row is still plain text:\n%s", row)
	}
	if !strings.Contains(ansi.Strip(row), "--flag") {
		t.Errorf("the code span lost its content:\n%s", row)
	}
	// The label is the row's own, not the renderer's.
	if !strings.Contains(ansi.Strip(row), "You") {
		t.Errorf("no You label:\n%s", row)
	}
}

// A fenced block in a sent message keeps its lines, rather than being reflowed
// into the prose around it.
func TestUserEntry_KeepsAFencedBlock(t *testing.T) {
	m := New([]provider.Message{}, mockStream)
	row := ansi.Strip(m.renderEntry(entry{kind: entryUser, text: "look:\n\n```go\nx := 1\ny := 2\n```"}, 60))
	if strings.Contains(row, "```") {
		t.Errorf("fence markers survived:\n%s", row)
	}
	if !strings.Contains(row, "x := 1") || !strings.Contains(row, "y := 2") {
		t.Errorf("code lines missing:\n%s", row)
	}
}
