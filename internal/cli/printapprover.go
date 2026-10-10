package cli

// The unattended approver: how a run with nobody in front of it answers
// approval-gated tool calls from its flags and lists, and the verdict and
// written-path records that ride along with each answer.

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/approval"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/evidence"
	"github.com/rfizzle/shhh/internal/mcp"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/process"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/radius"
	"github.com/rfizzle/shhh/internal/safety"
	"github.com/rfizzle/shhh/internal/scope"
	"github.com/rfizzle/shhh/internal/structural"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/chat"
	"github.com/rfizzle/shhh/internal/web"
)

// lastVerdict is the policy's most recent answer, which is what says whether
// a refusal is why the turn ended.
//
// The last one and not any one. A run that was denied a command, found
// another way and finished was not refused — it did the work — and reporting
// it as refused would teach a script to ignore the code. A denial that is
// still standing when the model stops is the one that ended the turn.
type lastVerdict struct {
	mu   sync.Mutex
	code string
}

// wrap is the reporter the approver is handed: every verdict reaches the
// record through next and is remembered here on its way past, so there is no
// second place a decision has to be reported to and could be forgotten.
func (l *lastVerdict) wrap(next func(decision, reason string)) func(string, string) {
	return func(decision, reason string) {
		l.mu.Lock()
		l.code = decision
		l.mu.Unlock()
		next(decision, reason)
	}
}

// wrapTook is wrap for the reporter that carries how long the classifier
// took.
func (l *lastVerdict) wrapTook(next func(decision, reason string, took time.Duration)) func(string, string, time.Duration) {
	return func(decision, reason string, took time.Duration) {
		l.mu.Lock()
		l.code = decision
		l.mu.Unlock()
		next(decision, reason, took)
	}
}

func (l *lastVerdict) refused() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.code == observe.DecisionDeny
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
	path := tools.WrittenPath(tc.Name, tc.Arguments)
	if path == "" || strings.HasPrefix(result, "error:") {
		return
	}
	w.mu.Lock()
	w.list = append(w.list, path)
	w.mu.Unlock()
}

// wrote adds paths a call did not make: a child's patch landing on the tree
// is this run changing files, and nothing in the call log says so — the
// writer edited a copy of the checkout and the parent only applied it.
func (w *writtenByCalls) wrote(paths ...string) {
	if len(paths) == 0 {
		return
	}
	w.mu.Lock()
	w.list = append(w.list, paths...)
	w.mu.Unlock()
}

func (w *writtenByCalls) paths() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.list...)
}

// changed is this run's changeset for the digest its readings are made of, in
// the three numbers a session's changeset answers with: the files, and no
// lines. Nothing here reads a file either side of a write — the list is the
// calls that were made, not a record of what they did — and a count of files
// is what the reading needs to tell a run that has started acting from one
// that is still reading (agent.SummaryChanges takes both shapes). A file
// written twice is one file, as it is on the rail.
func (w *writtenByCalls) changed() (files, added, removed int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	seen := make(map[string]bool, len(w.list))
	for _, p := range w.list {
		seen[p] = true
	}
	return len(seen), 0, 0
}

// headlessGate is what a run with nobody in front of it always holds an
// answer for: the command, the two file tools and git's writing half. Which
// calls are put to a decision, and at which tier, is the classifier's.
func headlessGate(name string) bool {
	return name == tools.ExecCommandName || tools.IsMutating(name) || name == structural.GitWriteToolName
}

// headlessWrites is what a run with nobody in front of it hands the git
// stager. A conversation gets nothing: it has no editor, so it has no work of
// its own to stage. Everything else stages exactly the paths its own calls
// wrote, which is the rule a session states with its changeset. ignore is
// commit.secret_ignore, the fixtures a commit may carry a credential shape in.
func headlessWrites(session chatSession, own *writtenByCalls, ignore []string) *structural.Writes {
	if session.conversation {
		return nil
	}
	return &structural.Writes{Files: own.paths, Hooks: projectTrust().RunsOwnPrograms(), SecretIgnore: ignore}
}

