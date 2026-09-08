package chat

// Running a code block, and the confirm that stands in front of it.
//
// /run and a !bang both land on the same card and the same execution, which
// is the point: what a command costs does not depend on which door asked for
// it (docs/interface/surfaces.md#the-approval-card).

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/dryrun"
	"github.com/rfizzle/shhh/internal/hook"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// startRun resolves which code block from the last response to execute.
// It returns either a message for the transcript, or entersConfirm=true after
// switching to the confirmation state. Bare /run takes the first block: the
// several-blocks case is routed to the picker before it gets here.
func (m *Model) startRun(parts []string) (result string, entersConfirm bool) {
	if m.runFn == nil {
		return "Command execution is not available in this session.", false
	}
	blocks := extractCodeBlocks(m.lastAssistantText())
	if len(blocks) == 0 {
		return "No code blocks in the last response to run.", false
	}
	idx := 0
	if len(parts) > 1 {
		n, err := strconv.Atoi(parts[1])
		if err != nil || n < 1 || n > len(blocks) {
			return fmt.Sprintf("Usage: /run [1-%d]", len(blocks)), false
		}
		idx = n - 1
	}
	m.pendingRun = blocks[idx]
	// /run is the user's own command: it never runs contained, so the working
	// scope has nothing to say about it.
	m.pendingScope = scopeReach{}
	m.pendingBlast = m.resolveRadius(nil)
	m.clearQueueStrip()
	m.setTurnState(stateConfirmRun)
	return "", true
}

// updateConfirmRun routes confirm-prompt keys through the approval card
// ; the card's y/n/esc semantics match the original prompt, and [a]
// is offered only where a grant is allowed at all.
func (m Model) updateConfirmRun(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// A memory proposal confirms through its own prompt, not the card. It is
	// a row of the register like every other mode (overlay.go) rather than
	// the hand check it was in each of the five places a state is read.
	if o := m.askOverlay(); o != nil {
		next, act := o.Update(m, msg)
		return next, act.run
	}
	// The card's own note field, if one is open. It is answered above
	// everything below it because it holds the keyboard: while it is up the
	// card's letters, its digits and its chords are all text
	// (approval.go).
	if m.decisionNote != nil {
		return m.updateDecisionNote(msg)
	}
	// And the card's other field, the command itself, for exactly the same
	// reason: while it is up every letter is text (amend.go).
	if m.commandEdit != nil {
		return m.updateCommandEdit(msg)
	}
	// And the queue, where the key over the stack opened it as a list. It
	// holds the keyboard the way the fields above do, and for a reason of the
	// same family: while it is up the keys are the selector's — its `a` ticks
	// every row rather than granting the session (queue.go).
	if m.queueList != nil {
		return m.updateQueueList(msg)
	}
	// And the grants the always-allow key offers — the third surface the card
	// holds under itself, after its two fields — answered here for the reason
	// they are: it holds the keyboard, so the card's own letters, digits and
	// chords are inert until it is closed (grant.go).
	if m.grantChoice != nil {
		return m.updateGrantChoice(msg)
	}
	// The card's own scroll, answered before the decision keys so a held
	// card cannot read a chord as the start of a sentence. The chords reach
	// here only while the card holds the keyboard; ungated they still
	// scroll the transcript, exactly as before.
	if keys.Match(msg, keys.Decision.ScrollUp, keys.Decision.ScrollDown,
		keys.Decision.PanLeft, keys.Decision.PanRight) {
		return m.scrollCard(msg, m.approvalCard())
	}
	// The dry run, before the card: it is not one of the card's answers and
	// the card would read it as the start of a sentence.
	if next, cmd, ok := m.dryRunKey(msg); ok {
		return next, cmd
	}
	// The explanation, for the same reason and in the same place: it settles
	// nothing, and the card would read the letter as the start of a sentence.
	if next, cmd, ok := m.explainKey(msg); ok {
		return next, cmd
	}
	// The amendment, in the same place and for the same reason — it settles
	// nothing, it opens a field (amend.go).
	if next, cmd, ok := m.amendKey(msg); ok {
		return next, cmd
	}
	done, result := m.approvalCard().Update(msg)
	if !done {
		return m, nil
	}
	switch result {
	case components.ApprovalApprove:
		return m.approvePending("")
	// The two answers that carry a sentence settle nothing yet: the key opens
	// the field, and the answer is given when the field is confirmed
	// (docs/capabilities/approvals-and-safety.md#a-no-can-say-why-and-a-yes-can-say-what-next).
	case components.ApprovalApproveNoted:
		return m.openDecisionNote(true)
	case components.ApprovalDenyNoted:
		return m.openDecisionNote(false)
	case components.ApprovalFullDiff:
		// [d] opens the pending edit full screen; esc returns here
		// with the approval still pending.
		if req := m.pendingApproval; req != nil && req.kind == approvalDiff {
			return m.openDiffFull(&components.DiffView{
				Path:   req.path,
				Verb:   req.verb,
				Hunks:  req.hunks,
				Syntax: diffSyntax(req.path),
			}, stateConfirmRun)
		}
		// A command card's [d] opens its own facts the same way: the whole
		// command, the warnings and the blast radius, unclipped, with the
		// decision still pending behind it.
		if req := m.pendingApproval; req == nil || req.kind == approvalExec {
			return m.openOutputFull(m.commandCardView(), noOutputEntry, stateConfirmRun)
		}
	case components.ApprovalBatch:
		// [A] renders this decision and every queued decision the session
		// would classify the same way as the list that answers them. It
		// settles nothing on its own — the answer is given when the list is
		// confirmed, and esc leaves the queue as it was (queue.go).
		// Membership was on the strip before the key opened it, and a flagged
		// action was never in it.
		return m.openQueueList()
	case components.ApprovalAlways:
		// The key settles nothing on its own: it opens the list of grants
		// the card can make, each row naming what it covers and when it ends
		// (grant.go). Taking a row grants and runs, which is what the key
		// did before the list stood in front of it; esc leaves with nothing
		// granted and the decision still waiting.
		//
		// The grant is scoped to what the card showed — this command's
		// leading words or the line itself, this file's directory or the
		// file — because that is what the reader read. The blanket grants
		// the key used to hand out are `/permissions allow` now: a
		// session-wide "never ask me again" is a decision worth typing, not
		// a decision worth pressing while a card is in front of you.
		//
		// Safety-flagged commands, generic gated tools, and /run keep asking
		// (the card offers [a] only where a grant is allowed).
		return m.openGrantChoice()
	case components.ApprovalRelease:
		// The card had the keyboard by arrival and this key is not one of its
		// answers, so it is the first letter of a sentence. The
		// decision stays exactly where it was.
		return m.releaseToDraft(msg)
	case components.ApprovalDeny:
		if m.pendingApproval != nil {
			return m.declineApproval()
		}
		m.pendingRun = ""
		m.pendingRunLocal = false
		m.setTurnState(stateInput)
		m.syncViewport()
		m.appendEntry(entry{kind: entrySystem, text: "Run cancelled."})
		m.viewport.SetLines(m.renderHistoryLines())
		m.viewport.GotoBottom()
		return m, nil
	}
	return m, nil
}

