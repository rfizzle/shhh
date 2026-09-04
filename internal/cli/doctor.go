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
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"
	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/clipboard"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/hook"
	"github.com/rfizzle/shhh/internal/lsp"
	"github.com/rfizzle/shhh/internal/memory"
	"github.com/rfizzle/shhh/internal/migrate"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/reports"
	"github.com/rfizzle/shhh/internal/resolve"
	"github.com/rfizzle/shhh/internal/sandbox"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/structural"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/update"
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
	// The offer on the trust row, for a terminal that is not one. It is
	// under the doctor because the doctor is where the withheld list is
	// reported, and an answer given somewhere the question is not asked is
	// an answer given blind.
	cmd.AddCommand(&cobra.Command{
		Use:   "trust",
		Short: "Let this checkout's skills, agent profiles, quality suites and servers load",
		Long: "Record that the checkout you are standing in may put what it declares into a session: its skills, " +
			"agent profiles, quality suites, hooks and MCP servers. They run as you. The answer covers the checkout " +
			"as it stands, so an edit to any of those files asks again.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return runSetTrust(cmd, true) },
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "distrust",
		Short: "Withdraw that",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, args []string) error { return runSetTrust(cmd, false) },
	})
	return cmd
}

// runSetTrust records or withdraws this checkout's answer and prints it.
func runSetTrust(cmd *cobra.Command, trust bool) error {
	db, err := openStore()
	if err != nil {
		return fmt.Errorf("the local store is unavailable, so trust cannot be recorded: %w", err)
	}
	defer db.Close()
	note, err := setProjectTrust(db, projectTrust(), trust)
	if err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), note)
	return nil
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
			return runDoctorScreen(cfg, probes)
		},
	}

	cmd.Flags().BoolVar(&table, "table", false, "print the report as text instead of the surface")

	return cmd
}

