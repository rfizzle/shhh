// Package eval measures whether a session actually does the work.
//
// Everything else in this repository tests the harness: that the loop records
// what it dispatched, that a tier cannot be crossed, that a row wraps at the
// right column. None of it can answer the question that decides whether a
// prompt edit was an improvement — given a real task in a real checkout, does
// the agent finish it, and at what cost. That question has one honest form:
// run the task, then check the workspace, and let the check say.
//
// So a case is a workspace and a sentence, and its verdict is a command the
// case author wrote. Nothing here inspects the transcript to decide whether
// the work was done — a rubric over the model's own words grades the
// explanation rather than the change, and an agent that says it fixed the bug
// scores exactly as well as one that fixed it. The tests have to pass.
//
// The numbers beside the verdict are what turn a pass into a comparison. A
// change that keeps every case passing and doubles the rounds is a
// regression, and it is invisible to anything that only counts passes.
//
// The calls a session makes beside the coding loop leave no workspace behind
// to check, so they get the second shape in table.go: a labelled table, an
// answer from a closed set, and a comparison rather than a judgement.
// See docs/capabilities/evals.md.
package eval

import (
	"fmt"
	"time"
)

// Case is one task: a workspace to do it in, a sentence asking for it, and
// the command that decides whether it was done — or, where Kind says so, a
// labelled table put to one of the calls a session makes beside the coding
// loop (table.go).
type Case struct {
	// Name identifies the case in a report and on the command line. It
	// defaults to the directory the case was loaded from.
	Name string
	// Kind is what this case measures. The fields below that speak of a
	// workspace are KindWorkspace's; Rows is what the other kinds are scored
	// against, and they have no workspace, no prompt and no check.
	Kind Kind
	Rows []Row
	// Dir is the case directory, and Workspace the fixture inside it that is
	// copied for each attempt. A case never runs in its own directory: a run
	// edits files, and a case that graded the second attempt against the
	// first one's leftovers would drift a little further every time it ran.
	Dir       string
	Workspace string
	// Prompt is the task, as a person would type it.
	Prompt string
	// Check is the argv run in the workspace after the agent stops. Exit zero
	// is the pass, and nothing else about it is interpreted.
	Check []string
	// Skip, when set, is why this case is not run — a case that needs a
	// toolchain this machine does not have says so rather than failing as
	// though the agent were at fault.
	Skip string
}

// Attempt is one run of one case. A case is run more than once because the
// thing being measured is not deterministic, and a single pass is not
// evidence of anything.
type Attempt struct {
	// Passed is the check's verdict. Err is set when the run never got as far
	// as being checked — the session failed, or the attempt timed out — which
	// is a different fact from a check that ran and said no.
	Passed bool
	Err    error
	// CheckOutput is what the check printed when it failed, which is the only
	// thing that says why.
	CheckOutput string
	// Rounds is how many tool-call rounds the turn took, and Calls how many
	// tools it asked for across them. Both are read from the transcript, so
	// they measure the work rather than the account of it. A table attempt
	// leaves both zero: it is one request per row and no loop at all, and a
	// row count reported as rounds would be compared against a coding turn's.
	Rounds int
	Calls  int
	// TokensIn and TokensOut are the session's usage; Cost is what the price
	// table made of it, and Priced whether the table knew the model. A table
	// attempt sums them across its rows and is priced the same way, so a
	// model or ceiling change reads as a cost change here too and not only as
	// a score change.
	TokensIn  int
	TokensOut int
	Cost      float64
	Priced    bool
	Elapsed   time.Duration
	// Score is a table attempt's row-by-row outcome, and nil for a workspace
	// attempt. Passed above is its hard verdict: every row answered, and
	// every answer one its row accepts.
	Score *Score
}

// Result is every attempt at one case.
type Result struct {
	Case     Case
	Attempts []Attempt
}

