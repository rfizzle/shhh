package chat

// The start screen's reading
// (docs/capabilities/chat.md#the-start-screen-is-read-for-this-checkout).
//
// Once the start screen has drawn, a cheap model reads what the checkout says
// about itself — the instruction block as the prompt got it, the fact line,
// the gate, the changed files' names, the recent commit subjects and the
// backlog's ready items — and writes at most two offers the fixed table
// cannot. The rules are the offered next step's:
//
//   - It is never a message. A written offer is a row; choosing it sends its
//     line as the person's own, through every gate a typed line goes
//     through, and nothing else of the reading is saved or shown to the
//     model.
//   - It lands only where nothing has moved: a reading that comes back after
//     a key, a chosen row or a turn is dropped, and the record says so. A
//     failed or slow one changes nothing, and the screen never says one is
//     out — the fixed rows are the screen, not a placeholder.
//   - It takes read-only rows only (readOnlyOffers), and the price on them
//     is the slot's: the writer does not set it.
//   - behavior.suggestions and /ui suggest are its switch too; off asks for
//     nothing and draws nothing written.

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
)

// startOffersState is the reading and its writer. written is the one thing a
// frame reads.
type startOffersState struct {
	writer *agent.StartOfferer
	// gather reads what the screen does not already hold — git and the
	// backlog — on the reading's goroutine, so the screen never waits for it.
	gather func(context.Context) agent.StartOffersRequest
	// asked latches: a session is read once, at its open.
	asked bool
	// keysAt is when the keyboard had last been touched as the reading was
	// asked for; a reading that lands after another key is dropped.
	keysAt  time.Time
	cancel  context.CancelFunc
	written []agent.StartOffer
}

// startOffersDoneMsg carries the reading back.
type startOffersDoneMsg struct {
	verdict agent.StartOffersVerdict
}

// WithStartOffers wires the start screen's reading: the writer, and what reads
// the rest of the evidence off the UI goroutine. A nil writer asks nothing,
// which is every surface but the interactive coding session.
func (m Model) WithStartOffers(w *agent.StartOfferer, gather func(context.Context) agent.StartOffersRequest) Model {
	m.startOffers.writer = w
	m.startOffers.gather = gather
	return m
}

// startOffersCmd asks for the reading once, the first time the session has a
// size to draw its start screen at and the list is live. It is derived from
// the model after each message, as the close readings are.
func (m *Model) startOffersCmd() tea.Cmd {
	if m.startOffers.asked || !m.ready || m.start == nil {
		return nil
	}
	m.startOffers.asked = true
	// A line handed in at launch is a turn about to start, and a session
	// with the switch off or no model to ask asks nothing.
	if !m.suggest.on || !m.startOffers.writer.Enabled() || m.initialPrompt != "" || !m.startChoosing() {
		return nil
	}
	info := *m.start
	req := agent.StartOffersRequest{
		Instructions: info.Project.Instruction.Block,
		Facts:        startFactsLine(info),
		Gate:         startGateLine(info),
	}
	m.startOffers.keysAt = m.lastKeypress
	writer, gather := m.startOffers.writer, m.startOffers.gather
	ctx, cancel := context.WithCancel(context.Background())
	m.startOffers.cancel = cancel
	return func() tea.Msg {
		defer cancel()
		if gather != nil {
			// Bounded as the request is: a tree that is slow to read is a
			// slow reading, and a slow reading changes nothing.
			gctx, gcancel := context.WithTimeout(ctx, writer.Timeout())
			more := gather(gctx)
			gcancel()
			req.Dirty, req.Commits, req.Ready = more.Dirty, more.Commits, more.Ready
		}
		return startOffersDoneMsg{verdict: writer.Offer(ctx, req)}
	}
}

// finishStartOffers lands a reading, or drops it. A failed reading, or one
// that lands with the switch turned off, changes nothing and files nothing:
// there was nothing to draw. One that lands after the person has moved — a
// key, a chosen row, a turn — is dropped and filed, because the rows they
// were reading must not change under them.
func (m *Model) finishStartOffers(msg startOffersDoneMsg) {
	m.startOffers.cancel = nil
	if msg.verdict.Failed || len(msg.verdict.Offers) == 0 || !m.suggest.on {
		return
	}
	if !m.startChoosing() || m.turnInFlight() || !m.lastKeypress.Equal(m.startOffers.keysAt) {
		m.signal(observe.SignalStartOffer, observe.StartOfferDropped)
		return
	}
	m.startOffers.written = msg.verdict.Offers
	// The rows are pane content: draw them again now, since no resize
	// will.
	m.refreshTranscript()
}

// writtenStartOffers is the landed reading's offers while the switch is on.
func (m Model) writtenStartOffers() []agent.StartOffer {
	if !m.suggest.on {
		return nil
	}
	return m.startOffers.written
}

// stopStartOffers cancels a reading still out.
func (m *Model) stopStartOffers() {
	if m.startOffers.cancel != nil {
		m.startOffers.cancel()
		m.startOffers.cancel = nil
	}
}

// startFactsLine is the fact line as the screen states it.
func startFactsLine(info StartInfo) string {
	facts := startFacts(info.Project)
	parts := make([]string, 0, len(facts))
	for _, f := range facts {
		parts = append(parts, f.Text)
	}
	return strings.Join(parts, " · ")
}

// startGateLine is the gate line as the screen states it.
func startGateLine(info StartInfo) string {
	for _, n := range startNotes(info) {
		if n.Label == "gate" {
			return strings.TrimSpace(n.Value + " — " + n.Detail)
		}
	}
	return ""
}
