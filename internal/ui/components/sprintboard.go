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
// taken (docs/capabilities/todo.md#a-session-proposes-you-accept). Taking it
// is the chord every write is, and the goal is the card's first row, so
// enter means the row under the pointer here as it does on every list.

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
const sprintMeterCells = meterCellsRail

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
	// Lanes are the items a sprint is working at once, each with the step
	// it is at, in the order they were taken. The head states how many and
	// names none of them, because each is a row with its step in the note.
	// None draws nothing: a sprint working one item at a time says which on
	// that item's own row.
	Lanes []SprintLane
}

// SprintLane is one item a sprint is working beside others.
type SprintLane struct {
	Slug, Stage string
}

// SprintPlanRow is one proposed item on the plan card.
type SprintPlanRow struct {
	Slug, Title string
	// Note is the one line saying why this item is in the set, as the
	// reading behind the proposal wrote it.
	Note string
	// Dropped is a row the reader took out. It stays on the card rather
	// than leaving it: the card is the only record of what was proposed,
	// and a row that vanished could not be put back.
	Dropped bool
}

// SprintPlanOut is one candidate the reading left out of the set, with the
// word for why. The word is the host's — this package draws it.
type SprintPlanOut struct {
	Slug, Title, Why string
}

// SprintPlan is the proposal on the tab: the set the session offers, in the
// order it would be written, with the reason each item is in it. Nothing
// here is on disk — the file is written by the key that takes the card, and
// by nothing else.
type SprintPlan struct {
	// Budget is what the header says the proposal was bounded by, in the
	// host's words. A proposal the reader cannot see the shape of is one
	// they have to take on trust.
	Budget string
	// Goal is the sentence the sprint would be written with, and Release
	// the line saying what kind of release the set reads as. They are two
	// fields because the goal is the person's to rewrite and the release
	// line is the reading's judgement: editing one must not take the other
	// with it.
	Goal, Release string
	// Rows are the proposed items in the order they would be written.
	Rows []SprintPlanRow
	// Left are the candidates the reading did not take. They are folded
	// under the set rather than listed beside it: what the reader is
	// answering is the set, and what was left out is the evidence behind
	// the answer (docs/interface/principles.md#fold-never-hide).
	Left []SprintPlanOut

	// open reports the left-out list is unfolded.
	open bool
	// list is the shared pointer and window (list.go), over the goal row
	// and then the set. focus is the set's row the pointer is on, and -1
	// for the goal row above them.
	list  List[int]
	focus int
}

// kept is the slugs still in the proposal, in the order they are drawn. It
// is what the key that takes the card hands back.
func (p *SprintPlan) kept() []string {
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
	idx := make([]int, 0, len(p.Rows)+1)
	idx = append(idx, sprintGoalRow)
	for i := range p.Rows {
		idx = append(idx, i)
	}
	p.list.Items = idx
	p.list.Focus = min(max(p.focus+1, 0), len(idx)-1)
	p.list.normalize()
	p.focus = p.list.Focus - 1
}

// sprintGoalRow is the goal's place in the card's list: above the set.
const sprintGoalRow = -1

// onGoal reports the pointer is on the goal row.
func (p *SprintPlan) onGoal() bool { return p.focus == sprintGoalRow }

// update is the keyboard while the plan card is up. Every key here is the
// card's: the screen's own keys are not live under it, which is the
// register's reading of a takeover
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
// The notice is the line the screen shows under it, empty for none.
func (p *SprintPlan) update(pressed string) (backlogResult, string) {
	p.sync()
	switch {
	case p.list.Move(pressed, keys.Sprint.Move):
		p.focus = p.list.Focus - 1
	case keys.Is(pressed, keys.Sprint.Toggle):
		if p.focus >= 0 && p.focus < len(p.Rows) {
			p.Rows[p.focus].Dropped = !p.Rows[p.focus].Dropped
		}
	case keys.Is(pressed, keys.Sprint.Left) && len(p.Left) > 0:
		p.open = !p.open
	case keys.Is(pressed, keys.Sprint.Goal) && p.onGoal():
		return backlogResult{Do: &BacklogCommand{Act: BacklogSprintGoal}}, ""
	case keys.Is(pressed, keys.Sprint.Take):
		kept := p.kept()
		if len(kept) == 0 {
			// Taking an empty set would write a sprint that scopes the
			// ready list to nothing, which is the one file here nobody can
			// work out of. The card says so and stays up, because the way
			// back is one keystroke on the row the reader just cleared.
			return backlogResult{}, "nothing is left in the set; " + keys.Bracket(keys.Sprint.Toggle) +
				" puts a row back, " + keys.Bracket(keys.Sprint.Cancel) + " writes nothing"
		}
		return backlogResult{Do: &BacklogCommand{Act: BacklogSprintTake, Slugs: kept}}, ""
	case keys.Is(pressed, keys.Sprint.Cancel):
		return backlogResult{Do: &BacklogCommand{Act: BacklogSprintCancel}}, ""
	}
	return backlogResult{}, ""
}

