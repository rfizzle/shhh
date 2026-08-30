// Package browse is the saved-chat browser
// (docs/interface/surfaces.md#the-supporting-screens): a whole-screen list
// with a filter, a detail pane per item, and the housekeeping the picker
// inside a session offers — delete behind an inline confirm, rename in a
// one-line row (docs/capabilities/sessions-and-memory.md#housekeeping).
package browse

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

type Item struct {
	ID      string
	Title   string
	Preview string
	Detail  string
	// Deleting is what deleting the item also removes, in words — "and
	// its 2 branches" — so the confirm can state it
	// (docs/interface/surfaces.md#the-inline-confirm). Empty when nothing
	// else goes.
	Deleting string
}

// Ops are the housekeeping a host lets the list do to an item. A nil func
// is a key the list does not offer.
type Ops struct {
	Delete func(id string) error
	Rename func(id, name string) error
}

type ActionDef struct {
	Label    string
	Shortcut string
}

type ResultAction struct {
	Action string
	Item   Item
}

type Model struct {
	items    []Item
	filtered []int
	actions  []ActionDef

	filter textinput.Model
	cursor int
	action int

	width  int
	height int
	ready  bool
	detail bool
	quit   bool

	ops Ops
	// confirm is the armed delete confirm and target the item it names;
	// rename is the open rename row over the same target. notice is a
	// one-line answer that outlives the key that produced it — a refused
	// rename — until the next key.
	confirm  *components.Confirm
	target   int
	rename   textinput.Model
	renaming bool
	notice   string

	Result *ResultAction
}

// WithOps gives the list its housekeeping keys.
func (m Model) WithOps(ops Ops) Model {
	m.ops = ops
	return m
}

func New(items []Item, actions []ActionDef) Model {
	ti := textinput.New()
	ti.Placeholder = "Type to filter..."
	ti.CharLimit = 100

	filtered := make([]int, len(items))
	for i := range items {
		filtered[i] = i
	}

	return Model{
		items:    items,
		filtered: filtered,
		actions:  actions,
		filter:   ti,
	}
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true
		return m, nil

	case tea.KeyPressMsg:
		m.notice = ""
		if m.confirm != nil {
			return m.updateConfirm(msg)
		}
		if m.renaming {
			return m.updateRename(msg)
		}
		if m.filter.Focused() {
			switch msg.String() {
			case keys.Shown(keys.Select.Cancel):
				m.filter.Blur()
				m.filter.SetValue("")
				m.applyFilter()
				return m, nil
			case keys.Shown(keys.Browse.Take):
				m.filter.Blur()
				return m, nil
			default:
				var cmd tea.Cmd
				m.filter, cmd = m.filter.Update(msg)
				m.applyFilter()
				return m, cmd
			}
		}

		if m.detail {
			return m.updateDetail(msg)
		}
		return m.updateList(msg)
	}

	if m.filter.Focused() {
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m Model) updateList(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Browse.Quit):
		m.quit = true
		return m, tea.Quit
	case pressed == "j", pressed == "down":
		if len(m.filtered) > 0 {
			m.cursor++
			if m.cursor >= len(m.filtered) {
				m.cursor = len(m.filtered) - 1
			}
		}
	case pressed == "k", pressed == "up":
		if len(m.filtered) > 0 {
			m.cursor--
			if m.cursor < 0 {
				m.cursor = 0
			}
		}
	case keys.Is(pressed, keys.Browse.Open):
		if len(m.filtered) > 0 {
			m.detail = true
			m.action = 0
		}
	case keys.Is(pressed, keys.Browse.Filter):
		// Focusing the input is what starts its caret blinking in v2, so the
		// two are one call rather than a focus and a remembered follow-up.
		return m, m.filter.Focus()
	case keys.Is(pressed, keys.Browse.Delete) && m.ops.Delete != nil:
		if len(m.filtered) == 0 {
			return m, nil
		}
		item := m.items[m.filtered[m.cursor]]
		with := ""
		if item.Deleting != "" {
			with = " " + item.Deleting
		}
		m.target = m.filtered[m.cursor]
		m.confirm = &components.Confirm{Prompt: fmt.Sprintf("Delete %q%s? Files on disk are untouched.", item.Title, with)}
	case keys.Is(pressed, keys.Browse.Rename) && m.ops.Rename != nil:
		if len(m.filtered) == 0 {
			return m, nil
		}
		m.target = m.filtered[m.cursor]
		ti := textinput.New()
		ti.Prompt = "rename ▸ "
		ti.CharLimit = 0
		ti.SetValue(m.items[m.target].Title)
		ti.CursorEnd()
		ti.SetWidth(max(m.width-len(ti.Prompt)-2, 8))
		m.rename, m.renaming = ti, true
		return m, m.rename.Focus()
	}
	return m, nil
}

