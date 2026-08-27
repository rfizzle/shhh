package ui

// The one-shot's action bar (S-113, DESIGN-TUI.md §18b). It used to be a
// navigable menu: seven boxes, a cursor, arrow keys to move it and enter to
// take what was under it. That costs two keystrokes to reach a key that was
// already printed on the box, and it makes the front door the one surface in
// shhh where a key hint is not the key. It is now one row of bracketed keys,
// pressed directly, like every hint run in the session UI.
//
// The row is also where the safe default lives. On an ordinary command enter
// runs; on a destructive one enter spends itself on saying what would be
// affected, and running takes a deliberate `y` — the same keys either way, so
// nothing has to be re-learned, with the default doing the safe half of the
// job when the job has a dangerous half.

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type Action int

const (
	ActionNone Action = iota
	ActionRun
	ActionCopy
	ActionRevise
	ActionCancel
	ActionEdit
	ActionExplain
	ActionRunAll
	ActionRunStep
	ActionSave
	// ActionAffected is enter on a destructive command: state what the
	// command would reach, and leave running to `y`.
	ActionAffected
	// ActionDryRun is `[d]` — run the command's own no-op form.
	ActionDryRun
	// ActionBack is `[u]` — step back to the command before the last revise.
	ActionBack
)

type ActionSelectedMsg struct {
	Action Action
}

// key is one offer on the bar: the key as it is pressed, the key as it is
// drawn, what it does, and how it is coloured.
type key struct {
	press string
	shown string
	label string
	do    Action
	tone  keyTone
}

type keyTone int

const (
	toneOffer keyTone = iota
	tonePrimary
	toneDanger
	toneQuiet
)

// ActionBarModel is the key row. It holds no cursor because there is nothing
// to move: every offer is reachable by the key printed beside it.
type ActionBarModel struct {
	selected Action
	multi    bool
	// danger moves the safe default: enter states the radius, `y` runs.
	danger bool
	// dryRun is whether the command has a no-op form to offer (internal/dryrun).
	dryRun bool
	// affected is whether the radius block is already on screen, which is
	// what spends enter on a destructive command.
	affected bool
	// revision counts revises so far; above zero, `[u]` steps back.
	revision int
}

func NewActionBarModel() ActionBarModel {
	return ActionBarModel{}
}

func (m ActionBarModel) Selected() Action { return m.selected }

func (m ActionBarModel) SetMulti(multi bool) ActionBarModel {
	m.multi = multi
	return m
}

// SetDanger moves the safe default. It is set from the resolved radius, not
// from the words in the command, so the bar and the containment line above it
// cannot disagree about what the command is.
func (m ActionBarModel) SetDanger(danger bool) ActionBarModel {
	m.danger = danger
	return m
}

// SetDryRun offers `[d]` only where a dry run exists. A key that cannot be
// honoured is not offered (DESIGN-TUI.md §17a), and here the cost of offering
// one that is not there is running the real command.
func (m ActionBarModel) SetDryRun(available bool) ActionBarModel {
	m.dryRun = available
	return m
}

func (m ActionBarModel) SetAffected(shown bool) ActionBarModel {
	m.affected = shown
	return m
}

func (m ActionBarModel) SetRevision(n int) ActionBarModel {
	m.revision = n
	return m
}

// runAction is what running means for this command — one, or all of several.
func (m ActionBarModel) runAction() Action {
	if m.multi {
		return ActionRunAll
	}
	return ActionRun
}

// keys builds the row. Order is fixed: the default first, then the keys that
// change the command, then the ones that take it elsewhere, then the way out.
func (m ActionBarModel) keys() []key {
	var out []key
	if m.danger {
		if m.affected {
			out = append(out, key{press: "y", shown: "y", label: "run it", do: m.runAction(), tone: toneDanger})
		} else {
			out = append(out,
				key{press: "enter", shown: "↵", label: "show what it would affect", do: ActionAffected, tone: tonePrimary},
				key{press: "y", shown: "y", label: "run it", do: m.runAction(), tone: toneDanger},
			)
		}
	} else {
		label := "run"
		if m.multi {
			label = "run all"
		}
		out = append(out, key{press: "enter", shown: "↵", label: label, do: m.runAction(), tone: tonePrimary})
	}
	if m.multi {
		out = append(out, key{press: "t", shown: "t", label: "step by step", do: ActionRunStep})
	}
	if m.dryRun {
		out = append(out, key{press: "d", shown: "d", label: "dry run", do: ActionDryRun})
	}
	out = append(out,
		key{press: "e", shown: "e", label: "edit", do: ActionEdit},
		key{press: "r", shown: "r", label: "revise", do: ActionRevise},
	)
	if m.revision > 0 {
		out = append(out, key{press: "u", shown: "u", label: "back", do: ActionBack})
	}
	out = append(out,
		key{press: "x", shown: "x", label: "explain", do: ActionExplain},
		key{press: "c", shown: "c", label: "copy", do: ActionCopy},
		key{press: "s", shown: "s", label: "save", do: ActionSave},
		key{press: "esc", shown: "esc", label: "quit", do: ActionCancel, tone: toneQuiet},
	)
	return out
}

func (m ActionBarModel) Reset() ActionBarModel {
	m.selected = ActionNone
	return m
}

func (m ActionBarModel) Init() tea.Cmd {
	return nil
}

func (m ActionBarModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	msgKey, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	pressed := msgKey.String()
	for _, k := range m.keys() {
		if pressed != k.press {
			continue
		}
		m.selected = k.do
		action := k.do
		return m, func() tea.Msg { return ActionSelectedMsg{Action: action} }
	}
	if pressed == "q" {
		m.selected = ActionCancel
		return m, func() tea.Msg { return ActionSelectedMsg{Action: ActionCancel} }
	}
	return m, nil
}

// View draws the row, with the revision counter leading it when there is one
// to state. The counter is chrome and the keys are offers, so they are the
// two colours this row has.
func (m ActionBarModel) View() string {
	var b strings.Builder
	if m.revision > 0 {
		b.WriteString(DimStyle.Render("revision " + strconv.Itoa(m.revision) + "  "))
	}
	for i, k := range m.keys() {
		if i > 0 {
			b.WriteString(KeyLabelStyle.Render("  "))
		}
		b.WriteString(keyStyle(k.tone).Render("[" + k.shown + "]"))
		b.WriteString(KeyLabelStyle.Render(" " + k.label))
	}
	return BarStyle.Render(b.String())
}

func keyStyle(t keyTone) lipgloss.Style {
	switch t {
	case tonePrimary:
		return PrimaryKeyStyle
	case toneDanger:
		return DangerKeyStyle
	case toneQuiet:
		return KeyLabelStyle
	}
	return KeyStyle
}
