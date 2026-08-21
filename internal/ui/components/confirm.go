package components

import (
	tea "github.com/charmbracelet/bubbletea"
)

// Confirm is the inline one-line yes/no prompt (DESIGN-TUI.md §5) for moments
// that don't warrant a card. It renders in the input area; the default is No,
// and esc declines — never destroys.
type Confirm struct {
	Prompt string
}

// Update resolves on the first decisive key: y confirms; n, enter, esc, and
// ctrl+c decline (default No). The result is a bool.
func (c *Confirm) Update(msg tea.KeyMsg) (done bool, result any) {
	switch msg.String() {
	case "y", "Y":
		return true, true
	case "n", "N", "enter", "esc", "ctrl+c":
		return true, false
	}
	return false, nil
}

func (c *Confirm) View(width int) string {
	return clip(c.Prompt+"  "+headlineStyle.Render("[y/N]"), width)
}
