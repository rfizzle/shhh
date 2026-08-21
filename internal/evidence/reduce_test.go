package evidence

import (
	"fmt"
	"strings"
	"testing"
)

func TestSanitize_StripsControlSequences(t *testing.T) {
	in := "\x1b[31mred\x1b[0m line\r\nnext\rline\x1b]0;title\x07tail\x00\x01"
	got := sanitize(in)
	want := "red line\nnext\nlinetail"
	if got != want {
		t.Fatalf("sanitize = %q, want %q", got, want)
	}
}

func TestSanitize_KeepsTabsAndNewlines(t *testing.T) {
	in := "a\tb\nc"
	if got := sanitize(in); got != in {
		t.Fatalf("sanitize mangled plain text: %q", got)
	}
}

func TestReduce_SmallPassesThroughUntouched(t *testing.T) {
	// At the threshold, the minimum-savings guard fails open: even output
	// full of control sequences comes back byte-identical.
	in := "\x1b[31m" + strings.Repeat("x", ReduceThreshold-10)
	got, ok := reduce(in)
	if ok || got != in {
		t.Fatalf("small result must pass through untouched (ok=%v)", ok)
	}
}

func TestReduce_HeadTailKeep(t *testing.T) {
	var lines []string
	for i := 1; i <= 500; i++ {
		lines = append(lines, fmt.Sprintf("line %03d of plain output padding padding", i))
	}
	in := strings.Join(lines, "\n")
	got, ok := reduce(in)
	if !ok {
		t.Fatal("large result should reduce")
	}
	if len(got) >= len(in)-MinSavingsBytes {
		t.Fatalf("reduction saved too little: %d -> %d", len(in), len(got))
	}
	if !strings.HasPrefix(got, "line 001") {
		t.Fatalf("head must be kept verbatim, got prefix %q", got[:40])
	}
	if !strings.HasSuffix(got, "line 500 of plain output padding padding") {
		t.Fatalf("tail must be kept verbatim, got suffix %q", got[len(got)-45:])
	}
	if !strings.Contains(got, "lines elided") {
		t.Fatal("reduced view must note the elision")
	}
}

func TestReduce_PreservesInvariantLines(t *testing.T) {
	var lines []string
	for i := 1; i <= 500; i++ {
		lines = append(lines, fmt.Sprintf("ok line %03d with some padding text here", i))
	}
	lines[249] = "--- FAIL: TestSomething (0.01s)"
	lines[300] = "some/file.go:42: error: undefined variable"
	in := strings.Join(lines, "\n")
	got, ok := reduce(in)
	if !ok {
		t.Fatal("large result should reduce")
	}
	if !strings.Contains(got, "L250: --- FAIL: TestSomething (0.01s)") {
		t.Fatalf("test failure line must survive reduction with its line number:\n%s", got)
	}
	if !strings.Contains(got, "L301: some/file.go:42: error: undefined variable") {
		t.Fatalf("error line must survive reduction:\n%s", got)
	}
}

func TestReduce_FlaggedLinesBounded(t *testing.T) {
	var lines []string
	for i := 1; i <= 1000; i++ {
		lines = append(lines, fmt.Sprintf("step %04d error: something broke again", i))
	}
	in := strings.Join(lines, "\n")
	got, ok := reduce(in)
	if !ok {
		t.Fatal("large result should reduce")
	}
	if len(got) > 8192 {
		t.Fatalf("reduced view must stay bounded even when every line is flagged: %d bytes", len(got))
	}
	if !strings.Contains(got, "more flagged line(s) not shown") {
		t.Fatal("overflowing flagged lines must be counted, not silently dropped")
	}
}

func TestReduce_Deterministic(t *testing.T) {
	var lines []string
	for i := 0; i < 400; i++ {
		lines = append(lines, fmt.Sprintf("output line %d with enough padding to matter", i))
	}
	in := strings.Join(lines, "\n")
	a, _ := reduce(in)
	b, _ := reduce(in)
	if a != b {
		t.Fatal("reduction must be deterministic")
	}
}

func TestReduce_ClipsLongLines(t *testing.T) {
	in := strings.Repeat("a", 3000) + "\n" + strings.Repeat("b", 3000) + "\n" + strings.Repeat("c", 3000)
	got, ok := reduce(in)
	if !ok {
		t.Fatal("should reduce")
	}
	for _, line := range strings.Split(got, "\n") {
		if len(line) > maxReducedLineBytes+len("…") {
			t.Fatalf("line exceeds the per-line clip: %d bytes", len(line))
		}
	}
}
