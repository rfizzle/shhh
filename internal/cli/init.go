package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

const zshSnippet = `# shhh shell integration
# Add to ~/.zshrc: eval "$(shhh init zsh)"
# For completions: shhh completion zsh > "${fpath[1]}/_shhh"

_shhh_raw() {
  local result
  result=$(shhh --raw "$BUFFER" 2>/dev/null)
  if [[ -n "$result" ]]; then
    BUFFER="$result"
    CURSOR=${#BUFFER}
  fi
  zle redisplay
}
zle -N _shhh_raw
bindkey '^K' _shhh_raw
`

const bashSnippet = `# shhh shell integration
# Add to ~/.bashrc: eval "$(shhh init bash)"
# For completions: source <(shhh completion bash)

_shhh_raw() {
  local result
  result=$(shhh --raw "$READLINE_LINE" 2>/dev/null)
  if [[ -n "$result" ]]; then
    READLINE_LINE="$result"
    READLINE_POINT=${#READLINE_LINE}
  fi
}
bind -x '"\C-k": _shhh_raw'
`

const fishSnippet = `# shhh shell integration
# Add to ~/.config/fish/config.fish: shhh init fish | source
# For completions: shhh completion fish > ~/.config/fish/completions/shhh.fish

function _shhh_raw
  set -l buf (commandline)
  if test -n "$buf"
    set -l result (shhh --raw "$buf" 2>/dev/null)
    if test -n "$result"
      commandline -r -- "$result"
      commandline -f end-of-line
    end
  end
end
bind \ck _shhh_raw
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
			case "bash":
				fmt.Fprint(cmd.OutOrStdout(), bashSnippet)
				return nil
			case "fish":
				fmt.Fprint(cmd.OutOrStdout(), fishSnippet)
				return nil
			default:
				return fmt.Errorf("unsupported shell: %q (supported: zsh, bash, fish)", args[0])
			}
		},
	}
	return cmd
}
