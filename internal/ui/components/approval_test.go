package components

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rfizzle/shhh/internal/diff"
)

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEscape}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "space":
		return tea.KeyMsg{Type: tea.KeySpace}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestApprovalCard_CommandVariant(t *testing.T) {
	c := &ApprovalCard{
		Variant:  ApprovalCommand,
		Title:    "Approve command",
		Headline: "Assistant wants to run: go test ./...",
		Question: "Run this command?",
	}
	view := c.View(80)
	if !strings.Contains(view, "Approve command") || !strings.Contains(view, "go test ./...") {
		t.Fatalf("command card should show title and command:\n%s", view)
	}
	if !strings.Contains(view, "[y/N]") || strings.Contains(view, "[y/n/a]") {
		t.Fatalf("without AllowAlways the card must offer [y/N] only:\n%s", view)
	}

	c.AllowAlways = true
	c.AlwaysHint = "a: always allow commands this session"
	view = c.View(80)
	if !strings.Contains(view, "[y/n/a]") || !strings.Contains(view, "always allow commands") {
		t.Fatalf("AllowAlways should offer [y/n/a] with the hint:\n%s", view)
	}
}

func TestApprovalCard_WarningsAndContainment(t *testing.T) {
	c := &ApprovalCard{
		Variant:     ApprovalCommand,
		Title:       "Approve command",
		Headline:    "Assistant wants to run: rm -rf /",
		Warnings:    []string{"deletes files recursively"},
		Containment: "contained · workspace profile",
		Question:    "Run this command?",
	}
	view := c.View(80)
	if !strings.Contains(view, "⚠ deletes files recursively") {
		t.Fatalf("safety warnings should render as ⚠ rows:\n%s", view)
	}
	if !strings.Contains(view, "⛨ contained · workspace profile") {
		t.Fatalf("containment state should render as a ⛨ row:\n%s", view)
	}
}

func TestApprovalCard_EditVariantShowsDiffAndStats(t *testing.T) {
	c := &ApprovalCard{
		Variant:  ApprovalEdit,
		Title:    "Approve edit",
		Headline: "Assistant wants to edit main.go",
		Hunks:    diff.Compute("a\nb\n", "a\nc\nd\n"),
		Question: "Apply this change?",
	}
	view := c.View(80)
	for _, want := range []string{"@@", "-b", "+c", "+d", "+2 −1 · 1 hunk"} {
		if !strings.Contains(view, want) {
			t.Fatalf("edit card should contain %q:\n%s", want, view)
		}
	}
}

func TestApprovalCard_EditVariantBoundsHeight(t *testing.T) {
	var old, new strings.Builder
	for i := 0; i < 100; i++ {
		new.WriteString("line\n")
	}
	c := &ApprovalCard{
		Variant:  ApprovalEdit,
		Title:    "Approve edit",
		Headline: "Assistant wants to write big.txt",
		Hunks:    diff.Compute(old.String(), new.String()),
		Question: "Apply this change?",
		MaxLines: 12,
	}
	view := c.View(80)
	if got := len(strings.Split(view, "\n")); got != 12 {
		t.Fatalf("card should occupy exactly its MaxLines budget, got %d rows", got)
	}
	if !strings.Contains(view, "more diff lines") {
		t.Fatalf("bounded card should note the truncated diff:\n%s", view)
	}
}

func TestApprovalCard_GenericVariant(t *testing.T) {
	c := &ApprovalCard{
		Variant:  ApprovalGeneric,
		Title:    "Approve tool",
		Headline: "Assistant wants to use my_tool",
		Summary:  "do the thing",
		Question: "Allow this?",
	}
	view := c.View(80)
	if !strings.Contains(view, "use my_tool") || !strings.Contains(view, "do the thing") {
		t.Fatalf("generic card should show tool and summary:\n%s", view)
	}
}

func TestApprovalCard_Keys(t *testing.T) {
	c := &ApprovalCard{Question: "Run this command?"}
	cases := []struct {
		key    string
		done   bool
		result any
	}{
		{"y", true, ApprovalApprove},
		{"enter", true, ApprovalApprove},
		{"n", true, ApprovalDeny},
		{"esc", true, ApprovalDeny},
		{"ctrl+c", true, ApprovalDeny},
		{"a", false, nil}, // AllowAlways off: [a] ignored
		{"z", false, nil},
	}
	for _, tc := range cases {
		done, result := c.Update(key(tc.key))
		if done != tc.done || result != tc.result {
			t.Fatalf("key %q: got done=%v result=%v, want done=%v result=%v",
				tc.key, done, result, tc.done, tc.result)
		}
	}

	c.AllowAlways = true
	if done, result := c.Update(key("a")); !done || result != ApprovalAlways {
		t.Fatalf("with AllowAlways, [a] should resolve to ApprovalAlways, got done=%v result=%v", done, result)
	}
}