// containmentProbes are the two checks `shhh code doctor` has always been
// over: what wraps an approved command, and what could run one in a container
// instead.
func containmentProbes() []doctorProbe {
	return []doctorProbe{
		{name: "sandbox", run: probeSandbox},
		{name: "engine", run: probeEngine},
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
// what shhh is, what it was configured with, what it can talk to, then what
// it can do to this machine, and last what it might become.
func doctorProbes() []doctorProbe {
	return []doctorProbe{
		{name: "binary", run: probeBinary},
		{name: "config", run: probeConfig},
		{name: "migrate", run: probeMigrate},
		{name: "model", run: probeModel},
		{name: "store", run: probeStore},
		{name: "logs", run: probeLogs},
		{name: "reports", run: probeReports},
		{name: "otel", run: probeOtel},
		{name: "sandbox", run: probeSandbox},
		{name: "engine", run: probeEngine},
		{name: "git", run: probeGit},
		{name: "project", run: probeProject},
		{name: "trust", run: probeTrust},
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

// The probes. Each one gathers what it can see and hands it to a reading; the
// reading is where the judgement lives, and it is a pure function so the
// whole report can be tested on a machine that has none of this.

func probeBinary(context.Context, config.Config) doctorFinding {
	path, err := os.Executable()
	if err != nil {
		path = ""
	}
	return doctorBinary(version, runtime.GOOS, runtime.GOARCH, path)
}

// doctorBinary states what is running. It is the one check that cannot fail:
// a report that could not say which binary produced it would be a report
// nobody could act on, so it leads.
func doctorBinary(version, goos, goarch, path string) doctorFinding {
	f := doctorFinding{Subject: "shhh " + version, Detail: goos + "/" + goarch, Outcome: "ok"}
	if path != "" {
		f.Detail += " · " + shortPath(path)
	}
	if version == "dev" || version == "" {
		// A dev build is not a problem, but every version-shaped answer below
		// it — the update check especially — is about to say nothing, and the
		// reader should know why before they read those rows.
		f.Subject = "shhh (dev build)"
		f.Detail = goos + "/" + goarch
		if path != "" {
			f.Detail += " · " + shortPath(path)
		}
		f.Outcome = "unversioned"
	}
	return f
}

// probeConfig reads the files again rather than taking the config it was
// handed: this is the one command that runs when the load failed (root.go
// lets it through on ownsConfigError), and the config in hand is then the
// zero value with the reason left behind in the startup path.
func probeConfig(_ context.Context, _ config.Config) doctorFinding {
	paths := config.Paths()
	read := ""
	for _, p := range paths {
		// The same test the load makes: a file that is there but cannot be
		// read is the file the row is about, not a missing one.
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			read = p
			break
		}
	}
	cfg, err := config.LoadFrom(paths...)
	var proj config.Project
	if err == nil {
		// The same layering a session would do, refusal included: the doctor
		// is where a file that stops every command is read, and a checkout's
		// file can stop them the same way the user's can.
		cfg, proj, err = layerProjectConfig(cfg, workingDir())
		if err != nil {
			read = config.ProjectPath(workingDir())
		}
	}
	return doctorConfig(read, paths, cfg, proj, err)
}

// doctorConfig says which file was read and what it set. No file at all is
// not a failure — shhh runs on its defaults — but the row says so plainly
// rather than being left out, because "why is this on" is the question a
// setup check gets asked. A file that would not load is the row's failure,
// worded as the refusal every other command gave, so the doctor is where the
// person who was just refused reads why.
func doctorConfig(read string, paths []string, cfg config.Config, proj config.Project, err error) doctorFinding {
	if err != nil {
		f := doctorFinding{
			Subject: shortPath(read), Detail: err.Error(),
			Outcome: "refused", State: components.DoctorFailed,
			Consequence: "no command starts until the file loads",
			FixLabel:    "fix the file",
			Fix:         []string{"edit " + shortPath(read)},
		}
		// The offer goes on a fix line of its own rather than in the
		// detail, which clips at the column's width: a row that reads
		// `unknown key "behaviour" (did you …` has cut off the one word
		// the reader came for.
		var unknown *config.UnknownKeyError
		if errors.As(err, &unknown) {
			names := make([]string, len(unknown.Keys))
			f.Fix = f.Fix[:0]
			for i, k := range unknown.Keys {
				names[i] = fmt.Sprintf("%q", k.Key)
				if k.Nearest != "" {
					f.Fix = append(f.Fix, "rename "+k.Key+" to "+k.Nearest)
				} else {
					f.Fix = append(f.Fix, "remove "+k.Key+": no setting reads it")
				}
			}
			noun := "unknown key "
			if len(names) > 1 {
				noun = "unknown keys "
			}
			f.Detail = noun + strings.Join(names, ", ")
		}
		return f
	}
	if read == "" {
		f := doctorFinding{
			Subject: "no config file", Detail: "every setting is on its default",
			Outcome: "defaults", State: components.DoctorSkipped,
		}
		if len(paths) > 0 {
			f.FixLabel = "show where one would go"
			f.Fix = []string{
				"shhh config          edit the settings and write the file",
				"it would be written to " + shortPath(paths[0]),
			}
		}
		return f
	}
	f := doctorFinding{
		Subject: shortPath(read),
		Detail:  countOf(configSettingsSet(cfg), "setting set", "settings set"),
		Outcome: "ok",
	}
	return withLiteralKeyWarning(withProjectFile(f, proj), cfg)
}

// withProjectFile names the checkout's own settings file on the row and the
// keys it decided. Both files are named because the value in force can come
// from either, and a row naming one of two files is a row that sends the
// reader to edit the wrong one; the keys go on the fix lines rather than in
// the detail, which clips at its column's width.
func withProjectFile(f doctorFinding, proj config.Project) doctorFinding {
	if !proj.Loaded() {
		return f
	}
	f.Detail = joinDetail(f.Detail, proj.Display+" sets "+countOf(len(proj.Keys), "key", "keys"))
	if len(proj.Keys) == 0 {
		return f
	}
	// One line naming the file and its keys, so it still says whose keys
	// these are under whatever label the credential warning below may put on
	// the block.
	f.Fix = append(f.Fix, proj.Display+" sets "+strings.Join(proj.Keys, ", "))
	if f.FixLabel == "" {
		f.FixLabel = "show what this checkout sets"
	}
	return f
}

// withLiteralKeyWarning turns the config row into a warning when the file
// holds a credential rather than the name of one. It says what that costs
// rather than that the key is deprecated: a person who reads "api_key is
// deprecated" goes looking for the replacement, and a person who reads that
// the file is a copy of their key already knows why it matters and what a
// backup of it is.
//
// It is a warning and not a failure. The key works, the session starts, and
// the fix is two commands the reader chooses when to run — refusing to start
// over a file that has been fine for a year would be shhh deciding a security
// posture on someone's behalf.
// See docs/capabilities/secrets.md#where-a-value-comes-from.
func withLiteralKeyWarning(f doctorFinding, cfg config.Config) doctorFinding {
	held := literalKeys(cfg)
	if len(held) == 0 {
		return f
	}
	f.Outcome = "key in the file"
	f.State = components.DoctorWarned
	f.Detail = joinDetail(f.Detail, heldPhrase(held))
	f.Consequence = "this file is a copy of your key — so is every backup and every clone of it"
	f.FixLabel = "name the variable instead"
	for _, k := range held {
		f.Fix = append(f.Fix,
			"export "+k.envVar+"=… in your shell profile",
			"shhh config set "+k.envKey+" "+k.envVar,
			"then remove "+k.key+" from the file",
		)
	}
	return f
}

// literalKey is one credential the file holds as a value: the key holding it,
// the key that would name a variable instead, and the variable to name. The
// provider's own variable is the suggestion where the resolved provider has
// one, because a person exporting a key for anthropic already has somewhere
// the dialect will look for it.
type literalKey struct {
	key    string
	envKey string
	envVar string
}

// heldPhrase names the keys holding a value, agreeing with how many there
// are. A row reading `provider.api_key hold the key itself` is a row the
// reader stops trusting about the rest of the sentence.
func heldPhrase(held []literalKey) string {
	names := make([]string, len(held))
	for i, k := range held {
		names[i] = k.key
	}
	if len(names) == 1 {
		return names[0] + " holds the key itself"
	}
	return strings.Join(names, " and ") + " hold the key itself"
}

// literalKeys are the credentials this file holds as values rather than as
// names. It walks the settings table rather than naming the two keys, so a
// credential that gains the second spelling is warned about by existing
// rather than by being remembered here.
func literalKeys(cfg config.Config) []literalKey {
	var held []literalKey
	for _, s := range config.Settings() {
		envKey := s.EnvKey()
		if envKey == "" {
			continue
		}
		if value, set := config.Value(cfg, s.Key); !set || value == "" {
			continue
		}
		held = append(held, literalKey{key: s.Key, envKey: envKey, envVar: suggestedKeyVar(s.Key, cfg)})
	}
	return held
}

// suggestedKeyVar is the variable the fix names: for the provider key, the
// one the resolved provider's dialect already reads, and otherwise a variable
// spelled from the key, which is the shape every other credential in the
// documentation uses.
func suggestedKeyVar(key string, cfg config.Config) string {
	if key == "provider.api_key" {
		vars := resolve.KeyVars(resolve.Resolve(resolve.Opts{ConfigProvider: cfg.Provider.Default}).Provider)
		return vars[len(vars)-1]
	}
	_, tail, _ := strings.Cut(key, ".")
	return "SHHH_" + strings.ToUpper(tail)
}

// configSettingsSet counts the settings standing against the defaults. It is
// the header count `shhh config` states, read here so the two screens agree
// on what "set" means: a value the file supplied, not a value shhh chose.
func configSettingsSet(cfg config.Config) int {
	n := 0
	for _, set := range []bool{
		cfg.Provider.Default != "", cfg.Provider.Model != "", cfg.Provider.APIKey != "",
		cfg.Provider.APIKeyEnv != "", cfg.Web.SearchAPIKeyEnv != "",
		cfg.Provider.BaseURL != "", cfg.Provider.Name != "",
		cfg.Behavior.SilentMode, cfg.Behavior.Shell != "", cfg.Behavior.ContextMaxTokens > 0,
		cfg.Behavior.MaxToolRounds != 0, cfg.Behavior.SafetyWarnings != nil,
		cfg.Appearance.Mouse != nil, cfg.Appearance.Notify != nil,
		cfg.Behavior.SystemPromptExtra != "", len(cfg.Behavior.CommandAllowlist) > 0,
		len(cfg.Behavior.ReadOnlyCommands) > 0,
		cfg.Sandbox.Profile != "", cfg.Sandbox.ContainerImage != "", cfg.Sandbox.ContainerEngine != "",
		cfg.Sandbox.RequireIsolation != "", len(cfg.Sandbox.DenyExtra) > 0, len(cfg.Sandbox.WriteExtra) > 0,
		cfg.Web.SearchAPIKey != "", cfg.Web.AllowPrivate, cfg.LSP.Disabled,
		cfg.Summary.Model != "", cfg.Summary.Disabled,
	} {
		if set {
			n++
		}
	}
	return n
}

func probeMigrate(context.Context, config.Config) doctorFinding {
	return doctorMigrate(migrate.Plan(migrationDir()))
}

// migrationDir is the checkout the project migrations are asked about. A
// working directory that cannot be read is named as nothing rather than as
// ".": a detector that walked up from the process would answer about a
// checkout the reader was never told it looked at.
func migrationDir() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return dir
}

// doctorMigrate reads whether this machine is still shaped the way an older
// shhh shaped it. It is the one check that can change something, and it is
// here rather than in a `shhh migrate` command on purpose: a migration nobody
// knows they need is a migration nobody runs, and the place a person already
// goes when something is not where they left it is the doctor
// (docs/capabilities/configuration.md#a-migration-is-a-doctor-check).
//
// It reads as a warning and never as a failure. Nothing is broken — shhh
// starts, runs, and records — it is just doing so without whatever is in the
// old place, and the consequence line is where that is said plainly.
func doctorMigrate(pending []migrate.Pending) doctorFinding {
	if len(pending) == 0 {
		return doctorFinding{
			Subject: "nothing to migrate", Detail: "this machine is on the current layout",
			Outcome: "ok",
		}
	}
	f := doctorFinding{
		Subject:     countOf(len(pending), "migration pending", "migrations pending"),
		Detail:      migrateDetail(pending),
		Outcome:     "pending",
		State:       components.DoctorWarned,
		Consequence: migrateConsequence(pending),
		FixLabel:    "show what would change",
		Fix:         migrateFix(pending),
	}
	if auto := migrateAuto(pending); len(auto) > 0 {
		f.Action = "make " + migrateThese(auto)
		f.ActionPrompt = "Make " + migrateThese(auto) + " now?"
		f.Apply = func() ([]string, error) { return applyMigrations(auto) }
	}
	return f
}

// migrateAuto is the pending migrations shhh can carry out itself. One it
// cannot is still reported — the fix lines say what to do — but it offers no
// key, because an offer that cannot be honoured is worse than none
// (invariant 5).
func migrateAuto(pending []migrate.Pending) []migrate.Pending {
	var auto []migrate.Pending
	for _, p := range pending {
		if p.Auto() {
			auto = append(auto, p)
		}
	}
	return auto
}

// migrateThese names what `[a]` would do, in the plural the count calls for.
func migrateThese(auto []migrate.Pending) string {
	if len(auto) == 1 {
		return "the change"
	}
	return countOf(len(auto), "change", "changes")
}

// migrateDetail is the target field: what each pending migration is about.
func migrateDetail(pending []migrate.Pending) string {
	summaries := make([]string, 0, len(pending))
	for _, p := range pending {
		summaries = append(summaries, p.Summary)
	}
	return strings.Join(summaries, " · ")
}

// migrateConsequence is what leaving them costs. The migrations write their
// own, because only the migration knows what the reader is missing.
func migrateConsequence(pending []migrate.Pending) string {
	lines := make([]string, 0, len(pending))
	for _, p := range pending {
		lines = append(lines, p.Consequence)
	}
	return strings.Join(lines, "; ")
}

// migrateFix is the lines behind `[f]`: every migration named, then its steps
// under it. A migration shhh will not make itself says so on its own line,
// because otherwise the reader would sit waiting for a key that never comes.
func migrateFix(pending []migrate.Pending) []string {
	var lines []string
	for i, p := range pending {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, p.Name+":")
		lines = append(lines, p.Steps...)
		if !p.Auto() {
			lines = append(lines, "shhh cannot make this one for you")
		}
	}
	return lines
}

// applyMigrations carries out every automatic migration and reports what
// changed, one line each. It stops at the first failure and keeps the lines
// from before it: what already moved is what the reader needs to know before
// they try again.
func applyMigrations(auto []migrate.Pending) ([]string, error) {
	var done []string
	for _, p := range auto {
		lines, err := p.Apply()
		done = append(done, lines...)
		if err != nil {
			return done, err
		}
	}
	return done, nil
}

func probeModel(ctx context.Context, cfg config.Config) doctorFinding {
	resolved := resolve.Resolve(resolve.Opts{
		ConfigProvider:  cfg.Provider.Default,
		ConfigModel:     cfg.Provider.Model,
		ConfigReasoning: cfg.Provider.Reasoning,
	})
	survey := resolve.SurveyPlaces(ctx, resolve.SurveyOpts{
		Provider:        resolved.Provider,
		ConfigAPIKey:    cfg.ProviderAPIKey(),
		ConfigAPIKeyEnv: cfg.Provider.APIKeyEnv,
		ConfigPaths:     config.Paths(),
	})
	f := doctorModelFinding(resolved.Provider, resolved.Model, survey)
	// A model decided by an env var or a flag looks exactly like one decided
	// by the config file, which is how `/model default` came to look broken
	// while writing the file correctly. The row that reports the
	// model is the row that has to say who chose it.
	if over := resolve.ModelOutranks(resolve.Opts{ConfigModel: cfg.Provider.Model}); over != "" && cfg.Provider.Model != "" {
		f.Detail = joinDetail(f.Detail, over+", overruling provider.model = "+cfg.Provider.Model)
	}
	// A reasoning level is the other half of what a request asks for,
	// and an unreadable one is a session that will fail to start rather than
	// quietly reason less than it was told to.
	if effort, err := provider.ParseEffort(resolved.Reasoning); err != nil {
		f.Detail = joinDetail(f.Detail, err.Error())
	} else if effort.On() {
		f.Detail = joinDetail(f.Detail, "reasoning "+effort.String())
	}
	return f
}

// doctorModelFinding reads the same walk the no-provider card reads:
// the four places a key can come from, and what was in each. A key that was
// found is reported as found and not as accepted — accepting one means
// spending a request on it, and a diagnostic that billed you for running it
// would be a diagnostic nobody runs.
//
// The check is named `model` rather than `provider` because the verb field is
// eight columns and `provider` fills all eight, leaving the target beside it
// with no gap; `model` is the verb a failure row already gives a provider
// failure, so the two rows line up.
func doctorModelFinding(providerName, model string, survey resolve.Survey) doctorFinding {
	f := doctorFinding{Subject: providerName}
	if model != "" {
		f.Detail = model
	}
	for _, place := range survey.Places {
		if !place.Found {
			continue
		}
		switch place.Kind {
		case resolve.PlaceEnv, resolve.PlaceConfig:
			f.Detail = joinDetail(f.Detail, "key "+doctorMasked(place.Finding)+" found")
			f.Outcome = "ok"
			return f
		case resolve.PlaceProfiles:
			f.Detail = joinDetail(f.Detail, "gateway profile "+place.Finding+" is ready")
			f.Outcome = "ok"
			return f
		case resolve.PlaceLocal:
			f.Detail = joinDetail(f.Detail, "a local runtime is answering on "+place.Finding)
			f.Outcome = "ok"
			return f
		}
	}
	f.Detail = joinDetail(f.Detail, "no key in any of the four places")
	f.Outcome = "no key"
	f.State = components.DoctorFailed
	f.Consequence = "no session will start until a key is found — every one exits on \"no provider\""
	f.FixLabel = "show the four places shhh looks"
	f.Fix = doctorKeyPlaces(survey)
	return f
}

// doctorKeyPlaces is the fix behind `[f]` on a provider with no key: the same
// four places, each with what was there. It is the card's own body
// written as lines, because a fix that only said "set an API key" would be
// telling the reader something they already knew.
func doctorKeyPlaces(survey resolve.Survey) []string {
	lines := make([]string, 0, len(survey.Places)+1)
	for _, place := range survey.Places {
		detail := place.Detail
		if place.Found {
			detail = doctorMasked(place.Finding)
			if place.Detail != "" {
				detail += " · " + place.Detail
			}
		}
		lines = append(lines, fmt.Sprintf("%-9s %s", string(place.Kind), detail))
	}
	if survey.Likely != "" {
		lines = append(lines, "likely    "+survey.Likely)
	}
	return lines
}

// doctorMasked keeps a secret masked wherever the survey already masked it.
// The survey reports a key by its last four characters and never by more
// , and this is the one place in the report a key is mentioned at all,
// so it is worth saying that the masking is inherited rather than reapplied.
func doctorMasked(finding string) string { return finding }

func probeStore(context.Context, config.Config) doctorFinding {
	db, err := openStore()
	if err != nil {
		return doctorStore("", 0, err)
	}
	// Opening runs the migrations, so a store that opens is a store this
	// build can read as well as find. Nothing is queried beyond that: the
	// question here is whether the file works, and the surfaces that read it
	// count their own rows.
	defer db.Close()
	path := doctorStorePath()
	var size int64
	if info, statErr := os.Stat(path); statErr == nil {
		size = info.Size()
	}
	return doctorStore(path, size, nil)
}

// doctorStorePath is where the local store lives, for the row to name. It asks
// storage rather than deriving it again: this file had its own copy of the
// rule, and a report that names a different path from the one the store
// actually opens is worse than no path at all.
func doctorStorePath() string {
	dir, err := storage.Dir()
	if err != nil {
		return "shhh.db"
	}
	return filepath.Join(dir, "shhh.db")
}

// doctorStore reads the local store: history, snippets, metrics and chat logs
// all live in it, so a store that will not open is the check that explains
// four other things being empty.
func doctorStore(path string, size int64, err error) doctorFinding {
	if err != nil {
		return doctorFinding{
			Subject: "the local store did not open", Detail: err.Error(), Outcome: "unreadable",
			State:       components.DoctorFailed,
			Consequence: "history, snippets and metrics will all be empty, and nothing new will be recorded",
			FixLabel:    "show the two things to check",
			Fix: []string{
				"ls -l " + shortPath(doctorStorePath()),
				"the directory must be writable; delete the file to start a fresh store",
			},
		}
	}
	return doctorFinding{
		Subject: shortPath(path),
		Detail:  "opened · migrations current · " + doctorBytes(size),
		Outcome: "ok",
	}
}

// doctorBytes is a file size in the units a reader thinks in. A store nobody
// has written to yet is stated as such rather than as `0 B`, which reads like
// a fault where the truth is a fresh install.
func doctorBytes(n int64) string {
	switch {
	case n <= 0:
		return "nothing recorded yet"
	case n < 1<<10:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.0f kB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
}

func probeLogs(context.Context, config.Config) doctorFinding {
	path, err := logPath()
	if err != nil {
		return doctorLogs("", 0, err)
	}
	// A log that is not there is the ordinary case on a machine where
	// nothing has gone wrong, so its absence is a size of zero rather than
	// an error: the row's job is to name the file a failure would be written
	// to, and it can do that either way.
	var size int64
	if info, statErr := os.Stat(path); statErr == nil {
		size = info.Size()
	}
	return doctorLogs(path, size, nil)
}

// doctorLogs reads the diagnostic log: where it is, and how much is in it.
// This is the row `shhh logs` is the reader for, so the path it names is the
// path that command opens — both ask logPath.
//
// It does not ask whether the file can be written, and the row above it is
// why: the store lives in the same directory and opening it is the check
// that fails, loudly, when that directory cannot be used. Naming the same
// fault twice on one screen reads as two faults, and the second one here
// would have to write a file to find out.
func doctorLogs(path string, size int64, err error) doctorFinding {
	if err != nil {
		// There is nowhere for state to go at all: no XDG_DATA_HOME and no
		// home directory to fall back on.
		return doctorFinding{
			Subject: "there is nowhere to write the log", Detail: err.Error(), Outcome: "nowhere",
			State:       components.DoctorWarned,
			Consequence: "a refused request will be reported on the screen and nowhere else",
			FixLabel:    "show where it would go",
			Fix: []string{
				"shhh writes its state under $XDG_DATA_HOME, or ~/.local/share when that is unset",
				"one of the two has to name a directory this user can create",
			},
		}
	}
	return doctorFinding{
		Subject: shortPath(path),
		Detail:  doctorBytes(size),
		Outcome: "ok",
	}
}

func probeReports(context.Context, config.Config) doctorFinding {
	dir, err := reportsDir()
	if err != nil {
		// The store row above already failed loudly on the same missing
		// state directory; this row states its own consequence and stops.
		return doctorReports("", 0, 0, err)
	}
	count, size, err := reports.Census(dir)
	return doctorReports(dir, count, size, err)
}

// doctorReports reads the report store: where it is, how many pages it
// holds, and what they cost in disk. This is the row `shhh reports` is the
// reader for, so the path it names is the one the listing opens — both ask
// reportsDir, and retention is the answer to a store that grows
// (docs/capabilities/reports.md#findable-and-prunable).
func doctorReports(path string, count int, size int64, err error) doctorFinding {
	if err != nil {
		return doctorFinding{
			Subject: "the report store is not readable", Detail: err.Error(), Outcome: "unreadable",
			State:       components.DoctorWarned,
			Consequence: "the report tool is not offered, and pages already made cannot be reopened",
			FixLabel:    "show the place to check",
			Fix: []string{
				"ls -l " + shortPath(path),
				"the directory must be readable; deleting it starts an empty store",
			},
		}
	}
	detail := doctorBytes(size)
	if count > 0 {
		detail = countOf(count, "report", "reports") + " · " + doctorBytes(size)
	}
	return doctorFinding{
		Subject: shortPath(path),
		Detail:  detail,
		Outcome: "ok",
	}
}

func probeOtel(_ context.Context, cfg config.Config) doctorFinding {
	return doctorOtel(cfg.Otel.Endpoint)
}

// doctorOtel reads the one setting that sends the session record off this
// machine, and it reads it rather than reaching it. A collector is somebody
// else's process on somebody else's network: opening a connection to it
// would make this the one check in the run that costs a third party a round
// trip, and a collector that happens to be down is not a fault of this
// machine's — the exporter's own answer to that is to give up quietly and
// write one line to the log.
//
// The row exists so that "my sessions are leaving this machine" is a fact a
// reader can see without opening the config file, which is the same reason
// the store and the log rows name their paths.
func doctorOtel(endpoint string) doctorFinding {
	if strings.TrimSpace(endpoint) == "" {
		return doctorFinding{
			Subject: "the record stays on this machine",
			Outcome: "off",
			State:   components.DoctorSkipped,
		}
	}
	target, err := observe.ParseEndpoint(endpoint)
	if err != nil {
		return doctorFinding{
			Subject: "the collector endpoint is not a URL", Detail: err.Error(), Outcome: "unusable",
			State:       components.DoctorWarned,
			Consequence: "sessions are recorded locally and nothing is exported",
			FixLabel:    "show the shape it takes",
			Fix: []string{
				"shhh config set otel.endpoint http://localhost:4318",
				"the scheme decides whether the record crosses the network in the clear, so it is never guessed",
			},
		}
	}
	return doctorFinding{
		Subject: target,
		Detail:  "content-free",
		Outcome: "ok",
	}
}

func probeSandbox(_ context.Context, cfg config.Config) doctorFinding {
	policy, err := sandboxPolicy(cfg)
	if err != nil {
		return doctorFinding{
			Subject: "the containment policy is not readable", Detail: err.Error(),
			Outcome: "misconfigured", State: components.DoctorFailed,
			Consequence: "every contained command will fail until the policy is fixed; none of them runs bare",
			FixLabel:    "show the setting to fix",
			Fix:         []string{"shhh config set sandbox.profile workspace"},
		}
	}
	return doctorSandbox(sandbox.Detect(), string(policy.Profile), runtime.GOOS)
}

// doctorSandbox reads the containment mechanism. This is the check the
// artboard leads its failure with, and the consequence is quoted from the
// surface the reader will actually meet it on: the approval card promotes
// `⚠ UNCONTAINED` to its title bar when nothing wraps the command.
func doctorSandbox(avail sandbox.Availability, profile, goos string) doctorFinding {
	if avail.OK {
		return doctorFinding{
			Subject: avail.Mechanism,
			Detail:  joinDetail(avail.Detail, profile+" profile"),
			Outcome: "contained",
		}
	}
	f := doctorFinding{
		Subject: "no containment mechanism", Detail: avail.Detail,
		Outcome: "uncontained", State: components.DoctorFailed,
		Consequence: "every approval will show ⚠ UNCONTAINED, and an approved command runs as you",
		FixLabel:    "show the fix for this host",
	}
	switch goos {
	case "linux":
		f.Fix = []string{
			"sudo apt install bubblewrap        (or the package your distribution ships)",
			"unprivileged user namespaces must be enabled: sysctl kernel.unprivileged_userns_clone=1",
			"shhh doctor                        to check it took",
		}
	case "darwin":
		f.Fix = []string{
			"sandbox-exec ships with macOS; a PATH that hides /usr/bin is the usual cause",
			"shhh doctor                        to check it took",
		}
	default:
		// Nothing to install: the platform has no mechanism at all, and
		// naming one that does not exist would be worse than saying so.
		f.FixLabel = "show what can be done instead"
		f.Fix = []string{
			"there is no containment mechanism on " + goos,
			"run the agent inside a container sandbox instead: shhh code -p --sandbox",
		}
	}
	return f
}

func probeEngine(_ context.Context, cfg config.Config) doctorFinding {
	eng := sandbox.DetectEngine(cfg.Sandbox.ContainerEngine)
	imageErr := sandbox.ValidateImage(cfg.Sandbox.ContainerImage, cfg.Sandbox.ImageAllowlist)
	return doctorEngine(eng, cfg.Sandbox.ContainerImage, imageErr, ownedSandboxCount())
}

// ownedSandboxCount is how many sandbox containers this machine still owns.
// An unreadable ownership store answers -1, which the reading states rather
// than passing off as none.
func ownedSandboxCount() int {
	store, err := sandbox.OpenStore()
	if err != nil {
		return -1
	}
	recs, err := store.List()
	if err != nil {
		return -1
	}
	return len(recs)
}

// doctorEngine reads container sandboxes, which are opt-in: `shhh code
// -p --sandbox` asks for one and nothing else does. So a machine with no
// engine is `⊘ not checked` rather than a failure — the row states what is
// not available instead of claiming something is broken (invariant 4).
func doctorEngine(eng sandbox.Engine, image string, imageErr error, owned int) doctorFinding {
	if !eng.OK {
		return doctorFinding{
			Subject: "no container engine", Detail: eng.Detail,
			Outcome: "not available", State: components.DoctorSkipped,
			Consequence: "shhh code -p --sandbox will refuse to start; nothing else needs one",
			FixLabel:    "show what a sandbox needs",
			Fix: []string{
				"install podman (rootless, preferred) or docker",
				"shhh config set sandbox.container_image <name>@sha256:<digest>",
			},
		}
	}
	f := doctorFinding{Subject: eng.Name, Detail: eng.Detail, Outcome: "ok"}
	if owned > 0 {
		f.Detail = joinDetail(f.Detail, countOf(owned, "container owned", "containers owned"))
	}
	if imageErr != nil {
		f.Detail = imageErr.Error()
		f.Outcome = "no image"
		f.State = components.DoctorWarned
		f.Consequence = "the engine is there, so only the image stands between this host and a sandbox"
		f.FixLabel = "show the setting to fix"
		f.Fix = []string{"shhh config set sandbox.container_image <name>@sha256:<digest>"}
		return f
	}
	f.Detail = joinDetail(f.Detail, "image "+shortImage(image))
	return f
}

// shortImage is a digest-pinned reference with the digest cut to its first
// twelve characters — enough to tell two pins apart, short enough for a row.
func shortImage(image string) string {
	name, digest, found := strings.Cut(image, "@sha256:")
	if !found || len(digest) <= 12 {
		return image
	}
	return name + "@sha256:" + digest[:12] + "…"
}

func probeGit(ctx context.Context, _ config.Config) doctorFinding {
	dir, err := os.Getwd()
	if err != nil {
		return doctorGit(doctorGitState{Err: err})
	}
	return doctorGit(readGitState(ctx, dir))
}

// doctorGitState is what git was able to say about the working directory.
type doctorGitState struct {
	// Repo says the directory is inside a work tree at all.
	Repo bool
	// Root is the work tree's own directory, which is what the row names —
	// the working directory may be several levels inside it.
	Root string
	// Changed is how many paths `git status --porcelain` listed, and
	// Untracked how many of those git has never seen.
	Changed   int
	Untracked int
	// Dir is where the walk started, for the row to name when there is no
	// repository to name instead.
	Dir string
	// Err is a git that could not be run at all, which is a different answer
	// from a directory that is not a repository.
	Err error
}

// readGitState runs the three cheap commands that answer the question.
func readGitState(ctx context.Context, dir string) doctorGitState {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, doctorGitTimeout)
	defer cancel()

	state := doctorGitState{Dir: dir}
	git := func(args ...string) (string, error) {
		out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).Output()
		return strings.TrimSpace(string(out)), err
	}
	if _, err := exec.LookPath("git"); err != nil {
		state.Err = err
		return state
	}
	inside, err := git("rev-parse", "--is-inside-work-tree")
	if err != nil || inside != "true" {
		return state
	}
	state.Repo = true
	if root, rootErr := git("rev-parse", "--show-toplevel"); rootErr == nil {
		state.Root = root
	}
	status, statusErr := git("status", "--porcelain")
	if statusErr != nil {
		state.Err = statusErr
		return state
	}
	for _, line := range strings.Split(status, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		state.Changed++
		if strings.HasPrefix(line, "??") {
			state.Untracked++
		}
	}
	return state
}

