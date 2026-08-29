package cli

// The doctor surface (S-130,
// docs/interface/surfaces.md#the-supporting-screens). `shhh code doctor`
// reported on the sandbox ladder and nothing else, while the design system
// named a `shhh doctor` covering the whole setup — the name had no command
// behind it. S-130 settled that by promoting and widening: `shhh doctor` is a
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
// a doctor run is that it gets pasted into an issue (§19d).

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"
	"github.com/rfizzle/shhh/internal/clipboard"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/lsp"
	"github.com/rfizzle/shhh/internal/memory"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/resolve"
	"github.com/rfizzle/shhh/internal/sandbox"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/structural"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/update"
	"github.com/spf13/cobra"
)

// defaultDoctorWidth is what the surface is drawn at before the terminal has
// said how wide it is — the width the `Tools` artboard draws it at (§19d).
const defaultDoctorWidth = 110

// doctorGitTimeout bounds each git invocation. Reading a work tree's state is
// three cheap commands; a repository where they are not is a repository where
// waiting longer would not have helped either.
const doctorGitTimeout = 3 * time.Second

func newDoctorCmd() *cobra.Command {
	return doctorCommand("doctor", "Check this machine's shhh setup",
		"Run every setup check — the binary, the config file, the provider and its key, the local store, "+
			"command containment, container sandboxes, the workspace, the tools on PATH, durable memory, and "+
			"whether a newer shhh exists — and report each as a pass/fail row with the fix on the row that failed.",
		doctorProbes())
}

// doctorCommand builds a run over some set of the checks. `shhh doctor` takes
// all of them; `shhh code doctor` takes the containment pair, which is the
// scope that command has always had (S-130).
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
				fmt.Fprintln(cmd.OutOrStdout(), doctorReport(runDoctorChecks(cmd.Context(), cfg, probes)))
				return nil
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
		{"sandbox", probeSandbox},
		{"engine", probeEngine},
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
}

// doctorProbe is one check: the name it wears in the §6a verb field, and the
// walk that answers it. Names are seven columns or fewer so the target beside
// them keeps its gap — the field is §6c's own eight and nothing here widens
// it.
type doctorProbe struct {
	name string
	run  func(context.Context, config.Config) doctorFinding
}

// doctorProbes is every check, in the order they run and the order they read:
// what shhh is, what it was configured with, what it can talk to, then what
// it can do to this machine, and last what it might become.
func doctorProbes() []doctorProbe {
	return []doctorProbe{
		{"binary", probeBinary},
		{"config", probeConfig},
		{"model", probeModel},
		{"store", probeStore},
		{"sandbox", probeSandbox},
		{"engine", probeEngine},
		{"git", probeGit},
		{"tools", probeTools},
		{"memory", probeMemory},
		{"update", probeUpdate},
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
		State: f.State, Duration: doctorDuration(took),
	}
}

// doctorDuration is the 6-column field: blank under half a second, the same
// rule every activity row in the product follows (§6a). Most checks are a
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

func probeConfig(_ context.Context, cfg config.Config) doctorFinding {
	paths := config.Paths()
	read := ""
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			read = p
			break
		}
	}
	return doctorConfig(read, paths, cfg)
}

// doctorConfig says which file was read and what it set. No file at all is
// not a failure — shhh runs on its defaults — but the row says so plainly
// rather than being left out, because "why is this on" is the question a
// setup check gets asked (§19a).
func doctorConfig(read string, paths []string, cfg config.Config) doctorFinding {
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
	return doctorFinding{
		Subject: shortPath(read),
		Detail:  countOf(configSettingsSet(cfg), "setting set", "settings set"),
		Outcome: "ok",
	}
}