// headlessApproval is what the unattended approver answers a call with: the
// run's flags and its allowlist, the standing rules, the runner, the reducer
// and the record, the toolsets whose calls it runs, and what the run has
// beyond its flags. It is one value because three surfaces build the
// approver — a scripted run, and a served session twice over — and a
// signature with a parameter per toolset was one where two nils swapped in a
// call still compiled. A zero field is a run that was not handed that thing.
type headlessApproval struct {
	opts      printOpts
	allowlist []string
	// rules is the standing rules — the deny lists, the containment's
	// refusal, the working scope — in the one assembly the screen answers
	// its calls from too (unattendedRules).
	rules        approval.Router
	run          func(context.Context, string) tools.ExecResult
	red          *evidence.Reducer
	record       func(decision, reason string)
	webTools     *web.Toolset
	procSup      *process.Supervisor
	mutationHook chat.MutationHook
	mcpTools     *mcp.Toolset
	structTools  *structural.Toolset
	un           unattended

	// recordTook is record for a verdict the classifier reached, with how
	// long it took; nil sends those through record untimed.
	recordTook func(decision, reason string, took time.Duration)
}

// headlessApprover resolves approval-gated tool calls without a prompt:
// policy decides. Safety-flagged commands are always denied — there is no
// human to confirm them in a headless run. Approved results run through the
// reduction pipeline (red is nil-safe) like every other tool result. Each
// verdict is reported to record (nil-safe) as a content-free decision event.
//
// The standing rules are read before anything that can approve, --yes
// included: a headless run is the surface with nobody to ask, so a list the
// user wrote to mean "never" has to mean it here most of all. They are the
// screen's rules, asked through the same assembly; what this approver adds
// after them — the flags, the judge, a refusal where nobody can be asked — is
// its own.
//
// The containment's refusal, when the run has one, is the answer every
// command gets before policy is consulted at all: a run told to require
// containment on a host with none has nothing left to decide.
//
// un is what this run has beyond its flags: the supervisor a spawn is handed
// to, and the judge a call the flags did not answer is put to (approvals.go).
// A zero value is the surface exactly as it was — flags, or a refusal.
func headlessApprover(ctx context.Context, r headlessApproval) func(provider.ToolCall) string {
	holds := agent.Answers{Has: r.answers}
	if r.procSup != nil {
		holds.Command = process.CommandOf
	}
	// The two ways of running a command differ only here: what the refusal
	// calls the call, what a malformed call is told, and what runs once it
	// is admitted.
	start := commandAdmission{
		noun:     "process start",
		denied:   "error: process start denied (",
		parseErr: func(err error) string { return "error: " + err.Error() },
		exec: func(tc provider.ToolCall, _ string) string {
			exec := func(_ string, args json.RawMessage) (string, error) { return r.procSup.Execute(args) }
			return r.red.Process(tc.Name, agent.ExecuteWith(exec, tc))
		},
	}
	foreground := commandAdmission{
		noun:     "command",
		denied:   "error: command denied (",
		parseErr: func(error) string { return "error: invalid command arguments" },
		exec: func(_ provider.ToolCall, command string) string {
			// The typed result, not an output/status pair: a command that
			// never started keeps its category and one whose ending nobody
			// read keeps that, on the status line the transcript, the stream
			// and the record all read.
			// See docs/capabilities/headless.md#the-stream-is-the-record-as-it-happens.
			result := r.run(ctx, command)
			result.Output = r.red.Process(tools.ExecCommandName, result.Output)
			// The store is handed to the formatter as well as to the
			// reduction: the pipeline fails open on a result it would barely
			// shrink, and the cap below it still has a middle to put
			// somewhere the model can ask for it.
			return tools.FormatExecResultKeeping(result, r.red.Keep)
		},
	}
	return func(tc provider.ToolCall) string {
		call, callErr := agent.ClassifyCall(tc.Name, json.RawMessage(tc.Arguments), holds)
		switch {
		case !r.answers(tc.Name):
			// A call this run holds nothing for is refused below, whatever
			// its tier.
		case call.Action.Kind == agent.ActionOther && tc.Name == subagent.SpawnToolName:
			return r.approveSpawn(tc, call)
		case call.Action.Kind == agent.ActionOther:
			return r.approveServerCall(tc, call)
		case call.Action.Kind == agent.ActionFetch:
			return r.approveFetch(tc, call)
		// A process start is approved like a command: safety-flagged
		// commands are always denied headless; --yes or an allowlist match
		// opts in.
		case call.Action.Kind == agent.ActionCommand && tc.Name == process.ToolName:
			return r.admitCommand(tc, call, callErr, start)
		case call.Action.Kind == agent.ActionCommand:
			return r.admitCommand(tc, call, callErr, foreground)
		case call.Tier == agent.TierWrite && tc.Name == structural.GitWriteToolName:
			return r.approveGitWrite(tc, call)
		case call.Tier == agent.TierWrite:
			return r.approveWrite(tc, call)
		}
		return "error: tool " + tc.Name + " cannot be approved in this session"
	}
}