func execToolResult(output string, exitCode int) string {
	return tools.FormatExecResult(output, exitCode)
}

func (m Model) executeRun() (tea.Model, tea.Cmd) {
	command := m.pendingRun
	local := m.pendingRunLocal
	m.pendingRun = ""
	m.pendingRunLocal = false
	// An approved command that writes outside the working scope puts those
	// directories in it — otherwise containment would go on refusing
	// the write the user has just approved.
	m.applyScopeGrant()
	m.setTurnState(stateRunningCmd)
	m.runningCommand = command
	m.runStart = time.Now()
	tail := &commandTail{}
	m.runTail = tail
	m.syncViewport()
	// An assistant command gets a ceiling; a command the reader typed does
	// not, because they are here and the cancel key is their ceiling.
	assistant := m.pendingApproval != nil
	var ctx context.Context
	var cancel context.CancelFunc
	if assistant && m.policy.timeout > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), m.policy.timeout)
	} else {
		ctx, cancel = context.WithCancel(context.Background())
	}
	m.runCancel = cancel
	runID := m.agent.RunID()
	runFn := m.runFn
	tailFn := m.tailRunFn
	// Assistant commands run contained when a mechanism is available;
	// /run — the user's own command — stays on the plain runner.
	if m.pendingApproval != nil && m.containment.Run != nil {
		runFn = m.containment.Run
		tailFn = m.containment.TailRun
	}
	// An assistant command is a call like any other to the seams either side
	// of it: what a hook in front of it wanted the model to read leads the
	// output, and the hooks behind it are told what the command produced
	// (hooks.go). A `/run` the reader typed is not the assistant's call and
	// fires neither.
	hooks := m.hooks
	hookLead, hookAt := "", m.hookPos()
	hookCall := hook.Call{Name: tools.ExecCommandName, Arguments: command}
	if assistant && m.pendingApproval != nil {
		hookLead = m.pendingApproval.hookContext
	}
	return m, func() tea.Msg {
		start := time.Now()
		var out string
		var code int
		// The tail-capable runner feeds the live row when wired.
		if tailFn != nil {
			out, code = tailFn(ctx, command, tail.Set)
		} else {
			out, code = runFn(ctx, command)
		}
		if assistant {
			if hookLead != "" {
				out = hookLead + "\n" + out
			}
			out = hooks.PostTool(context.Background(), hookAt, hookCall, out, execOutcome(code)).Lead(out)
		}
		// What the ceiling did to a command that reached it — stopped it, or
		// handed it to the process supervisor because it was still
		// printing — is said in the output by the runner that holds it, in
		// words, because an exit code cannot tell either of those from a
		// command that broke.
		return cmdDoneMsg{runID: runID, command: command, output: out, exitCode: code, duration: time.Since(start), local: local}
	}
}

