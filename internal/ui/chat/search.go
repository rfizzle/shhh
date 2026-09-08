package chat

// What the transcript search knows about the rows nothing is showing
// (docs/interface/surfaces.md#reading-mode).
//
// The pane marks the query where it finds it in the lines it is drawing
// (viewport.go), and that is the whole of what a search over rendered lines
// can do: a run of reads folded into `▸ ⚙ 6 reads` is six rows the pane never
// rendered, so a count taken from the pane is a count of the rows that
// happened to be open. That is the one thing a fold may not do
// (docs/interface/principles.md#fold-never-hide) — it made `no match` a fact
// about the reader's fold state rather than about the session.
//
// So the search asks the entries. Every fold on the transcript — a step
// showing only its header, a run of read-only calls showing only its count —
// is asked what the query would find behind it, by drawing those rows as the
// fold would draw them if it opened. The number goes on the fold's own row,
// it is added to the rail's total, and [enter] opens the fold onto the first
// row that holds one.
//
// Drawing them is what makes the two counts one count. A row's rendered form
// is what a reader will see when the fold opens, so a match counted here is a
// match that will be marked there — the alternative, scanning the raw entry,
// counts occurrences in a tool result the row does not print and promises the
// reader occurrences the fold cannot deliver.
//
// The position on the rail is placed by line rather than by occurrence: every
// match a fold is covering sits at that fold's own row, which is where it is
// on screen. So `3/9` counts the six the reader can step through and the
// three behind the fold in the order they are actually in.

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// transcriptFold is one fold row and the entries it is covering right now:
// a step drawn as its header alone, or a run of read-only calls drawn as one
// counted group row. idx is the entry the fold's row is anchored to, which is
// the entry the reading cursor stands on and the entry the fold override is
// written to.
type transcriptFold struct {
	idx        int
	start, end int
	// step marks a step's header, whose override is stepFold; a group row's
	// is groupFold.
	step bool
}

// transcriptFolds lists every fold row on the transcript in the order they
// are drawn. It is one walk of the same blocks and slots the renderer walks
// (steps.go, fold.go), so a fold the reader can see is a fold the search can
// count behind, and there is no third list of what is folded.
//
// It asks whether a step is folded rather than building the step's header,
// because building one counts the query behind the fold — which is the work
// this walk exists to hand out, and paying for it here would pay for it
// twice.
func (m Model) transcriptFolds(es []entry) []transcriptFold {
	var folds []transcriptFold
	for _, blk := range m.blocksOf(es) {
		g := blk.step
		if g == nil || g.queued() {
			continue
		}
		if m.stepFolded(g, es, m.stepStateFor(blk, es)) {
			folds = append(folds, transcriptFold{idx: g.titleIdx, start: g.start, end: g.end, step: true})
			continue
		}
		for _, sl := range m.stepSlots(es, g) {
			if sl.group {
				folds = append(folds, transcriptFold{idx: sl.idx, start: sl.idx, end: sl.idx + sl.span})
			}
		}
	}
	return folds
}

// foldAt is the fold whose row the entry at idx is: the step header, or the
// group row. Nothing else on the transcript covers rows.
func (m Model) foldAt(es []entry, idx int) (transcriptFold, bool) {
	for _, f := range m.transcriptFolds(es) {
		if f.idx == idx {
			return f, true
		}
	}
	return transcriptFold{}, false
}

// searchRowWidth is the width one entry's row is drawn at, which is the
// width the fold will draw it at when it opens: two columns narrower for a
// row the reading cursor can stand on that is not already on the grid,
// because the gutter takes them (focus.go). A count taken at another width
// would split a match across a wrap the reader never sees.
func (m Model) searchRowWidth(e entry) int {
	w := m.transcriptWidth()
	if m.gutterShowing() && selectable(e) {
		w = gutterWidth(w, onGrid(e))
	}
	return max(w, 1)
}

// searchMatchesIn counts what the query would find in the entries [start,end)
// as the transcript draws them. It is the count behind a fold: the rows are
// rendered here exactly as opening the fold would render them, so the number
// on the fold's row is the number of marks that appear when it opens.
//
// It answers zero with no query rather than scanning, because it is asked
// from the render of every folded step on every frame.
func (m Model) searchMatchesIn(es []entry, start, end int) int {
	query := strings.ToLower(m.viewport.SearchQuery())
	if query == "" {
		return 0
	}
	n := 0
	for i := max(start, 0); i < min(end, len(es)); i++ {
		n += countMatches(m.renderEntry(es[i], m.searchRowWidth(es[i])), query)
	}
	return n
}

// countMatches counts the folded query in rendered text, line by line and
// cell by cell, the way the pane finds it (viewport.go's find): a match never
// spans a line break, and the escape sequences a row is dressed in are not
// part of what was said.
func countMatches(text, query string) int {
	if query == "" {
		return 0
	}
	n := 0
	for _, line := range strings.Split(text, "\n") {
		plain := strings.ToLower(ansi.Strip(line))
		for at := 0; ; {
			j := strings.Index(plain[at:], query)
			if j < 0 {
				break
			}
			at += j + len(query)
			n++
		}
	}
	return n
}

