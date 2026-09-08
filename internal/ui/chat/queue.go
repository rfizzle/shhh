package chat

// The approval queue strip and batch approval (
// docs/interface/surfaces.md#the-approval-card). The queue has always existed
// — the approval queue existed — but it was invisible: the card said nothing
// about what was stacked behind it, so five decisions cost five identical
// keystrokes and read as one decision asked five times. This file exposes the
// stack and adds the one key that answers a category of it.
//
// Membership is decided by the same matcher the [a] session grant uses, so
// "the same way" means one thing in both features rather than two. A
// safety-flagged action belongs to no list: it is taken out and asked on its
// own, whatever else is in the queue.
//
// The key over the stack renders it as the pick-several list rather than
// answering it sight unseen: allowing four and denying two is one pass over a
// list you can see, and the alternative it replaces was all of them or six
// cards (docs/interface/surfaces.md#the-approval-card). Confirming marks
// rather than executes — the marks are read when each call reaches the head —
// because a call the list allowed can be inadmissible by the time it runs.
//
// Like the blast-radius block beside it, the strip is resolved once, when the
// decision is armed — it previews every queued call, which reads the files
// the edits would change, and View runs on every frame.

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/radius"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// queueStripRows bounds the strip so a long queue cannot push the card off a
// short terminal; what does not fit is counted, never dropped silently. The
// bound tightens further on a short terminal, down to a floor of two items —
// below that the strip stops being a list and is only a count.
const queueStripRows = 6

// stripRows is the item bound for the terminal this session is running in.
func (m Model) stripRows() int {
	return max(min(queueStripRows, m.height/6), 2)
}

// confirmPanelBound is how tall the confirm panel may grow. The card keeps
// the 40% bound it has always had (docs/interface/principles.md#the-grammar)
// and the strip's rows sit above it: the strip is context for the decision,
// not part of it, and taking its rows out of the card would spend the
// decision's own space on the list of decisions.
func (m Model) confirmPanelBound() int {
	// The rail and the undressed draft a gated decision adds are paid for
	// here too, so the card is never the thing clipped off the bottom to
	// make room for them.
	return m.maxConfirmPanelHeight() + m.pendingQueue.Rows() + m.gatedExtraRows()
}

// resolveQueue builds the strip above the card and the set the queue key
// would put on the list. cur is the decision being shown — already built by
// the caller, so its diff is not computed twice — and is the head of the
// queue it describes.
func (m Model) resolveQueue(cur *approvalRequest) (components.QueueStrip, []string) {
	calls := m.agent.PendingApprovals()
	if cur == nil || len(calls) < 2 {
		return components.QueueStrip{}, nil
	}
	kind, batchable := m.batchCategory(cur)
	first := m.approvalTotal - len(calls) + 1
	label, detail := queueLabel(cur)
	items := []components.QueueItem{{
		Number: max(first, 1), Label: label, Detail: detail,
		Severity: queueSeverity(cur), Batch: batchable,
	}}
	var batch []string
	for i, tc := range calls[1:] {
		req := m.previewQueued(tc)
		label, detail := queueLabel(req)
		item := components.QueueItem{
			Number: max(first, 1) + i + 1, Label: label, Detail: detail,
			Severity: queueSeverity(req),
		}
		if k, ok := m.batchCategory(req); batchable && ok && k == kind {
			item.Batch = true
			batch = append(batch, tc.ID)
		}
		items = append(items, item)
	}
	strip := components.QueueStrip{Items: items, MaxRows: m.stripRows()}
	if len(batch) > 0 {
		strip.Note = fmt.Sprintf("%s lists the %d marked",
			keys.Bracket(keys.Decision.Batch), len(batch)+1)
	}
	return strip, batch
}

// previewQueued describes a queued call for the strip. A call whose arguments
// will not parse is listed as what it is rather than omitted: it is still a
// decision the queue holds, and it will be reported when its turn comes.
func (m Model) previewQueued(tc provider.ToolCall) *approvalRequest {
	req, err := m.buildApprovalRequest(tc)
	if err != nil {
		return &approvalRequest{
			call: tc, kind: approvalGeneric,
			title: tc.Name, summary: "invalid arguments — will be skipped",
		}
	}
	return req
}

// skippedArgsNotice is the row for a refused call that cannot even be named:
// a call arriving with no tool name leaves nothing to say but that one of
// them was skipped.
const skippedArgsNotice = "Skipped a tool call with invalid arguments."

