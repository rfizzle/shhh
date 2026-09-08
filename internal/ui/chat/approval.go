package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/ask"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/digest"
	"github.com/rfizzle/shhh/internal/hook"
	"github.com/rfizzle/shhh/internal/memory"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/process"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// GatedPreview describes what an approval-gated tool call is about to do, for
// the confirm prompt. When Path is set, OldText/NewText are shown as a
// colored unified diff; otherwise Summary is shown as a generic preview.
type GatedPreview struct {
	Action  string // short verb for the title, e.g. "write", "edit"
	Path    string
	OldText string
	NewText string
	Summary string
	// Fields is the tool's own blast-radius block — a fetch states
	// its domain and what it sends, a spawn states the scope it claims. shhh
	// cannot resolve these from the arguments the way it resolves a shell
	// command's paths, so the tool that owns them supplies them.
	Fields []GatedField
	// Write puts the call at the write tier rather than the command tier: it
	// proceeds where an edit proceeds, is asked where an edit is asked, and
	// is refused in plan mode. It is for a tool that changes the machine
	// without writing a file to it — the git writer is the one — and it is
	// the tool's own statement rather than a guess, because nothing here can
	// read a tier out of a schema.
	Write bool
	// Title is the card's headline where the tool's name is not the act —
	// `commit 3 files` rather than `use git_write`. Empty keeps the name.
	Title string
	// DenyLine is the command line this call stands for, matched against the
	// deny list before anything can allow it. A tool with a closed verb set
	// stands for a line the person may already have refused, and a tool that
	// did not say so would be the way around the list.
	DenyLine string
	// Host is the host an outbound request leaves for, exactly as the card's
	// own field states it. It is what [a] grants and what the two host lists
	// are matched against, so the thing granted is the thing the reader read.
	// See docs/capabilities/approvals-and-safety.md#a-host-is-granted-once.
	Host string
}

// GatedField is one row of a tool's blast-radius block.
type GatedField struct {
	Label  string
	Value  string
	Detail string
	// Open marks a value that leaves something open — an outbound request, a
	// writable scope — so the card can colour it and rate the card by it.
	Open bool
}

// GatedPreviewFunc builds the confirm-prompt preview for one tool call's
// arguments. An error skips the call with an error tool result, mirroring how
// invalid execute_command arguments are handled.
type GatedPreviewFunc func(args json.RawMessage) (GatedPreview, error)

// approvalKind selects which preview the confirm prompt renders for a queued
// approval-gated tool call.
type approvalKind int

const (
	approvalExec    approvalKind = iota // one-line command + safety warnings
	approvalDiff                        // colored unified diff of a file write/edit
	approvalGeneric                     // one-line summary of the tool call
	approvalMemory                      // memory proposal: scope selector with optional note
	// approvalQuestion is the model's own question, which is a decision
	// rather than an approval: it stops the turn and it is put to the
	// person, but nothing about the machine changes either way
	// (question.go).
	approvalQuestion
)

// approvalRequest is the head of the approval queue: one tool call awaiting
// the user's decision, with everything needed to preview and execute it.
type approvalRequest struct {
	call    provider.ToolCall
	kind    approvalKind
	command string      // approvalExec: the command handed to the runner; also set for a process start so mode policy treats it as a command
	title   string      // action headline, e.g. "edit main.go"
	verb    string      // approvalDiff: the action verb, e.g. "edit"
	path    string      // approvalDiff: the file being modified
	hunks   []diff.Hunk // approvalDiff: the change to show
	summary string      // one-line description for transcript entries
	// fields is a gated tool's own blast-radius block, from its
	// GatedPreview.
	fields []GatedField
	// write marks a generic approval that sits at the write tier, from its
	// GatedPreview: mode policy answers it the way it answers an edit.
	write bool
	// host is the host a generic approval's outbound request leaves for,
	// from its GatedPreview: what [a] grants and what the host lists answer.
	host string
	// autoRule names what approved this call on the reader's behalf — the
	// mode or grant that allowed it, "classifier", or the batch — and is
	// empty on a call the reader answered at the card. It rides the request
	// to the act's own row, which is where the account is stated, and it is
	// what the changeset record's origin is taken from.
	autoRule string
	// autoCost is what that judgement took, where it took anything, which is
	// the classifier and nothing else.
	autoCost time.Duration
	// memoryDraft is the proposed entry for approvalMemory.
	memoryDraft memory.Draft
	// question is the parsed question for approvalQuestion — the call's first
	// where it carried several, which is the one the card opens on.
	question ask.Question
	// sheet is the rest of them, where an approvalQuestion carried more than
	// one: the tabs, the answers gathered so far, and which tab has the
	// keyboard (question.go). It rides the request for the reason the dry
	// run's own two fields do — it is a fact about this call — and because it
	// has to outlive the card, which esc puts down with the answers already
	// on it. Nil is a call that asked one question.
	sheet *questionSheet
	// mustAsk is a hook in front of this call having asked for it, or having
	// failed on a call there is somebody to ask about. It out-ranks the batch
	// approval, the mode and the classifier, all of which answer the question
	// the hook has just declined to (hooks.go).
	mustAsk bool
	// hookContext is what a hook in front of this call wanted the model to
	// read. It leads the result, where every other notice goes.
	hookContext string
	// dryCommand is the harmless form of an exec card's command, for the
	// commands that have one, and dryRunning whether it is running right now
	// (run.go). Both ride the request rather than the model because they are
	// facts about this call: the next decision in the queue is a different
	// command with a different answer, and one left behind on the model
	// would be advertised over it.
	dryCommand string
	dryRunning bool
	// amendedFrom is the line the call carried, on a request whose command
	// the reader has rewritten (amend.go). It is what the card's `was` row
	// prints, what the model is told ran in place of what it asked for, and
	// what marks the transcript row as the reader's line rather than the
	// model's. Empty on every request nobody amended.
	amendedFrom string
	// explaining is whether a paragraph about this command is being read
	// right now (run.go). It rides the request for the reason the dry run's
	// two fields do: the answer is about this call, and one left behind on
	// the model would be advertised over the next decision in the queue.
	explaining bool
}

// approvedToolDoneMsg carries the executor result of an approved non-exec
// tool call.
type approvedToolDoneMsg struct {
	runID    int
	result   string
	duration time.Duration
	// evicted names the turns the changeset write dropped to stay inside its
	// bound, so the session can say so rather than losing them silently.
	evicted []int64
}

// MutationHook post-processes an applied file-modification tool result before
// it is reduced and recorded — the LSP integration uses it to append
// fresh diagnostics for the touched file so the model can self-correct in the
// same round. It runs off the UI goroutine and must return promptly (its own
// waits are bounded).
type MutationHook func(name string, args json.RawMessage, result string) string

// WithMutationHook installs the post-mutation result hook.
func (m Model) WithMutationHook(hook MutationHook) Model {
	m.mutationHook = hook
	return m
}

