package components

import (
	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// Confirm is the inline one-line yes/no prompt (DESIGN-TUI.md §5) for moments
// that don't warrant a card. It renders in the input area; the default is No,
// and esc declines — never destroys.
type Confirm struct {
	Prompt string
}

// Update resolves on the first decisive key: y confirms; n, enter, esc, and
// ctrl+c decline (default No). The result is a bool.
func (c *Confirm) Update(msg tea.KeyPressMsg) (done bool, result any) {
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Confirm.Yes):
		return true, true
	case keys.Is(pressed, keys.Confirm.No):
		return true, false
	}
	return false, nil
}

func (c *Confirm) View(width int) string {
	return clip(c.Prompt+"  "+sty.Headline.Render(confirmKeys()), width)
}

// confirmKeys is the answer set every confirm in the product draws: the two
// keys, with the default one capitalised (§5).
func confirmKeys() string {
	return "[" + keys.Shown(keys.Confirm.Yes) + "/" + keys.Shown(keys.Confirm.No) + "]"
}
