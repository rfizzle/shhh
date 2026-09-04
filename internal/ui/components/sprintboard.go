package components

// The sprint tab of the backlog screen
// (docs/interface/surfaces.md#the-sprint-board,
// docs/capabilities/todo.md#a-sprint-is-a-file-that-names-its-items).
//
// A sprint is a set of items in a stated order under a goal, and it was
// readable only as a listing: the command prints it, the rail carries its
// name and its count, and neither of them says where the set stands. The
// two questions actually asked of a sprint — what is this set for, and how
// far through it are we — are board questions, so the answer is a tab of
// the screen the backlog already has rather than a fourth place items are
// drawn.
//
// The tab is the screen's own two panes over the sprint's slugs in the
// file's order. What it adds above them is the head: the goal, the progress
// meter, what the set has cost, and the one row that says how it ended.
//
// Planning is the same tab before there is a file. The proposal is drawn
// here as a card that holds the keyboard, and nothing is written until it is
// taken (docs/capabilities/todo.md#a-session-proposes-you-accept). The card
// keeps `j/k`, which the list under it had to give up: while the card is up
// the list is drawn and not live, so no filter letter is competing for the
// keystroke.

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// sprintMeterCells is the progress bar's width on the board: the product's
// standard meter, which is what the same ratio is drawn at everywhere else
// it appears. The head runs the tab's full width, so there is nothing here
// forcing a narrower bar, and a meter that changed length by surface would
// read as a different quantity.
const sprintMeterCells = MeterCellsRail

// SprintBoard is the sprint as the tab draws it: what the host read off the
// file, the backlog and the run's checkpoint, already in words. This package
// holds no opinion about what a sprint is — it draws the fields it is given,
// the way every row on this screen is a reading the host made.
type SprintBoard struct {
	// Name is the set's own name; Goal is the paragraph above its list, as
	// written.
	Name, Goal string
	// Done of Total is what the meter draws. Total counts the slugs the
	// backlog still holds, so a slug deleted from the backlog leaves the
	// ratio rather than counting as finished.
	Done, Total int
	// Spend is what the set has cost so far, in the host's words, from the
	// session record. Empty draws no spend row: a set nobody has spent
	// anything on says nothing rather than saying zero.
	Spend string
	// Stopped is how the set stopped, where something stopped it — the
	// block and the item that wrote it. It is a warning row, because a
	// sprint that stopped on a block attempted nothing after it.
	Stopped string
	// Next is the item the sprint takes next. It is what the first row on
	// the other side of a session boundary says, and it is here so the
	// board and that row cannot disagree.
	Next string
	// Report is the page a closed sprint wrote, and Closed says this board
	// is a record rather than a plan. The row offering the page is the
	// board's last, which is where an activity row puts its link too.
	Report string
	Closed bool
	// Rows are the set's slugs in the file's order, each one placed against
	// the backlog by the host. A row's Note is where it stands in the set,
	// which is not the same reading as its status in the backlog.
	Rows []BacklogRow
}

// SprintPlanRow is one proposed item on the plan card.
type SprintPlanRow struct {
	Slug, Title string
	// Note is the one line saying why this item is in the set.
	Note string
	// Dropped is a row the reader took out. It stays on the card rather
	// than leaving it: the card is the only record of what was proposed,
	// and a row that vanished could not be put back.
	Dropped bool
}

// SprintPlan is the proposal on the tab: the set the session offers, in the
// order it would be written, with the reason each item is in it. Nothing
// here is on disk — the file is written by the key that takes the card, and
// by nothing else.
type SprintPlan struct {
	// Budget is what the header says the proposal was filtered to, in the
	// host's words. A proposal the reader cannot see the shape of is one
	// they have to take on trust.
	Budget string
	// Goal is the goal the sprint would be written with.
	Goal string
	// Rows are the proposed items in the order they would be written.
	Rows []SprintPlanRow

	// list is the shared pointer and window (list.go).
	list  List[int]
	focus int
}