// configSettingsSet counts the settings standing against the defaults. It is
// the header count `shhh config` states, read here so the two screens agree
// on what "set" means: a value the file supplied, not a value shhh chose.
func configSettingsSet(cfg config.Config) int {
	n := 0
	for _, set := range []bool{
		cfg.Provider.Default != "", cfg.Provider.Model != "", cfg.Provider.APIKey != "",
		cfg.Provider.BaseURL != "", cfg.Provider.Name != "",
		cfg.Behavior.SilentMode, cfg.Behavior.Shell != "", cfg.Behavior.ContextMaxTokens > 0,
		cfg.Behavior.MaxToolRounds != 0, cfg.Behavior.SafetyWarnings != nil,
		cfg.Appearance.Mouse, cfg.Appearance.Notify != nil,
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

func probeModel(ctx context.Context, cfg config.Config) doctorFinding {
	resolved := resolve.Resolve(resolve.Opts{
		ConfigProvider:  cfg.Provider.Default,
		ConfigModel:     cfg.Provider.Model,
		ConfigReasoning: cfg.Provider.Reasoning,
	})
	survey := resolve.SurveyPlaces(ctx, resolve.SurveyOpts{
		Provider:     resolved.Provider,
		ConfigAPIKey: cfg.Provider.APIKey,
		ConfigPaths:  config.Paths(),
	})
	f := doctorModelFinding(resolved.Provider, resolved.Model, survey)
	// A model decided by an env var or a flag looks exactly like one decided
	// by the config file, which is how `/model default` came to look broken
	// while writing the file correctly (S-136). The row that reports the
	// model is the row that has to say who chose it.
	if over := resolve.ModelOutranks(resolve.Opts{ConfigModel: cfg.Provider.Model}); over != "" && cfg.Provider.Model != "" {
		f.Detail = joinDetail(f.Detail, over+", overruling provider.model = "+cfg.Provider.Model)
	}
	// A reasoning level is the other half of what a request asks for (S-139),
	// and an unreadable one is a session that will fail to start rather than
	// quietly reason less than it was told to.
	if effort, err := provider.ParseEffort(resolved.Reasoning); err != nil {
		f.Detail = joinDetail(f.Detail, err.Error())
	} else if effort.On() {
		f.Detail = joinDetail(f.Detail, "reasoning "+effort.String())
	}
	return f
}

// doctorModelFinding reads the same walk the no-provider card reads (§17b):
// the four places a key can come from, and what was in each. A key that was
// found is reported as found and not as accepted — accepting one means
// spending a request on it, and a diagnostic that billed you for running it
// would be a diagnostic nobody runs.
//
// The check is named `model` rather than `provider` because §6c's verb field
// is eight columns and `provider` fills all eight, leaving the target beside
// it with no gap; `model` is the verb §17a already gives a provider failure,
// so the two rows line up.
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
// four places, each with what was there. It is the card's own body (§17b)
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
// (§19a), and this is the one place in the report a key is mentioned at all,
// so it is worth saying that the masking is inherited rather than reapplied.
func doctorMasked(finding string) string { return finding }

func probeStore(context.Context, config.Config) doctorFinding {
	db, err := storage.Open()
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

// doctorStorePath is where the local store lives, for the row to name. It is
// derived the same way storage.Open derives it.
func doctorStorePath() string {
	if runtime.GOOS == "darwin" {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, "Library", "Application Support", "shhh", "shhh.db")
		}
	}
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "shhh", "shhh.db")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "share", "shhh", "shhh.db")
	}
	return "shhh.db"
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
// `⚠ UNCONTAINED` to its title bar when nothing wraps the command (§2b).
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

