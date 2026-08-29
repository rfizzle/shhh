package chat

// The command palette (S-112, DESIGN-TUI.md §18a). Ctrl+K opens one prompt
// over everything the session can reach — the commands in the S-078 registry,
// the saved chats, and the files this session touched or the checkout changed
// most recently — filtered as you type.
//
// It complements the inline `/` menu rather than replacing it: `/` completes a
// command you are already typing, Ctrl+K finds one you are looking for. That
// difference is why the two treat an unavailable command differently. The menu
// drops a command that needs an idle turn, because it is completing something
// you are in the middle of typing; the palette keeps it, dimmed behind ⊘ with
// the reason on its description row, because the palette is where you look for
// a command you cannot find — and "it is not here" is the one answer that
// sends you hunting.
//
// The surface is statePick with a query on it, not a fourth list
// implementation: the same components.Select card, the same open/leave
// accounting, the same bottom-panel height. What the palette adds is the query
// line, the group rails, and a dispatch that runs the entry rather than
// applying an index — a command from the palette goes through runCommand, so
// an idle-only command answers with the same notice it would from the input.

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// paletteFileLimit bounds each half of the FILES group: the paths this
// session changed, and the checkout's most recently modified files.
const paletteFileLimit = 30

// paletteGroup is one of the three things the palette searches, in the order
// they render.
type paletteGroup int

const (
	paletteCommands paletteGroup = iota
	paletteSessions
	paletteFiles
)

// label is the group's rail, which is also the row it renders as.
func (g paletteGroup) label() string {
	switch g {
	case paletteSessions:
		return "SESSIONS"
	case paletteFiles:
		return "FILES"
	}
	return "COMMANDS"
}

// paletteEntry is one candidate, or — with header set — one of the rails
// between them. text is what enter dispatches and tab writes into the input;
// match holds every string the query is tested against, so an alias finds its
// command and a file is found by its base name as well as by its path.
type paletteEntry struct {
	group  paletteGroup
	header bool
	text   string
	label  string
	desc   string
	match  []string
	// space asks for a trailing space when tab writes the entry, because
	// something else follows it: a command's argument, a path in a sentence.
	space bool
	// meta is the short field right-aligned at the end of the row: a
	// command's key binding, and nothing that needs a sentence.
	meta string
	// dim is why the entry cannot be acted on right now — the registry's
	// idleOnly reason while the agent works. Empty means it can.
	dim string
	// rank is how well the query matched: 0 an exact command name, 1 a
	// prefix, 2 a subsequence.
	rank int
}

// paletteState is the open palette: what has been typed, every candidate
// gathered when it opened, and the rows currently showing — one per option in
// the picker, headers included, so a chosen row is an index into it.
type paletteState struct {
	query string
	all   []paletteEntry
	rows  []paletteEntry
}

// openPalette gathers the candidates and shows the palette over the frame.
// The dynamic halves — saved chats, recent files — are read here and not per
// keystroke (S-079).
func (m Model) openPalette() (tea.Model, tea.Cmd) {
	m.palette = &paletteState{all: m.paletteCandidates()}
	m.picker = &components.Select{
		Title:      "Palette",
		Unnumbered: true,
		// The palette is the filter row always open (S-123): the query line
		// it used to draw for itself is the card's own now, so the two cannot
		// disagree about what a query line looks like. It keeps its own chip,
		// which counts matches rather than a catalog.
		Filtering: true,
		Hint:      "enter run · tab complete · ↑↓ move · esc dismiss",
	}
	m.pickerApply = nil
	m.refreshPalette()
	m.enterSurface(statePick)
	m.syncViewport()
	return m, nil
}

// closePalette hands the screen back to whatever the turn became while the
// palette had it.
func (m *Model) closePalette() {
	m.palette = nil
	m.picker = nil
	m.pickerApply = nil
	m.leaveSurface()
}

// updatePalette routes keys while the palette is showing. Everything that is
// not movement, dispatch or dismissal is text: a digit is a digit and j is a
// j, which is why the card is unnumbered.
func (m Model) updatePalette(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Select.Cancel):
		m.closePalette()
		m.syncViewport()
		return m, nil

	case keys.Is(pressed, keys.Select.Palette.Prev):
		m.picker.Update(tea.KeyPressMsg{Code: tea.KeyUp})
		return m, nil

	case keys.Is(pressed, keys.Select.Palette.Next):
		m.picker.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		return m, nil

	case keys.Is(pressed, keys.Select.Palette.Run, keys.Select.Palette.Write):
		row, ok := m.paletteFocus()
		if !ok {
			return m, nil
		}
		m.closePalette()
		// A file has nothing to run: both keys put its path in the draft,
		// which is the only thing a path means to a prompt.
		if keys.Is(pressed, keys.Select.Palette.Write) || row.group == paletteFiles {
			m.paletteInsert(row)
			m.syncViewport()
			return m, nil
		}
		return m.dispatchPalette(row)

	}

	// Everything else belongs to the card's query line — backspace, ctrl+u
	// and every key that types. The palette stopped keeping its own copy of
	// that when the filter row landed (S-123); it keeps the match rule, which
	// is the half the component never had.
	m.picker.Update(msg)
	if m.picker.QueryChanged() {
		m.palette.query = m.picker.Query
		m.refreshPalette()
	}
	return m, nil
}

