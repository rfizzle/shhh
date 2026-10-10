package components

// The backlog screen's filter: the query row typed into, and the rule its
// words are matched by. It is its own file because the rule a row is matched
// by and the words the header says about it are one decision, and the list
// beside it only draws what survives.

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// The status words a query takes. They are the three questions a reader
// asks of where an item stands; any other word is the title's, or a
// field's when it carries a colon.
const (
	backlogWordReady   = "ready"
	backlogWordBlocked = "blocked"
	backlogWordDone    = "done"
)

// backlogFilter is the query row over the list screens' shared filter,
// which holds the rows showing. It owns the query and whether its row is
// open; the fields a word can name are the host's, handed in by the screen.
// See docs/architecture.md#the-backlog-screens-pieces.
//
// It was a query and four cycles, one letter each — status, priority, kind,
// ready — and the letters cost the list its `k`. Words typed into the one
// row ask the same questions and more of them (`ready p:high kind:bug`), and
// they are on screen as typed, so the header never has to say what a run of
// presses left the filter standing on.
type backlogFilter struct {
	query     string
	filtering bool
	listFilter
}

// open puts the query row up, taking every letter.
func (b *backlogFilter) open() { b.filtering = true }

// forArchive drops the words about where an active item stands, for a step
// onto the archive: every archived item has the same status, so a `ready`
// carried in there would empty a list the reader had just asked to see.
func (b *backlogFilter) forArchive() {
	var kept []string
	for _, w := range strings.Fields(b.query) {
		if l := strings.ToLower(w); l != backlogWordReady && l != backlogWordBlocked {
			kept = append(kept, w)
		}
	}
	b.query = strings.Join(kept, " ")
}

// edit is the keyboard while the filter row is open, once the screen has
// taken the arrows for the pointer. Every letter is a letter here; enter
// closes the row and keeps what was typed, so the list's own keys come back
// over the list it narrowed, and esc backs out of the row one level at a
// time. It reports whether the filter changed.
func (b *backlogFilter) edit(msg tea.KeyPressMsg, pressed string) bool {
	switch {
	case keys.Is(pressed, keys.Backlog.Back):
		// Esc clears what was typed, and on an empty filter it closes the
		// row and hands the letters back — the rule every list in the
		// product answers to
		// (docs/interface/principles.md#esc-is-always-the-safe-answer).
		if b.query == "" {
			b.filtering = false
			return false
		}
		b.query = ""
	case keys.Is(pressed, keys.Backlog.Read):
		b.filtering = false
		return false
	case keys.Is(pressed, keys.Query.Rub):
		if r := []rune(b.query); len(r) > 0 {
			b.query = string(r[:len(r)-1])
		}
	default:
		b.query += typedRunes(msg)
	}
	return true
}

// clear puts the filter back to showing everything.
func (b *backlogFilter) clear() { b.query, b.filtering = "", false }

// words is what the header says the filter is. A list that is shorter than
// the backlog must say why on the screen that shortened it
// (docs/interface/principles.md#fold-never-hide).
func (b *backlogFilter) words() string {
	if q := strings.Join(strings.Fields(b.query), " "); q != "" {
		return "matching " + q
	}
	return ""
}

// match is the positions of the rows every word of the query leaves
// showing.
func (b *backlogFilter) match(rows []BacklogRow, priority BacklogField, fields []BacklogField) []int {
	terms := strings.Fields(strings.ToLower(b.query))
	var kept []int
	for i, row := range rows {
		if backlogMatches(row, terms, priority, fields) {
			kept = append(kept, i)
		}
	}
	return kept
}

// backlogMatches is the rule over one row: every word has to hold. A status
// word asks where the item stands, `field:word` asks what a header field
// says — `p:high`, `kind:bug`, the field named by any prefix of its name —
// and any other word is found in the slug or the title by the rule every
// list screen matches with.
//
// A file that will not parse answers none of the first two — it has no
// header — and it survives them rather than being hidden by one: the row is
// the only thing on screen saying the file is there, and a filter that
// swallowed it would be hiding exactly the item the reader has to go and
// fix.
func backlogMatches(row BacklogRow, terms []string, priority BacklogField, fields []BacklogField) bool {
	for _, term := range terms {
		ok, asked := backlogHeaderTerm(row, term, priority, fields)
		switch {
		case asked && row.State == BacklogUnreadable:
		case asked && !ok:
			return false
		case !asked && !matches(term, row.Slug, row.Title):
			return false
		}
	}
	return true
}

// backlogHeaderTerm answers one word that asks about the header, and
// reports whether it was one. A colon naming no field is text: a title may
// carry one.
func backlogHeaderTerm(row BacklogRow, term string, priority BacklogField, fields []BacklogField) (ok, asked bool) {
	switch term {
	case backlogWordReady:
		return row.State == BacklogReady, true
	case backlogWordBlocked:
		return row.State == BacklogBlocked, true
	case backlogWordDone:
		return row.State == BacklogArchived || row.Status == backlogWordDone, true
	}
	name, want, colon := strings.Cut(term, ":")
	if !colon || name == "" {
		return false, false
	}
	if strings.HasPrefix(strings.ToLower(priority.Name), name) {
		return strings.HasPrefix(strings.ToLower(row.Priority), want), true
	}
	for _, f := range fields {
		if strings.HasPrefix(strings.ToLower(f.Name), name) {
			return strings.HasPrefix(strings.ToLower(row.Values[f.Name]), want), true
		}
	}
	return false, false
}

// rows is the filter row: what has been typed, and where the next
// character goes.
func (b *backlogFilter) rows(width int) []string {
	if !b.filtering {
		return nil
	}
	typed := sty.info.Render(queryPrompt) + sty.queryText.Render(b.query+queryCursor)
	if b.query == "" {
		typed += sty.dim.Render(" type a title, ready, blocked, done, or field:word")
	}
	return []string{Clip(typed, width)}
}
