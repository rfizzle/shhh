package cli

import (
	"fmt"
	"io"

	"github.com/rfizzle/shhh/internal/project"
	"github.com/spf13/cobra"
)

const zshSnippet = `# shhh shell integration
# Add to ~/.zshrc: eval "$(shhh init zsh)"
# For completions: shhh completion zsh > "${fpath[1]}/_shhh"
# ^K overrides kill-to-end-of-line here. The reserved list keeps ctrl+k with the
# line editor inside shhh (docs/interface/reserved-keys.md#what-is-left), so
# the chord means "work on this line" in both places.

_shhh_raw() {
  local result err errf
  errf=$(mktemp) || return
  result=$(shhh cmd --raw "$BUFFER" 2>"$errf")
  err=$(<"$errf")
  rm -f "$errf"
  if [[ -n "$result" ]]; then
    BUFFER="$result"
    CURSOR=${#BUFFER}
  elif [[ -n "$err" ]]; then
    zle -M "shhh: ${err}"
  fi
  zle redisplay
}
zle -N _shhh_raw
bindkey '^K' _shhh_raw
`

const bashSnippet = `# shhh shell integration
# Add to ~/.bashrc: eval "$(shhh init bash)"
# For completions: source <(shhh completion bash)
# ^K overrides kill-to-end-of-line here. The reserved list keeps ctrl+k with the
# line editor inside shhh (docs/interface/reserved-keys.md#what-is-left), so
# the chord means "work on this line" in both places.

_shhh_raw() {
  local result err errf
  errf=$(mktemp) || return
  result=$(shhh cmd --raw "$READLINE_LINE" 2>"$errf")
  err=$(<"$errf")
  rm -f "$errf"
  if [[ -n "$result" ]]; then
    READLINE_LINE="$result"
    READLINE_POINT=${#READLINE_LINE}
  elif [[ -n "$err" ]]; then
    printf '\nshhh: %s\n' "$err" >&2
  fi
}
bind -x '"\C-k": _shhh_raw'
`

const fishSnippet = `# shhh shell integration
# Add to ~/.config/fish/config.fish: shhh init fish | source
# For completions: shhh completion fish > ~/.config/fish/completions/shhh.fish
# ^K overrides kill-to-end-of-line here. The reserved list keeps ctrl+k with the
# line editor inside shhh (docs/interface/reserved-keys.md#what-is-left), so
# the chord means "work on this line" in both places.

function _shhh_raw
  set -l buf (commandline)
  if test -n "$buf"
    set -l errf (mktemp)
    set -l result (shhh cmd --raw "$buf" 2>$errf)
    set -l err (string collect < $errf)
    rm -f $errf
    if test -n "$result"
      commandline -r -- "$result"
      commandline -f end-of-line
    else if test -n "$err"
      echo "shhh: $err" >&2
      commandline -f repaint
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
				return writeSnippet(cmd, zshSnippet)
			case "bash":
				return writeSnippet(cmd, bashSnippet)
			case "fish":
				return writeSnippet(cmd, fishSnippet)
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

// writeSnippet prints a script verbatim. The scripts hold `%s`, so they go
// through a plain write rather than a formatter.
func writeSnippet(cmd *cobra.Command, script string) error {
	_, err := io.WriteString(cmd.OutOrStdout(), script)
	return err
}
