package worktree

import (
	"fmt"
	"strconv"
	"strings"
)

// A merge kept in conflict is written the way `git merge-file --diff3` marks
// it: the landed text, the text both started from, then the lane's, each
// region between a line opening with the landed label and one closing with the
// lane's. The two rules below settle a region without anyone reading it; they
// are the only two, and each is exact about the shape it answers for, because
// a rule that guessed at a region would drop a side's work without a word.
// See docs/capabilities/todo.md#a-sprint-can-work-several-items-at-once.

// baseLabel is what the marked text calls the text both sides started from.
const baseLabel = "base"

// MarkerSize is how long the marks are written: longer than any run of
// marker characters a line of a real file holds, so a setext underline or a
// quoted conflict in the text is never mistaken for a mark.
const MarkerSize = 25

// mark is a marker line without its line ending, for the character c and a
// label (none for the middle one).
func mark(c, label string) string {
	m := strings.Repeat(c, MarkerSize)
	if label == "" {
		return m
	}
	return m + " " + label
}

// isMark is whether a line is exactly this marker, whichever line ending git
// wrote it with: a region of CRLF lines is marked with CRLF.
func isMark(line, marker string) bool {
	return strings.HasSuffix(line, "\n") && strings.TrimRight(line, "\r\n") == marker
}

// stillMarked is whether text holds an opening mark at all, which a merge
// that was settled must not.
func stillMarked(text string) bool { return strings.Contains(text, strings.Repeat("<", MarkerSize)) }

// region is one conflict region as three lists of lines, each line with its
// newline: the landed side, the base both started from, and the lane's.
type region struct{ Ours, Base, Theirs []string }

// piece is a run of marked text that is either plain or one region. A region
// carries the text it was written as, so a region no rule settles is put back
// exactly, and the lines it spans for the quote.
type piece struct {
	plain       string
	region      *region
	raw         string
	first, last int // 0-based line indexes of the opening and closing markers
}

