package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/evidence"
	"github.com/rfizzle/shhh/internal/mcp"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/process"
	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/radius"
	"github.com/rfizzle/shhh/internal/reports"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/safety"
	"github.com/rfizzle/shhh/internal/sandbox"
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

// headlessObserver is the headless run's adaptation to the observer contract
// in internal/observe, the way internal/ui/chat/observe.go is the chat
// model's. The codes live there because every surface reports the same ones;
// what lives here is where a headless run's own accounting — its single
// turn, the round the loop has reached — is read off to fill them in.
//
// A run recorded without a position cannot be told apart from one that
// circled for forty rounds, which is the shape the record exists to show.
// See docs/capabilities/sessions-and-memory.md#every-composition-is-one-population.
type headlessObserver struct {
	rec *observeRecorder
	// rounds is the tool round the loop has reached. It is a function rather
	// than the agent itself because that is the whole of what a position
	// needs from it.
	rounds func() int
}

// pos is where the run is now. A headless run is one turn by construction —
// one prompt in, one answer out — so the turn is always 1 rather than a
// counter nothing would increment.
func (h headlessObserver) pos() observe.Pos {
	return observe.Pos{Turn: 1, Round: int64(h.rounds())}
}

// toolResult records one executed call from its result text: the outcome
// and, for a failure, its class — and the repeat detector's notice where the
// result carries one, since being told it is circling is a thing that
// happened to the run and not a property of the call.
func (h headlessObserver) toolResult(tool string, duration time.Duration, result string) {
	outcome, class := observe.ToolOutcome(result)
	at := h.pos()
	h.rec.toolCallAt(at, tool, duration, outcome, class)
	if agent.IsRepeatNotice(result) {
		h.rec.signal(at, observe.SignalRepeat, tool)
	}
}

// decision records what the approver resolved a gated call to. A headless
// run resolves every one of them from policy rather than from a person, so
// this is the only place its approval rate can come from.
func (h headlessObserver) decision(decision, reason string) {
	h.rec.decisionAt(h.pos(), decision, reason)
}

// summary records a reading. Every reading lands here and not only the ones
// that go on to interrupt the turn: a drift rate is a fraction, and this is
// its denominator.
func (h headlessObserver) summary(v agent.SummaryVerdict) {
	h.rec.signal(h.pos(), observe.SignalSummary, observe.SummaryCode(v.State))
}

// intervene records the run interrupting its own turn to ask it to take
// stock.
func (h headlessObserver) intervene(iv agent.Intervention) {
	h.rec.signal(h.pos(), observe.SignalIntervene, iv.Kind.Signal())
}

// tree records the run being told the tree moved under it.
func (h headlessObserver) tree(n agent.TreeNotice) {
	h.rec.signal(h.pos(), observe.SignalTree, n.Signal())
}

// retry records one wait the run sat out after a request the provider never
// answered. It is recorded per attempt, so a population of unattended runs
// can be asked how much of its wall clock was a provider's and not its own.
func (h headlessObserver) retry(n agent.RetryNotice) {
	h.rec.signal(h.pos(), observe.SignalRetry, n.Signal())
}

// writtenByCalls is the paths a headless run's mutating calls wrote: the
// subtrahend the tree reading needs, where a session would hand in its
// changeset. A call that came back as an error wrote nothing.
type writtenByCalls struct {
	mu   sync.Mutex
	list []string
}

func (w *writtenByCalls) wrap(resolve func(provider.ToolCall) string) func(provider.ToolCall) string {
	return func(tc provider.ToolCall) string {
		result := resolve(tc)
		w.note(tc, result)
		return result
	}
}

func (w *writtenByCalls) note(tc provider.ToolCall, result string) {
	if !tools.IsMutating(tc.Name) || strings.HasPrefix(result, "error:") {
		return
	}
	var args struct {
		Path string `json:"path"`
	}
	if json.Unmarshal([]byte(tc.Arguments), &args) != nil || args.Path == "" {
		return
	}
	w.mu.Lock()
	w.list = append(w.list, args.Path)
	w.mu.Unlock()
}

