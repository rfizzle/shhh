// Package diff computes line-based unified diffs as hunks, for the TUI diff
// viewer and approval previews (S-076). It works from old/new content directly
// (edit_file knows both sides), so nothing shells out to git except the
// session-level /diff view.
package diff

import "fmt"

// Kind classifies one line of a hunk.
type Kind int

const (
	Context Kind = iota
	Add
	Del
)

// Span is a half-open rune range [Start, End) into a Line's Text, marking the
// changed segment of a paired add/del line for intraline emphasis.
type Span struct {
	Start, End int
}

// Line is one line of a hunk. Text carries no diff marker. OldNo/NewNo are
// 1-based line numbers in the old/new content; 0 means the line does not exist
// on that side.
type Line struct {
	Kind  Kind
	Text  string
	OldNo int
	NewNo int
	// Emph marks the changed span within a del/add line pair; empty when the
	// whole line should be treated as changed.
	Emph []Span
}

// Hunk is one group of changed lines with surrounding context.
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Lines              []Line
}

// Header renders the unified-diff hunk header ("@@ -1,2 +1,3 @@"). A side with
// zero lines reports the line before the change, matching git's convention.
func (h Hunk) Header() string {
	os, ns := h.OldStart, h.NewStart
	if h.OldCount == 0 {
		os = h.OldStart - 1
	}
	if h.NewCount == 0 {
		ns = h.NewStart - 1
	}
	return fmt.Sprintf("@@ -%d,%d +%d,%d @@", os, h.OldCount, ns, h.NewCount)
}

// Stats totals added and deleted lines across hunks.
func Stats(hunks []Hunk) (adds, dels int) {
	for _, h := range hunks {
		for _, l := range h.Lines {
			switch l.Kind {
			case Add:
				adds++
			case Del:
				dels++
			}
		}
	}
	return adds, dels
}

// contextLines is how many unchanged lines surround each change in a hunk;
// changes closer than twice this merge into one hunk.
const contextLines = 3

// maxCells bounds the LCS table size; beyond it the diff degrades to a
// whole-file replacement rather than allocating unbounded memory.
const maxCells = 4_000_000

type op struct {
	kind Kind
	text string
}

// Compute diffs oldText against newText and returns the hunks, or nil when
// the texts are line-identical.
func Compute(oldText, newText string) []Hunk {
	ops := diffOps(splitLines(oldText), splitLines(newText))
	changed := false
	for _, o := range ops {
		if o.kind != Context {
			changed = true
			break
		}
	}
	if !changed {
		return nil
	}
	hunks := buildHunks(ops)
	for i := range hunks {
		markIntraline(hunks[i].Lines)
	}
	return hunks
}

// splitLines splits text into lines, treating a trailing newline as a
// terminator rather than an extra empty line.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// diffOps aligns two line slices into an ordered sequence of context, delete,
// and add operations via longest common subsequence.
func diffOps(a, b []string) []op {
	n, m := len(a), len(b)
	if n > 0 && m > 0 && n*m > maxCells {
		ops := make([]op, 0, n+m)
		for _, l := range a {
			ops = append(ops, op{Del, l})
		}
		for _, l := range b {
			ops = append(ops, op{Add, l})
		}
		return ops
	}

	// lcs[i][j] = length of the longest common subsequence of a[i:] and b[j:].
	lcs := make([][]int32, n+1)
	for i := range lcs {
		lcs[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}

	ops := make([]op, 0, n+m)
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, op{Context, a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			ops = append(ops, op{Del, a[i]})
			i++
		default:
			ops = append(ops, op{Add, b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, op{Del, a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, op{Add, b[j]})
	}
	return ops
}

// buildHunks groups ops into hunks with contextLines of surrounding context,
// assigning old/new line numbers to every line.
func buildHunks(ops []op) []Hunk {
	include := make([]bool, len(ops))
	for i, o := range ops {
		if o.kind == Context {
			continue
		}
		for j := max(0, i-contextLines); j <= min(len(ops)-1, i+contextLines); j++ {
			include[j] = true
		}
	}

	var out []Hunk
	oldNo, newNo := 1, 1
	i := 0
	for i < len(ops) {
		if !include[i] {
			// Only context ops can be excluded.
			oldNo++
			newNo++
			i++
			continue
		}
		j := i
		for j < len(ops) && include[j] {
			j++
		}
		h := Hunk{OldStart: oldNo, NewStart: newNo}
		h.Lines = make([]Line, 0, j-i)
		for _, o := range ops[i:j] {
			switch o.kind {
			case Context:
				h.Lines = append(h.Lines, Line{Kind: Context, Text: o.text, OldNo: oldNo, NewNo: newNo})
				h.OldCount++
				h.NewCount++
				oldNo++
				newNo++
			case Del:
				h.Lines = append(h.Lines, Line{Kind: Del, Text: o.text, OldNo: oldNo})
				h.OldCount++
				oldNo++
			case Add:
				h.Lines = append(h.Lines, Line{Kind: Add, Text: o.text, NewNo: newNo})
				h.NewCount++
				newNo++
			}
		}
		out = append(out, h)
		i = j
	}
	return out
}

// markIntraline pairs each run of deletions with the run of additions that
// follows it and marks the changed span of every pair, so renderers can
// emphasize just the edited segment of a modified line.
func markIntraline(lines []Line) {
	i := 0
	for i < len(lines) {
		if lines[i].Kind != Del {
			i++
			continue
		}
		ds := i
		for i < len(lines) && lines[i].Kind == Del {
			i++
		}
		as := i
		for i < len(lines) && lines[i].Kind == Add {
			i++
		}
		pairs := min(as-ds, i-as)
		for k := 0; k < pairs; k++ {
			markPair(&lines[ds+k], &lines[as+k])
		}
	}
}

// markPair computes the common prefix and suffix of a del/add line pair and
// records the differing middle as each line's emphasis span. Pairs with
// nothing in common get no span (the whole line reads as changed).
func markPair(d, a *Line) {
	dr, ar := []rune(d.Text), []rune(a.Text)
	limit := min(len(dr), len(ar))
	p := 0
	for p < limit && dr[p] == ar[p] {
		p++
	}
	s := 0
	for s < limit-p && dr[len(dr)-1-s] == ar[len(ar)-1-s] {
		s++
	}
	if p == 0 && s == 0 {
		return
	}
	if e := len(dr) - s; p < e {
		d.Emph = []Span{{Start: p, End: e}}
	}
	if e := len(ar) - s; p < e {
		a.Emph = []Span{{Start: p, End: e}}
	}
}