// note reports a verdict to the record, which a run may not have been
// handed.
func (r headlessApproval) note(decision, reason string) {
	r.noteTook(decision, reason, 0)
}

// noteTook is note for a verdict the classifier reached, with how long it
// took.
func (r headlessApproval) noteTook(decision, reason string, took time.Duration) {
	switch {
	case took > 0 && r.recordTook != nil:
		r.recordTook(decision, reason, took)
	case r.record != nil:
		r.record(decision, reason)
	}
}

// refuse is where every one of this approver's refusals goes. The record
// takes the content-free event, and the diagnostic log takes the line a
// person reads at 3 a.m. when the run did nothing and stderr went to
// wherever the scheduler sends it. One place for both, because a refusal
// that reached one of them and not the other is a run whose record and
// whose log disagree about what happened to it.
func (r headlessApproval) refuse(tc provider.ToolCall, command, rule string) {
	r.refuseTook(tc, command, rule, 0)
}

// refuseTook is refuse for a refusal the classifier reached, with how long it
// took.
func (r headlessApproval) refuseTook(tc provider.ToolCall, command, rule string, took time.Duration) {
	r.noteTook(observe.DecisionDeny, rule, took)
	at := r.un.pos()
	agent.LogRefusal(tc.Name, command, rule, at.Turn, at.Round)
}

// answer is what a gated call gets once every standing refusal — the
// containment requirement, the deny list, the safety table — has had its
// say: the flags, then the classifier where --mode auto asked for one,
// then a refusal. ok is false with the refusal to hand back; ok is true
// with the reason code the allow is recorded under, which the caller
// notes after its own scope check, since a call refused for what it
// reaches was never allowed.
//
// The action it builds the verdict on carries no scope fields, and it is
// the caller's headlessScopeCheck rather than ResolveUnattended's own
// backstop that holds the boundary here. The two are the same rule read
// at different moments: a session resolves what a call reaches before
// the classifier sees it, so its verdict can be overruled in one place;
// an unattended run resolves it after, because the check it already had
// answers with the sentence the model reads and adds an ordinary
// directory to the scope as it goes. Whichever runs, an Allow that
// reaches somewhere the run was not given is refused before it runs.
//
// It is one method and not a branch written out at each tier because
// that order is the whole permission policy of an unattended run, and a
// tier that spelled it out again is a tier that can come to disagree.
//
// took is how long the classifier took where it was asked, and zero where a
// flag answered, for the row the caller notes the allow under.
func (r headlessApproval) answer(tc provider.ToolCall, action agent.Action, byFlag bool, flagReason, what, without string) (string, time.Duration, bool) {
	if byFlag {
		return flagReason, 0, true
	}
	decision, why, code, took := r.un.judge.judge(tc, action)
	if decision == agent.Allow {
		return code, took, true
	}
	r.refuseTook(tc, action.Command, code, took)
	if r.un.judge == nil {
		return "error: " + what + " not approved: headless mode denies " + without, 0, false
	}
	return agent.UnattendedRefusedResult(what, why), 0, false
}

