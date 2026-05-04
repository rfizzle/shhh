package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/inline"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/resolve"
	"github.com/spf13/cobra"
)

var version = "dev"

func NewRootCmd() *cobra.Command {
	var flags resolve.Opts
	var inlineMode bool

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
			if len(args) == 0 {
				return cmd.Help()
			}

			prompt := strings.Join(args, " ")
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

			if inlineMode {
				err := inline.Run(cmd.Context(), inline.Opts{
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
	cmd.Flags().BoolVar(&inlineMode, "inline", false, "output only the raw command (for shell integration)")

	cmd.AddCommand(newInitCmd())

	return cmd
}
