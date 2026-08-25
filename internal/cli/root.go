package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-isatty"
	"github.com/rfizzle/shhh/internal/clipboard"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/profile"
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
	"github.com/rfizzle/shhh/internal/update"
	"github.com/spf13/cobra"
)

var version = "dev"

func NewRootCmd() *cobra.Command {
	var flags resolve.Opts
	var rawMode bool
	var explainMode bool
	var silentMode bool

	cmd := &cobra.Command{
		Use:     "shhh [prompt]",
		Short:   "Natural language to shell commands",
		Long:    "Turn plain English into executable shell commands.",
		Version: version,
		Args:    cobra.ArbitraryArgs,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			// Gateway profiles register as providers before anything
			// resolves one, so `--provider <name>` and provider.default work
			// the same as a built-in. A malformed profile is reported and
			// skipped rather than taking the session down; `shhh providers`
			// shows the details.
			profiles, profileErrs := profile.Load(profile.Dirs(config.Paths()))
			profile.Register(profiles)
			for _, perr := range profileErrs {
				fmt.Fprintf(os.Stderr, "shhh: provider profile: %v\n", perr)
			}

			flags.ConfigProvider = cfg.Provider.Default
			flags.ConfigModel = cfg.Provider.Model

			cmd.SetContext(withConfig(cmd.Context(), cfg))

			update.BackgroundCheck(version)

			go func() {
				if db, err := storage.Open(); err == nil {
					db.PurgeOldHistory(cfg.EffectiveRetentionDays())
					db.Close()
				}
			}()

			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			stdinIsTTY := isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())

			cfg := ConfigFrom(cmd.Context())
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

			p, err := provider.Resolve(resolved.Provider, provider.ResolveOpts{
				APIKey:        flags.FlagAPIKey,
				Model:         resolved.Model,
				ConfigAPIKey:  cfg.ProviderAPIKey(),
				ConfigBaseURL: cfg.ProviderBaseURL(),
				ConfigName:    cfg.ProviderDisplayName(),
			})
			if err != nil {
				return err
			}

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
					fmt.Fprintln(os.Stderr, "error:", err)
					os.Exit(1)
				}
				return nil
			}

			info := shell.Detect()
			sysPrompt := prompt.Build(info, promptExtra)

			messages := []provider.Message{
				{Role: provider.RoleSystem, Content: sysPrompt},
				{Role: provider.RoleUser, Content: userPrompt},
			}

			compOpts := provider.CompletionOpts{Model: resolved.Model}

			db, _ := storage.Open()
			if db != nil {
				defer db.Close()
			}

			ctx, cancel := context.WithCancel(cmd.Context())
			defer cancel()

			events, err := p.StreamCompletion(ctx, messages, compOpts)
			if err != nil {
				return err
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

			newExplain := func(command string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
				eCtx, eCancel := context.WithCancel(cmd.Context())
				eMsgs := []provider.Message{
					{Role: provider.RoleSystem, Content: prompt.BuildExplain()},
					{Role: provider.RoleUser, Content: command},
				}
				ev, eErr := p.StreamCompletion(eCtx, eMsgs, compOpts)
				if eErr != nil {
					eCancel()
					return nil, nil, eErr
				}
				return ev, eCancel, nil
			}

			autoExplain := explainMode && !silentMode && !cfg.Behavior.SilentMode
			model := ui.NewGenerateModel(events, cancel, messages, newStream, newExplain, info.Shell).WithAutoExplain(autoExplain)
			program := tea.NewProgram(model)
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
					Provider:  p.Name(),
					Model:     resolved.Model,
					Prompt:    userPrompt,
					Command:   result.Command,
					Action:    actionName,
					TTFT:      metrics.TTFT,
					Duration:  metrics.Duration,
					TokensIn:  metrics.TokensIn,
					TokensOut: metrics.TokensOut,
					Success:   metrics.Success,
				})
			}

			if result.Err != nil {
				return result.Err
			}

			if cfg.SafetyWarningsEnabled() {
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

			return nil
		},
	}

	cmd.PersistentFlags().StringVar(&flags.FlagProvider, "provider", "", "LLM provider (openai, anthropic, gemini, openrouter, openai-compatible)")
	cmd.PersistentFlags().StringVar(&flags.FlagModel, "model", "", "model name to use")
	cmd.PersistentFlags().StringVar(&flags.FlagAPIKey, "api-key", "", "API key (overrides env var)")
	cmd.Flags().BoolVar(&rawMode, "raw", false, "force pipe mode: raw command output, no TUI")
	cmd.Flags().BoolVarP(&explainMode, "explain", "e", false, "automatically explain the generated command")
	cmd.Flags().BoolVarP(&silentMode, "silent", "s", false, "suppress explanation output")

	cmd.AddCommand(newInitCmd())
	cmd.AddCommand(newConfigCmd())
	cmd.AddCommand(newChatCmd())
	cmd.AddCommand(newCodeCmd())
	cmd.AddCommand(newMetricsCmd())
	cmd.AddCommand(newObserveCmd())
	cmd.AddCommand(newRateCmd())
	cmd.AddCommand(newHistoryCmd())
	cmd.AddCommand(newSnippetsCmd())
	cmd.AddCommand(newMemoryCmd())
	cmd.AddCommand(newProvidersCmd())
	cmd.AddCommand(newCompletionCmd(cmd))

	cmd.SetVersionTemplate(versionTemplate())

	return cmd
}

func versionTemplate() string {
	base := "shhh version {{.Version}}\n"
	if r := update.Check(version); r != nil {
		base += fmt.Sprintf("Update available: %s → %s (brew upgrade shhh)\n", r.Current, r.Latest)
	}
	return base
}
