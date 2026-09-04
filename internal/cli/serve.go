package cli

// `shhh serve` is the agent behind a protocol rather than behind a screen.
// Everything a session does is assembled exactly as an unattended run's is —
// the same toolset, the same containment, the same hooks at the same seams,
// the same event stream — and the only thing that differs is who answers an
// approval: a client on the other end of a socket rather than a flag decided
// before the run started.
//
// It exists because the loop is passive and has always been drivable by
// something that is not this program's terminal
// (docs/architecture.md#one-agent-several-front-ends); until now nothing but
// this program could reach it.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/hook"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/resolve"
	"github.com/rfizzle/shhh/internal/rpc"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/skill"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/structural"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/spf13/cobra"
)

// serveOpts is what a served session is configured with. There is no --yes
// and no auto-approval of any kind on this command, deliberately: the whole
// point of the surface is that there is somebody to ask, and a flag that
// answered in advance would be the client pre-approving a tier through the
// back door.
// See docs/capabilities/headless.md#a-client-answers-one-call-at-a-time.
type serveOpts struct {
	socket string
	flags  resolve.Opts

	addDirs        []string
	secretFlags    []string
	requireSandbox bool
	maxRounds      int
	maxRoundsSet   bool
}

// newServeCmd is the protocol entry point: the coding agent's loop with a
// JSON-RPC surface in front of it instead of a terminal.
func newServeCmd() *cobra.Command {
	var opts serveOpts
	var stdio bool

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the coding agent over JSON-RPC for another client to drive",
		Long: "Speak JSON-RPC over stdio or a unix socket so a client that is not shhh's own terminal " +
			"can open a session, run a turn, answer its approvals and read the same events " +
			"`shhh code -p --output jsonl` prints. One JSON object per line, in both directions.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts.maxRoundsSet = cmd.Flags().Changed("max-rounds")
			if opts.maxRoundsSet && opts.maxRounds < 0 {
				return fmt.Errorf("--max-rounds cannot be negative (0 removes the cap)")
			}
			if stdio && opts.socket != "" {
				return fmt.Errorf("--stdio speaks to the process that started this one and --socket listens for clients: pass one of them")
			}
			return runServe(cmd, opts)
		},
	}

	cmd.Flags().BoolVar(&stdio, "stdio", false, "speak the protocol on stdin and stdout (the default when --socket is not given)")
	cmd.Flags().StringVar(&opts.socket, "socket", "", "listen for clients on this unix socket instead of stdio")
	addModelFlags(cmd, &opts.flags)
	cmd.Flags().BoolVar(&opts.requireSandbox, "require-sandbox", false, "refuse the assistant's commands outright where no containment mechanism is in force, rather than running them unconfined")
	cmd.Flags().IntVar(&opts.maxRounds, "max-rounds", 0, "cap consecutive tool-call rounds per turn (0 removes the cap; default: behavior.max_tool_rounds)")
	addDirFlag(cmd, &opts.addDirs)
	addSecretFlag(cmd, &opts.secretFlags)

	return cmd
}

// runServe opens the store every session on this server shares and then
// speaks the protocol on whichever transport was asked for.
//
// One store and not one per session: the file takes exactly one connection by
// design (docs/architecture.md#state-is-local-single-connection-and-boring),
// and a server holding several would trade that for a class of lock
// contention nobody can reproduce.
func runServe(cmd *cobra.Command, opts serveOpts) error {
	db, _ := openStore()
	if db != nil {
		defer db.Close()
	}
	srv := rpc.NewServer(func(ctx context.Context, p rpc.StartParams, seams rpc.Seams) (rpc.Loop, error) {
		return openServeLoop(cmd, opts, db, p, seams, nil)
	})
	defer srv.Close()

	if opts.socket == "" {
		return srv.ServeConn(cmd.Context(), os.Stdin, os.Stdout)
	}
	return serveOnSocket(cmd.Context(), srv, opts.socket)
}