func (w *writtenByCalls) paths() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.list...)
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
	if err := session.openSecrets(cmd, red, procSup); err != nil {
		return err
	}
	if procSup != nil {
		session.toolDefs = append(append([]provider.Tool{}, session.toolDefs...), process.Definition())
		defer procSup.Close()
	}
	// Report pages, mirroring the interactive session — except that a
	// headless run never pops a browser: nobody is guaranteed to be at the
	// desktop, and the URL reaches the transcript either way.
	pub := openReportsPublisher(ConfigFrom(cmd.Context()), "print", false)
	if pub != nil {
		session.toolDefs = append(append([]provider.Tool{}, session.toolDefs...), reports.ToolDefinition())
		defer pub.Close()
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
	// sandboxProfile is the profile the run's commands are actually under,
	// for the record: empty when nothing contains them.
	sandboxProfile := ""
	if opts.sandbox {
		srun, cleanup, err := startSandbox(cmd.Context(), cfg, session.vault.Names())
		if err != nil {
			return fmt.Errorf("sandbox: %w", err)
		}
		defer cleanup()
		run = srun
		// The container took the same profile the spec parsed; a name the
		// parser refused could not have started it.
		if profile, err := sandbox.ParseProfile(cfg.Sandbox.Profile); err == nil {
			sandboxProfile = string(profile)
		}
		if procSup != nil {
			// Approved commands exec inside the disposable container and a
			// started process cannot follow them in: what the supervisor
			// would hold is the exec client, not the process, so stopping
			// it would leave something running in a container nothing is
			// watching. Starting on the host instead would put the one
			// thing that keeps running outside the strongest containment
			// this run has, so a start is refused and says why.
			// See docs/capabilities/containment.md#a-started-process-is-contained-too.
			procSup.SetContainment(process.Containment{
				Mechanism: "container sandbox",
				Wrap: func(string, []string) ([]string, error) {
					return nil, fmt.Errorf("a long-running process cannot be started inside this run's disposable container; use execute_command, or run without --sandbox")
				},
			})
		}
	} else {
		containment, err := buildContainment(cfg, sc, procSup)
		if err != nil {
			return err
		}
		if containment.Run != nil {
			run = containment.Run
			sandboxProfile = containment.Profile
		}
	}
	run = scrubRunner(session.vault, run)
	// The ceiling matters most here. A session has a reader who can cancel a
	// command that is never going to finish; a headless run has nobody, and
	// the executor it is holding is held until something outside kills it.
	run = boundedRunner(run, cfg.CommandTimeout())

	a := agent.New(env.messages, env.stream)
	a.SetSteering(steering(cfg, env.prompts))
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
	if pub != nil {
		baseExecutor = pub.WrapExecutor(baseExecutor)
	}
	if session.skills.Len() > 0 {
		baseExecutor = session.skills.WrapExecutor(baseExecutor)
	}
	executor := baseExecutor
	if red != nil {
		executor = red.WrapExecutor(baseExecutor)
	}
	// The reducer scrubs before it stores; this wrap is the second door on
	// what the model reads, not the one that keeps the store clean.
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
	// No mode and no classifier: a headless run answers approvals with
	// --yes and --allow, and the record says so by leaving both empty
	// rather than borrowing the mode a session would have started in.
	recorder.stamp(env.prompts.fingerprintOf(env.sysPrompt), session.skills.Len(), projectFingerprintRoot(), sessionSettings(cfg, runSettings{
		effort:  env.effort,
		rounds:  roundCapFor(opts.rounds(cfg)),
		sandbox: sandboxProfile,
		model:   auxiliaryModel(env.provName, env.modelName),
		summary: cfg.HeadlessSummaryEnabled(),
	}))
	// The gate's verdict, mirroring the interactive session — and a run
	// with nobody in front of it is the one whose verdict the record most
	// needs, because there was no one there to read it on the way past.
	recordGateVerdicts(qgate, recorder)
	obs := headlessObserver{rec: recorder, rounds: a.Rounds}
	// A headless run is one turn; it closes here with the rounds it took,
	// the same event an interactive turn ends with.
	var runErr error
	runStart := time.Now()
	defer func() {
		recorder.turn(1, int64(a.Rounds()), time.Since(runStart), headlessTurnOutcome(runErr))
	}()

	var usage provider.Usage
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
	// A non-interactive run has nobody to notice it has drifted or that it
	// already has what it needs, which is why readings default on here. The
	// prompt is the instruction every one of them is judged against.
	summaryRun := agent.NewSummaryRun(
		newSummarizer(cfg, env, ledger, cfg.HeadlessSummaryEnabled()),
		agent.NewRecorder(0), initialPrompt)
	resolve := headlessApprover(cmd.Context(), opts, allowlist, run, red, obs.decision,
		session.web, procSup, lspMutationHook(session.lsp), sc, session.mcpTools)
	// A headless run has no changeset, so what it wrote is read off the
	// calls that wrote it.
	if c := treeCheck(cfg); c != nil {
		own := &writtenByCalls{}
		c.Own = own.paths
		resolve = own.wrap(resolve)
		a.SetTreeCheck(*c)
	}
	h := &agent.Headless{
		Agent:   a,
		Summary: summaryRun,
		OnIntervene: func(iv agent.Intervention) {
			fmt.Fprintf(os.Stderr, "» %s\n", iv.Notice)
			obs.intervene(iv)
		},
		OnSummary: obs.summary,
		Gate:      gate,
		Resolve:   resolve,
		OnTree: func(n agent.TreeNotice) {
			fmt.Fprintf(os.Stderr, "» %s\n", n.Notice)
			obs.tree(n)
		},
		OnToolCall: func(tc provider.ToolCall) {
			fmt.Fprintf(os.Stderr, "» %s %s\n", tc.Name, clipActivityLine(tc.Arguments))
		},
		OnToolResult: func(r agent.ToolResult) {
			if outcome, _ := observe.ToolOutcome(r.Result); outcome == observe.OutcomeError {
				// The call is named on the failure line as well as on its own
				// line above, because a round's reads go out together: the
				// line the indent used to point at is no longer the line
				// directly above it.
				fmt.Fprintf(os.Stderr, "  ↳ %s: %s\n", r.Call.Name, clipActivityLine(r.Result))
			}
			obs.toolResult(r.Call.Name, r.Duration, r.Result)
		},
		// The wait goes to stderr beside the run's other activity, because a
		// script that reads stdout for the answer is not the reader this line
		// is for: a run that has gone quiet for a minute is otherwise
		// indistinguishable from one that has hung.
		OnRetry: func(n agent.RetryNotice) {
			if n.Partial != "" && !strings.HasSuffix(n.Partial, "\n") {
				// Half a sentence is already on stdout and the retry asks the
				// whole question again, so it is closed off here rather than
				// left for the reply that replaces it to run on from.
				fmt.Fprintln(os.Stdout)
			}
			fmt.Fprintf(os.Stderr, "» %s\n", n.Notice)
			obs.retry(n)
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
	h.SetRetryLimit(cfg.Behavior.ProviderRetries)
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

// headlessTurnOutcome is how a headless run's single turn ended, in the
// closed set a session's turns end in. The round cap is `cap-paused` and not
// a failure even though it ends this run: the event being counted is a turn
// that stopped at its cap, which is the same event on every surface, and
// spelling it `failed` here would read the whole headless population as
// having no capped turns at all. What differs is only that nobody is here to
// grant more rounds, and that difference is the exit code's to report.
func headlessTurnOutcome(err error) string {
	switch {
	case err == nil:
		return observe.TurnDone
	case errors.Is(err, agent.ErrRoundCap):
		return observe.TurnCapPaused
	case errors.Is(err, agent.ErrInterrupted):
		return observe.TurnCancelled
	}
	return observe.TurnFailed
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
				note(observe.DecisionAllow, observe.ReasonHeadlessYes)
				return red.Process(tc.Name, agent.ExecuteWith(mcpTools.Execute, tc))
			}
			note(observe.DecisionDeny, observe.ReasonHeadlessDefault)
			return "error: " + tc.Name + " not approved: headless mode denies external actions by default (run with --yes)"
		}
		// web_fetch is an external action: --yes opts in, the default
		// denies like every other gated call.
		if webTools != nil && tc.Name == web.FetchToolName {
			if opts.yes {
				note(observe.DecisionAllow, observe.ReasonHeadlessYes)
				return red.Process(tc.Name, agent.ExecuteWith(webTools.Execute, tc))
			}
			note(observe.DecisionDeny, observe.ReasonHeadlessDefault)
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
				note(observe.DecisionDeny, observe.ReasonSafety)
				return "error: process start denied (" + strings.Join(risks, "; ") + "); safety-flagged commands require interactive approval"
			}
			if opts.yes || agent.AllowlistMatches(allowlist, command) {
				// A process start is a command, and the working scope
				// applies to it as much as to a foreground one.
				if deny, ok := headlessScopeCheck(sc, opts.yes, radius.WritePaths(command)); !ok {
					note(observe.DecisionDeny, observe.ReasonOutOfScope)
					return deny
				}
				if opts.yes {
					note(observe.DecisionAllow, observe.ReasonHeadlessYes)
				} else {
					note(observe.DecisionAllow, observe.ReasonAllowlist)
				}
				exec := func(_ string, args json.RawMessage) (string, error) { return procSup.Execute(args) }
				return red.Process(tc.Name, agent.ExecuteWith(exec, tc))
			}
			note(observe.DecisionDeny, observe.ReasonHeadlessDefault)
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
				note(observe.DecisionDeny, observe.ReasonSafety)
				return "error: command denied (" + strings.Join(risks, "; ") + "); safety-flagged commands require interactive approval"
			}
			if opts.yes || agent.AllowlistMatches(allowlist, args.Command) {
				// The working scope is checked before the grant is
				// spent: an allowlisted command shape is not a licence to
				// write outside the directories this run was given.
				if deny, ok := headlessScopeCheck(sc, opts.yes, radius.WritePaths(args.Command)); !ok {
					note(observe.DecisionDeny, observe.ReasonOutOfScope)
					return deny
				}
				if opts.yes {
					note(observe.DecisionAllow, observe.ReasonHeadlessYes)
				} else {
					note(observe.DecisionAllow, observe.ReasonAllowlist)
				}
				out, code := run(ctx, args.Command)
				return tools.FormatExecResult(red.Process(tools.ExecCommandName, out), code)
			}
			note(observe.DecisionDeny, observe.ReasonHeadlessDefault)
			return "error: command not approved: headless mode denies commands by default (run with --yes or --allow)"
		}
		if tools.IsMutating(tc.Name) {
			if opts.yes {
				if mut, err := tools.PreviewMutation(tc.Name, json.RawMessage(tc.Arguments)); err == nil {
					if deny, ok := headlessScopeCheck(sc, opts.yes, []string{mut.Path}); !ok {
						note(observe.DecisionDeny, observe.ReasonOutOfScope)
						return deny
					}
				}
				note(observe.DecisionAllow, observe.ReasonHeadlessYes)
				result := agent.ExecuteWith(tools.ExecuteMutating, tc)
				if mutationHook != nil {
					result = mutationHook(tc.Name, json.RawMessage(tc.Arguments), result)
				}
				return red.Process(tc.Name, result)
			}
			note(observe.DecisionDeny, observe.ReasonHeadlessDefault)
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

// jsonMessage is one message as every JSON transcript emits it: the role and
// the words, and a tool call's name and arguments where there was one.
// Attachments are left out — a transcript for a script is text.
//
// `shhh chats show --json` and `shhh code --print --json` are two views of the
// same conversation, so they are one projection: two structs of the same
// fields built by two loops drift the moment either grows a field.
type jsonMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
	ToolCalls  []jsonToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

// jsonToolCall is one call the assistant made, name and arguments apart so a
// script does not have to split a string. The id is what a tool result refers
// back to, and is always present — a consumer that keys on it should find an
// empty string rather than no field where one was never recorded.
type jsonToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// jsonMessages is the conversation as data.
func jsonMessages(msgs []provider.Message) []jsonMessage {
	out := make([]jsonMessage, 0, len(msgs))
	for _, m := range msgs {
		row := jsonMessage{Role: string(m.Role), Content: m.Content, ToolCallID: m.ToolCallID}
		for _, tc := range m.ToolCalls {
			row.ToolCalls = append(row.ToolCalls, jsonToolCall{ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments})
		}
		out = append(out, row)
	}
	return out
}

func writeJSONTranscript(w io.Writer, msgs []provider.Message, final string, usage provider.Usage, runErr error) error {
	t := jsonTranscript{
		Success:  runErr == nil,
		Final:    final,
		Usage:    jsonUsage{PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens},
		Messages: jsonMessages(msgs),
	}
	if runErr != nil {
		t.Error = runErr.Error()
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(t)
}