// answers is which calls this run has an answer for: the tools it was
// handed, and the command and the two file tools, which it always answers
// for. It is this surface's statement of what it holds and nothing more:
// the tier each call sits at, and the action the verdict is built on, are
// the classifier's, so this run cannot come to read a call's tier
// differently from the session beside it.
// See docs/capabilities/approvals-and-safety.md#one-classifier-names-a-calls-tier.
func (r headlessApproval) answers(name string) bool {
	switch {
	case r.un.sup != nil && name == subagent.SpawnToolName,
		r.mcpTools != nil && r.mcpTools.Has(name),
		r.webTools != nil && name == web.FetchToolName,
		r.procSup != nil && name == process.ToolName,
		r.structTools != nil && name == structural.GitWriteToolName,
		name == tools.ExecCommandName,
		tools.IsMutating(name):
		return true
	}
	return false
}

// approveSpawn answers a call starting a child. Starting a child is a gated
// call and is answered like the rest: --yes is the blanket yes a person gave
// the whole run, auto mode puts it to the classifier, and a run given neither
// refuses it.
//
// That one answer covers the child as well as the spawn. A child works under
// this run's policy (print.go, serve.go) and there is no second card to draw
// for the calls it goes on to make, so this is the decision — and the only
// one.
// See docs/capabilities/subagents.md#spawning-is-a-decision.
func (r headlessApproval) approveSpawn(tc provider.ToolCall, call agent.Classified) string {
	// A model the session cannot run is refused ahead of the verdict, as
	// the session refuses it ahead of its card: no classifier round is spent
	// on a spawn that cannot start.
	// See docs/capabilities/subagents.md#the-model-is-offered-the-models-it-can-name.
	if _, err := r.un.sup.CheckModel(json.RawMessage(tc.Arguments)); err != nil {
		return "error: " + err.Error()
	}
	reason, took, ok := r.answer(tc, call.Action,
		r.opts.yes, observe.ReasonHeadlessYes, "spawning an agent", "sub-agents by default (run with --yes)")
	if !ok {
		return reason
	}
	r.noteTook(observe.DecisionAllow, reason, took)
	return r.red.Process(tc.Name, agent.ExecuteWith(func(_ string, args json.RawMessage) (string, error) {
		return r.un.sup.Spawn(args)
	}, tc))
}

// approveServerCall answers a server call, an external action like a fetch:
// --yes opts in, the default denies.
func (r headlessApproval) approveServerCall(tc provider.ToolCall, call agent.Classified) string {
	reason, took, ok := r.answer(tc, call.Action,
		r.opts.yes, observe.ReasonHeadlessYes, tc.Name, "external actions by default (run with --yes)")
	if !ok {
		return reason
	}
	r.noteTook(observe.DecisionAllow, reason, took)
	return r.red.Process(tc.Name, agent.ExecuteWith(r.mcpTools.Execute, tc))
}

// approveFetch answers web_fetch, an external action: --yes opts in, the
// default denies like every other gated call.
func (r headlessApproval) approveFetch(tc provider.ToolCall, call agent.Classified) string {
	// The host is on the action because it is the unit a fetch is judged
	// on: the classifier is being asked whether this page is an outbound
	// channel worth stopping for, and where the request goes is most of that
	// question.
	// The reading comes from the function every surface asks, so an
	// unattended run judges a host the way the session beside it does.
	fetchAction := call.Action
	if plan, err := r.webTools.FetchPlan(json.RawMessage(tc.Arguments)); err == nil {
		fetchAction.Host = plan.Host
	}
	var reason string
	var took time.Duration
	if r.un.conversation != nil {
		// The conversation's policy is the whole answer: it refuses a host
		// on the deny list and allows every other read, so no flag and no
		// classifier is asked. It writes its own refusal line, which is why
		// this does not go through refuse.
		decision, why := r.un.conversation.Decide(fetchAction)
		if decision != agent.Allow {
			r.note(observe.DecisionDeny, observe.ReasonCode(why))
			return agent.DeniedHostResult
		}
		reason = observe.ReasonCode(why)
	} else {
		var ok bool
		reason, took, ok = r.answer(tc, fetchAction,
			r.opts.yes, observe.ReasonHeadlessYes, "web fetch", "external actions by default (run with --yes)")
		if !ok {
			return reason
		}
	}
	r.noteTook(observe.DecisionAllow, reason, took)
	fetch := func(name string, args json.RawMessage) (string, error) {
		return r.webTools.Execute(web.Orchestrator, name, args)
	}
	return r.red.Process(tc.Name, agent.ExecuteWith(fetch, tc))
}