// updateConfirm answers the armed delete confirm: y deletes, everything
// else is No.
func (m Model) updateConfirm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	done, result := m.confirm.Update(msg)
	if !done {
		return m, nil
	}
	target := m.target
	m.confirm = nil
	if confirmed, _ := result.(bool); !confirmed {
		return m, nil
	}
	item := m.items[target]
	if err := m.ops.Delete(item.ID); err != nil {
		m.notice = "Could not delete: " + err.Error()
		return m, nil
	}
	m.items = append(m.items[:target:target], m.items[target+1:]...)
	m.applyFilter()
	m.notice = fmt.Sprintf("Deleted %q.", item.Title)
	return m, nil
}

// updateRename routes keys to the rename row: enter commits, esc keeps the
// old name, everything else types.
func (m Model) updateRename(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Select.Cancel):
		m.renaming = false
		return m, nil
	case keys.Is(pressed, keys.Browse.Take):
		m.renaming = false
		next := strings.TrimSpace(m.rename.Value())
		item := m.items[m.target]
		if next == "" || next == item.Title {
			return m, nil
		}
		if err := m.ops.Rename(item.ID, next); err != nil {
			m.notice = "Could not rename: " + err.Error()
			return m, nil
		}
		m.items[m.target].ID, m.items[m.target].Title = next, next
		// The detail's first line is the name under its label; only that
		// occurrence is the name, so only that one is replaced.
		m.items[m.target].Detail = strings.Replace(m.items[m.target].Detail, "Name:     "+item.Title, "Name:     "+next, 1)
		m.applyFilter()
		m.notice = fmt.Sprintf("Renamed %q to %q.", item.Title, next)
		return m, nil
	}
	var cmd tea.Cmd
	m.rename, cmd = m.rename.Update(msg)
	return m, cmd
}

func (m Model) updateDetail(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Browse.Leave):
		m.quit = true
		return m, tea.Quit
	case keys.Is(pressed, keys.Browse.Back):
		m.detail = false
		return m, nil
	case keys.Is(pressed, keys.Browse.Action):
		m.action++
		if m.action >= len(m.actions) {
			m.action = 0
		}
	case keys.Is(pressed, keys.Browse.Prev):
		m.action--
		if m.action < 0 {
			m.action = len(m.actions) - 1
		}
	case keys.Is(pressed, keys.Browse.Take):
		if len(m.actions) > 0 && len(m.filtered) > 0 {
			item := m.items[m.filtered[m.cursor]]
			m.Result = &ResultAction{
				Action: m.actions[m.action].Label,
				Item:   item,
			}
			return m, tea.Quit
		}
	default:
		for _, a := range m.actions {
			if msg.String() == a.Shortcut {
				if len(m.filtered) > 0 {
					item := m.items[m.filtered[m.cursor]]
					m.Result = &ResultAction{
						Action: a.Label,
						Item:   item,
					}
					return m, tea.Quit
				}
			}
		}
	}
	return m, nil
}

// View is the frame: the screen the browser paints, and the one terminal
// state it asks for. In v2 the alt screen is a field on the view rather than
// an option each host remembers to pass, so the browser owns the
// whole-window surface it was written for.
func (m Model) View() tea.View {
	v := tea.NewView(m.screen())
	v.AltScreen = true
	return v
}

func (m Model) screen() string {
	if m.quit {
		return ""
	}
	if !m.ready {
		return "Initializing…"
	}
	if m.detail {
		return m.viewDetail()
	}
	return m.viewList()
}

func (m Model) viewList() string {
	var b strings.Builder

	title := sty.ListTitle.Render(fmt.Sprintf(" %d items", len(m.filtered)))
	if m.filter.Focused() || m.filter.Value() != "" {
		title += "  " + m.filter.View()
	} else {
		title += sty.Hint.Render("  " + strings.Join(m.listHints(), "  "))
	}
	b.WriteString(title + "\n")
	b.WriteString(divider(m.width) + "\n")

	foot := m.footer()
	listHeight := m.height - 3 - len(foot)
	if listHeight < 1 {
		listHeight = 1
	}

	start, end := m.visibleRange(listHeight)
	for i := start; i < end; i++ {
		idx := m.filtered[i]
		item := m.items[idx]
		line := m.formatListItem(item, m.width-4)
		if i == m.cursor {
			b.WriteString(sty.Cursor.Render("> ") + sty.SelectedItem.Render(line) + "\n")
		} else {
			b.WriteString("  " + sty.Item.Render(line) + "\n")
		}
	}
	for _, line := range foot {
		b.WriteString(line + "\n")
	}

	return b.String()
}

