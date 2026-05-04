package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

const zshSnippet = `# shhh shell integration
# Add to ~/.zshrc: eval "$(shhh init zsh)"

_shhh_inline() {
  local result
  result=$(shhh --inline "$BUFFER" 2>/dev/null)
  if [[ -n "$result" ]]; then
    BUFFER="$result"
    CURSOR=${#BUFFER}
  fi
  zle redisplay
}
zle -N _shhh_inline
bindkey '^K' _shhh_inline
`

func newInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init <shell>",
		Short: "Output shell integration snippet",
		Long:  "Print a shell snippet to stdout for eval in your rc file.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "zsh":
				fmt.Fprint(cmd.OutOrStdout(), zshSnippet)
				return nil
			default:
				return fmt.Errorf("unsupported shell: %q (supported: zsh)", args[0])
			}
		},
	}
	return cmd
}