// view is the plan card: the budget it was bounded by, the goal it would
// be written with, the proposed items with the line saying why each is in
// the set, and under them what the reading left out. It is drawn as the
// card it is rather than as a pane, because what it is asking for is one
// answer about the whole set.
func (p *SprintPlan) view(width, budget int) []string {
	p.sync()
	rows := []string{Clip(sty.dim.Render("nothing is written until ")+
		sty.key.Render(keys.Bracket(keys.Sprint.Take)), width)}
	if goal := strings.TrimSpace(p.Goal); goal != "" {
		rows = append(rows, wrapDim(goal, width)...)
	}
	if line := strings.TrimSpace(p.Release); line != "" {
		rows = append(rows, wrapDim(line, width)...)
	}
	rows = append(rows, "")
	// The left-out list is laid out first because the set gives ground to
	// it: the set's own rows window and say how many went, and these do
	// not — a fold that scrolled off would be a fold with no way back.
	tail := p.leftRows(width)
	// 0 is unbounded to the list below, so a bounded card that has spent
	// its whole budget on the head still asks for one row: a card with no
	// row on it cannot be answered, and the marker under it says how many
	// went. An unfolded list longer than the card gives ground the same
	// way, because the set is what the reader is answering.
	body := 0
	if budget > 0 {
		tail = truncRows(tail, budget-len(rows)-1, width)
		body = max(budget-len(rows)-len(tail), 1)
	}
	return append(append(rows, p.rows(width, body)...), tail...)
}

// leftRows is what the card says about the candidates the reading did not
// take: the count and the words while it is folded, one row each when it is
// open. A proposal with nothing left out draws neither — every candidate
// was taken, and there is no list to offer.
func (p *SprintPlan) leftRows(width int) []string {
	if len(p.Left) == 0 {
		return nil
	}
	if !p.open {
		head := fmt.Sprintf("%s left out · %s", plural(len(p.Left), "item"), leftWords(p.Left))
		return []string{"", sty.dim.Render(Clip(head, width)) + " " + sty.key.Render(keys.Bracket(keys.Sprint.Left))}
	}
	out := []string{"", sty.dim.Render(Clip(plural(len(p.Left), "item")+" left out", width)) +
		" " + sty.key.Render(keys.Bracket(keys.Sprint.Left))}
	word := leftWordWidth(p.Left)
	for _, l := range p.Left {
		rest := strings.TrimSpace(l.Slug + "  " + l.Title)
		out = append(out, sty.dim.Render(Clip(fmt.Sprintf("  %-*s  %s", word, l.Why, rest), width)))
	}
	return out
}

// leftWords is how many of the left-out items took each word, in the order
// the host gave them. It is the folded row's whole account, so it names the
// words rather than only the count: "four left out" says a reading dropped
// four items and not what it dropped them for.
func leftWords(left []SprintPlanOut) string {
	var order []string
	count := map[string]int{}
	for _, l := range left {
		if count[l.Why] == 0 {
			order = append(order, l.Why)
		}
		count[l.Why]++
	}
	parts := make([]string, 0, len(order))
	for _, w := range order {
		parts = append(parts, fmt.Sprintf("%d %s", count[w], w))
	}
	return strings.Join(parts, ", ")
}

// leftWordWidth is the column the words line up in: the longest one this
// proposal actually used. It is measured off the rows rather than off a
// vocabulary, because this package draws the words it is handed and holds
// no list of which ones there are.
func leftWordWidth(left []SprintPlanOut) int {
	n := 0
	for _, l := range left {
		n = max(n, len(l.Why))
	}
	return n
}

// rows is the proposal's own list, windowed. A dropped row keeps its place
// and loses its tick.
func (p *SprintPlan) rows(width, budget int) []string {
	if len(p.Rows) == 0 {
		return []string{p.goalRow(width), sty.dim.Render(Clip("nothing was proposed", width))}
	}
	lo, hi := p.list.Range(budget)
	if budget <= 0 {
		lo, hi = 0, len(p.Rows)+1
	}
	var out []string
	if lo > 0 {
		out = append(out, sty.dim.Render(Clip(fmt.Sprintf("↑ %d above", lo), width)))
	}
	for i := lo; i < hi; i++ {
		if i == 0 {
			out = append(out, p.goalRow(width))
			continue
		}
		out = append(out, p.row(i-1, width))
	}
	if below := len(p.Rows) + 1 - hi; below > 0 {
		out = append(out, sty.dim.Render(Clip(fmt.Sprintf("↓ %d below", below), width)))
	}
	return out
}

// goalRow is the row the goal is written from. The goal itself is the
// paragraph above the set; the row is the act on it, which is why it is a
// row the pointer lands on rather than a letter of its own.
func (p *SprintPlan) goalRow(width int) string {
	words := "write what the set is for"
	if strings.TrimSpace(p.Goal) != "" {
		words = "rewrite what the set is for"
	}
	pointer, name := PointerColumn(), sty.info
	inner := max(width-GridPointerWidth, 1)
	body := name.Render("goal") + sty.dim.Render(Clip("  "+words, max(inner-4, 1)))
	if p.onGoal() {
		return sty.focusPointer.Render("❯ ") + litRowKeeping(body, 0, 0, inner)
	}
	return pointer + body
}

