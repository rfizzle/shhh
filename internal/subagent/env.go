package subagent

import (
	"context"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/tools"
)

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
	// (contained when a mechanism is available). It answers with how the
	// command ended as well as what it printed, so a command that never
	// started keeps its category and one whose ending nobody read says so.
	RunCommand func(ctx context.Context, command string) tools.ExecResult
	// CommandRefusal, when set, is the answer every command this child makes
	// gets before anything decides about it: a child whose commands must be
	// contained on a host with nothing to contain them. It is answered ahead
	// of the policy, the classifier and the card, because a card whose every
	// answer ends in the same refusal puts no decision to anyone.
	// See docs/capabilities/containment.md#containment-can-be-required.
	CommandRefusal string
	// Reduce runs a command's output through the session's reduction
	// pipeline before it becomes the child's tool result: a head, a tail,
	// every line that names an error or a failure, and an id that pages the
	// whole output back out of the evidence store. Nil reduces nothing,
	// which leaves a child reading the first few kilobytes of a long
	// command and nothing after them — for a test run, forty passing
	// packages and never the verdict.
	//
	// It is a field rather than a wrap around RunCommand because the
	// reduction sits between the scrub and the format: the store keeps the
	// text the child was shown, and reducing an already-formatted result
	// would let the notice count the exit-code line as output.
	// See docs/capabilities/evidence.md#reduction-is-for-unbounded-output.
	Reduce func(tool, result string) string
	// KeepResult reports a tool result the window trim must leave where it
	// is. A result is elided on the assumption that it was consumed when it
	// arrived; a skill's instructions are the exception, because they are
	// what the rest of the child's work is meant to follow — and losing them
	// fails silently, since the child carries on without them. Nil keeps
	// nothing back.
	KeepResult func(content string) bool
	// Archive is where a tool result goes just before the trim replaces it,
	// answering with the id the placeholder then names, so what left the
	// child's window is still somewhere the child's own evidence tool can
	// ask for. False is a result that could not be kept and the trim goes
	// ahead with the bare placeholder: a child at the end of its window is
	// exactly the child that most needs the room back. Nil makes elision
	// permanent.
	// See docs/capabilities/evidence.md#a-trim-makes-the-same-promise.
	Archive func(tool, content string) (string, bool)
	// Keep is where the middle of a long command result goes when the bound
	// on a tool result cuts it, answering with the id that pages the whole
	// output back. It is the same store the two fields above write to, and
	// it is a third field because it is a third moment: the reduction runs
	// first and fails open on output it would barely shrink, and what
	// reaches the cap after that still has a middle worth keeping. Without
	// it a child whose reduction failed open is told a byte count and
	// offered nothing.
	// See docs/capabilities/evidence.md#the-reader-can-always-get-the-whole-thing-back.
	Keep tools.ExecKeep
	// Gated names the tools that must go through approval routing.
	Gated map[string]bool
	// Scrub, when set, is installed on the child's agent so its
	// conversation never holds a session secret; nil scrubs nothing.
	Scrub func(provider.Message) provider.Message
	// Summarizer, when set and enabled, takes periodic readings of the child
	// so a run that has drifted or that already has what it needs is
	// interrupted the way a session is. Nil takes no readings, which is what
	// summary.subagents=false leaves behind: a child nothing reads is never
	// listed as steered, so the roster says so rather than leaving the parent
	// to read an empty field as good news.
	Summarizer *agent.Summarizer
	// Steering is the interruption machinery's tuning as the config file
	// left it. A child runs the same machinery a session does, so the same
	// wordings and thresholds reach it — all but the interval, which is the
	// surface's own.
	Steering agent.Steering
	// Retries bounds a child's stalls as the config file left it; nil keeps
	// the built-in bound. A fan-out is where waiting one out matters most,
	// because a limit refuses every child at once.
	Retries *int
	// Window is the child model's context window as the downloaded price
	// table answers it, and 0 where the table has no row for the model. The
	// table is asked in the CLI because that is where it is open, and it is
	// asked first: the family floor this package can reach on its own is a
	// conservative reading of a model name, so a child on a model the table
	// knows would otherwise recover its window against a smaller number than
	// it has, or — for a name no family matches — against nothing at all.
	Window int64
	// ToolTokens is what the child's registered definitions cost on every
	// request. They are not in the conversation, so a child that left them
	// out of the estimate would think it had a toolset's worth of room it
	// does not have — and a child's toolset is now most of a session's.
	ToolTokens int64
	// WrapAuto and WrapGated put the surface's own seams around the child's
	// two tool dispatchers: the calls that run on their own, and the calls
	// that are decided about. They are what carries the person's own
	// commands into a child — a rule that stopped at the session would be a
	// rule anybody could walk around by delegating the act
	// (docs/capabilities/hooks.md#a-hook-fires-in-a-child-too).
	//
	// They wrap rather than replace: what comes back runs the dispatcher it
	// was handed, so a surface that installs nothing changes nothing. The
	// gated wrap goes around the whole resolution — the policy, the
	// classifier and the card included — which is where a session's own
	// approver meets the same seam; only the containment refusal
	// (CommandRefusal) stands in front of it, as it stands in front of a
	// session's pre-tool hook. That keeps the tier rule: a call is
	// dispatched by the kind of call it is, whatever a seam said about it.
	// Nil leaves the dispatcher exactly as this Env built it.
	WrapAuto  func(Seam, agent.ToolExecutor) agent.ToolExecutor
	WrapGated func(Seam, func(provider.ToolCall) string) func(provider.ToolCall) string
	// Start, Stop and Compaction are the surface's seams at the rest of a
	// child's life: as it starts, as it ends, and either side of a
	// compaction of its conversation.
	//
	// Start answers with a refusal, or "" to let the child run; a refusal
	// fails the child before its first request. Stop is handed the report the
	// child ended on and answers with a steer, or "" to let it end; a steer
	// on a child that answered goes in through Steer and the child carries
	// on, and on one that ended any other way it is dropped, since there is
	// nothing left to carry on. Compaction puts the seams on the child's own
	// recovery step, which cannot be refused. None of the three is handed
	// anything that names the child's role, its tools or its mode as a thing
	// to set: what comes back is text, or nothing.
	// See docs/capabilities/hooks.md#a-child-starts-and-ends-at-a-seam.
	Start      func(Life) (refusal string)
	Stop       func(l Life, final string) (steer string)
	Compaction func(Life, *agent.Compactor)
	// Sweeps is where this child's circling detector is asked what ground it
	// has been over without writing anything, for the digest its readings are
	// made of. The surface owns the detector because the surface is what
	// wrapped the child's two dispatchers with it, and a count taken anywhere
	// else would be counting a different window. Nil where the surface wired
	// no detector, which leaves the field out of the digest.
	//
	// A child is the surface this matters most on: it is the least
	// supervised thing a session runs, its rounds are spent out of sight, and
	// one that read for twenty-seven rounds and wrote nothing was called on
	// target by all three of its readings.
	Sweeps func() []string
	// TreeCheck, when set, is the reading that tells the child's turn its
	// workspace moved under it, as the surface configured it. Own is filled
	// in by this package rather than there: what the child has written is
	// the child's own record, and the reading has to subtract it or every
	// file the child edits comes back at the next boundary as somebody
	// else's work.
	//
	// A child is what the reading is for as much as a session is — it works
	// beside its siblings, beside the session that spawned it and beside
	// whoever else has the checkout open, and it is the party with nobody
	// watching its screen. Nil takes no reading.
	TreeCheck *agent.TreeCheck
}

