package components

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// StopAnswer is what the stop confirm resolved to.
type StopAnswer int

const (
	// StopNothing is the default and what esc, n and enter give: nothing was
	// stopped.
	StopNothing StopAnswer = iota
	// StopCancel ends the turn and leaves the agent where it is.
	StopCancel
	// StopKill ends the agent and discards its workspace.
	StopKill
)

// StopConfirm is the one-row question the manager's stop key asks
// (docs/interface/surfaces.md#the-agent-manager): the inline confirm with two
// ways to say yes, drawn `[y] cancel · [k] kill`. It is not a surface of its
// own; esc declines it like every confirm, and stops nothing.
type StopConfirm struct {
	Prompt string
	// Turns are the agents a cancel reaches, which is more than the ones a
	// kill is handed: a kill takes the subtree of each name, a cancel is one
	// turn at a time.
	Turns []string
	// Yes is the word the confirm's yes is drawn as. Empty is the agent
	// manager's "cancel": the turn stops and the agent stays. The process
	// manager's yes is "stop", since a process or a call has no turn to
	// cancel apart from itself.
	Yes string
}

// Update resolves on the first decisive key. Only the two answers act.
func (c *StopConfirm) Update(msg tea.KeyPressMsg) (done bool, answer StopAnswer) {
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Confirm.Yes):
		return true, StopCancel
	case keys.Is(pressed, keys.Agent.Kill):
		return true, StopKill
	case keys.Is(pressed, keys.Confirm.No):
		return true, StopNothing
	}
	return false, StopNothing
}

// answers are the two keys, drawn the way a key row draws them. They are
// what a narrow pane may never clip, so the prompt gives way to them.
func (c *StopConfirm) answers() string {
	yes := c.Yes
	if yes == "" {
		yes = "cancel"
	}
	// The keys and the separator in chrome, the words in the terminal's own
	// tone: the prompt beside them is the body, and the answers are not.
	tones := HintTones{Key: sty.dim, Sep: sty.dim, Safe: sty.dim}
	return tones.Join([]KeyOffer{
		{Key: keys.Bracket(keys.Confirm.Yes), Label: yes},
		{Key: keys.Bracket(keys.Agent.Kill), Label: "kill"},
	})
}

func (c *StopConfirm) View(width int) string {
	answers := c.answers()
	return Clip(Clip(sty.body.Render(c.Prompt), max(width-lipgloss.Width(answers)-2, 1))+"  "+answers, width)
}
