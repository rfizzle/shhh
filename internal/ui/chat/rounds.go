package chat

// The round-limit pause (docs/interface/surfaces.md#the-recovery-row).
//
// A turn that used up its tool rounds used to end on one grey line telling
// you to send a message to keep going. Everything a reader needed was
// missing: what the agent had managed before it stopped, whether the tests
// still covered it, and whether carrying on meant the same turn or a new one.
// The limit is a checkpoint now — the last of the three recovery verbs, and
// the only one that is not a failure at all: nothing broke, the turn reached
// a bound the session set for it.
//
// The row stands in for the turn's close block rather than sitting above one.
// It already says what the turn did, what it changed and what the ways on
// are, and a second block offering [v] and [u] beside it would be the same
// answer twice.

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// roundGrantBlock is the first grant, and the step every later one grows by.
// Grants escalate rather than repeat: a stop you have already answered with
// "keep going" is not worth asking again at the same interval, so each grant
// is the whole budget granted so far plus another block — which doubles it
// (50, then 100, then 200, then 400). Three presses put the ceiling past any
// turn that has ever finished, so the checkpoint puts itself out instead of
// becoming a toll collected every few minutes.
const roundGrantBlock = 50

// keys.Row.Rounds is the keystroke that takes the offer, and keys.Row.Uncap
// the one that ends the question for the rest of the turn. The row
// draws the grant as `[+50]` — the block, not the keystroke — because both
// design surfaces do (docs/interface/surfaces.md#the-recovery-row and
// ui_kits/cockpit/Edges.html in the shhh Design System project); focus mode's
// hint line names the literal keys, which is where the reader looks for one.

// uncapRoundsLabel is the second offer, which appears only once the first has
// been taken (see roundPause.keys). It buys the rest of the turn outright: no
// further stops, the rail counting up against no bound. It is the session's
// version of `--max-rounds 0`, and like the grant it expires with the turn.
const uncapRoundsLabel = "let it run"

// roundPause is one stop at the tool-round ceiling: what the turn had spent
// and what it had changed at the moment it stopped. The counts are a snapshot
// rather than a live read of the changeset, because the row is a record of
// where the turn got to — a granted turn goes on changing files, and a record
// that rewrites itself underneath you is worse than a stale one.
type roundPause struct {
	// turn is the turn that stopped; [v] and [u] act on it.
	turn int64
	// used and limit are the counter as the rail reported it.
	used, limit int
	// granted is how many rounds this turn had already been given before
	// this stop. It is the one fact that distinguishes the second pause from
	// the first: it is what the qualifier reports, it sizes the next grant,
	// and its being non-zero is what puts [!] on the row.
	granted int
	// files, added and removed are the turn's changeset when it stopped.
	files          int
	added, removed int
	// stale marks a turn whose last edit landed after the last thing that
	// checked it. It is the difference between "it stopped" and "it stopped
	// halfway through something".
	stale bool
	// spent marks an offer that has been taken, or one belonging to a turn
	// the session has moved past. The row keeps its words and loses the key,
	// like every other spent offer on a failure row.
	spent bool
}

// pauseAtRoundLimit stops the turn at its ceiling and puts the checkpoint on
// screen. The conversation is untouched: the round that just finished left
// its results in it, so granting more rounds is the request the loop was
// about to make and nothing is re-asked.
func (m Model) pauseAtRoundLimit() (tea.Model, tea.Cmd) {
	p := &roundPause{
		turn:    m.turnCount,
		used:    m.agent.Rounds(),
		limit:   m.effectiveMaxToolRounds(),
		granted: m.roundGrant,
		stale:   checksStale(m.turnEntries()),
	}
	if t, ok := m.changes.Turn(m.turnCount); ok {
		p.files, p.added, p.removed = t.Files(), t.Added, t.Removed
	}
	m.roundPause = p
	m.appendEntry(entry{kind: entryRoundPause, turn: m.turnCount, pause: p, duration: m.turnElapsed()})
	// The turn is over as far as every other path is concerned — this is the
	// one transition back to the input, and the close it would append is the
	// row above (appendTurnClose).
	m.setTurnState(stateInput)
	m.syncViewport()
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoBottom()
	return m, m.autosaveCmd()
}

// pausedAtRoundLimit reports whether the current turn's last word was the
// pause. It is what keeps the round counter on the rail after the turn has
// handed the keyboard back, and what tells the close block it is not needed.
func (m Model) pausedAtRoundLimit() bool {
	return m.roundPause != nil && !m.roundPause.spent && m.roundPause.turn == m.turnCount
}

// checksStale reports whether the turn's last edit landed after the last time
// anything checked the code — a quality gate, or a command that runs the
// suite. A turn that edited nothing makes no claim either way.
func checksStale(es []entry) bool {
	lastEdit, lastCheck := -1, -1
	for i, e := range es {
		switch {
		case e.kind == entryDiff:
			lastEdit = i
		case e.kind == entryTool && e.toolName == quality.ToolName:
			lastCheck = i
		case e.kind == entryCommand && isTestCommand(e.text):
			lastCheck = i
		}
	}
	return lastEdit >= 0 && lastCheck < lastEdit
}

