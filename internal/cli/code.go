package cli

import (
	"fmt"
	"strings"

	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/resolve"
	"github.com/rfizzle/shhh/internal/structural"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/spf13/cobra"
)

// resumeFromPicker is what --resume carries when it was given no chat to
// open: show the browser and let the person choose. Naming one instead is
// how the same request is made where there is nobody to choose — a run
// behind --print, which can draw no picker and would otherwise have to
// refuse the flag outright.
const resumeFromPicker = "-"

// resumeNamed is the conversation --resume named, or empty when it named
// none. The picker's stand-in is not a chat anybody saved, so it must not
// reach the store as one.
func resumeNamed(value string) string {
	if value == resumeFromPicker {
		return ""
	}
	return value
}

// newCodeCmd is the coding-agent entry point: the same chat TUI as `shhh
// chat` but with the agent system prompt and the full toolset (read-only +
// exec + write/edit). Agent-only flags belong here, not on `shhh chat`.
func newCodeCmd() *cobra.Command {
	var flags resolve.Opts
	var continueLast bool
	var resumeChat string
	var printMode bool
	var popts printOpts
	var addDirs []string
	var secretFlags []string
	var requireSandbox bool

	cmd := &cobra.Command{
		Use:   "code [prompt]",
		Short: "Start a coding agent session",
		Long:  "Open an agent session that can read, search, edit, and run code in the current directory, with approval-gated file edits and commands.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// --max-rounds applies to a session as much as to a headless
			// run: `--max-rounds 0` is how a session is told up front to run
			// unattended, which is the one thing the in-session [+] and [!]
			// cannot do — they are keys, and nobody is there to press them.
			popts.maxRoundsSet = cmd.Flags().Changed("max-rounds")
			// What the run will write, from the two spellings that say it.
			// It is settled here rather than inside the run so that a value
			// nothing can honour is refused before a provider is resolved.
			output, err := resolveOutput(popts.output, popts.json)
			if err != nil {
				return err
			}
			popts.output = output
			headless := printMode || popts.json || popts.sandbox || cmd.Flags().Changed("output")
			if popts.maxRoundsSet && popts.maxRounds < 0 {
				return fmt.Errorf("--max-rounds cannot be negative (0 removes the cap)")
			}
			if cmd.Flags().Changed("resume") && strings.TrimSpace(resumeChat) == "" {
				return fmt.Errorf("--resume needs a chat to open: pass --resume=<name>, or --resume on its own to pick one")
			}
			session := chatSession{
				title:          "shhh code",
				kind:           "code",
				buildPrompt:    prompt.BuildAgent,
				toolDefs:       tools.DefinitionsFull(),
				flags:          &flags,
				continueLast:   continueLast,
				resumePick:     resumeChat == resumeFromPicker,
				resumeName:     resumeNamed(resumeChat),
				web:            openWebTools(ConfigFrom(cmd.Context())),
				lsp:            openLSP(ConfigFrom(cmd.Context())),
				structural:     structural.Detect(),
				gate:           true,
				processes:      true,
				maxRounds:      popts.maxRounds,
				maxRoundsSet:   popts.maxRoundsSet,
				addDirs:        addDirs,
				skills:         loadSkills(),
				secretFlags:    secretFlags,
				mcp:            true,
				requireSandbox: requireSandbox,
			}
			if headless {
				return runPrintSession(cmd, args, session, popts)
			}
			// Sub-agent orchestration and durable memory are
			// interactive-only: approvals and memory confirmations route to
			// the user, which headless print mode cannot do.
			session.agents = true
			session.memory = true
			return runChatSession(cmd, args, session)
		},
	}

	cmd.Flags().BoolVarP(&continueLast, "continue", "c", false, "resume the most recent chat session")
	cmd.Flags().StringVarP(&resumeChat, "resume", "r", "", "resume a saved chat: on its own it opens the picker, --resume=<name> opens the one it names")
	// The value is optional, and pflag needs a stand-in for "given, unvalued"
	// to say so. A lone dash is the one spelling nothing in the store will
	// collide with: a slot is a timestamp or a name somebody typed.
	cmd.Flags().Lookup("resume").NoOptDefVal = resumeFromPicker
	addModelFlags(cmd, &flags)
	cmd.Flags().BoolVarP(&printMode, "print", "p", false, "run headless: stream the response to stdout and exit (no TUI)")
	cmd.Flags().BoolVar(&popts.json, "json", false, "with --print, emit a structured JSON transcript instead of streaming text (implies --print; the same as --output json)")
	cmd.Flags().StringVar(&popts.output, "output", "", "with --print, what the run writes: text (the answer as it is written), json (the transcript at the end) or jsonl (one event per line while it runs) (implies --print)")
	cmd.Flags().BoolVar(&popts.yes, "yes", false, "with --print, auto-approve file edits and commands (safety-flagged commands stay denied)")
	cmd.Flags().StringArrayVar(&popts.allow, "allow", nil, "with --print, auto-approve commands matching this prefix (repeatable; extends the config allowlist)")
	cmd.Flags().BoolVar(&popts.sandbox, "sandbox", false, "run approved commands inside a disposable container sandbox; needs a configured digest-pinned image (implies --print)")
	cmd.Flags().BoolVar(&requireSandbox, "require-sandbox", false, "refuse the assistant's commands outright where no containment mechanism is in force, rather than running them unconfined")
	cmd.Flags().IntVar(&popts.maxRounds, "max-rounds", 0, "cap consecutive tool-call rounds per turn (0 removes the cap, for a run left unattended; default: behavior.max_tool_rounds)")

	addDirFlag(cmd, &addDirs)
	addSecretFlag(cmd, &secretFlags)

	cmd.AddCommand(newCodeDoctorCmd())

	return cmd
}

// newCodeDoctorCmd is `shhh code doctor`: the containment slice of `shhh
// doctor`. The command was scoped to process containment and
// container sandboxes before the design system named a `shhh doctor`
// covering the whole setup; that one was promoted and widened, leaving this
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
