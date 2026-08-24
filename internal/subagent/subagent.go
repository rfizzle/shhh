// Package subagent orchestrates child agents for `shhh code` (S-068): the
// model delegates scoped work to background children via spawn_agent, each
// child is a full internal/agent instance driven by the headless loop, and
// everything consequential a child wants to do — commands, edits, its final
// patch — routes back to the parent session's user for approval. Writer
// children work against an isolated git worktree; their changes only reach
// the real checkout as a user-approved patch.
package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/safety"
	"github.com/rfizzle/shhh/internal/tools"
)

// Role scopes a child's toolset: researchers get read-only tools plus the
// web, writers get the full toolset against an isolated worktree.
type Role string

const (
	RoleResearcher Role = "researcher"
	RoleWriter     Role = "writer"
)

// ParseRole maps a spawn_agent role argument to its Role.
func ParseRole(s string) (Role, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "researcher":
		return RoleResearcher, nil
	case "writer":
		return RoleWriter, nil
	}
	return "", fmt.Errorf("unknown role %q (valid: researcher, writer)", s)
}

// Hard budgets and bounds. Concurrency and per-child budgets are deliberately
// bounded: a runaway parent cannot fan out or spend without limit.
const (
	// DefaultMaxConcurrent children run at once; further spawns queue.
	DefaultMaxConcurrent = 3
	// MaxChildren caps how many children one session may spawn in total.
	MaxChildren = 16
	// DefaultMaxRounds and MaxRoundsCeiling bound a child's tool rounds.
	DefaultMaxRounds = agent.DefaultMaxToolRounds
	MaxRoundsCeiling = 50
	// DefaultMaxTokens and MaxTokensCeiling bound a child's token spend
	// (prompt + completion, provider-reported).
	DefaultMaxTokens  = 200_000
	MaxTokensCeiling  = 1_000_000
	minChildMaxTokens = 1_000
)

// State is a child's lifecycle state.
type State int

const (
	StateQueued State = iota
	StateRunning
	StateBlocked // waiting on the parent user's approval
	StateDone
	StateFailed
)

func (s State) String() string {
	switch s {
	case StateQueued:
		return "queued"
	case StateRunning:
		return "running"
	case StateBlocked:
		return "blocked"
	case StateDone:
		return "done"
	default:
		return "failed"
	}
}

// Status is one child's live snapshot, for progress rows and agent_report.
type Status struct {
	Name      string
	Role      Role
	Task      string
	State     State
	Detail    string
	ToolCalls int
	TokensIn  int64
	TokensOut int64
}

// Env is everything a child needs to run, assembled by the CLI so this
// package stays free of provider and config plumbing.
type Env struct {
	// SystemPrompt is the child's role-specific system prompt.
	SystemPrompt string
	// Stream opens completion streams bound to the child's context and
	// role-scoped tool definitions.
	Stream agent.StreamFunc
	// Executor runs the child's auto-run (non-gated) tools, already rooted at
	// the child's workspace and wrapped in output reduction.
	Executor agent.ToolExecutor
	// ExecuteGated runs approved non-exec gated calls (file mutations,
	// web_fetch); the supervisor roots the arguments before calling it.
	ExecuteGated agent.ToolExecutor
	// RunCommand executes an approved shell command in the child's workspace
	// (contained when a mechanism is available).
	RunCommand func(ctx context.Context, command string) (output string, exitCode int)
	// Gated names the tools that must go through approval routing.
	Gated map[string]bool
}

// EnvFactory builds a child's Env; ctx is the child's context (cancelling it
// must abort the child's streams) and root its working directory.
type EnvFactory func(ctx context.Context, role Role, root string) (Env, error)

// Recorder receives a child's content-free observability events (S-065); any
// callback may be nil.
type Recorder struct {
	Usage    func(turns, tokensIn, tokensOut int64)
	ToolCall func(tool, outcome string)
	End      func()
}

// Options configures a Supervisor.
type Options struct {
	// Root is the parent session's workspace directory.
	Root string
	// NewEnv builds each child's runtime; required.
	NewEnv EnvFactory
	// Record opens a child's observability recorder; nil disables recording.
	Record func(role Role) Recorder
	// CommandAllowlist is the parent's config allowlist, inherited by
	// children (inheriting it keeps the child at most as permissive).
	CommandAllowlist []string
	// MaxConcurrent bounds simultaneously running children; <= 0 uses
	// DefaultMaxConcurrent.
	MaxConcurrent int
}