// WithGatedTools registers tools that must be approved by the user before
// they run through the tool executor; each entry builds the confirm-prompt
// preview for its tool. Gated tools never run via the auto-run path.
func (m Model) WithGatedTools(previews map[string]GatedPreviewFunc) Model {
	m.gatedTools = previews
	return m
}

// requiresApproval reports whether a tool call must go through the approval
// queue instead of the auto-run executor path. File-modification tools are
// always gated, mirroring how execute_command is intercepted.
func (m Model) requiresApproval(tc provider.ToolCall) bool {
	if tc.Name == tools.ExecCommandName && m.runFn != nil {
		return true
	}
	if tools.IsMutating(tc.Name) {
		return true
	}
	// remember is always gated: agent-proposed memories persist only
	// after explicit user confirmation.
	if tc.Name == memory.RememberToolName {
		return true
	}
	// And a question always stops the turn, because a question that ran
	// without stopping would be a question nobody answered (question.go).
	// Only where the session handed the model the tool: a call to a tool
	// this run does not have is answered as one and never put on a card.
	if tc.Name == ask.ToolName && m.asks {
		return true
	}
	// The process tool gates on its arguments: start launches a
	// command and needs approval; status/read/input/stop auto-run.
	if m.processes.Manage != nil && tc.Name == process.ToolName {
		return process.NeedsApproval(json.RawMessage(tc.Arguments))
	}
	_, ok := m.gatedTools[tc.Name]
	return ok
}

// buildApprovalRequest turns a queued tool call into its confirm prompt.
func (m Model) buildApprovalRequest(tc provider.ToolCall) (*approvalRequest, error) {
	if tc.Name == tools.ExecCommandName {
		var args struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil || strings.TrimSpace(args.Command) == "" {
			return nil, fmt.Errorf("invalid command arguments")
		}
		return &approvalRequest{
			call:    tc,
			kind:    approvalExec,
			command: args.Command,
			summary: firstLine(args.Command),
			// The harmless form of the command, where there is one. It is
			// derived here with everything else the card states about the
			// call, not at render: the derivation reads the whole command
			// line, and the card is rebuilt every frame.
			dryCommand: dryRunForm(args.Command),
		}, nil
	}

	// Memory proposals get the scope-selector prompt, never a generic card.
	if tc.Name == memory.RememberToolName {
		return m.buildMemoryApproval(tc)
	}

	// A question gets the question card, never a generic one: the card is
	// the question and its answers, and "use ask" would be a card about the
	// mechanism (question.go).
	if tc.Name == ask.ToolName {
		return m.buildQuestionApproval(tc)
	}

	// A process start is approved like a command: the card shows the
	// command text, and mode policy treats it as one (allowlist, safety).
	if m.processes.Manage != nil && tc.Name == process.ToolName {
		name, command, err := process.StartSummary(json.RawMessage(tc.Arguments))
		if err != nil {
			return nil, err
		}
		title := "start process " + name
		return &approvalRequest{
			call:    tc,
			kind:    approvalGeneric,
			title:   title,
			command: command,
			summary: title + ": " + firstLine(command),
		}, nil
	}

	// A registered preview overrides the built-in mutating-tool handling.
	preview, ok := m.gatedTools[tc.Name]
	if !ok {
		if !tools.IsMutating(tc.Name) {
			return nil, fmt.Errorf("tool %s cannot be approved in this session", tc.Name)
		}
		mut, err := tools.PreviewMutation(tc.Name, json.RawMessage(tc.Arguments))
		if err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		title := mut.Action + " " + mut.Path
		return &approvalRequest{
			call:    tc,
			kind:    approvalDiff,
			title:   title,
			verb:    mut.Action,
			path:    mut.Path,
			hunks:   diff.Compute(mut.OldText, mut.NewText),
			summary: title,
		}, nil
	}
	p, err := preview(json.RawMessage(tc.Arguments))
	if err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if p.Path != "" {
		action := p.Action
		if action == "" {
			action = "modify"
		}
		title := action + " " + p.Path
		return &approvalRequest{
			call:    tc,
			kind:    approvalDiff,
			title:   title,
			verb:    action,
			path:    p.Path,
			hunks:   diff.Compute(p.OldText, p.NewText),
			summary: title,
			fields:  p.Fields,
		}, nil
	}
	summary := p.Summary
	if summary == "" {
		summary = digest.FormatArgs(tc.Arguments)
	}
	// The tool's own headline where it wrote one: `use git_write` names the
	// mechanism, and a card asks about an act.
	title := "use " + tc.Name
	if p.Title != "" {
		title = p.Title
	}
	return &approvalRequest{
		call:    tc,
		kind:    approvalGeneric,
		title:   title,
		command: p.DenyLine,
		summary: summary,
		fields:  p.Fields,
		write:   p.Write,
		host:    p.Host,
	}, nil
}

// advanceApprovalQueue shows the confirm prompt for the next queued
// approval-gated tool call, or resumes the model stream once the queue is
// empty.
func (m Model) advanceApprovalQueue() (tea.Model, tea.Cmd) {
	tc, ok := m.agent.NextApproval()
	if !ok {
		return m.resumeToolLoop()
	}
	req, err := m.buildApprovalRequest(tc)
	if err != nil {
		m.agent.ResolveApproval(m.refusedResult(tc, "error: "+err.Error()))
		m.appendEntry(m.skippedCallEntry(tc.Name, err))
		m.viewport.SetLines(m.renderHistoryLines())
		m.viewport.GotoBottom()
		return m.advanceApprovalQueue()
	}
	// A session that requires containment answers a command here, before
	// anything is drawn: the card exists to put a decision to the reader, and
	// there is no decision left when the answer is the same whichever key
	// they press. The model reads the refusal as the call's result, which is
	// where it can act on it.
	if refusal := m.containmentRefusal(req); refusal != "" {
		m.agent.ResolveApproval(m.refusedResult(req.call, refusal))
		m.appendEntry(entry{
			kind: entrySystem,
			text: "Refused — nothing is containing commands in this session: " + req.summary,
			// The expansion is what the model was told, verbatim, including
			// the fix for this host: the reader is the one who can act on it.
			toolResult: refusal,
		})
		m.viewport.SetLines(m.renderHistoryLines())
		m.viewport.GotoBottom()
		return m.advanceApprovalQueue()
	}
	// A command the deny list names is answered here too, and for the same
	// reason: the list is the user's standing answer, so there is no card to
	// draw, no earlier [A] that reaches it and no classifier round to spend.
	// The row is the rule-denial row rather than a notice, because a denial
	// is a moment that mattered and the reader's next act depends on knowing
	// a rule and not a person refused it.
	if m.deniedByRule(req) {
		result, reason, why := m.ruleDenial(req)
		m.recordDecision(observe.DecisionDeny, observe.ReasonDenylist)
		m.lastDenial = req.summary + " — " + why
		// Surfaces on the notice rail until the next user turn.
		m.denialNotice = req.summary
		m.agent.ResolveApproval(m.refusedResult(req.call, result))
		m.appendEntry(deniedEntry(req, decidedByAuto, reason, 0))
		m.viewport.SetLines(m.renderHistoryLines())
		m.viewport.GotoBottom()
		return m.advanceApprovalQueue()
	}
	// And the person's own commands, at the seam in front of the decision.
	// They run off the UI goroutine like the classifier and for the same
	// reason: a hook is a command, and a card that froze while one ran would
	// be the session stopping for something nobody is watching.
	if m.hooks.Has(hook.PreTool, req.call.Name) {
		return m.startPreToolHook(req)
	}
	return m.armApprovalDecision(req)
}

