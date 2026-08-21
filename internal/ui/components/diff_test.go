package components

import (
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/diff"
)

func sampleHunks(t *testing.T) []diff.Hunk {
	t.Helper()
	return diff.Compute("a\nb\nc\n", "a\nB\nc\n")
}

func TestUnifiedLines_Basics(t *testing.T) {
	lines := UnifiedLines(sampleHunks(t), 80, UnifiedOpts{})
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"@@ -1,3 +1,3 @@", "-b", "+B", " a"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("unified rendering should contain %q:\n%s", want, joined)
		}
	}
}

func TestUnifiedLines_LineNumbers(t *testing.T) {
	lines := UnifiedLines(sampleHunks(t), 80, UnifiedOpts{LineNumbers: true})
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "- 2  b") || !strings.Contains(joined, "+ 2  B") {
		t.Fatalf("numbered rendering should carry old/new line numbers:\n%s", joined)
	}
}

func TestUnifiedLines_TruncatesWithNotice(t *testing.T) {
	hunks := diff.Compute("", strings.Repeat("x\n", 20))
	lines := UnifiedLines(hunks, 80, UnifiedOpts{MaxLines: 5})
	if len(lines) != 5 {
		t.Fatalf("expected 5 rendered lines, got %d", len(lines))
	}
	// 21 total rows (header + 20 adds), 4 kept → 17 dropped.
	if !strings.Contains(lines[4], "+17 more diff lines") {
		t.Fatalf("last line should be the truncation notice, got %q", lines[4])
	}
}

func TestUnifiedLines_EmptyShowsNoChanges(t *testing.T) {
	lines := UnifiedLines(nil, 80, UnifiedOpts{})
	if len(lines) != 1 || !strings.Contains(lines[0], "no changes") {
		t.Fatalf("empty diff should render a no-changes notice, got %v", lines)
	}
}

func TestDiffView_RowView(t *testing.T) {
	v := &DiffView{Path: "main.go", Verb: "edit", Hunks: sampleHunks(t)}
	row := v.RowView(80)
	for _, want := range []string{"✎ edit", "main.go", "+1 −1 · 1 hunk", "[enter] expand"} {
		if !strings.Contains(row, want) {
			t.Fatalf("collapsed row should contain %q, got %q", want, row)
		}
	}
	if strings.Contains(row, "\n") {
		t.Fatalf("collapsed row must be a single line, got %q", row)
	}
}

func TestDiffView_ModeCycleAndEsc(t *testing.T) {
	v := &DiffView{Path: "main.go", Hunks: sampleHunks(t), Height: 10}
	if done, _ := v.Update(key("enter")); done || v.Mode != DiffExpanded {
		t.Fatalf("enter should expand, got mode %d", v.Mode)
	}
	if done, _ := v.Update(key("enter")); done || v.Mode != DiffFull {
		t.Fatalf("second enter should open the full view, got mode %d", v.Mode)
	}
	if done, _ := v.Update(key("esc")); done || v.Mode != DiffExpanded {
		t.Fatalf("esc from full view should step back to expanded, got mode %d", v.Mode)
	}
	if done, _ := v.Update(key("esc")); !done {
		t.Fatal("esc from expanded should dismiss the viewer")
	}
}

func TestDiffView_FullViewScrollAndToggle(t *testing.T) {
	hunks := diff.Compute("", strings.Repeat("x\n", 30))
	v := &DiffView{Path: "big.txt", Hunks: hunks, Mode: DiffFull, Height: 10}

	view := v.View(80)
	if !strings.Contains(view, "j/k scroll") {
		t.Fatalf("full view should show its key hints:\n%s", view)
	}
	if got := len(strings.Split(view, "\n")); got != 10 {
		t.Fatalf("full view should occupy its Height budget, got %d rows", got)
	}

	v.Update(key("j"))
	if v.Offset != 1 {
		t.Fatalf("j should scroll down, offset %d", v.Offset)
	}
	v.Update(key("k"))
	v.Update(key("k"))
	if v.Offset != 0 {
		t.Fatalf("k should clamp at the top, offset %d", v.Offset)
	}

	v.Update(key("s"))
	if !v.SideBySide {
		t.Fatal("s should toggle side-by-side")
	}
}

func TestDiffView_SideBySideAtWideWidth(t *testing.T) {
	v := &DiffView{Path: "main.go", Hunks: sampleHunks(t), Mode: DiffFull, Height: 12}
	view := v.View(140)
	if !strings.Contains(view, "│") {
		t.Fatalf("wide full view should render side-by-side panes:\n%s", view)
	}
	// The changed pair shares one row: old-side b and new-side B.
	found := false
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "b") && strings.Contains(line, "B") && strings.Contains(line, "│") {
			found = true
		}
	}
	if !found {
		t.Fatalf("del/add pair should align on one side-by-side row:\n%s", view)
	}
}

func TestDiffView_HunkJump(t *testing.T) {
	var oldSb, newSb strings.Builder
	for i := 0; i < 30; i++ {
		oldSb.WriteString("l\n")
		if i == 2 || i == 25 {
			newSb.WriteString("changed\n")
		} else {
			newSb.WriteString("l\n")
		}
	}
	hunks := diff.Compute(oldSb.String(), newSb.String())
	if len(hunks) != 2 {
		t.Fatalf("test setup expects 2 hunks, got %d", len(hunks))
	}
	v := &DiffView{Hunks: hunks, Mode: DiffFull, Height: 8}
	v.Update(key("n"))
	if v.Offset == 0 {
		t.Fatal("n should jump to the next hunk")
	}
	v.Update(key("p"))
	if v.Offset != 0 {
		t.Fatalf("p should jump back to the first hunk, offset %d", v.Offset)
	}
}

func TestUnifiedLines_IntralineEmphasisKeepsText(t *testing.T) {
	hunks := diff.Compute("return nil\n", "return err\n")
	lines := UnifiedLines(hunks, 80, UnifiedOpts{Emphasis: true})
	joined := stripANSI(strings.Join(lines, "\n"))
	if !strings.Contains(joined, "-return nil") || !strings.Contains(joined, "+return err") {
		t.Fatalf("emphasized rendering must preserve the full text:\n%s", joined)
	}
}

// stripANSI removes escape sequences so tests can assert on plain text.
func stripANSI(s string) string {
	var b strings.Builder
	inSeq := false
	for _, r := range s {
		switch {
		case inSeq:
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inSeq = false
			}
		case r == '\x1b':
			inSeq = true
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
