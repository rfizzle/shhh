package evidence

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/rfizzle/shhh/internal/tools"
)

// Reduction pipeline tuning. Every knob lives here, so there is one
// place to reason about how much of a tool result the model sees.
const (
	// ReduceThreshold is the minimum-savings guard: results at or below this
	// many bytes pass through the pipeline untouched (fail open) — reducing
	// small results costs fidelity without buying context.
	ReduceThreshold = 4096

	// headLineMax/headByteMax bound the verbatim head of a reduced result;
	// tailLineMax/tailByteMax the verbatim tail. Whichever bound is hit
	// first wins.
	headLineMax = 40
	headByteMax = 1400
	tailLineMax = 20
	tailByteMax = 900

	// keptLineMax/keptByteMax bound the flagged lines (errors, test
	// failures) preserved from the elided middle.
	keptLineMax = 40
	keptByteMax = 900

	// maxReducedLineBytes clips one line in the reduced view, so a single
	// minified line cannot consume a whole budget.
	maxReducedLineBytes = 320

	// MinSavingsBytes backstops the guard from the other side: a reduction
	// that would save less than this passes the original through instead.
	MinSavingsBytes = 512
)

// invariantRe flags lines the reduction must never elide silently: errors,
// panics, and test failures. Flagged middle lines are kept (bounded) with
// their line numbers.
var invariantRe = regexp.MustCompile(`(?i)\berror\b|\bfail(s|ed|ing|ure)?\b|\bpanic\b|\bfatal\b|\bexception\b|\btraceback\b|\bnot ok\b|✗`)

// ansiRe matches ANSI CSI and OSC sequences plus any other two-byte escape.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;:?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b.`)

// sanitize strips terminal control sequences from tool output destined for
// the model: ANSI escapes are removed, \r\n and lone \r become \n, and other
// control characters (except \n and \t) are dropped.
func sanitize(s string) string {
	s = ansiRe.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r == '\r':
			return '\n'
		case r < 0x20 || r == 0x7f:
			return -1
		}
		return r
	}, s)
}

// reduce applies the deterministic reduction pipeline to one tool result:
// control-sequence sanitization, a verbatim head and tail, and flagged middle
// lines (errors, test failures) preserved with line numbers. ok=false means
// the result passed through untouched — it was at or below ReduceThreshold,
// or reducing it would not save MinSavingsBytes (both fail open).
func reduce(s string) (reduced string, ok bool) {
	if len(s) <= ReduceThreshold {
		return s, false
	}
	lines := strings.Split(sanitize(s), "\n")
	total := len(lines)

	var head []string
	headBytes := 0
	headEnd := 0
	for headEnd < total && len(head) < headLineMax {
		l := clipLine(lines[headEnd])
		if len(head) > 0 && headBytes+len(l) > headByteMax {
			break
		}
		head = append(head, l)
		headBytes += len(l) + 1
		headEnd++
	}

	tailStart := total
	tailBytes := 0
	var tailRev []string
	for tailStart > headEnd && len(tailRev) < tailLineMax {
		l := clipLine(lines[tailStart-1])
		if len(tailRev) > 0 && tailBytes+len(l) > tailByteMax {
			break
		}
		tailRev = append(tailRev, l)
		tailBytes += len(l) + 1
		tailStart--
	}

	var kept []string
	keptBytes := 0
	overflow := 0
	for i := headEnd; i < tailStart; i++ {
		if !invariantRe.MatchString(lines[i]) {
			continue
		}
		l := fmt.Sprintf("L%d: %s", i+1, clipLine(lines[i]))
		if len(kept) >= keptLineMax || keptBytes+len(l) > keptByteMax {
			overflow++
			continue
		}
		kept = append(kept, l)
		keptBytes += len(l) + 1
	}

	var b strings.Builder
	b.WriteString(strings.Join(head, "\n"))
	if elided := tailStart - headEnd; elided > 0 {
		fmt.Fprintf(&b, "\n… [%d of %d lines elided", elided, total)
		if len(kept) > 0 {
			fmt.Fprintf(&b, "; %d flagged line(s) kept below", len(kept))
		}
		if overflow > 0 {
			fmt.Fprintf(&b, "; %d more flagged line(s) not shown", overflow)
		}
		b.WriteString("]")
		for _, l := range kept {
			b.WriteString("\n" + l)
		}
		if len(tailRev) > 0 {
			b.WriteString("\n… [tail of output]")
		}
	}
	for i := len(tailRev) - 1; i >= 0; i-- {
		b.WriteString("\n" + tailRev[i])
	}

	reduced = b.String()
	if len(s)-len(reduced) < MinSavingsBytes {
		return s, false
	}
	return reduced, true
}

// clipLine bounds one line of the reduced view at maxReducedLineBytes.
func clipLine(line string) string {
	if cut, truncated := tools.TruncateOutput(line, maxReducedLineBytes); truncated {
		return cut + "…"
	}
	return line
}