// EventKind tags a supervisor event.
type EventKind int

const (
	// EventUpdate is a status change (progress rows re-render).
	EventUpdate EventKind = iota
	// EventAsk is an approval request routed to the parent user.
	EventAsk
	// EventDone marks a child finished (done or failed).
	EventDone
)

// Event is one supervisor notification for the parent front-end.
type Event struct {
	Kind   EventKind
	Ask    *Ask
	Status Status
}

// AskKind selects the approval card a routed request renders with.
type AskKind int

const (
	AskCommand AskKind = iota
	AskEdit
	AskGeneric
	AskPatch
)

// Ask is one child approval request routed into the parent's approval flow:
// never silently parked, never auto-denied — the child blocks until the user
// answers (or the child is cancelled).
type Ask struct {
	// Agent is the child's name, shown as the card label.
	Agent    string
	Kind     AskKind
	Title    string
	Command  string      // AskCommand: the command text
	Warnings []string    // AskCommand: safety.Check risks
	Hunks    []diff.Hunk // AskEdit / AskPatch: the change to review
	Summary  string      // AskGeneric: one-line description

	once sync.Once
	resp chan bool
}

// NewAsk builds an approval request; the supervisor uses it internally and
// front-end tests use it to drive the routing surface.
func NewAsk(agentName string, kind AskKind, title string) *Ask {
	return &Ask{Agent: agentName, Kind: kind, Title: title, resp: make(chan bool, 1)}
}

// Respond records the user's decision. Safe to call more than once; only the
// first decision counts.
func (a *Ask) Respond(approved bool) {
	a.once.Do(func() { a.resp <- approved })
}

// Answered non-blockingly consumes the recorded decision, for tests.
func (a *Ask) Answered() (approved, ok bool) {
	select {
	case v := <-a.resp:
		return v, true
	default:
		return false, false
	}
}

// child is one sub-agent: an internal/agent instance plus its runtime and
// live status.
type child struct {
	name      string
	role      Role
	task      string
	root      string // working directory (worktree subdir for writers)
	worktree  string // worktree top dir; "" for researchers
	repoTop   string // parent repo toplevel; "" for researchers
	mode      agent.Mode
	maxTokens int64

	ctx    context.Context
	cancel context.CancelFunc
	agent  *agent.Agent
	env    Env
	rec    Recorder
	done   chan struct{}

	mu        sync.Mutex
	state     State
	detail    string
	toolCalls int
	tokensIn  int64
	tokensOut int64
	budgetHit bool
	report    string
	patchNote string
}

func (c *child) set(state State, detail string) {
	c.mu.Lock()
	c.state = state
	c.detail = detail
	c.mu.Unlock()
}

func (c *child) status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Status{
		Name:      c.name,
		Role:      c.role,
		Task:      c.task,
		State:     c.state,
		Detail:    c.detail,
		ToolCalls: c.toolCalls,
		TokensIn:  c.tokensIn,
		TokensOut: c.tokensOut,
	}
}

// addUsage accumulates provider-reported usage and reports whether the
// child's token budget is now exceeded.
func (c *child) addUsage(u *provider.Usage) (over bool) {
	if u == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tokensIn += int64(u.PromptTokens)
	c.tokensOut += int64(u.CompletionTokens)
	if c.maxTokens > 0 && c.tokensIn+c.tokensOut > c.maxTokens {
		c.budgetHit = true
		return true
	}
	return false
}

// Supervisor owns a session's children: spawning, bounded concurrency,
// approval routing, cancellation, and worktree cleanup.
type Supervisor struct {
	opts   Options
	ctx    context.Context
	cancel context.CancelFunc
	events chan Event
	sem    chan struct{}

	mu         sync.Mutex
	children   []*child
	byName     map[string]*child
	counters   map[Role]int
	parentMode agent.Mode

	wg        sync.WaitGroup
	closeOnce sync.Once
}

// New builds a Supervisor. The parent-mode ceiling starts at manual (the
// safest) until SetParentMode reports the session's real mode.
func New(ctx context.Context, opts Options) *Supervisor {
	if opts.MaxConcurrent <= 0 {
		opts.MaxConcurrent = DefaultMaxConcurrent
	}
	sctx, cancel := context.WithCancel(ctx)
	return &Supervisor{
		opts:       opts,
		ctx:        sctx,
		cancel:     cancel,
		events:     make(chan Event, 64),
		sem:        make(chan struct{}, opts.MaxConcurrent),
		byName:     map[string]*child{},
		counters:   map[Role]int{},
		parentMode: agent.ModeManual,
	}
}

