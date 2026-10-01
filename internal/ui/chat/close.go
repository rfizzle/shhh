package chat

// Turn summary and changeset row (
// docs/interface/surfaces.md#the-turns-close): a turn closes with the rows
// that answer what it did, what it changed, and whether the checks still
// pass. They are ordinary transcript entries — raw data plus a passive
// renderer — so they re-render at any width like everything else, and focus
// mode is what handles the keys they offer.
//
// Nothing here recomputes what the session already knows: the steps and tools
// come from the turn's own entries, the wall time and spend from the vitals
// history, and the files from the changeset store.

import (
	"context"
	"fmt"
	"strings"

	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/receipt"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// The changeset row is the door to its turn's review: clicked, or selected
// and opened with enter (inertkeys.go). Selected, it also takes the handover,
// which opens the commit card for its turn (commit.go). Taking the turn back
// and running its checks again are commands — /undo and /gate run — and the
// row says so in its note where there is room, rather than offering a letter
// that was a second path to the same act
// (docs/interface/surfaces.md#the-turns-close).

// appendTurnClose closes the turn with its summary rows. It runs where the
// turn's accounting is closed — one place, so a turn cannot end without
// saying what it did — and only for a turn the user actually started: a /run
// finishing is not a turn ending.
func (m *Model) appendTurnClose() {
	if !m.turnOpen {
		return
	}
	m.turnOpen = false
	// A steer's notice offers its take-back only while the turn it
	// interrupted runs (intervene.go). This is the one moment that offer
	// expires, and nothing lands in the transcript to redraw the block it
	// was painted into.
	m.expireSteerOffers()
	// And a grant the reader gave for the length of this turn, beside it and
	// at the same seam: this is the one moment the expiry can be made with
	// the turn's own close in the transcript next to it. It ends where the
	// turn is closed rather than where the next one opens, because a session
	// that stops between the two must not be sitting on a permission it has
	// already told the reader is over (policy.go).
	// See docs/capabilities/approvals-and-safety.md#a-grant-says-when-it-ends.
	m.expireTurnGrants()
	outcome := m.turnOutcomeCode()
	m.recordTurn(outcome)
	// And toward the slot's standing account, beside the record: this is the
	// one place a turn is counted as over (account.go).
	m.noteAccountTurn()
	// A turn that stopped at its round limit has already closed, with the
	// pause row: it states the rounds it used, what it changed, and
	// the ways on, and a second block offering review beside it would be
	// the same answer twice. Granting the rounds spends the pause,
	// so the turn it continues into closes here in the ordinary way.
	if m.pausedAtRoundLimit() {
		return
	}
	// And, where the machinery interrupted this turn, what the interruption
	// came to. Below the pause on purpose, unlike the turn event above it: a
	// granted pause reopens *this* turn rather than starting another
	// (resumeGrantedTurn), so this runs twice for one turn if it runs before
	// the return — and the second row would be counted as a second
	// interrupted turn, against a steer count the reopening deliberately
	// does not reset. A cap-paused turn is a real turn reading and repeats
	// happily; one interrupted turn is one row (intervene.go).
	m.recordIntervened(outcome)
	m.appendEntry(entry{kind: entryTurnClose, turn: m.turnCount, close: m.turnCloseData()})
	// The person's own commands at the turn's end, fired here because this is
	// where the turn's accounting is closed and there is exactly one of these
	// per turn.
	//
	// It is the one seam that runs on the goroutine drawing the screen, and
	// deliberately, which is the opposite of what the pre-tool seam does. A
	// gated call is a decision somebody is looking at, so a hook that held it
	// would be the card frozen; this is a turn ending, and a hook that ran
	// after the turn had already gone back to the input would be a
	// `turn_close` hook that closes nothing — its note would land in the next
	// turn, and a run that stopped in between would never fire it at all.
	// What keeps this from stopping the session is the ceiling, which is why
	// a hook's is short and cannot be turned off
	// (docs/capabilities/hooks.md#a-hook-that-runs-too-long-has-failed).
	m.hookNotes(m.hooks.TurnClose(context.Background(), m.hookPos(), m.lastAssistantText()))
}

// turnCloseData assembles the close block from what the session already
// tracks.
func (m Model) turnCloseData() *components.TurnClose {
	es := m.turnEntries()
	commit := turnCommitRow(es)
	changes := m.turnChangesRow(commit != nil)
	c := components.TurnClose{
		State:   m.turnOutcome,
		Elapsed: components.FormatElapsed(m.turnElapsed()),
		Changes: changes,
		// A commit row already answers what the turn wrote, so it is only
		// with neither that an unvouched act is answered with nothing.
		WroteNothing: changes == nil && commit == nil && m.ranUnvouched(es),
		Commit:       commit,
		Notes:        m.turnNotesClause(),
		Checks:       turnChecksRow(es, m.gate.Manage != nil),
	}
	// The count is the steps this turn actually ran, so an approved plan's
	// declared-but-not-started steps are not counted as work done.
	for _, blk := range m.blocksOf(es) {
		if blk.step != nil && !blk.step.queued() {
			c.Steps++
		}
	}
	for _, e := range es {
		if isActivityEntry(e) {
			c.Tools++
		}
	}
	// The turn's own cost, priced per request as it went; an unpriced model
	// reports tokens rather than a made-up zero.
	if t, ok := m.vitals.lastTurn(); ok {
		if t.Priced {
			c.Spend = formatCost(t.Cost)
		} else {
			c.Spend = m.freshRateLabel(t.In, t.Out)
		}
	}
	return &c
}

// turnChangesRow is the changed-files row, read from the turn's changeset.
// A turn that changed nothing has no such row: where it only read, the
// summary row stands alone, and where it ran something that could have
// written, the close says `wrote nothing` in its place (ranUnvouched).
//
// A turn that committed still opens its review and loses the undo offer. Undo
// puts files back out of the session's own records, which still works, but
// offering it beside a commit would read as an offer to take the commit back,
// and the honest key for that is `git revert` — a sentence somebody types,
// not a key shhh can put on a row. The commit row below says so in words.
func (m Model) turnChangesRow(committed bool) *components.TurnChanges {
	t, ok := m.changes.Turn(m.turnCount)
	if !ok {
		return nil
	}
	return m.turnChangesFor(t, committed)
}

// ranUnvouched reports whether the turn ran a command, or a server call the
// person did not mark read-only — the acts that carry the mutation rail
// because shhh cannot see what they wrote. The changeset records only what
// the edit tools wrote, so for such a turn an empty changeset is not the
// same answer as a turn that only read: a command is assumed to write
// (docs/interface/principles.md#weight-tracks-risk), and the close says it
// wrote nothing rather than falling silent on the question. A call that was
// refused, skipped or never started ran nothing, and a dry run the reader
// asked for ran a derived form of the command, not the command.
func (m Model) ranUnvouched(es []entry) bool {
	for _, e := range es {
		switch e.kind {
		case entryCommand:
			if !e.localRun && e.commandResult.Outcome != tools.ExecDidNotStart {
				return true
			}
		case entryTool:
			if e.deniedBy != "" || e.skipped != "" {
				continue
			}
			switch m.receiptOf(e).Kind {
			case receipt.KindRun, receipt.KindRemote:
				return true
			}
		}
	}
	return false
}

// turnCommitRow is the commit this turn made, or nothing. The receipt is the
// tool's own words — one wording for the row, the close and the unattended
// runner — and the last commit of a turn is the one the close names, because
// that is the one HEAD is standing on.
func turnCommitRow(es []entry) *components.TurnCommit {
	for i := len(es) - 1; i >= 0; i-- {
		e := es[i]
		if e.kind != entryTool || !receipt.Build(receipt.Call{Name: e.toolName, Args: e.toolArgs}).IsCommit() {
			continue
		}
		line := firstLine(e.toolResult)
		if line == "" || strings.HasPrefix(line, "error:") {
			continue
		}
		return &components.TurnCommit{Receipt: line}
	}
	return nil
}

// restoreTurnClose puts the last restored turn's changeset row on the
// transcript when the rebuilt conversation has none. A close block is
// never saved with the messages, so a resume that only restored the
// conversation would have the files on the rail and no row offering
// review, undo or commit.
func (m *Model) restoreTurnClose() {
	if m.changes == nil {
		return
	}
	t, ok := m.changes.Latest()
	if !ok || t.Files() == 0 {
		return
	}
	for _, e := range m.transcript {
		if e.kind == entryTurnClose && e.turn == t.N {
			return
		}
	}
	m.appendEntry(entry{
		kind:     entryTurnClose,
		turn:     t.N,
		restored: true,
		close: &components.TurnClose{
			State:   components.TurnDone,
			Changes: m.turnChangesFor(t, false),
		},
	})
}

// turnChangesFor is the changeset row for a turn already in hand, so a
// resume can draw the last sitting's close without pretending that turn
// is this sitting's current one.
func (m Model) turnChangesFor(t changeset.Turn, committed bool) *components.TurnChanges {
	if t.Files() == 0 {
		return nil
	}
	// Review or keep. Review is the row's own open and is drawn in front of
	// this once the row is selected (inertkeys.go); keeping it is the
	// handover, which opens the commit card, for as long as the changeset is
	// uncommitted — banking work twice is not an offer. Taking it back is
	// /undo, said in the note where there is room, and only while there is
	// something /undo can reach: history is not.
	var offers []components.TurnKey
	back := ""
	if !committed {
		offers = append(offers, commitOffer())
		back = fmt.Sprintf("/undo %d takes it back", t.N)
	}
	return &components.TurnChanges{
		Files:   t.Files(),
		Added:   t.Added,
		Removed: t.Removed,
		Mode:    t.ModeChange(),
		Keys:    offers,
		Note:    trackingNote(t),
		Back:    back,
	}
}

// trackingNote says what git knew about the files when they were edited — the
// input to how reversible the turn is. Outside a repository every
// answer is unknown, which is not the same as untracked, so the note says so
// differently.
func trackingNote(t changeset.Turn) string {
	var tracked, untracked int
	for _, r := range t.Records {
		switch r.Track {
		case changeset.TrackTracked:
			tracked++
		case changeset.TrackUntracked:
			untracked++
		}
	}
	switch {
	case tracked == 0 && untracked == 0:
		return "not a git repository"
	case untracked == 0:
		return "all tracked in git"
	case tracked == 0:
		return "all new to git"
	}
	return fmt.Sprintf("%d tracked · %d new", tracked, untracked)
}

// turnChecksRow is the verdict row: what the turn ran to check its own work.
// Several runs collapse into one tally rather than one row each — the row
// answers "does it still build", not "what did you run" — and a run the
// repository's own suite has since answered is counted rather than argued
// with (resolved.go).
//
// gated says the session has a quality gate to run again, which is what puts
// the `/gate run` that does it in the row's note. A verdict a command left
// never carries it, however the session is configured: re-running that would
// be a line nobody is looking at any more.
func turnChecksRow(es []entry, gated bool) *components.TurnChecks {
	r := resolveChecks(es)
	standing := r.standing()
	if len(standing) == 0 {
		return nil
	}
	row := components.TurnChecks{Superseded: r.superseded()}
	if suite := suiteOfTurn(es); gated && r.suites() > 0 && suite != "" {
		row.Again = "/gate run " + suite
	}
	if len(standing) == 1 {
		row.Failed = standing[0].outcome == checkFailed
		row.Label, row.Counts = standing[0].label, standing[0].counts
		return &row
	}
	passed := 0
	for _, a := range standing {
		if a.outcome == checkPassed {
			passed++
		}
	}
	row.Failed = passed < len(standing)
	row.Label = "checks"
	row.Counts = fmt.Sprintf("%d of %d passing", passed, len(standing))
	return &row
}

// suiteOfTurn is the suite the verdict on a turn's close row came from, read
// back out of the turn's own gate row. The entries are read rather than the
// suite being stored on the row, because the row is a rendering and the run
// that produced it is the fact.
func suiteOfTurn(es []entry) string {
	for i := len(es) - 1; i >= 0; i-- {
		if s, ok := gateVerdict(es[i]); ok {
			return s.Suite
		}
	}
	return ""
}

// plural is the chat-side counterpart of the components helper, for the
// labels this package builds itself.
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
