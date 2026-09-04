package eval

// Keeping a run, so the next one can be read against it.
//
// A suite costs real requests, which makes every run a thing worth keeping:
// the reader who wants to know whether a prompt edit helped has one run in
// front of them and the other in their memory of a report they read last
// week. Memory is the wrong place for a median. So a run can be written down
// and a later one compared with it, and the comparison is what is read.
//
// What is written down is what the report already shows — the verdict and the
// medians beside it — plus, for a case with no workspace, its rates. Those
// are rows of their own rather than a pass count: a classifier that let one
// more action through is a control failing a little further open, and a
// verdict that stayed "failed" either side says nothing about it.
// See docs/capabilities/evals.md#a-run-can-be-compared-with-the-last-one.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// BaselineVersion is the format of a written baseline. It is compared on read
// so that a file from a future shhh is refused with a sentence rather than
// silently decoded into zero medians, which would read as a run in which
// everything got faster.
const BaselineVersion = 1

// MinRateSamples is how many samples a side needs before a delta prints a
// rate. Under it the row carries its counts and no ratio.
//
// A rate over a handful of samples is a random number with a percent sign:
// one attempt either way moves three samples by thirty-three points, and a
// row that prints that invites acting on it. Five is the smallest denominator
// where a single sample moves the figure by no more than twenty points, which
// is the coarsest reading still worth calling a reading.
//
// The samples are attempts for a workspace case and rows for a table one,
// because that is where each shape's independent draws are: a twenty-row
// table put once is twenty samples, and repeating it is more of them.
//
// Nothing about this is particular to a suite: a comparison over the session
// record owes its reader the same restraint against its own cohort counts.
// When a second surface needs the rule, the two should share one constant
// rather than keep two that can drift apart.
// See docs/capabilities/evals.md#a-rate-needs-enough-samples-to-be-a-rate.
const MinRateSamples = 5

// Baseline is a run written down.
type Baseline struct {
	Version int `json:"version"`
	// Model is what was measured. Two baselines on different models compare
	// fine — that is most of what a comparison is for — but the report says
	// so, because a reader looking for the effect of a prompt edit should not
	// discover the model moved underneath it.
	Model    string         `json:"model"`
	Recorded time.Time      `json:"recorded"`
	Cases    []CaseBaseline `json:"cases"`
}

// CaseBaseline is one case's line in a baseline: its verdict, and the medians
// the report prints beside it.
type CaseBaseline struct {
	Name string `json:"name"`
	Kind Kind   `json:"kind"`
	// Verdict is the word, not the enum: a baseline is read by a person
	// editing prompts, and a file of small integers is one they cannot check
	// against the report they are holding.
	Verdict  string  `json:"verdict"`
	Attempts int     `json:"attempts"`
	Passes   int     `json:"passes"`
	Rounds   float64 `json:"median_rounds"`
	Tokens   float64 `json:"median_tokens"`
	Seconds  float64 `json:"median_seconds"`
	Cost     float64 `json:"cost"`
	Priced   bool    `json:"priced"`
	// Table is a table case's rates, and nil for a workspace case.
	Table *TableBaseline `json:"table,omitempty"`
}

// TableBaseline is a table case's outcomes, counted apart the way the report
// counts them. FalseAllow and FalseDeny are never summed into one figure
// here either: the file is what a later run is compared against, and a
// baseline that had already added them together could not tell a control
// failing further open from one merely getting fussier.
type TableBaseline struct {
	Rows       int `json:"rows"`
	Correct    int `json:"correct"`
	FalseAllow int `json:"false_allow"`
	FalseDeny  int `json:"false_deny"`
	Wrong      int `json:"wrong"`
	Unanswered int `json:"unanswered"`
}

// parseVerdict is the inverse, refusing a word it does not know rather than
// defaulting to one: a typo that silently became "failed" would report a
// regression that never happened.
func parseVerdict(s string) (Verdict, error) {
	for _, v := range []Verdict{Passed, Flaky, Failed, Skipped, Errored} {
		if v.String() == s {
			return v, nil
		}
	}
	return 0, fmt.Errorf("%q is not a verdict this suite gives", s)
}