// row is one proposed item: the box, the slug, the title, and the reason it
// is in the set. The reason is the field that gives ground, because the row
// is answerable without it and unreadable without the slug.
func (p *SprintPlan) row(i, width int) string {
	r := p.Rows[i]
	box, name := sty.add.Render("[x]"), brightStyle()
	if r.Dropped {
		box, name = sty.dim.Render("[ ]"), sty.dim
	}
	focused := i == p.focus
	pointer := PointerColumn()
	if focused {
		pointer = sty.focusPointer.Render("❯ ")
	}
	inner := max(width-GridPointerWidth, 1)
	// The row the keyboard is on is lit under the pointer, the way it is on
	// every other list; the box keeps its own colour inside the highlight, and
	// it is told where it ends because the tick in it is a letter (LitRow).
	lit := func(body string) string {
		if !focused {
			return pointer + body
		}
		return pointer + litRowKeeping(body, 0, lipgloss.Width(box)+1, inner)
	}
	lead := box + " " + name.Render(r.Slug)
	room := inner - lipgloss.Width(lead)
	if room < minBacklogTitle {
		return lit(Clip(lead, inner))
	}
	rest := r.Title
	if r.Note != "" {
		rest = strings.TrimSpace(r.Title + "  ·  " + r.Note)
	}
	return lit(lead + sty.dim.Render(Clip("  "+rest, room)))
}

// headRows is the head above the sprint tab's two panes: what the set is
// for, how far through it is, what it has cost, and how it ended. Every one
// of them is a row that is absent rather than empty when the host has
// nothing to put in it — a board of blank fields says a sprint is going
// badly when what it means is that nothing has happened yet.
func (board *SprintBoard) headRows(width int) []string {
	if board == nil {
		return nil
	}
	var rows []string
	if goal := strings.TrimSpace(board.Goal); goal != "" {
		rows = append(rows, wrapDim(goal, width)...)
	}
	if meter, ok := sprintMeter(board.Done, board.Total, sprintMeterCells); ok {
		line := meter.View()
		// The spend joins the meter's row where the whole of it fits, and
		// takes a row of its own where it would not: a figure stated against
		// a ceiling is read for its last word, and clipping would take that
		// word first.
		joined := line + sty.dim.Render("  ·  "+board.Spend)
		switch {
		case board.Spend == "":
			rows = append(rows, Clip(line, width))
		case lipgloss.Width(joined) <= width:
			rows = append(rows, joined)
		default:
			rows = append(rows, Clip(line, width), sty.dim.Render(Clip(board.Spend, width)))
		}
	} else if board.Spend != "" {
		rows = append(rows, sty.dim.Render(Clip(board.Spend, width)))
	}
	if board.Stopped != "" {
		rows = append(rows, wrapWarn("⚠ "+board.Stopped, width)...)
	}
	rows = append(rows, laneRows(board.Lanes, width)...)
	if board.Next != "" {
		rows = append(rows, sty.dim.Render(Clip("next · ", width))+
			sty.body.Render(Clip(board.Next, max(width-7, 1))))
	}
	// The page is the board's last row and the link is the whole of it,
	// which is the shape an activity row gives a published report: a URL is
	// the one field that must never be clipped into something the reader
	// cannot paste.
	if board.Report != "" {
		rows = append(rows, sty.info.Render(Clip("→ "+board.Report, width)))
	}
	return rows
}

// laneRows is what the head says about the items being worked at once: how
// many, and nothing about which. Each of them is a row of the list under the
// head already, with the step it is at in the row's note, and the list is
// where the pointer moves and the pane beside it answers for the row — a
// slug the head named as well would be the same fact twice, one screen apart.
// See docs/interface/departures.md#the-sprint-boards-layout-was-decided-in-the-binary.
func laneRows(lanes []SprintLane, width int) []string {
	if len(lanes) == 0 {
		return nil
	}
	return []string{sty.dim.Render(Clip(fmt.Sprintf("working · %d at once", len(lanes)), width))}
}

// sprintOffers is the key row while the plan card holds the keyboard. The
// screen's own offers are not among them, and of the card's the row the
// pointer is on decides two: the toggle on a row of the set, the goal on
// the goal row — a key that cannot act is not an offer
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
func sprintOffers(p *SprintPlan) []KeyOffer {
	out := []KeyOffer{keyOffer(keys.Sprint.Move)}
	if p != nil && p.onGoal() {
		out = append(out, keyOffer(keys.Sprint.Goal))
	} else {
		out = append(out, keyOffer(keys.Sprint.Toggle))
	}
	if p != nil && len(p.Left) > 0 {
		out = append(out, keyOffer(keys.Sprint.Left))
	}
	return append(out,
		keyOffer(keys.Sprint.Take),
		keyOffer(keys.Sprint.Cancel),
	)
}