// doctorGit reads the workspace. What hangs on it is undo: `[u] undo turn`
// restores from what the changeset recorded, and outside a repository there
// is nothing to compare an edit against — so a directory that is not a work
// tree is a warning rather than a pass, and it says which key is affected.
func doctorGit(state doctorGitState) doctorFinding {
	switch {
	case state.Err != nil && !state.Repo:
		return doctorFinding{
			Subject: "git did not answer", Detail: state.Err.Error(),
			Outcome: "unknown", State: components.DoctorWarned,
			Consequence: "shhh cannot tell a tracked file from an untracked one, so every edit reads as unrecoverable",
			FixLabel:    "show what is missing",
			Fix:         []string{"install git and re-run: shhh doctor"},
		}
	case !state.Repo:
		return doctorFinding{
			Subject: shortPath(state.Dir), Detail: "not a git work tree",
			Outcome: "no repo", State: components.DoctorWarned,
			Consequence: "every edit here will say it cannot be undone, because there is nothing to restore from",
			FixLabel:    "show the one command",
			Fix:         []string{"git init                 or start the agent inside a repository"},
		}
	}
	root := state.Root
	if root == "" {
		root = state.Dir
	}
	f := doctorFinding{Subject: shortPath(root), Outcome: "ok"}
	switch {
	case state.Changed == 0:
		f.Detail = "clean"
	case state.Untracked == 0:
		f.Detail = countOf(state.Changed, "file changed", "files changed") + ", all tracked"
	default:
		f.Detail = fmt.Sprintf("%s, %d untracked",
			countOf(state.Changed, "file changed", "files changed"), state.Untracked)
	}
	return f
}