// Events is the supervisor's notification stream for the parent front-end.
func (s *Supervisor) Events() <-chan Event { return s.events }

// SetParentMode records the parent session's permission mode; children are
// clamped to it at every decision, so a child can never be more permissive
// than its parent.
func (s *Supervisor) SetParentMode(m agent.Mode) {
	s.mu.Lock()
	s.parentMode = m
	s.mu.Unlock()
}

func (s *Supervisor) childMode(c *child) agent.Mode {
	s.mu.Lock()
	ceiling := s.parentMode
	s.mu.Unlock()
	return agent.ClampMode(c.mode, ceiling)
}

// Snapshot returns every child's live status in spawn order.
func (s *Supervisor) Snapshot() []Status {
	s.mu.Lock()
	kids := make([]*child, len(s.children))
	copy(kids, s.children)
	s.mu.Unlock()
	out := make([]Status, len(kids))
	for i, c := range kids {
		out[i] = c.status()
	}
	return out
}

// ActiveCounts reports how many children are still working and how many of
// those are blocked on the user, for the status bar badge.
func (s *Supervisor) ActiveCounts() (active, blocked int) {
	for _, st := range s.Snapshot() {
		switch st.State {
		case StateQueued, StateRunning:
			active++
		case StateBlocked:
			active++
			blocked++
		}
	}
	return active, blocked
}

// CancelAll cancels every child; blocked approval waits unblock and each
// child finishes as failed/cancelled with a well-formed conversation.
func (s *Supervisor) CancelAll() {
	s.mu.Lock()
	kids := make([]*child, len(s.children))
	copy(kids, s.children)
	s.mu.Unlock()
	for _, c := range kids {
		c.cancel()
	}
}

// Close cancels everything, waits for children to finish, removes leftover
// worktrees, and closes the event stream. Idempotent.
func (s *Supervisor) Close() {
	s.closeOnce.Do(func() {
		s.cancel()
		s.CancelAll()
		s.wg.Wait()
		s.mu.Lock()
		kids := make([]*child, len(s.children))
		copy(kids, s.children)
		s.mu.Unlock()
		for _, c := range kids {
			if c.worktree != "" {
				removeWorktree(c.repoTop, c.worktree)
			}
		}
		close(s.events)
	})
}

// WrapExecutor intercepts the orchestration tools on the parent session's
// executor chain; every other call passes through.
func (s *Supervisor) WrapExecutor(next agent.ToolExecutor) agent.ToolExecutor {
	return func(name string, args json.RawMessage) (string, error) {
		switch name {
		case SpawnToolName:
			return s.spawn(args)
		case ReportToolName:
			return s.report(args)
		}
		return next(name, args)
	}
}

// spawn validates the arguments, prepares the child's workspace (a git
// worktree for writers), and starts it in the background.
func (s *Supervisor) spawn(raw json.RawMessage) (string, error) {
	args, err := parseSpawnArgs(raw)
	if err != nil {
		return "", err
	}
	if s.ctx.Err() != nil {
		return "", errors.New("the agent supervisor is shut down")
	}

	s.mu.Lock()
	if len(s.children) >= MaxChildren {
		s.mu.Unlock()
		return "", fmt.Errorf("agent limit reached (%d per session)", MaxChildren)
	}
	name := args.Name
	if name == "" {
		s.counters[args.role]++
		name = fmt.Sprintf("%s-%d", args.role, s.counters[args.role])
	}
	if _, exists := s.byName[name]; exists {
		s.mu.Unlock()
		return "", fmt.Errorf("an agent named %q already exists", name)
	}
	mode := s.parentMode
	s.mu.Unlock()

	root := s.opts.Root
	worktree, repoTop := "", ""
	if args.role == RoleWriter {
		worktree, root, repoTop, err = addWorktree(s.opts.Root)
		if err != nil {
			return "", fmt.Errorf("cannot create an isolated worktree for a writer agent: %w", err)
		}
	}

	cctx, cancel := context.WithCancel(s.ctx)
	env, err := s.opts.NewEnv(cctx, args.role, root)
	if err != nil {
		cancel()
		if worktree != "" {
			removeWorktree(repoTop, worktree)
		}
		return "", fmt.Errorf("cannot set up the agent: %w", err)
	}

	a := agent.New([]provider.Message{{Role: provider.RoleSystem, Content: env.SystemPrompt}}, env.Stream)
	a.SetMaxRounds(args.maxRounds)

	c := &child{
		name:      name,
		role:      args.role,
		task:      args.Task,
		root:      root,
		worktree:  worktree,
		repoTop:   repoTop,
		mode:      mode,
		maxTokens: args.maxTokens,
		ctx:       cctx,
		cancel:    cancel,
		agent:     a,
		env:       env,
		done:      make(chan struct{}),
		state:     StateQueued,
		detail:    "queued",
	}
	// The auto-run executor is the env's rooted, reduced chain.
	a.SetExecutor(env.Executor)
	if s.opts.Record != nil {
		c.rec = s.opts.Record(args.role)
	}

	s.mu.Lock()
	s.children = append(s.children, c)
	s.byName[name] = c
	s.mu.Unlock()

	s.wg.Add(1)
	go s.run(c)
	s.emitUpdate(c)

	note := ""
	if args.role == RoleWriter {
		note = " It edits an isolated copy of the workspace; its changes come back as a single patch the user reviews."
	}
	return fmt.Sprintf("Spawned %s (%s, max %d rounds, ~%s token budget).%s It works in the background: call agent_report with name=%q in a later step to wait for and collect its final report, or agent_report with no arguments for a status overview.",
		name, args.role, args.maxRounds, formatTokens(args.maxTokens), note, name), nil
}

