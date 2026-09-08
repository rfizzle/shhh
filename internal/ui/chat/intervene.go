package chat

// Delivering an interruption the agent decided on.
//
// The decision — steer, early check-in, or the interval's own — is
// agent.NextIntervention, shared with every headless run and every sub-agent
// (internal/agent/intervene.go). What is left here is the two things a
// session does that a background run does not: it shows the reader what
// happened, and it records the signal.

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// considerVerdict offers a fresh reading to the agent's policy. It only ever
// queues; the round boundary delivers.
//
// Both bounds are counted in the interval in force rather than the configured
// one: a session backing off from a failing summariser reads half as often,
// and a cooldown that did not widen with it would let two interventions land
// on consecutive readings.
//
// A reading is judged for age where it is applied, which for a session is the
// moment it lands rather than a later boundary — there is nowhere else it
// waits. An interruption withheld because the reading described a round the
// session has long since passed is recorded and nothing more: no message
// joins the conversation, so there is nothing to show the reader, and the row
// on the rail is already the reading itself.
func (m *Model) considerVerdict(v agent.SummaryVerdict) {
	m.agent.SetInterveneBounds(m.summaryInterval(), m.summarizer.Config().CooldownIntervals())
	if reason := m.agent.ConsiderVerdict(v, m.agent.Rounds(), m.working()); reason != "" {
		m.signal(observe.SignalIntervene, reason)
	}
}

// WithSteering installs the interruption machinery's tuning: the thresholds
// and the wordings the config file overrode, or a zero value for the
// built-in set.
func (m Model) WithSteering(s agent.Steering) Model {
	m.agent.SetSteering(s)
	return m
}

// injectInterventions delivers whatever the round boundary owes, and shows it.
func (m *Model) injectInterventions() {
	iv, ok := m.agent.NextIntervention(m.summaryTarget)
	if !ok {
		return
	}
	m.agent.AppendMachine(iv.Message)
	// One round for both the row the reader sees and the row the next digest
	// is told about, so a withdrawal can unsay exactly the line it put there.
	round := m.agent.Rounds()
	// Stamped with the turn and holding the interruption itself, because the
	// notice is also the place the reader takes it back from
	// (withdrawSteer below).
	m.appendEntry(entry{
		kind:       entrySystem,
		text:       iv.Notice,
		turn:       m.turnCount,
		intervened: &interveneRow{iv: iv, row: iv.Row(round)},
	})
	m.signal(observe.SignalIntervene, iv.Kind.Signal())
	// Written down where the reading schedule can see it (summary.go): a
	// turn that ends on the model's answer to this message still closes on a
	// fresh reading even though no further round was taken, the next reading
	// falls due a few rounds from here rather than a whole interval away,
	// and the reader taking it is told what was said instead of being handed
	// the evidence that earned it and asked to revise its own verdict.
	m.summary.noteIntervention(iv, round)
	// A row was appended, so the pane is redrawn the way every other system
	// row is; the resize hook alone would leave it unseen until the next
	// stream flush.
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoBottom()
}

// Taking a steer back (docs/capabilities/coding-agent.md#a-steer-can-be-taken-back).
//
// The check that steers a turn is a cheap model reading a digest, and the
// steer above is written to ask rather than to accuse precisely because it is
// wrong often enough to matter. What the reader could do about a wrong one
// was nothing: the message was already in the conversation, the model was
// already re-orienting around it, and the next reading would say the same
// thing a couple of intervals later. So the notice carries `[u]`, and the
// whole of what it costs to disagree with the machinery is one key.
//
// Only a steer offers it. The interval's own check-in is the floor beneath
// this machinery rather than a verdict of it — nothing has been alleged, and
// a row offering to switch off the last thing watching an unattended turn is
// an offer nobody should be given. The early check-in a sufficiency reading
// buys is the same message arriving sooner, which is the same reasoning.

// interveneRow is what an interruption's notice holds: the interruption
// itself, so the row can take it back, and whether the reader already has.
//
// A pointer on the entry for the reason a round-limit pause's is one —
// withdrawing spends the offer wherever the row is drawn from — and the
// whole Intervention rather than its words because taking one back has to
// know what kind it was and what the next digest was told about it.
type interveneRow struct {
	iv agent.Intervention
	// row is what noteIntervention put in the digest for this interruption,
	// kept so a withdrawal can unsay exactly that line rather than guess at
	// the round it was stamped with.
	row       string
	withdrawn bool
}