// commandContextMessage is appended to the conversation so the model can see
// what a /run produced, without triggering a response. It goes in the user's
// role, because that is the only role a fact can be read in, and is flagged
// as the session's own so no surface offers it back as something the reader
// typed (provider.Message.Machine).
func commandContextMessage(command, output string, exitCode int) string {
	if cut, truncated := tools.TruncateOutput(output, tools.MaxExecOutputBytes); truncated {
		output = cut + "\n… (output truncated)"
	}
	if strings.TrimSpace(output) == "" {
		output = "(no output)"
	}
	return fmt.Sprintf(commandContextPrefix+"\n```\n%s\n```\nExit code: %d\nOutput:\n```\n%s\n```", command, exitCode, output)
}

// The dry run at the card.
//
// A command that can be asked what it would do rather than told to do it is
// the one honest answer to a reader hesitating over `rsync --delete`, and the
// hesitation happens here (docs/interface/surfaces.md#the-approval-card).
// The offer is made only where internal/dryrun could derive a harmless form:
// a key that would run the real thing is not an offer, it is a trap.

// dryRunTimeout bounds a derived form that turns out not to be the quick
// report it was asked to be. It is the floor under the session's own command
// ceiling rather than a second policy: nothing is watching this run, and a
// card waiting forever on it would have to be answered blind.
const dryRunTimeout = 30 * time.Second

// dryRunForm is the harmless form of a command, or "" where the command has
// none. The empty string is the whole of what the card needs to know: no key
// is offered, and the card says nothing about a dry run at all.
func dryRunForm(command string) string {
	form, ok := dryrun.Derive(command)
	if !ok {
		return ""
	}
	return form
}

// dryRunOffer is the key the command card advertises beside its decision run,
// and nothing at all for a command with no harmless form.
//
// While the run is in flight the key stays where it was drawn with the words
// changed: the answer to the press is already on its way, so a second press
// has nothing to add, and taking the row away mid-wait would read as the
// offer having been withdrawn.
func dryRunOffer(req *approvalRequest) []components.KeyOffer {
	if req == nil || req.dryCommand == "" {
		return nil
	}
	label := "dry run — see what it would do"
	if req.dryRunning {
		label = "dry run — running"
	}
	return []components.KeyOffer{{Key: keys.Bracket(keys.Decision.DryRun), Label: label}}
}

// dryRunKey answers the card's dry-run key: the derived form runs, and the
// decision stays exactly where it was. handled is false for every key this is
// not, and for a card that took the keyboard by arriving — that card claims
// the two answers and nothing else, and this letter is the reader's sentence
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
func (m Model) dryRunKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	if !keys.Match(msg, keys.Decision.DryRun) || m.heldOnArrival {
		return m, nil, false
	}
	req := m.pendingApproval
	if req == nil || req.kind != approvalExec || req.dryCommand == "" || req.dryRunning {
		return m, nil, false
	}
	// The runner the real command would have used, chosen the way executeRun
	// chooses it: a form derived from an assistant's command is still the
	// assistant's command, and running it outside the containment the
	// decision is being made about would be reporting on a different machine.
	run := m.runFn
	if m.containment.Run != nil {
		run = m.containment.Run
	}
	if run == nil {
		return m, nil, false
	}
	req.dryRunning = true
	limit := m.policy.timeout
	if limit <= 0 {
		limit = dryRunTimeout
	}
	command, call, runID := req.dryCommand, req.call.ID, m.agent.RunID()
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), limit)
		defer cancel()
		start := time.Now()
		out, code := run(ctx, command)
		return dryRunDoneMsg{runID: runID, call: call, command: command,
			output: out, exitCode: code, duration: time.Since(start)}
	}, true
}

