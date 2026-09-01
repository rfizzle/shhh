package components

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// SelectOption is one row of a selector: the label, the dim continuation
// beside it, and the short field right-aligned at the end of the row.
// RequireNote marks options that a NoteSelect refuses to confirm without a
// note.
type SelectOption struct {
	Label string
	// Desc is the option's own continuation, drawn dim on the same row in a
	// column of its own — the price beside a model, what a command does, when
	// a session was last touched. Every row carries it, not only the
	// focused one: a catalog you have to walk to read is a catalog you cannot
	// compare. The plan card is the exception and says why (FocusDesc).
	Desc string
	// Value is the row's own answer, drawn between the label column and the
	// description in a colour of its own: `⏵⏵ auto`, `⛨ workspace-write`,
	// `gpt-5.2`. It is the config screen's field and no other list's —
	// everywhere else a continuation is a note about the option, and a note
	// has nothing to be toned about. A list that sets none renders exactly as
	// it did before this field existed.
	Value string
	// ValueTone reads the value the way a card field is read: safe,
	// open, at risk, or an unremarkable statement of fact. The glyph beside
	// it says the same thing, so the colour is never carrying it alone
	// (invariant 1).
	ValueTone FieldTone
	// Meta is the right-aligned field at the end of the row: a key binding,
	// `this one`, the reason an unavailable row is unavailable. It is the
	// short field — one clause, never a sentence — and it is dropped before
	// Desc is, because Desc is the row's own words and Meta is a label on
	// them.
	Meta        string
	MetaTone    FieldTone
	RequireNote bool
	// Header marks a row that labels the run of options beneath it rather than
	// offering one — the palette's COMMANDS / SESSIONS / FILES rails (
	// docs/interface/surfaces.md#the-palette). Focus steps over it, it is never
	// numbered, and no key can land on it.
	Header bool
	// Dim marks an option that is showing but cannot be acted on right now,
	// rendered behind ⊘ with its Desc stating why. It stays
	// selectable, because choosing it is how the surface says why.
	Dim bool
}

// SelectResult is the single-select Update result.
type SelectResult struct {
	Index    int
	Canceled bool
	// Alt is set when AltKey took the option rather than enter. It is the
	// card's way of answering "which of the two readings of this choice",
	// for a surface where taking an option means one thing now and another
	// thing from now on.
	Alt bool
}

// queryCursor is the block that follows what has been typed into the filter
// row. It is a character rather than a terminal cursor because the card is
// drawn into a viewport, where there is only ever one real cursor and the
// input frame owns it.
const queryCursor = "█"