// searchMemo is the last census and the state it was a census of. A key that
// still matches is a census that is still true, so a frame that changed
// nothing the count depends on is answered without walking the session again.
type searchMemo struct {
	key   searchKey
	at    int
	total int
}

// searchKey is everything the position on the rail is a function of. It is
// deliberately not the transcript itself: what the folds are covering changes
// when the query changes, when a row lands, when a fold opens or when the
// pane finds a different number of occurrences — and a streaming turn writing
// into the row it already has changes none of those, which is the case the
// memo is here for.
//
// attached is on it because the reading of a child's session is a different
// transcript behind the same fields, and two transcripts of the same length
// with the same folds would otherwise share one answer.
type searchKey struct {
	query     string
	attached  string
	entries   int
	width     int
	folds     uint64
	verbosity verbosity
	gutter    bool
	// at and found are the pane's own pointer and count. The position moves
	// with them, and they move when the reader steps between occurrences or
	// when a row the query matches arrives at the live end.
	at, found int
}

// searchKeyFor reads the key off the model. The fold overrides are hashed
// rather than compared one by one because there is one per entry and the key
// has to be a value the memo can hold: the hash is arithmetic over three
// small integers per row, against the row renders it is standing in for.
func (m Model) searchKeyFor(es []entry) searchKey {
	const offset, prime = uint64(14695981039346656037), uint64(1099511628211)
	folds := offset
	for i := range es {
		folds ^= uint64(es[i].stepFold)<<4 | uint64(es[i].groupFold)<<2 | uint64(es[i].detailFold)
		folds *= prime
	}
	at, found := m.viewport.MatchPosition()
	return searchKey{
		query:     m.viewport.SearchQuery(),
		attached:  m.attachedTo,
		entries:   len(es),
		width:     m.transcriptWidth(),
		folds:     folds,
		verbosity: m.verbosity,
		gutter:    m.gutterShowing(),
		at:        at,
		found:     found,
	}
}

// searchPosition is the pointer's place among every occurrence in the
// session, and how many there are.
//
// The pane's own count is the visible half. The other half is what the folds
// are covering, and each fold's occurrences are placed at that fold's row —
// which is where they are on the screen — so the position steps over them in
// the order the reader would meet them.
func (m Model) searchPosition() (at, total int) {
	at, total = m.viewport.MatchPosition()
	if !m.viewport.Searching() {
		return at, total
	}
	es := *m.entries()
	key := m.searchKeyFor(es)
	// A box nobody has written to holds the zero key, whose query is empty —
	// and this is only reached with one that is not, so the first read is a
	// miss rather than an answer about no search at all.
	if m.searchMemo != nil && m.searchMemo.key == key {
		return m.searchMemo.at, m.searchMemo.total
	}
	hidden := m.searchHidden()
	if len(hidden) > 0 {
		// The line is the pane's own, so a fold the reader has scrolled past
		// is behind them in the count as well as on the screen.
		starts := m.entryLineStarts()
		cur, onOne := m.viewport.MatchLine()
		for _, h := range hidden {
			total += h.count
			if line, ok := starts[h.fold.idx]; ok && onOne && line < cur {
				at += h.count
			}
		}
	}
	if m.searchMemo != nil {
		*m.searchMemo = searchMemo{key: key, at: at, total: total}
	}
	return at, total
}

// hiddenMatches is one fold and how many occurrences it is covering.
type hiddenMatches struct {
	fold  transcriptFold
	count int
}

// searchHidden is what every fold on the transcript is covering: the fold's
// row and how many occurrences are behind it, in the order the folds are
// drawn. Folds covering nothing are left out, so an empty result means the
// pane is showing every match there is.
func (m Model) searchHidden() []hiddenMatches {
	if !m.viewport.Searching() {
		return nil
	}
	es := *m.entries()
	var out []hiddenMatches
	for _, f := range m.transcriptFolds(es) {
		if n := m.searchMatchesIn(es, f.start, f.end); n > 0 {
			out = append(out, hiddenMatches{fold: f, count: n})
		}
	}
	return out
}

// searchedRows is how many rows the search looked at: every entry in the
// transcript plus the header of every step, whether or not a fold is showing
// them. It is what the empty result reports, because "no match" is only worth
// reading beside the size of what was read.
func (m Model) searchedRows() int {
	es := *m.entries()
	rows := len(es)
	for _, blk := range m.blocksOf(es) {
		if blk.step != nil && blk.step.titleIdx == stepNoTitle {
			// A declared step nobody started is a header with no entry
			// behind it, so it is a row the count has not counted yet.
			rows++
		}
	}
	return rows
}

// matchesInside is what a fold row says about the query behind it. The word
// carries it and the number qualifies it, the way every counted fold row on
// the transcript states what it swallowed (invariant 4).
func matchesInside(n int) string {
	if n == 1 {
		return "1 match inside"
	}
	return fmt.Sprintf("%d matches inside", n)
}