// armApprovalDecision is everything the queue does once the standing answers
// have had their say: the card's own reading of the call, then the decision.
// It is its own function because the hook seam above is asynchronous, and the
// answer that comes back has to re-enter the queue exactly where it left —
// not at the top, where the containment refusal and the deny list would be
// asked a second time about a call they have already passed.
func (m Model) armApprovalDecision(req *approvalRequest) (tea.Model, tea.Cmd) {
	m.pendingApproval = req
	if req.kind == approvalExec {
		m.pendingRun = req.command
	}
	// What the decision reaches outside the working scope: resolved
	// before the blast radius because the radius block carries it as a row —
	// the card names it, the policy asks about it, and approving the call
	// grants it.
	m.pendingScope = m.scopeReachFor(req)
	// The blast radius is resolved once here, not inside View: it stats the
	// filesystem and asks git about the paths it found.
	m.pendingBlast = m.resolveRadius(req)
	// A question is put to the person in every mode, because every gate
	// below this one exists to decide which *acts* stop to ask — and a
	// question is not an act. A --yes, a session grant, accept-edits and the
	// classifier would each be answering "which of these three designs" at
	// random, so none of them is given the chance
	// (docs/capabilities/coding-agent.md#the-model-can-ask). Plan mode is no
	// exception: asking changes nothing, so there is nothing to refuse. The
	// turn's own budget for questions is the one thing that answers one, and
	// it is spent here where the question is asked (question.go).
	if req.kind == approvalQuestion {
		return m.armQuestion(req)
	}
	// Agent-proposed memories always require explicit user
	// confirmation: no mode, session grant, or classifier can wave one
	// through. Plan mode falls through to the policy below, which refuses the
	// write like any other.
	if req.kind == approvalMemory && m.policy.mode != agent.ModePlan {
		m.recordDecision(observe.DecisionAsk, observe.ReasonMemory)
		m.openMemoryAsk(req)
		m.armConfirm(req)
		return m, nil
	}
	// A hook that asked is a card and nothing else. It sits above the batch
	// approval for the reason the deny list sits above it: an answer given
	// earlier to a different call is not an answer to this one, and a hook
	// that wanted this call seen is asking for exactly that.
	if req.mustAsk {
		m.recordDecision(observe.DecisionAsk, observe.ReasonHook)
		m.armConfirm(req)
		return m, nil
	}
	// A decision the queue list already answered is carried out when its turn
	// comes, without asking again: allowed, it runs; denied, it takes the
	// path the reader's own no takes on a card of its own (queue.go).
	if allow, answered := m.takeQueueAnswer(req); answered {
		if !allow {
			return m.declineApproval()
		}
		m.recordDecision(observe.DecisionAllow, observe.ReasonUserBatch)
		req.autoRule = batchRule
		if req.kind == approvalExec {
			return m.executeRun()
		}
		return m.executeApprovedTool()
	}
	// Mode policy: the permissive modes and session
	// grants skip the prompt, plan mode refuses the call outright, and
	// safety-flagged commands always prompt.
	switch decision, reason := m.policyDecision(req); decision {
	case agent.Allow:
		m.recordDecision(observe.DecisionAllow, observe.ReasonCode(reason))
		req.autoRule = reason
		if req.kind == approvalExec {
			return m.executeRun()
		}
		return m.executeApprovedTool()
	case agent.Deny:
		m.recordDecision(observe.DecisionDeny, observe.ReasonCode(reason))
		m.pendingApproval = nil
		m.pendingRun = ""
		m.pendingScope = scopeReach{}
		m.agent.ResolveApproval(m.refusedResult(req.call, denialResult(reason)))
		m.appendEntry(deniedEntry(req, decidedByAuto, reason, 0))
		m.viewport.SetLines(m.renderHistoryLines())
		m.viewport.GotoBottom()
		return m.advanceApprovalQueue()
	}
	// In auto mode the classifier judges what the static policy would
	// ask about — except safety-flagged actions, which always prompt the human.
	if act := m.approvalAction(req); m.policy.mode == agent.ModeAuto && m.classifier != nil &&
		!act.SafetyFlagged && !act.ScopeSensitive {
		return m.startClassifierCheck(req)
	}
	m.recordDecision(observe.DecisionAsk, observe.AskReason(m.approvalAction(req)))
	m.armConfirm(req)
	return m, nil
}

// startPreToolHook runs the hooks in front of a gated call in the
// background; what they came to arrives as preToolHookMsg.
//
// The state is the one a running command wears, because that is what this is:
// the session is holding a decision while a command of the person's own runs.
func (m Model) startPreToolHook(req *approvalRequest) (tea.Model, tea.Cmd) {
	m.setTurnState(stateRunningCmd)
	m.syncViewport()
	hooks := m.hooks
	runID := m.agent.RunID()
	at := m.hookPos()
	call := hook.Call{ID: req.call.ID, Name: req.call.Name, Arguments: req.call.Arguments}
	return m, func() tea.Msg {
		return preToolHookMsg{runID: runID, req: req,
			verdict: hooks.PreTool(context.Background(), at, call, true)}
	}
}

// finishPreToolHook applies what the hooks said to the pending decision: a
// refusal is the rule-denial row, a rewrite rebuilds the card from the
// arguments that will actually run, and everything else carries on to the
// decision that was always going to be made.
func (m Model) finishPreToolHook(msg preToolHookMsg) (tea.Model, tea.Cmd) {
	req, v := msg.req, msg.verdict
	m.hookNotes(v)
	if v.Denied() {
		m.recordDecision(observe.DecisionDeny, observe.ReasonHook)
		m.lastDenial = req.summary + " — " + hookWhy(v.Reason)
		// Surfaces on the notice rail until the next user turn.
		m.denialNotice = req.summary
		m.agent.ResolveApproval(m.refusedResult(req.call, hook.DeniedResult(v.Reason)))
		m.appendEntry(deniedEntry(req, decidedByAuto, hook.DenyRule(v.Reason), 0))
		m.viewport.SetLines(m.renderHistoryLines())
		m.viewport.GotoBottom()
		return m.advanceApprovalQueue()
	}
	if v.Input != nil {
		// The card is built again from the arguments that will run, so what
		// the reader is shown is what will happen and not what was asked
		// for. A rewrite the preview cannot read is the call skipped with
		// the error, exactly as invalid arguments from the model are.
		call := req.call
		call.Arguments = string(v.Input)
		rebuilt, err := m.buildApprovalRequest(call)
		if err != nil {
			m.agent.ResolveApproval(m.refusedResult(call, "error: "+err.Error()))
			m.appendEntry(m.skippedCallEntry(call.Name, err))
			m.viewport.SetLines(m.renderHistoryLines())
			m.viewport.GotoBottom()
			return m.advanceApprovalQueue()
		}
		req = rebuilt
	}
	// Both are the hooks' to set and nothing else's: a request reaches this
	// function straight from the queue, so there is nothing here to carry
	// over from.
	req.mustAsk, req.hookContext = v.Asked(), v.Context
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoBottom()
	return m.armApprovalDecision(req)
}