// skippedArgsRow is the line a malformed call leaves behind. It names the
// tool because three of these in one session are otherwise indistinguishable
// — one mistake repeated and three different ones read exactly alike — and
// the tool is the first thing that tells them apart.
func skippedArgsRow(name string) string {
	if name == "" {
		return skippedArgsNotice
	}
	return "skipped · " + name + " · invalid arguments"
}

// skippedCallEntry is the transcript's account of a call the queue refused
// before it could reach a card. name is the tool the model asked for.
//
// Both refusals fold rather than shorten: one line, with the sentence the
// model was given underneath it and nothing dropped
// (docs/interface/principles.md#fold-never-hide). The model is handed that
// sentence whichever refusal this is, so a reader shown only the notice is
// the one party to the failure who cannot tell whether the session is
// recovering or repeating itself.
//
// A file that changed since it was read is not a malformed call, and saying
// so cost the reader the one fact only they have: which editor, sibling
// session or background build touched the file. So that refusal gets its own
// row, naming the file and what happened to it.
func (m Model) skippedCallEntry(name string, err error) entry {
	// The expansion is the sentence the model was given, verbatim: a reader
	// deciding whether the model can recover needs to see what it was
	// actually told, not this row's paraphrase of it.
	var stale tools.StaleError
	if !errors.As(err, &stale) {
		var given string
		if err != nil {
			given = err.Error()
		}
		return entry{kind: entrySystem, text: skippedArgsRow(name), toolResult: given}
	}
	return entry{
		kind:       entrySystem,
		text:       stale.Skipped(m.rowPath(stale.Path)),
		toolResult: stale.Error(),
	}
}

// rowPath is a path as a transcript row writes it: relative to the session's
// workspace when it is inside it, and as it arrived when it is not — that a
// file is somewhere else is the fact worth seeing, and a relative path to it
// would hide that behind a run of "..".
func (m Model) rowPath(p string) string {
	root := m.workspace
	if root == "" || p == "" {
		return p
	}
	rel, err := filepath.Rel(root, m.inWorkspace(p))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return p
	}
	return rel
}

// batchCategory is the class a session grant ([a]) would cover, which is
// exactly the question [A] asks of the queue — so both read it from here. A
// flagged action, and anything the grants do not cover, belongs to no batch.
func (m Model) batchCategory(req *approvalRequest) (agent.ActionKind, bool) {
	act := m.approvalAction(req)
	// A decision that leaves the working scope is never swept into a
	// batch: [A] answers the calls the session would classify the same way,
	// and a directory nobody has put in scope is the one thing on the card
	// the reader has not already answered for.
	// A fetch is out for a reason of its own: two fetches in one queue are
	// two hosts, which is two decisions however alike the two calls look.
	if act.SafetyFlagged || act.Kind == agent.ActionOther || act.Kind == agent.ActionFetch ||
		len(act.OutOfScope) > 0 {
		return act.Kind, false
	}
	return act.Kind, true
}

// queueLabel is the one line the strip gives an item — enough to recognise
// the decision, never enough to make it; the card below is where a decision
// is made — and the detail that rides the rating column beside it. An edit's
// diff stats go there rather than into the label, so a long path is what
// shortens on a narrow terminal.
func queueLabel(req *approvalRequest) (label, detail string) {
	switch req.kind {
	case approvalExec:
		return firstLine(req.command), ""
	case approvalDiff:
		adds, dels := diff.Stats(req.hunks)
		return firstLine(req.title), fmt.Sprintf("+%d −%d", adds, dels)
	case approvalQuestion:
		// The question, not the tool that carried it: a strip is what lets a
		// reader recognise the decision without opening the card, and two
		// queued questions both labelled `ask` would be two rows nobody can
		// tell apart (question.go).
		return firstLine(req.summary), ""
	}
	if req.title != "" {
		return firstLine(req.title), ""
	}
	return firstLine(req.summary), ""
}

// queueSeverity rates a queued item from the same resolver the card's own
// severity comes from, but without the filesystem and git reads the card's
// fields need: a strip is a list, and a list of five should not cost five
// stats and five git calls.
func queueSeverity(req *approvalRequest) components.Severity {
	switch req.kind {
	case approvalExec:
		return severityOf(radius.Outline(req.command).Level)
	case approvalDiff:
		return components.SeverityMedium
	case approvalMemory:
		return components.SeverityNone
	}
	if req.command != "" {
		return severityOf(radius.Outline(req.command).Level)
	}
	for _, f := range req.fields {
		if f.Open {
			return components.SeverityMedium
		}
	}
	return components.SeverityLow
}

