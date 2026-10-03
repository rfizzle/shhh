package cli

// The doctor surface (
// docs/interface/surfaces.md#the-supporting-screens). `shhh code doctor`
// reported on the sandbox ladder and nothing else, while the design system
// named a `shhh doctor` covering the whole setup — the name had no command
// behind it. That was settled by promoting and widening: `shhh doctor` is a
// top-level command over ten checks, `shhh code doctor` stays as the way in
// from the coding agent, and `/sandbox doctor` is unchanged because in a
// session the question really is only about containment.
//
// The host owns every diagnostic semantic and the screen owns none: what a
// check looks at, what its answer means, what it will cost the reader, and
// what the fix is are all written here, as pure readings of what was probed.
// The probes are separated from the readings on purpose — `doctorSandbox` is
// a function of a `sandbox.Availability`, not of this machine — so the whole
// report is testable without a sandbox, a provider key or a git repository.
//
// Checks run one at a time, and the screen redraws after each: a run that is
// still going shows what has answered so far, one row `▸ running` and the
// rest `· queued`. That is the artboard's own picture, and it is also the
// honest one — the update check talks to the network and the provider check
// probes a local port, so a doctor run is not instant.
//
// `--table`, and any non-terminal stdout, prints the same report as text.
// That text is also what `[c]` copies, because the next thing that happens to
// a doctor run is that it gets pasted into an issue.

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/migrate"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/spf13/cobra"
)

// defaultDoctorWidth is what the surface is drawn at before the terminal has
// said how wide it is — the width the `Tools` artboard draws it at.
const defaultDoctorWidth = 110

// doctorGitTimeout bounds each git invocation. Reading a work tree's state is
// three cheap commands; a repository where they are not is a repository where
// waiting longer would not have helped either.
const doctorGitTimeout = 3 * time.Second

// ownsConfigError marks the one command that runs when the config file will
// not load. Every other command is refused at startup with the reason; this
// one reports the same reason as its config row, which is where the person
// who was just refused comes to read why.
const ownsConfigError = "owns-config-error"

func newDoctorCmd() *cobra.Command {
	cmd := doctorCommand("doctor", "Check this machine's shhh setup",
		"Run every setup check — the binary, the config file, any migration this machine still owes, the "+
			"provider and its key, the local store, command containment, container sandboxes, the workspace, "+
			"what this checkout may make a session load, the tools on PATH, durable memory, and whether a newer "+
			"shhh exists — and report each as a pass/fail row with the fix on the row that failed.",
		doctorProbes())
	cmd.Annotations = map[string]string{ownsConfigError: "yes"}
	// `--migrate` is the same offer the surface makes with `[a]`, for a
	// terminal that is not one: a script, a pipe, a machine being set up by
	// something other than a person. It is a flag rather than a `shhh
	// migrate` command because there is only ever one place to find out that
	// a migration is due, and it is this one.
	migrateFlag(cmd)
	return cmd
}

// migrateFlag adds `--migrate` to a doctor command: carry out every pending
// migration shhh can make itself, print what changed, and stop. Nothing else
// runs — a run that both migrated and reported would leave the reader unable
// to tell which half of the output described the machine before the change.
func migrateFlag(cmd *cobra.Command) {
	var apply bool
	cmd.Flags().BoolVar(&apply, "migrate", false,
		"carry out every pending migration and print what changed, instead of running the checks")
	inner := cmd.RunE
	cmd.RunE = func(c *cobra.Command, args []string) error {
		if !apply {
			return inner(c, args)
		}
		return runMigrations(c.OutOrStdout())
	}
}

// runMigrations is `shhh doctor --migrate`. It says what it is about to do
// before it does it, and names anything it will not do, so the output is a
// record rather than a result.
func runMigrations(out io.Writer) error {
	pending := migrate.Plan(migrationDir())
	r := report.Report{Title: "shhh doctor --migrate"}
	if len(pending) == 0 {
		return report.Fprint(out, emptyInto(r, "nothing to migrate",
			"this machine is on the current layout"))
	}
	r.Subject = countOf(len(pending), "migration", "migrations")
	var applyErr error
	for _, p := range pending {
		row := report.Row{State: report.Run, Subject: p.Name, Fix: p.Steps}
		if !p.Auto() {
			row.State, row.Outcome = report.Skip, "by hand"
			row.Consequence = "shhh cannot make this one for you"
			r.Sections = append(r.Sections, report.Section{Rows: []report.Row{row}})
			continue
		}
		lines, err := p.Apply()
		row.State, row.Outcome = report.Pass, "applied"
		row.Body = lines
		if err != nil {
			row.State, row.Outcome, applyErr = report.Fail, "failed", err
		}
		r.Sections = append(r.Sections, report.Section{Rows: []report.Row{row}})
		if applyErr != nil {
			break
		}
	}
	if err := report.Fprint(out, r); err != nil {
		return err
	}
	return applyErr
}