// Select is the single-select card (docs/interface/surfaces.md#selectors):
// ↑↓/jk move, enter selects, number keys select immediately, esc cancels, and
// — on a list long enough to want one — / opens a filter row above the
// window.
type Select struct {
	Title   string
	Options []SelectOption
	Focus   int
	// MaxLines bounds the card height, frame included; long lists scroll with
	// … markers. 0 means unbounded.
	MaxLines int
	// Chips ride the right end of the title border. A card that sets
	// none gets the window's own count instead — see chips.
	Chips []string
	// Hint replaces the default key-hint line for a surface whose keys are
	// not the default ones.
	Hint string
	// AltKey is a second way to take the focused option, and AltLabel is what
	// it buys. They are for a card whose choice has two readings — /model's
	// "this session" and "and from now on" — where an option that quietly
	// picked one of them would be a decision the card made for the reader.
	// Empty AltKey leaves the card with enter alone.
	//
	// Like j/k it is a bare letter, so it acts only while the query line is
	// closed: a card being typed into keeps every letter as text. On a card
	// that opens as a search that is ctrl+u away, and the key row says so.
	AltKey   string
	AltLabel string
	// EnterLabel is what enter buys, for a card where "select" is not the
	// whole answer because AltKey buys something else. Empty is "select".
	EnterLabel string
	// Actions are keys the host answers on the focused option beyond
	// taking it — the saved-chat picker's delete and rename. The card only
	// offers them on its key row; like AltKey they are bare letters and so
	// are text while the query line is open, which is the host's to honour
	// (it reads Filtering before answering one). They are offers and are
	// never dropped from the row.
	Actions []keys.Binding
	// Unnumbered drops the "1." prefixes and the number-jump keys, for a
	// surface where a digit is text rather than a jump.
	Unnumbered bool
	// FocusDesc keeps each option's Desc under the focused option instead of
	// on every row. It is the plan card's rule and no other surface's:
	// there the descriptions are consequences of taking the option, and four
	// consequences stacked at once is a wall rather than a choice. Everywhere
	// else the description is a property of the option, which is a thing you
	// read across the list.
	FocusDesc bool
	// QueryHint is the placeholder the query row draws while nothing has been
	// typed into it. A row opened by a key names what it is for; a surface
	// that is a query line from the start (the palette) does not need telling.
	QueryHint string

	// Filterable offers / on the key row and lets it open the query line.
	// Past a dozen entries walking is the slow way, so every picker
	// that opens over a catalog sets this; a card that is a fixed set of
	// answers does not.
	Filterable bool
	// Filtering is whether the query line is open. It is the card's own
	// state when / opened it, and a caller's when the surface is a query
	// line with a list under it from the start — the palette, and every card
	// that opens over a catalog rather than over a fixed set of answers
	// (docs/interface/surfaces.md#selectors). ctrl+u on an empty query closes
	// it again, which is how a card whose rows carry their own letters gets
	// them back.
	Filtering bool
	// Query is the text that produced Options. The component never filters:
	// the caller passes the matches and the query that made them, so the
	// match rule stays where it is chosen rather than hiding in a primitive
	//. Query is what the row echoes and what a matched run is bolded
	// against.
	Query string
	// Total is how many options Query was applied to, for the row's "4 of 24
	// match" and for the title rail. 0 means the caller has not said, and
	// the card counts what it was given instead.
	Total int
	// Closest names the nearest option that does exist, shown on the card
	// that matched nothing. The caller knows it because the caller is what
	// matched.
	Closest string

	// queryEdited records that the last Update changed Query, so a host can
	// re-run its match rule. QueryChanged reads and clears it.
	queryEdited bool

	// window is the slice of Options the card shows when the list is taller
	// than the card, and the shared one every long list scrolls
	// through the shared window — see listwindow.go. A filter that shortened the
	// list clamps it and a Focus outside it pulls it back, so no host has to
	// reset it.
	window listWindow
}

// geometry is what the shared window needs to know about this list: every
// option is one row — its description rides the row rather than sitting under
// it — except on a FocusDesc card, where the focused option carries its
// consequence underneath. Group rails are labels rather than options, so the
// markers do not offer to scroll to them.
func (s *Select) geometry() listGeometry {
	return listGeometry{
		n:     len(s.Options),
		focus: s.Focus,
		height: func(i int) int {
			if s.FocusDesc && i == s.Focus && !s.Options[i].Header && s.Options[i].Desc != "" {
				return 2
			}
			return 1
		},
		counts: func(i int) bool { return !s.Options[i].Header },
	}
}

func (s *Select) Update(msg tea.KeyPressMsg) (done bool, result any) {
	s.normalizeFocus()
	pressed := msg.String()
	switch {
	case pressed == "up":
		s.move(-1)
		return false, nil
	case pressed == "down":
		s.move(1)
		return false, nil
	case keys.Is(pressed, keys.Select.Take):
		// A card that matched nothing has nothing for enter to take, and a
		// key that cannot act does not act (invariant 5).
		if s.selectable() == 0 {
			return false, nil
		}
		return true, SelectResult{Index: s.Focus}
	case keys.Is(pressed, keys.Select.Cancel):
		// esc leaves the picker rather than closing the query line: the card asks
		// that leaving change nothing, and a filter you have to escape twice
		// is a mode.
		return true, SelectResult{Index: -1, Canceled: true}
	}
	// With the query line open, the query line is the surface: everything
	// that is not movement or dispatch is text. A digit typed into a model
	// name is a digit and so is a j — the reading the palette has always had
	//, which the filter row is what generalizes. It is also what
	// stops a model name with a 5 in it from switching the model mid-word.
	if s.Filtering {
		// Clearing a filter that is already empty closes the row, which is
		// how the card's own letters are got back without leaving it — the
		// history screen's rule, and the only way back for a card that opened
		// with the row already open.
		if keys.Is(pressed, keys.Select.ClearQ) && s.Query == "" {
			s.Filtering = false
			return false, nil
		}
		s.editQuery(msg)
		return false, nil
	}
	// The second reading of the focused option, checked before the digits so
	// a card whose alt key is a digit is still coherent. Like enter it needs
	// something to take.
	if s.AltKey != "" && pressed == s.AltKey && s.selectable() > 0 {
		return true, SelectResult{Index: s.Focus, Alt: true}
	}
	switch {
	case keys.Is(pressed, keys.Select.Filter):
		if s.Filterable {
			s.Filtering = true
		}
	case pressed == "k", pressed == "j":
		// On a list that is typed into, j and k are letters.
		if s.Unnumbered {
			break
		}
		if pressed == "k" {
			s.move(-1)
		} else {
			s.move(1)
		}
	default:
		if s.Unnumbered {
			break
		}
		if n := digitIndex(pressed, s.selectable()); n >= 0 {
			s.Focus = s.selectableIndex(n)
			return true, SelectResult{Index: s.Focus}
		}
	}
	return false, nil
}