// probeHooks is the doctor's reading of the person's own commands at the
// session's seams. It reads the same two files a session reads and through
// the same loader, so a hook the doctor lists is a hook a session fires.
func probeHooks(_ context.Context, cfg config.Config) doctorFinding {
	if cfg.Hooks.Disabled {
		return doctorFinding{
			Subject: "hooks are off", Detail: "hooks.disabled",
			Outcome: "off", State: components.DoctorSkipped,
		}
	}
	return doctorHooks(hookSet(cfg), cfg.HookCeiling())
}

// doctorHooks is that reading. Hooks are the person's own commands, so
// nothing here is a fault: a checkout with none is not missing anything, and
// an entry that would not load is a warning naming it rather than a failure,
// because the session started and started without it.
func doctorHooks(set *hook.Set, ceiling time.Duration) doctorFinding {
	notes := set.Notes()
	if set.Len() == 0 && len(notes) == 0 {
		return doctorFinding{
			Subject: "no hooks", Detail: strings.Join(hook.Events(), " · "),
			Outcome: "empty", State: components.DoctorSkipped,
		}
	}
	f := doctorFinding{
		Subject: countOf(set.Len(), "hook", "hooks"),
		Detail:  strings.Join(set.Events(), " · "),
		Outcome: "ok",
	}
	if ceiling > 0 {
		f.Detail += " · " + ceiling.String() + " each"
	}
	if len(notes) > 0 {
		f.Outcome = "unreadable"
		f.State = components.DoctorSkipped
		f.Consequence = countOf(len(notes), "entry", "entries") + " did not load and will not fire"
		f.Fix = notes
		f.FixLabel = fmt.Sprintf("show the %s", countOf(len(f.Fix), "line", "lines"))
	}
	return f
}

