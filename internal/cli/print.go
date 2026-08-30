package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/evidence"
	"github.com/rfizzle/shhh/internal/mcp"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/process"
	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/radius"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/safety"
	"github.com/rfizzle/shhh/internal/scope"
	"github.com/rfizzle/shhh/internal/skill"
	"github.com/rfizzle/shhh/internal/stdin"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/chat"
	"github.com/rfizzle/shhh/internal/web"
	"github.com/spf13/cobra"
)

// printOpts are the approval and output flags for headless print mode
// . The default is maximally safe: every approval-gated tool call is
// denied; --yes and --allow opt in explicitly.
type printOpts struct {
	json    bool
	yes     bool
	allow   []string
	sandbox bool
	// maxRounds overrides behavior.max_tool_rounds for this run, where 0
	// means no cap at all. maxRoundsSet tells the two zeroes apart: the flag
	// left alone (config, then the default) and --max-rounds 0 (uncapped).
	maxRounds    int
	maxRoundsSet bool
}

// rounds is the per-turn tool-round cap for this run: the flag when it was
// given, the config otherwise. Hitting the cap ends a headless run with
// ErrRoundCap, so --max-rounds 0 is the way to say "run until it is done" on
// a foreground run a person can interrupt.
func (o printOpts) rounds(cfg config.Config) int {
	return maxRoundsFor(cfg, o.maxRounds, o.maxRoundsSet)
}

// maxRoundsFor resolves the round cap the same way for a headless run and a
// session, which is the whole point of it: `--max-rounds 0` means the same
// thing on both sides of --print, and only the way out of a runaway differs
// (the exit code there, the interrupt key here).
//
// The zero the flag was left at and the zero it was set to are different
// answers, which is what set distinguishes: unset falls through to the
// config, where zero in turn means "nobody chose" and negative means no cap.
func maxRoundsFor(cfg config.Config, flag int, set bool) int {
	if !set {
		return cfg.Behavior.MaxToolRounds
	}
	if flag == 0 {
		return agent.UnlimitedToolRounds
	}
	return flag
}