// editQuery applies one keystroke to the open query line: ctrl+u clears it,
// backspace takes a rune back, and anything that types adds to it.
func (s *Select) editQuery(msg tea.KeyPressMsg) {
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Select.ClearQ):
		if s.Query == "" {
			return
		}
		s.Query, s.queryEdited = "", true
	case pressed == "backspace":
		if r := []rune(s.Query); len(r) > 0 {
			s.Query, s.queryEdited = string(r[:len(r)-1]), true
		}
	default:
		if text := typedRunes(msg); text != "" {
			s.Query, s.queryEdited = s.Query+text, true
		}
	}
}

// QueryChanged reports — and clears — whether the last Update edited the
// query. It is how a host learns to re-run its match rule: the card does not
// filter, so the answer to a changed query is new Options from the caller.
func (s *Select) QueryChanged() bool {
	changed := s.queryEdited
	s.queryEdited = false
	return changed
}

// typedRunes is what a key contributes to a query line, or "" for a key that
// types nothing — which in v2 is the key's own Text, empty for every special
// key and for a key held under a modifier.
func typedRunes(msg tea.KeyPressMsg) string { return msg.Text }

func (s *Select) View(width int) string {
	s.normalizeFocus()
	// The query line and the key hints are pinned: the list scrolls under
	// them, so what the card spends on them comes off the list's budget
	// before the window is drawn.
	head := s.queryRows(width)
	tail := hintRows(s.hintSegments(width), width)
	body, shown := s.visibleRows(width, s.bodyBudget(len(head)+len(tail)), !s.Unnumbered)
	rows := append(head, body...)
	rows = append(rows, tail...)
	rows = boundRows(rows, s.MaxLines)
	return renderChromeCard(cardChrome{title: s.Title, chips: s.chips(shown)}, rows, width)
}

// hintSegments is the card's key row, and the order it gives things up in.
// The filter changes it twice: an open query line offers the key that clears
// it, and a card that matched nothing offers only that one and esc, because
// nothing else on it can act.
//
// Nothing on a key row is ever truncated (invariant 4), so a row too long for
// the terminal sheds a segment rather than clipping one: the number-jump
// reminder goes first, because every row on screen is already carrying its
// own number, and then j/k, which is a second name for a key the row still
// offers. What an offer is — the filter, the selection, the way out — never
// goes.
// enterLabel is what enter buys, defaulting to the plain reading.
func (s *Select) enterLabel() string {
	if s.EnterLabel != "" {
		return s.EnterLabel
	}
	return "select"
}

// hasRowKeys reports whether closing the query line would hand anything back:
// the numbers, the card's second reading of a choice, or a host action. A card
// with none of them has nothing behind its filter row and never offers a way
// out of it.
func (s *Select) hasRowKeys() bool {
	return !s.Unnumbered || s.AltKey != "" || len(s.Actions) > 0
}

