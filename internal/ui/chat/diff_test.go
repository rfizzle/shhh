package chat

import (
	"fmt"
	"strings"
	"testing"
)

func diffTexts(t *testing.T, diff []diffLine) []string {
	t.Helper()
	out := make([]string, len(diff))
	for i, dl := range diff {
		out[i] = dl.text
	}
	return out
}

func TestUnifiedDiff_IdenticalReturnsNil(t *testing.T) {
	if d := unifiedDiff("a\nb\n", "a\nb\n"); d != nil {
		t.Fatalf("identical texts should produce no diff, got %v", diffTexts(t, d))
	}
	if d := unifiedDiff("", ""); d != nil {
		t.Fatalf("empty texts should produce no diff, got %v", diffTexts(t, d))
	}
}

func TestUnifiedDiff_NewFileIsAllAdditions(t *testing.T) {
	d := unifiedDiff("", "a\nb\n")
	got := diffTexts(t, d)
	want := []string{"@@ -0,0 +1,2 @@", "+a", "+b"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("line %d: got %q, want %q", i, got[i], want[i])
		}
	}
	for _, dl := range d[1:] {
		if dl.kind != diffAdd {
			t.Fatalf("new-file diff should be all additions, got kind %d", dl.kind)
		}
	}
}

func TestUnifiedDiff_DeletionOnly(t *testing.T) {
	d := unifiedDiff("a\nb\n", "a\n")
	got := diffTexts(t, d)
	want := []string{"@@ -1,2 +1,1 @@", " a", "-b"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestUnifiedDiff_ContextBoundsHunk(t *testing.T) {
	var oldSb, newSb strings.Builder
	for i := 1; i <= 10; i++ {
		fmt.Fprintf(&oldSb, "l%d\n", i)
		if i == 5 {
			newSb.WriteString("changed\n")
		} else {
			fmt.Fprintf(&newSb, "l%d\n", i)
		}
	}
	d := unifiedDiff(oldSb.String(), newSb.String())
	got := diffTexts(t, d)
	want := []string{"@@ -2,7 +2,7 @@", " l2", " l3", " l4", "-l5", "+changed", " l6", " l7", " l8"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestUnifiedDiff_SeparateHunks(t *testing.T) {
	var oldSb, newSb strings.Builder
	for i := 1; i <= 20; i++ {
		fmt.Fprintf(&oldSb, "l%d\n", i)
		switch i {
		case 2:
			newSb.WriteString("first\n")
		case 18:
			newSb.WriteString("second\n")
		default:
			fmt.Fprintf(&newSb, "l%d\n", i)
		}
	}
	d := unifiedDiff(oldSb.String(), newSb.String())
	var headers []string
	for _, dl := range d {
		if dl.kind == diffHunk {
			headers = append(headers, dl.text)
		}
	}
	if len(headers) != 2 {
		t.Fatalf("changes far apart should produce two hunks, got %v", headers)
	}
	if headers[0] != "@@ -1,5 +1,5 @@" || headers[1] != "@@ -15,6 +15,6 @@" {
		t.Fatalf("unexpected hunk headers: %v", headers)
	}
}

func TestRenderDiffLines_Truncates(t *testing.T) {
	var diff []diffLine
	for i := 0; i < 20; i++ {
		diff = append(diff, diffLine{diffAdd, fmt.Sprintf("+line %d", i)})
	}
	lines := renderDiffLines(diff, 80, 5)
	if len(lines) != 5 {
		t.Fatalf("expected 5 rendered lines, got %d", len(lines))
	}
	if !strings.Contains(lines[4], "+16 more diff lines") {
		t.Fatalf("last line should be the truncation notice, got %q", lines[4])
	}
}

func TestRenderDiffLines_EmptyShowsNoChanges(t *testing.T) {
	lines := renderDiffLines(nil, 80, 5)
	if len(lines) != 1 || !strings.Contains(lines[0], "no changes") {
		t.Fatalf("empty diff should render a no-changes notice, got %v", lines)
	}
}

func TestClipLine(t *testing.T) {
	if got := clipLine("short", 10); got != "short" {
		t.Fatalf("short lines should pass through, got %q", got)
	}
	if got := clipLine("abcdefghij", 5); got != "abcd…" {
		t.Fatalf("long lines should clip with ellipsis, got %q", got)
	}
}
