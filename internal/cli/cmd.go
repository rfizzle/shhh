package cli

// `shhh cmd`: one prompt, one command, one decision — the smallest of the
// four sizes and the one the product is named for.
// See docs/product.md#the-four-sizes.

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/mattn/go-isatty"
	"github.com/rfizzle/shhh/internal/clipboard"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/raw"
	"github.com/rfizzle/shhh/internal/resolve"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/safety"
	"github.com/rfizzle/shhh/internal/shell"
	"github.com/rfizzle/shhh/internal/stdin"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui"
	"github.com/spf13/cobra"
)

// newCmdCmd is the one-shot: a prompt in, a command on screen, and a row of
// keys that decide what happens to it. With no terminal on the other end —
// piped, scripted, in CI — it drops every piece of chrome and writes the bare
// command to stdout instead, which is what `--raw` forces when there is one.
// See docs/capabilities/generation.md.
func newCmdCmd() *cobra.Command {
	var flags resolve.Opts
	var rawMode bool
	var explainMode bool
	var silentMode bool

	cmd := &cobra.Command{
		Use:   "cmd [prompt]",
		Short: "Generate one shell command",
		Long:  "Turn a prompt into a single shell command, shown with what it does and a row of keys — run it, edit it, ask for another, copy it, save it. Nothing runs until you say so.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			stdinIsTTY := isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())

			cfg := ConfigFrom(cmd.Context())
			// The config half of a resolution, the way every other command
			// that reaches a provider fills it in (session.go): the root
			// carries no model flags to fill in for anyone now.
			flags.ConfigProvider = cfg.Provider.Default
			flags.ConfigModel = cfg.Provider.Model
			flags.ConfigReasoning = cfg.Provider.Reasoning

			maxChars := cfg.EffectiveContextMaxTokens() * 4

			var userPrompt string
			var pipeMode bool

			switch {
			case !stdinIsTTY && len(args) > 0:
				stdinContent, err := stdin.Read(os.Stdin, maxChars)
				if err != nil {
					return err
				}
				userPrompt = strings.Join(args, " ")
				if stdinContent != "" {
					userPrompt = stdin.FormatPromptWithContext(userPrompt, stdinContent)
				}
				pipeMode = rawMode
			case !stdinIsTTY && len(args) == 0:
				scanner := bufio.NewScanner(os.Stdin)
				var lines []string
				for scanner.Scan() {
					lines = append(lines, scanner.Text())
				}
				if err := scanner.Err(); err != nil {
					return fmt.Errorf("reading stdin: %w", err)
				}
				userPrompt = strings.TrimSpace(strings.Join(lines, "\n"))
				if userPrompt == "" {
					return fmt.Errorf("no prompt provided on stdin")
				}
				pipeMode = true
			case len(args) > 0:
				userPrompt = strings.Join(args, " ")
				pipeMode = rawMode
			default:
				return cmd.Help()
			}

			resolved := resolve.Resolve(flags)

			// A session with no provider gets the card that says where shhh
			// looked, not the dialect's own one-line complaint.
			p, req, err := resolveProvider(cmd.Context(), cfg, providerRequest{
				Provider: resolved.Provider,
				Model:    resolved.Model,
				APIKey:   flags.FlagAPIKey,
			})
			if err != nil {
				return err
			}
			resolved.Provider, resolved.Model = req.Provider, req.Model

			// The one-shot spends on more than the command it prints: a
			// revision, an explanation and the description written for a
			// saved snippet are all requests too. Gating the provider once,
			// here, is what stops those being free in the record — the
			// alternative is remembering to instrument each of them, and the
			// explanation was already being missed.
			// See docs/architecture.md#spend-is-counted-at-the-provider.
			ledger := meter.New(loadPricing())
			p = meter.WithFallbackModel(ledger.For(p, meter.SourceOneShot), resolved.Model)

			promptExtra := prompt.CombineExtra(cfg.Behavior.SystemPromptExtra, project.FindContext())

			if pipeMode {
				err := raw.Run(cmd.Context(), raw.Opts{
					Provider:          p,
					Model:             resolved.Model,
					Prompt:            userPrompt,
					SystemPromptExtra: promptExtra,
					Stdout:            os.Stdout,
					Stderr:            os.Stderr,
				})
				if err != nil {
					// Piped output has no chrome by contract, so the failure
					// arrives as one classified line rather than as a row.
					if line, ok := ui.FailureLine(err); ok {
						fmt.Fprintln(os.Stderr, line)
					} else {
						fmt.Fprintln(os.Stderr, "error:", err)
					}
					os.Exit(1)
				}
				return nil
			}

			info := shell.Detect()
			// The interactive one-shot asks for the alternatives section as
			// well; the pipe path above went out through prompt.Build
			// and its stdout is one command, as it has always been.
			sysPrompt := prompt.BuildAlternatives(info, promptExtra)

			messages := []provider.Message{
				{Role: provider.RoleSystem, Content: sysPrompt},
				{Role: provider.RoleUser, Content: userPrompt},
			}

			effort, err := provider.ParseEffort(resolved.Reasoning)
			if err != nil {
				return err
			}
			compOpts := provider.CompletionOpts{Model: resolved.Model, Effort: effort}

			db, _ := openStore()
			if db != nil {
				defer db.Close()
			}

			ctx, cancel := context.WithCancel(cmd.Context())
			defer cancel()

			events, err := p.StreamCompletion(ctx, messages, compOpts)
			if err != nil {
				return reportFailure(err, resolved.Model)
			}

			var metrics *storage.StreamMetrics
			events, metrics = storage.InstrumentStream(events)

			newStream := func(msgs []provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error) {
				sCtx, sCancel := context.WithCancel(cmd.Context())
				ev, sErr := p.StreamCompletion(sCtx, msgs, compOpts)
				if sErr != nil {
					sCancel()
					return nil, nil, sErr
				}
				return ev, sCancel, nil
			}

			newExplain := func(command string, long bool) (<-chan provider.StreamEvent, context.CancelFunc, error) {
				eCtx, eCancel := context.WithCancel(cmd.Context())
				eMsgs := []provider.Message{
					{Role: provider.RoleSystem, Content: prompt.BuildExplain(long)},
					{Role: provider.RoleUser, Content: command},
				}
				ev, eErr := p.StreamCompletion(eCtx, eMsgs, compOpts)
				if eErr != nil {
					eCancel()
					return nil, nil, eErr
				}
				return ev, eCancel, nil
			}

			// The explanation is on by default now: a command you do
			// not understand is a command you should not run. `-e` buys the
			// long form rather than the only form, and silent mode still
			// suppresses both.
			explain := ui.ExplainBrief
			switch {
			case silentMode || cfg.Behavior.SilentMode:
				explain = ui.ExplainNone
			case explainMode:
				explain = ui.ExplainLong
			}
			model := ui.NewGenerateModel(events, cancel, messages, newStream, newExplain, info.Shell).WithExplain(explain)
			program := newProgram(model)
			finalModel, err := program.Run()
			if err != nil {
				return err
			}

			result := finalModel.(ui.GenerateModel).Result()

			var requestID int64
			if db != nil {
				actionName := ""
				switch result.Action {
				case ui.ActionRun:
					actionName = "run"
				case ui.ActionRunAll:
					actionName = "run-all"
				case ui.ActionRunStep:
					actionName = "run-step"
				case ui.ActionCopy:
					actionName = "copy"
				case ui.ActionRevise:
					actionName = "revise"
				case ui.ActionEdit:
					actionName = "edit"
				case ui.ActionSave:
					actionName = "save"
				case ui.ActionCancel:
					actionName = "cancel"
				}
				requestID, _ = db.RecordRequest(storage.RequestRecord{
					Provider: p.Name(),
					Model:    resolved.Model,
					Prompt:   userPrompt,
					Command:  result.Command,
					Action:   actionName,
					TTFT:     metrics.TTFT,
					Duration: metrics.Duration,
					// Timing belongs to the first request; the tokens are
					// every request the interaction made — revisions and
					// explanations included — because that is what the user
					// paid to get this command.
					TokensIn:  ledgerTokens(ledger.Total().In),
					TokensOut: ledgerTokens(ledger.Total().Out),
					Success:   metrics.Success,
				})
			}

			if result.Err != nil {
				// Classified, never raw: the one-shot renders the
				// same failure row the session does, with the way out stated as
				// a command rather than as a key nothing is listening for.
				return reportFailure(result.Err, resolved.Model)
			}

			// The result surface already moves the safe default on a
			// destructive command and takes a deliberate `y` for it,
			// so asking the same question again here is a second prompt for
			// one decision. It still runs for anything that reached this
			// point without being asked.
			if cfg.SafetyWarningsEnabled() && !result.Confirmed {
				if result.Action == ui.ActionRun || result.Action == ui.ActionRunAll || result.Action == ui.ActionRunStep {
					if warnings := safety.Check(result.Command); len(warnings) > 0 {
						fmt.Fprintln(os.Stderr, "\n⚠ Safety warning:")
						for _, w := range warnings {
							fmt.Fprintf(os.Stderr, "  • %s\n", w.Risk)
						}
						fmt.Fprint(os.Stderr, "\nProceed? [y/N] ")
						reader := bufio.NewReader(os.Stdin)
						input, _ := reader.ReadString('\n')
						input = strings.TrimSpace(strings.ToLower(input))
						if input != "y" && input != "yes" {
							fmt.Fprintln(os.Stderr, "Aborted.")
							return nil
						}
					}
				}
			}

			switch result.Action {
			case ui.ActionRun:
				code := runner.Run(result.Command)
				if db != nil && requestID > 0 {
					_ = db.RecordExitCode(requestID, code)
				}
				os.Exit(code)
			case ui.ActionRunAll:
				cmds := ui.SplitCommands(result.Command)
				for _, c := range cmds {
					code := runner.Run(c)
					if code != 0 {
						if db != nil && requestID > 0 {
							_ = db.RecordExitCode(requestID, code)
						}
						os.Exit(code)
					}
				}
				if db != nil && requestID > 0 {
					_ = db.RecordExitCode(requestID, 0)
				}
			case ui.ActionRunStep:
				cmds := ui.SplitCommands(result.Command)
				reader := bufio.NewReader(os.Stdin)
				for i, c := range cmds {
					fmt.Fprintf(os.Stderr, "Step %d/%d: %s\n", i+1, len(cmds), c)
					fmt.Fprint(os.Stderr, "Run? [Y/n] ")
					input, _ := reader.ReadString('\n')
					input = strings.TrimSpace(strings.ToLower(input))
					if input == "n" || input == "no" {
						fmt.Fprintln(os.Stderr, "Skipped remaining steps.")
						break
					}
					code := runner.Run(c)
					if code != 0 {
						fmt.Fprintf(os.Stderr, "Step %d exited with code %d. Stop.\n", i+1, code)
						if db != nil && requestID > 0 {
							_ = db.RecordExitCode(requestID, code)
						}
						os.Exit(code)
					}
				}
				if db != nil && requestID > 0 {
					_ = db.RecordExitCode(requestID, 0)
				}
			case ui.ActionSave:
				if db != nil && result.SaveName != "" {
					if err := db.SaveSnippet(result.SaveName, result.Command); err != nil {
						fmt.Fprintf(os.Stderr, "Error saving snippet: %v\n", err)
					} else {
						fmt.Fprintf(os.Stderr, "Saved snippet %q.\n", result.SaveName)
						if desc := generateDescription(cmd.Context(), p, result.Command); desc != "" {
							_ = db.UpdateSnippetDescription(result.SaveName, desc)
							fmt.Fprintf(os.Stderr, "Description: %s\n", desc)
						}
					}
				}
			case ui.ActionCopy:
				cr := clipboard.Copy(result.Command)
				if cr.Warning != "" {
					fmt.Fprintln(os.Stderr, cr.Warning)
				} else {
					fmt.Fprintln(os.Stderr, "Copied to clipboard.")
				}
			}

			// Saving a snippet writes a description, and writing it is
			// another request. It lands after the row above, so the row is
			// revised rather than left understating the interaction.
			if db != nil && requestID != 0 {
				total := ledger.Total()
				_ = db.UpdateRequestTokens(requestID, ledgerTokens(total.In), ledgerTokens(total.Out))
			}

			return nil
		},
	}

	// Declaration order is reading order: the flags that shape the one-shot
	// first, then the ones that name a model. Sorting would interleave them,
	// and a `--api-key` above `--explain` says nothing about either.
	cmd.Flags().SortFlags = false
	cmd.Flags().BoolVarP(&explainMode, "explain", "e", false, "explain the generated command at length (one line is shown by default)")
	cmd.Flags().BoolVarP(&silentMode, "silent", "s", false, "suppress explanation output")
	// fang draws one FLAGS section, so the break between the two kinds is a
	// blank line hung off the last one-shot flag's description. A list you
	// scan needs an axis, and this list has two halves.
	// See docs/interface/surfaces.md#outside-the-tui.
	cmd.Flags().BoolVar(&rawMode, "raw", false, "force pipe mode: raw command output, no TUI\n")
	addModelFlags(cmd, &flags)

	return cmd
}