// run drives one child to completion on its own goroutine.
func (s *Supervisor) run(c *child) {
	defer s.wg.Done()
	defer close(c.done)
	if c.rec.End != nil {
		defer c.rec.End()
	}
	defer func() {
		if c.worktree != "" {
			removeWorktree(c.repoTop, c.worktree)
		}
	}()

	// Bounded concurrency: take a slot or notice cancellation while queued.
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-c.ctx.Done():
		c.set(StateFailed, "cancelled")
		s.emit(Event{Kind: EventDone, Status: c.status()})
		return
	}

	c.set(StateRunning, "running")
	s.emitUpdate(c)

	h := &agent.Headless{
		Agent: c.agent,
		Gate:  func(name string) bool { return c.env.Gated[name] },
		Resolve: func(tc provider.ToolCall) string {
			return s.resolveGated(c, tc)
		},
		OnUsage: func(u *provider.Usage) {
			if c.addUsage(u) {
				c.cancel()
			}
			if c.rec.Usage != nil {
				st := c.status()
				c.rec.Usage(1, st.TokensIn, st.TokensOut)
			}
		},
		OnToolCall: func(tc provider.ToolCall) {
			c.mu.Lock()
			c.toolCalls++
			n := c.toolCalls
			c.mu.Unlock()
			c.set(StateRunning, fmt.Sprintf("running · %d tools", n))
			s.emitUpdate(c)
		},
		OnToolResult: func(tc provider.ToolCall, result string) {
			if c.rec.ToolCall != nil {
				outcome := "ok"
				if strings.HasPrefix(result, "error:") {
					outcome = "error"
				}
				c.rec.ToolCall(tc.Name, outcome)
			}
		},
	}

	report, err := h.Run(c.task)
	if err != nil {
		// Keep the child's conversation well-formed for inspection.
		c.agent.CancelTurn()
		c.set(StateFailed, s.failReason(c, err))
		s.emit(Event{Kind: EventDone, Status: c.status()})
		return
	}

	c.mu.Lock()
	c.report = report
	tools := c.toolCalls
	c.mu.Unlock()

	if c.role == RoleWriter {
		s.reviewPatch(c)
	}

	c.set(StateDone, fmt.Sprintf("done · %d tools", tools))
	s.emit(Event{Kind: EventDone, Status: c.status()})
}

func (s *Supervisor) failReason(c *child, err error) string {
	c.mu.Lock()
	budgetHit := c.budgetHit
	c.mu.Unlock()
	switch {
	case budgetHit:
		return fmt.Sprintf("failed · token budget (~%s) exceeded", formatTokens(c.maxTokens))
	case errors.Is(err, agent.ErrRoundCap):
		return fmt.Sprintf("failed · round limit (%d) reached", c.agent.MaxRounds())
	case c.ctx.Err() != nil:
		return "cancelled"
	default:
		return "failed · " + firstLine(err.Error())
	}
}

