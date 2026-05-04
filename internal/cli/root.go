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
	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/raw"
	"github.com/rfizzle/shhh/internal/resolve"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/shell"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui"
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

			flags.ConfigProvider = cfg.Provider.Default
			flags.ConfigModel = cfg.Provider.Model

			resolved := resolve.Resolve(flags)
			flags.ConfigProviderModel = cfg.ProviderModel(resolved.Provider)

			cmd.SetContext(withConfig(cmd.Context(), cfg))
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			stdinIsTTY := isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())
			pipeMode := rawMode || !stdinIsTTY

			var userPrompt string
			switch {
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
			case len(args) > 0:
				userPrompt = strings.Join(args, " ")
			default:
				return cmd.Help()
			}

			cfg := ConfigFrom(cmd.Context())

			resolved := resolve.Resolve(flags)

			p, err := provider.Resolve(resolved.Provider, provider.ResolveOpts{
				APIKey:        flags.FlagAPIKey,
				Model:         resolved.Model,
				ConfigAPIKey:  cfg.ProviderAPIKey(resolved.Provider),
				ConfigBaseURL: cfg.ProviderBaseURL(resolved.Provider),
				ConfigName:    cfg.ProviderName(resolved.Provider),
			})
			if err != nil {
				return err
			}

			if pipeMode {
				err := raw.Run(cmd.Context(), raw.Opts{
					Provider: p,
					Model:    resolved.Model,
					Prompt:   userPrompt,
					Stdout:   os.Stdout,
					Stderr:   os.Stderr,
				})
				if err != nil {
					fmt.Fprintln(os.Stderr, "error:", err)
					os.Exit(1)
				}
				return nil
			}

			info := shell.Detect()
			sysPrompt := prompt.Build(info)

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

			model := ui.NewGenerateModel(events, cancel, messages, newStream, newExplain)
			program := tea.NewProgram(model)
			finalModel, err := program.Run()
			if err != nil {
				return err
			}

			result := finalModel.(ui.GenerateModel).Result()

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
				_ = db.RecordRequest(storage.RequestRecord{
					Provider: p.Name(),
					Model:    resolved.Model,
					Prompt:   userPrompt,
					Command:  result.Command,
					Action:   actionName,
					TTFT:     metrics.TTFT,
					Duration: metrics.Duration,
					Success:  metrics.Success,
				})
			}

			if result.Err != nil {
				return result.Err
			}

			switch result.Action {
			case ui.ActionRun:
				code := runner.Run(result.Command)
				os.Exit(code)
			case ui.ActionRunAll:
				cmds := ui.SplitCommands(result.Command)
				for _, c := range cmds {
					code := runner.Run(c)
					if code != 0 {
						os.Exit(code)
					}
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
						os.Exit(code)
					}
				}
			case ui.ActionSave:
				if db != nil && result.SaveName != "" {
					if err := db.SaveSnippet(result.SaveName, result.Command); err != nil {
						fmt.Fprintf(os.Stderr, "Error saving snippet: %v\n", err)
					} else {
						fmt.Fprintf(os.Stderr, "Saved snippet %q.\n", result.SaveName)
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

	cmd.PersistentFlags().StringVar(&flags.FlagProvider, "provider", "", "LLM provider (openai, openai-compatible)")
	cmd.PersistentFlags().StringVar(&flags.FlagModel, "model", "", "model name to use")
	cmd.PersistentFlags().StringVar(&flags.FlagAPIKey, "api-key", "", "API key (overrides env var)")
	cmd.Flags().BoolVar(&rawMode, "raw", false, "force pipe mode: raw command output, no TUI")
	cmd.Flags().BoolVarP(&explainMode, "explain", "e", false, "automatically explain the generated command")
	cmd.Flags().BoolVarP(&silentMode, "silent", "s", false, "suppress explanation output")

	cmd.AddCommand(newInitCmd())
	cmd.AddCommand(newConfigCmd())
	cmd.AddCommand(newChatCmd())
	cmd.AddCommand(newMetricsCmd())
	cmd.AddCommand(newHistoryCmd())
	cmd.AddCommand(newSnippetsCmd())

	return cmd
}