// serveOnSocket listens on a unix socket and serves it until the context ends.
//
// A socket that is already there is refused rather than replaced. It is
// either a server that is still running — whose clients would silently stop
// being served — or the remains of one that died, and only the person who
// knows which can say. Unlinking on their behalf is how the first case
// becomes the second.
func serveOnSocket(ctx context.Context, srv *rpc.Server, path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists: another server may be listening on it; remove it if not", path)
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(path) }()
	// Anyone who can open this socket can run commands as this user in this
	// checkout, so nobody else may open it. The mode is set after the listen
	// because there is no way to ask for one before it, which leaves a
	// window; the directory above is created private for the same reason,
	// and is what closes it for a socket the caller puts somewhere new.
	if err := os.Chmod(path, 0o600); err != nil {
		_ = l.Close()
		return err
	}
	return srv.Serve(ctx, l)
}

// serveLoop is one session's agent and everything assembled around it. It is
// the unattended run's assembly with the two seams the protocol supplies
// wired into it, so a client driving a turn gets the run's own behaviour and
// not an imitation of it.
type serveLoop struct {
	agent    *agent.Agent
	headless *agent.Headless
	saved    *headlessChat
	recorder *observeRecorder
	ledger   *meter.Ledger
	events   *jsonlStream
	obs      headlessObserver
	// gate is the quality suite this turn closes on, or nil where the
	// workspace names none, and newCloseGate is what opens one. It is
	// replaced at each turn because its hand-back budget and its verdict are
	// the turn's, not the session's.
	gate         *headlessCloseGate
	newCloseGate func() *headlessCloseGate
	// summarizer takes the periodic readings of a turn. The run over it is
	// built per turn because what a reading is judged against is that turn's
	// instruction, and a session has one per turn.
	summarizer *agent.Summarizer
	// verdict is the policy's last answer within the current turn, for the
	// same reason: a denial the turn before ended on says nothing about this
	// one. The approver reports through whichever is loaded when it fires.
	verdict atomic.Pointer[lastVerdict]
	// fork opens a second loop over a copy of this one's conversation. It is
	// a closure rather than a method because forking is re-running the whole
	// assembly, and only the command that built this one holds what that
	// takes.
	fork func(rpc.Seams, []provider.Message) (rpc.Loop, error)
	// closers end what the assembly opened, in the order they were given.
	closers []func()

	mu sync.Mutex
	// steering is what a client has said to a running turn and the loop has
	// not read yet. It is drained at the round boundary the way a session's
	// typed steering is.
	steering []string
	// turn is which turn is running, and conversation the message list as
	// the last one left it. Both are read from the connection's goroutine
	// while the turn's own is writing, which is why they are behind the lock
	// and the message list is copied rather than shared.
	turn         int64
	conversation []provider.Message
	transcript   json.RawMessage
	usage        provider.Usage
}

