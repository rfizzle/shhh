package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newCompletionCmd(root *cobra.Command) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completion <shell>",
		Short: "Generate shell completion script",
		Long: `Generate a completion script for the specified shell.

To load completions:

  bash:
    source <(shhh completion bash)

  zsh:
    shhh completion zsh > "${fpath[1]}/_shhh"

  fish:
    shhh completion fish | source
    # Or persist:
    shhh completion fish > ~/.config/fish/completions/shhh.fish`,
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"bash", "zsh", "fish"},
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return root.GenBashCompletion(cmd.OutOrStdout())
			case "zsh":
				return root.GenZshCompletion(cmd.OutOrStdout())
			case "fish":
				return root.GenFishCompletion(cmd.OutOrStdout(), true)
			default:
				return fmt.Errorf("unsupported shell: %q (supported: bash, zsh, fish)", args[0])
			}
		},
	}
	return cmd
}