// startClassifierCheck sends the pending approval to the auto-mode permission
// classifier in the background; the verdict arrives as
// classifierDoneMsg.
func (m Model) startClassifierCheck(req *approvalRequest) (tea.Model, tea.Cmd) {
	m.setTurnState(stateClassifying)
	m.syncViewport()
	ctx, cancel := context.WithCancel(context.Background())
	m.classifierCancel = cancel
	classifier := m.classifier
	runID := m.agent.RunID()
	cwd, _ := os.Getwd()
	creq := agent.ClassifierRequest{
		Tool:      req.call.Name,
		Arguments: req.call.Arguments,
		CWD:       cwd,
		Recent:    m.agent.RequestMessages(),
	}
	return m, func() tea.Msg {
		return classifierDoneMsg{runID: runID, verdict: classifier.Judge(ctx, creq)}
	}
}

// finishClassifierCheck applies the classifier's verdict to the pending
// approval: allow executes it, deny refuses it with the reason as the tool
// result, and a failed check falls back to asking the user (fail closed).
func (m Model) finishClassifierCheck(v agent.ClassifierVerdict) (tea.Model, tea.Cmd) {
	// The verdict was billed at the provider gate as it streamed, so there is
	// nothing to add here — only the observer to nudge, because a judgement
	// the session paid for is a change to what the session has spent.
	m.notifyUsage()

	req := m.pendingApproval
	switch decision, reason := agent.ResolveAuto(m.approvalAction(req), v); decision {
	case agent.Allow:
		m.recordDecision(observe.DecisionAllow, observe.ReasonClassifier)
		req.autoRule, req.autoCost = classifierRule, v.Elapsed
		if req.kind == approvalExec {
			return m.executeRun()
		}
		return m.executeApprovedTool()
	case agent.Deny:
		m.recordDecision(observe.DecisionDeny, observe.ReasonClassifier)
		m.lastDenial = req.summary + " — " + reason
		// Surfaces on the notice rail until the next user turn.
		m.denialNotice = req.summary
		m.pendingApproval = nil
		m.pendingRun = ""
		m.pendingScope = scopeReach{}
		m.agent.ResolveApproval(m.refusedResult(req.call, denialResult(reason)))
		// The row names the rule, not the judgement: every other rule denial
		// states a rule the reader can change, and the classifier's sentence
		// is neither short enough for the outcome column nor a thing to
		// change. It is on the notice rail and behind the row's own
		// `/permissions why`, which is where a reason belongs
		// (docs/interface/principles.md#closed-vocabularies).
		m.appendEntry(deniedEntry(req, decidedByAuto, classifierRule, v.Elapsed))
		m.viewport.SetLines(m.renderHistoryLines())
		m.viewport.GotoBottom()
		return m.advanceApprovalQueue()
	}
	// Ask: the classifier failed closed or the safety backstop fired — the
	// user decides, never a silent allow.
	if v.Failed {
		m.recordDecision(observe.DecisionAsk, observe.ReasonClassifierFailed)
		m.appendEntry(entry{kind: entrySystem, text: "Classifier unavailable (" + v.Reason + "); asking you instead."})
		m.viewport.SetLines(m.renderHistoryLines())
		m.viewport.GotoBottom()
	} else {
		m.recordDecision(observe.DecisionAsk, observe.ReasonSafety)
	}
	m.armConfirm(req)
	return m, nil
}

// refusedResult is a gated call that never ran, as the model reads it. Every
// way this session refuses one goes through it, so the repeat detector sees
// the whole tier the way the unattended drivers' single wrapped approver does
// — an edit refused for the same reason twice is told it has been refused
// before, rather than proposed a third time. That includes a decline the
// reader typed: the row that says so is on their screen and not in the
// conversation, and the model reads only the result (repeat.go).
func (m *Model) refusedResult(tc provider.ToolCall, content string) string {
	out := m.repeats.Notice(tc.Name, json.RawMessage(tc.Arguments), content)
	if agent.IsRepeatNotice(out) {
		m.signal(observe.SignalRepeat, tc.Name)
	}
	return out
}

// denialResult is the tool result for a call the session refused without
// asking: plan mode's own sentence, the scope's when the path is one no grant
// can reach, and the classifier's reason otherwise.
func denialResult(reason string) string {
	switch {
	case reason == agent.DenyReasonDenylist:
		return agent.DenylistResult
	case reason == agent.DenyReasonHost:
		return agent.DeniedHostResult
	case reason == "plan mode":
		return agent.PlanModeResult
	case strings.HasPrefix(reason, "outside the working scope"):
		return agent.ScopeRefusedResult(reason)
	}
	return "error: auto mode denied this tool call: " + reason
}

// applyScopeGrant records the pending decision's out-of-scope directories in
// the working scope and says so in the transcript. It runs on the one path
// every approval takes on its way to executing, so no key has to remember to
// call it and none of them can widen the scope without saying so.
func (m *Model) applyScopeGrant() {
	reach := m.pendingScope
	m.pendingScope = scopeReach{}
	if note := m.grantScope(reach); note != "" {
		m.noteGrant(note)
	}
}

// decisionNote is the note field a decision card's shifted answer opened: the
// answer it will carry, and the field it is being written in
// (docs/capabilities/approvals-and-safety.md#a-no-can-say-why-and-a-yes-can-say-what-next).
//
// The field is bubbles' own one-line input rather than a component of its
// own, for the reason every other field in the product is (components/input.go):
// what is shared is how a field is built and repainted, not what it is.
type decisionNote struct {
	// allow is which of the two answers the sentence goes out with.
	allow bool
	field textinput.Model
}

// drawn is the field as the card will draw it: sized to the room the card
// leaves it and repainted from the palette as it stands now (input.go).
//
// Both the render and the cursor go through here rather than one of them
// reading the stored field: the field's own caret is clamped to its width, so
// asking an unsized copy where the caret is puts it past the card's right
// edge the moment the sentence outgrows the row.
func (n decisionNote) drawn(width int) textinput.Model {
	field := n.field
	field.SetWidth(components.FieldWidth(width))
	components.StyleTextInput(&field)
	return field
}

