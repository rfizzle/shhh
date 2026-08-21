package cli

import (
	"fmt"

	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/resolve"
	"github.com/rfizzle/shhh/internal/sandbox"
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
				buildPrompt:  prompt.BuildAgent,
				toolDefs:     tools.DefinitionsFull(),
				flags:        &flags,
				continueLast: continueLast,
				resumePick:   resumePick,
			}
			if printMode || popts.json {
				return runPrintSession(cmd, args, session, popts)
			}
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

	cmd.AddCommand(newCodeDoctorCmd())

	return cmd
}

// newCodeDoctorCmd reports the process-containment mechanism and resolved
// policy (S-062): which wrapper is available (and why not, when none is), the
// profile, and the write grants and deny mask agent commands run under.
func newCodeDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Report command-containment (sandbox) status",
		Long:  "Show which OS-level containment mechanism wraps agent-executed commands, or why none is available, plus the resolved write grants and deny mask.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := ConfigFrom(cmd.Context())
			policy, err := sandboxPolicy(cfg)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), sandbox.Report(sandbox.Detect(), policy))
			return nil
		},
	}
}
