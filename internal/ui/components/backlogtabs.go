package components

// Which of the backlog screen's three tabs is up, the key that steps between
// them, the items each one lists, and the layout the sprint tab draws that
// the other two do not. It is its own file because the tab is the screen's
// one fork: every renderer beside it draws whatever rows it was handed, so
// the answer to "which rows, and under what head" is worth having in one
// place rather than as a condition inside each of them.

import (
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// swapTab steps to the next tab there is. The sprint tab is skipped where
// the project has no sprint, so the key never lands on a tab with nothing
// on it.
//
// The status and ready filters come off on the archive: every archived item
// has the same status, so a status filter carried in there would empty a
// list the reader had just asked to see.
func (b *BacklogScreen) swapTab() {
	for range backlogTabs {
		b.tab = (b.tab + 1) % backlogTabs
		if b.tab != backlogTabSprint || b.sprintTab() {
			break
		}
	}
	b.reading, b.pager.Offset = false, 0
	b.confirm, b.pending = nil, nil
	if b.archived() {
		b.status, b.ready = 0, false
	}
	// The pointer is kept per tab rather than reset, so coming back lands
	// where the reader left off. Dropping the two filters above can only
	// widen what is showing, so the row it was on is still one of them.
	b.sync()
}

// sprintRows is the sprint tab: the board's head, then the two panes over
// the set's own items. The head is pinned rather than scrolled with the
// list — what the set is for and how far through it is are the two facts
// the tab exists to state, and a head that scrolled away would leave a
// list of slugs indistinguishable from the backlog's.
func (b *BacklogScreen) sprintRows(width, budget int) []string {
	head := b.boardRows(width)
	if len(head) > 0 {
		head = append(head, screenRule(width))
	}
	if budget <= 0 {
		return append(head, b.panes(width, 0)...)
	}
	// A head taller than the tab leaves no list at all, so it gives ground
	// first: the rows under it are the set, and a board with no set on it
	// is a paragraph.
	if len(head) >= budget-backlogMinBody {
		head = truncRows(head, max(budget-backlogMinBody, 1), width)
	}
	return append(head, b.panes(width, budget-len(head))...)
}

// tabOffer names the tab the key would go to rather than the tab it is on,
// because a key is named for what it does.
func (b *BacklogScreen) tabOffer() KeyOffer {
	switch {
	case b.archived():
		return keyOfferAs(keys.Backlog.Tab, "the backlog")
	case b.sprinting():
		return keyOfferAs(keys.Backlog.Tab, "what shipped")
	case b.sprintTab():
		return keyOfferAs(keys.Backlog.Tab, "the sprint")
	}
	return keyOfferAs(keys.Backlog.Tab, "what shipped")
}

// sprintOffer is the one key here whose words depend on the row: the same
// act reads as adding or as dropping according to whether the set already
// names this item.
func (b *BacklogScreen) sprintOffer(row BacklogRow) KeyOffer {
	if row.InSprint {
		return keyOfferAs(keys.Backlog.Sprint, "drop it from "+b.Sprint)
	}
	return keyOfferAs(keys.Backlog.Sprint, "add it to "+b.Sprint)
}

// archived, sprinting and planning are which tab the screen is on and
// whether the proposal is what that tab is showing.
func (b *BacklogScreen) archived() bool  { return b.tab == backlogTabDone }
func (b *BacklogScreen) sprinting() bool { return b.tab == backlogTabSprint }
func (b *BacklogScreen) planning() bool  { return b.Plan != nil }

// sprintTab reports that there is a sprint tab to step onto: a board to
// draw, or a proposal to answer.
func (b *BacklogScreen) sprintTab() bool { return b.Board != nil || b.Plan != nil }

// rows is the tab's own items.
func (b *BacklogScreen) rows() []BacklogRow {
	switch {
	case b.archived():
		return b.Done
	case b.sprinting() && b.Board != nil:
		return b.Board.Rows
	case b.sprinting():
		return nil
	}
	return b.Rows
}