// clearSearchFolds puts back every fold the search opened, and reports
// whether it put any back. A fold the reader opened is theirs and stays open:
// the two are told apart by the override that was written, foldSearch against
// foldOpen, so nothing has to remember a list of rows.
func (m *Model) clearSearchFolds() bool {
	es := *m.entries()
	found := false
	for i := range es {
		if es[i].stepFold == foldSearch {
			es[i].stepFold, found = foldAuto, true
		}
		if es[i].groupFold == foldSearch {
			es[i].groupFold, found = foldAuto, true
		}
	}
	if found {
		m.invalidateRenderCache()
		// A cursor standing on a row that has just folded away is a reading
		// position with nothing under it, so it comes back to the row that
		// swallowed it — the same answer a reflow gives (render.go).
		m.snapFocusOntoARow()
	}
	return found
}

// snapFocusOntoARow puts the reading cursor back on something selectable when
// what it was standing on stopped being drawn: the nearest row above it,
// which is the fold that took it.
func (m *Model) snapFocusOntoARow() {
	if m.state != stateFocus || m.focusIdx < 0 {
		return
	}
	idxs := m.expandableIndices()
	if len(idxs) == 0 {
		return
	}
	for _, idx := range idxs {
		if idx == m.focusIdx {
			return
		}
	}
	best := idxs[0]
	for _, idx := range idxs {
		if idx <= m.focusIdx {
			best = idx
		}
	}
	m.focusIdx = best
}

// openFoldToMatch is [enter] on a fold row the search is counting behind:
// the fold opens and the cursor lands on the first row inside that holds a
// match, so a session that says nine matches walks to nine of them.
//
// It opens one level. A step whose match is inside a folded run of reads
// gives back that run's counted row, still saying what it is covering, and
// the same key opens that — which is the fold's own grammar rather than a
// search that reaches through two of them at once.
//
// The fold is recorded as the search's, not the reader's: clearing the query
// puts it back, because it was opened to answer a question that is over.
func (m Model) openFoldToMatch() (Model, bool) {
	if !m.viewport.Searching() {
		return m, false
	}
	es := *m.entries()
	if m.focusIdx < 0 || m.focusIdx >= len(es) {
		return m, false
	}
	f, ok := m.foldAt(es, m.focusIdx)
	if !ok || m.searchMatchesIn(es, f.start, f.end) == 0 {
		return m, false
	}
	if f.step {
		es[f.idx].stepFold = foldSearch
	} else {
		es[f.idx].groupFold = foldSearch
	}
	m.invalidateRenderCache()
	m.focusFirstMatchUnder(es, f)
	m.refreshFocusView()
	m.pointAtFocusedRow()
	return m, true
}

// focusFirstMatchUnder moves the cursor to the first row the opened fold is
// now showing that holds a match — a row, or the counted row of a run that
// holds one, which is the row the next [enter] opens.
func (m *Model) focusFirstMatchUnder(es []entry, f transcriptFold) {
	if !f.step {
		for i := f.start; i < f.end; i++ {
			if m.searchMatchesIn(es, i, i+1) > 0 {
				m.focusIdx = i
				return
			}
		}
		return
	}
	blk, ok := m.stepBlockAt(es, f.idx)
	if !ok {
		return
	}
	for _, sl := range m.stepSlots(es, blk.step) {
		if m.searchMatchesIn(es, sl.idx, sl.idx+sl.span) > 0 {
			m.focusIdx = sl.idx
			return
		}
	}
}

// pointAtFocusedRow moves the search's pointer to the first occurrence on the
// row the cursor is now standing on, so the count on the rail and the row the
// reader is looking at are the same place.
func (m *Model) pointAtFocusedRow() {
	starts := m.unitLineStarts()
	if line, ok := starts[m.focusIdx]; ok {
		m.viewport.PointAtLine(line)
	}
	m.viewport.RevealMatch()
}

// focusMatchRow puts the reading cursor on the row the pointer's occurrence
// is on. That is how the occurrence the reader is on is told apart from the
// others: every match is bold and this one is bold on the lit row, which is a
// structural difference and so survives mono (invariant 1).
func (m *Model) focusMatchRow() {
	if m.state != stateFocus {
		return
	}
	line, ok := m.viewport.MatchLine()
	if !ok {
		return
	}
	starts := m.unitLineStarts()
	best, bestLine := -1, -1
	for _, idx := range m.expandableIndices() {
		if start, ok := starts[idx]; ok && start <= line && start >= bestLine {
			best, bestLine = idx, start
		}
	}
	if best < 0 || best == m.focusIdx {
		return
	}
	m.focusIdx = best
	// The lit row is a render of the row, and the row may sit in a block the
	// caches have frozen with another row lit (render.go, focus.go).
	m.redrawFocusContent()
	m.viewport.RevealMatch()
}
