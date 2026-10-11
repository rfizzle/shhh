// Package doctor is the machine's setup report: one finding per check, the
// grid and the text report they are drawn into, and the checks that need
// nothing of the command layer but the store, the config and a few paths
// handed in. The checks that read the command layer's own state (the config
// layers, the flows, the sandbox policy, the project's wordings) stay beside
// it and return the same Finding.
//
// The host owns every diagnostic semantic and the screen owns none: what a
// check looks at, what its answer means, what it will cost the reader, and
// what the fix is are all written here, as pure readings of what was probed
// (docs/interface/surfaces.md#the-supporting-screens). A check is a function
// of what it was handed, not of this machine, so the whole report is testable
// without a sandbox, a provider key or a repository.
package doctor

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// Finding is one check's answer, before the runner stamps how long it
// took. Every field is a sentence the host wrote: the screen formats none of
// them.
type Finding struct {
	Subject     string
	Detail      string
	Outcome     string
	Consequence string
	FixLabel    string
	Fix         []string
	State       components.DoctorState
	// Action, ActionPrompt and Apply are the one thing a check can offer
	// beyond reading the machine. Almost every check leaves them empty: a
	// diagnostic looks and does not touch. A migration is the exception the
	// product makes on purpose — the change is one shhh can make correctly and
	// the reader cannot make quickly — and it is still asked about first
	// (docs/capabilities/configuration.md#a-migration-is-a-doctor-check).
	Action       string
	ActionPrompt string
	Apply        func() ([]string, error)
}

// Probe is one check: the name it wears in the grid's verb field, and
// the walk that answers it. Names are seven columns or fewer so the target
// beside them keeps its gap — the field is the vocabulary's own eight and
// nothing here widens it.
type Probe struct {
	Name string
	Run  func(context.Context, config.Config) Finding
	// Queued is what the row says before the probe has run, for a probe
	// whose name is not in doctorQueuedSubject's vocabulary — the servers
	// `shhh mcp` lists are named by the user, not by this file.
	Queued string
}

// RunChecks runs every check to completion, for the text report. The
// surface runs the same probes one message at a time instead, so that a run
// in progress is something the reader can watch.
func RunChecks(ctx context.Context, cfg config.Config, probes []Probe) []components.DoctorCheck {
	if ctx == nil {
		ctx = context.Background()
	}
	checks := make([]components.DoctorCheck, 0, len(probes))
	for _, probe := range probes {
		started := time.Now()
		checks = append(checks, Check(probe.Name, probe.Run(ctx, cfg), time.Since(started)))
	}
	return checks
}

// Check stamps a finding with the name it ran under and what it cost.
func Check(name string, f Finding, took time.Duration) components.DoctorCheck {
	return components.DoctorCheck{
		Name: name, Subject: f.Subject, Detail: f.Detail, Outcome: f.Outcome,
		Consequence: f.Consequence, Fix: f.Fix, FixLabel: f.FixLabel,
		Action: f.Action, ActionPrompt: f.ActionPrompt,
		State: f.State, Duration: Duration(took),
	}
}

// Duration is the 6-column field: blank under half a second, the same
// rule every activity row in the product follows. Most checks are a
// stat and a string comparison, so most of this column is deliberately empty.
func Duration(d time.Duration) string {
	if d < 500*time.Millisecond {
		return ""
	}
	if d < 10*time.Second {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}

// ReportOf is the run as a report: the same rows the surface draws,
// without the grid. It is what `--table` prints and what `[c]` copies, so a
// report pasted into an issue carries the consequences and the fixes too —
// those are the half of the run somebody else needs in order to help. `shhh
// mcp` prints the same rows over servers rather than checks, which is what
// the title and the nouns are for.
//
// The name column is pinned to eight rather than sized to the run, because
// eight is the discipline: a check named wider than the verb field is the
// signal that the vocabulary has drifted, and a column that grew to fit it
// would hide exactly that (docs/interface/principles.md#one-grid).
func ReportOf(title, one, many string, checks []components.DoctorCheck) report.Report {
	rows := make([]report.Row, 0, len(checks))
	for _, check := range checks {
		rows = append(rows, report.Row{
			State:       report.StateOf(check.State),
			Name:        check.Name,
			Subject:     check.Subject,
			Detail:      check.Detail,
			Outcome:     check.Outcome,
			Consequence: check.Consequence,
			Fix:         check.Fix,
		})
	}
	return report.Report{
		Title:    title,
		Subject:  report.Count(len(checks), one, many),
		Sections: []report.Section{{Rows: rows, NameWidth: NameWidth}},
		Tally:    SummaryLine(checks),
	}
}

// NameWidth is the verb field the doctor and mcp reports pin their name
// column to — the same eight columns the transcript's grid gives a verb.
const NameWidth = 8

// SummaryLine counts every outcome, the same tally the surface's foot
// row states.
func SummaryLine(checks []components.DoctorCheck) string {
	counts := map[components.DoctorState]int{}
	for _, check := range checks {
		counts[check.State]++
	}
	var parts []string
	for _, tally := range []struct {
		state components.DoctorState
		word  string
	}{
		{components.DoctorFailed, "failed"},
		{components.DoctorWarned, "warnings"},
		{components.DoctorPassed, "passed"},
		{components.DoctorSkipped, "not checked"},
	} {
		if n := counts[tally.state]; n > 0 {
			word := tally.word
			if n == 1 && tally.state == components.DoctorWarned {
				word = "warning"
			}
			parts = append(parts, fmt.Sprintf("%d %s", n, word))
		}
	}
	if len(parts) == 0 {
		return "no checks to run"
	}
	return strings.Join(parts, " · ")
}

// JoinDetail joins two halves of a target field with the product's own
// separator, and stands the one that exists alone where the other does not.
func JoinDetail(head, tail string) string {
	switch {
	case head == "":
		return tail
	case tail == "":
		return head
	}
	return head + " · " + tail
}
