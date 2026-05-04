package cli

import (
	"github.com/rfizzle/shhh/internal/resolve"
	"github.com/spf13/cobra"

	_ "github.com/rfizzle/shhh/internal/provider"
)

var version = "dev"

func NewRootCmd() *cobra.Command {
	var flags resolve.Opts

	cmd := &cobra.Command{
		Use:     "shhh [prompt]",
		Short:   "Natural language to shell commands",
		Long:    "Turn plain English into executable shell commands.",
		Version: version,
		Args:    cobra.ArbitraryArgs,
	}

	cmd.PersistentFlags().StringVar(&flags.FlagProvider, "provider", "", "LLM provider (openai, openai-compatible)")
	cmd.PersistentFlags().StringVar(&flags.FlagModel, "model", "", "model name to use")
	cmd.PersistentFlags().StringVar(&flags.FlagAPIKey, "api-key", "", "API key (overrides env var)")

	return cmd
}
