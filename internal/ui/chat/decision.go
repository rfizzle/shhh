package chat

import "github.com/rfizzle/shhh/internal/ui/components"

// decision holds the prompts a turn can be waiting on beside the approval
// card (approvalstate.go), embedded in the Model so its fields are read as the
// Model's own. Together with the card's request and the queue's answers they
// are the turn's pending decisions, and a turn that is cancelled drops all of
// them in one place: Model.clearDecisions.
type decision struct {
	// memoryAsk is the memory confirm while it is up (memory.go).
	memoryAsk *components.NoteSelect
	// question is the card the model's own question is being asked on, and
	// nil whenever none is outstanding (question.go). It rides beside
	// approval.request rather than inside it because the request is what the
	// call is and the card is what the reader is looking at.
	question *questionCard
}

// clearPrompts drops both prompts.
func (d *decision) clearPrompts() {
	d.memoryAsk, d.question = nil, nil
}

// clearDecisions drops every decision a turn was waiting on: the approval card,
// the queue's strip and answers, and the two prompts. A cancel does it as the
// turn ends; a turn that finishes or breaks has none outstanding.
func (m *Model) clearDecisions() {
	m.approval.request = nil
	// The queue the strip described is gone with the turn, and so is every
	// answer the list gave against it.
	m.clearQueueStrip()
	m.approval.batchAnswered, m.approval.total = nil, 0
	// A card left behind is not merely a stale pointer: the grace window
	// reads the question to decide which keys a burst may not answer
	// (interrupt.go), so the next decision to arrive would be protected by the
	// wrong card's keys. A question the reader had handed to the draft goes
	// with it too, which is what makes the next message an ordinary message
	// again (question.go).
	m.clearPrompts()
}