func probeTools(context.Context, config.Config) doctorFinding {
	var found, missing []string
	for _, tool := range structural.ToolBinaries() {
		if _, err := exec.LookPath(tool); err == nil {
			found = append(found, tool)
			continue
		}
		missing = append(missing, tool)
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

func probeMemory(context.Context, config.Config) doctorFinding {
	dir, err := os.Getwd()
	if err != nil {
		return doctorMemory("", 0, 0, err)
	}
	db, err := storage.Open()
	if err != nil {
		// The store's own row already says this, and saying it twice would be
		// the report blaming one fault on two checks.
		return doctorMemory(memory.ProjectScope(dir), 0, 0, nil)
	}
	defer db.Close()
	entries, listErr := memory.NewStore(db, memory.ProjectScope(dir)).List()
	project := 0
	for _, e := range entries {
		if e.Scope != memory.GlobalScope {
			project++
		}
	}
	return doctorMemory(memory.ProjectScope(dir), project, len(entries)-project, listErr)
}

// doctorMemory reads durable memory (S-070) for this project. An empty store
// is the ordinary state of a new project rather than a fault, so it is `⊘`
// with the words for it, not a warning.
func doctorMemory(project string, forProject, global int, err error) doctorFinding {
	if err != nil {
		return doctorFinding{
			Subject: "memory did not load", Detail: err.Error(),
			Outcome: "unreadable", State: components.DoctorWarned,
			Consequence: "sessions in this project will start with nothing remembered",
		}
	}
	if forProject+global == 0 {
		return doctorFinding{
			Subject: "nothing remembered yet", Detail: shortPath(project),
			Outcome: "empty", State: components.DoctorSkipped,
		}
	}
	detail := countOf(forProject, "entry", "entries") + " for this project"
	if global > 0 {
		detail += " · " + strconv.Itoa(global) + " global"
	}
	return doctorFinding{Subject: shortPath(project), Detail: detail, Outcome: "ok"}
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

// doctorReport is the run as text: the same rows the surface draws, without
// the grid. It is what `--table` prints and what `[c]` copies, so a report
// pasted into an issue carries the consequences and the fixes too — those are
// the half of the run somebody else needs in order to help.
func doctorReport(checks []components.DoctorCheck) string {
	var b strings.Builder
	fmt.Fprintf(&b, "shhh doctor — %s\n\n", countOf(len(checks), "check", "checks"))
	for _, check := range checks {
		target := check.Subject
		if check.Detail != "" {
			target = joinDetail(target, check.Detail)
		}
		fmt.Fprintf(&b, "%s %-8s %s", doctorReportGlyph(check.State), check.Name, target)
		if check.Outcome != "" {
			fmt.Fprintf(&b, "  [%s]", check.Outcome)
		}
		b.WriteString("\n")
		if check.Consequence != "" {
			fmt.Fprintf(&b, "    %s\n", check.Consequence)
		}
		for _, line := range check.Fix {
			fmt.Fprintf(&b, "      %s\n", line)
		}
	}
	fmt.Fprintf(&b, "\n%s\n", doctorSummaryLine(checks))
	return strings.TrimRight(b.String(), "\n")
}

// doctorReportGlyph is the text report's leading glyph. It is the surface's
// own, because a report pasted somewhere else should still read as this
// product's (§10d) — and because the glyph, not the colour, is what carries
// the state in the first place (invariant 1).
func doctorReportGlyph(state components.DoctorState) string {
	switch state {
	case components.DoctorWarned:
		return "⚠"
	case components.DoctorFailed:
		return "✗"
	case components.DoctorSkipped:
		return "⊘"
	case components.DoctorRunning:
		return "▸"
	case components.DoctorQueued:
		return "·"
	}
	return "✓"
}

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
	width   int

	screen components.DoctorScreen
}

// doctorDoneMsg carries one probe's answer back to the model.
type doctorDoneMsg struct {
	at      int
	finding doctorFinding
	took    time.Duration
}

// doctorTickMsg drives the one spinner on the screen, at §10c's own interval.
type doctorTickMsg time.Time

func newDoctorModel(cfg config.Config, probes []doctorProbe) doctorModel {
	m := doctorModel{cfg: cfg, probes: probes, width: defaultDoctorWidth}
	m.screen.Checks = make([]components.DoctorCheck, len(m.probes))
	for i, probe := range m.probes {
		m.screen.Checks[i] = components.DoctorCheck{
			Name: probe.name, Subject: doctorQueuedSubject(probe.name),
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
	case "model":
		return "the provider and where its key comes from"
	case "store":
		return "the local store"
	case "sandbox":
		return "what contains an approved command"
	case "engine":
		return "container sandboxes"
	case "git":
		return "the workspace, and whether an edit can be undone"
	case "tools":
		return "the tools and language servers on PATH"
	case "memory":
		return "what this project remembers"
	case "update":
		return "check for a newer shhh"
	}
	return name
}

func (m doctorModel) Init() tea.Cmd {
	return tea.Batch(m.runNext(), doctorTick())
}

// runNext starts the check at the cursor, or nothing when the run is done.
func (m doctorModel) runNext() tea.Cmd {
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

// doctorTick is the one tick source (§10c): the spinner in the header and on
// the running row are the same frame.
func doctorTick() tea.Cmd {
	return tea.Tick(components.SpinnerInterval, func(t time.Time) tea.Msg {
		return doctorTickMsg(t)
	})
}

func (m doctorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.screen.MaxLines = msg.Width, msg.Height
		return m, nil

	case doctorTickMsg:
		if !m.screen.Running {
			// The spinner stops when the run does; a frame still turning over
			// a finished run would say the screen is doing something.
			return m, nil
		}
		m.screen.Frame++
		m.screen.Elapsed = doctorElapsed(time.Since(m.started))
		return m, doctorTick()

	case doctorDoneMsg:
		m.screen.Checks[msg.at] = doctorCheck(m.probes[msg.at].name, msg.finding, msg.took)
		m.at = msg.at + 1
		m.screen.Elapsed = doctorElapsed(time.Since(m.started))
		if m.at >= len(m.probes) {
			m.screen.Running = false
			return m, nil
		}
		m.markRunning(m.at)
		return m, m.runNext()

	case tea.KeyPressMsg:
		m.screen.Notice = ""
		done, result := m.screen.Update(msg)
		if command, ok := result.(components.DoctorCommand); ok {
			return m.apply(command)
		}
		if done {
			return m, tea.Quit
		}
	}
	return m, nil
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
func (m doctorModel) apply(command components.DoctorCommand) (tea.Model, tea.Cmd) {
	switch command.Act {
	case components.DoctorCopy:
		if res := clipboard.Copy(doctorReport(m.screen.Checks)); !res.OK {
			m.screen.Notice = "clipboard: " + res.Warning
		} else {
			m.screen.Notice = "copied the report to the clipboard"
		}
		return m, nil
	case components.DoctorRerun:
		fresh := newDoctorModel(m.cfg, m.probes)
		fresh.width, fresh.screen.MaxLines = m.width, m.screen.MaxLines
		fresh.started = time.Now()
		fresh.markRunning(0)
		return fresh, tea.Batch(fresh.runNext(), doctorTick())
	}
	return m, nil
}

// View is the frame: the doctor screen, on the alt screen it takes over
// (S-155).
func (m doctorModel) View() tea.View {
	v := tea.NewView(m.screen.View(m.width))
	v.AltScreen = true
	return v
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
	m := newDoctorModel(cfg, probes)
	m.started = time.Now()
	m.markRunning(0)
	_, err := newProgram(m).Run()
	return err
}