// resolveGated is the child's approval path: the child's clamped mode policy
// decides, and anything it would ask about routes to the parent user.
func (s *Supervisor) resolveGated(c *child, tc provider.ToolCall) string {
	raw := json.RawMessage(tc.Arguments)
	rooted, err := RootArgs(c.root, tc.Name, raw)
	if err != nil {
		return "error: " + err.Error()
	}

	action, actionErr := actionFor(tc.Name, rooted)
	if actionErr != nil {
		return "error: " + actionErr.Error()
	}
	policy := agent.ModePolicy{Mode: s.childMode(c), CommandAllowlist: s.opts.CommandAllowlist}
	decision, _ := policy.Decide(action)
	switch decision {
	case agent.Deny:
		return agent.PlanModeResult
	case agent.Ask:
		ask, askErr := s.buildAsk(c, tc.Name, rooted, action)
		if askErr != nil {
			return "error: " + askErr.Error()
		}
		approved, ok := s.await(c, ask)
		if !ok {
			return agent.CancelledResult
		}
		if !approved {
			if action.Kind == agent.ActionCommand {
				return "error: the user declined to run this command"
			}
			return "error: the user declined this tool call"
		}
	}

	if tc.Name == tools.ExecCommandName {
		if c.env.RunCommand == nil {
			return "error: command execution is not available to this agent"
		}
		out, code := c.env.RunCommand(c.ctx, action.Command)
		return tools.FormatExecResult(out, code)
	}
	return agent.ExecuteWith(c.env.ExecuteGated, provider.ToolCall{ID: tc.ID, Name: tc.Name, Arguments: string(rooted)})
}

// actionFor classifies a gated call for the mode policy.
func actionFor(name string, args json.RawMessage) (agent.Action, error) {
	switch {
	case name == tools.ExecCommandName:
		var a struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(args, &a); err != nil || strings.TrimSpace(a.Command) == "" {
			return agent.Action{}, errors.New("invalid command arguments")
		}
		cmd := strings.TrimSpace(a.Command)
		return agent.Action{
			Kind:          agent.ActionCommand,
			Command:       cmd,
			SafetyFlagged: len(safety.Check(cmd)) > 0,
		}, nil
	case tools.IsMutating(name):
		return agent.Action{Kind: agent.ActionEdit}, nil
	}
	return agent.Action{Kind: agent.ActionOther}, nil
}

// buildAsk assembles the approval request the parent user reviews.
func (s *Supervisor) buildAsk(c *child, name string, rooted json.RawMessage, action agent.Action) (*Ask, error) {
	switch action.Kind {
	case agent.ActionCommand:
		ask := NewAsk(c.name, AskCommand, "run "+firstLine(action.Command))
		ask.Command = action.Command
		for _, w := range safety.Check(action.Command) {
			ask.Warnings = append(ask.Warnings, w.Risk)
		}
		return ask, nil
	case agent.ActionEdit:
		mut, err := tools.PreviewMutation(name, rooted)
		if err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		ask := NewAsk(c.name, AskEdit, mut.Action+" "+displayPath(c.root, mut.Path))
		ask.Hunks = diff.Compute(mut.OldText, mut.NewText)
		return ask, nil
	}
	ask := NewAsk(c.name, AskGeneric, "use "+name)
	ask.Summary = compactArgs(rooted)
	return ask, nil
}

// await routes one ask to the parent and blocks the child until the user
// answers or the child is cancelled; ok is false on cancellation.
func (s *Supervisor) await(c *child, ask *Ask) (approved, ok bool) {
	c.set(StateBlocked, "waiting approval: "+ask.Title)
	s.emit(Event{Kind: EventAsk, Ask: ask, Status: c.status()})
	select {
	case v := <-ask.resp:
		c.set(StateRunning, "running")
		s.emitUpdate(c)
		return v, true
	case <-c.ctx.Done():
		return false, false
	}
}