// openDecisionNote opens the field under the card. Nothing is decided by
// opening it: the answer the key stands for is given when the field is
// confirmed, and esc closes it with the decision still waiting, because a key
// that turned a hesitation into an answer would be a key nobody could afford
// to press (docs/interface/principles.md#esc-is-always-the-safe-answer).
func (m Model) openDecisionNote(allow bool) (tea.Model, tea.Cmd) {
	field := components.NewTextInput()
	field.Prompt = ""
	// The terminal's own cursor rather than a painted one: this session
	// places a real cursor wherever it is being typed into (confirmCursor),
	// and a field painting a second one would draw two.
	field.SetVirtualCursor(false)
	cmd := field.Focus()
	m.decisionNote = &decisionNote{allow: allow, field: field}
	m.syncViewport()
	return m, cmd
}

// updateDecisionNote routes a key while the field holds the keyboard. Two keys
// are the whole of what it answers and every other key is text — the digits,
// the card's own letters and its scroll chords included — because a surface
// being typed into keeps every letter as text, the way the selector's query
// row and the transcript search already do
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
func (m Model) updateDecisionNote(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	open := *m.decisionNote
	switch {
	case keys.Match(msg, keys.Select.Cancel):
		// Back to the card with the decision exactly where it was. The
		// sentence goes with the field: a draft nobody sent is not an answer,
		// and keeping it would put words the reader abandoned on the next
		// answer they give.
		m.decisionNote = nil
		m.syncViewport()
		return m, nil
	case keys.Match(msg, keys.Select.Take):
		note := strings.TrimSpace(open.field.Value())
		m.decisionNote = nil
		if open.allow {
			return m.approvePending(note)
		}
		return m.declineApprovalWith(note)
	}
	// The field is replaced rather than written through the pointer: the
	// model is a value every update hands back a copy of, and a shared field
	// would put this keystroke into the copy the last frame was drawn from.
	var cmd tea.Cmd
	open.field, cmd = open.field.Update(msg)
	m.decisionNote = &open
	m.syncViewport()
	return m, cmd
}

// approvePending is the allow, with the reader's sentence if they wrote one.
//
// The sentence goes out through the steering channel a message typed while
// the turn works already uses (stream.go): it has the gutter mark, it has the
// notice-rail count, and it is already understood as "change what you are
// doing", so a second channel spelled differently would be a second answer to
// the same question. It is not machine-authored — the reader wrote it — so it
// resets the round count and joins the conversation as their own words, which
// is what a steer typed into the draft a second later would have done.
func (m Model) approvePending(note string) (tea.Model, tea.Cmd) {
	if note != "" {
		m.steering = append(m.steering, steeringItem{text: note})
	}
	if m.pendingApproval != nil {
		m.recordDecision(observe.DecisionAllow, observe.ReasonUser)
	}
	if m.pendingApproval != nil && m.pendingApproval.kind != approvalExec {
		return m.executeApprovedTool()
	}
	return m.executeRun()
}

// declineApproval records an error tool result for the pending call and moves
// on to the next queued approval.
func (m Model) declineApproval() (tea.Model, tea.Cmd) { return m.declineApprovalWith("") }

// declineApprovalWith is the same decline carrying the reader's own sentence,
// which is the whole of what the model is told
// (docs/capabilities/approvals-and-safety.md#a-no-can-say-why-and-a-yes-can-say-what-next).
//
// The three fixed sentences below say only that the call was refused, which
// the reader's sentence says better and more usefully: a result carrying both
// would make the model read past the boilerplate to reach the correction, and
// the exec and memory variants would each add a second way of saying no. What
// stays is the tier marker every refusal in the product carries, so a refusal
// is still a refusal on the wire and not output the call produced.
//
// An empty sentence is not a sentence, and takes the fixed one unchanged: a
// reader who pressed the shifted letter and pressed enter meant the plain
// answer, and the model must not be able to tell the two paths apart.
func (m Model) declineApprovalWith(note string) (tea.Model, tea.Cmd) {
	m.recordDecision(observe.DecisionDeny, observe.ReasonUser)
	req := m.pendingApproval
	m.pendingApproval = nil
	m.pendingRun = ""
	m.pendingScope = scopeReach{}
	content := "error: the user declined this tool call"
	switch req.kind {
	case approvalExec:
		content = "error: the user declined to run this command"
	case approvalMemory:
		content = "error: the user declined to save this memory; do not re-propose it this session"
	}
	if note != "" {
		content = "error: " + note
	}
	m.agent.ResolveApproval(m.refusedResult(req.call, content))
	m.appendEntry(deniedEntry(req, decidedByYou, "", 0).withDenyNote(note))
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoBottom()
	return m.advanceApprovalQueue()
}

// deniedEntry is the transcript row for a refused call: the same activity row
// every other call gets, with ⊘, the decider's name and a duration field
// saying it never ran (docs/interface/principles.md#closed-vocabularies). A
// denial is a moment that mattered, so the row keeps its mutation rail —
// which is why it is a row and not a system notice
// (docs/interface/principles.md#weight-tracks-risk).
func deniedEntry(req *approvalRequest, decider, rule string, elapsed time.Duration) entry {
	return entry{
		kind:     entryTool,
		toolName: req.call.Name,
		toolArgs: req.call.Arguments,
		deniedBy: decider,
		denyRule: rule,
		duration: elapsed,
	}
}

// withDenyNote folds the reader's sentence under the denied row rather than
// into its outcome field: the outcome column states who refused, in the
// closed vocabulary every row's outcome comes from, and a sentence clipped
// into it would be a reason nobody could read
// (docs/interface/principles.md#fold-never-hide). It is the row's body, so it
// opens with the row and is carried verbatim.
//
// Only a reader's denial can have one. A rule's denial goes through its own
// path with its own code and has nothing to say beyond which rule it was
// (docs/capabilities/approvals-and-safety.md#denials-are-two-different-facts).
func (e entry) withDenyNote(note string) entry {
	e.denyNote = note
	return e
}

// executeApprovedTool runs an approved non-exec tool call through the tool
// executor in the background; the result arrives as approvedToolDoneMsg.
func (m Model) executeApprovedTool() (tea.Model, tea.Cmd) {
	// An approved call that reaches outside the working scope puts what it
	// reaches into the scope: the approval is the answer to "is this
	// directory part of the work", and an answer the scope did not record is
	// one the next call would ask again.
	m.applyScopeGrant()
	m.setTurnState(stateRunningCmd)
	m.syncViewport()
	a := m.agent
	runID := a.RunID()
	call := m.pendingApproval.call
	// Built-in mutating tools run through their own dispatcher; the session
	// executor (the auto-run read-only path) never learns them. A registered
	// gated tool keeps the session executor. The session executor is already
	// wrapped by the reduction pipeline, so only the direct mutating
	// dispatch reduces here.
	_, registered := m.gatedTools[call.Name]
	mutating := !registered && tools.IsMutating(call.Name)
	reduce := m.evidence.Reduce
	mutated := m.mutationHook
	// A registered gated tool goes back through the session executor, which
	// the detector already wraps, so only the direct mutating dispatch is
	// noted here — the same reason the reduction is applied only here.
	// It is applied last, where the executor's wrapper sits: outside the
	// reduction, so the notice leads the result the model is really handed.
	repeats := m.repeats
	if !mutating {
		repeats = nil
	}
	// The changeset record is taken around the call, on this goroutine: the
	// file as it is now, then the file the call leaves behind. Both
	// reads happen next to the write, so a file that changed underneath the
	// approval preview is recorded as it really was, not as it was previewed.
	record := m.changeRecorder()
	// What a hook in front of this call wanted the model to read leads the
	// result, where every other notice goes (hooks.go).
	lead := m.pendingApproval.hookContext
	return m, func() tea.Msg {
		var result string
		before := record.before()
		start := time.Now()
		if mutating {
			result = agent.ExecuteWith(tools.ExecuteMutating, call)
			if mutated != nil {
				result = mutated(call.Name, json.RawMessage(call.Arguments), result)
			}
			if reduce != nil {
				result = reduce(call.Name, result)
			}
			result = repeats.Notice(call.Name, json.RawMessage(call.Arguments), result)
		} else {
			result = a.ExecuteCall(call)
		}
		if lead != "" {
			result = lead + "\n" + result
		}
		evicted := record.after(before)
		return approvedToolDoneMsg{runID: runID, result: result, duration: time.Since(start), evicted: evicted}
	}
}