// openServeLoop assembles one session. initial, when set, is the conversation
// the session begins from — a fork's copy of its parent's — and takes the
// place of both the fresh system prompt and anything the start parameters
// asked to resume.
func openServeLoop(cmd *cobra.Command, opts serveOpts, db *storage.DB, p rpc.StartParams, seams rpc.Seams, initial []provider.Message) (rpc.Loop, error) {
	l := &serveLoop{}
	// Anything opened before the assembly finishes has to be closed if it
	// does not, which is what this defer is: on the way out with an error,
	// nothing is left running with nobody holding it.
	ok := false
	defer func() {
		if !ok {
			l.release()
		}
	}()

	cfg := ConfigFrom(cmd.Context())
	// Each session resolves its own provider and model, so the flags cannot
	// be the command's own value: buildSessionEnv writes the resolution back
	// through the pointer, and one shared between sessions would leave the
	// second reading the first's answer.
	flags := opts.flags
	session := chatSession{
		title:          "shhh serve",
		kind:           "code",
		buildPrompt:    prompt.BuildAgent,
		toolDefs:       tools.DefinitionsFull(),
		flags:          &flags,
		continueLast:   p.Continue,
		resumeName:     p.Resume,
		web:            openWebTools(cfg),
		lsp:            openLSP(cfg),
		structural:     structural.Detect(),
		gate:           true,
		processes:      true,
		maxRounds:      opts.maxRounds,
		maxRoundsSet:   opts.maxRoundsSet,
		addDirs:        opts.addDirs,
		skills:         loadSkills(),
		secretFlags:    opts.secretFlags,
		mcp:            true,
		requireSandbox: opts.requireSandbox,
	}

	sc, err := sessionScope(cfg, session.addDirs)
	if err != nil {
		return nil, err
	}
	ts, err := buildToolset(cmd, &session, "serve", toolsetOpts{scope: sc})
	if err != nil {
		return nil, err
	}
	l.closers = append(l.closers, ts.close)
	red, qgate, procSup := ts.evidence, ts.gate, ts.proc

	session.sibling = readSibling(db)
	if session.mcp {
		l.closers = append(l.closers, session.attachMCP(cmd.Context(), db, false))
	}
	registerSkills(&session)
	session.promptExtra = prompt.CombineExtra(session.promptExtra, scopePromptBlock(sc))
	session.promptExtra = prompt.CombineExtra(session.promptExtra, prompt.Toolbox(session.toolDefs))

	prices := loadPricing()
	l.ledger = meter.New(prices)
	env, err := buildSessionEnv(cmd, session, l.ledger)
	if err != nil {
		return nil, err
	}
	cfg = env.cfg

	// The containment, and then what it contains. A served session is a
	// long-lived process and has no disposable container of its own: a
	// sandbox is created for one run and torn down with it, which is not what
	// a session that outlives every one of its turns is.
	run := runner.RunCapture
	sandboxProfile := ""
	containment, err := buildContainment(cfg, sc, procSup)
	if err != nil {
		return nil, err
	}
	if containment.Run != nil {
		run = containment.Run
		sandboxProfile = containment.Profile
	}
	run = scrubRunner(session.vault, run)
	// Nobody is at a keyboard to cancel a command that will not finish, which
	// is the same reason an unattended run bounds one.
	run = boundedRunner(run, cfg.CommandTimeout())

	hookCwd, _ := os.Getwd()
	hooked := hookSet(cfg)
	for _, note := range hookNotes(hooked) {
		fmt.Fprintf(os.Stderr, "» hooks: %s\n", note)
	}
	hooks := buildHooks(cfg, hooked, containment.Wrap, hookCwd)
	hookStart := hooks.SessionStart(cmd.Context())
	hookNoteLine(hookStart)
	if hookStart.Context != "" {
		env.sysPrompt = prompt.CombineExtra(env.sysPrompt, hookStart.Context)
		if len(env.messages) > 0 && env.messages[0].Role == provider.RoleSystem {
			env.messages[0].Content = env.sysPrompt
		}
	}

	messages := env.messages
	if initial != nil {
		// A fork carries its parent's conversation, and the parent's system
		// prompt with it: the two were built together and a fresh one over an
		// old conversation would describe a different session.
		messages = initial
		session.continueLast, session.resumeName = false, ""
	}
	saved, messages, err := openHeadlessChat(db, session, messages, env.sysPrompt)
	if err != nil {
		return nil, err
	}
	l.saved = saved
	l.closers = append(l.closers, func() {
		if db != nil && saved != nil {
			_ = db.ReleaseChatSlot(saved.slot)
		}
	})

	a := agent.New(messages, env.stream)
	a.SetSteering(steering(cfg, env.prompts))
	a.SetScrub(session.vault.ScrubMessage)
	if session.skills.Len() > 0 {
		a.KeepResults(skill.IsContent)
	}
	a.SetMaxRounds(maxRoundsFor(cfg, opts.maxRounds, opts.maxRoundsSet))
	l.agent = a

	l.recorder = startObserveRecorder(db, "serve", env.prov.Name(), env.modelName, prices)
	l.closers = append(l.closers, l.recorder.end)
	hooks.SetSession(hookSession(l.recorder.sessionID()))
	// No mode and no classifier, the same as an unattended run: approvals here
	// are answered by a client one call at a time, and neither of those two
	// settings is what decided them.
	l.recorder.stamp(env.prompts.fingerprintOf(env.sysPrompt), session.skills.Len(), projectFingerprintRoot(),
		sessionSettings(cfg, runSettings{
			effort:  env.effort,
			rounds:  roundCapFor(maxRoundsFor(cfg, opts.maxRounds, opts.maxRoundsSet)),
			sandbox: sandboxProfile,
			model:   auxiliaryModel(env.provName, env.modelName),
			summary: cfg.HeadlessSummaryEnabled(),
		}))
	recordGateVerdicts(qgate, l.recorder)

	// The events a client reads are the run's own, written by the same
	// encoder to a writer that hands each finished line to the protocol
	// instead of to stdout.
	l.events = newJSONLStream(&eventLines{emit: seams.Emit})
	l.obs = headlessObserver{rec: l.recorder, rounds: a.Rounds, turn: l.turnNow, stream: l.events}
	l.verdict.Store(&lastVerdict{})
	record := func(decision, reason string) { l.verdict.Load().wrap(l.obs.decision)(decision, reason) }

	// The same line between the tier that runs on its own and the tier that
	// has to be answered for that a scripted run draws (approvals.go).
	gate := unattendedGate(session.web, procSup, session.mcpTools)
	a.SetExecutor(agent.ToolExecutor(hooks.WrapExecutor(l.hookPos,
		func(name string, args json.RawMessage) bool {
			return gate(provider.ToolCall{Name: name, Arguments: string(args)})
		},
		hook.Executor(agent.NewRepeatDetector().WrapExecutor(ts.executor(session))))))

	own := &writtenByCalls{}
	// The unattended run's approver, opted in, is what a call the client
	// allowed is run through — so the deny list, the containment refusal, the
	// safety table and the working scope answer it exactly as they answer a
	// `--yes` run. An answer is a decision, and a decision cannot outrank a
	// standing refusal, which is why the answer chooses an approver rather
	// than replacing one.
	allowed := headlessApprover(cmd.Context(), printOpts{yes: true}, cfg.Behavior.CommandAllowlist,
		cfg.Behavior.CommandDenylist, run, containment.Refusal, red, answeredByClient(record),
		session.web, procSup, chainMutation(lspMutationHook(session.lsp), hookPostMutation(hooks)), sc, session.mcpTools)
	resolveCall := func(tc provider.ToolCall) string {
		if seams.Ask(rpc.Call{Tool: tc.Name, Arguments: tc.Arguments, Turn: l.turnNow(), Round: int64(a.Rounds())}) {
			return allowed(tc)
		}
		// Nothing behind a refusal can turn it into a yes, so it is stated
		// here rather than handed to an approver that would still consult the
		// allowlist and run the call the client had just declined.
		record(observe.DecisionDeny, observe.ReasonUser)
		return declinedByClient(tc)
	}
	resolveCall = own.wrap(resolveCall)
	resolveCall = hookApprover(hooks, l.hookPos, record, resolveCall)
	if c := headlessTree(cfg, session.sibling, own); c != nil {
		a.SetTreeCheck(*c)
	}

	l.headless = &agent.Headless{
		Agent:        a,
		Compact:      headlessCompactor(cmd.Context(), cfg, env, l.ledger, prices, session.toolDefs),
		Gate:         gate,
		Resolve:      resolveCall,
		Steer:        l.drainSteering,
		OnText:       l.obs.text,
		OnToolCall:   l.obs.call,
		OnIntervene:  l.obs.intervene,
		OnSummary:    l.obs.summary,
		OnCompact:    l.obs.compact,
		OnTree:       l.obs.tree,
		OnRetry:      l.obs.retry,
		OnToolResult: l.obs.toolResult,
		OnUsage: func(*provider.Usage) {
			// Read back off the ledger rather than summed here, so anything
			// billed at the gate that this callback never hears about is
			// still in the figure a client reads.
			t := l.ledger.Total()
			u := provider.Usage{
				PromptTokens:     int(t.In),
				CompletionTokens: int(t.Out),
				CachedTokens:     int(t.Cached),
			}
			l.mu.Lock()
			l.usage = u
			l.mu.Unlock()
			l.recorder.usagePriced(l.turnNow(), t.In, t.Out, t.Cost, t.Priced)
			l.obs.usage(u)
		},
	}
	l.headless.SetRetryLimit(cfg.Behavior.ProviderRetries)
	l.summarizer = newSummarizer(cfg, env, l.ledger, cfg.HeadlessSummaryEnabled())
	if suite, retries, ok := onCloseGate(qgate); ok {
		l.newCloseGate = func() *headlessCloseGate {
			return &headlessCloseGate{ctx: cmd.Context(), gate: qgate, suite: suite, retries: retries, written: own.paths}
		}
	}
	l.fork = func(s rpc.Seams, msgs []provider.Message) (rpc.Loop, error) {
		return openServeLoop(cmd, opts, db, rpc.StartParams{}, s, msgs)
	}
	l.snapshot()

	ok = true
	return l, nil
}