// probePrompts reads every wording a file replaced, from the settings and
// from the checkout's own prompts directory.
//
// It is a check of its own because an unreadable wording is the one config
// failure that stops a session from starting at all, and a reader who has
// just written the path is exactly who is looking here.
func probePrompts(_ context.Context, cfg config.Config) doctorFinding {
	return doctorPrompts(readWordings(cfg.Prompts, projectPrompts()))
}

// doctorPrompts is that reading. Replacing nothing is the ordinary case and
// not a fault, so a machine running the built-in prose reads as empty rather
// than as missing something.
func doctorPrompts(rows []wordingRow) doctorFinding {
	if len(rows) == 0 {
		return doctorFinding{
			Subject: "no wordings replaced", Detail: "the built-in prose",
			Outcome: "empty", State: components.DoctorSkipped,
		}
	}
	names := make([]string, 0, len(rows))
	var unreadable []string
	for _, r := range rows {
		names = append(names, r.key)
		if r.err != nil {
			unreadable = append(unreadable, r.err.Error())
		}
	}
	f := doctorFinding{
		Subject: countOf(len(rows), "wording", "wordings"),
		Detail:  strings.Join(names, " · "),
		Outcome: "ok",
	}
	if len(unreadable) > 0 {
		f.Outcome = "unreadable"
		f.State = components.DoctorFailed
		f.Consequence = countOf(len(unreadable), "wording", "wordings") + " cannot be read, and no session starts until that is settled"
		f.Fix = unreadable
		f.FixLabel = fmt.Sprintf("show the %s", countOf(len(f.Fix), "reason", "reasons"))
	}
	return f
}

