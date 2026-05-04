package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/mattn/go-isatty"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/raw"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/resolve"
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
			flags.ConfigAPIKey = cfg.ProviderAPIKey(resolved.Provider)

			cmd.SetContext(withConfig(cmd.Context(), cfg))
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			stdinIsTTY := isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())
			pipeMode := rawMode || !stdinIsTTY

			var prompt string
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
				prompt = strings.TrimSpace(strings.Join(lines, "\n"))
				if prompt == "" {
					return fmt.Errorf("no prompt provided on stdin")
				}
			case len(args) > 0:
				prompt = strings.Join(args, " ")
			default:
				return cmd.Help()
			}

			cfg := ConfigFrom(cmd.Context())

			resolved := resolve.Resolve(flags)
			resolved.APIKey = resolve.First(
				flags.FlagAPIKey,
				cfg.ProviderAPIKey(resolved.Provider),
				"",
			)

			p, err := provider.Resolve(resolved.Provider, provider.ResolveOpts{
				APIKey:  resolved.APIKey,
				Model:   resolved.Model,
				BaseURL: cfg.ProviderBaseURL(resolved.Provider),
			})
			if err != nil {
				return err
			}

			if pipeMode {
				err := raw.Run(cmd.Context(), raw.Opts{
					Provider: p,
					Model:    resolved.Model,
					Prompt:   prompt,
					Stdout:   os.Stdout,
					Stderr:   os.Stderr,
				})
				if err != nil {
					fmt.Fprintln(os.Stderr, "error:", err)
					os.Exit(1)
				}
				return nil
			}

			// Normal TUI mode — will be wired in a future story
			return fmt.Errorf("interactive mode not yet wired in root command — use --inline for now")
		},
	}

	cmd.PersistentFlags().StringVar(&flags.FlagProvider, "provider", "", "LLM provider (openai, openai-compatible)")
	cmd.PersistentFlags().StringVar(&flags.FlagModel, "model", "", "model name to use")
	cmd.PersistentFlags().StringVar(&flags.FlagAPIKey, "api-key", "", "API key (overrides env var)")
	cmd.Flags().BoolVar(&rawMode, "raw", false, "force pipe mode: raw command output, no TUI")
	cmd.Flags().BoolVarP(&explainMode, "explain", "e", false, "automatically explain the generated command")
	cmd.Flags().BoolVarP(&silentMode, "silent", "s", false, "suppress explanation output")

	cmd.AddCommand(newInitCmd())

	return cmd
}