// refreshPalette rebuilds the card from the query: the matches, the rows that
// fit, the count on the title rail, and the pointer back on the first row a
// key can land on.
func (m *Model) refreshPalette() {
	p := m.palette
	matches := paletteMatches(p.all, p.query)
	p.rows = paletteRows(matches, m.paletteRowBudget())

	opts := make([]components.SelectOption, len(p.rows))
	for i, r := range p.rows {
		opts[i] = components.SelectOption{
			Label:  r.label,
			Desc:   r.desc,
			Meta:   r.meta,
			Header: r.header,
			Dim:    r.dim != "",
		}
	}
	m.picker.Options = opts
	m.picker.Query = p.query
	m.picker.Chips = []string{paletteCount(len(matches))}
	m.picker.MaxLines = m.maxConfirmPanelHeight()
	m.picker.Focus = m.picker.FirstSelectable()
}

// paletteRowBudget is how many result rows fit the bottom panel: everything
// the card spends before them — its frame, the query line and the hint run —
// comes off the top. Descriptions ride their own rows' right-hand columns
// since S-126, so the focused row no longer buys one of its own.
func (m Model) paletteRowBudget() int {
	return max(m.maxConfirmPanelHeight()-4, 1)
}

// paletteCount is the title rail's chip. It counts the matches, not the rows
// showing, because the rail is where you look to find out that there are
// more.
func paletteCount(n int) string {
	switch n {
	case 0:
		return "no matches"
	case 1:
		return "1 result"
	}
	return fmt.Sprintf("%d results", n)
}

// paletteFocus is the entry under the pointer, or false when the pointer is
// on nothing — an empty list, or a query that matched nothing.
func (m Model) paletteFocus() (paletteEntry, bool) {
	idx := m.picker.Focus
	if idx < 0 || idx >= len(m.palette.rows) || m.palette.rows[idx].header {
		return paletteEntry{}, false
	}
	return m.palette.rows[idx], true
}

// dispatchPalette runs the chosen entry. A command goes through the same
// dispatch the input uses, so an idle-only command answers with the notice
// that names what it would disturb rather than being refused here (S-087).
func (m Model) dispatchPalette(row paletteEntry) (tea.Model, tea.Cmd) {
	name := commandName(row.text)
	if name == "" {
		m.paletteInsert(row)
		m.syncViewport()
		return m, nil
	}
	m.recordInput(row.text)
	return m.runCommand(row.text, name)
}

// paletteInsert writes the entry into the draft rather than running it (tab,
// and enter on a file). It appends: the palette is opened over whatever was
// being typed, and a path picked mid-sentence belongs at the end of that
// sentence, not instead of it.
func (m *Model) paletteInsert(row paletteEntry) {
	val := m.input.Value()
	if val != "" && !strings.HasSuffix(val, " ") {
		val += " "
	}
	val += row.text
	if row.space {
		val += " "
	}
	m.input.SetValue(val)
	m.input.SetCursorColumn(len([]rune(val)))
	m.syncCompletions()
}

// --- candidates ------------------------------------------------------------

// paletteCandidates is everything the palette can offer, in group order.
func (m Model) paletteCandidates() []paletteEntry {
	out := m.paletteCommandEntries()
	out = append(out, m.paletteSessionEntries()...)
	return append(out, m.paletteFileEntries()...)
}

