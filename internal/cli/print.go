package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mattn/go-isatty"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/safety"
	"github.com/rfizzle/shhh/internal/stdin"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/spf13/cobra"
)

// printOpts are the approval and output flags for headless print mode
// (S-057). The default is maximally safe: every approval-gated tool call is
// denied; --yes and --allow opt in explicitly.
type printOpts struct {
	json  bool
	yes   bool
	allow []string
}

// runPrintSession runs the agent loop to completion without the TUI:
// assistant text streams to stdout, tool activity to stderr, and --json
// replaces the streamed text with a structured transcript on stdout. The
// returned error drives the process exit code.
func runPrintSession(cmd *cobra.Command, args []string, session chatSession, opts printOpts) error {
	env, err := buildSessionEnv(cmd, session)
	if err != nil {
		return err
	}
	cfg := env.cfg

	initialPrompt := ""
	if len(args) > 0 {
		initialPrompt = args[0]
	}
	// Piped stdin is the prompt itself when no argument is given, and extra
	// context for the prompt otherwise (mirroring the chat TUI).
	if !isatty.IsTerminal(os.Stdin.Fd()) && !isatty.IsCygwinTerminal(os.Stdin.Fd()) {
		maxChars := cfg.EffectiveContextMaxTokens() * 4
		content, err := stdin.Read(os.Stdin, maxChars)
		if err != nil {
			return err
		}
		if content != "" {
			if initialPrompt == "" {
				initialPrompt = content
			} else {
				initialPrompt = stdin.FormatPromptWithContext(initialPrompt, content)
			}
		}
	}
	if strings.TrimSpace(initialPrompt) == "" {
		return fmt.Errorf("print mode needs a prompt: pass one as an argument or pipe it on stdin")
	}

	allowlist := append([]string{}, cfg.Behavior.CommandAllowlist...)
	allowlist = append(allowlist, opts.allow...)

	a := agent.New(env.messages, env.stream)
	a.SetExecutor(tools.Execute)
	a.SetMaxRounds(cfg.Behavior.MaxToolRounds)

	var usage provider.Usage
	h := &agent.Headless{
		Agent:   a,
		Gate:    headlessGate,
		Resolve: headlessApprover(cmd.Context(), opts, allowlist, runner.RunCapture),
		OnToolCall: func(tc provider.ToolCall) {
			fmt.Fprintf(os.Stderr, "» %s %s\n", tc.Name, clipActivityLine(tc.Arguments))
		},
		OnToolResult: func(tc provider.ToolCall, result string) {
			if strings.HasPrefix(result, "error:") {
				fmt.Fprintf(os.Stderr, "  ↳ %s\n", clipActivityLine(result))
			}
		},
		OnUsage: func(u *provider.Usage) {
			usage.PromptTokens += u.PromptTokens
			usage.CompletionTokens += u.CompletionTokens
		},
	}
	if !opts.json {
		h.OnText = func(text string) { fmt.Fprint(os.Stdout, text) }
	}

	final, runErr := h.Run(initialPrompt)
	if !opts.json && final != "" && !strings.HasSuffix(final, "\n") {
		fmt.Fprintln(os.Stdout)
	}
	if opts.json {
		if err := writeJSONTranscript(os.Stdout, a.Messages(), final, usage, runErr); err != nil {
			return err
		}
	}
	return runErr
}

// headlessGate mirrors the TUI's requiresApproval: exec and file-modification
// tools go through approval; read-only tools run directly.
func headlessGate(name string) bool {
	return name == tools.ExecCommandName || tools.IsMutating(name)
}

// headlessApprover resolves approval-gated tool calls without a prompt:
// policy decides. Safety-flagged commands are always denied — there is no
// human to confirm them in a headless run.
func headlessApprover(ctx context.Context, opts printOpts, allowlist []string, run func(context.Context, string) (string, int)) func(provider.ToolCall) string {
	return func(tc provider.ToolCall) string {
		if tc.Name == tools.ExecCommandName {
			var args struct {
				Command string `json:"command"`
			}
			if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil || strings.TrimSpace(args.Command) == "" {
				return "error: invalid command arguments"
			}
			if warnings := safety.Check(args.Command); len(warnings) > 0 {
				risks := make([]string, 0, len(warnings))
				for _, w := range warnings {
					risks = append(risks, w.Risk)
				}
				return "error: command denied (" + strings.Join(risks, "; ") + "); safety-flagged commands require interactive approval"
			}
			if opts.yes || agent.AllowlistMatches(allowlist, args.Command) {
				out, code := run(ctx, args.Command)
				return tools.FormatExecResult(out, code)
			}
			return "error: command not approved: headless mode denies commands by default (run with --yes or --allow)"
		}
		if tools.IsMutating(tc.Name) {
			if opts.yes {
				return agent.ExecuteWith(tools.ExecuteMutating, tc)
			}
			return "error: file modification not approved: headless mode denies edits by default (run with --yes)"
		}
		return "error: tool " + tc.Name + " cannot be approved in this session"
	}
}

// clipActivityLine renders text as a single bounded line for stderr activity
// logging.
func clipActivityLine(raw string) string {
	s := strings.Join(strings.Fields(raw), " ")
	if cut, truncated := tools.TruncateOutput(s, 120); truncated {
		return cut + "…"
	}
	return s
}

// jsonTranscript is the --json output: the outcome, usage totals, and the
// full conversation including tool calls and results.
type jsonTranscript struct {
	Success  bool          `json:"success"`
	Error    string        `json:"error,omitempty"`
	Final    string        `json:"final"`
	Usage    jsonUsage     `json:"usage"`
	Messages []jsonMessage `json:"messages"`
}

type jsonUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

type jsonMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
	ToolCalls  []jsonToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type jsonToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func writeJSONTranscript(w io.Writer, msgs []provider.Message, final string, usage provider.Usage, runErr error) error {
	t := jsonTranscript{
		Success:  runErr == nil,
		Final:    final,
		Usage:    jsonUsage{PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens},
		Messages: make([]jsonMessage, 0, len(msgs)),
	}
	if runErr != nil {
		t.Error = runErr.Error()
	}
	for _, m := range msgs {
		jm := jsonMessage{
			Role:       string(m.Role),
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
		}
		for _, tc := range m.ToolCalls {
			jm.ToolCalls = append(jm.ToolCalls, jsonToolCall{ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments})
		}
		t.Messages = append(t.Messages, jm)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(t)
}