// commandAdmission is what sets one way of running a command apart from the
// other, for admitCommand: the noun its refusals use and the opening of its
// safety refusal, what a call whose arguments did not parse is told, and what
// runs once it is admitted. Whether a destroying command is read against the
// directory the call names is the classification's (approval.CallOf).
type commandAdmission struct {
	noun     string
	denied   string
	parseErr func(error) string
	exec     func(tc provider.ToolCall, command string) string
}

// admitCommand is the one ladder a command climbs before it runs unattended,
// whether it starts a process or runs in the foreground: the containment
// requirement, the arguments, the deny list, the safety table, the flags and
// the classifier, then the working scope. The order is the policy, so it is
// written once.
func (r headlessApproval) admitCommand(tc provider.ToolCall, call agent.Classified, callErr error, how commandAdmission) string {
	// Refused before the approval it would otherwise be given: a run that
	// requires containment has nothing to approve where none is in force,
	// and the refusal is the result the model reads.
	//
	// It is read off the rules before the arguments are, so a command whose
	// arguments did not parse is still told the one thing that would have
	// stopped it anyway.
	if r.rules.Containment != "" {
		return r.rules.Containment
	}
	if callErr != nil {
		return how.parseErr(callErr)
	}
	command := call.Action.Command
	// Before --yes and before the allowlist: a deny list that a flag could
	// out-rank would be a preference and not a rule. A destroying command
	// pointed at something this run may not destroy is answered in the same
	// place, through the function the session's policy asks.
	if refusal, refused := r.rules.Rule(approval.CallOf(tc.Name, call)); refused {
		r.refuse(tc, command, refusal.Code)
		return refusal.Result
	}
	if warnings := safety.Check(command); len(warnings) > 0 {
		risks := make([]string, 0, len(warnings))
		for _, w := range warnings {
			risks = append(risks, w.Risk)
		}
		r.refuse(tc, command, observe.ReasonSafety)
		return how.denied + strings.Join(risks, "; ") + "); safety-flagged commands require interactive approval"
	}
	byFlag, flagReason := headlessCommandFlags(r.opts, r.allowlist, command)
	reason, took, ok := r.answer(tc, call.Action,
		byFlag, flagReason, how.noun, "commands by default (run with --yes or --allow)")
	if !ok {
		return reason
	}
	// The working scope is checked before the grant is spent: an
	// allowlisted command shape is not a licence to write outside the
	// directories this run was given, and a process start is a command as
	// much as a foreground one.
	if deny, ok := headlessScopeCheck(r.rules.Scope, r.opts.yes, radius.WritePaths(command)); !ok {
		r.refuse(tc, command, observe.ReasonOutOfScope)
		return deny
	}
	r.noteTook(observe.DecisionAllow, reason, took)
	return how.exec(tc, command)
}

// approveGitWrite answers a git write. It sits at the write tier, so it is
// answered where a file modification is answered — after the deny list,
// which reads the command line the call stands for, because a person who
// refused `git commit` refused the act and not the spelling.
func (r headlessApproval) approveGitWrite(tc provider.ToolCall, call agent.Classified) string {
	line := call.Action.Command
	if refusal, refused := r.rules.Rule(approval.CallOf(tc.Name, call)); refused {
		r.refuse(tc, line, refusal.Code)
		return refusal.Result
	}
	// The line the call stands for travels with it, so the judge reads
	// `git commit` rather than a tool name and a blob of arguments — the
	// same reading the deny list just took.
	reason, took, ok := r.answer(tc, call.Action,
		r.opts.yes, observe.ReasonHeadlessYes, "git write", "writes by default (run with --yes)")
	if !ok {
		return reason
	}
	r.noteTook(observe.DecisionAllow, reason, took)
	return r.red.Process(tc.Name, agent.ExecuteWith(r.structTools.Execute, tc))
}