// paletteCommandEntries is the S-078 registry: the commands this session has
// wired, with their descriptions and their key bindings. A command that needs
// an idle turn is dimmed with its reason rather than dropped.
func (m Model) paletteCommandEntries() []paletteEntry {
	working := m.working()
	var rows []slashCommand
	for _, c := range slashCommands {
		if c.enabled != nil && !c.enabled(&m) {
			continue
		}
		rows = append(rows, c)
	}

	out := make([]paletteEntry, 0, len(rows))
	for _, c := range rows {
		e := paletteEntry{
			group: paletteCommands,
			text:  c.name,
			label: c.name,
			// The key binding is the row's meta field, right-aligned by the
			// card (§4a, S-126). It used to be padded into the label here,
			// which made a second column the component knew nothing about and
			// could not keep aligned once a filter shortened the list.
			meta:  c.key,
			desc:  c.desc,
			match: append([]string{strings.TrimPrefix(c.name, "/")}, trimSlashes(c.aliases)...),
			space: c.args != "",
		}
		if working && c.idleOnly != "" {
			// An unavailable command's shortcut is not an offer, so the meta
			// field states why it is unavailable instead of what would have
			// run it (invariant 5).
			e.dim, e.meta = c.idleOnly, ""
			e.desc = "needs the turn to be finished — " + c.idleOnly
		}
		out = append(out, e)
	}
	return out
}

// paletteSessionEntries is the saved chats, most recently updated first.
// Loading one replaces the conversation, so while the agent works they carry
// /load's own reason.
func (m Model) paletteSessionEntries() []paletteEntry {
	if m.db == nil {
		return nil
	}
	entries, err := m.db.ListChats()
	if err != nil {
		return nil
	}
	reason, _ := idleOnlyReason("/load")
	out := make([]paletteEntry, 0, len(entries))
	for _, e := range entries {
		label := e.Name
		if e.Name == m.sessionName {
			label += "  (current)"
		}
		row := paletteEntry{
			group: paletteSessions,
			text:  "/load " + e.Name,
			label: label,
			desc:  sessionDesc(e.Turns, e.UpdatedAt),
			match: []string{e.Name},
		}
		if m.working() {
			row.dim = reason
			row.desc = "needs the turn to be finished — " + reason
		}
		out = append(out, row)
	}
	return out
}

// paletteFileEntries is the paths this session changed, newest turn first,
// then the checkout's own recently modified files. A path this session
// touched is described by what it did to it, which is the thing you would
// have opened the palette to find.
func (m Model) paletteFileEntries() []paletteEntry {
	seen := make(map[string]bool)
	var out []paletteEntry
	if m.changes != nil {
		turns := m.changes.Turns()
		for i := len(turns) - 1; i >= 0 && len(out) < paletteFileLimit; i-- {
			for _, r := range turns[i].Records {
				if seen[r.Path] {
					continue
				}
				seen[r.Path] = true
				out = append(out, paletteEntry{
					group: paletteFiles,
					text:  r.Path,
					label: r.Path,
					desc:  fmt.Sprintf("changed this session · +%d −%d", r.Added, r.Removed),
					match: pathMatches(r.Path),
					space: true,
				})
			}
		}
	}
	now := time.Now()
	for _, f := range m.recentProjectFiles() {
		if seen[f.Path] {
			continue
		}
		seen[f.Path] = true
		out = append(out, paletteEntry{
			group: paletteFiles,
			text:  f.Path,
			label: f.Path,
			desc:  "modified " + agoLabel(f.Mod, now),
			match: pathMatches(f.Path),
			space: true,
		})
	}
	return out
}

// recentProjectFiles walks the checkout for its most recently modified files.
// The hook is what the tests set, so nothing in the package depends on the
// directory the suite happens to run in.
func (m Model) recentProjectFiles() []project.RecentFile {
	if m.recentFiles != nil {
		return m.recentFiles()
	}
	dir := ""
	if m.start != nil {
		dir = m.start.Project.Dir
	}
	return project.RecentFiles(dir, paletteFileLimit)
}

// pathMatches is what a file is found by: its whole path, so a directory
// narrows it, and its base name, so the name alone still ranks as a prefix.
func pathMatches(path string) []string {
	base := path
	if i := strings.LastIndex(path, "/"); i >= 0 {
		base = path[i+1:]
	}
	if base == path {
		return []string{path}
	}
	return []string{base, path}
}

// trimSlashes drops the leading / from a command's aliases, so the query is
// matched against them the way it is matched against the name.
func trimSlashes(names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, strings.TrimPrefix(n, "/"))
	}
	return out
}

// --- matching --------------------------------------------------------------

// paletteMatches filters and ranks the candidates against the query. Matching
// is subsequence-based across all three groups, because the whole point is
// finding something you only half remember; an exact command name outranks
// everything, so typing a command in full never leaves a longer sibling — or
// a file that happens to contain those letters — under the pointer.
func paletteMatches(all []paletteEntry, query string) []paletteEntry {
	q := strings.ToLower(strings.TrimSpace(query))
	q = strings.TrimPrefix(q, "/")
	if q == "" {
		return all
	}
	out := make([]paletteEntry, 0, len(all))
	for _, e := range all {
		if rank, ok := paletteRank(e, q); ok {
			e.rank = rank
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		// An exact command name floats over its own group and every other.
		if (out[i].rank == 0) != (out[j].rank == 0) {
			return out[i].rank < out[j].rank
		}
		if out[i].group != out[j].group {
			return out[i].group < out[j].group
		}
		return out[i].rank < out[j].rank
	})
	return out
}

