package cli

import (
	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/resolve"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/spf13/cobra"
)

// newChatCmd is `shhh chat`: a conversation. It shares the session runner
// (session.go) and the TUI with `shhh code`, and differs in what it
// registers — every read the session has and nothing that acts — and in
// what the TUI is told not to draw. See docs/capabilities/chat.md.
func newChatCmd() *cobra.Command {
	var flags resolve.Opts
	var continueLast bool
	var resumePick bool
	var addDirs []string
	var secretFlags []string

	cmd := &cobra.Command{
		Use:   "chat [prompt]",
		Short: "Start a conversation",
		Long:  "Open a multi-turn conversation that answers questions, reads files and the web, and can delegate to read-only sub-agents. It changes nothing on the machine; use `shhh code` to edit and run.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// The conversation's toolset is every read the session has:
			// the filesystem tools, the web, and nothing that could act.
			// execute_command is not registered, so no mode can reach it.
			return runChatSession(cmd, args, chatSession{
				title:        "shhh chat",
				kind:         "chat",
				buildPrompt:  prompt.BuildConversation,
				toolDefs:     tools.Definitions(),
				flags:        &flags,
				continueLast: continueLast,
				resumePick:   resumePick,
				addDirs:      addDirs,
				skills:       loadSkills(),
				secretFlags:  secretFlags,
				web:          openWebTools(ConfigFrom(cmd.Context())),
				agents:       true,
				memory:       true,
				conversation: true,
				mcp:          true,
			})
		},
	}

	cmd.Flags().BoolVarP(&continueLast, "continue", "c", false, "resume the most recent chat session")
	cmd.Flags().BoolVarP(&resumePick, "resume", "r", false, "pick a saved chat to resume")
	cmd.Flags().StringVar(&flags.FlagProvider, "provider", "", "provider to send the session to")
	cmd.Flags().StringVar(&flags.FlagModel, "model", "", "model name to use")
	cmd.Flags().StringVar(&flags.FlagAPIKey, "api-key", "", "key for the provider, overriding the env var")
	addDirFlag(cmd, &addDirs)
	addSecretFlag(cmd, &secretFlags)

	return cmd
}

// addSecretFlag registers --secret: a value commands may use and the
// model never sees, named from the environment or given as NAME=value.
func addSecretFlag(cmd *cobra.Command, specs *[]string) {
	cmd.Flags().StringArrayVar(specs, "secret", nil,
		"declare a secret for commands to use as $NAME and the model never to see: NAME reads it from the environment, NAME=value gives it (repeatable; extends secrets.env)")
}