// answeredByClient rewrites the one verdict the unattended approver spells
// for itself. Everything else it reports — the deny list, the safety table,
// the scope — is the same fact whoever is driving and goes through untouched.
// Its opt-in is not: nothing here was decided by a flag before the run
// started, the call was put to whoever is driving, and the record has a word
// for a call answered that way.
func answeredByClient(next func(decision, reason string)) func(string, string) {
	return func(decision, reason string) {
		if reason == observe.ReasonHeadlessYes {
			reason = observe.ReasonUser
		}
		next(decision, reason)
	}
}

// errServeRefused is a refused turn stated in words for a client, on the
// close line and under the same status an unattended run would exit with.
//
// It is the unattended run's fact without the unattended run's advice: that
// sentence says to re-run with --yes or --allow, and neither flag exists on
// this command. What to do about a refusal the client itself made is the
// client's, and one it did not make came from the deny list, the safety table
// or an uncontained host, none of which is meant to be worked around.
var errServeRefused = errors.New("a tool call was refused and the turn ended on that refusal")

// declinedByClient is the tool result a call the client refused is recorded
// as, in the words every other surface declines one in.
func declinedByClient(tc provider.ToolCall) string {
	if tc.Name == tools.ExecCommandName {
		return "error: the user declined to run this command"
	}
	return "error: the user declined this tool call"
}

