package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"

	"charm.land/fang/v2"
	"github.com/mattn/go-isatty"
	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/profile"
	"github.com/rfizzle/shhh/internal/update"
	"github.com/spf13/cobra"
)

var version = "dev"

// Execute runs the command tree under fang, which is what dresses every
// surface the binary has that is not a Bubble Tea program: `--help`,
// the error a failed command prints on its way out, and the `man` page it can
// now generate. It replaces cobra's own bare `Error: …` plus usage dump; the
// TUIs draw themselves and are untouched.
//
// Signal handling is deliberately not fang's (fang.WithNotifySignal): the
// interactive surfaces read ctrl+c as a keystroke in raw mode, and turning
// SIGINT into a context cancellation for the whole tree would leave the
// second ctrl+c with nothing to do on any path that does not honour the
// context promptly. runner.Run already forwards signals to the command it
// spawned, which is the one place the process is not the thing being asked
// to stop.
func Execute(ctx context.Context) error {
	return execute(ctx, NewRootCmd())
}

// ExitCode is the status the process leaves behind for whatever ran it. A run
// with nobody in front of it states what happened in it, from a closed set
// (docs/capabilities/headless.md#the-exit-code-is-the-contract); everything
// else exits 1, because a command that could not run is one fact however it
// failed and a script that branched on the difference would be branching on
// an accident.
//
// It is a function here rather than a switch in main because the codes are
// the command tree's to define: main knows only that a failure has a number.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var coded exitError
	if errors.As(err, &coded) {
		return coded.code
	}
	return 1
}

// execute is the dressing applied to a tree, split from building the real one
// so a test can render a page exactly as the binary prints it rather than
// against cobra's undressed template.
func execute(ctx context.Context, cmd *cobra.Command) error {
	return fang.Execute(ctx, cmd, fang.WithVersion(version))
}

func NewRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "shhh",
		Short: "Natural language to shell commands",
		// The product's own first sentence, because help is where most
		// readers meet it, and the sizes it names are what the command groups
		// below sort the tree into.
		// See docs/product.md#the-four-sizes.
		Long:    "shhh turns what you meant into something your machine can run, and it does that at four different sizes.",
		Version: version,
		// The root generates nothing itself: a prompt goes to `shhh cmd`,
		// which is the size it belongs to. What is left here is a word that
		// is not a command, so the refusal names the one that would have run
		// it rather than printing the whole tree at someone who was one word
		// away.
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return nil
			}
			return fmt.Errorf("unknown command %q for %q\nto generate a command from that prompt: shhh cmd %q",
				args[0], cmd.CommandPath(), strings.Join(args, " "))
		},
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// First, so that nothing after it can fail before there is
			// somewhere to write that down, and so that a library reaching
			// for the standard logger finds the file rather than the screen
			// a session is about to borrow. It opens nothing: a command that
			// logs nothing leaves no file behind.
			openLog()

			// A file that will not load stops every command here, the way
			// an unreadable prompt file stops a session: a setting the
			// person wrote and believes is in force is not something the
			// record can recover afterwards. The doctor is the one exception,
			// because it is where the refusal is read.
			cfg, err := config.Load()
			if err != nil && cmd.Annotations[ownsConfigError] == "" {
				return err
			}

			// Gateway profiles register as providers before anything
			// resolves one, so `--provider <name>` and provider.default work
			// the same as a built-in. A malformed profile is reported and
			// skipped rather than taking the session down; `shhh providers`
			// shows the details.
			profiles, profileErrs := profile.Load(profile.Dirs(config.Paths()))
			profile.Register(profiles)
			// The command that reports these failures itself is told about
			// them once, in its own report, rather than here and then again
			// as a row — the same file named twice on one screen reads as two
			// faults. Registration still runs for it: what loaded is what a
			// session would resolve either way.
			if cmd.Annotations[ownsProfileErrors] == "" {
				for _, perr := range profileErrs {
					fmt.Fprintf(os.Stderr, "shhh: provider profile: %v\n", perr)
				}
			}

			cmd.SetContext(withConfig(cmd.Context(), cfg))

			update.BackgroundCheck(version)

			// Both windows ride the first store a command opens (store.go)
			// rather than a connection of their own from here.
			setHistoryRetention(cfg.EffectiveRetentionDays())
			setObserveRetention(cfg.EffectiveObserveRetentionDays())
			// And the collector, for the same reason and in the same place:
			// a recorder is opened by four surfaces that resolve their own
			// model and provider, and this is where the config is read
			// (observe.go).
			setObserveExport(cfg.Otel.Endpoint)

			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			// A pipe reaching the root is a script written against the
			// bare-prompt form, and printing help into it would put the page
			// where a command was expected — `echo … | shhh | sh` would run
			// the help. It is a failure, and it says where the prompt goes.
			if !isatty.IsTerminal(os.Stdin.Fd()) && !isatty.IsCygwinTerminal(os.Stdin.Fd()) {
				return fmt.Errorf("a prompt on stdin is read by `shhh cmd`: echo \"…\" | shhh cmd")
			}
			return cmd.Help()
		},
	}

	// Three groups, because a reader arrives knowing which of the three they
	// want: to work, to look something up, or to set the machine up. The
	// dressing draws a group's title in the same style as USAGE and FLAGS, so
	// the grouping reads as part of the same page rather than as a list with
	// captions in it.
	// See docs/interface/surfaces.md#outside-the-tui.
	cmd.AddGroup(
		&cobra.Group{ID: groupSessions, Title: "Sessions"},
		&cobra.Group{ID: groupRecords, Title: "Records"},
		&cobra.Group{ID: groupSetup, Title: "Setup"},
	)
	addGrouped(cmd, groupSessions, newChatCmd(), newCmdCmd(), newCodeCmd())
	addGrouped(cmd, groupRecords, newChatsCmd(), newHistoryCmd(), newLogsCmd(),
		newReportsCmd(), newSnippetsCmd(), newMemoryCmd(), newMetricsCmd(),
		newObserveCmd(), newRateCmd(), newTodoCmd())
	addGrouped(cmd, groupSetup, newInitCmd(), newConfigCmd(), newDoctorCmd(),
		newProvidersCmd(), newSkillsCmd(), newMCPCmd(), newEvalCmd(), newUpdateCmd(),
		newCompletionCmd(cmd))

	// `help` is how cobra spells `--help`, and listing it beside the commands
	// that do something puts the way out among the destinations. Building it
	// here rather than leaving it to execution is what lets it be hidden:
	// cobra makes its own only when the tree has none.
	cmd.InitDefaultHelpCmd()
	for _, sub := range cmd.Commands() {
		if sub.Name() == "help" {
			sub.Hidden = true
		}
	}

	cmd.SetVersionTemplate(versionTemplate())

	return cmd
}

