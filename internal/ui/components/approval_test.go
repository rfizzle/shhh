package components

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/diff"
)

func key(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	case "ctrl+u":
		return tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	}
	return tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
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
		SafeDefault: "[n] deny — the safe answer",
		Footnote:    "[a] always — not offered: a safety-flagged command is never pre-approved",
	}
	lines := strings.Split(ansi.Strip(c.View(90)), "\n")
	view := strings.Join(lines, "\n")
	for _, want := range []string{
		"touches   ./dist — 412 files, 84.0 MB",
		"undo      none — rm bypasses the changeset",
		"network   open — the workspace profile allows it",
		"[n] deny — the safe answer",
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
	// The diff body carries line numbers (S-074,
	// docs/interface/surfaces.md#the-approval-card), and the reversibility line
	// rides the stats row (S-101).
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
	done, result := c.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	if !done || result != ApprovalFullDiff {
		t.Fatalf("d should request the full diff, got done=%v result=%v", done, result)
	}

	// Without FullDiff, d is unrecognized and the card keeps waiting.
	c.FullDiff = false
	if done, _ := c.Update(tea.KeyPressMsg{Code: 'd', Text: "d"}); done {
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

// --- click targets (S-159, §7e) -------------------------------------------

// runRow is the rendered row carrying the card's decision run.
func runRow(t *testing.T, c *ApprovalCard, width int) string {
	t.Helper()
	for _, line := range strings.Split(c.View(width), "\n") {
		if strings.Contains(ansi.Strip(line), c.keys()) {
			return line
		}
	}
	t.Fatalf("no rendered row carries %s", c.keys())
	return ""
}

// Every cell of the run belongs to a key — the brackets to the keys at the
// ends, each separator to the key before it — because one cell is not a
// target and a press between two keys should mean the one it is standing on.
func TestApprovalCard_EveryCellOfTheRunIsAKey(t *testing.T) {
	c := &ApprovalCard{
		Variant: ApprovalCommand, Title: "Approve command",
		Headline: "Assistant wants to run: go test ./...",
		Question: "Run this command?", AllowAlways: true,
		AlwaysHint: `a: always allow "go test"`,
	}
	row := runRow(t, c, 80)
	plain := ansi.Strip(row)
	at := strings.Index(plain, c.keys())
	start := ansi.StringWidth(plain[:at])
	// [ y / n / a ] — the opening bracket and each separator go to the key
	// before them, the closing bracket to the last.
	want := []string{"y", "y", "y", "n", "n", "a", "a"}
	for i, key := range want {
		got, ok := c.KeyAt(row, start+i)
		if !ok || got != key {
			t.Fatalf("cell %d of %s should be %q, got %q (found=%v)", i, c.keys(), key, got, ok)
		}
	}
	if _, ok := c.KeyAt(row, start+len(want)); ok {
		t.Fatal("the cell past the run belongs to no key")
	}
	if _, ok := c.KeyAt(row, start-1); ok {
		t.Fatal("the cell before the run belongs to no key")
	}
}

// The safe answer is drawn as a capital N — §2's default marker, not a
// shifted key — so the cell has to resolve to the keystroke the card answers.
func TestApprovalCard_TheDefaultMarkerIsNotAKey(t *testing.T) {
	c := &ApprovalCard{
		Variant: ApprovalCommand, Title: "Approve command",
		Headline: "Assistant wants to run: go test ./...",
		Question: "Run this command?",
	}
	if c.keys() != "[y/N]" {
		t.Fatalf("expected the default marker on the safe answer, got %s", c.keys())
	}
	row := runRow(t, c, 80)
	plain := ansi.Strip(row)
	start := ansi.StringWidth(plain[:strings.Index(plain, c.keys())])
	// [ y / N ]
	got, ok := c.KeyAt(row, start+3)
	if !ok || got != "n" {
		t.Fatalf("the capital N should answer as n, got %q (found=%v)", got, ok)
	}
	done, result := c.Update(key(got))
	if !done || result != ApprovalDeny {
		t.Fatalf("the key the cell named should deny, got %v/%v", done, result)
	}
}

// A row that does not carry the run carries no target: the geometry is read
// out of the render, so a key a narrow terminal clipped away is not clickable.
func TestApprovalCard_ARowWithoutTheRunHasNoKeys(t *testing.T) {
	c := &ApprovalCard{
		Variant: ApprovalCommand, Title: "Approve command",
		Headline: "Assistant wants to run: go test ./...",
		Question: "Run this command?",
	}
	if _, ok := c.KeyAt("Assistant wants to run: go test ./...", 4); ok {
		t.Fatal("a body row should carry no decision key")
	}
}

// A card holding the keyboard by arrival claims two keys, so those are the
// only two cells a pointer can land on — [a] and [d] still want the handover.
func TestApprovalCard_HeldOnArrivalOffersOnlyItsTwoKeys(t *testing.T) {
	c := &ApprovalCard{
		Variant: ApprovalCommand, Title: "Approve command",
		Headline:    "Assistant wants to run: go test ./...",
		Question:    "Run this command?",
		AllowAlways: true, AlwaysHint: `a: always allow "go test"`,
		FullDiff: true, HeldOnArrival: true, Handover: "ctrl+g",
	}
	run := c.KeyRun()
	if len(run) != 2 || run[0].Key != "y" || run[1].Key != "n" {
		t.Fatalf("an arrival card should draw y and n alone, got %+v", run)
	}
	row := runRow(t, c, 80)
	for _, absent := range []string{"a", "d"} {
		for col := range ansi.StringWidth(ansi.Strip(row)) {
			if k, ok := c.KeyAt(row, col); ok && k == absent {
				t.Fatalf("an arrival card must offer no cell for %q", absent)
			}
		}
	}
}