// roundCounter is the vitals rail's round segment: the counter, plus the
// block on offer while a stop is standing, so the rail says both what the
// bound is and what taking the offer would make it.
func (m Model) roundCounter() string {
	s := m.roundLabel()
	if m.pausedAtRoundLimit() {
		s += fmt.Sprintf(" +%d", m.roundPause.grant())
	}
	return s
}

// roundLabel is the counter on its own, shared with the close block's note
// . A turn running without a ceiling keeps the counter's shape and puts
// `∞` where the bound would be: the rail must not invent a number that does
// not exist, and it cannot say so in words either — its segments are joined
// with `·`, so `round 7 · no bound` would read as two facts rather than one.
func (m Model) roundLabel() string {
	if m.roundsUnbounded() {
		return fmt.Sprintf("round %d/∞", m.agent.Rounds())
	}
	return fmt.Sprintf("round %d/%d", m.agent.Rounds(), m.effectiveMaxToolRounds())
}

// roundPauseRow renders the pause on the column grid, under the `rounds` verb
// it
// shares with nothing else. `⚠` rather than `✗`: the turn is recoverable, and
// the row exists to say how.
func (m Model) roundPauseRow(e entry) components.RecoveryRow {
	p := e.pause
	if p == nil {
		return components.RecoveryRow{}
	}
	return components.RecoveryRow{
		State:     components.RecoveryStalled,
		Verb:      components.VerbRounds,
		Subject:   fmt.Sprintf("%d of %d used", p.used, p.limit),
		Qualifier: p.qualifier(),
		Outcome:   "stopped",
		Duration:  turnDuration(e.duration),
		Detail:    []string{p.detail()},
		Keys:      p.keys(),
	}
}

// qualifier is the dim half of the target field: why this number is the
// number. A turn that has already been granted rounds says so, because
// "35 of 35 used" on its own reads like a limit nobody chose.
func (p roundPause) qualifier() string {
	if p.granted > 0 {
		return fmt.Sprintf("%d already granted", p.granted)
	}
	return "the turn's own bound"
}

// detail is what the turn managed, which is the question the row is
// answering. Every clause is conditional on the thing it names: a turn that
// changed nothing says so rather than reporting three zeroes, and one whose
// edits are still covered by a check says nothing about the suite.
func (p roundPause) detail() string {
	if p.files == 0 {
		return "nothing changed"
	}
	d := fmt.Sprintf("%s changed +%d −%d", plural(p.files, "file"), p.added, p.removed)
	if p.stale {
		d += " · the suite has not been re-run since"
	}
	return d
}

// grant is the block this stop offers: everything the turn has been granted
// already, plus another block, which is the same thing as doubling the grant
// each time (roundGrantBlock). The first stop offers 50, the second 100, the
// third 200.
func (p roundPause) grant() int { return p.granted + roundGrantBlock }

// keys are the ways on. Reviewing and undoing are offered only when there is
// a changeset to act on — a key that cannot be honoured is not offered — and
// both offers go once either has been taken.
//
// `[!]` appears only from the second stop, because the first one is the
// checkpoint doing its job: you have not yet seen this turn stopped, so the
// question it asks is worth asking. Once you have answered it, offering to
// stop asking is the more useful of the two answers.
func (p roundPause) keys() []components.KeyOffer {
	var offers []components.KeyOffer
	if p.files > 0 {
		offers = append(offers, components.KeyOffer{Key: keys.Bracket(keys.Row.Review), Label: "review what it did"})
	}
	if !p.spent {
		offers = append(offers, components.KeyOffer{Key: fmt.Sprintf("[+%d]", p.grant()), Label: "more rounds"})
		if p.granted > 0 {
			offers = append(offers, components.KeyOffer{Key: keys.Bracket(keys.Row.Uncap), Label: uncapRoundsLabel})
		}
	}
	if p.files > 0 {
		offers = append(offers, components.KeyOffer{Key: keys.Bracket(keys.Row.Undo), Label: "undo the turn"})
	}
	return offers
}

// roundPauseOffers is the hint bar's version of the same offers. The two
// differ in exactly one place and for one reason: the row draws the grant as
// the block it grants ([+100]), while the bar has to name the key that takes
// it, or it is advertising a keystroke nobody can type — so the bar's label
// carries the number the row's bracket did.
func roundPauseOffers(p *roundPause) []components.KeyOffer {
	var offers []components.KeyOffer
	if p.files > 0 {
		offers = append(offers, components.KeyOffer{Key: keys.Bracket(keys.Row.Review), Label: "review what it did"})
	}
	if !p.spent {
		offers = append(offers, components.KeyOffer{Key: keys.Bracket(keys.Row.Rounds), Label: fmt.Sprintf("%d more rounds", p.grant())})
		if p.granted > 0 {
			offers = append(offers, components.KeyOffer{Key: keys.Bracket(keys.Row.Uncap), Label: uncapRoundsLabel})
		}
	}
	if p.files > 0 {
		offers = append(offers, components.KeyOffer{Key: keys.Bracket(keys.Row.Undo), Label: "undo the turn"})
	}
	return offers
}