// withdrawableSteer reports whether an entry's `[u]` is still an offer: it is
// a steer, nothing has withdrawn it yet, and the turn it interrupted is the
// turn still running.
//
// A finished turn's notice keeps its words and loses its key. What
// withdrawing buys is the round the model would spend re-orienting and the
// readings that would go on saying the same thing, and a turn that is over
// has already spent the first and will take none of the second — worse, it
// has already answered the steer, so removing the question would leave the
// answer stranded in the conversation with nothing above it.
//
// The turn is read off turnOpen rather than off working(): a turn holding an
// approval card is not working and has not ended either, and an offer that
// blinked out under every card and back afterwards would be one nobody could
// rely on being there.
func (m Model) withdrawableSteer(e entry) bool {
	return e.intervened != nil && !e.intervened.withdrawn &&
		e.intervened.iv.Kind == agent.InterveneSteer &&
		e.turn == m.turnCount && m.turnOpen
}

// expireSteerOffers says the transcript has to be drawn again because a
// take-back has just gone out of date. The turn's end is the one moment that
// happens without anything landing in the transcript, and a block that froze
// into the render cache while the turn ran is still painting the offer — grey
// beside a live draft, but painting it.
//
// It scans rather than remembering, because the alternative is a second piece
// of state that has to be kept in step with the row it describes; the scan is
// a nil check per entry, once per turn.
func (m *Model) expireSteerOffers() {
	for _, e := range m.transcript {
		if e.intervened != nil && !e.intervened.withdrawn &&
			e.intervened.iv.Kind == agent.InterveneSteer && e.turn == m.turnCount {
			m.invalidateRenderCache()
			return
		}
	}
}

// steerOffers are the keys the notice carries. `[u]` is keys.Row.Undo with
// this row's own words, the way `[r]` is one key on two rows that mean
// different things by it: the register declares the keystroke, and what it
// does is the row's to say.
func (m Model) steerOffers(e entry) []components.TurnKey {
	if !m.withdrawableSteer(e) {
		return nil
	}
	return []components.TurnKey{{Key: keys.Bracket(keys.Row.Undo), Label: "take the steer back"}}
}

// steerOfferLine is that run as the line the notice draws under itself, or ""
// where the row makes no offer. It is indented under the notice the way every
// other detail body is, and it goes grey unless reading mode's cursor is
// standing here (inertkeys.go).
func (m Model) steerOfferLine(e entry, keysLive bool) string {
	offers := m.steerOffers(e)
	if len(offers) == 0 {
		return ""
	}
	run := components.KeyRun(offers, !keysLive, m.rowHandover(keysLive))
	if run == "" {
		return ""
	}
	return "\n" + strings.Repeat(" ", components.GridDetailIndent) + run
}

// focusedSteerNotice is the withdrawable steer notice the reading cursor is
// standing on. Like every other row offer it is the session's own transcript
// only: an attached child's feed is not a place a parent's steer can be
// taken back from.
func (m Model) focusedSteerNotice() (entry, bool) {
	if m.attachedTo != "" || m.focusIdx < 0 || m.focusIdx >= len(m.transcript) {
		return entry{}, false
	}
	if e := m.transcript[m.focusIdx]; m.withdrawableSteer(e) {
		return e, true
	}
	return entry{}, false
}

// withdrawSteer routes a keystroke to the focused steer notice, reporting
// false when the row is not claiming it — which leaves `[u]` on a changeset
// row and the pager's half page exactly as they were.
//
// The record is not written here. A withdrawal is the cheapest evidence the
// thresholds have that the check was wrong about a turn, and where that is
// filed is a question about an intervention's outcome — one place, one row,
// joined forward to the reading that follows it.
func (m Model) withdrawSteer(key string) (tea.Model, tea.Cmd, bool) {
	if !keys.Is(key, keys.Row.Undo) {
		return m, nil, false
	}
	e, ok := m.focusedSteerNotice()
	if !ok {
		return m, nil, false
	}
	if !m.agent.WithdrawIntervention(e.intervened.iv) {
		// Compaction is the way this happens: the steer was folded into a
		// summary of the conversation, and there is no message left to take
		// out. The row says so rather than claiming a withdrawal it did not
		// perform.
		next, cmd := m.focusNotice("That steer is no longer in the conversation to take back.")
		return next, cmd, true
	}
	e.intervened.withdrawn = true
	m.summary.dropIntervention(e.intervened.row)
	m.transcript[m.focusIdx].text = withdrawnNotice(e.intervened.iv)
	m.invalidateRenderCache()
	m.refreshFocusView()
	return m, nil, true
}

// withdrawnNotice is what the row says once the steer has gone: that the
// message left, that it was never answered, and what the machinery will not
// do for the rest of the turn. It keeps the reading's own reason, because the
// row is still the record of what the check believed — the reader disagreed
// with it, which is not the same as it never having been said.
func withdrawnNotice(iv agent.Intervention) string {
	const tail = "The message left the conversation unanswered, and no further reading steers this turn."
	if reason := strings.TrimSpace(iv.Reason); reason != "" {
		return fmt.Sprintf("Steer withdrawn — %s. %s", reason, tail)
	}
	return "Steer withdrawn — " + tail
}
