package diff

import (
	"fmt"
	"math/rand"
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

// numbered builds n lines of the form "line 1".
func numbered(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("line %d", i+1)
	}
	return out
}

func joined(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// A ten-line append to a five-thousand-line file is one hunk at the end, not
// a whole-file replacement: the search only ever sees the ten lines that
// differ, because the five thousand common ones are trimmed before it runs.
func TestCompute_AppendToLargeFileIsOneHunk(t *testing.T) {
	old := numbered(5000)
	added := make([]string, 10)
	for i := range added {
		added[i] = fmt.Sprintf("appended %d", i+1)
	}
	h := Compute(joined(old), joined(append(append([]string{}, old...), added...)))

	if len(h) != 1 {
		t.Fatalf("an append should produce one hunk, got %d", len(h))
	}
	want := []string{
		"@@ -4998,3 +4998,13 @@",
		" line 4998", " line 4999", " line 5000",
	}
	for _, l := range added {
		want = append(want, "+"+l)
	}
	if got := flatten(t, h); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v, want %v", got, want)
	}
	if adds, dels := Stats(h); adds != 10 || dels != 0 {
		t.Fatalf("expected +10 -0, got +%d -%d", adds, dels)
	}
}

// One changed line in the middle of a five-thousand-line file is one small
// hunk with its context and its intraline emphasis intact.
func TestCompute_LocalisedEditInLargeFileIsOneHunk(t *testing.T) {
	old := numbered(5000)
	edited := append([]string{}, old...)
	edited[2499] = "code 2500"
	h := Compute(joined(old), joined(edited))

	if len(h) != 1 {
		t.Fatalf("a localised edit should produce one hunk, got %d", len(h))
	}
	want := []string{
		"@@ -2497,7 +2497,7 @@",
		" line 2497", " line 2498", " line 2499",
		"-line 2500", "+code 2500",
		" line 2501", " line 2502", " line 2503",
	}
	if got := flatten(t, h); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v, want %v", got, want)
	}
	if adds, dels := Stats(h); adds != 1 || dels != 1 {
		t.Fatalf("expected +1 -1, got +%d -%d", adds, dels)
	}
	// "line"/"code" differ up to the shared "e 2500" tail.
	del, add := h[0].Lines[3], h[0].Lines[4]
	if len(del.Emph) != 1 || del.Emph[0] != (Span{Start: 0, End: 3}) {
		t.Fatalf("unexpected del emphasis span: %+v", del.Emph)
	}
	if len(add.Emph) != 1 || add.Emph[0] != (Span{Start: 0, End: 3}) {
		t.Fatalf("unexpected add emphasis span: %+v", add.Emph)
	}
}

// A reversed file has an edit distance near twice its length, far past
// maxEdits. The result is coarser than the minimum, but it is still a
// correct script and it still arrives.
func TestCompute_PastTheEditBoundDegradesToReplacement(t *testing.T) {
	const n = 5000
	old := numbered(n)
	reversed := make([]string, n)
	for i, l := range old {
		reversed[n-1-i] = l
	}
	h := Compute(joined(old), joined(reversed))

	if len(h) != 1 {
		t.Fatalf("a wholesale replacement should be one hunk, got %d", len(h))
	}
	if adds, dels := Stats(h); adds != n || dels != n {
		t.Fatalf("expected +%d -%d, got +%d -%d", n, n, adds, dels)
	}
	for i, l := range h[0].Lines {
		want := Del
		if i >= n {
			want = Add
		}
		if l.Kind != want {
			t.Fatalf("line %d: deletions should all precede additions, got %+v", i, l)
		}
	}
}

// applyOps replays an edit script into the two texts it claims to describe.
// A script that does not reproduce both is wrong however few edits it uses,
// and a wrong diff is worse than a coarse one.
func applyOps(ops []op) (oldLines, newLines []string) {
	for _, o := range ops {
		switch o.kind {
		case Context:
			oldLines = append(oldLines, o.text)
			newLines = append(newLines, o.text)
		case Del:
			oldLines = append(oldLines, o.text)
		case Add:
			newLines = append(newLines, o.text)
		}
	}
	return oldLines, newLines
}

// lcsLen is the quadratic reference the production code no longer keeps: the
// length of the longest common subsequence, which fixes the fewest edits any
// correct script can use.
func lcsLen(a, b []string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				cur[j] = prev[j+1] + 1
			} else {
				cur[j] = max(prev[j], cur[j+1])
			}
		}
		prev, cur = cur, prev
		clear(cur)
	}
	return prev[0]
}

// Random line sequences over a tiny alphabet, so common lines repeat and the
// search has real choices to make. Every script must replay into its inputs,
// use the fewest possible edits, and keep each change's deletions ahead of
// its additions, which is the shape the hunk and intraline passes read.
func TestDiffOps_ScriptsAreCorrectMinimalAndGrouped(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	alphabet := []string{"a", "b", "c", "d", "e"}
	gen := func(maxLen int) []string {
		out := make([]string, rng.Intn(maxLen))
		for i := range out {
			out[i] = alphabet[rng.Intn(len(alphabet))]
		}
		return out
	}

	for i := 0; i < 5000; i++ {
		a, b := gen(16), gen(16)
		ops := diffOps(a, b)

		gotOld, gotNew := applyOps(ops)
		if strings.Join(gotOld, ",") != strings.Join(a, ",") || strings.Join(gotNew, ",") != strings.Join(b, ",") {
			t.Fatalf("script does not replay into its inputs\nold %v -> %v\nnew %v -> %v", a, gotOld, b, gotNew)
		}

		edits := 0
		for _, o := range ops {
			if o.kind != Context {
				edits++
			}
		}
		if want := len(a) + len(b) - 2*lcsLen(a, b); edits != want {
			t.Fatalf("script uses %d edits, the minimum is %d\nold %v\nnew %v", edits, want, a, b)
		}

		seenAdd := false
		for _, o := range ops {
			switch o.kind {
			case Context:
				seenAdd = false
			case Add:
				seenAdd = true
			case Del:
				if seenAdd {
					t.Fatalf("a deletion follows an addition within one change\nold %v\nnew %v", a, b)
				}
			}
		}
	}
}