// listHints is the list's key row, read from the register so it cannot say
// a key the list does not answer.
func (m Model) listHints() []string {
	hints := []string{
		keys.Shown(keys.Browse.Filter) + " " + keys.Words(keys.Browse.Filter),
		keys.Shown(keys.Browse.Open) + " select",
	}
	if m.ops.Delete != nil {
		hints = append(hints, keys.Shown(keys.Browse.Delete)+" "+keys.Words(keys.Browse.Delete))
	}
	if m.ops.Rename != nil {
		hints = append(hints, keys.Shown(keys.Browse.Rename)+" "+keys.Words(keys.Browse.Rename))
	}
	return append(hints, keys.Shown(keys.Browse.Quit)+" "+keys.Words(keys.Browse.Quit))
}

// footer is what the list draws under its rows: the armed confirm, the open
// rename row, or the notice the last key left.
func (m Model) footer() []string {
	switch {
	case m.confirm != nil:
		return []string{m.confirm.View(m.width)}
	case m.renaming:
		return []string{m.rename.View(), sty.Hint.Render(keys.Shown(keys.Browse.Take) + " renames  " + keys.Shown(keys.Select.Cancel) + " keeps the name")}
	case m.notice != "":
		return []string{sty.Hint.Render(m.notice)}
	}
	return nil
}

func (m Model) viewDetail() string {
	if len(m.filtered) == 0 {
		return "No item selected."
	}

	item := m.items[m.filtered[m.cursor]]
	var b strings.Builder

	b.WriteString(sty.DetailTitle.Render(item.Title) + "\n")
	b.WriteString(divider(m.width) + "\n")

	detailHeight := m.height - 5
	if detailHeight < 1 {
		detailHeight = 1
	}
	detail := item.Detail
	lines := strings.Split(detail, "\n")
	if len(lines) > detailHeight {
		lines = lines[:detailHeight]
	}
	b.WriteString(sty.DetailBody.Render(strings.Join(lines, "\n")) + "\n")

	b.WriteString(divider(m.width) + "\n")
	b.WriteString(m.renderActions() + "\n")

	return b.String()
}

func (m Model) renderActions() string {
	var parts []string
	for i, a := range m.actions {
		label := a.Label + " (" + a.Shortcut + ")"
		if i == m.action {
			parts = append(parts, sty.ActiveAction.Render(label))
		} else {
			parts = append(parts, sty.InactiveAction.Render(label))
		}
	}
	hint := sty.Hint.Render("  " + keys.Shown(keys.Browse.Back) + " " + keys.Words(keys.Browse.Back))
	return lipgloss.JoinHorizontal(lipgloss.Center, parts...) + hint
}

func (m Model) formatListItem(item Item, maxWidth int) string {
	title := item.Title
	preview := item.Preview
	if preview == "" {
		return truncate(title, maxWidth)
	}
	titleWidth := lipgloss.Width(title)
	sep := "  "
	remaining := maxWidth - titleWidth - lipgloss.Width(sep)
	if remaining <= 0 {
		return truncate(title, maxWidth)
	}
	return title + sep + sty.Preview.Render(truncate(preview, remaining))
}

func (m Model) visibleRange(height int) (int, int) {
	total := len(m.filtered)
	if total == 0 {
		return 0, 0
	}
	start := 0
	if m.cursor >= height {
		start = m.cursor - height + 1
	}
	end := start + height
	if end > total {
		end = total
	}
	return start, end
}

func (m *Model) applyFilter() {
	query := strings.ToLower(m.filter.Value())
	if query == "" {
		m.filtered = make([]int, len(m.items))
		for i := range m.items {
			m.filtered[i] = i
		}
	} else {
		m.filtered = nil
		for i, item := range m.items {
			if strings.Contains(strings.ToLower(item.Title), query) ||
				strings.Contains(strings.ToLower(item.Preview), query) {
				m.filtered = append(m.filtered, i)
			}
		}
	}
	if m.cursor >= len(m.filtered) {
		m.cursor = max(0, len(m.filtered)-1)
	}
}

func truncate(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= maxWidth {
		return s
	}
	if maxWidth <= 1 {
		return "…"
	}
	runes := []rune(s)
	for i := len(runes) - 1; i >= 0; i-- {
		candidate := string(runes[:i]) + "…"
		if lipgloss.Width(candidate) <= maxWidth {
			return candidate
		}
	}
	return "…"
}

func divider(width int) string {
	return sty.DividerLine.Render(strings.Repeat("─", width))
}