func probeTools(context.Context, config.Config) doctorFinding {
	var found, missing []string
	for _, tool := range structural.ToolBinaries() {
		path, err := exec.LookPath(tool)
		if err != nil {
			missing = append(missing, tool)
			continue
		}
		// A name on PATH that resolves to a program the agent cannot use is
		// reported as an absence carrying its reason, because "it is
		// installed and shhh says it is not" is otherwise a dead end.
		if reason := structural.UnsupportedBinary(tool, path); reason != "" {
			missing = append(missing, tool+" ("+reason+")")
			continue
		}
		found = append(found, tool)
	}
	servers := make([]string, 0, 4)
	for _, spec := range lsp.DetectServers() {
		servers = append(servers, spec.Name)
	}
	return doctorTools(found, missing, servers)
}

// doctorTools reads what the agent will find on PATH. None of it is required
// — every structural tool has a built-in fallback and the LSP integration is
// a clean no-op without a server — so this check never fails. It states what
// is there and what is not, because "why did it not use ast-grep" is a
// question with an answer.
func doctorTools(found, missing, servers []string) doctorFinding {
	f := doctorFinding{Outcome: "ok"}
	switch {
	case len(found) == 0:
		f.Subject = "no structural tools"
		f.Outcome = "built-ins only"
		f.State = components.DoctorSkipped
	default:
		f.Subject = strings.Join(found, ", ")
	}
	switch {
	case len(servers) == 0 && len(missing) == 0:
		f.Detail = "no language server on PATH"
	case len(servers) == 0:
		f.Detail = "no " + strings.Join(missing, "/") + ", no language server"
	case len(missing) == 0:
		f.Detail = strings.Join(servers, ", ")
	default:
		f.Detail = "no " + strings.Join(missing, "/") + " · " + strings.Join(servers, ", ")
	}
	return f
}

