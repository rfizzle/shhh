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
// safety-flagged action belongs to no batch: it is taken out and asked on its
// own, whatever else is in the queue.
//
// Like the blast-radius block beside it, the strip is resolved once, when the
// decision is armed — it previews every queued call, which reads the files
// the edits would change, and View runs on every frame.

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/radius"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/components"
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

// resolveQueue builds the strip above the card and the batch [A] would
// answer. cur is the decision being shown — already built by the caller, so
// its diff is not computed twice — and is the head of the queue it describes.
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
		strip.Note = fmt.Sprintf("[A] answers the %d marked", len(batch)+1)
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

// skippedArgsNotice is the row for a call the queue could not build a
// decision from: the model wrote arguments the tool cannot read, and there is
// nothing for a person to do about it but read the model's next attempt.
const skippedArgsNotice = "Skipped a tool call with invalid arguments."

// skippedCallEntry is the transcript's account of a call the queue refused
// before it could reach a card.
//
// A file that changed since it was read is not a malformed call, and saying
// so cost the reader the one fact only they have: which editor, sibling
// session or background build touched the file. So that refusal gets its own
// row, naming the file and what happened to it, and folds the model's own
// sentence underneath rather than dropping it — the row is dense, not
// shorter (docs/interface/principles.md#fold-never-hide).
func (m Model) skippedCallEntry(err error) entry {
	var stale tools.StaleError
	if !errors.As(err, &stale) {
		return entry{kind: entrySystem, text: skippedArgsNotice}
	}
	return entry{
		kind: entrySystem,
		text: stale.Skipped(m.rowPath(stale.Path)),
		// The expansion is the sentence the model was given, verbatim: a
		// reader deciding whether the model can recover needs to see what it
		// was actually told, not this row's paraphrase of it.
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
	if act.SafetyFlagged || act.Kind == agent.ActionOther || len(act.OutOfScope) > 0 {
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

// approveBatch marks every member of the resolved batch approved, so each one
// runs when it reaches the head of the queue instead of being asked again.
// Nothing is executed out of order: the queue still drains front to back, and
// each member is re-checked against the mode and the safety checker on its
// way through.
func (m *Model) approveBatch() {
	if m.batchApproved == nil {
		m.batchApproved = make(map[string]bool, len(m.pendingBatch))
	}
	for _, id := range m.pendingBatch {
		m.batchApproved[id] = true
	}
}

// takeBatchApproval reports whether this call was answered by an earlier [A],
// consuming the grant either way. It refuses to honour one in plan mode or
// over a flagged action — the batch was built without flagged members, and
// the mode can have changed since the key was pressed.
func (m *Model) takeBatchApproval(req *approvalRequest) bool {
	if !m.batchApproved[req.call.ID] {
		return false
	}
	delete(m.batchApproved, req.call.ID)
	act := m.approvalAction(req)
	return m.mode != agent.ModePlan && !act.SafetyFlagged && len(act.OutOfScope) == 0
}

// armConfirm shows the confirm prompt for the pending decision, resolving the
// queue strip and the batch [A] would answer alongside it.
func (m *Model) armConfirm(req *approvalRequest) {
	m.pendingQueue, m.pendingBatch = m.resolveQueue(req)
	// setTurnState resets the card's scroll along with the keyboard: every
	// arrival at a decision passes through it, this one included.
	m.setTurnState(stateConfirmRun)
	m.syncViewport()
}

// clearQueueStrip drops the strip for a decision that has no queue behind it
// — /run, which is the user's own command and never queued.
func (m *Model) clearQueueStrip() {
	m.pendingQueue, m.pendingBatch = components.QueueStrip{}, nil
}