// changeRecording captures one approved file modification for the changeset
// store. It is built on the UI goroutine and used on the executor's, which is
// why it carries plain values and a store that locks.
type changeRecording struct {
	store   *changeset.Store
	tracker *changeset.Tracker
	turn    int64
	path    string
	origin  changeset.Origin
}

// changeRecorder describes what this approval would record. A call with no
// file behind it (a command, a memory, a generic tool) records nothing.
func (m Model) changeRecorder() changeRecording {
	req := m.pendingApproval
	if req == nil || req.kind != approvalDiff || req.path == "" {
		return changeRecording{}
	}
	origin := changeset.Approved
	if req.autoRule != "" {
		origin = changeset.AutoApproved
	}
	return changeRecording{
		store:   m.changes,
		tracker: m.tracker,
		turn:    m.turnCount,
		path:    req.path,
		origin:  origin,
	}
}

// fileState is a file as it was at one moment: content, permissions, and
// whether it was there at all.
type fileState struct {
	text   string
	exists bool
	track  changeset.Tracking
	// mode is zero where there was no file to have permissions. The record
	// keeps it so undo can put a file the turn deleted back the way it found
	// it, executable bit included.
	mode os.FileMode
}

// before reads the file the approved call is about to modify, along with
// whether git knew about it — the input to the reversibility line elsewhere
// in the UI.
func (c changeRecording) before() fileState {
	if c.path == "" {
		return fileState{}
	}
	st := readFileState(c.path)
	st.track = c.tracker.Track(c.path)
	return st
}

// after records what the call actually left behind and returns the turns
// evicted by the write. The gate is the content, not the tool's own account
// of itself: a call that changed nothing records nothing, and a call that
// changed a file records it whatever it went on to report. Undo depends on
// the store knowing everything the workspace lost.
func (c changeRecording) after(before fileState) []int64 {
	if c.path == "" {
		return nil
	}
	now := readFileState(c.path)
	return c.store.Add(c.turn, changeset.Record{
		Path:         c.path,
		Before:       before.text,
		After:        now.text,
		BeforeExists: before.exists,
		AfterExists:  now.exists,
		BeforeMode:   before.mode,
		// The after side is read for the same reason the before side is: a
		// change of permissions and nothing else is still a change. The
		// tools here do not make one — an existing file keeps the mode it
		// has — so this records nothing today and records it the day one
		// does, rather than being the seam where a mode is lost again.
		AfterMode: now.mode,
		Agent:     changeset.MainAgent,
		Origin:    c.origin,
		Track:     before.track,
	})
}

// readFileState reads a file, reporting a missing one as absent rather than
// as an error: a write that creates a file has no before-content, and that is
// the fact the record needs.
func readFileState(path string) fileState {
	data, err := os.ReadFile(path)
	if err != nil {
		return fileState{}
	}
	st := fileState{text: string(data), exists: true}
	if fi, err := os.Stat(path); err == nil {
		// Permission bits only. Ownership and times are not the session's to
		// give back: no edit here ever changed them.
		st.mode = fi.Mode().Perm()
	}
	return st
}

// approvalCard assembles the components.ApprovalCard
// (docs/interface/surfaces.md#the-approval-card) for the pending approval or
// /run confirmation. Both the confirm prompt's rendering and its key handling
// flow through this one card. While the user is attached to a child, the
// orchestrator's own card is labeled so it is never mistaken for the focused
// agent's.
func (m Model) approvalCard() *components.ApprovalCard {
	card := m.buildApprovalCard()
	if m.attachedTo != "" {
		card.Title = "orchestrator ▸ " + card.Title
	}
	// Whether the card's keys are live at all is not the card's to decide
	// (invariant 5): it depends on which surface holds the keyboard.
	m.applyNotYetLive(card)
	m.applyDecisionNote(card)
	m.applyCommandEdit(card)
	m.applyGrantChoice(card)
	return card
}

// applyCommandEdit puts the open command field on the card. The field is the
// model's for the reason the note field is: the card is rebuilt every frame
// and what is being typed is not (amend.go).
func (m Model) applyCommandEdit(card *components.ApprovalCard) {
	e := m.commandEdit
	if e == nil {
		return
	}
	card.AmendOpen, card.AmendRefused = true, e.refused
	card.AmendField = e.drawn(m.contentWidth()).View()
}

// applyDecisionNote puts the open note field on the card. The offer itself is
// the card's own reading of what is waiting on the answer — there is nothing
// for a sentence to reach on a /run the reader typed, and a memory proposal
// answers on its own surface — and the field is the model's, because the card
// is rebuilt every frame and what is being typed is not.
func (m Model) applyDecisionNote(card *components.ApprovalCard) {
	card.Noted = m.pendingApproval != nil && m.memoryAsk == nil
	n := m.decisionNote
	if n == nil || !card.Noted {
		return
	}
	card.NoteOpen, card.NoteAllow = true, n.allow
	card.NoteField = n.drawn(m.contentWidth()).View()
}