// eventLines turns the bytes the run's event stream writes back into the
// events a client is sent. The stream writes one JSON object per line and
// this reads exactly that, so what reaches a client over the protocol is the
// line `--output jsonl` would have printed rather than a second encoding of
// the same event that could disagree with it.
// See docs/capabilities/headless.md#the-stream-is-the-record-as-it-happens.
type eventLines struct {
	emit func(json.RawMessage)
	rest []byte
}

func (e *eventLines) Write(p []byte) (int, error) {
	e.rest = append(e.rest, p...)
	for {
		i := bytes.IndexByte(e.rest, '\n')
		if i < 0 {
			return len(p), nil
		}
		line := bytes.TrimSpace(e.rest[:i])
		e.rest = append([]byte(nil), e.rest[i+1:]...)
		if len(line) > 0 {
			e.emit(append(json.RawMessage(nil), line...))
		}
	}
}

// hookPos is where this session is now, for the hooks at the tool seams. It
// is the run's own reading with the turn filled in, because a served session
// has as many turns as a client asks for.
func (l *serveLoop) hookPos() hook.Pos {
	return hook.Pos{Turn: l.turnNow(), Round: int64(l.agent.Rounds())}
}

func (l *serveLoop) turnNow() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.turn
}

// Steer queues text for the running turn.
func (l *serveLoop) Steer(text string) {
	l.mu.Lock()
	l.steering = append(l.steering, text)
	l.mu.Unlock()
}