// queuePosition is the card title's "(2 of 5)": where this decision sits in
// the round, which the strip's dots — drawn over what is left — cannot say.
func (m Model) queuePosition() string {
	remaining := m.agent.QueuedApprovals()
	if remaining < 2 && m.approvalTotal < 2 {
		return ""
	}
	total := max(m.approvalTotal, remaining)
	return fmt.Sprintf("%d of %d", max(total-remaining+1, 1), total)
}

// queueList is the queue behind the card, open as the list that answers it:
// the rows the reader is checking and unchecking, and the calls they stand
// for. It is a struct of its own held by a pointer that is nil while the list
// is down, the way every other mode with state of its own is (model.go).
type queueList struct {
	// sel is the pick-several list. It is a pointer and written through as
	// one, the way the question card holds its own (question.go): the ticks
	// are the reader's answer being assembled and there is nothing to be
	// gained by copying it out and back on every keystroke.
	sel *components.MultiSelect
	// ids are the calls the rows stand for, in the rows' order — ids[0] is
	// the decision the card is showing and the rest are queued behind it. The
	// list is answered by walking these rather than by reading labels back
	// off the rows, so a row's wording and the call it answers can never come
	// apart.
	//
	// A row the list could not take carries no id: it is counted at the end
	// and is not one of these.
	ids []string
}

// openQueueList opens the queue as the list. The rows are exactly the ones
// the strip marked — the reader has been looking at that membership since the
// card was armed, and a key that opened a different set than the one it
// advertised would be the key answering a question nobody asked.
//
// Everything starts checked, because that is the answer the key used to give
// on its own: a reader who learned it as "the rest like this one" presses it,
// sees the set, and enter is still that answer.
func (m Model) openQueueList() (tea.Model, tea.Cmd) {
	req := m.pendingApproval
	if req == nil || len(m.pendingBatch) == 0 {
		return m, nil
	}
	marked := make(map[string]bool, len(m.pendingBatch))
	for _, id := range m.pendingBatch {
		marked[id] = true
	}
	label, detail := queueLabel(req)
	opts := []components.SelectOption{{Label: label, Meta: detail}}
	rated := []components.Severity{queueSeverity(req)}
	ids := []string{req.call.ID}
	apart := 0
	for _, tc := range m.agent.PendingApprovals() {
		if tc.ID == req.call.ID {
			continue
		}
		if !marked[tc.ID] {
			// Flagged, out of scope, a fetch, or simply another kind of act:
			// it is asked on its own card and is counted here rather than
			// dropped, so the list never implies the queue ends where it does
			// (docs/interface/principles.md#fold-never-hide).
			apart++
			continue
		}
		queued := m.previewQueued(tc)
		label, detail := queueLabel(queued)
		opts = append(opts, components.SelectOption{Label: label, Meta: detail})
		rated = append(rated, queueSeverity(queued))
		ids = append(ids, tc.ID)
	}
	if apart > 0 {
		opts = append(opts, components.SelectOption{
			Label: apartRow(apart), Meta: "each is its own card", Dim: true,
		})
		rated = append(rated, components.SeverityNone)
	}
	sel := components.NewMultiSelect(queueListTitle, opts)
	for i := range ids {
		sel.Checked[i] = true
	}
	sel.Severities = rated
	// The list windows inside the same forty per cent every decision is drawn
	// in (docs/interface/principles.md#one-interaction-panel): a queue of
	// twenty scrolls behind the markers the rest of the package uses rather
	// than pushing the keys that answer it off the screen.
	sel.MaxLines = m.maxConfirmPanelHeight()
	// Nothing checked is an answer here, and it is "deny all of them". This
	// list is setting what happens to each row rather than choosing among
	// them, so refusing an empty answer would leave the reader no way to say
	// the one thing the old key could never say.
	sel.AllowNone = true
	m.queueList = &queueList{sel: sel, ids: ids}
	m.syncViewport()
	return m, nil
}

// queueListTitle says what enter does, because that is the fact a reader
// needs before they press it and the boxes alone do not carry it: a tick is
// an allow and an empty box is a denial, not a row left for later.
const queueListTitle = "Allow the checked, deny the rest"