func (m Model) buildApprovalCard() *components.ApprovalCard {
	card := &components.ApprovalCard{
		MaxLines: m.maxConfirmPanelHeight(),
		// The card is rebuilt every frame, so its scroll rides the model
		// and is reset whenever the card changes (armConfirm).
		BodyOffset: m.cardScroll,
		PanOffset:  m.cardPan,
	}
	req := m.pendingApproval
	// Where this decision sits in the round, and the key that answers the
	// rest of its category along with it.
	card.QueuePos = m.queuePosition()
	if card.Batch = len(m.pendingBatch) > 0; card.Batch {
		card.BatchHint = fmt.Sprintf("answer %d like this as a list", len(m.pendingBatch)+1)
	}
	// The blast-radius block, resolved when the decision was armed.
	// It also carries the safety risks, so the card states severity and
	// warnings from one source rather than two.
	m.pendingBlast.applyTo(card)

	if req == nil || req.kind == approvalExec {
		card.Variant = components.ApprovalCommand
		card.Title = "Approve command"
		card.Answer = "run it once"
		// [d] opens the command card's own full view — the whole command,
		// the warnings and the blast radius, unclipped — the way it opens an
		// edit's diff (docs/interface/surfaces.md#the-approval-card).
		card.FullDiff = true
		card.FullLabel = "full view"
		if req != nil {
			card.Headline = "Assistant wants to run: " + firstLine(m.pendingRun)
		} else {
			card.Headline = "Run: " + firstLine(m.pendingRun)
		}
		// A line the reader wrote says so on the rail and prints the line it
		// replaced under the headline: the card is about their command now,
		// and a card that looked identical to the one the model asked for
		// would be the one thing this key must never leave behind
		// (docs/capabilities/approvals-and-safety.md#an-amended-command-is-a-new-command).
		if req != nil && req.amendedFrom != "" {
			card.Amended = true
			card.Was = firstLine(req.amendedFrom)
		}
		// [a] is offered only for assistant commands without safety warnings:
		// flagged actions can never be pre-approved, and /run stays manual.
		// The card says why it is missing rather than omitting it silently.
		//
		// What it leads to is named on the card, because a key whose scope is
		// not stated is a key pressed on a guess: the reader sees `go test`
		// before they widen anything, not `commands` after they have. How
		// long the grant lasts is the list's own to say, one press later, on
		// the row that makes it (grant.go).
		if req != nil && len(card.Warnings) == 0 {
			if prefix := agent.GrantPrefix(req.command); prefix != "" {
				card.AllowAlways = true
				card.AlwaysHint = "allow " + strconv.Quote(prefix) + " without asking"
				// A command that writes outside the working scope is granting
				// two things at once, and the key says both: [y]
				// would add the directory for this session, [a] adds it and
				// stops asking about this shape of command as well.
				if m.pendingScope.any() {
					card.AlwaysHint += ", and " + displayDir(m.pendingScope.first()) + " with it"
				}
			}
		}
		card.ExtraHints = append(dryRunOffer(req), m.explainOffer(req)...)
		card.ExtraHints = append(card.ExtraHints, amendOffer(req)...)
		return card
	}

	card.Headline = "Assistant wants to " + req.title
	switch req.kind {
	case approvalDiff:
		card.Variant = components.ApprovalEdit
		card.Title = "Approve edit"
		card.Hunks = req.hunks
		card.Syntax = diffSyntax(req.path)
		card.FullDiff = len(req.hunks) > 0
		card.Answer = "apply the change"
		// No grant of any length on a flagged card, here as on the command
		// card above: "only for a minute" is still blanket, and a flagged
		// action is never blanket-approved
		// (docs/capabilities/approvals-and-safety.md#a-grant-says-when-it-ends).
		if len(card.Warnings) == 0 {
			card.AllowAlways = true
			card.AlwaysHint = "allow edits in " + displayDir(filepath.Dir(req.path)) + " without asking"
			if m.pendingScope.any() {
				card.AlwaysHint += " and add it to the working scope"
			}
		}
	default:
		card.Variant = components.ApprovalGeneric
		card.Title = "Approve tool"
		card.Answer = "allow it"
		if req.summary != req.title {
			card.Summary = firstLine(req.summary)
		}
		// A request that names a host offers [a], and the key says the host
		// rather than the category: what the reader read on the card's own
		// domain row is exactly what pressing it grants, and a page from the
		// same site is then not a card at all.
		if req.host != "" && len(card.Warnings) == 0 {
			card.AllowAlways = true
			card.AlwaysHint = "allow " + req.host + " without asking"
		}
	}
	return card
}

// applyTo puts the resolved block onto the card: the severity, the reading
// that makes it that and the border which reinforces both, the risks, the
// fields — the containment row among them — the footnote naming the key that
// is not offered, and, on a card whose answer is worth naming, the words esc
// is offered under.
func (b blastRadius) applyTo(card *components.ApprovalCard) {
	card.Severity, card.SeverityReason = b.severity, b.reason
	card.Fields = b.fields
	card.Uncontained = b.uncontained
	card.Footnote = b.footnote
	if b.safe != "" {
		card.Return = b.safe
	}
	card.Reversibility = b.reversibility
	if len(b.risks) > 0 {
		card.Warnings = []string{strings.Join(b.risks, "; ")}
	}
}

// cardPanStep is how far one press of the card's pan chords moves a wide
// body. Five columns, the step the horizontal-scroll convention settled on:
// one column reads as sticking, a screenful loses the reader's place.
const cardPanStep = 5

// scrollCard answers the card's scroll chords: a row of body per press, five
// columns of pan, clamped against what the card can actually show so fifty
// presses past the end cost one press back.
//
// It is handed the card rather than fetching one, because the session's own
// decision and a child's routed request are two cards with one pair of
// offsets between them, and bounds taken off the wrong card would clamp the
// scroll against a body nobody is reading (subagents.go).
func (m Model) scrollCard(msg tea.KeyPressMsg, card *components.ApprovalCard) (tea.Model, tea.Cmd) {
	maxBody, maxPan := card.ScrollBounds(m.contentWidth())
	switch {
	case keys.Match(msg, keys.Decision.ScrollUp):
		m.cardScroll = max(m.cardScroll-1, 0)
	case keys.Match(msg, keys.Decision.ScrollDown):
		m.cardScroll = min(m.cardScroll+1, maxBody)
	case keys.Match(msg, keys.Decision.PanLeft):
		m.cardPan = max(m.cardPan-cardPanStep, 0)
	case keys.Match(msg, keys.Decision.PanRight):
		m.cardPan = min(m.cardPan+cardPanStep, maxPan)
	}
	return m, nil
}

// commandCardView is the command card's [d]: the whole command, the warnings
// and the blast radius as one full-screen page, wrapped rather than clipped
// — the card's body clips wide lines behind the pan; this view is where the
// whole of a long command is read before it is answered.
func (m Model) commandCardView() *components.OutputView {
	card := m.buildApprovalCard()
	lines := strings.Split(strings.TrimRight(m.pendingRun, "\n"), "\n")
	if word := card.Severity.Word(); word != "" || len(card.Warnings) > 0 {
		lines = append(lines, "")
		if word != "" {
			lines = append(lines, word)
		}
		for _, w := range card.Warnings {
			lines = append(lines, "⚠ "+w)
		}
	}
	if len(card.Fields) > 0 {
		lines = append(lines, "")
		for _, f := range card.Fields {
			row := f.Label + "  " + f.Value
			if f.Detail != "" {
				row += " — " + f.Detail
			}
			lines = append(lines, row)
		}
	}
	return &components.OutputView{Title: card.Title, Lines: lines, Wrap: true}
}

