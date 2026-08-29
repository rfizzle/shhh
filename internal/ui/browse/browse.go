package browse

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

type Item struct {
	ID      string
	Title   string
	Preview string
	Detail  string
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

	Result *ResultAction
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

	case tea.KeyMsg:
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

func (m Model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
		m.filter.Focus()
		return m, m.filter.Cursor.BlinkCmd()
	}
	return m, nil
}

func (m Model) updateDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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

func (m Model) View() string {
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
		title += sty.Hint.Render("  / filter  enter select  q quit")
	}
	b.WriteString(title + "\n")
	b.WriteString(divider(m.width) + "\n")

	listHeight := m.height - 3
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

	return b.String()
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