// Baseline is the summary as a file: the same numbers the report prints,
// nothing derived from them, and nothing about the machine it ran on.
func (s Summary) Baseline() Baseline {
	b := Baseline{Version: BaselineVersion, Model: s.Model, Recorded: time.Now().UTC()}
	for _, res := range s.Results {
		c := CaseBaseline{
			Name:     res.Case.Name,
			Kind:     res.Case.Kind,
			Verdict:  res.Verdict().String(),
			Attempts: len(res.Attempts),
			Passes:   res.Passes(),
			Rounds:   res.MedianRounds(),
			Tokens:   res.Median(func(a Attempt) float64 { return float64(a.TokensIn + a.TokensOut) }),
			Seconds:  res.Median(func(a Attempt) float64 { return a.Elapsed.Seconds() }),
		}
		if c.Kind == "" {
			c.Kind = KindWorkspace
		}
		c.Cost, c.Priced = res.Cost()
		if score, ok := res.Score(); ok {
			c.Table = &TableBaseline{
				Rows:       score.Rows(),
				Correct:    score.Correct(),
				FalseAllow: score.FalseAllow(),
				FalseDeny:  score.FalseDeny(),
				Wrong:      score.Wrong(),
				Unanswered: score.Unanswered(),
			}
		}
		b.Cases = append(b.Cases, c)
	}
	return b
}

// WriteBaseline saves a baseline where the reader asked for it, creating the
// directory above it. It is indented because the file is meant to be read and
// diffed by hand as much as by this command.
func WriteBaseline(path string, b Baseline) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("cannot make room for the baseline: %w", err)
		}
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot write the baseline: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("cannot write the baseline: %w", err)
	}
	return nil
}

// ReadBaseline loads one, refusing a file it cannot read as the numbers it
// claims to be.
func ReadBaseline(path string) (Baseline, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Baseline{}, fmt.Errorf("cannot read the baseline: %w", err)
	}
	var b Baseline
	if err := json.Unmarshal(data, &b); err != nil {
		return Baseline{}, fmt.Errorf("%s: %w", path, err)
	}
	if b.Version != BaselineVersion {
		return Baseline{}, fmt.Errorf("%s: written by a different shhh (format %d, this one reads %d)",
			path, b.Version, BaselineVersion)
	}
	if len(b.Cases) == 0 {
		return Baseline{}, fmt.Errorf("%s: no cases in it, so there is nothing to compare against", path)
	}
	for _, c := range b.Cases {
		if _, err := parseVerdict(c.Verdict); err != nil {
			return Baseline{}, fmt.Errorf("%s: %s: %w", path, c.Name, err)
		}
	}
	return b, nil
}

// Change is what a case did between two runs.
type Change int

const (
	// Unchanged — the verdict held and nothing moved far enough to read.
	Unchanged Change = iota
	// Improved and Regressed are the two directions.
	Improved
	Regressed
	// Incomparable — a side that was skipped or never ran has no numbers,
	// and calling that an improvement because the failures stopped would be
	// the worst reading available.
	Incomparable
)

// Delta is one case between two runs.
type Delta struct {
	Name          string
	Before, After CaseBaseline
	Change        Change
	// Why is what decided the change, in the words the row prints.
	Why string
}

// Comparison is a whole run against a whole baseline.
type Comparison struct {
	Before, After Baseline
	Cases         []Delta
}

// Tally counts the deltas by direction.
func (c Comparison) Tally() (improved, regressed, unchanged, incomparable int) {
	for _, d := range c.Cases {
		switch d.Change {
		case Improved:
			improved++
		case Regressed:
			regressed++
		case Incomparable:
			incomparable++
		default:
			unchanged++
		}
	}
	return improved, regressed, unchanged, incomparable
}

// Compare reads one run against another, in this run's case order.
//
// It refuses two runs over different case sets rather than comparing the
// overlap. A suite that gained a case is a suite whose totals moved for a
// reason that is not the change being measured, and quietly dropping the case
// from the comparison would hide exactly that.
func Compare(before, after Baseline) (Comparison, error) {
	byName := make(map[string]CaseBaseline, len(before.Cases))
	for _, c := range before.Cases {
		byName[c.Name] = c
	}
	ran := make(map[string]bool, len(after.Cases))
	var added []string
	for _, c := range after.Cases {
		ran[c.Name] = true
		if _, ok := byName[c.Name]; !ok {
			added = append(added, c.Name)
		}
	}
	var missing []string
	for _, c := range before.Cases {
		if !ran[c.Name] {
			missing = append(missing, c.Name)
		}
	}
	if len(missing) > 0 || len(added) > 0 {
		sort.Strings(missing)
		sort.Strings(added)
		var parts []string
		if len(added) > 0 {
			parts = append(parts, "not in the baseline: "+strings.Join(added, ", "))
		}
		if len(missing) > 0 {
			parts = append(parts, "in the baseline and not in this run: "+strings.Join(missing, ", "))
		}
		return Comparison{}, fmt.Errorf("the two runs measured different cases, so their totals are not comparable — %s",
			strings.Join(parts, "; "))
	}

	cmp := Comparison{Before: before, After: after}
	for _, a := range after.Cases {
		cmp.Cases = append(cmp.Cases, delta(byName[a.Name], a))
	}
	return cmp, nil
}

