package components

import (
	"strings"
	"unicode"

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
	return Clip(sty.Body.Render(c.Prompt)+"  "+confirmKeys(), width)
}

// confirmKeys is the answer set every confirm in the product draws: the two
// keys, with the default one capitalised.
//
// Only the capital is emphasised. The whole pair used to be drawn bold in
// Info, which said "these are keys" — something the brackets already say —
// and left the one fact the pair exists to carry, that the default is the
// answer which changes nothing, resting on the shape of a letter alone. Bold
// and bright on that letter and the chrome grey on everything around it puts
// the emphasis where the meaning is, and it survives a terminal with no
// colour, where the capital is still capital
// (docs/interface/surfaces.md#the-inline-confirm).
func confirmKeys() string {
	return confirmPair(keys.Shown(keys.Confirm.Yes), keys.Shown(keys.Confirm.No))
}

// confirmPair paints one bracketed answer set: the capital — whichever of the
// spellings it is — bold and bright, and every other cell chrome. It takes
// the spellings rather than the bindings because the undo confirm swaps one
// of them for a key spelled differently on purpose.
func confirmPair(shown ...string) string {
	var b strings.Builder
	b.WriteString(sty.Dim.Render("["))
	for i, s := range shown {
		if i > 0 {
			b.WriteString(sty.Dim.Render("/"))
		}
		// The first rune decides, and it has to be a letter that has a
		// lower case: `esc` and `+` are neither upper nor lower, and a
		// comparison against ToUpper would call them the default.
		if r := []rune(s); len(r) > 0 && unicode.IsUpper(r[0]) {
			b.WriteString(sty.Bright.Bold(true).Render(s))
			continue
		}
		b.WriteString(sty.Dim.Render(s))
	}
	b.WriteString(sty.Dim.Render("]"))
	return b.String()
}