// runPrintSession runs the agent loop to completion without the TUI:
// assistant text streams to stdout, tool activity to stderr, and --json
// replaces the streamed text with a structured transcript on stdout. The
// returned error drives the process exit code.
func runPrintSession(cmd *cobra.Command, args []string, session chatSession, opts printOpts) error {
	// The working scope, mirroring the interactive session: the
	// directory the run was started in, plus config's scope_dirs and any
	// --add-dir. Nobody is here to grant a directory mid-run, so what the
	// flags and the config say is the whole scope for the run.
	sc, err := sessionScope(ConfigFrom(cmd.Context()), session.addDirs)
	if err != nil {
		return err
	}

	// Tool-output reduction, mirroring the interactive session: bulky
	// results are reduced with the originals retrievable via the evidence
	// tool.
	red := openEvidence()
	if red != nil {
		session.toolDefs = append(append([]provider.Tool{}, session.toolDefs...), evidence.ToolDefinition())
	}
	// Guarded web tools, mirroring the interactive session; web_fetch
	// stays approval-gated, which headless resolves via --yes.
	if session.web != nil {
		session.toolDefs = append(append([]provider.Tool{}, session.toolDefs...), session.web.Definitions()...)
	}
	// LSP integration, mirroring the interactive session: navigation
	// tools when a server was detected, after-edit diagnostics on approved
	// edits, shutdown with the run.
	if session.lsp != nil {
		session.toolDefs = append(append([]provider.Tool{}, session.toolDefs...), session.lsp.Definitions()...)
		defer session.lsp.Close()
	}
	// Structural code tools, mirroring the interactive session:
	// read-only wrappers, each registered only when its binary is on PATH.
	if session.structural != nil {
		session.toolDefs = append(append([]provider.Tool{}, session.toolDefs...), session.structural.Definitions()...)
	}
	// Quality gate, mirroring the interactive session: auto-run — the
	// model only ever names a suite from the trusted config.
	var qgate *quality.Runner
	if session.gate {
		qgate = openQualityGate(ConfigFrom(cmd.Context()), red, sc)
	}
	if qgate != nil {
		session.toolDefs = append(append([]provider.Tool{}, session.toolDefs...), quality.ToolDefinition())
	}
	// Long-running process supervisor, mirroring the interactive
	// session: start stays approval-gated (resolved via --yes/--allow), and
	// Close terminates every owned process tree when the run ends.
	var procSup *process.Supervisor
	if session.processes {
		procSup = openProcessSupervisor(red)
	}
	if err := session.openSecrets(cmd, procSup); err != nil {
		return err
	}
	if procSup != nil {
		session.toolDefs = append(append([]provider.Tool{}, session.toolDefs...), process.Definition())
		defer procSup.Close()
	}

	// Skills, mirroring the interactive session: the catalog in the
	// prompt, the activation tool in the toolset, both only when something
	// loaded. Activation is a read, so headless needs no approval for it.
	if session.skills.Len() > 0 {
		session.toolDefs = append(append([]provider.Tool{}, session.toolDefs...), skill.ToolDefinition(session.skills))
		session.promptExtra = prompt.CombineExtra(session.promptExtra, skill.PromptBlock(session.skills))
	}

	// The local store is opened here rather than with the recorder below
	// because trust for a project MCP server is read from it.
	db, _ := openStore()
	if db != nil {
		defer db.Close()
	}
	// MCP servers, mirroring the interactive session. A read-only server's
	// tools run; every other server's calls are gated and resolved the way
	// web_fetch is — --yes opts in, the default denies.
	if session.mcp {
		defer session.attachMCP(cmd.Context(), db, false)()
	}

	// The model is told where the work is; a headless run cannot be
	// asked for a directory mid-flight, so knowing the boundary is the
	// difference between a report that names it and a round spent retrying.
	session.promptExtra = prompt.CombineExtra(session.promptExtra, scopePromptBlock(sc))

	// …and what it has to work with, for the same reason: nobody is
	// there to suggest the tool it did not know it had.
	session.promptExtra = prompt.CombineExtra(session.promptExtra, prompt.Toolbox(session.toolDefs))

	// Headless runs bill through the same gate the TUI does; a print run
	// that under-reported would be the harder one to notice, because nobody
	// is watching a rail while it works.
	prices := loadPricing()
	ledger := meter.New(prices)

	env, err := buildSessionEnv(cmd, session, ledger)
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

	// Headless approved commands run contained when a mechanism is available
	// — there is no human watching, so containment matters most here.
	// --sandbox goes further: a disposable container is created for
	// the run and approved commands exec inside it; if the sandbox cannot be
	// created and verified, the run fails instead of downgrading.
	run := runner.RunCapture
	if opts.sandbox {
		srun, cleanup, err := startSandbox(cmd.Context(), cfg, session.vault.Names())
		if err != nil {
			return fmt.Errorf("sandbox: %w", err)
		}
		defer cleanup()
		run = srun
	} else if containment, err := buildContainment(cfg, sc); err != nil {
		return err
	} else if containment.Run != nil {
		run = containment.Run
	}
	run = scrubRunner(session.vault, run)

	a := agent.New(env.messages, env.stream)
	a.SetScrub(session.vault.ScrubMessage)
	if session.skills.Len() > 0 {
		a.KeepResults(skill.IsContent)
	}
	baseExecutor := agent.ToolExecutor(tools.Execute)
	if session.web != nil {
		baseExecutor = session.web.WrapExecutor(tools.Execute)
	}
	if session.lsp != nil {
		baseExecutor = session.lsp.WrapExecutor(baseExecutor)
	}
	if session.structural != nil {
		baseExecutor = session.structural.WrapExecutor(baseExecutor)
	}
	if session.mcpTools != nil {
		baseExecutor = session.mcpTools.WrapExecutor(baseExecutor)
	}
	if qgate != nil {
		baseExecutor = qgate.WrapExecutor(baseExecutor)
	}
	if procSup != nil {
		baseExecutor = procSup.WrapExecutor(baseExecutor)
	}
	if session.skills.Len() > 0 {
		baseExecutor = session.skills.WrapExecutor(baseExecutor)
	}
	executor := baseExecutor
	if red != nil {
		executor = red.WrapExecutor(baseExecutor)
	}
	executor = session.vault.WrapExecutor(executor)
	// Repeat detection. A headless run needs it most: there is nobody
	// watching to notice the same search going round for the third time.
	a.SetExecutor(agent.NewRepeatDetector().WrapExecutor(executor))
	a.SetMaxRounds(opts.rounds(cfg))

	// Session observability: headless runs record the same
	// content-free events as interactive sessions; failure just disables
	// recording. Tool calls are strictly sequential here, so one start
	// timestamp is enough for durations.
	recorder := startObserveRecorder(db, "print", env.prov.Name(), env.modelName, prices)
	defer recorder.end()
	recorder.stamp(env.sysPrompt, session.skills.Len(), projectFingerprintRoot())
	// A headless run is one turn; it closes here with the rounds it took,
	// the same event an interactive turn ends with.
	var runErr error
	runStart := time.Now()
	defer func() {
		outcome := "done"
		if runErr != nil {
			outcome = "failed"
		}
		recorder.turn(1, int64(a.Rounds()), time.Since(runStart), outcome)
	}()

	var usage provider.Usage
	var callStart time.Time
	webTools := session.web
	mcpTools := session.mcpTools
	gate := func(tc provider.ToolCall) bool {
		if webTools != nil && tc.Name == web.FetchToolName {
			return true
		}
		// The process tool gates on its arguments: only start needs
		// approval; status/read/input/stop auto-run.
		if procSup != nil && tc.Name == process.ToolName {
			return process.NeedsApproval(json.RawMessage(tc.Arguments))
		}
		if mcpTools != nil && mcpTools.Has(tc.Name) {
			return !mcpTools.ReadOnly(tc.Name)
		}
		return headlessGate(tc.Name)
	}
	h := &agent.Headless{
		Agent:   a,
		Gate:    gate,
		Resolve: headlessApprover(cmd.Context(), opts, allowlist, run, red, recorder.decision, session.web, procSup, lspMutationHook(session.lsp), sc, session.mcpTools),
		OnToolCall: func(tc provider.ToolCall) {
			callStart = time.Now()
			fmt.Fprintf(os.Stderr, "» %s %s\n", tc.Name, clipActivityLine(tc.Arguments))
		},
		OnToolResult: func(tc provider.ToolCall, result string) {
			outcome := "ok"
			if strings.HasPrefix(result, "error:") {
				outcome = "error"
				fmt.Fprintf(os.Stderr, "  ↳ %s\n", clipActivityLine(result))
			}
			recorder.toolCall(tc.Name, time.Since(callStart), outcome)
		},
		OnUsage: func(*provider.Usage) {
			// What the run has spent is read back from the ledger rather than
			// summed here. The request that just landed was billed at the
			// gate, and so is anything a later feature adds to a headless
			// run without touching this callback.
			t := ledger.Total()
			usage = provider.Usage{
				PromptTokens:     int(t.In),
				CompletionTokens: int(t.Out),
				CachedTokens:     int(t.Cached),
			}
			recorder.usagePriced(1, t.In, t.Out, t.Cost, t.Priced)
		},
	}
	if !opts.json {
		h.OnText = func(text string) { fmt.Fprint(os.Stdout, text) }
	}

	final, err := h.Run(initialPrompt)
	runErr = err
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
// human to confirm them in a headless run. Approved results run through the
// reduction pipeline (red is nil-safe) like every other tool result. Each
// verdict is reported to record (nil-safe) as a content-free decision event.
func headlessApprover(ctx context.Context, opts printOpts, allowlist []string, run func(context.Context, string) (string, int), red *evidence.Reducer, record func(decision, reason string), webTools *web.Toolset, procSup *process.Supervisor, mutationHook chat.MutationHook, sc *scope.Scope, mcpTools *mcp.Toolset) func(provider.ToolCall) string {
	note := func(decision, reason string) {
		if record != nil {
			record(decision, reason)
		}
	}
	return func(tc provider.ToolCall) string {
		// A server call is an external action like a fetch: --yes opts
		// in, the default denies.
		if mcpTools != nil && mcpTools.Has(tc.Name) {
			if opts.yes {
				note("allow", "headless-yes")
				return red.Process(tc.Name, agent.ExecuteWith(mcpTools.Execute, tc))
			}
			note("deny", "headless-default")
			return "error: " + tc.Name + " not approved: headless mode denies external actions by default (run with --yes)"
		}
		// web_fetch is an external action: --yes opts in, the default
		// denies like every other gated call.
		if webTools != nil && tc.Name == web.FetchToolName {
			if opts.yes {
				note("allow", "headless-yes")
				return red.Process(tc.Name, agent.ExecuteWith(webTools.Execute, tc))
			}
			note("deny", "headless-default")
			return "error: web fetch not approved: headless mode denies external actions by default (run with --yes)"
		}
		// A process start is approved like a command: safety-flagged
		// commands are always denied headless; --yes or an allowlist match
		// opts in.
		if procSup != nil && tc.Name == process.ToolName {
			_, command, err := process.StartSummary(json.RawMessage(tc.Arguments))
			if err != nil {
				return "error: " + err.Error()
			}
			if warnings := safety.Check(command); len(warnings) > 0 {
				risks := make([]string, 0, len(warnings))
				for _, w := range warnings {
					risks = append(risks, w.Risk)
				}
				note("deny", "safety")
				return "error: process start denied (" + strings.Join(risks, "; ") + "); safety-flagged commands require interactive approval"
			}
			if opts.yes || agent.AllowlistMatches(allowlist, command) {
				// A process start is a command, and the working scope
				// applies to it as much as to a foreground one.
				if deny, ok := headlessScopeCheck(sc, opts.yes, radius.WritePaths(command)); !ok {
					note("deny", "out-of-scope")
					return deny
				}
				if opts.yes {
					note("allow", "headless-yes")
				} else {
					note("allow", "allowlist")
				}
				exec := func(_ string, args json.RawMessage) (string, error) { return procSup.Execute(args) }
				return red.Process(tc.Name, agent.ExecuteWith(exec, tc))
			}
			note("deny", "headless-default")
			return "error: process start not approved: headless mode denies commands by default (run with --yes or --allow)"
		}
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
				note("deny", "safety")
				return "error: command denied (" + strings.Join(risks, "; ") + "); safety-flagged commands require interactive approval"
			}
			if opts.yes || agent.AllowlistMatches(allowlist, args.Command) {
				// The working scope is checked before the grant is
				// spent: an allowlisted command shape is not a licence to
				// write outside the directories this run was given.
				if deny, ok := headlessScopeCheck(sc, opts.yes, radius.WritePaths(args.Command)); !ok {
					note("deny", "out-of-scope")
					return deny
				}
				if opts.yes {
					note("allow", "headless-yes")
				} else {
					note("allow", "allowlist")
				}
				out, code := run(ctx, args.Command)
				return tools.FormatExecResult(red.Process(tools.ExecCommandName, out), code)
			}
			note("deny", "headless-default")
			return "error: command not approved: headless mode denies commands by default (run with --yes or --allow)"
		}
		if tools.IsMutating(tc.Name) {
			if opts.yes {
				if mut, err := tools.PreviewMutation(tc.Name, json.RawMessage(tc.Arguments)); err == nil {
					if deny, ok := headlessScopeCheck(sc, opts.yes, []string{mut.Path}); !ok {
						note("deny", "out-of-scope")
						return deny
					}
				}
				note("allow", "headless-yes")
				result := agent.ExecuteWith(tools.ExecuteMutating, tc)
				if mutationHook != nil {
					result = mutationHook(tc.Name, json.RawMessage(tc.Arguments), result)
				}
				return red.Process(tc.Name, result)
			}
			note("deny", "headless-default")
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
