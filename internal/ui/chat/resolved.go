package chat

// What a turn's verification came to, once (
// docs/interface/surfaces.md#the-turns-close).
//
// A turn that ran the tests, watched them fail, fixed the code and ran the
// repository's own suite again has one true answer about the tree it leaves
// behind, and it is the last one. Reading that answer is a single derivation
// here rather than a scan in each surface, because the surfaces that report a
// verdict — the close row, the verification pinned beside a review, and the
// rail's standing alerts — must not be able to disagree: a session that says
// the gate passed on one row and the checks are failing on another has told
// the reader nothing they can act on.
//
// Nothing here rewrites history. A superseded attempt keeps its row, its
// outcome and its time; what it loses is the vote.

import (
	"fmt"
	"strings"

	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// testCommandHints are the command shapes whose exit code is a verdict about
// the code rather than about the shell. The quality gate is the authoritative
// source — it reports a tally the session can quote — and this list is the
// approximation for the turns that just ran the suite themselves.
var testCommandHints = []string{
	"go test", "gotestsum", "npm test", "npm run test", "yarn test",
	"pnpm test", "pytest", "cargo test", "make test", "mix test",
	"dotnet test", "rspec", "bundle exec rspec",
}

func isTestCommand(command string) bool {
	c := strings.ToLower(firstLine(command))
	for _, hint := range testCommandHints {
		if strings.Contains(c, hint) {
			return true
		}
	}
	return false
}

// verification is where a span's most recent applicable quality-gate pass
// sits: the position before which every failure has been answered. unverified
// is a span nothing has checked.
//
// It is a type of its own because two readers want different amounts of the
// same fact. The close row wants the whole reading below; the rail wants only
// this, on every frame and over the whole transcript, and making it build the
// attempt list to ask one question would be a scan nobody reads.
type verification int

const unverified verification = -1

// lastVerification is that position in es. The most recent pass wins: an
// earlier one has already been answered for by it, and a later failure is
// about a tree the earlier run never saw.
func lastVerification(es []entry) verification {
	at := unverified
	for i, e := range es {
		if s, ok := gateVerdict(e); ok && s.OK() {
			at = verification(i)
		}
	}
	return at
}

// settled reports whether the entry at position i — in the same slice the
// verification was read from — has been answered by it. It is what the rail
// asks of a failing command before it keeps a standing alert for one: the
// suite has since run over that tree and come back clean, so the failure is
// history rather than bad news.
func (v verification) settled(i int) bool { return v >= 0 && i < int(v) }

// gateVerdict is the quality-gate reading a row carries, and false for every
// row that is not one.
//
// It reads what the trim kept in preference to the row's own body. A trim
// replaces a tool result with a placeholder, and a caller that parsed the
// placeholder would find no gate there and report a settled turn as unsettled
// — while the close row it has to agree with was drawn once, before the trim,
// and says the opposite forever. A verdict outlives the text it was written
// in (context.go).
func gateVerdict(e entry) (quality.Summary, bool) {
	if e.kind != entryTool || e.toolName != quality.ToolName {
		return quality.Summary{}, false
	}
	if e.elided != nil && e.elided.verdict != "" {
		return quality.Summarize(e.elided.verdict)
	}
	return quality.Summarize(e.toolResult)
}

// gateVerdictLine is what a trim keeps of a gate result: the verdict line and
// the staleness line under it, verbatim, which are the whole of what a later
// reading takes. Empty for a row that carries no verdict to keep, which is
// every row but the gate's.
func gateVerdictLine(e entry) string {
	if _, ok := gateVerdict(e); !ok {
		return ""
	}
	kept := []string{firstLine(e.toolResult)}
	for _, line := range strings.Split(e.toolResult, "\n") {
		if strings.HasPrefix(line, "STALE:") {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

// checkOutcome is what one verification attempt came to.
type checkOutcome int

const (
	checkPassed checkOutcome = iota
	checkFailed
	// checkStopped is an attempt nobody let finish — the reader's cancel,
	// the ceiling. It is not a failure and it is not a pass: the run reached
	// no verdict, so there is nothing for the turn to report about it, and
	// the act itself is already on the grid as its own row.
	checkStopped
)

// checkAttempt is one thing a turn ran to check its own work: a quality-gate
// run, or a command whose exit code is a verdict about the code rather than
// about the shell.
type checkAttempt struct {
	// at is the attempt's position in the entries it was read out of, so a
	// caller holding the same slice can ask what a later verification
	// settled.
	at      int
	outcome checkOutcome
	// label names what ran, and counts is its tally — the two fields the
	// checks row and the review's verdict are built from.
	label, counts string
	// suite marks the quality gate. Its pass is the repository's own verdict
	// over the tree it ran against, which is what makes it the only thing
	// that can answer for an attempt before it, and its presence is what
	// puts the re-run offer on a row.
	suite bool
}

// resolvedChecks is a turn's verification read as one state: every attempt in
// the order it ran, and which of them settled the ones before it.
type resolvedChecks struct {
	attempts []checkAttempt
	// verified is where the pass that settled the span sits. Everything
	// before it has been answered.
	verified verification
}

// resolveChecks reads a turn's entries — or a whole transcript, for the rail,
// which asks the same question of a longer span — into that one state.
//
// A gate pass supersedes only what ran before it, because that is all it can
// speak for: the suite ran over the tree as it stood, and a command that
// failed afterwards is a fact about a tree the suite never saw. A stale pass
// supersedes nothing at all, for the reason it is marked stale — the run
// itself disowned the tree it is a verdict about.
func resolveChecks(es []entry) resolvedChecks {
	r := resolvedChecks{verified: unverified}
	for i, e := range es {
		switch s, isGate := gateVerdict(e); {
		case isGate:
			counts := fmt.Sprintf("%d/%d checks", s.Passed, s.Total)
			if s.Duration != "" {
				counts += " · " + s.Duration
			}
			if s.Stale {
				counts += " · stale"
			}
			a := checkAttempt{
				at:      i,
				outcome: checkFailed,
				label:   "quality gate " + s.Suite,
				counts:  counts,
				suite:   true,
			}
			if s.OK() {
				a.outcome, r.verified = checkPassed, verification(i)
			}
			r.attempts = append(r.attempts, a)
		case e.kind == entryCommand && isTestCommand(e.text):
			// A command has no tally of its own, so the exit code is the
			// count: it either came back clean or it did not. A command that
			// never exited says what ended it instead, the way its own row
			// does (activity.go).
			var counts []string
			outcome := checkPassed
			switch {
			case e.end.outcome != "":
				counts = append(counts, e.end.outcome)
				outcome = checkFailed
				if e.end.outcome == components.OutcomeStopped {
					outcome = checkStopped
				}
			case e.exitCode != 0:
				counts = append(counts, components.OutcomeExit(e.exitCode))
				outcome = checkFailed
			}
			if d := activityDuration(e.duration); d != "" {
				counts = append(counts, d)
			}
			r.attempts = append(r.attempts, checkAttempt{
				at:      i,
				outcome: outcome,
				label:   firstLine(e.text),
				counts:  strings.Join(counts, " · "),
			})
		}
	}
	return r
}

// standing are the attempts a reader is still owed an answer about: the
// verification that settled the turn and everything that ran after it. A
// stopped attempt is never one of them — it reached no verdict, and a row
// reporting a verdict nobody has is the failure this file exists to prevent.
func (r resolvedChecks) standing() []checkAttempt {
	var out []checkAttempt
	for _, a := range r.attempts {
		if a.outcome == checkStopped || r.verified.settled(a.at) {
			continue
		}
		out = append(out, a)
	}
	return out
}

// superseded are the failures a later verification answered. They are counted
// rather than dropped: the turn ran them, and a close row that said nothing
// about them would be hiding the work it took to get to green.
func (r resolvedChecks) superseded() int {
	n := 0
	for _, a := range r.attempts {
		if a.outcome == checkFailed && r.verified.settled(a.at) {
			n++
		}
	}
	return n
}

// suites reports whether any quality-gate run is behind this reading, which
// is what decides that there is a suite to offer again.
func (r resolvedChecks) suites() int {
	n := 0
	for _, a := range r.attempts {
		if a.suite {
			n++
		}
	}
	return n
}
