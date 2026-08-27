package chat

// The round-limit pause (S-109, DESIGN-TUI.md §17a).
//
// A turn that used up its tool rounds used to end on one grey line telling you
// to send a message to keep going. Everything a reader needed was missing:
// what the agent had managed before it stopped, whether the tests still
// covered it, and whether carrying on meant the same turn or a new one. The
// limit is a checkpoint now — the last of §17's three recovery verbs, and the
// only one that is not a failure at all: nothing broke, the turn reached a
// bound the session set for it.
//
// The row stands in for the turn's close block rather than sitting above one.
// It already says what the turn did, what it changed and what the ways on are,
// and a second block offering [v] and [u] beside it would be the same answer
// twice (S-098).

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// roundGrantSize is one block of rounds. It is what [+10] grants and what the
// vitals rail advertises while the offer stands, so the ceiling the counter
// reports after a grant is the one the row promised.
const roundGrantSize = 10

// grantRoundsKey is the keystroke that takes the offer. The row draws it as
// `[+10]` — the grant, not the keystroke — because both design surfaces do
// (DESIGN-TUI.md §17a and ui_kits/cockpit/Edges.html in the shhh Design System
// project); focus mode's hint line names the literal key, which is where the
// reader looks for one.
const grantRoundsKey = "+"

// grantRoundsOffer and grantRoundsLabel are the offer as it reads on the row:
// the bracket is the grant, the words are what it buys. The bracket is derived
// from the size so the two cannot disagree; the label spells the number out,
// which is the one thing here that has to be rewritten by hand if the block
// ever changes size.
var grantRoundsOffer = fmt.Sprintf("[+%d]", roundGrantSize)

const grantRoundsLabel = "ten more rounds"

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
	// this stop, which is the one fact that distinguishes the second pause
	// from the first.
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
	// like every other spent offer in §17a.
	spent bool
}

// pauseAtRoundLimit stops the turn at its ceiling and puts the checkpoint on
// screen. The conversation is untouched: the round that just finished left its
// results in it, so granting more rounds is the request the loop was about to
// make and nothing is re-asked.
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
	m.viewport.SetContent(m.renderHistory())
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

// roundPauseRow renders the pause on the §6a grid, under the `rounds` verb it
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

// detail is what the turn managed, which is the question the row is answering.
// Every clause is conditional on the thing it names: a turn that changed
// nothing says so rather than reporting three zeroes, and one whose edits are
// still covered by a check says nothing about the suite.
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

// keys are the three ways on. Reviewing and undoing are offered only when
// there is a changeset to act on — a key that cannot be honoured is not
// offered (§17a) — and the grant goes once it has been taken.
func (p roundPause) keys() []components.KeyOffer {
	var keys []components.KeyOffer
	if p.files > 0 {
		keys = append(keys, components.KeyOffer{Key: "[" + reviewKey + "]", Label: "review what it did"})
	}
	if !p.spent {
		keys = append(keys, components.KeyOffer{Key: grantRoundsOffer, Label: grantRoundsLabel})
	}
	if p.files > 0 {
		keys = append(keys, components.KeyOffer{Key: "[" + undoKey + "]", Label: "undo the turn"})
	}
	return keys
}

// roundPauseHint is the focus-mode hint for a pause row: the same offers the
// row makes, with the grant named by the key that takes it rather than by the
// block it grants.
func roundPauseHint(p *roundPause) string {
	var parts []string
	if p.files > 0 {
		parts = append(parts, reviewKey+" review")
	}
	if !p.spent {
		parts = append(parts, grantRoundsKey+" "+grantRoundsLabel)
	}
	if p.files > 0 {
		parts = append(parts, undoKey+" undo turn")
	}
	if len(parts) == 0 {
		return "nothing left to offer"
	}
	return strings.Join(parts, " · ")
}

// focusedRoundPause returns the pause row the focus cursor is on, if it is on
// one. Like every recovery row it lives in the session's own transcript, so an
// attached child's feed never offers it (S-077).
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
	case reviewKey:
		if p.files == 0 {
			return m, nil, false
		}
		next, cmd := m.openReview(e.turn)
		return next, cmd, true
	case undoKey:
		if p.files == 0 {
			return m, nil, false
		}
		next, cmd := m.undoTurn(e.turn, nil)
		return next, cmd, true
	case grantRoundsKey:
		if p.spent {
			return m, nil, false
		}
		next, cmd := m.grantRounds(p)
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
	p.spent = true
	m.roundPause = nil
	m.roundGrant += roundGrantSize
	m.invalidateRenderCache()
	m.turnOpen, m.turnOutcome = true, components.TurnDone
	m.turnEnded = time.Time{}
	m.vitals.reopenTurn()
	m.setTurnState(stateStreaming)
	m.streaming = ""
	m.atBottom = true
	m.trimForRequest()
	m.syncViewport()
	m.viewport.SetContent(m.renderHistory())
	m.viewport.GotoBottom()
	return m, tea.Batch(m.requestStream(), m.autosaveCmd())
}

// resetRounds starts a turn's tool-round budget over: the counter, whatever
// [+10] added to it, and the offer that added it. Fresh user input, a rewind,
// a retry and a compaction all get the configured ceiling back — and the
// outstanding offer is spent rather than dropped, because a turn the session
// has moved past cannot be given more rounds, and the row must not go on
// saying it can.
func (m *Model) resetRounds() {
	m.agent.ResetRounds()
	m.roundGrant = 0
	if m.roundPause != nil {
		m.roundPause.spent = true
		m.roundPause = nil
		m.invalidateRenderCache()
	}
}