// Kept is the slugs still in the proposal, in the order they are drawn. It
// is what the key that takes the card hands back.
func (p *SprintPlan) Kept() []string {
	var out []string
	for _, r := range p.Rows {
		if !r.Dropped {
			out = append(out, r.Slug)
		}
	}
	return out
}

// sync rebuilds the card's window. The rows never change under it — a plan
// is a snapshot the reader is answering — so this only has to hold the
// pointer inside them.
func (p *SprintPlan) sync() {
	idx := make([]int, len(p.Rows))
	for i := range p.Rows {
		idx[i] = i
	}
	p.list.Items = idx
	p.list.Focus = min(max(p.focus, 0), max(len(p.Rows)-1, 0))
	p.list.Normalize()
	p.focus = p.list.Focus
}

// updatePlan is the keyboard while the plan card is up. Every key here is
// the card's: the screen's own letters are not live under it, which is the
// register's reading of a takeover and the reason the pair `j/k` works here
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
func (b *BacklogScreen) updatePlan(pressed string) (bool, BacklogResult) {
	p := b.Plan
	p.sync()
	switch {
	case keys.Is(pressed, keys.Sprint.Move):
		p.list.Move(sprintMoveDelta(pressed))
		p.focus = p.list.Focus
	case keys.Is(pressed, keys.Sprint.Toggle):
		if p.focus < len(p.Rows) {
			p.Rows[p.focus].Dropped = !p.Rows[p.focus].Dropped
		}
	case keys.Is(pressed, keys.Sprint.Goal):
		return false, BacklogResult{Do: &BacklogCommand{Act: BacklogSprintGoal}}
	case keys.Is(pressed, keys.Sprint.Take):
		kept := p.Kept()
		if len(kept) == 0 {
			// Taking an empty set would write a sprint that scopes the
			// ready list to nothing, which is the one file here nobody can
			// work out of. The card says so and stays up, because the way
			// back is one keystroke on the row the reader just cleared.
			b.Notice = "nothing is left in the set; " + keys.Bracket(keys.Sprint.Toggle) +
				" puts a row back, " + keys.Bracket(keys.Sprint.Cancel) + " writes nothing"
			return false, BacklogResult{}
		}
		return false, BacklogResult{Do: &BacklogCommand{Act: BacklogSprintTake, Slugs: kept}}
	case keys.Is(pressed, keys.Sprint.Cancel):
		return false, BacklogResult{Do: &BacklogCommand{Act: BacklogSprintCancel}}
	}
	return false, BacklogResult{}
}

// sprintMoveDelta reads which end of the card's movement binding was
// pressed. It is not moveDelta because this binding has four keys and that
// one has two: `k` goes up here, and on the list below it cycles a filter.
func sprintMoveDelta(pressed string) int {
	if pressed == "up" || pressed == "k" {
		return -1
	}
	return 1
}

// planRows is the plan card: the budget it was filtered to, the goal it
// would be written with, and the proposed items with the reason each is in
// the set. It is drawn as the card it is rather than as a pane, because
// what it is asking for is one answer about the whole set.
func (b *BacklogScreen) planRows(width, budget int) []string {
	p := b.Plan
	p.sync()
	rows := []string{Clip(sty.Dim.Render("nothing is written until ")+
		sty.Info.Render(keys.Bracket(keys.Sprint.Take)), width)}
	if goal := strings.TrimSpace(p.Goal); goal != "" {
		rows = append(rows, wrapDim(goal, width)...)
	}
	rows = append(rows, "")
	// 0 is unbounded to the list below, so a bounded card that has spent
	// its whole budget on the head still asks for one row: a card with no
	// row on it cannot be answered, and the marker under it says how many
	// went.
	body := 0
	if budget > 0 {
		body = max(budget-len(rows), 1)
	}
	return append(rows, p.rows(width, body)...)
}

