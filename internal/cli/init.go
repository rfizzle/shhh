package cli

import (
	"fmt"

	"github.com/rfizzle/shhh/internal/project"
	"github.com/spf13/cobra"
)

const zshSnippet = `# shhh shell integration
# Add to ~/.zshrc: eval "$(shhh init zsh)"
# For completions: shhh completion zsh > "${fpath[1]}/_shhh"

_shhh_raw() {
  local result
  result=$(shhh cmd --raw "$BUFFER" 2>/dev/null)
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
  result=$(shhh cmd --raw "$READLINE_LINE" 2>/dev/null)
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
    set -l result (shhh cmd --raw "$buf" 2>/dev/null)
    if test -n "$result"
      commandline -r -- "$result"
      commandline -f end-of-line
    end
  end
end
bind \ck _shhh_raw
`

func newInitCmd() *cobra.Command {
	var projectMode bool

	cmd := &cobra.Command{
		Use:   "init [shell]",
		Short: "Output shell integration snippet or scaffold project config",
		Long:  "Print a shell snippet to stdout for eval in your rc file, or create a .shhh/project.md context file with --project.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if projectMode {
				return initProject(".")
			}
			if len(args) == 0 {
				return fmt.Errorf("specify a shell (zsh, bash, fish) or use --project")
			}
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
	cmd.Flags().BoolVar(&projectMode, "project", false, "create a .shhh/project.md context file in the current directory")
	return cmd
}

// initProject scaffolds the context file in dir. The write itself belongs to
// internal/project, because the chat session's start screen offers the same
// one. The flag says "the current directory" and has to mean it, so the
// command hands it "." rather than a resolved root.
func initProject(dir string) error {
	_, err := project.Scaffold(dir)
	return err
}