// dryRunDoneMsg is what the harmless form printed. It carries the call it was
// asked about so the answer cannot be attached to the next decision in the
// queue.
type dryRunDoneMsg struct {
	runID    int
	call     string
	command  string
	output   string
	exitCode int
	duration time.Duration
}

// finishDryRun files what the dry run printed and puts it on the screen.
//
// The row goes in whatever the reader did while it ran — something ran on
// this machine and the transcript is the account of what ran — and it is
// marked local, because a dry run is asked by the person and its output never
// joins the conversation: the model asked to run the real command and is
// still waiting for the answer to that.
//
// The screen only opens if the decision it was asked about is still the one
// being asked, and the card is still what is on it: a reader who opened the
// card's full view meanwhile is reading something they asked for, and taking
// that away would be one answer cancelling another. It opens the way the full
// view opens and comes back the same way, with the decision still unanswered,
// which is the whole point of the key: nothing here approves anything.
func (m Model) finishDryRun(msg dryRunDoneMsg) (tea.Model, tea.Cmd) {
	if msg.runID != m.agent.RunID() {
		return m, nil
	}
	out := strings.TrimRight(msg.output, "\n")
	m.appendEntry(entry{kind: entryCommand, text: msg.command, toolResult: out,
		exitCode: msg.exitCode, localRun: true, duration: msg.duration})
	req := m.pendingApproval
	// The command as well as the call: the reader can have amended the line
	// while the form was running, and a screen reporting on a command the
	// card is no longer about would be an answer to a question nobody is
	// still asking (amend.go).
	pending := req != nil && req.call.ID == msg.call &&
		req.dryCommand == msg.command && m.state == stateConfirmRun
	if req != nil && req.call.ID == msg.call {
		req.dryRunning = false
	}
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoBottom()
	if !pending {
		return m, nil
	}
	// The screen came from the press rather than from a row, like the card's
	// own full view: leaving it leaves, and the row it left behind is where
	// the output is read from a second time.
	return m.openOutputFull(dryRunView(msg, out), noOutputEntry, stateConfirmRun)
}

// dryRunView is the full screen the answer opens on: what the derived form
// was, what it printed, and — where it printed nothing or stopped badly — the
// sentence that says so, since a blank screen is not an answer to a question
// somebody pressed a key to ask.
func dryRunView(msg dryRunDoneMsg, out string) *components.OutputView {
	title := "dry run — " + firstLine(msg.command)
	if msg.exitCode != 0 {
		title += fmt.Sprintf(" (exit %d)", msg.exitCode)
	}
	lines := strings.Split(out, "\n")
	if strings.TrimSpace(out) == "" {
		lines = []string{"It reported nothing — the dry run found no work to do."}
	}
	return &components.OutputView{Title: title, Lines: lines}
}

// The explanation at the card.
//
// The alternative to this key is leaving the card to search the web with the
// decision still waiting behind you, which is the state the offer exists to
// remove (docs/interface/surfaces.md#the-approval-card).
//
// It is the dry run's shape with a model where the subprocess was: a bounded
// thing runs, what it produced takes the screen the full view takes, the
// screen gives itself back with the decision still unanswered, and nothing it
// produced enters the conversation. Copying the shape is what keeps the two
// consistent when one of them changes. The one place they part is the
// transcript: a dry run leaves a row because something ran on this machine,
// and nothing ran here — a reading is not an act, which is why the summary
// and the session's title leave no row either.

