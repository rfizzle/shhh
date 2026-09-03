// Package diff computes line-based unified diffs as hunks, for the TUI diff
// viewer and approval previews. It works from old/new content directly
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
// 1-based line numbers in the old/new content; 0 means the line does not
// exist on that side.
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

// Header renders the unified-diff hunk header ("@@ -1,2 +1,3 @@"). A side
// with zero lines reports the line before the change, matching git's
// convention.
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

// maxEdits bounds the number of differing lines the search will resolve
// inside one region. The scan costs on the order of d²/4 diagonal probes for
// an edit distance of d, so two large files with nothing in common would
// otherwise hold the render for as long as it takes; past the bound the
// region degrades to a delete-all/add-all replacement, which is coarse but
// immediate. 4096 was measured rather than picked: the worst case at that
// distance runs in about 10ms here, comfortably inside the 80ms the TUI
// gives a frame, and no realistic edit to a source file comes near it.
const maxEdits = 4096

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

// search carries the two furthest-reaching-path rows the middle-snake scan
// needs, one per direction, indexed by diagonal. They are grown to fit the
// first and widest region and then reused by every recursive call: a call
// finishes its scan before it recurses, so no two calls read a row at the
// same time.
type search struct {
	fwd, rev []int
}

// diffOps aligns two line slices into an ordered sequence of context, delete
// and add operations, using Myers' O((N+M)·D) difference algorithm in the
// linear-space divide-and-conquer form. Cost tracks the size of the change
// rather than the size of the files, so an append to a long file is nearly
// free; the whole-file table this replaced could not diff two files of a few
// thousand lines at all.
func diffOps(a, b []string) []op {
	var s search
	return groupChanges(s.align(make([]op, 0, len(a)+len(b)), a, b))
}

// align appends the ops that turn a into b. Common leading and trailing
// lines are matched directly — this is what makes an append or a localised
// edit cheap, since it leaves the search only the lines that actually
// differ — and what remains is split at a point on a shortest edit path and
// recursed into.
func (s *search) align(ops []op, a, b []string) []op {
	p := 0
	for p < len(a) && p < len(b) && a[p] == b[p] {
		p++
	}
	for _, l := range a[:p] {
		ops = append(ops, op{Context, l})
	}
	a, b = a[p:], b[p:]

	t := 0
	for t < len(a) && t < len(b) && a[len(a)-1-t] == b[len(b)-1-t] {
		t++
	}
	tail := a[len(a)-t:]
	a, b = a[:len(a)-t], b[:len(b)-t]

	switch {
	case len(a) == 0:
		// Also the case where both sides are empty, which appends nothing.
		for _, l := range b {
			ops = append(ops, op{Add, l})
		}
	case len(b) == 0:
		for _, l := range a {
			ops = append(ops, op{Del, l})
		}
	default:
		if x, y, ok := s.middleSnake(a, b); ok {
			ops = s.align(ops, a[:x], b[:y])
			ops = s.align(ops, a[x:], b[y:])
			break
		}
		// Past maxEdits: replace the region wholesale. Still a correct edit
		// script, just not a minimal one.
		for _, l := range a {
			ops = append(ops, op{Del, l})
		}
		for _, l := range b {
			ops = append(ops, op{Add, l})
		}
	}

	for _, l := range tail {
		ops = append(ops, op{Context, l})
	}
	return ops
}

// middleSnake returns a point (x, y) lying on some shortest edit path from
// a to b, found by running the greedy edit-graph scan forward from the start
// and backward from the end until the two meet in the middle. Splitting
// there and recursing is what keeps memory to two rows instead of a table.
//
// Callers must pass non-empty slices whose first and last lines already
// differ; that guarantees an edit distance of at least two, which in turn
// guarantees the returned point splits the region into two strictly smaller
// ones and the recursion terminates. ok is false when the edit distance
// exceeds maxEdits.
func (s *search) middleSnake(a, b []string) (x, y int, ok bool) {
	n, m := len(a), len(b)
	half := (n + m + 1) / 2
	// Diagonal k is stored at centre+k; the scan reads k±1, hence the slack.
	centre := half + 1
	size := 2*half + 3
	if len(s.fwd) < size {
		// The first call is on the widest region, so this grows once.
		s.fwd = make([]int, size)
		s.rev = make([]int, size)
	}
	fwd, rev := s.fwd[:size], s.rev[:size]
	for i := range fwd {
		fwd[i], rev[i] = -1, -1
	}
	fwd[centre+1] = 0
	rev[centre+1] = 0

	// The two scans can only meet on a diagonal offset by delta, and which
	// scan sees the meeting first depends on delta's parity.
	delta := n - m
	meetForward := delta%2 != 0

	// Diagonals that have run off the edit graph are dropped from the range
	// scanned rather than re-tested.
	var fLo, fHi, rLo, rHi int
	limit := min(half, (maxEdits+1)/2)
	for d := 0; d <= limit; d++ {
		for k := -d + fLo; k <= d-fHi; k += 2 {
			i := centre + k
			var fx int
			if k == -d || (k != d && fwd[i-1] < fwd[i+1]) {
				fx = fwd[i+1]
			} else {
				fx = fwd[i-1] + 1
			}
			fy := fx - k
			for fx < n && fy < m && a[fx] == b[fy] {
				fx++
				fy++
			}
			fwd[i] = fx
			switch {
			case fx > n:
				fHi += 2
			case fy > m:
				fLo += 2
			case meetForward:
				j := centre + delta - k
				if j >= 0 && j < size && rev[j] >= 0 && rev[j] <= n && fx >= n-rev[j] {
					return fx, fy, true
				}
			}
		}
		for k := -d + rLo; k <= d-rHi; k += 2 {
			i := centre + k
			var rx int
			if k == -d || (k != d && rev[i-1] < rev[i+1]) {
				rx = rev[i+1]
			} else {
				rx = rev[i-1] + 1
			}
			ry := rx - k
			for rx < n && ry < m && a[n-rx-1] == b[m-ry-1] {
				rx++
				ry++
			}
			rev[i] = rx
			switch {
			case rx > n:
				rHi += 2
			case ry > m:
				rLo += 2
			case !meetForward:
				j := centre + delta - k
				if j < 0 || j >= size || fwd[j] < 0 || fwd[j] > n {
					continue
				}
				fx := fwd[j]
				fy := fx - (j - centre)
				if fy >= 0 && fy <= m && fx >= n-rx {
					return fx, fy, true
				}
			}
		}
	}
	return 0, 0, false
}

// groupChanges reorders each run of changed ops so that its deletions all
// precede its additions. The scan can interleave the two inside one change,
// and every reader downstream expects git's grouping: the intraline pass
// pairs the k-th deletion of a run with its k-th addition, and a reviewer
// reads the old block then the new one. Reordering inside a run moves no
// line across a context line, so the script still applies.
func groupChanges(ops []op) []op {
	var adds []op
	for i := 0; i < len(ops); {
		if ops[i].kind == Context {
			i++
			continue
		}
		j := i
		for j < len(ops) && ops[j].kind != Context {
			j++
		}
		adds = adds[:0]
		w := i
		for _, o := range ops[i:j] {
			if o.kind == Del {
				ops[w] = o
				w++
				continue
			}
			adds = append(adds, o)
		}
		copy(ops[w:j], adds)
		i = j
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
