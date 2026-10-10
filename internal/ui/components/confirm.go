package components

import (
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// Confirm is the inline one-line yes/no prompt
// (docs/interface/surfaces.md#the-inline-confirm) for moments that don't
// warrant a card. It renders in the input area; the default is No, and esc
// declines — never destroys.
type Confirm struct {
	Prompt string
	// NotYetLive says the prompt is drawn beside a draft that still holds the
	// keyboard, which the model's yes-or-no question is
	// (chat/question.go). The answer set goes with it: `y` and `n` are
	// letters of the sentence being typed until the keyboard is handed over,
	// and the host draws the key that hands it over in their place
	// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
	// Every other confirm in the product holds the keyboard and leaves this
	// false.
	NotYetLive bool
	// KeyList says the host answers `?` over this confirm with its register
	// and the glyph legend, so the answer set is followed by `[?] keys`. It
	// is the first thing the row gives up: a confirm is one line, and the
	// answers are what may never be clipped away.
	KeyList bool
}

// Update resolves on the first decisive key: y confirms; n, enter and esc
// decline (default No).
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
	if c.NotYetLive {
		return Clip(sty.body.Render(c.Prompt), width)
	}
	return Clip(sty.body.Render(c.Prompt)+"  "+c.withKeyList(confirmKeys(), width-lipgloss.Width(c.Prompt)-2), width)
}

// withKeyList is an answer set with `[?] keys` after it where the confirm
// offers it and the room holds both; where it does not, the answers alone.
func (c *Confirm) withKeyList(answers string, room int) string {
	if !c.KeyList {
		return answers
	}
	with := answers + sty.dim.Render(" · ") + keyListSegment()
	if lipgloss.Width(with) > room {
		return answers
	}
	return with
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
	b.WriteString(sty.dim.Render("["))
	for i, s := range shown {
		if i > 0 {
			b.WriteString(sty.dim.Render("/"))
		}
		// The first rune decides, and it has to be a letter that has a
		// lower case: `esc` and `+` are neither upper nor lower, and a
		// comparison against ToUpper would call them the default.
		if r := []rune(s); len(r) > 0 && unicode.IsUpper(r[0]) {
			b.WriteString(sty.bright.Bold(true).Render(s))
			continue
		}
		b.WriteString(sty.dim.Render(s))
	}
	b.WriteString(sty.dim.Render("]"))
	return b.String()
}