func (s *Select) hintSegments(width int) []string {
	if s.Hint != "" {
		return []string{s.Hint}
	}
	if s.Filtering {
		if s.selectable() == 0 {
			return []string{offer(keys.Select.ClearQ), offer(keys.Select.Cancel)}
		}
		// ctrl+u is one key with two readings, and the row names the one it
		// has: with something typed it clears; with nothing typed it closes
		// the row, which is what a card whose rows carry their own keys —
		// /model's [d], the saved chats' [x] and [r] — needs said, because
		// those keys are text until it does.
		back := words(keys.Select.ClearQ, "clear")
		if s.Query == "" {
			if !s.hasRowKeys() {
				return []string{offer(keys.Select.Move), offer(keys.Select.Take),
					offer(keys.Select.Cancel)}
			}
			back = words(keys.Select.ClearQ, "row keys")
		}
		return []string{offer(keys.Select.Move), offer(keys.Select.Take),
			back, offer(keys.Select.Cancel)}
	}
	move := offer(keys.Select.MoveJK)
	jump, filter := fmt.Sprintf("1–%d jump", s.selectable()), ""
	if s.Unnumbered {
		// No numbers to offer means no j/k either, on a list typed into.
		move, jump = offer(keys.Select.Move), ""
	}
	if s.Filterable {
		filter = offer(keys.Select.Filter)
	}
	// The two readings of the choice, when there are two. Both are offers and
	// so neither is ever dropped; what enter buys has to be named once the
	// alt key names something else, or the pair reads as "select, or this
	// other specific thing" and enter becomes the unlabelled one.
	take, alt := offer(keys.Select.Take), ""
	if s.AltKey != "" {
		take = words(keys.Select.Take, s.enterLabel())
		alt = s.AltKey + " " + s.AltLabel
	}
	inner := max(width-cardFrameWidth, 1)
	actions := make([]string, 0, len(s.Actions))
	for _, b := range s.Actions {
		actions = append(actions, offer(b))
	}
	rungs := [][]string{
		rung([]string{move, take, alt}, actions, []string{jump, filter, offer(keys.Select.Cancel)}),
		rung([]string{move, take, alt}, actions, []string{filter, offer(keys.Select.Cancel)}),
		rung([]string{offer(keys.Select.Move), take, alt}, actions, []string{filter, offer(keys.Select.Cancel)}),
	}
	for _, rung := range rungs {
		segs := presentSegments(rung)
		if lipgloss.Width(strings.Join(segs, " · ")) <= inner {
			return segs
		}
	}
	return presentSegments(rungs[len(rungs)-1])
}

// rung is one key row in order: what moves and takes, then the
// host's own actions, then the filter and the way out.
func rung(head, actions, tail []string) []string {
	out := make([]string, 0, len(head)+len(actions)+len(tail))
	out = append(out, head...)
	out = append(out, actions...)
	return append(out, tail...)
}