// Score is every attempt's rows together, which is what a table case's row in
// the report is read from: the rates are what the case is for, and a rate off
// one attempt of twenty rows is a rate off twenty samples whichever way the
// repeat count is set.
func (r Result) Score() (Score, bool) {
	if !r.Case.Kind.IsTable() {
		return Score{}, false
	}
	merged := Score{Kind: r.Case.Kind}
	for _, a := range r.Attempts {
		if a.Score != nil {
			merged.Answers = append(merged.Answers, a.Score.Answers...)
		}
	}
	return merged, len(merged.Answers) > 0
}

// Passes is how many attempts the check accepted.
func (r Result) Passes() int {
	n := 0
	for _, a := range r.Attempts {
		if a.Passed {
			n++
		}
	}
	return n
}

// Verdict is the case's outcome across its attempts: passed when every
// attempt passed, failed when none did, and flaky in between.
//
// Flaky is a verdict of its own rather than a rounding of the pass rate,
// because it is the one that says something about the harness. A case that
// passes four times in five is not a case that mostly works; it is a task the
// session finds hard enough to lose, and averaging that into a percentage
// hides exactly the cases worth reading.
type Verdict int

const (
	Failed Verdict = iota
	Flaky
	Passed
	Skipped
	Errored
)

func (r Result) Verdict() Verdict {
	if r.Case.Skip != "" {
		return Skipped
	}
	if len(r.Attempts) == 0 {
		return Errored
	}
	// A case whose every attempt failed before it could be checked says
	// nothing about the agent, and must not be read as a failed task.
	ran := 0
	for _, a := range r.Attempts {
		if a.Err == nil {
			ran++
		}
	}
	if ran == 0 {
		return Errored
	}
	switch passes := r.Passes(); {
	case passes == len(r.Attempts):
		return Passed
	case passes == 0:
		return Failed
	default:
		return Flaky
	}
}

// Median is the middle value of what f returns across the attempts that ran,
// which is the summary statistic to use on a handful of samples: one attempt
// that spent three times the rounds moves a mean and does not move this.
func (r Result) Median(f func(Attempt) float64) float64 {
	var vals []float64
	for _, a := range r.Attempts {
		if a.Err == nil {
			vals = append(vals, f(a))
		}
	}
	if len(vals) == 0 {
		return 0
	}
	sortFloats(vals)
	mid := len(vals) / 2
	if len(vals)%2 == 1 {
		return vals[mid]
	}
	return (vals[mid-1] + vals[mid]) / 2
}

// MedianRounds and Cost are the two numbers a comparison is usually read on:
// how much work the task took, and what it cost.
func (r Result) MedianRounds() float64 {
	return r.Median(func(a Attempt) float64 { return float64(a.Rounds) })
}

// Cost is the total across every attempt, and Priced whether the table knew
// the model well enough for that total to mean anything.
func (r Result) Cost() (total float64, priced bool) {
	for _, a := range r.Attempts {
		total += a.Cost
		priced = priced || a.Priced
	}
	return total, priced
}

// Summary is a whole run: what was measured, and how it came out.
type Summary struct {
	Model   string
	Results []Result
}

// Tally counts the results by verdict.
func (s Summary) Tally() (passed, flaky, failed, skipped, errored int) {
	for _, r := range s.Results {
		switch r.Verdict() {
		case Passed:
			passed++
		case Flaky:
			flaky++
		case Failed:
			failed++
		case Skipped:
			skipped++
		case Errored:
			errored++
		}
	}
	return passed, flaky, failed, skipped, errored
}

// Cost is what the whole run cost, and whether that number means anything.
func (s Summary) Cost() (total float64, priced bool) {
	for _, r := range s.Results {
		c, p := r.Cost()
		total += c
		priced = priced || p
	}
	return total, priced
}

// Elapsed is the wall clock across every attempt.
func (s Summary) Elapsed() time.Duration {
	var d time.Duration
	for _, r := range s.Results {
		for _, a := range r.Attempts {
			d += a.Elapsed
		}
	}
	return d
}

// Err is the run's own failure, for a summary that could not be produced.
func (s Summary) Err() error {
	if len(s.Results) == 0 {
		return fmt.Errorf("no cases ran")
	}
	return nil
}

// sortFloats is an insertion sort, which is the right one for the handful of
// attempts a case ever has and saves the import.
func sortFloats(v []float64) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j] < v[j-1]; j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
}