// confirmLines renders the approval card — or the memory prompt when
// one is showing — one row per element.
func (m Model) confirmLines() []string {
	width := m.contentWidth()
	// The queue strip sits above whichever surface is asking.
	strip := m.pendingQueue.View(width)
	if o := m.askOverlay(); o != nil {
		return append(strip, o.Lines(m, width, 0)...)
	}
	if l := m.queueList; l != nil {
		// The list replaces both the card and the strip above it: it is that
		// strip opened, and drawing the two together would put every row on
		// the screen twice — once as context and once as the decision — in a
		// panel bounded to two fifths of the terminal.
		return strings.Split(l.sel.View(width), "\n")
	}
	return append(strip, strings.Split(m.approvalCard().View(width), "\n")...)
}

// confirmCursor is where the terminal's cursor stands in the confirm panel:
// inside whichever of the card's two fields is open, and nowhere otherwise —
// a card that is read rather than written into places none, and the terminal
// hides its cursor over it (the register's cursor column, overlay.go).
//
// The card says where it drew the field and the field says where its caret is
// inside it; what this adds is the rows confirmPanelLines puts above the
// card, which is the same two things in the same order dressDecision leads
// with.
//
// The panel's own width is passed in and deliberately not used: the card is
// rendered at the content width (confirmPanelLines), and a cursor counted at
// any other width would describe a card nobody drew. The two are the same
// number today, and this is the one that stays right if they stop being.
func (m Model) confirmCursor(int) *tea.Cursor {
	width := m.contentWidth()
	var field textinput.Model
	switch {
	case m.decisionNote != nil:
		field = m.decisionNote.drawn(width)
	case m.commandEdit != nil:
		field = m.commandEdit.drawn(width)
	default:
		return nil
	}
	x, y, ok := m.approvalCard().FieldOrigin(width)
	if !ok {
		return nil
	}
	cur := field.Cursor()
	if cur == nil {
		return nil
	}
	cur.X += x
	cur.Y += y + len(m.pendingQueue.View(width))
	if m.decisionGated() {
		// The rail that names the keyboard's owner leads the panel.
		cur.Y++
	}
	return cur
}

// confirmPanelLines is the whole bottom panel a gated confirm occupies: the
// card, the rail that names the keyboard's owner, and the draft it is holding
// while it does. confirmLines stays the card alone, because that
// is what the rest of the surface asks it for.
func (m Model) confirmPanelLines() []string {
	return m.dressDecision(m.confirmLines(), m.contentWidth())
}

// panel is the bottom panel this frame is showing, resolved once (layout.go):
// the rows the surface that owns the panel drew, and how many rows the
// vertical split gives them. The two used to be separate answers — one switch
// rendering a surface to count its rows, another rendering it again to draw
// it — so every takeover surface was rendered twice per frame.
func (m Model) panel() panelBody {
	if m.framed == nil {
		return m.resolvePanel()
	}
	if m.framed.panel == nil {
		p := m.resolvePanel()
		m.framed.panel = &p
	}
	return *m.framed.panel
}

// panelView is the panel's rows fitted to the rows they were given: what the
// bottom of the surface draws when the draft box is not what is showing.
func (m Model) panelView() string { return m.panel().view() }

// bottomPanelHeight is how many rows the bottom panel currently occupies; the
// confirm and plan-approval prompts may grow beyond the input's fixed height.
func (m Model) bottomPanelHeight() int { return m.panel().height }

// resolvePanel renders whichever surface owns the bottom panel and sizes it.
func (m Model) resolvePanel() panelBody {
	if testHookRenderPanel != nil {
		testHookRenderPanel()
	}
	// The agent manager and a routed child ask cover whatever surface the
	// state was showing (coverOverlay, overlay.go), so they are rendered here
	// once and both the row count and the draw read the same rows. Neither
	// can cover the prompt frame — the frame yields the panel to them — so a
	// frame that is showing renders neither.
	showingFrame := m.frameShowing()
	var cover []string
	if !showingFrame {
		if c := m.coverOverlay(); c != nil {
			cover = c.Lines(m, m.contentWidth(), 0)
		}
	}

	var lines []string
	bound := m.maxConfirmPanelHeight()
	// The register says which mode owns the panel and how tall it may grow.
	// A decision still waiting on the handover owns none of it: it rides
	// above the frame rather than filling the panel — the placement the
	// register calls floating — so the panel is the input's and
	// interruptHeight is what pays for the card.
	o := m.panelOverlay()
	if m.decisionUngated() && showingFrame {
		o = nil
	}
	switch {
	case o != nil:
		lines, bound = o.Lines(m, m.contentWidth(), 0), o.Bound(m)
	case m.panelCovered():
		lines = cover
	case m.historySearching():
		// The history search extends the input area the way the menu does.
		return panelBody{lines: cover, height: min(m.input.Height()+len(m.historySearchLines()), m.maxConfirmPanelHeight())}
	case m.completionActive() && m.attachedTo == "":
		// The completion menu extends the input area.
		return panelBody{lines: cover, height: min(m.input.Height()+len(m.completionMenuLines()), m.maxConfirmPanelHeight())}
	}
	// The cover takes the rows whatever was under it was given: it replaces
	// the panel's content and never its accounting.
	body := lines
	if cover != nil {
		body = cover
	}
	if n := len(lines); n > inputHeight {
		return panelBody{lines: body, height: min(n, bound)}
	}
	if lines == nil {
		if o := overlayFor(m.state); o != nil && o.place == placePane {
			// A pane overlay replaces the input with a one-line hint; a grown
			// draft comes back with the input, and paying its rows here would
			// blank most of the panel under the hint.
			return panelBody{lines: cover, height: inputHeight}
		}
		// The bare draft box: its height follows its content (frame.go,
		// syncInputHeight), so the panel reads the box rather than the
		// three-row constant.
		return panelBody{lines: cover, height: m.input.Height()}
	}
	return panelBody{lines: body, height: inputHeight}
}

// testHookRenderPanel, when non-nil, observes every render of the bottom
// panel. It exists to hold the property this function's memo buys: a frame
// resolves the panel once, and a second render in one paint is the frame
// having been bypassed — which nothing else on the surface can see, because
// both renders produce the same rows.
var testHookRenderPanel func()

// maxConfirmPanelHeight bounds how far the confirm panel may grow into the
// viewport (docs/interface/principles.md#the-grammar: at most 40% of terminal
// height).
func (m Model) maxConfirmPanelHeight() int {
	return max(m.height*2/5, inputHeight)
}

// syncViewportHeight resizes the viewport when the bottom panel grows or
// shrinks (e.g. a diff preview replacing the input area).
func (m *Model) syncViewport() {
	if !m.ready {
		return
	}
	// Both dimensions can move without a resize: the chrome takes rows as
	// surfaces open and close, and the inspector rail takes columns whenever
	// the pane split toggles.
	h, w := m.viewportHeight(), m.transcriptWidth()
	if h == m.viewport.Height() && w == m.viewport.Width() {
		return
	}
	// The pane split can take columns without a terminal resize, and
	// a selection's coordinates were taken at the old width.
	m.resizeSelection(w)
	m.viewport.SetHeight(h)
	m.viewport.SetWidth(w)
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoBottom()
}