// drainSteering is the loop's half of that: everything said since the last
// round boundary, taken in one go.
func (l *serveLoop) drainSteering() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	queued := l.steering
	l.steering = nil
	return queued
}

// Interrupt stops the running turn at its next checkpoint.
func (l *serveLoop) Interrupt() { l.headless.Interrupt() }

// Transcript is the conversation as the last turn left it.
func (l *serveLoop) Transcript() json.RawMessage {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.transcript
}

// Fork opens a session over a copy of this conversation.
func (l *serveLoop) Fork(s rpc.Seams) (rpc.Loop, error) {
	l.mu.Lock()
	msgs := append([]provider.Message(nil), l.conversation...)
	l.mu.Unlock()
	return l.fork(s, msgs)
}

// Run carries out one turn and reports it the way an unattended run reports
// its own: the turn's outcome to the record, the conversation to the slot a
// `--continue` will find it in, and a close line on the stream carrying the
// outcome, the answer and the status the same turn would have exited with.
//
// The code is on the line even though nothing here is about to exit. It is
// the projection of the outcome the record kept
// (docs/capabilities/headless.md#the-exit-code-is-the-contract), and a client
// reading the stream should not have to learn a second way to ask how a turn
// went depending on which surface produced it.
func (l *serveLoop) Run(turn int64, prompt string) (string, error) {
	l.mu.Lock()
	l.turn = turn
	l.steering = nil
	l.mu.Unlock()
	// This turn's own readings. A hand-back budget and a standing refusal
	// both belong to the turn that produced them; carried over, the first
	// would be spent before the turn started and the second would report a
	// turn as refused for something the turn before it was told.
	l.verdict.Store(&lastVerdict{})
	l.gate = nil
	if l.newCloseGate != nil {
		l.gate = l.newCloseGate()
		l.headless.OnClose = l.gate.close
	}
	// The readings this turn takes are judged against this turn's
	// instruction: a session's second question is not a drift from its first.
	l.headless.Summary = agent.NewSummaryRun(l.summarizer, agent.NewRecorder(0), prompt)

	started := time.Now()
	final, runErr := l.headless.Run(prompt)
	outcome := headlessTurnOutcome(runErr)
	l.recorder.turn(turn, int64(l.agent.Rounds()), time.Since(started), outcome)
	l.saved.save(l.agent.Messages())
	l.recorder.link(l.saved.slot)
	l.snapshot()

	gateErr := l.gate.err()
	refused := l.verdict.Load().refused()
	out := runErr
	switch {
	case out != nil:
	case gateErr != nil:
		out = gateErr
	case refused:
		out = errServeRefused
	}
	l.mu.Lock()
	usage := l.usage
	l.mu.Unlock()
	l.events.closed(l.obs.pos(), outcome, headlessExitCode(outcome, gateErr != nil, refused), final, usage, out)
	return final, out
}

// snapshot takes the conversation as it stands, which is what a client
// attaching later is shown and what a fork begins from. It is taken between
// turns and never during one: the message list belongs to the goroutine
// running the turn, and a reader crossing it mid-round would be reading a
// conversation that is missing the results it is about to owe.
func (l *serveLoop) snapshot() {
	msgs := append([]provider.Message(nil), l.agent.Messages()...)
	encoded, err := json.Marshal(jsonMessages(msgs))
	l.mu.Lock()
	defer l.mu.Unlock()
	l.conversation = msgs
	if err == nil {
		l.transcript = encoded
	}
}

// Close releases what the assembly opened.
func (l *serveLoop) Close() error {
	l.release()
	return nil
}

// release runs the closers in the reverse of the order they were added, the
// way the toolset's own do: what was opened last was opened over what came
// before it.
func (l *serveLoop) release() {
	for i := len(l.closers) - 1; i >= 0; i-- {
		l.closers[i]()
	}
	l.closers = nil
}