// Seam is what a child's dispatchers hand whoever wraps them: where the child
// has got to, where a line about it goes on its own transcript, and where a
// verdict about one of its calls is filed.
//
// All three are the child's, and the child does not exist when the surface
// builds its Env — a wrap is built once for the surface and a Seam is passed
// per child, which is why these are arguments to the wrap rather than fields
// captured in it.
type Seam struct {
	// At is where the child has got to: which of its turns, and which round
	// of that turn.
	At func() observe.Pos
	// Note puts one line on the child's transcript. A child has no screen of
	// its own, and a surface that wrote to stderr instead would draw over the
	// session that spawned it.
	Note func(text string)
	// Record files a verdict at the codes a session records its own at. An
	// approval rate that covered the parent and not its children would be a
	// rate over the half of the work a person was looking at.
	Record func(decision, code string)
}

// Life is who a child is, for the seams at its start, its end and its
// compactions: its name, its role and the agent that spawned it, beside the
// child's own seam for where a line about it goes and where it has got to.
type Life struct {
	Name, Role, Parent string
	Seam
}

// autoExecutor is the child's auto-run dispatcher with the surface's seam
// around it, or the bare dispatcher where the surface installed none.
func (e Env) autoExecutor(s Seam) agent.ToolExecutor {
	if e.WrapAuto == nil {
		return e.Executor
	}
	return e.WrapAuto(s, e.Executor)
}