// doctorCommand builds a run over some set of the checks. `shhh doctor` takes
// all of them; `shhh code doctor` takes the containment pair, which is the
// scope that command has always had.
func doctorCommand(use, short, long string, probes []doctorProbe) *cobra.Command {
	var table bool

	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Long:  long,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := ConfigFrom(cmd.Context())
			if table || !term.IsTerminal(os.Stdout.Fd()) {
				return report.Fprint(cmd.OutOrStdout(),
					doctorReportOf("shhh doctor", "check", "checks",
						runDoctorChecks(cmd.Context(), cfg, probes)))
			}
			return runDoctorScreen(cmd.Context(), cfg, probes)
		},
	}

	cmd.Flags().BoolVar(&table, "table", false, "print the report as text instead of the surface")

	return cmd
}

// containmentProbes are the checks `shhh code doctor` is over: what wraps an
// approved command, what could run one in a container instead, and the image
// that container starts from.
func containmentProbes() []doctorProbe {
	return []doctorProbe{
		{name: "sandbox", run: probeSandbox},
		{name: "engine", run: probeEngine},
		{name: "image", run: probeImage},
	}
}

// doctorFinding is one check's answer, before the runner stamps how long it
// took. Every field is a sentence the host wrote: the screen formats none of
// them.
type doctorFinding struct {
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

// doctorProbe is one check: the name it wears in the grid's verb field, and
// the walk that answers it. Names are seven columns or fewer so the target
// beside them keeps its gap — the field is the vocabulary's own eight and
// nothing here widens it.
type doctorProbe struct {
	name string
	run  func(context.Context, config.Config) doctorFinding
	// queued is what the row says before the probe has run, for a probe
	// whose name is not in doctorQueuedSubject's vocabulary — the servers
	// `shhh mcp` lists are named by the user, not by this file.
	queued string
}

// doctorProbes is every check, in the order they run and the order they read:
// what shhh is and the terminal it is drawn in, what it was configured with,
// what it can talk to, then what it can do to this machine, and last what it
// might become.
func doctorProbes() []doctorProbe {
	return []doctorProbe{
		{name: "binary", run: probeBinary},
		{name: "keys", run: probeOptionKey},
		{name: "keymap", run: probeKeymap},
		{name: "config", run: probeConfig},
		{name: "migrate", run: probeMigrate},
		{name: "model", run: probeModel},
		{name: "flows", run: probeFlows},
		{name: "search", run: probeSearch},
		{name: "hosts", run: probeHosts},
		{name: "store", run: probeStore},
		{name: "logs", run: probeLogs},
		{name: "reports", run: probeReports},
		{name: "otel", run: probeOtel},
		{name: "sandbox", run: probeSandbox},
		{name: "engine", run: probeEngine},
		{name: "image", run: probeImage},
		{name: "git", run: probeGit},
		{name: "project", run: probeProject},
		{name: "trust", run: probeTrust},
		{name: "needs", run: probeToolchain},
		{name: "hooks", run: probeHooks},
		{name: "prompts", run: probePrompts},
		{name: "tools", run: probeTools},
		{name: "memory", run: probeMemory},
		{name: "update", run: probeUpdate},
	}
}

// runDoctorChecks runs every check to completion, for the text report. The
// surface runs the same probes one message at a time instead, so that a run
// in progress is something the reader can watch.
func runDoctorChecks(ctx context.Context, cfg config.Config, probes []doctorProbe) []components.DoctorCheck {
	if ctx == nil {
		ctx = context.Background()
	}
	checks := make([]components.DoctorCheck, 0, len(probes))
	for _, probe := range probes {
		started := time.Now()
		checks = append(checks, doctorCheck(probe.name, probe.run(ctx, cfg), time.Since(started)))
	}
	return checks
}

// doctorCheck stamps a finding with the name it ran under and what it cost.
func doctorCheck(name string, f doctorFinding, took time.Duration) components.DoctorCheck {
	return components.DoctorCheck{
		Name: name, Subject: f.Subject, Detail: f.Detail, Outcome: f.Outcome,
		Consequence: f.Consequence, Fix: f.Fix, FixLabel: f.FixLabel,
		Action: f.Action, ActionPrompt: f.ActionPrompt,
		State: f.State, Duration: doctorDuration(took),
	}
}

// doctorDuration is the 6-column field: blank under half a second, the same
// rule every activity row in the product follows. Most checks are a
// stat and a string comparison, so most of this column is deliberately empty.
func doctorDuration(d time.Duration) string {
	if d < 500*time.Millisecond {
		return ""
	}
	if d < 10*time.Second {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}

// doctorReportOf is the run as a report: the same rows the surface draws,
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
func doctorReportOf(title, one, many string, checks []components.DoctorCheck) report.Report {
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
		Subject:  countOf(len(checks), one, many),
		Sections: []report.Section{{Rows: rows, NameWidth: doctorNameWidth}},
		Tally:    doctorSummaryLine(checks),
	}
}

// doctorNameWidth is the verb field the doctor and mcp reports pin their name
// column to — the same eight columns the transcript's grid gives a verb.
const doctorNameWidth = 8

// doctorSummaryLine counts every outcome, the same tally the surface's foot
// row states.
func doctorSummaryLine(checks []components.DoctorCheck) string {
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

// joinDetail joins two halves of a target field with the product's own
// separator, and stands the one that exists alone where the other does not.
func joinDetail(head, tail string) string {
	switch {
	case head == "":
		return tail
	case tail == "":
		return head
	}
	return head + " · " + tail
}