// reviewPatch computes the writer's worktree patch and routes it through the
// approval flow before anything touches the real checkout.
func (s *Supervisor) reviewPatch(c *child) {
	patch, err := worktreePatch(c.worktree)
	if err != nil {
		c.mu.Lock()
		c.patchNote = "the worktree patch could not be computed: " + firstLine(err.Error()) + "; no files were changed"
		c.mu.Unlock()
		return
	}
	if strings.TrimSpace(patch) == "" {
		c.mu.Lock()
		c.patchNote = "no file changes were made"
		c.mu.Unlock()
		return
	}

	hunks, files := PatchHunks(patch)
	adds, dels := diff.Stats(hunks)
	title := fmt.Sprintf("apply patch (+%d −%d, %d file(s))", adds, dels, files)
	ask := NewAsk(c.name, AskPatch, title)
	ask.Hunks = hunks

	note := ""
	approved, ok := s.await(c, ask)
	switch {
	case !ok:
		note = "cancelled before the patch was reviewed; no files were changed" + savedPatchNote(c.name, patch)
	case !approved:
		note = "the user declined the patch; no files were changed" + savedPatchNote(c.name, patch)
	default:
		if applyErr := applyPatch(c.repoTop, patch); applyErr != nil {
			note = "the patch failed to apply cleanly: " + firstLine(applyErr.Error()) + savedPatchNote(c.name, patch)
		} else {
			note = fmt.Sprintf("patch applied to the workspace (+%d −%d, %d file(s))", adds, dels, files)
		}
	}
	c.mu.Lock()
	c.patchNote = note
	c.mu.Unlock()
}

// savedPatchNote persists an unapplied patch so nothing is lost, returning
// the note fragment naming where it went (empty if saving failed).
func savedPatchNote(name, patch string) string {
	f, err := os.CreateTemp("", "shhh-"+name+"-*.patch")
	if err != nil {
		return ""
	}
	defer f.Close()
	if err := f.Chmod(0o600); err == nil {
		if _, err := f.WriteString(patch); err == nil {
			return " (patch saved to " + f.Name() + ")"
		}
	}
	return ""
}

// report implements agent_report: a status overview with no name, or a
// blocking wait for one child's final report.
func (s *Supervisor) report(raw json.RawMessage) (string, error) {
	var args struct {
		Name string `json:"name"`
		Wait *bool  `json:"wait"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
	}
	if args.Name == "" {
		return s.statusOverview(), nil
	}

	s.mu.Lock()
	c, ok := s.byName[args.Name]
	s.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("no agent named %q; spawn it first, or call agent_report with no arguments for the roster", args.Name)
	}

	if args.Wait == nil || *args.Wait {
		select {
		case <-c.done:
		case <-s.ctx.Done():
			return "", errors.New("cancelled")
		}
	}
	return c.reportText(), nil
}

func (s *Supervisor) statusOverview() string {
	statuses := s.Snapshot()
	if len(statuses) == 0 {
		return "No agents have been spawned this session."
	}
	var sb strings.Builder
	for _, st := range statuses {
		fmt.Fprintf(&sb, "%s (%s): %s — %s\n", st.Name, st.Role, st.Detail, firstLine(st.Task))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// reportText is what the parent model receives about a child: its status
// line, its final report, and (for writers) what happened to its patch.
func (c *child) reportText() string {
	st := c.status()
	c.mu.Lock()
	report := c.report
	patchNote := c.patchNote
	c.mu.Unlock()

	var sb strings.Builder
	fmt.Fprintf(&sb, "%s (%s) — %s · %d tool calls · ~%s tokens\n\n",
		st.Name, st.Role, st.Detail, st.ToolCalls, formatTokens(st.TokensIn+st.TokensOut))
	switch {
	case st.State == StateFailed && report == "":
		sb.WriteString("The agent did not finish; no final report was produced.")
	case report == "":
		sb.WriteString("(the agent produced no final report)")
	default:
		sb.WriteString(report)
	}
	if patchNote != "" {
		sb.WriteString("\n\n[" + patchNote + "]")
	}
	return sb.String()
}

// emit delivers a must-see event (asks, completions), giving up only when the
// supervisor is shut down.
func (s *Supervisor) emit(ev Event) {
	select {
	case s.events <- ev:
	case <-s.ctx.Done():
	}
}

// emitUpdate delivers a best-effort progress update; drops are fine because
// rendering reads live snapshots.
func (s *Supervisor) emitUpdate(c *child) {
	select {
	case s.events <- Event{Kind: EventUpdate, Status: c.status()}:
	default:
	}
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " …"
	}
	return s
}

func formatTokens(n int64) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%.0fk", float64(n)/1000)
}

// compactArgs renders tool arguments as a short "k=v" line for generic
// approval summaries.
func compactArgs(raw json.RawMessage) string {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return string(raw)
	}
	var parts []string
	for k, v := range m {
		switch val := v.(type) {
		case string:
			parts = append(parts, k+"="+val)
		default:
			b, _ := json.Marshal(val)
			parts = append(parts, k+"="+string(b))
		}
	}
	return strings.Join(parts, " ")
}
