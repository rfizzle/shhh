package cli

import (
	"fmt"
	"strings"

	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/resolve"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/spf13/cobra"
)

// newChatCmd is `shhh chat`: a conversation. It shares the session runner
// (session.go) and the TUI with `shhh code`, and differs in what it
// registers — every read the session has and nothing that acts — and in
// what the TUI is told not to draw. See docs/capabilities/chat.md.
//
// It shares the unattended runner too (print.go). A conversation behind
// --print is this session with the screen taken away, which is what lets
// work that only reads be spent in one rather than in a coding agent whose
// containment, changeset and command runner it would never use.
// See docs/capabilities/chat.md#a-conversation-runs-without-a-screen.
func newChatCmd() *cobra.Command {
	var flags resolve.Opts
	var continueLast bool
	var resumeChat string
	var printMode bool
	var popts printOpts
	var addDirs []string
	var secretFlags []string

	cmd := &cobra.Command{
		Use:   "chat [prompt]",
		Short: "Start a conversation",
		Long:  "Open a multi-turn conversation that answers questions, reads files and the web, and can delegate to read-only sub-agents. It changes nothing on the machine; use `shhh code` to edit and run.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// What the run will write, from the two spellings that say it.
			// It is settled before a provider is resolved for the reason the
			// coding agent settles it there: a shape nothing can honour is a
			// usage error, not a run that gets half way.
			output, err := resolveOutput(popts.output, popts.json)
			if err != nil {
				return err
			}
			popts.output = output
			// An empty name is a request to resume that names nothing, and
			// starting a new conversation for it would answer a question
			// nobody asked.
			if cmd.Flags().Changed("resume") && strings.TrimSpace(resumeChat) == "" {
				return fmt.Errorf("--resume needs a chat to open: pass --resume=<name>, or --resume on its own to pick one")
			}
			session := conversationSession(cmd, &flags, continueLast, resumeChat == resumeFromPicker, addDirs, secretFlags)
			session.resumeName = resumeNamed(resumeChat)
			if printMode || popts.json || cmd.Flags().Changed("output") {
				// The session says what it is rather than leaving the two
				// fields describing the screen it does not have. A delegate
				// is a spawn and a spawn is an approval; a memory is a
				// proposal the person confirms. Neither has anybody to
				// answer for it here, so neither is registered — and the
				// coding agent says the same thing the other way round, by
				// turning both on only for the interactive branch.
				// See docs/capabilities/headless.md#everything-the-session-has-unless-somebody-has-to-answer.
				session.agents, session.memory = false, false
				return runPrintSession(cmd, args, session, popts)
			}
			return runChatSession(cmd, args, session)
		},
	}

	cmd.Flags().BoolVarP(&continueLast, "continue", "c", false, "resume the most recent chat session")
	cmd.Flags().StringVarP(&resumeChat, "resume", "r", "", "resume a saved chat: on its own it opens the picker, --resume=<name> opens the one it names")
	// The value is optional, and pflag needs a stand-in for "given, unvalued"
	// to say so — the same lone dash the coding agent uses, and for the same
	// reason: a run behind --print can draw no picker, and the refusal it
	// answers with offers a spelling this command has to have.
	cmd.Flags().Lookup("resume").NoOptDefVal = resumeFromPicker
	addModelFlags(cmd, &flags)
	cmd.Flags().BoolVarP(&printMode, "print", "p", false, "run headless: stream the answer to stdout and exit (no TUI)")
	cmd.Flags().BoolVar(&popts.json, "json", false, "with --print, emit a structured JSON transcript instead of streaming text (implies --print; the same as --output json)")
	cmd.Flags().StringVar(&popts.output, "output", "", "with --print, what the run writes: text (the answer as it is written), json (the transcript at the end) or jsonl (one event per line while it runs) (implies --print)")
	// The one opt-in a conversation has anything to spend. Nothing here
	// edits a file or runs a command, so the only decision a read-only
	// session ever puts to a person is whether a request may leave the
	// machine, and this is that answer given in advance.
	// See docs/capabilities/chat.md#chat-changes-nothing.
	cmd.Flags().BoolVar(&popts.yes, "yes", false, "with --print, auto-approve the requests that leave the machine (a web fetch, a server not marked read-only)")
	addDirFlag(cmd, &addDirs)
	addSecretFlag(cmd, &secretFlags)

	return cmd
}

// conversationSession is `shhh chat`'s session, shared with `shhh chats`,
// which is the same conversation opened on a saved one. The toolset is every
// read the session has: the filesystem tools, the web, and nothing that
// acts. execute_command is not registered, so no mode can reach it.
func conversationSession(cmd *cobra.Command, flags *resolve.Opts, continueLast, resumePick bool, addDirs, secretFlags []string) chatSession {
	return chatSession{
		title:        "shhh chat",
		kind:         "chat",
		buildPrompt:  prompt.BuildConversation,
		toolDefs:     tools.Definitions(),
		flags:        flags,
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
	}
}

// addSecretFlag registers --secret: a value commands may use and the
// model never sees, named from the environment or given as NAME=value.
func addSecretFlag(cmd *cobra.Command, specs *[]string) {
	cmd.Flags().StringArrayVar(specs, "secret", nil,
		"declare a secret for commands to use as $NAME and the model never to see: NAME reads it from the environment, NAME=value gives it (repeatable; extends secrets.env)")
}
