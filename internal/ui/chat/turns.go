package chat

// The turns screen (docs/interface/surfaces.md#the-supporting-screens):
// `/turns`, and the rail's THIS TURN heading, open every turn the session has
// run. The rail answers for the present turn; the figures of the others sit
// in the close row each one left in the transcript, and that row is what the
// screen reads — the block itself, not a second count of it — so a turn reads
// the same here as it does in the feed.
//
// A resumed conversation is the gap. A close block is never saved with the
// messages, so the turns of an ended sitting come back with no close (or,
// for the last one with files, the bare changeset row restoreTurnClose puts
// back); what survives of them is their files, in the changeset store. Those
// turns are drawn as their number and their files and say their figures were
// not kept, rather than reporting a cost nobody measured
// (docs/interface/principles.md#a-stat-that-cannot-be-reported-is-left-out).

import (
	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// openTurns puts the screen up. It is built once per opening, like the
// readings screen: what it draws is the session as it stood when the reader
// asked, and a list that grew a row under them mid-read would move the
// pointer off the turn they were on.
func (m Model) openTurns() (tea.Model, tea.Cmd) {
	screen := m.turnsScreenData()
	if len(screen.Turns) == 0 {
		return m.systemNotice("the session has run no turns yet")
	}
	m.screens = m.screens.with(stateTurns, &screen)
	m.enterSurface(stateTurns)
	return m, nil
}

// updateTurns routes keys while the screen is up. `[enter]` on a turn with a
// changeset opens that turn's review, which comes back here rather than to
// the prompt: the reader is walking a list, and a look at one turn is not
// leaving it.
func (m Model) updateTurns(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	screen := m.screens.turns()
	if screen == nil {
		return m.closeTurnsScreen()
	}
	done, result := screen.Update(msg)
	if !done {
		return m, nil
	}
	if result.Review == 0 {
		return m.closeTurnsScreen()
	}
	next, cmd := m.openReview(result.Review)
	if nm, ok := next.(Model); ok && nm.state == stateReview {
		nm.reviewReturn = stateTurns
		return nm, cmd
	}
	return next, cmd
}

// closeTurnsScreen hands the screen back to the turn, the way its own esc
// does and the way the rail cell that opened it does.
func (m Model) closeTurnsScreen() (tea.Model, tea.Cmd) {
	m.screens = m.screens.without(stateTurns)
	m.leaveSurface()
	m.syncViewport()
	return m, nil
}

// renderTurnsHint is the one line the screen leaves where the draft box was:
// the way out and nothing else, the way the readings screen's does.
func (m Model) renderTurnsHint() string {
	return sty.SystemMsg.Render("turns · ") + segAs(keys.Screen.Quit, "back to the prompt").render()
}

// turnsScreenData builds the screen, newest first, over every number the
// session has handed a turn. A turn is drawn from the close row it left, the
// running one from the rail's THIS TURN reading, and one with neither from
// the files the changeset store still holds for it; a turn with none of the
// three has nothing true to put on a row, and is left out.
func (m Model) turnsScreenData() components.TurnsScreen {
	closes := map[int64]entry{}
	tools := 0
	for i := len(m.transcript) - 1; i >= 0; i-- {
		e := m.transcript[i]
		if isActivityEntry(e) {
			tools++
		}
		// The newest close a turn left is the one that stands: an undo or a
		// rewind lands as a close of its own after the turn it acts on.
		if e.kind == entryTurnClose && e.close != nil {
			if _, seen := closes[e.turn]; !seen {
				closes[e.turn] = e
			}
		}
	}
	var items []components.TurnsItem
	for n := m.turnCount; n >= 1; n-- {
		item := components.TurnsItem{N: n}
		recorded, ok := m.changes.Recall(n)
		if ok {
			item.Reviewable = true
			item.Files, item.Added, item.Removed = turnsFiles(recorded)
		}
		e, closed := closes[n]
		switch {
		case closed && !e.restored:
			item.Close = e.close
		case n == m.turnCount && m.turnOpen:
			item.Running = m.inspectorTurn(m.planChecklist())
			if item.Running == nil {
				// Nothing measured yet, and nothing reported: the turn is
				// running and that is all that is known.
				item.Running = &components.InspectorTurn{Running: m.working()}
			}
		case len(item.Files) == 0:
			continue
		}
		items = append(items, item)
	}
	screen := components.TurnsScreen{Turns: items}
	if len(items) > 0 {
		screen.Subject = plural(len(items), "turn")
		screen.Tools = plural(tools, "tool")
	}
	// The session's spend as /stats states it: the ledger's, which is every
	// request the session paid for rather than the turns' own rounds alone.
	if spent := m.totalsLabel(m.sessionSpend()); spent != "" {
		screen.Spend = spent + " spent"
	}
	return screen
}

// turnsFiles is a turn's records as the screen lists them, in the order the
// store holds them, with their totals.
func turnsFiles(t changeset.Turn) (files []components.TurnsFile, added, removed int) {
	for _, r := range t.Records {
		files = append(files, components.TurnsFile{Path: r.Path, Added: r.Added, Removed: r.Removed})
	}
	return files, t.Added, t.Removed
}