// presentSegments drops the rungs' empty placeholders, which is what a
// segment the card has no reason to offer leaves behind.
func presentSegments(segs []string) []string {
	out := make([]string, 0, len(segs))
	for _, s := range segs {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// queryRows is the pinned filter row: the ▸ prompt with what has been
// typed and its block cursor, and — for a caller that said how big the
// catalog behind it is — how many of that catalog the query matched. Both
// counts are on the row so the list it came from is never hidden, and where
// the terminal cannot carry them side by side they stack rather than one
// being dropped (invariant 4).
func (s *Select) queryRows(width int) []string {
	if !s.Filtering {
		return nil
	}
	inner := max(width-cardFrameWidth, 1)
	typed := sty.Info.Render("▸ ") + sty.QueryText.Render(s.Query+queryCursor)
	if s.Query == "" && s.QueryHint != "" {
		// A row that has just been opened by a key says what the key was for.
		// It goes as soon as anything is typed, because from then on the row
		// is showing what it is doing.
		typed += sty.Dim.Render(" " + s.QueryHint)
	}
	if s.Total <= 0 {
		return []string{clip(typed, inner)}
	}
	count := sty.Dim.Render(fmt.Sprintf("%d of %d match", s.selectable(), s.Total))
	if pad := inner - lipgloss.Width(typed) - lipgloss.Width(count); pad >= 2 {
		return []string{typed + strings.Repeat(" ", pad) + count}
	}
	return []string{clip(typed, inner), clip(count, inner)}
}

// chips is the count on the title rail: how big the catalog is, and — when
// the card could not show all of it — how much of it is on screen. A caller
// that set its own chips keeps them, and a list that fits with no filter open
// spends no rail saying that nothing was hidden.
func (s *Select) chips(shown int) []string {
	if len(s.Chips) > 0 {
		return s.Chips
	}
	matched := s.selectable()
	if !s.Filtering && shown >= matched {
		return nil
	}
	total := s.Total
	if total <= 0 {
		total = matched
	}
	chip := fmt.Sprintf("%d available", total)
	if shown < matched {
		chip += fmt.Sprintf(" · %d showing", shown)
	}
	return []string{chip}
}

// optionGrid is the column grid the option rows share: the width of the
// numbering column and of the label column the descriptions start after. It
// is measured over the whole list rather than over the window, so the
// description column does not shift under the reader as the window slides.
type optionGrid struct{ num, label int }

// grid measures the columns. A list where nothing has a description or a meta
// field spends no columns on either, so a plain menu renders exactly as wide
// as its longest row; and a label wider than half the card takes the row,
// with its description following one space behind rather than pushed off the
// edge.
func (s *Select) grid(numbered, inner int) optionGrid {
	var g optionGrid
	if numbered > 0 {
		g.num = numbered
	}
	continued := false
	for _, opt := range s.Options {
		if opt.Header {
			continue
		}
		if opt.Desc != "" || opt.Meta != "" || opt.Value != "" {
			continued = true
		}
	}
	if !continued || s.FocusDesc {
		return g
	}
	for _, opt := range s.Options {
		if opt.Header {
			continue
		}
		g.label = max(g.label, lipgloss.Width(s.labelText(opt)))
	}
	g.label = min(g.label, max(inner/2, 8))
	return g
}

// labelText is what the row says before its description: the option, behind
// the ⊘ that marks it unavailable. The glyph, not the dimming, is what says
// unavailable — colour never carries meaning alone (invariant 1).
func (s *Select) labelText(opt SelectOption) string {
	if opt.Dim {
		return "⊘ " + opt.Label
	}
	return opt.Label
}

// optionRows renders Options[lo:hi] with the ❯ pointer on the focused row.
// Numbering counts from the start of the list rather than from the window,
// because the number keys address the list and not what happens to be on
// screen.
func (s *Select) optionRows(width int, numbered bool, lo, hi int) []string {
	inner := width - cardFrameWidth
	numWidth := 0
	if numbered {
		numWidth = len(strconv.Itoa(s.selectable())) + 1
	}
	g := s.grid(numWidth, inner)
	var rows []string
	n := 0
	for i, opt := range s.Options {
		if opt.Header {
			if i >= lo && i < hi {
				// A group rail is info and bold, the way `decision/Select` draws it
				// (`c-info b`) and the way the palette and the config screen both show
				// it. It read dim until the config screen wanted the
				// SESSION / WORKSPACE rails and found the rails it already had painted as
				// chrome.
				rows = append(rows, sty.Headline.Render(clip(opt.Label, inner)))
			}
			continue
		}
		n++
		if i < lo || i >= hi {
			continue
		}
		rows = append(rows, s.optionRow(opt, n, i == s.Focus, g, inner))
		if s.FocusDesc && i == s.Focus && opt.Desc != "" {
			rows = append(rows, sty.Dim.Render(clip("    "+opt.Desc, inner)))
		}
	}
	return rows
}

// minDescWidth is the narrowest a description column is worth drawing. Below
// it the description is dropped whole rather than clipped to an ellipsis and
// two letters — a row that says nothing reads better than one that says
// almost nothing.
const minDescWidth = 8

// minValueWidth is the narrowest a value is worth drawing. It is lower than
// minDescWidth because a value is the row's answer rather than a note about
// it: `25` clipped to nothing tells the reader the setting is unset, which is
// a different and wrong thing.
const minValueWidth = 3

// descGap is the space between a continuation and what precedes it: two
// columns after the label, where the description is a field of its own, and
// one after a value, where it qualifies the value it follows —
// `normal — reads fold, mutations never do` reads as one clause and is one.
func descGap(value string) string {
	if value == "" {
		return "  "
	}
	return " "
}

// optionRow lays one option across the card: the pointer, the number
// right-aligned in its column, the label, the row's own value beside it, the
// description dim after that, and the meta field right-aligned at the end
// . What a narrow terminal cannot carry it gives up from the least
// load-bearing end: the description goes first, then the value is clipped,
// and the meta field — a whole clause naming why a row is what it is — is the
// last thing standing beside the label, which is the row and never goes.
func (s *Select) optionRow(opt SelectOption, n int, focused bool, g optionGrid, inner int) string {
	head := "  "
	if focused {
		head = "❯ "
	}
	if g.num > 0 {
		head += padLeft(strconv.Itoa(n)+".", g.num) + " "
	}
	label := s.labelText(opt)
	left := head + padRight(label, g.label)

	meta, value, desc := "", "", ""
	avail := inner - lipgloss.Width(left)
	if opt.Meta != "" && avail >= lipgloss.Width(opt.Meta)+2 {
		meta = opt.Meta
		avail -= lipgloss.Width(meta) + 2
	}
	if opt.Value != "" && avail >= minValueWidth+2 {
		value = clip(opt.Value, avail-2)
		avail -= lipgloss.Width(value) + 2
	}
	gap := descGap(value)
	if !s.FocusDesc && opt.Desc != "" && avail >= minDescWidth {
		desc = clip(opt.Desc, avail-len(gap))
	}

	// The focused row is painted whole: it is already bold, so a matched run
	// has no emphasis left to spend on it, and the pointer and the background
	// are what say which row this is.
	if focused {
		row := left
		if value != "" {
			row += "  " + value
		}
		if desc != "" {
			row += gap + desc
		}
		if meta != "" {
			row = padRight(row, inner-lipgloss.Width(meta)) + meta
		}
		return sty.FocusRow.Render(clip(row, inner))
	}

	body := emphasizeMatch(label, s.Query)
	if opt.Dim {
		// A row that cannot be acted on is not a row the query is hunting
		// for, and the dimming is one run: emphasis inside it would break the
		// run and say the wrong thing twice.
		body = sty.Dimmer.Render(label)
		desc, meta = sty.Dimmer.Render(desc), sty.Dimmer.Render(meta)
		value = sty.Dimmer.Render(value)
	} else {
		desc = sty.Dim.Render(desc)
		if value != "" {
			value = opt.ValueTone.style().Render(value)
		}
		if meta != "" {
			meta = opt.MetaTone.style().Render(meta)
		}
	}
	row := head + padRight(body, g.label)
	if lipgloss.Width(value) > 0 {
		row += "  " + value
	}
	if lipgloss.Width(desc) > 0 {
		row += gap + desc
	}
	if lipgloss.Width(meta) > 0 {
		row = padRight(row, inner-lipgloss.Width(meta)) + meta
	}
	return clip(row, inner)
}

// emphasizeMatch bolds the run of the label the query names. Bold and
// never a tint: exactly three background tints exist inside a screen and each
// already means one thing, and bold is the emphasis that survives mono.
//
// The match rule stays with the caller, so this only shows where a query the
// caller already accepted lands inside a row. A query that is not a literal
// run of the label — a subsequence match, a match on a field the row does not
// show — emphasizes nothing rather than guessing.
func emphasizeMatch(label, query string) string {
	if query == "" {
		return label
	}
	lower, lq := strings.ToLower(label), strings.ToLower(query)
	if len(lower) != len(label) || len(lq) != len(query) {
		// Lowering moved the bytes, so an offset into it is not an offset
		// into the label. Rare, and not worth emphasizing the wrong run.
		return label
	}
	i := strings.Index(lower, lq)
	if i < 0 {
		return label
	}
	return label[:i] + sty.Match.Render(label[i:i+len(query)]) + label[i+len(query):]
}

// bodyBudget is how many rows the option list may spend on a card of this
// height: the total less its frame and less everything pinned above and below
// the list. It returns 0 — unbounded — for a card with no height bound, which
// is what a test or a surface that sizes itself gets.
func (s *Select) bodyBudget(pinned int) int {
	return bodyBudget(s.MaxLines, pinned)
}

// visibleRows renders the option list windowed to a body budget, with the
// overflow markers the window makes necessary, and reports how many options
// ended up on screen. A card that wraps a Select renders the list itself —
// NoteSelect puts a note field under it — and goes through here too, so
// the pointer is never clipped off the bottom.
func (s *Select) visibleRows(width, budget int, numbered bool) ([]string, int) {
	rows, shown, _ := s.visibleRowsFocus(width, budget, numbered)
	return rows, shown
}

// visibleRowsFocus is visibleRows and where in what it returned the focused
// option's last row landed, for a host that draws something under that row
// rather than over the list: the config screen opens its picker there.
// It is -1 when the window does not hold the focus, which is what a filter
// that matched nothing leaves behind.
func (s *Select) visibleRowsFocus(width, budget int, numbered bool) ([]string, int, int) {
	s.normalizeFocus()
	if s.Filtering && s.selectable() == 0 {
		return s.noMatchRows(width), 0, -1
	}
	g := s.geometry()
	lo, hi := s.window.rangeFor(g, budget)
	rows := s.optionRows(width, numbered, lo, hi)
	focusAt := -1
	if s.Focus >= lo && s.Focus < hi {
		focusAt = g.rows(lo, s.Focus) + g.height(s.Focus) - 1
	}
	if lo > 0 {
		rows = append([]string{listOverflowRow("↑", g.countIn(0, lo), "", width)}, rows...)
		if focusAt >= 0 {
			focusAt++
		}
	}
	if hi < len(s.Options) {
		rows = append(rows, listOverflowRow("↓", g.countIn(hi, len(s.Options)), "", width))
	}
	return rows, g.countIn(lo, hi), focusAt
}

// noMatchRows is what a filter that matched nothing renders: a row, not
// an empty pane. The card keeps its frame, the query row above keeps both
// counts, the key that clears the filter stays on the key row, and a line
// names the nearest thing that does exist — which the caller supplies,
// because the caller is what matched.
func (s *Select) noMatchRows(width int) []string {
	inner := max(width-cardFrameWidth, 1)
	rows := []string{sty.Dim.Render(clip("  "+fmt.Sprintf("no match for %q", s.Query), inner))}
	if s.Closest != "" {
		rows = append(rows, sty.Dim.Render(clip("  closest is "+s.Closest, inner)))
	}
	return rows
}

// move steps the focus by delta, over any header rows in the way. A move that
// runs off either end leaves the focus where it was.
func (s *Select) move(delta int) {
	for i := s.Focus + delta; i >= 0 && i < len(s.Options); i += delta {
		if !s.Options[i].Header {
			s.Focus = i
			return
		}
	}
}

// normalizeFocus keeps the pointer on a row that can be chosen: a list that
// opens on a header — or on nothing, after a filter shortened it — moves the
// focus to the nearest option instead.
func (s *Select) normalizeFocus() {
	if len(s.Options) == 0 {
		s.Focus = 0
		return
	}
	if s.Focus < 0 {
		s.Focus = 0
	}
	if s.Focus >= len(s.Options) {
		s.Focus = len(s.Options) - 1
	}
	if !s.Options[s.Focus].Header {
		return
	}
	for i := s.Focus; i < len(s.Options); i++ {
		if !s.Options[i].Header {
			s.Focus = i
			return
		}
	}
	for i := s.Focus; i >= 0; i-- {
		if !s.Options[i].Header {
			s.Focus = i
			return
		}
	}
}

// FirstSelectable is the index of the first row a key can land on. A filtered
// list puts its pointer here after every keystroke, because the rows
// under it are not the rows that were there a moment ago.
func (s *Select) FirstSelectable() int {
	for i, opt := range s.Options {
		if !opt.Header {
			return i
		}
	}
	return 0
}

// selectable counts the rows a key can land on, which is every row until a
// list carries headers.
func (s *Select) selectable() int {
	n := 0
	for _, opt := range s.Options {
		if !opt.Header {
			n++
		}
	}
	return n
}

// selectableIndex maps a 1-based position among the selectable rows — what
// the number keys and the "1." prefixes count — to its index in Options.
func (s *Select) selectableIndex(n int) int {
	seen := 0
	for i, opt := range s.Options {
		if opt.Header {
			continue
		}
		if seen++; seen == n {
			return i
		}
	}
	return 0
}

// digitIndex maps a number key to a 1-based position among n rows, or -1.
func digitIndex(key string, n int) int {
	if len(key) != 1 || key[0] < '1' || key[0] > '9' {
		return -1
	}
	if pos := int(key[0] - '0'); pos <= n {
		return pos
	}
	return -1
}

// boundRows clips a row list to a card's MaxLines budget (frame included),
// replacing the last visible row with an … marker when rows were dropped.
func boundRows(rows []string, maxLines int) []string {
	if maxLines <= 0 {
		return rows
	}
	budget := max(maxLines-2, 1)
	if len(rows) <= budget {
		return rows
	}
	keep := max(budget-1, 1)
	return append(rows[:keep:keep], sty.Dim.Render("…"))
}