// parseMarked splits text written with these labels into plain runs and
// regions. A marker with no partner, or out of order, is plain text: a file
// that quotes a conflict is not one.
func parseMarked(text, ours, theirs string) []piece {
	lines := strings.SplitAfter(text, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	var out []piece
	var plain strings.Builder
	flush := func() {
		if plain.Len() > 0 {
			out = append(out, piece{plain: plain.String()})
			plain.Reset()
		}
	}
	for i := 0; i < len(lines); i++ {
		if !isMark(lines[i], mark("<", ours)) {
			plain.WriteString(lines[i])
			continue
		}
		r, end, ok := scanRegion(lines, i, ours, theirs)
		if !ok {
			plain.WriteString(lines[i])
			continue
		}
		flush()
		out = append(out, piece{region: r, raw: strings.Join(lines[i:end+1], ""), first: i, last: end})
		i = end
	}
	flush()
	return out
}

// scanRegion reads the region whose opening marker is lines[open], answering
// with it and the index of its closing marker.
func scanRegion(lines []string, open int, ours, theirs string) (*region, int, bool) {
	r := &region{}
	into := &r.Ours
	stage := 0
	for i := open + 1; i < len(lines); i++ {
		switch {
		case stage == 0 && isMark(lines[i], mark("|", baseLabel)):
			into, stage = &r.Base, 1
		case stage == 1 && isMark(lines[i], mark("=", "")):
			into, stage = &r.Theirs, 2
		case stage == 2 && isMark(lines[i], mark(">", theirs)):
			return r, i, true
		default:
			*into = append(*into, lines[i])
		}
	}
	return nil, 0, false
}

// integerTokens is where the whole-number tokens of a line are: a run of
// digits with no letter, digit, underscore or dot against it and no minus
// before it, so a hash, a version, a name with a number in it and a negative
// are not counted.
func integerTokens(line string) [][2]int {
	var out [][2]int
	for i := 0; i < len(line); {
		if line[i] < '0' || line[i] > '9' {
			i++
			continue
		}
		j := i
		for j < len(line) && line[j] >= '0' && line[j] <= '9' {
			j++
		}
		if !joinsToken(line, i-1, "-") && !joinsToken(line, j, "") {
			out = append(out, [2]int{i, j})
		}
		i = j
	}
	return out
}

// joinsToken is whether the byte at i, if there is one, would make a run of
// digits next to it part of a longer token.
func joinsToken(s string, i int, extra string) bool {
	if i < 0 || i >= len(s) {
		return false
	}
	c := s[i]
	return c == '_' || c == '.' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte(extra, c) >= 0
}

// withoutTokens is a line with its integer tokens taken out.
func withoutTokens(line string, toks [][2]int) string {
	var b strings.Builder
	prev := 0
	for _, t := range toks {
		b.WriteString(line[prev:t[0]])
		b.WriteByte('#')
		prev = t[1]
	}
	b.WriteString(line[prev:])
	return b.String()
}

// sumRule settles a region where both sides moved the one integer on one line
// the same way: the base's line with that integer raised (or lowered) by both
// deltas. The line holds exactly one whole-number token, everything else on
// the three lines is the same, an integer written with a leading zero is not
// counted, and a sum below zero is not settled. A count two items each raised
// by one is raised by two.
func sumRule(r region) ([]string, bool) {
	if len(r.Ours) != 1 || len(r.Base) != 1 || len(r.Theirs) != 1 {
		return nil, false
	}
	o, b, t := r.Ours[0], r.Base[0], r.Theirs[0]
	ot, bt, tt := integerTokens(o), integerTokens(b), integerTokens(t)
	if len(bt) != 1 || len(ot) != 1 || len(tt) != 1 {
		return nil, false
	}
	if withoutTokens(b, bt) != withoutTokens(o, ot) || withoutTokens(b, bt) != withoutTokens(t, tt) {
		return nil, false
	}
	var v [3]int64
	for i, side := range []struct {
		line string
		tok  [2]int
	}{{o, ot[0]}, {b, bt[0]}, {t, tt[0]}} {
		s := side.line[side.tok[0]:side.tok[1]]
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || strconv.FormatInt(n, 10) != s {
			return nil, false
		}
		v[i] = n
	}
	do, dt := v[0]-v[1], v[2]-v[1]
	if do == 0 || dt == 0 || (do > 0) != (dt > 0) || v[1]+do+dt < 0 {
		return nil, false
	}
	return []string{b[:bt[0][0]] + strconv.FormatInt(v[1]+do+dt, 10) + b[bt[0][1]:]}, true
}

// bothInsertedRule settles a region where the base has nothing and both sides
// inserted lines at the same place that share none: the landed lines, then
// the lane's. The landed side goes first because it is the one already
// committed. A blank line is not a shared line: two sections each opening with
// one are two sections.
func bothInsertedRule(r region) ([]string, bool) {
	if len(r.Base) != 0 || len(r.Ours) == 0 || len(r.Theirs) == 0 {
		return nil, false
	}
	seen := map[string]bool{}
	for _, l := range r.Ours {
		if strings.TrimSpace(l) != "" {
			seen[l] = true
		}
	}
	for _, l := range r.Theirs {
		if seen[l] {
			return nil, false
		}
	}
	return append(append([]string(nil), r.Ours...), r.Theirs...), true
}

// settleRegions writes each region a rule settles as its settlement and
// leaves every other as it was marked, answering with the text and how many
// regions are left.
func settleRegions(text, ours, theirs string) (string, int) {
	var b strings.Builder
	left := 0
	for _, p := range parseMarked(text, ours, theirs) {
		if p.region == nil {
			b.WriteString(p.plain)
			continue
		}
		lines, ok := sumRule(*p.region)
		if !ok {
			lines, ok = bothInsertedRule(*p.region)
		}
		if !ok {
			left++
			b.WriteString(p.raw)
			continue
		}
		b.WriteString(strings.Join(lines, ""))
	}
	return b.String(), left
}

// markedRegions quotes each region still marked in text with the lines around
// it, under the line it starts at.
func markedRegions(text, ours, theirs string, context int) string {
	lines := strings.SplitAfter(text, "\n")
	var b strings.Builder
	for _, p := range parseMarked(text, ours, theirs) {
		if p.region == nil {
			continue
		}
		from, to := max(p.first-context, 0), min(p.last+1+context, len(lines))
		fmt.Fprintf(&b, "@@ line %d\n%s", p.first+1, strings.Join(lines[from:to], ""))
		if !strings.HasSuffix(b.String(), "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}
