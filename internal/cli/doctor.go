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
	"io"
	"os"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/rfizzle/shhh/internal/cli/doctor"
	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/migrate"
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
func doctorCommand(use, short, long string, probes []doctor.Probe) *cobra.Command {
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
					doctor.ReportOf("shhh doctor", "check", "checks",
						doctor.RunChecks(cmd.Context(), cfg, probes)))
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
func containmentProbes() []doctor.Probe {
	return []doctor.Probe{
		{Name: "sandbox", Run: probeSandbox},
		{Name: "engine", Run: probeEngine},
		{Name: "image", Run: probeImage},
	}
}

// doctorProbes is every check, in the order they run and the order they read:
// what shhh is and the terminal it is drawn in, what it was configured with,
// what it can talk to, then what it can do to this machine, and last what it
// might become.
func doctorProbes() []doctor.Probe {
	return []doctor.Probe{
		{Name: "binary", Run: probeBinary},
		{Name: "keys", Run: doctor.OptionKey},
		{Name: "keymap", Run: probeKeymap},
		{Name: "theme", Run: probeTheme},
		{Name: "config", Run: probeConfig},
		{Name: "migrate", Run: probeMigrate},
		{Name: "model", Run: probeModel},
		{Name: "flows", Run: probeFlows},
		{Name: "search", Run: probeSearch},
		{Name: "hosts", Run: probeHosts},
		{Name: "store", Run: doctor.Store(openStore)},
		{Name: "logs", Run: doctor.Logs(logPath)},
		{Name: "reports", Run: doctor.Reports(reportsDir)},
		{Name: "otel", Run: doctor.Otel},
		{Name: "sandbox", Run: probeSandbox},
		{Name: "engine", Run: probeEngine},
		{Name: "image", Run: probeImage},
		{Name: "git", Run: probeGit},
		{Name: "project", Run: probeProject},
		{Name: "trust", Run: probeTrust},
		{Name: "needs", Run: probeToolchain},
		{Name: "hooks", Run: probeHooks},
		{Name: "prompts", Run: probePrompts},
		{Name: "tools", Run: probeTools},
		{Name: "memory", Run: probeMemory},
		{Name: "update", Run: probeUpdate},
	}
}
