package components

// The pointer every list on the surface moves
// (docs/interface/surfaces.md#selectors).
//
// Five lists here answer the same four questions — where may the pointer
// land, where does it go next, which run of items is on screen, and what did
// the query leave showing — and every one of them had written its own answer.
// The copies were close enough to read as the same rule and far enough apart
// to behave differently: the selector stepped over its group rails and the
// multi-select clamped, the saved-chat browser pinned its window to the
// bottom while every card here slides its window one item at a time, and four
// filters spelled the same case-folded substring test four ways.
//
// So the arithmetic is here and the lists keep their own rows. What is shared
// is the movement, the window and the match; what a list still owns is what
// an item looks like, which is the part that is actually a fact about that
// list.

import "strings"

// List is the pointer and the window a list of anything shares.
//
// It is a view over the caller's own slice and pointer rather than a second
// copy of them. Every list here carries its items and its focus as exported
// fields a host reads and writes — the model picker opens with the pointer on
// the model in use, the agent manager clamps it when a child finishes — so
// what moves in here is the arithmetic over those fields and not the fields
// themselves. Build one where the movement happens and let it go.
//
// The exception is the window, which is state and has to outlive the
// keystroke: a list that re-centred every time the pointer moved would be
// unreadable, so the window remembers where it was and the owner keeps the
// List that remembers it.
type List[T any] struct {
	// Items is the list as it stands — already filtered, in the order it is
	// drawn.
	Items []T
	// Focus is the item the pointer is on, or -1 when the pointer is
	// somewhere this list does not cover: the agent manager's blocked
	// children are pinned above the window rather than inside it.
	Focus int
	// Skip marks an item the pointer passes over rather than lands on — a
	// group rail, which labels the options under it rather than offering
	// one. nil is a list every item of which can be chosen.
	Skip func(T) bool
	// Rows is how many screen rows item i renders to: one for most, two for
	// an item that carries its consequence underneath. nil is one apiece.
	Rows func(i int) int
	// window is the run being shown, remembered between keystrokes
	// (listwindow.go).
	window listWindow
}

// skip reports whether the pointer passes over item i.
func (l *List[T]) skip(i int) bool {
	return l.Skip != nil && l.Skip(l.Items[i])
}

// height is how many rows item i renders to.
func (l *List[T]) height(i int) int {
	if l.Rows == nil {
		return 1
	}
	return l.Rows(i)
}

// geometry is what the window needs to know about this list.
func (l *List[T]) geometry() listGeometry {
	return listGeometry{
		n:      len(l.Items),
		focus:  l.Focus,
		height: l.height,
		counts: func(i int) bool { return !l.skip(i) },
	}
}

// Move steps the pointer by delta, over any item it may not land on. A move
// that runs off either end leaves the pointer where it was, so the ends of a
// list are felt rather than wrapped past.
func (l *List[T]) Move(delta int) {
	for i := l.Focus + delta; i >= 0 && i < len(l.Items); i += delta {
		if !l.skip(i) {
			l.Focus = i
			return
		}
	}
}

// Normalize puts the pointer on an item that can be chosen: a list that
// opened on a group rail — or on nothing, after a filter shortened it —
// moves to the nearest item instead. Forwards first, because a rail labels
// what is under it and the reader's eye is already there.
func (l *List[T]) Normalize() {
	if len(l.Items) == 0 {
		l.Focus = 0
		return
	}
	l.Focus = min(max(l.Focus, 0), len(l.Items)-1)
	if !l.skip(l.Focus) {
		return
	}
	for i := l.Focus; i < len(l.Items); i++ {
		if !l.skip(i) {
			l.Focus = i
			return
		}
	}
	for i := l.Focus; i >= 0; i-- {
		if !l.skip(i) {
			l.Focus = i
			return
		}
	}
}

// First is the position of the first item the pointer can land on. A filtered
// list puts its pointer here after every keystroke, because the items under
// it are not the items that were there a moment ago.
func (l *List[T]) First() int {
	for i := range l.Items {
		if !l.skip(i) {
			return i
		}
	}
	return 0
}

// Count is how many items the pointer can land on, which is all of them until
// a list carries rails.
func (l *List[T]) Count() int {
	n := 0
	for i := range l.Items {
		if !l.skip(i) {
			n++
		}
	}
	return n
}

// Index maps a 1-based position among the items that can be chosen — what the
// number keys and the `1.` prefixes count — to its position in Items.
func (l *List[T]) Index(n int) int {
	seen := 0
	for i := range l.Items {
		if l.skip(i) {
			continue
		}
		if seen++; seen == n {
			return i
		}
	}
	return 0
}

// Range is the half-open run of items a body budget shows, with the window
// moved the least it can to keep the pointer inside it.
func (l *List[T]) Range(budget int) (lo, hi int) {
	return l.window.rangeFor(l.geometry(), budget)
}

// CountIn is how many items in [lo:hi) the pointer could land on — what an
// overflow marker says it is hiding, and what a title rail says is showing.
func (l *List[T]) CountIn(lo, hi int) int { return l.geometry().countIn(lo, hi) }

// RowsIn is how many screen rows items [lo:hi) render to.
func (l *List[T]) RowsIn(lo, hi int) int { return l.geometry().rows(lo, hi) }

// Matches is the type-to-filter rule every list here answers: the query,
// folded to lower case, found somewhere inside any one of the fields the item
// offers it. An empty query matches everything, which is what makes "no
// filter" and "a filter nothing fails" one code path rather than a branch
// each caller writes.
//
// Substring rather than fuzzy, deliberately. The fields are a setting's name
// and the config key behind it, a command and the prompt that produced it, a
// saved chat's title and its opening words — a reader typing `retention` is
// naming a thing rather than sketching it, and a fuzzy match over keys that
// short would leave half the list looking like a near miss.
//
// The query is folded here and not trimmed: leading space is something the
// reader typed, and a caller that means to ignore it says so itself.
func Matches(query string, fields ...string) bool {
	if query == "" {
		return true
	}
	q := strings.ToLower(query)
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f), q) {
			return true
		}
	}
	return false
}

// Filter is the positions of the items a query left showing, in the order
// they were in. Positions rather than the items themselves because that is
// what the lists that filter actually need: the pointer is an index into the
// list as the host holds it, and a filtered copy would make every key that
// acts on the focused item convert back.
func Filter[T any](items []T, query string, fields func(T) []string) []int {
	out := make([]int, 0, len(items))
	for i, item := range items {
		if Matches(query, fields(item)...) {
			out = append(out, i)
		}
	}
	return out
}
