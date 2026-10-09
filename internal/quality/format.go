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
// checks ran, or held more changed content than the fingerprint would hash —
// leads with a stale warning, so a pass over old code is never presented
// silently as current.
func (r *Result) Format(current Fingerprint) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Quality gate %q: %s", r.Suite, strings.ToUpper(string(r.Verdict)))
	switch r.Verdict {
	case VerdictPass, VerdictFail:
		fmt.Fprintf(&b, " — %d/%d checks passed", r.passed(), len(r.Checks))
		if n := r.flaked(); n > 0 {
			fmt.Fprintf(&b, ", %d flaked", n)
		}
		fmt.Fprintf(&b, " (%s)", roundDuration(r.Duration))
	case VerdictBlocked:
		b.WriteString(" — the gate could not run: " + r.Reason + ". Blocked is never a pass.")
	case VerdictCancelled:
		b.WriteString(" — " + r.Reason + ". Cancelled is never a pass.")
	}
	b.WriteString("\nTree: " + r.Fingerprint.Describe() + "\n")
	if r.Contained != "" {
		b.WriteString("Containment: " + r.Contained + "\n")
	}
	if note := r.staleNote(current); note != "" {
		b.WriteString("STALE: " + note + "\n")
	}
	for _, c := range r.Checks {
		b.WriteString(formatCheck(c))
	}
	return strings.TrimRight(b.String(), "\n")
}

// staleNote is why the result does not apply to the tree whose fingerprint
// is current, and empty where it does. Format writes it as the STALE line and
// Summary reads it as the stale mark, so the two cannot disagree about which
// results are stale.
func (r *Result) staleNote(current Fingerprint) string {
	switch {
	case r.ChangedDuringRun:
		return "the tree changed while the checks ran — this verdict (even a pass) does not apply to the current tree; run the gate again."
	// An unhashed fingerprint has to be caught before the equality test: two
	// of them can compare equal while the content underneath differs, which
	// is exactly the silent pass the fingerprint exists to prevent.
	case r.Fingerprint.Repo && current.Repo && (r.Fingerprint.Unhashed || current.Unhashed):
		return "too many changed paths to hash their content, so an edit to one of them cannot be detected — this verdict (even a pass) does not apply to the current tree; commit or clean up, then run the gate again."
	case r.Fingerprint.Repo && current.Repo && r.Fingerprint != current:
		return "the tree has changed since this run — this verdict (even a pass) does not apply to the current tree; run the gate again."
	}
	return ""
}

// passed is how many of the result's checks came back clean.
func (r *Result) passed() int {
	n := 0
	for _, c := range r.Checks {
		if c.OK() {
			n++
		}
	}
	return n
}

// flaked is how many of the result's checks passed only on their rerun.
func (r *Result) flaked() int {
	n := 0
	for _, c := range r.Checks {
		if c.Flaked {
			n++
		}
	}
	return n
}

// formatCheck is the one place a check's outcome is worded. The screen and
// the model read the same text, so a flake is spelled here and nowhere else.
func formatCheck(c CheckResult) string {
	var b strings.Builder
	evidence := ""
	if c.EvidenceID != "" {
		evidence = " [full output: evidence " + c.EvidenceID + "]"
	}
	// The count rides between the parenthesis and the evidence, outside both,
	// so no reader of the parenthesised outcome sees it.
	evidence = skippedFigure(c.Skips) + evidence
	name := c.Name + scopeWords(c.Scope)
	switch {
	case c.NotRun != "":
		return fmt.Sprintf("  - %s — %s (not run: %s)\n", name, c.Command, c.NotRun)
	case c.Err != "":
		fmt.Fprintf(&b, "  ! %s — %s (did not run: %s)%s\n", name, c.Command, c.Err, evidence)
	case c.TimedOut:
		fmt.Fprintf(&b, "  ✗ %s — %s (timed out after %s)%s\n", name, c.Command, roundDuration(c.Duration), evidence)
	case c.ExitCode != 0:
		fmt.Fprintf(&b, "  ✗ %s — %s (exit %d, %s)%s\n", name, c.Command, c.ExitCode, roundDuration(c.Duration), evidence)
	case c.Flaked:
		// The evidence named is the first run's, the failure: the pass is
		// the rerun, and what made the check flake is what a reader opens.
		fmt.Fprintf(&b, "  ~ %s — %s (flaked: failed then passed, %s + %s%s)%s\n", name, c.Command,
			roundDuration(c.Duration), roundDuration(c.RerunDuration), timesBefore(c.FlakedBefore), evidence)
	default:
		fmt.Fprintf(&b, "  ✓ %s — %s (%s)%s\n", name, c.Command, roundDuration(c.Duration), evidence)
	}
	for _, s := range c.Skips {
		b.WriteString("    " + s + "\n")
	}
	if c.OK() {
		return b.String()
	}
	if out := strings.TrimSpace(c.Output); out != "" {
		for _, line := range strings.Split(out, "\n") {
			b.WriteString("    " + line + "\n")
		}
	}
	return b.String()
}

// maxScopeWords is how many packages a scoped check's line names before it
// counts the rest.
const maxScopeWords = 4

// scopeWords is the packages a scoped check ran over, in the parenthesis
// after its name: `(internal/quality, internal/cli)`. A check that is not
// scoped has none.
func scopeWords(scope []string) string {
	switch {
	case len(scope) == 0:
		return ""
	case len(scope) > maxScopeWords:
		return fmt.Sprintf(" (%s, +%d more)", strings.Join(scope[:maxScopeWords], ", "), len(scope)-maxScopeWords)
	}
	return " (" + strings.Join(scope, ", ") + ")"
}

