package cli

import (
	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/resolve"
	"github.com/rfizzle/shhh/internal/structural"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/spf13/cobra"
)

// newCodeCmd is the coding-agent entry point: the same chat TUI as `shhh chat`
// but with the agent system prompt and the full toolset (read-only + exec +
// write/edit). Agent-only flags belong here, not on `shhh chat`.
func newCodeCmd() *cobra.Command {
	var flags resolve.Opts
	var continueLast bool
	var resumePick bool
	var printMode bool
	var popts printOpts

	cmd := &cobra.Command{
		Use:   "code [prompt]",
		Short: "Start a coding agent session",
		Long:  "Open an agent session that can read, search, edit, and run code in the current directory, with approval-gated file edits and commands.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			session := chatSession{
				title:        "shhh code",
				kind:         "code",
				buildPrompt:  prompt.BuildAgent,
				toolDefs:     tools.DefinitionsFull(),
				flags:        &flags,
				continueLast: continueLast,
				resumePick:   resumePick,
				web:          openWebTools(ConfigFrom(cmd.Context())),
				lsp:          openLSP(ConfigFrom(cmd.Context())),
				structural:   structural.Detect(),
				gate:         true,
				processes:    true,
			}
			if printMode || popts.json || popts.sandbox {
				return runPrintSession(cmd, args, session, popts)
			}
			// Sub-agent orchestration (S-068) and durable memory (S-070) are
			// interactive-only: approvals and memory confirmations route to
			// the user, which headless print mode cannot do.
			session.agents = true
			session.memory = true
			return runChatSession(cmd, args, session)
		},
	}

	cmd.Flags().BoolVarP(&continueLast, "continue", "c", false, "resume the most recent chat session")
	cmd.Flags().BoolVarP(&resumePick, "resume", "r", false, "pick a saved chat to resume")
	cmd.Flags().StringVar(&flags.FlagProvider, "provider", "", "LLM provider")
	cmd.Flags().StringVar(&flags.FlagModel, "model", "", "model name to use")
	cmd.Flags().StringVar(&flags.FlagAPIKey, "api-key", "", "API key (overrides env var)")
	cmd.Flags().BoolVarP(&printMode, "print", "p", false, "run headless: stream the response to stdout and exit (no TUI)")
	cmd.Flags().BoolVar(&popts.json, "json", false, "with --print, emit a structured JSON transcript instead of streaming text (implies --print)")
	cmd.Flags().BoolVar(&popts.yes, "yes", false, "with --print, auto-approve file edits and commands (safety-flagged commands stay denied)")
	cmd.Flags().StringArrayVar(&popts.allow, "allow", nil, "with --print, auto-approve commands matching this prefix (repeatable; extends the config allowlist)")
	cmd.Flags().BoolVar(&popts.sandbox, "sandbox", false, "run approved commands inside a disposable container sandbox; needs a configured digest-pinned image (implies --print)")

	cmd.AddCommand(newCodeDoctorCmd())

	return cmd
}

// newCodeDoctorCmd is `shhh code doctor`: the containment slice of `shhh
// doctor` (S-130). The command was scoped to process containment (S-062) and
// container sandboxes (S-063) before the design system named a `shhh doctor`
// covering the whole setup; S-130 promoted and widened that one and left this
// as the way into the same two checks from the coding agent. `/sandbox
// doctor` still prints the long text report in a session, where the question
// really is only about containment.
func newCodeDoctorCmd() *cobra.Command {
	return doctorCommand("doctor",
		"Report command-containment and container-sandbox status",
		"Show which OS-level containment mechanism wraps agent-executed commands and which container engine "+
			"can run sandboxes, or why not, and what to do about either. These are two of the checks "+
			"`shhh doctor` runs over the whole setup.",
		containmentProbes())
}
