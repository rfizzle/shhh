package components

// The list half of a take-over screen
// (docs/architecture.md#a-list-screen-is-one-shape-with-its-own-rows).
//
// Most of the supporting screens are a list on the left, the item under the
// pointer laid out on the right, and the shared chrome around both. Every one
// of them had written the list half for itself — the pointer and its walk,
// the row under it, the window, the register key, the header's key pair, the
// footer and the frame — and the copies differed only in their names, so a
// rule moved in one was a rule the next screen did not have. The filtering
// screens had the query line's handling written out again on top of that.
//
// So the list half is here and a screen embeds it. What a screen keeps is
// everything that is a fact about that screen: its rows and what one looks
// like, its preview, its header's fields, its offers and what they do, and
// whatever it opens over the list.

import (
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// listScreen is the list half of a take-over screen over rows of T. The rows
// are the screen's own field, under the name its host already writes, and are
// handed to the methods that read them.
type listScreen[T any] struct {
	// Focus is an index into the screen's rows — all of them, not only the
	// ones showing — so it survives a filter and a host that hands the rows
	// back rebuilt.
	Focus int
	// list is the selector window the rows are drawn in. It remembers where
	// the window was between keystrokes, and on a filtering screen it holds
	// the query line.
	list Select
	// keys says the register is open: the footer is every key the screen has,
	// and the header's first key offers to hide it.
	keys bool
	listFilter
}

// listFilter is the part of a list screen that filters, or draws its rows in
// an order of its own.
type listFilter struct {
	// shown are the positions in the screen's rows that are drawn, in the
	// order they are drawn. A screen that draws every row where it stands
	// leaves it nil.
	shown []int
}

// showing reports whether position row is one of the rows showing.
func (f *listFilter) showing(row int) bool { return slices.Contains(f.shown, row) }

// place is where position row is among the rows showing, and the first of
// them where the filter hid it.
func (f *listFilter) place(row int) int { return max(slices.Index(f.shown, row), 0) }

// screenParts is what a screen hands the shared frame: its rows rebuilt into
// the window, the chrome around the panes, and the panes.
type screenParts interface {
	sync()
	chrome(width int) screenChrome
	panes() screenPanes
}

// view draws the screen: the rows rebuilt first, because the header and the
// footer read the row under the pointer, then the chrome with the two panes
// in the rows it leaves.
func (l *listScreen[T]) view(width int, s screenParts) string {
	if width <= 0 {
		return ""
	}
	s.sync()
	return s.chrome(width).view(width, func(budget int) []string { return s.panes().rows(width, budget) })
}

// clamp keeps the pointer on one of n rows, for a host that handed back fewer
// than there were.
func (l *listScreen[T]) clamp(n int) { l.Focus = min(max(l.Focus, 0), max(n-1, 0)) }

// show hands the window the options it draws, how many rows its marker counts
// (zero counts the options), and which option the pointer is on.
func (l *listScreen[T]) show(opts []SelectOption, total, at int) {
	l.list.Options = opts
	l.list.Total = total
	l.list.Unnumbered = true
	l.list.Focus = at
}

// current is the row under the pointer, or nil for an empty list.
func (l *listScreen[T]) current(items []T) *T {
	if l.Focus < 0 || l.Focus >= len(items) {
		return nil
	}
	return &items[l.Focus]
}

// moved walks the pointer over the rows on the screen's movement binding and
// reports whether the keystroke was one of its keys.
func (l *listScreen[T]) moved(items []T, pressed string, move keys.Binding) bool {
	if len(items) == 0 {
		return false
	}
	walk := List[T]{Items: items, Focus: l.Focus}
	if !walk.Move(pressed, move) {
		return false
	}
	l.Focus = walk.Focus
	return true
}

// listRows is the list pane: the window, or the one sentence a screen with
// nothing to list says in its place.
func (l *listScreen[T]) listRows(items []T, empty string, width, budget int) []string {
	if len(items) == 0 {
		return []string{sty.dim.Render(Clip(empty, width))}
	}
	body, _ := l.list.visibleRows(cardWidthFor(width), budget, false)
	return body
}

// headerKeys is the pair the header ends with: the key that shows the whole
// register, and the way back in the one word a header field is
// (docs/interface/surfaces.md#the-supporting-screens).
func (l *listScreen[T]) headerKeys(list, back keys.Binding) string {
	run := keys.Bracket(list) + " " + keys.Words(list)
	if l.keys {
		run = keys.Bracket(list) + " hide the keys"
	}
	return run + " · " + words(back, "back")
}

// footer is the key row and the field that annotates it, or the whole
// register while it is open.
func (l *listScreen[T]) footer(offers, register []KeyOffer, field string) keyFooter {
	return keyFooter{offers: offers, register: register, showing: l.keys, field: field}
}

// offersBeside is a key row that gives way for the field beside it: the
// movement reminder goes first, since `[?]` carries it in full and every list
// in the product moves the same way.
func offersBeside(move KeyOffer, acts []KeyOffer, field string, width int) []KeyOffer {
	full := append([]KeyOffer{move}, acts...)
	if field == "" || fitsBeside(full, field, width) {
		return full
	}
	return acts
}

// fitRungs is the first rung of a key row that leaves room for the field
// beside it, the rungs ordered by what the row gives up first. Nothing is
// ever truncated to make room (invariant 4): a segment goes whole or stays
// whole. Where no rung fits, the field goes, and with nothing left to buy the
// row keeps every offer it had and wraps.
func fitRungs(field string, width int, rungs ...[]KeyOffer) []KeyOffer {
	if field == "" {
		return rungs[0]
	}
	for _, rung := range rungs {
		if fitsBeside(rung, field, width) {
			return rung
		}
	}
	return rungs[0]
}

// match is the positions of the rows the query leaves showing: the query,
// trimmed, found in any of the fields a screen names for a row.
func (l *listScreen[T]) match(items []T, fields func(T) []string) []int {
	return Filter(items, strings.TrimSpace(l.list.Query), fields)
}

// refocus puts the pointer on the first row a changed query left showing —
// the rows under it are not the rows that were there a moment ago.
func (l *listScreen[T]) refocus(items []T, fields func(T) []string) {
	if shown := l.match(items, fields); len(shown) > 0 {
		l.Focus = shown[0]
	}
}

// showFiltered is show for a screen with a query line, which counts against
// every row it has and says what the line filters by.
func (l *listScreen[T]) showFiltered(opts []SelectOption, total int, hint string) {
	l.show(opts, total, l.at())
	l.list.Filterable = true
	l.list.QueryHint = hint
}

// filterKey answers a key while the query line is open, and reports whether
// it was — with the line open the line is the surface, so every letter is
// text — and whether the query changed. A clear on an empty query closes the
// line, which is how the row keys are got back without leaving the screen.
func (l *listScreen[T]) filterKey(msg tea.KeyPressMsg) (open, changed bool) {
	if !l.list.Filtering {
		return false, false
	}
	if keys.Is(msg.String(), keys.Screen.ClearQ) && l.list.Query == "" {
		l.list.Filtering = false
		return true, false
	}
	l.list.editQuery(msg)
	return true, l.list.QueryChanged()
}

// filteredBy is the header's fields with the query stated after them. The
// header is what says what the count under it is a count of, so `4 of 12` on
// the query row reads as a list this row has already said is filtered.
func (l *listScreen[T]) filteredBy(left []RailSegment) []RailSegment {
	if query := strings.TrimSpace(l.list.Query); query != "" {
		left = append(left, screenField("filtered by "+strconv.Quote(query)))
	}
	return left
}

// queryListRows is the list pane of a screen with a query line: the line
// pinned above the window, the window, and under it what the filter hid.
func (l *listScreen[T]) queryListRows(total int, hidden func(n int) string, width, budget int) []string {
	head := l.list.queryRows(cardWidthFor(width))
	if len(head) > 0 {
		head = append(head, screenRule(width))
	}
	tail := l.hiddenRows(total, hidden, width)
	body, _ := l.list.visibleRows(cardWidthFor(width), listBudget(budget, len(head)+len(tail)), false)
	return append(append(head, body...), tail...)
}

// hiddenRows is the line under the list saying what the filter took out of
// it, counted in the screen's own noun. It is only ever drawn while something
// is hidden — a filter that hid nothing has nothing to confess (invariant 4).
func (l *listScreen[T]) hiddenRows(total int, hidden func(n int) string, width int) []string {
	if !l.list.Filtering {
		return nil
	}
	n := total - len(l.shown)
	if n <= 0 {
		return nil
	}
	row := sty.dim.Render(hidden(n)+" hidden by the filter · ") +
		sty.key.Render(keys.Bracket(keys.Screen.ClearQ)) + sty.dim.Render(" clear it")
	return []string{screenRule(width), Clip(row, width)}
}

// movedShown walks the pointer over the rows showing and reports whether the
// keystroke was the screen's movement key. The pointer is the row's place in
// all of them, so what moves is a List over the positions showing. With the
// query line open only the half of the binding no sentence produces moves it:
// a j typed into a filter is a letter.
func (l *listScreen[T]) movedShown(pressed string, move keys.Binding) bool {
	if len(l.shown) == 0 {
		return false
	}
	walk := List[int]{Items: l.shown, Focus: l.at()}
	moved := false
	if l.list.Filtering {
		moved = walk.moveTyping(pressed, move)
	} else {
		moved = walk.Move(pressed, move)
	}
	if !moved {
		return false
	}
	l.Focus = l.shown[walk.Focus]
	return true
}

// at is where the pointer is among the rows showing, and the first of them
// where the filter hid the row it was on.
func (l *listScreen[T]) at() int { return l.place(l.Focus) }

// currentShown is the row under the pointer among the rows showing, or nil
// when the filter left none.
func (l *listScreen[T]) currentShown(items []T) *T {
	if !l.showing(l.Focus) {
		return nil
	}
	return &items[l.Focus]
}