// explainOffer is the key the command card advertises beside its decision
// run, and nothing at all where nothing is configured to answer it: a key
// that could not be answered is not an offer.
//
// It is an assistant's command and not a /run the reader typed. A person who
// typed the line knows what it does, and a card with no request behind it has
// nothing to explain — which is the same reason the offer is off on an edit,
// where the diff is already the explanation.
//
// While the reading is in flight the key stays where it was drawn with the
// words changed, for the reason the dry run's does — the answer to the press
// is already on its way, and taking the row away mid-wait would read as the
// offer having been withdrawn.
func (m Model) explainOffer(req *approvalRequest) []components.KeyOffer {
	if req == nil || req.kind != approvalExec || !m.explainer.Enabled() {
		return nil
	}
	label := "explain — what this command does"
	if req.explaining {
		label = "explain — asking"
	}
	return []components.KeyOffer{{Key: keys.Bracket(keys.Decision.Explain), Label: label}}
}

// explainKey answers the card's explain key: a paragraph is read, and the
// decision stays exactly where it was. handled is false for every key this is
// not, and for a card that took the keyboard by arriving — that card claims
// its answers and nothing else, and this letter is the reader's sentence
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
func (m Model) explainKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	if !keys.Match(msg, keys.Decision.Explain) || m.heldOnArrival {
		return m, nil, false
	}
	req := m.pendingApproval
	if req == nil || req.kind != approvalExec || req.explaining || !m.explainer.Enabled() {
		return m, nil, false
	}
	req.explaining = true
	explainer := m.explainer
	command, call, runID := req.command, req.call.ID, m.agent.RunID()
	return m, func() tea.Msg {
		// The explainer bounds its own request, the way the classifier and
		// the titler do: the deadline is a fact about the reading rather than
		// about the surface that asked for it.
		v := explainer.Explain(context.Background(), agent.ExplainRequest{Command: command})
		return explainDoneMsg{runID: runID, call: call, command: command, verdict: v}
	}, true
}

// explainDoneMsg is the paragraph, or the sentence saying there is none. It
// carries the call it was asked about so the answer cannot be attached to the
// next decision in the queue.
type explainDoneMsg struct {
	runID   int
	call    string
	command string
	verdict agent.ExplainVerdict
}

// finishExplain puts the paragraph on the screen.
//
// The screen only opens if the decision it was asked about is still the one
// being asked, and the card is still what is on it: a reader who opened the
// card's full view meanwhile is reading something they asked for, and taking
// that away would be one answer cancelling another. It opens the way the full
// view opens and comes back the same way, with the decision still unanswered,
// which is the whole point of the key: nothing here approves anything.
//
// A failed reading opens the screen too. It is the same press being answered,
// and a key that silently did nothing when the request timed out would be
// indistinguishable from a key that is not wired up.
func (m Model) finishExplain(msg explainDoneMsg) (tea.Model, tea.Cmd) {
	if msg.runID != m.agent.RunID() {
		return m, nil
	}
	req := m.pendingApproval
	// The command as well as the call, for the reason the dry run reads
	// both: a paragraph about the line the reader has just replaced is not
	// an explanation of the card in front of them (amend.go).
	pending := req != nil && req.call.ID == msg.call &&
		req.command == msg.command && m.state == stateConfirmRun
	if req != nil && req.call.ID == msg.call {
		req.explaining = false
	}
	if !pending {
		return m, nil
	}
	return m.openOutputFull(explainView(msg), noOutputEntry, stateConfirmRun)
}

// explainView is the full screen the answer opens on: what was asked about,
// what came back, and a footer naming who said it and what it cost — because
// an explanation is a claim, and a reader about to decide on it is owed who
// made it and what asking took.
//
// The last line of the footer is the one thing the screen has to say that is
// not about the command: nothing here reached the model that is waiting for
// the decision. It asked to run this command, and it is still waiting for the
// answer to that.
func explainView(msg explainDoneMsg) *components.OutputView {
	v := msg.verdict
	title := "explain — " + firstLine(msg.command)
	var lines []string
	if v.Failed {
		// Never an empty paragraph: a blank screen is not an answer to a
		// question somebody pressed a key to ask.
		lines = []string{"The explanation could not be read.", "", v.Err}
	} else {
		lines = strings.Split(v.Text, "\n")
	}
	footer := "asked " + v.Model
	if u := v.Usage; u.PromptTokens > 0 || u.CompletionTokens > 0 {
		footer += fmt.Sprintf(" · ↑%d ↓%d", u.PromptTokens, u.CompletionTokens)
	}
	lines = append(lines, "", footer, "Nothing on this screen was sent to the model waiting on your answer.")
	return &components.OutputView{Title: title, Lines: lines, Wrap: true}
}