// rows is the proposal's own list, windowed. A dropped row keeps its place
// and loses its tick.
func (p *SprintPlan) rows(width, budget int) []string {
	if len(p.Rows) == 0 {
		return []string{sty.Dim.Render(Clip("nothing was proposed", width))}
	}
	lo, hi := p.list.Range(budget)
	if budget <= 0 {
		lo, hi = 0, len(p.Rows)
	}
	var out []string
	if lo > 0 {
		out = append(out, sty.Dim.Render(Clip(fmt.Sprintf("↑ %d above", lo), width)))
	}
	for i := lo; i < hi; i++ {
		out = append(out, p.row(i, width))
	}
	if below := len(p.Rows) - hi; below > 0 {
		out = append(out, sty.Dim.Render(Clip(fmt.Sprintf("↓ %d below", below), width)))
	}
	return out
}

// row is one proposed item: the box, the slug, the title, and the reason it
// is in the set. The reason is the field that gives ground, because the row
// is answerable without it and unreadable without the slug.
func (p *SprintPlan) row(i, width int) string {
	r := p.Rows[i]
	box, name := sty.Add.Render("[x]"), brightStyle()
	if r.Dropped {
		box, name = sty.Dim.Render("[ ]"), sty.Dim
	}
	pointer := "  "
	if i == p.focus {
		pointer = sty.FocusPointer.Render("❯ ")
	}
	lead := box + " " + name.Render(r.Slug)
	inner := max(width-2, 1)
	room := inner - lipgloss.Width(lead)
	if room < minBacklogTitle {
		return pointer + Clip(lead, inner)
	}
	rest := r.Title
	if r.Note != "" {
		rest = strings.TrimSpace(r.Title + "  ·  " + r.Note)
	}
	return pointer + lead + sty.Dim.Render(Clip("  "+rest, room))
}

// boardRows is the head above the sprint tab's two panes: what the set is
// for, how far through it is, what it has cost, and how it ended. Every one
// of them is a row that is absent rather than empty when the host has
// nothing to put in it — a board of blank fields says a sprint is going
// badly when what it means is that nothing has happened yet.
func (b *BacklogScreen) boardRows(width int) []string {
	board := b.Board
	if board == nil {
		return nil
	}
	var rows []string
	if goal := strings.TrimSpace(board.Goal); goal != "" {
		rows = append(rows, wrapDim(goal, width)...)
	}
	if meter, ok := SprintMeter(board.Done, board.Total, sprintMeterCells); ok {
		line := meter.View()
		if board.Spend != "" {
			line += sty.Dim.Render("  ·  " + board.Spend)
		}
		rows = append(rows, Clip(line, width))
	} else if board.Spend != "" {
		rows = append(rows, sty.Dim.Render(Clip(board.Spend, width)))
	}
	if board.Stopped != "" {
		rows = append(rows, wrapWarn("⚠ "+board.Stopped, width)...)
	}
	if board.Next != "" {
		rows = append(rows, sty.Dim.Render(Clip("next · ", width))+
			sty.Body.Render(Clip(board.Next, max(width-7, 1))))
	}
	// The page is the board's last row and the link is the whole of it,
	// which is the shape an activity row gives a published report: a URL is
	// the one field that must never be clipped into something the reader
	// cannot paste.
	if board.Report != "" {
		rows = append(rows, sty.Info.Render(Clip("→ "+board.Report, width)))
	}
	return rows
}

// sprintOffers is the key row while the plan card holds the keyboard. The
// screen's own offers are not among them: a key that cannot act is not an
// offer (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
func sprintOffers() []KeyOffer {
	return []KeyOffer{
		keyOffer(keys.Sprint.Move),
		keyOffer(keys.Sprint.Toggle),
		keyOffer(keys.Sprint.Goal),
		keyOffer(keys.Sprint.Take),
		keyOffer(keys.Sprint.Cancel),
	}
}