// roundsBand is how far a median may move before the delta calls it a
// direction.
//
// A median off a handful of attempts wobbles: one attempt that read a file
// twice moves it by a round, and a suite that called that a regression would
// cry wolf every run. A quarter is chosen and not derived — it is more than
// the round or two one attempt moves a median of three, and well under the
// doubling a prompt edit produces when it produces anything. Once enough runs
// are kept to measure the wobble, measure it and set this to what it says.
const roundsBand = 0.25

// delta decides one case's direction.
//
// The order is the order a reader cares in. A verdict that moved is the
// finding, whatever the numbers did — a case that now fails is not redeemed
// by having failed in fewer rounds. Under an unchanged verdict, a control
// that let more through comes next, because a classifier failing further open
// is the one regression here that costs something outside the suite. The
// medians are last: they are what says a change was paid for.
func delta(before, after CaseBaseline) Delta {
	d := Delta{Name: after.Name, Before: before, After: after}

	bv, bErr := parseVerdict(before.Verdict)
	av, aErr := parseVerdict(after.Verdict)
	switch {
	case bErr != nil || aErr != nil, !hasNumbers(bv), !hasNumbers(av):
		d.Change, d.Why = Incomparable, incomparableWhy(before, after)
		return d
	case rank(av) > rank(bv):
		d.Change, d.Why = Improved, verdictMeans(av)
		return d
	case rank(av) < rank(bv):
		d.Change, d.Why = Regressed, verdictMeans(av)
		return d
	}

	if before.Table != nil && after.Table != nil {
		switch {
		case after.Table.FalseAllow > before.Table.FalseAllow:
			d.Change = Regressed
			d.Why = fmt.Sprintf("%d → %d false allow — the control is further open",
				before.Table.FalseAllow, after.Table.FalseAllow)
			return d
		case after.Table.FalseAllow < before.Table.FalseAllow:
			d.Change = Improved
			d.Why = fmt.Sprintf("%d → %d false allow", before.Table.FalseAllow, after.Table.FalseAllow)
			return d
		case after.Table.Unanswered > before.Table.Unanswered:
			d.Change = Regressed
			d.Why = fmt.Sprintf("%d → %d with no answer — a call returning nothing is broken, not cautious",
				before.Table.Unanswered, after.Table.Unanswered)
			return d
		case after.Table.Correct != before.Table.Correct:
			d.Change = Improved
			if after.Table.Correct < before.Table.Correct {
				d.Change = Regressed
			}
			d.Why = fmt.Sprintf("%d → %d of %d correct", before.Table.Correct, after.Table.Correct, after.Table.Rows)
			return d
		}
	}

	if before.Rounds > 0 && after.Rounds > 0 {
		if moved := (after.Rounds - before.Rounds) / before.Rounds; moved >= roundsBand || moved <= -roundsBand {
			d.Change = Improved
			if moved > 0 {
				d.Change = Regressed
			}
			d.Why = fmt.Sprintf("%s → %s rounds for the same verdict",
				FormatRounds(before.Rounds), FormatRounds(after.Rounds))
			return d
		}
	}
	return d
}

// verdictMeans is what the case's new verdict costs the reader, for the line
// under the row. The pair of words is already in the row above it; what a
// reader needs beside them is what the move means, and "passed → failed"
// printed twice is not that.
func verdictMeans(after Verdict) string {
	switch after {
	case Failed:
		return "a task this setup finished before and now does not"
	case Flaky:
		return "a task this setup can now lose, which is a different fact from either passing or failing"
	}
	return "a task this setup could not finish reliably before"
}

// hasNumbers reports whether a verdict has any behind it. A skipped case
// never ran and an errored one broke, and neither is evidence about the task.
func hasNumbers(v Verdict) bool { return v == Passed || v == Flaky || v == Failed }

func incomparableWhy(before, after CaseBaseline) string {
	if before.Verdict == after.Verdict {
		return after.Verdict + " both times, so there is nothing to compare"
	}
	return before.Verdict + " → " + after.Verdict + ", and one of those has no numbers behind it"
}

// rank orders the three verdicts that have numbers behind them, so a move
// between any two of them has a direction.
func rank(v Verdict) int {
	switch v {
	case Passed:
		return 2
	case Flaky:
		return 1
	}
	return 0
}

// Samples is the smaller of the two sides' independent draws, which is what a
// rate over the pair is worth reading against. A table's draws are its rows;
// a workspace case's are its attempts.
func (d Delta) Samples() int {
	before, after := d.Before.Attempts, d.After.Attempts
	if d.Before.Table != nil && d.After.Table != nil {
		before, after = d.Before.Table.Rows, d.After.Table.Rows
	}
	return min(before, after)
}

// ReadableRate reports whether this case has enough samples for a ratio to
// mean anything. Where it does not, the row prints its counts and no rate.
func (d Delta) ReadableRate() bool { return d.Samples() >= MinRateSamples }