// focusedRoundPause returns the pause row the focus cursor is on, if it is on
// one. Like every recovery row it lives in the session's own transcript, so
// an attached child's feed never offers it.
func (m Model) focusedRoundPause() (entry, bool) {
	if m.attachedTo != "" || m.focusIdx < 0 || m.focusIdx >= len(m.transcript) {
		return entry{}, false
	}
	e := m.transcript[m.focusIdx]
	if e.kind != entryRoundPause || e.pause == nil {
		return entry{}, false
	}
	return e, true
}

// roundPauseKey routes a keystroke to the focused pause row, reporting false
// when the row is not claiming it — which leaves the changeset row's own [v]
// and [u] exactly as they were.
func (m Model) roundPauseKey(key string) (tea.Model, tea.Cmd, bool) {
	e, ok := m.focusedRoundPause()
	if !ok {
		return m, nil, false
	}
	p := e.pause
	switch key {
	case keys.Shown(keys.Row.Review):
		if p.files == 0 {
			return m, nil, false
		}
		next, cmd := m.openReview(e.turn)
		return next, cmd, true
	case keys.Shown(keys.Row.Undo):
		if p.files == 0 {
			return m, nil, false
		}
		next, cmd := m.undoTurn(e.turn, nil)
		return next, cmd, true
	case keys.Shown(keys.Row.Rounds):
		if p.spent {
			return m, nil, false
		}
		next, cmd := m.grantRounds(p)
		return next, cmd, true
	case keys.Shown(keys.Row.Uncap):
		if p.spent || p.granted == 0 {
			return m, nil, false
		}
		next, cmd := m.uncapRounds(p)
		return next, cmd, true
	}
	return m, nil, false
}

// grantRounds raises this turn's ceiling and lets it carry on. It is the same
// turn in every sense the session accounts for one: the conversation is not
// added to, the round counter is not reset, the changeset keeps collecting
// under the same turn number, and the accounting is reopened rather than
// started again — so the turn closes once, priced as one thing, with one
// changeset for [u] to take back.
func (m Model) grantRounds(p *roundPause) (tea.Model, tea.Cmd) {
	if m.working() {
		return m.systemNotice("The turn is already running again.")
	}
	m.roundGrant += p.grant()
	return m.resumeGrantedTurn(p)
}

// uncapRounds takes the second offer: the rest of the turn runs with no
// ceiling at all and no further stops, the rail counting up against no bound
// . Everything else is the grant — the same turn, the same changeset, one
// close at the end — and like the grant it lasts exactly one turn, because a
// session that should never stop says so once, at the command line
// (`--max-rounds 0`), rather than by a key pressed in the middle of a turn.
//
// The escape from a turn told to run is the one it always was: interrupting
// it. That is the trade the key states, and the reason it is not offered
// until the checkpoint has already stopped the turn once.
func (m Model) uncapRounds(p *roundPause) (tea.Model, tea.Cmd) {
	if m.working() {
		return m.systemNotice("The turn is already running again.")
	}
	m.roundsUncapped = true
	return m.resumeGrantedTurn(p)
}

// resumeGrantedTurn is what both offers do once they have decided the new
// bound: spend the offer and start the turn's next round.
func (m Model) resumeGrantedTurn(p *roundPause) (tea.Model, tea.Cmd) {
	p.spent = true
	m.roundPause = nil
	m.invalidateRenderCache()
	m.turnOpen, m.turnOutcome = true, components.TurnDone
	m.turnEnded = time.Time{}
	m.vitals.reopenTurn()
	m.setTurnState(stateStreaming)
	m.streaming = ""
	m.atBottom = true
	m.trimForRequest()
	m.syncViewport()
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoBottom()
	return m, tea.Batch(m.requestStream(), m.autosaveCmd())
}

// resetRounds starts a turn's tool-round budget over: the counter, whatever
// [+50] added to it, whether [!] lifted it altogether, and the offer that did
// either. Fresh user input, a rewind, a retry and a compaction all get the
// configured ceiling back — and the outstanding offer is spent rather than
// dropped, because a turn the session has moved past cannot be given more
// rounds, and the row must not go on saying it can.
//
// A session started uncapped (`--max-rounds 0`) is untouched by this: that
// bound is the Agent's and belongs to the session, not to the turn.
func (m *Model) resetRounds() {
	m.agent.ResetRounds()
	m.roundGrant = 0
	m.roundsUncapped = false
	if m.roundPause != nil {
		m.roundPause.spent = true
		m.roundPause = nil
		m.invalidateRenderCache()
	}
}
