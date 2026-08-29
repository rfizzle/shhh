package quality

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Format renders the result against the tree's current fingerprint. A result
// whose fingerprint no longer matches — or whose tree changed while the
// checks ran — leads with a stale warning, so a pass over old code is never
// presented silently as current.
func (r *Result) Format(current Fingerprint) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Quality gate %q: %s", r.Suite, strings.ToUpper(string(r.Verdict)))
	switch r.Verdict {
	case VerdictPass, VerdictFail:
		passed := 0
		for _, c := range r.Checks {
			if c.OK() {
				passed++
			}
		}
		fmt.Fprintf(&b, " — %d/%d checks passed (%s)", passed, len(r.Checks), roundDuration(r.Duration))
	case VerdictBlocked:
		b.WriteString(" — the gate could not run: " + r.Reason + ". Blocked is never a pass.")
	case VerdictCancelled:
		b.WriteString(" — " + r.Reason + ". Cancelled is never a pass.")
	}
	b.WriteString("\nTree: " + r.Fingerprint.Describe() + "\n")
	if r.Contained != "" {
		b.WriteString("Containment: " + r.Contained + "\n")
	}
	switch {
	case r.ChangedDuringRun:
		b.WriteString("STALE: the tree changed while the checks ran — this verdict (even a pass) does not apply to the current tree; run the gate again.\n")
	case r.Fingerprint.Repo && current.Repo && r.Fingerprint != current:
		b.WriteString("STALE: the tree has changed since this run — this verdict (even a pass) does not apply to the current tree; run the gate again.\n")
	}
	for _, c := range r.Checks {
		b.WriteString(formatCheck(c))
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatCheck(c CheckResult) string {
	var b strings.Builder
	evidence := ""
	if c.EvidenceID != "" {
		evidence = " [full output: evidence " + c.EvidenceID + "]"
	}
	switch {
	case c.Err != "":
		fmt.Fprintf(&b, "  ! %s — %s (did not run: %s)%s\n", c.Name, c.Command, c.Err, evidence)
	case c.TimedOut:
		fmt.Fprintf(&b, "  ✗ %s — %s (timed out after %s)%s\n", c.Name, c.Command, roundDuration(c.Duration), evidence)
	case c.ExitCode != 0:
		fmt.Fprintf(&b, "  ✗ %s — %s (exit %d, %s)%s\n", c.Name, c.Command, c.ExitCode, roundDuration(c.Duration), evidence)
	default:
		fmt.Fprintf(&b, "  ✓ %s — %s (%s)%s\n", c.Name, c.Command, roundDuration(c.Duration), evidence)
		return b.String()
	}
	if out := strings.TrimSpace(c.Output); out != "" {
		for _, line := range strings.Split(out, "\n") {
			b.WriteString("    " + line + "\n")
		}
	}
	return b.String()
}

func roundDuration(d time.Duration) string {
	return d.Round(100 * time.Millisecond).String()
}

// Summary is what a caller can learn from a formatted result without holding
// the Result itself: which suite ran, its verdict, the check tally and
// whether the verdict still applies to the tree. It is parsed here, beside
// Format, so the one place that writes the string is the one place that reads
// it back.
type Summary struct {
	Suite         string
	Verdict       Verdict
	Passed, Total int
	Duration      string
	// Stale marks a verdict the run itself disowned — the tree moved under
	// it. A stale pass is not a pass.
	Stale bool
}

// OK reports a verdict a caller may treat as green: a pass over the tree it
// actually ran against.
func (s Summary) OK() bool { return s.Verdict == VerdictPass && !s.Stale }

var summaryPattern = regexp.MustCompile(
	`^Quality gate "([^"]*)": ([A-Z]+)(?: — (\d+)/(\d+) checks passed \(([^)]*)\))?`)

// Summarize reads back a result rendered by Format. It reports false for
// anything else — a status line, an error, a tool result from elsewhere — so
// a caller never has to guess whether the gate is what it is looking at.
func Summarize(result string) (Summary, bool) {
	m := summaryPattern.FindStringSubmatch(strings.SplitN(result, "\n", 2)[0])
	if m == nil {
		return Summary{}, false
	}
	s := Summary{
		Suite:    m[1],
		Verdict:  Verdict(strings.ToLower(m[2])),
		Duration: m[5],
		Stale:    strings.Contains(result, "\nSTALE:"),
	}
	s.Passed, _ = strconv.Atoi(m[3])
	s.Total, _ = strconv.Atoi(m[4])
	return s, true
}