// approveWrite answers a file modification.
func (r headlessApproval) approveWrite(tc provider.ToolCall, call agent.Classified) string {
	mut, mutErr := r.un.seen.PreviewMutation(tc.Name, json.RawMessage(tc.Arguments))
	edit := call.Action
	if mutErr == nil {
		edit.Path = mut.Path
	}
	reason, took, ok := r.answer(tc, edit, r.opts.yes, observe.ReasonHeadlessYes,
		"file modification", "edits by default (run with --yes)")
	if !ok {
		return reason
	}
	if mutErr == nil {
		if deny, ok := headlessScopeCheck(r.rules.Scope, r.opts.yes, []string{mut.Path}); !ok {
			// No command on the line, and the path is not put on one: a
			// refusal for what a call reaches is a refusal about a path, and
			// this file is shared and outlives every session that writes to
			// it.
			r.refuse(tc, "", observe.ReasonOutOfScope)
			return deny
		}
	}
	r.noteTook(observe.DecisionAllow, reason, took)
	result := agent.ExecuteWith(r.un.seen.ExecuteMutating, tc)
	if r.mutationHook != nil {
		result = r.mutationHook(tc.Name, json.RawMessage(tc.Arguments), result)
	}
	return r.red.Process(tc.Name, result)
}

// headlessCommandFlags is what the run's own flags say about one command:
// --yes is the blanket answer and --allow is the list, and the reason code
// says which of the two answered so the record can tell a run that was waved
// through from one that matched a shape the person wrote down.
func headlessCommandFlags(opts printOpts, allowlist []string, command string) (bool, string) {
	switch {
	case opts.yes:
		return true, observe.ReasonHeadlessYes
	case agent.AllowlistMatches(allowlist, command):
		return true, observe.ReasonAllowlist
	}
	return false, ""
}

// unattendedRules is the standing rules of a run with nobody in front of it:
// the two deny lists from the configuration, and the run's containment — its
// working scope and the refusal it gives every command where it was told to
// contain them and cannot. It is the screen's assembly, built from the same
// facts the screen's policy is built from, so a rule added for one door is
// answered at the other. A scripted run and a served session both build their
// approvers on it, and a surface added beside them starts here.
// See docs/capabilities/approvals-and-safety.md#a-deny-list-is-answered-before-anything-can-allow.
func unattendedRules(cfg config.Config, sc *scope.Scope, containment string) approval.Router {
	return approval.Router{
		Denylist:    cfg.Behavior.CommandDenylist,
		DenyHosts:   cfg.Web.DenyHosts,
		Scope:       sc,
		Containment: containment,
	}
}

// headlessAllowlist is the commands a run's flags pre-approve: the
// configuration's allowlist, and what --allow added to it for this run.
func headlessAllowlist(cfg config.Config, opts printOpts) []string {
	return append(append([]string{}, cfg.Behavior.CommandAllowlist...), opts.allow...)
}

// onlyRegistered answers a call naming a tool this run never offered the way
// the dispatch chain answers one, rather than letting the approver behind it
// decide.
//
// The approver decides by tier: a name that mutates is a mutation and a
// command is a command, wherever the name came from and whether or not
// anything registered it. That is the right rule for a surface that
// registered the whole toolset and the wrong one for a surface that
// registered part of it — a conversation has no editor and no runner, and a
// run of one with --yes would otherwise write a file through a name it never
// offered. The model is told what it has (prompt.Toolbox), so a call to
// something else is a mistake either way; this decides which mistake it is,
// and the answer is the one the dispatch chain gives a name it does not know,
// so the model reads the same sentence whichever side of the gate the name
// fell on. That sentence names what this surface did register, which is the
// half the model cannot work out for itself: told only that `write_file` is
// unknown, a conversation's model reaches for `edit_file` next.
// See docs/capabilities/chat.md#chat-changes-nothing.
func onlyRegistered(defs []provider.Tool, next func(provider.ToolCall) string) func(provider.ToolCall) string {
	offered := make(map[string]bool, len(defs))
	names := make([]string, 0, len(defs))
	for _, d := range defs {
		offered[d.Name] = true
		names = append(names, d.Name)
	}
	return func(tc provider.ToolCall) string {
		if !offered[tc.Name] {
			return "error: " + tools.UnknownTool(tc.Name, names).Error()
		}
		return next(tc)
	}
}
