package components

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
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

func TestApprovalCard_Warnings(t *testing.T) {
	c := &ApprovalCard{
		Variant:  ApprovalCommand,
		Title:    "Approve command",
		Headline: "Assistant wants to run: rm -rf /",
		Warnings: []string{"deletes files recursively"},
		Question: "Run this command?",
	}
	view := c.View(80)
	if !strings.Contains(view, "⚠ deletes files recursively") {
		t.Fatalf("safety warnings should render as ⚠ rows:\n%s", view)
	}
}

// Severity leads the card as a word and rides the border as a chip, so a
// reader who cannot see the border colour loses nothing (S-101).
func TestApprovalCard_SeverityIsAWordNotOnlyAColour(t *testing.T) {
	c := &ApprovalCard{
		Variant:  ApprovalCommand,
		Title:    "Approve command",
		Headline: "Assistant wants to run: rm -rf ./dist",
		Severity: SeverityHigh,
		Warnings: []string{"deletes files recursively (rm -rf)"},
		Question: "Run this command?",
	}
	view := ansi.Strip(c.View(90))
	if strings.Count(view, "⚠ HIGH") != 2 {
		t.Fatalf("severity should lead the body and ride the title rail:\n%s", view)
	}
	if !strings.Contains(view, "⚠ HIGH  deletes files recursively (rm -rf)") {
		t.Fatalf("the severity word should lead the first risk:\n%s", view)
	}
	c.Severity = SeverityLow
	if !strings.Contains(ansi.Strip(c.View(90)), "⚠ low") {
		t.Fatal("a low-severity card still states its level")
	}
}

// The containment state folds into the title rail; an uncontained action
// promotes ⚠ UNCONTAINED there instead (S-101).
func TestApprovalCard_ContainmentChip(t *testing.T) {
	c := &ApprovalCard{
		Variant:  ApprovalCommand,
		Title:    "Approve command",
		Headline: "Assistant wants to run: go build ./...",
		Severity: SeverityLow,
		Chip:     "⛨ bwrap · workspace",
		Question: "Run this command?",
	}
	top := strings.SplitN(ansi.Strip(c.View(100)), "\n", 2)[0]
	if !strings.Contains(top, "⛨ bwrap · workspace") || !strings.Contains(top, "⚠ low") {
		t.Fatalf("the title rail should carry the containment chip and the severity: %q", top)
	}

	c.Chip, c.Uncontained = "", true
	top = strings.SplitN(ansi.Strip(c.View(100)), "\n", 2)[0]
	if !strings.Contains(top, "⚠ UNCONTAINED") {
		t.Fatalf("an uncontained action promotes ⚠ UNCONTAINED into the title: %q", top)
	}

	// A terminal too narrow for both sheds the containment chip first; the
	// severity is what the decision turns on and it survives.
	c.Chip, c.Uncontained = "⛨ bwrap · workspace", false
	top = strings.SplitN(ansi.Strip(c.View(46)), "\n", 2)[0]
	if strings.Contains(top, "bwrap") || !strings.Contains(top, "⚠ low") {
		t.Fatalf("narrow rail should drop the containment chip and keep the severity: %q", top)
	}
}

// The blast-radius block states what the action touches before the keys, and
// the keys sit below a rule so they never blend into it (S-101).
func TestApprovalCard_BlastRadiusBlockAndRule(t *testing.T) {
	c := &ApprovalCard{
		Variant:  ApprovalCommand,
		Title:    "Approve command",
		Headline: "Assistant wants to run: rm -rf ./dist",
		Severity: SeverityHigh,
		Fields: []CardField{
			{Label: "touches", Value: "./dist", Detail: "412 files, 84.0 MB"},
			{Label: "undo", Value: "none", Detail: "rm bypasses the changeset", Tone: ToneRisk},
			{Label: "network", Value: "open", Detail: "the workspace profile allows it", Tone: ToneOpen},
		},
		Question:    "Run this command?",
		SafeDefault: "esc — the safe answer",
		Footnote:    "[a] always — not offered: a safety-flagged command is never pre-approved",
	}
	lines := strings.Split(ansi.Strip(c.View(90)), "\n")
	view := strings.Join(lines, "\n")
	for _, want := range []string{
		"touches   ./dist — 412 files, 84.0 MB",
		"undo      none — rm bypasses the changeset",
		"network   open — the workspace profile allows it",
		"esc — the safe answer",
		"[a] always — not offered",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("card should contain %q:\n%s", want, view)
		}
	}
	rule, keys := -1, -1
	for i, line := range lines {
		if strings.HasPrefix(line, "├") {
			rule = i
		}
		if strings.Contains(line, "Run this command?") {
			keys = i
		}
	}
	if rule < 0 || keys < 0 || rule >= keys {
		t.Fatalf("the keys must sit below a horizontal rule (rule=%d keys=%d):\n%s", rule, keys, view)
	}
}

// A detail that cannot fit is dropped, leaving a whole statement rather than
// half of one.
func TestApprovalCard_FieldDropsDetailBeforeClipping(t *testing.T) {
	c := &ApprovalCard{
		Variant:  ApprovalCommand,
		Title:    "Approve command",
		Headline: "Assistant wants to run: rm -rf ./dist",
		Fields:   []CardField{{Label: "touches", Value: "./dist", Detail: strings.Repeat("very long detail ", 8)}},
		Question: "Run this command?",
	}
	view := ansi.Strip(c.View(44))
	if !strings.Contains(view, "touches   ./dist") {
		t.Fatalf("the value should survive a narrow card:\n%s", view)
	}
	if strings.Contains(view, "very long") {
		t.Fatalf("a detail that does not fit is dropped, not clipped:\n%s", view)
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
	// The diff body carries line numbers (S-074, DESIGN-TUI.md §2c), and the
	// reversibility line rides the stats row (S-101).
	c.Reversibility = "undo yes — recorded, and git has this file"
	view := c.View(80)
	for _, want := range []string{"@@", "- 2  b", "+ 2  c", "+ 3  d",
		"+2 −1 · 1 hunk · undo yes — recorded, and git has this file"} {
		if !strings.Contains(view, want) {
			t.Fatalf("edit card should contain %q:\n%s", want, view)
		}
	}
}

func TestApprovalCard_FullDiffKey(t *testing.T) {
	c := &ApprovalCard{
		Variant:  ApprovalEdit,
		Title:    "Approve edit",
		Hunks:    diff.Compute("a\n", "b\n"),
		Question: "Apply this change?",
		FullDiff: true,
	}
	if !strings.Contains(c.View(80), "d: full diff") {
		t.Fatal("card should hint the full-diff key when FullDiff is set")
	}
	done, result := c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if !done || result != ApprovalFullDiff {
		t.Fatalf("d should request the full diff, got done=%v result=%v", done, result)
	}

	// Without FullDiff, d is unrecognized and the card keeps waiting.
	c.FullDiff = false
	if done, _ := c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}}); done {
		t.Fatal("d should be ignored when FullDiff is off")
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
