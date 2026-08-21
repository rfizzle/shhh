package diff

import (
	"fmt"
	"strings"
	"testing"
)

// flatten renders hunks the way a unified diff prints them, for compact
// comparisons.
func flatten(t *testing.T, hunks []Hunk) []string {
	t.Helper()
	var out []string
	for _, h := range hunks {
		out = append(out, h.Header())
		for _, l := range h.Lines {
			switch l.Kind {
			case Add:
				out = append(out, "+"+l.Text)
			case Del:
				out = append(out, "-"+l.Text)
			default:
				out = append(out, " "+l.Text)
			}
		}
	}
	return out
}

func TestCompute_IdenticalReturnsNil(t *testing.T) {
	if h := Compute("a\nb\n", "a\nb\n"); h != nil {
		t.Fatalf("identical texts should produce no hunks, got %v", flatten(t, h))
	}
	if h := Compute("", ""); h != nil {
		t.Fatalf("empty texts should produce no hunks, got %v", flatten(t, h))
	}
}

func TestCompute_EmptiedFileIsAllDeletions(t *testing.T) {
	h := Compute("a\nb\n", "")
	got := flatten(t, h)
	want := []string{"@@ -1,2 +0,0 @@", "-a", "-b"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v, want %v", got, want)
	}
	if h[0].Lines[0].OldNo != 1 || h[0].Lines[1].OldNo != 2 {
		t.Fatalf("deleted lines should carry old line numbers, got %+v", h[0].Lines)
	}
}

func TestCompute_NewFileIsAllAdditions(t *testing.T) {
	h := Compute("", "a\nb\n")
	got := flatten(t, h)
	want := []string{"@@ -0,0 +1,2 @@", "+a", "+b"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i, l := range h[0].Lines {
		if l.Kind != Add || l.NewNo != i+1 || l.OldNo != 0 {
			t.Fatalf("new-file line %d should be an addition with a new line number, got %+v", i, l)
		}
	}
}

func TestCompute_DeletionOnly(t *testing.T) {
	h := Compute("a\nb\n", "a\n")
	got := flatten(t, h)
	want := []string{"@@ -1,2 +1,1 @@", " a", "-b"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestCompute_ContextBoundsHunk(t *testing.T) {
	var oldSb, newSb strings.Builder
	for i := 1; i <= 10; i++ {
		fmt.Fprintf(&oldSb, "l%d\n", i)
		if i == 5 {
			newSb.WriteString("changed\n")
		} else {
			fmt.Fprintf(&newSb, "l%d\n", i)
		}
	}
	h := Compute(oldSb.String(), newSb.String())
	got := flatten(t, h)
	want := []string{"@@ -2,7 +2,7 @@", " l2", " l3", " l4", "-l5", "+changed", " l6", " l7", " l8"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// Changes whose context regions touch merge into one hunk; changes farther
// apart stay separate.
func TestCompute_AdjacentHunksMergeSeparateOnesDoNot(t *testing.T) {
	build := func(changed ...int) (string, string) {
		var oldSb, newSb strings.Builder
		for i := 1; i <= 20; i++ {
			fmt.Fprintf(&oldSb, "l%d\n", i)
			isChanged := false
			for _, c := range changed {
				if i == c {
					isChanged = true
				}
			}
			if isChanged {
				fmt.Fprintf(&newSb, "changed%d\n", i)
			} else {
				fmt.Fprintf(&newSb, "l%d\n", i)
			}
		}
		return oldSb.String(), newSb.String()
	}

	// Changes at lines 5 and 9: context regions (2-8 and 6-12) overlap.
	oldText, newText := build(5, 9)
	if h := Compute(oldText, newText); len(h) != 1 {
		t.Fatalf("adjacent changes should merge into one hunk, got %d", len(h))
	}

	// Changes at lines 2 and 18: far apart, two hunks with correct headers.
	oldText, newText = build(2, 18)
	h := Compute(oldText, newText)
	if len(h) != 2 {
		t.Fatalf("distant changes should produce two hunks, got %d", len(h))
	}
	if h[0].Header() != "@@ -1,5 +1,5 @@" || h[1].Header() != "@@ -15,6 +15,6 @@" {
		t.Fatalf("unexpected hunk headers: %q, %q", h[0].Header(), h[1].Header())
	}
}

func TestCompute_LineNumbers(t *testing.T) {
	h := Compute("a\nb\nc\n", "a\nB\nc\n")
	if len(h) != 1 {
		t.Fatalf("expected one hunk, got %d", len(h))
	}
	lines := h[0].Lines
	// " a" (1/1), "-b" (2/-), "+B" (-/2), " c" (3/3)
	if lines[0].OldNo != 1 || lines[0].NewNo != 1 {
		t.Fatalf("context numbering wrong: %+v", lines[0])
	}
	if lines[1].OldNo != 2 || lines[1].NewNo != 0 {
		t.Fatalf("deletion numbering wrong: %+v", lines[1])
	}
	if lines[2].OldNo != 0 || lines[2].NewNo != 2 {
		t.Fatalf("addition numbering wrong: %+v", lines[2])
	}
	if lines[3].OldNo != 3 || lines[3].NewNo != 3 {
		t.Fatalf("trailing context numbering wrong: %+v", lines[3])
	}
}

func TestCompute_IntralineSpans(t *testing.T) {
	h := Compute("return results, nil\n", "return results, ErrRoundLimit\n")
	if len(h) != 1 || len(h[0].Lines) != 2 {
		t.Fatalf("expected one hunk with a del/add pair, got %+v", h)
	}
	del, add := h[0].Lines[0], h[0].Lines[1]
	// Common prefix "return results, " (16 runes); no common suffix ("l" vs
	// "t" at the ends). The changed spans cover "nil" and "ErrRoundLimit".
	if len(del.Emph) != 1 || del.Emph[0] != (Span{Start: 16, End: 19}) {
		t.Fatalf("unexpected del emphasis span: %+v", del.Emph)
	}
	if len(add.Emph) != 1 || add.Emph[0] != (Span{Start: 16, End: 29}) {
		t.Fatalf("unexpected add emphasis span: %+v", add.Emph)
	}
}

func TestCompute_IntralineSuffixAndInsertion(t *testing.T) {
	// Pure insertion inside a line: del has an empty changed middle (no span),
	// add marks just the inserted segment.
	h := Compute("ab\n", "aXb\n")
	del, add := h[0].Lines[0], h[0].Lines[1]
	if len(del.Emph) != 0 {
		t.Fatalf("pure insertion should leave the del line unmarked, got %+v", del.Emph)
	}
	if len(add.Emph) != 1 || add.Emph[0] != (Span{Start: 1, End: 2}) {
		t.Fatalf("expected the inserted rune marked, got %+v", add.Emph)
	}
}

func TestCompute_IntralineNoCommonPartMeansNoSpans(t *testing.T) {
	h := Compute("alpha\n", "zzzzz\n")
	for _, l := range h[0].Lines {
		if len(l.Emph) != 0 {
			t.Fatalf("unrelated lines should carry no emphasis spans, got %+v", l)
		}
	}
}

func TestStats(t *testing.T) {
	h := Compute("a\nb\nc\n", "a\nB\nc\nd\n")
	adds, dels := Stats(h)
	if adds != 2 || dels != 1 {
		t.Fatalf("expected +2 -1, got +%d -%d", adds, dels)
	}
}

func TestCompute_NoTrailingNewline(t *testing.T) {
	h := Compute("a", "a\nb")
	got := flatten(t, h)
	want := []string{"@@ -1,1 +1,2 @@", " a", "+b"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v, want %v", got, want)
	}
}