// The three kinds of command shhh has: the sessions that are the product, the
// records a session leaves behind, and the setup that makes one work.
const (
	groupSessions = "sessions"
	groupRecords  = "records"
	groupSetup    = "setup"
)

// addGrouped files each command under a group as it is added, so the whole
// grouping is one list to read rather than a field set once in every
// constructor.
func addGrouped(parent *cobra.Command, group string, cmds ...*cobra.Command) {
	for _, c := range cmds {
		c.GroupID = group
		parent.AddCommand(c)
	}
}

// versionTemplate is built while the command tree is, which is before
// Execute and therefore on the startup path of *every* invocation — `shhh
// "…"` included, not just `--version`. It reads the update cache and never
// the network: the blocking fetch that used to live here cost a round trip
// to api.github.com before the first frame, and cost the full five-second
// timeout on every run once that host was unreachable, because a failed
// fetch wrote no cache to stop the next one. update.BackgroundCheck, which
// PersistentPreRunE already starts, is what keeps the cache warm.
func versionTemplate() string {
	// The build is the title line rather than a row: it is what the command
	// was asked for, not a finding about it, and only the release check below
	// it has a state worth a glyph.
	r := report.Report{Title: "shhh {{.Version}} · " + runtime.GOOS + "/" + runtime.GOARCH}
	switch cached := update.CheckCached(version); {
	case version == "dev" || version == "":
		// The doctor's own wording for the same finding, because it is the
		// same finding: there is nothing to compare a dev build against.
		r.Sections = []report.Section{{Rows: []report.Row{{State: report.Skip,
			Subject: "update check", Detail: "a dev build has no released version"}}}}
	case cached != nil:
		r.Sections = []report.Section{{Rows: []report.Row{{State: report.Run,
			Subject: cached.Latest + " available", Detail: update.ReleasesPage}}}}
	}
	return r.String() + "\n"
}

// ledgerTokens adapts a ledger total to the nullable column the request
// record uses: nothing spent is an absent measurement, not a measured zero.
func ledgerTokens(n int64) *int64 {
	if n == 0 {
		return nil
	}
	return &n
}
