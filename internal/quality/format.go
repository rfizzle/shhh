package quality

import (
	"fmt"
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
