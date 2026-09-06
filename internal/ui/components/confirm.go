package components

import (
	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// Confirm is the inline one-line yes/no prompt
// (docs/interface/surfaces.md#the-inline-confirm) for moments that don't
// warrant a card. It renders in the input area; the default is No, and esc
// declines — never destroys.
type Confirm struct {
	Prompt string
}

// Update resolves on the first decisive key: y confirms; n, enter, esc, and
// ctrl+c decline (default No).
func (c *Confirm) Update(msg tea.KeyPressMsg) (done, yes bool) {
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Confirm.Yes):
		return true, true
	case keys.Is(pressed, keys.Confirm.No):
		return true, false
	}
	return false, false
}

// confirmed routes a key to an armed question and takes the question down
// once it has been answered. The four screens that arm one had a copy of
// these ten lines each, and a copy is where a confirm that forgets to clear
// its own pointer comes from: the answer is read, the screen goes on, and
// the question is still on the surface. What is left at each screen is its
// own consequence, which is the only part that differs between them.
func confirmed(c **Confirm, msg tea.KeyPressMsg) (answered, yes bool) {
	if *c == nil {
		return false, false
	}
	done, yes := (*c).Update(msg)
	if !done {
		return false, false
	}
	*c = nil
	return true, yes
}

func (c *Confirm) View(width int) string {
	return Clip(c.Prompt+"  "+sty.Headline.Render(confirmKeys()), width)
}

// confirmKeys is the answer set every confirm in the product draws: the two
// keys, with the default one capitalised.
func confirmKeys() string {
	return "[" + keys.Shown(keys.Confirm.Yes) + "/" + keys.Shown(keys.Confirm.No) + "]"
}