func probeProject(context.Context, config.Config) doctorFinding {
	dir, err := os.Getwd()
	if err != nil {
		dir = ""
	}
	return doctorProject(project.Instructions(dir, userInstructionsPath()))
}

// doctorProject lists the instruction files a session started here would put
// in its system prompt, in the order it states them. A checkout that has
// told the model nothing is the ordinary state of a new one rather than a
// fault, so it is `⊘` with the names it would have read, which is also the
// only place those names are written down for someone who has not read the
// manual.
func doctorProject(files []project.Instruction) doctorFinding {
	if len(files) == 0 {
		return doctorFinding{
			Subject: "nothing read", Detail: "no " + project.InstructionNames(),
			Outcome: "empty", State: components.DoctorSkipped,
		}
	}
	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, shortPath(f.Path))
	}
	return doctorFinding{
		Subject: countOf(len(files), "instruction file", "instruction files"),
		Detail:  strings.Join(paths, " · "), Outcome: "ok",
	}
}

func probeMemory(context.Context, config.Config) doctorFinding {
	dir, err := os.Getwd()
	if err != nil {
		return doctorMemory("", 0, 0, err)
	}
	db, err := openStore()
	if err != nil {
		// The store's own row already says this, and saying it twice would be
		// the report blaming one fault on two checks.
		return doctorMemory(memory.ProjectScope(dir), 0, 0, nil)
	}
	defer db.Close()
	entries, listErr := memory.NewStore(db, memory.ProjectScope(dir)).List()
	scoped := 0
	for _, e := range entries {
		if e.Scope != memory.GlobalScope {
			scoped++
		}
	}
	return doctorMemory(memory.ProjectScope(dir), scoped, len(entries)-scoped, listErr)
}

// doctorMemory reads durable memory for this project. An empty store
// is the ordinary state of a new project rather than a fault, so it is `⊘`
// with the words for it, not a warning.
func doctorMemory(scope string, forProject, global int, err error) doctorFinding {
	if err != nil {
		return doctorFinding{
			Subject: "memory did not load", Detail: err.Error(),
			Outcome: "unreadable", State: components.DoctorWarned,
			Consequence: "sessions in this project will start with nothing remembered",
		}
	}
	if forProject+global == 0 {
		return doctorFinding{
			Subject: "nothing remembered yet", Detail: shortPath(scope),
			Outcome: "empty", State: components.DoctorSkipped,
		}
	}
	detail := countOf(forProject, "entry", "entries") + " for this project"
	if global > 0 {
		detail += " · " + strconv.Itoa(global) + " global"
	}
	return doctorFinding{Subject: shortPath(scope), Detail: detail, Outcome: "ok"}
}

func probeUpdate(context.Context, config.Config) doctorFinding {
	return doctorUpdate(version, updateCheck(version))
}

// updateCheck is the release lookup, a variable so the report can be tested
// without the network. It answers the latest released version, or "" for a
// feed that did not answer.
var updateCheck = func(current string) string {
	res := update.Check(current)
	if res == nil {
		return ""
	}
	return res.Latest
}

// doctorUpdate says whether a newer shhh exists. A dev build has no version
// to compare, and an unreachable release feed is not a fault of this machine
// — both are `⊘`, and both say which.
func doctorUpdate(current string, latest string) doctorFinding {
	if current == "dev" || current == "" {
		return doctorFinding{
			Subject: "not checked", Detail: "a dev build has no released version to compare",
			Outcome: "unversioned", State: components.DoctorSkipped,
		}
	}
	if latest == "" {
		return doctorFinding{
			Subject: "no answer from the release feed", Detail: "this says nothing about your install",
			Outcome: "unknown", State: components.DoctorSkipped,
		}
	}
	if latest == current {
		return doctorFinding{Subject: "shhh " + current, Detail: "the latest release", Outcome: "ok"}
	}
	return doctorFinding{
		Subject: "shhh " + latest + " is out", Detail: "this machine is on " + current,
		Outcome: "out of date", State: components.DoctorWarned,
		FixLabel: "show how to upgrade",
		Fix: []string{
			"brew upgrade shhh                       if it came from the tap",
			"go install github.com/rfizzle/shhh/cmd/shhh@latest",
		},
	}
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

// doctorModel hosts the surface. It runs one probe at a time and redraws
// between them, so a run in progress is something the reader can watch rather
// than a blank terminal that resolves all at once.
type doctorModel struct {
	cfg     config.Config
	probes  []doctorProbe
	started time.Time
	at      int

	// findings are the answers behind the rows the screen is drawing. The
	// screen is handed only what it renders, and an action is not renderable
	// — so the function `[a]` invokes stays here, indexed the same way.
	findings []doctorFinding
	// nouns are what the header and the text report count, singular and
	// plural, when the screen is not doctor's own.
	nouns [2]string

	screen components.DoctorScreen
}

// doctorDoneMsg carries one probe's answer back to the model.
type doctorDoneMsg struct {
	at      int
	finding doctorFinding
	took    time.Duration
}

// doctorTickMsg drives the one spinner on the screen, at the shared tick
// interval.
type doctorTickMsg time.Time

// doctorAppliedMsg carries back what an action did, so a migration that has
// to move a large store does not freeze the surface while it runs.
type doctorAppliedMsg struct {
	lines []string
	err   error
}

func newDoctorModel(cfg config.Config, probes []doctorProbe) *doctorModel {
	m := &doctorModel{cfg: cfg, probes: probes}
	m.findings = make([]doctorFinding, len(m.probes))
	m.screen.Checks = make([]components.DoctorCheck, len(m.probes))
	for i, probe := range m.probes {
		subject := probe.queued
		if subject == "" {
			subject = doctorQueuedSubject(probe.name)
		}
		m.screen.Checks[i] = components.DoctorCheck{
			Name: probe.name, Subject: subject,
			Outcome: components.OutcomeQueued, Duration: components.NoDuration,
			State: components.DoctorQueued,
		}
	}
	m.screen.Running = len(m.probes) > 0
	m.screen.Spin = true
	return m
}

// doctorQueuedSubject is what a check says about itself before it has run.
// A queued row that said nothing would be a row the reader cannot read.
func doctorQueuedSubject(name string) string {
	switch name {
	case "binary":
		return "which shhh this is"
	case "config":
		return "the config file and what it sets"
	case "migrate":
		return "whether this machine is still shaped an older way"
	case "model":
		return "the provider and where its key comes from"
	case "store":
		return "the local store"
	case "logs":
		return "where a refused request is written down"
	case "reports":
		return "the report pages sessions built"
	case "otel":
		return "where the session record is sent"
	case "sandbox":
		return "what contains an approved command"
	case "engine":
		return "container sandboxes"
	case "git":
		return "the workspace, and whether an edit can be undone"
	case "project":
		return "the instruction files a session here reads"
	case "trust":
		return "what this checkout may make a session load"
	case "hooks":
		return "your own commands at the session's seams"
	case "prompts":
		return "the wordings a file replaced"
	case "tools":
		return "the tools and language servers on PATH"
	case "memory":
		return "what this project remembers"
	case "update":
		return "check for a newer shhh"
	}
	return name
}

// begin starts the first probe and the one tick source.
func (m *doctorModel) begin() tea.Cmd {
	return tea.Batch(m.runNext(), doctorTick())
}

// runNext starts the check at the cursor, or nothing when the run is done.
func (m *doctorModel) runNext() tea.Cmd {
	if m.at >= len(m.probes) {
		return nil
	}
	at, probe, cfg := m.at, m.probes[m.at], m.cfg
	return func() tea.Msg {
		started := time.Now()
		finding := probe.run(context.Background(), cfg)
		return doctorDoneMsg{at: at, finding: finding, took: time.Since(started)}
	}
}

// doctorTick is the one tick source: the spinner in the header and on
// the running row are the same frame.
func doctorTick() tea.Cmd {
	return tea.Tick(components.SpinnerInterval, func(t time.Time) tea.Msg {
		return doctorTickMsg(t)
	})
}

// answer carries out what a key asked for. A key that asked for something
// does not also close the screen: the whole point of `[c]` and `[r]` is that
// the report is still there afterwards.
func (m *doctorModel) answer(done bool, result components.DoctorResult) tea.Cmd {
	m.screen.Notice = ""
	if result.Command != nil {
		return m.apply(*result.Command)
	}
	if done {
		return tea.Quit
	}
	return nil
}

// other is the run's own traffic: a probe finishing, the spinner's tick, and
// what an applied action did.
func (m *doctorModel) other(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case doctorTickMsg:
		if !m.screen.Running {
			// The spinner stops when the run does; a frame still turning over
			// a finished run would say the screen is doing something.
			return nil
		}
		m.screen.Frame++
		m.screen.Elapsed = doctorElapsed(time.Since(m.started))
		return doctorTick()

	case doctorAppliedMsg:
		return m.applied(msg)

	case doctorDoneMsg:
		m.findings[msg.at] = msg.finding
		m.screen.Checks[msg.at] = doctorCheck(m.probes[msg.at].name, msg.finding, msg.took)
		m.at = msg.at + 1
		m.screen.Elapsed = doctorElapsed(time.Since(m.started))
		if m.at >= len(m.probes) {
			m.screen.Running = false
			return nil
		}
		m.markRunning(m.at)
		return m.runNext()
	}
	return nil
}