// apartRow is the count of queued decisions the list could not take. They are
// named as what they are — decisions asked on their own — rather than as rows
// that were removed, because from the reader's side nothing was taken away:
// each of them still arrives as its own card.
func apartRow(n int) string {
	if n == 1 {
		return "1 asked on its own"
	}
	return strconv.Itoa(n) + " asked on their own"
}

// updateQueueList routes a key while the list holds the keyboard. Every key
// is the selector's while it is up — the card's own letters included, because
// a surface that holds the keyboard answers its own keys and not those of the
// one it is standing in front of
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
func (m Model) updateQueueList(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	open := m.queueList
	done, res := open.sel.Update(msg)
	if !done {
		m.syncViewport()
		return m, nil
	}
	if res.Canceled {
		// Back to the card with the queue exactly as it was: nothing
		// answered, nothing marked, and the decision the card is showing
		// still waiting
		// (docs/interface/principles.md#esc-is-always-the-safe-answer).
		m.queueList = nil
		m.syncViewport()
		return m, nil
	}
	return m.answerQueueList(res.Indices)
}

// answerQueueList carries out the list: the checked rows are allowed and the
// unchecked denied, each by the path the same answer takes on a card of its
// own.
//
// Only the head is acted on now, because only the head is at the head. The
// rest are marked and read when their turn comes, which is the property the
// key had before it was a list: a call allowed here may be inadmissible by
// the time it runs — a preceding call changed the tree, a hook fired, the
// mode moved — and the queue drains front to back so that every one of them
// is asked those questions again where the answers are current.
func (m Model) answerQueueList(idx []int) (tea.Model, tea.Cmd) {
	open := m.queueList
	m.queueList = nil
	checked := make(map[int]bool, len(idx))
	for _, i := range idx {
		checked[i] = true
	}
	if m.batchAnswered == nil {
		m.batchAnswered = make(map[string]bool, len(open.ids))
	}
	for i, id := range open.ids[1:] {
		m.batchAnswered[id] = checked[i+1]
	}
	req := m.pendingApproval
	if req == nil {
		return m, nil
	}
	if !checked[0] {
		// The reader's own no, drawn as one: the same row, the same reason
		// code and the same result the plain key produces
		// (docs/capabilities/approvals-and-safety.md#denials-are-two-different-facts).
		return m.declineApproval()
	}
	m.recordDecision(observe.DecisionAllow, observe.ReasonUserBatch)
	if req.kind == approvalExec {
		return m.executeRun()
	}
	return m.executeApprovedTool()
}

// takeQueueAnswer reports how the list answered this call, if it answered it,
// consuming the answer either way.
//
// An allow is re-checked here rather than trusted: it was given before the
// calls ahead of this one ran, and plan mode, a safety flag or a path outside
// the working scope may have arrived since. A refused allow falls through to
// the policy below it and is asked afresh, which is what happens to a call
// nobody answered.
//
// A denial needs no such check. Nothing that could have changed makes a
// refused call admissible, and a reader who unchecked a row is owed that
// answer whatever the tree did in the meantime.
func (m *Model) takeQueueAnswer(req *approvalRequest) (allow, answered bool) {
	mark, ok := m.batchAnswered[req.call.ID]
	if !ok {
		return false, false
	}
	delete(m.batchAnswered, req.call.ID)
	if !mark {
		return false, true
	}
	act := m.approvalAction(req)
	if m.policy.mode == agent.ModePlan || act.SafetyFlagged || len(act.OutOfScope) > 0 {
		return false, false
	}
	return true, true
}

// armConfirm shows the confirm prompt for the pending decision, resolving the
// queue strip and the set the queue key would list alongside it.
func (m *Model) armConfirm(req *approvalRequest) {
	m.pendingQueue, m.pendingBatch = m.resolveQueue(req)
	// A list open over the last decision is not a list over this one: it was
	// answered, or escaped, before this card was armed.
	m.queueList = nil
	// setTurnState resets the card's scroll along with the keyboard: every
	// arrival at a decision passes through it, this one included.
	m.setTurnState(stateConfirmRun)
	m.syncViewport()
}

// clearQueueStrip drops the strip for a decision that has no queue behind it
// — /run, which is the user's own command and never queued — and with it the
// list, which is that strip opened and cannot outlive it.
func (m *Model) clearQueueStrip() {
	m.pendingQueue, m.pendingBatch, m.queueList = components.QueueStrip{}, nil, nil
}