// execResult is a command's output as the child's tool result: reduced, then
// formatted with somewhere to put what the format's own cap cuts. Every route
// from a child's command to the formatter runs through here, so every child
// command enters the same error-result convention as its parent after
// reduction — and the same offer of the whole output back.
func (e Env) execResult(result tools.ExecResult) string {
	if e.Reduce != nil {
		result.Output = e.Reduce(tools.ExecCommandName, result.Output)
	}
	return tools.FormatExecResultKeeping(result, e.Keep)
}

// childCompactor is a child's window-recovery step, or nothing where the
// window cannot be established. The price table's answer comes first, carried
// on the Env because that is where the table is open, and the family floor is
// the fallback — a name neither can place leaves the child running exactly as
// it did before, which is the cheaper of the two mistakes: recovering against
// a guessed window would throw away the work of a child that had most of its
// room left.
//
// The summary is asked of the child's own model. The one door a child has out
// is its stream, and a request on another model would have to be built
// somewhere that knows what the child's tools are.
func childCompactor(model string, env Env) *agent.Compactor {
	window := env.Window
	if window <= 0 {
		window, _ = provider.ContextWindowFor(model)
	}
	if window <= 0 {
		return nil
	}
	return &agent.Compactor{Model: model, Window: window, ToolTokens: env.ToolTokens}
}

// roundCap is the cap a child's agent is running under as the record spells
// it: the number, or 0 for none. MaxRounds alone cannot say the second,
// because it answers with the default for an uncapped agent.
func roundCap(a *agent.Agent) int {
	if a.Uncapped() {
		return 0
	}
	return a.MaxRounds()
}

// newChildAgent builds a child's agent with everything a child's agent needs.
//
// It exists because there are two paths to one — a spawn and a retry — and a
// setting added to the first quietly does not reach the second. That already
// happened once: a retried child would have inherited a session's check-in
// interval, and the only symptom would have been a long child nobody asked
// anything, which is precisely the failure the interval exists to prevent and
// precisely the one that leaves no trace.
func newChildAgent(env Env, maxRounds int) *agent.Agent {
	a := agent.New([]provider.Message{{Role: provider.RoleSystem, Content: env.SystemPrompt}}, env.Stream)
	a.SetMaxRounds(maxRounds)
	a.SetSteering(env.Steering)
	// After the steering, and deliberately: the configured interval is a
	// session's, and a child has none of what makes a session's long one
	// safe. Everything else in the set — the wordings, the widening, what a
	// steer quotes — is the same question asked of the same machinery.
	a.SetCheckInInterval(ChildCheckInInterval)
	// And its exit, in the same place and for the same kind of reason: a
	// child has no person to say "the work is finished" to, and one that says
	// it into its own transcript carries on reading. Its final report is what
	// ends its turn, so that is what its check-ins point at — every route to
	// one, not just the round cap that used to name it.
	a.SetFinished(agent.FinishedAsSubAgent)
	// And both halves of what a trim promises, here for the same reason the
	// interval and the exit are. A child's window is recovered at every round
	// boundary (childCompactor) rather than ahead of a person's request, so it
	// trims far more often than a session does, and there is nobody watching
	// it to notice a finding gone or a skill's instructions stop being
	// followed.
	if env.KeepResult != nil {
		a.KeepResults(env.KeepResult)
	}
	if env.Archive != nil {
		a.StoreElided(env.Archive)
	}
	if env.Scrub != nil {
		a.SetScrub(env.Scrub)
	}
	return a
}