// markRunning turns the queued row at the cursor into the running one. It is
// the model's job rather than the screen's: which check is in flight is a
// fact about the run, not about the rendering.
func (m *doctorModel) markRunning(at int) {
	m.screen.Checks[at].State = components.DoctorRunning
	m.screen.Checks[at].Outcome = components.OutcomeRunning
	m.screen.Checks[at].Duration = ""
}

// apply carries out one command. `[c]` copies the report the surface is
// showing; `[r]` puts every row back to queued and starts again, so a fix
// applied in another terminal can be checked without leaving this one.
func (m *doctorModel) apply(command components.DoctorCommand) tea.Cmd {
	switch command.Act {
	case components.DoctorCopy:
		if res := clipboard.Copy(m.report()); !res.OK {
			m.screen.Notice = "clipboard: " + res.Warning
		} else {
			m.screen.Notice = "copied the report to the clipboard"
		}
		return nil
	case components.DoctorRerun:
		return m.rerun()
	case components.DoctorApply:
		// The screen has already asked, so by the time this arrives the
		// answer was yes. It is run off the update loop because a migration
		// moves files, and a surface that stopped repainting while it did
		// would read as a hang.
		apply := m.findings[command.At].Apply
		if apply == nil {
			return nil
		}
		m.screen.Notice = "applying…"
		return func() tea.Msg {
			lines, err := apply()
			return doctorAppliedMsg{lines: lines, err: err}
		}
	}
	return nil
}

// applied reports what an action did and re-runs every check, because the
// answer to "did that work" is the report itself and not a line at the foot
// of a stale one. The notice survives the re-run: it is the record of what
// changed, and the rows that are about to redraw will not say it again.
func (m *doctorModel) applied(msg doctorAppliedMsg) tea.Cmd {
	cmd := m.rerun()
	switch {
	case msg.err != nil && len(msg.lines) == 0:
		m.screen.Notice = "nothing changed: " + msg.err.Error()
	case msg.err != nil:
		m.screen.Notice = countOf(len(msg.lines), "change made", "changes made") +
			", then it stopped: " + msg.err.Error()
	default:
		m.screen.Notice = countOf(len(msg.lines), "change made", "changes made")
	}
	return cmd
}

// rerun puts every row back to queued and starts the checks again — what
// `[r]` does, and what an applied action does after it, so the report the
// reader is left looking at is a reading of the machine as it is now. What
// the terminal and the calling command settled stays: the row budget, the
// screen's title and the nouns its report counts in are not findings.
func (m *doctorModel) rerun() tea.Cmd {
	rows, title, nouns := m.screen.MaxLines, m.screen.Title, m.nouns
	*m = *newDoctorModel(m.cfg, m.probes)
	m.screen.MaxLines, m.screen.Title, m.nouns = rows, title, nouns
	m.started = time.Now()
	m.markRunning(0)
	return m.begin()
}

// report is the run as text under the screen's own title.
func (m *doctorModel) report() string {
	title, one, many := "shhh doctor", "check", "checks"
	if m.screen.Title != "" {
		title, one, many = m.screen.Title, m.nouns[0], m.nouns[1]
	}
	return doctorReportOf(title, one, many, m.screen.Checks).String()
}

// doctorElapsed is the header's running clock: tenths while a run is short
// enough for them to mean something, whole seconds after that.
func doctorElapsed(d time.Duration) string {
	if d < 10*time.Second {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}

func runDoctorScreen(cfg config.Config, probes []doctorProbe) error {
	return runDoctorScreenTitled(cfg, probes, "", [2]string{})
}

// runDoctorScreenTitled is the screen under another command's name, with
// the nouns its header and report count in. Empty means doctor's own.
func runDoctorScreenTitled(cfg config.Config, probes []doctorProbe, title string, nouns [2]string) error {
	m := newDoctorModel(cfg, probes)
	m.screen.Title, m.nouns = title, nouns
	m.started = time.Now()
	m.markRunning(0)
	host := newScreenModel(&m.screen, defaultDoctorWidth, m.answer)
	host.begin, host.other = m.begin, m.other
	_, err := newProgram(host).Run()
	return err
}