// paletteRank scores one candidate against the lowered query: 0 an exact
// command name, 1 a prefix, 2 a subsequence, and false for no match at all.
func paletteRank(e paletteEntry, q string) (int, bool) {
	best, ok := 3, false
	for _, hay := range e.match {
		hay = strings.ToLower(hay)
		switch {
		case hay == q:
			rank := 1
			if e.group == paletteCommands {
				rank = 0
			}
			return rank, true
		case strings.HasPrefix(hay, q):
			best, ok = min(best, 1), true
		case subsequence(hay, q):
			best, ok = min(best, 2), true
		}
	}
	return best, ok
}

// paletteRows lays the matches out as the card's rows: a rail above each
// group that has anything in it, and the matches under it. What did not fit
// is counted on a last row rather than dropped silently (invariant 4) — the
// answer to it is to keep typing.
//
// When the budget cannot hold everything, each group that matched keeps a
// share of it rather than the first group taking the card. A query that found
// something in all three places has to say so: a palette that answers "there
// are no sessions" by not mentioning sessions is the thing this story exists
// to stop.
func paletteRows(matches []paletteEntry, budget int) []paletteEntry {
	if budget < 1 {
		budget = 1
	}
	groups := paletteByGroup(matches)
	take := paletteShares(groups, budget)

	rows := make([]paletteEntry, 0, budget)
	hidden := 0
	for _, g := range groups {
		n := take[g.group]
		if n > 0 {
			rows = append(rows, paletteEntry{group: g.group, header: true, label: g.group.label()})
			rows = append(rows, g.entries[:n]...)
		}
		hidden += len(g.entries) - n
	}
	if hidden == 0 {
		return rows
	}
	// The count needs a row of its own; the last result gives it up rather
	// than the card overflowing its panel.
	for len(rows)+1 > budget && len(rows) > 0 {
		if !rows[len(rows)-1].header {
			hidden++
		}
		rows = rows[:len(rows)-1]
	}
	rows = trimDanglingHeader(rows)
	return append(rows, paletteEntry{
		header: true,
		label:  fmt.Sprintf("… %d more — keep typing", hidden),
	})
}

// paletteGroupRun is one group's matches, in the order they will render.
type paletteGroupRun struct {
	group   paletteGroup
	entries []paletteEntry
}

// paletteByGroup partitions the matches, keeping both the group order and the
// order within each group that the ranking produced.
func paletteByGroup(matches []paletteEntry) []paletteGroupRun {
	var runs []paletteGroupRun
	for _, e := range matches {
		if n := len(runs); n > 0 && runs[n-1].group == e.group {
			runs[n-1].entries = append(runs[n-1].entries, e)
			continue
		}
		runs = append(runs, paletteGroupRun{group: e.group, entries: []paletteEntry{e}})
	}
	return runs
}

// paletteShares is how many of each group's matches fit: all of them when
// there is room, and otherwise an even share, with what one group cannot use
// handed to the ones that can.
func paletteShares(groups []paletteGroupRun, budget int) map[paletteGroup]int {
	take := make(map[paletteGroup]int, len(groups))
	need := 0
	for _, g := range groups {
		need += len(g.entries) + 1
	}
	if need <= budget {
		for _, g := range groups {
			take[g.group] = len(g.entries)
		}
		return take
	}

	// One row per rail, one for the count of what is left over.
	left := budget - len(groups) - 1
	if left < len(groups) {
		// Too tight to seat every group: fill in order and let the count row
		// speak for the rest.
		for _, g := range groups {
			n := min(max(left-1, 0), len(g.entries))
			take[g.group] = n
			left -= n + 1
			if left <= 0 {
				break
			}
		}
		return take
	}
	for _, g := range groups {
		n := min(left/len(groups), len(g.entries))
		take[g.group] = n
		left -= n
	}
	// Whatever an under-full group did not use goes to the ones still short.
	for _, g := range groups {
		if left <= 0 {
			break
		}
		spare := min(len(g.entries)-take[g.group], left)
		take[g.group] += spare
		left -= spare
	}
	return take
}

// trimDanglingHeader drops a rail left with nothing under it.
func trimDanglingHeader(rows []paletteEntry) []paletteEntry {
	for len(rows) > 0 && rows[len(rows)-1].header {
		rows = rows[:len(rows)-1]
	}
	return rows
}