// skippedFigure is the check line's `· 51 skipped`: the sum of the counts on
// the check's skip-reason lines, and nothing where none skipped. A host that
// cannot run a mechanism's tests shows as a number on the row.
// See docs/capabilities/testing.md#a-skipped-test-is-counted.
func skippedFigure(skips []string) string {
	n := 0
	for _, s := range skips {
		if m := skipLinePattern.FindStringSubmatch(s); m != nil {
			c, _ := strconv.Atoi(m[1])
			n += c
		}
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(" · %d skipped", n)
}

// timesBefore is the flaked line's count of the check's earlier flakes in
// this checkout — `, 3 times before` — and nothing on a first flake. It is
// the reason a reader stops trusting the check rather than the gate.
// See docs/capabilities/testing.md#a-flake-is-counted-where-it-happened.
func timesBefore(n int) string {
	switch {
	case n <= 0:
		return ""
	case n == 1:
		return ", 1 time before"
	}
	return fmt.Sprintf(", %d times before", n)
}

func roundDuration(d time.Duration) string {
	return d.Round(100 * time.Millisecond).String()
}

// Summary is what a reader of the gate learns about one run: which suite
// ran, its verdict, the check tally and whether the verdict still applies to
// the tree. It has two sources. A row the session made itself holds the
// Result and reads it with Result.Summary; a row that exists only as text —
// a tool result the model asked for, a reopened session, a child's
// transcript — reads it back with Summarize, beside Format, so the one place
// that writes the string is the one place that reads it back. A round-trip
// test holds the two to the same answer.
type Summary struct {
	Suite         string
	Verdict       Verdict
	Passed, Total int
	// Flaked is how many of the passed checks passed only on their rerun.
	Flaked int
	// FlakedBefore is the most times any check that flaked in this run had
	// flaked in the checkout before it.
	FlakedBefore int
	Duration     string
	// Stale marks a verdict that does not apply to the tree it was read
	// against — the tree moved under it. A stale pass is not a pass.
	Stale bool
}

// OK reports a verdict a caller may treat as green: a pass over the tree it
// actually ran against.
func (s Summary) OK() bool { return s.Verdict == VerdictPass && !s.Stale }

// OK reports whether the result is a pass a caller may act on, read against
// the tree whose fingerprint is current. It is the one reading of "passed"
// for every surface that holds a Result — the close row, the backlog run's
// verify stage, the unattended close — so a stale pass, which the close row
// marks stale, is not a pass anywhere else either.
// See docs/capabilities/testing.md#how-do-quality-gates-stay-repeatable.
func (r *Result) OK(current Fingerprint) bool { return r.Summary(current).OK() }

// Summary is the result read against the tree whose fingerprint is current:
// what Summarize would read back from Format(current), without the text.
func (r *Result) Summary(current Fingerprint) Summary {
	s := Summary{Suite: r.Suite, Verdict: r.Verdict, Stale: r.staleNote(current) != ""}
	switch r.Verdict {
	case VerdictPass, VerdictFail:
		s.Passed, s.Total, s.Flaked, s.Duration = r.passed(), len(r.Checks), r.flaked(), roundDuration(r.Duration)
		for _, c := range r.Checks {
			if c.Flaked {
				s.FlakedBefore = max(s.FlakedBefore, c.FlakedBefore)
			}
		}
	}
	return s
}

// flakedBeforePattern reads timesBefore's clause back off a flaked check's
// line. The clause sits just inside the line's closing parenthesis, after two
// durations that hold neither a comma nor a parenthesis, so a command
// holding either is never read as one.
var flakedBeforePattern = regexp.MustCompile(`(?m)^  ~ .*\(flaked: failed then passed, [^,()]*, (\d+) times? before\)`)

// summaryPattern reads the suite as the whole Go-quoted token Format's %q
// writes, escapes and all, so a name holding a quote or a backslash is read
// back rather than ending the match early; Summarize unquotes it.
var summaryPattern = regexp.MustCompile(
	`^Quality gate ("(?:[^"\\]|\\.)*"): ([A-Z]+)(?: — (\d+)/(\d+) checks passed(?:, (\d+) flaked)? \(([^)]*)\))?`)

// Summarize reads back a result rendered by Format. It reports false for
// anything else — a status line, an error, a tool result from elsewhere — so
// a caller never has to guess whether the gate is what it is looking at.
func Summarize(result string) (Summary, bool) {
	m := summaryPattern.FindStringSubmatch(strings.SplitN(result, "\n", 2)[0])
	if m == nil {
		return Summary{}, false
	}
	suite, err := strconv.Unquote(m[1])
	if err != nil {
		return Summary{}, false
	}
	s := Summary{
		Suite:    suite,
		Verdict:  Verdict(strings.ToLower(m[2])),
		Duration: m[6],
		Stale:    strings.Contains(result, "\nSTALE:"),
	}
	s.Passed, _ = strconv.Atoi(m[3])
	s.Total, _ = strconv.Atoi(m[4])
	s.Flaked, _ = strconv.Atoi(m[5])
	for _, f := range flakedBeforePattern.FindAllStringSubmatch(result, -1) {
		n, _ := strconv.Atoi(f[1])
		s.FlakedBefore = max(s.FlakedBefore, n)
	}
	return s, true
}
