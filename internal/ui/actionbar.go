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

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
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
	// ActionAlternatives is `[a]` — the other commands the generator
	// considered (S-114).
	ActionAlternatives
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

// bar is one offer on the row, built from the register (§7d): the key it
// answers to, the spelling it prints and the words beside it all come from
// one place. A caller with better words than the register's passes them; the
// key is never the caller's to choose.
func bar(b keys.Binding, label string, do Action, tone keyTone) key {
	if label == "" {
		label = keys.Words(b)
	}
	return key{press: b.Keys()[0], shown: keys.Shown(b), label: label, do: do, tone: tone}
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
	// others counts the alternatives on offer beside the command showing;
	// above zero, `[a]` opens the picker (S-114).
	others int
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

// SetAlternatives states how many other commands are on offer. Zero is the
// answer for every provider that cannot produce them and for every request
// with one sensible answer, and the row is then exactly what it was.
func (m ActionBarModel) SetAlternatives(n int) ActionBarModel {
	m.others = n
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
			out = append(out, bar(keys.OneShot.Confirm, "", m.runAction(), toneDanger))
		} else {
			out = append(out,
				bar(keys.OneShot.Run, "show what it would affect", ActionAffected, tonePrimary),
				bar(keys.OneShot.Confirm, "", m.runAction(), toneDanger),
			)
		}
	} else {
		label := "run"
		if m.multi {
			label = "run all"
		}
		out = append(out, bar(keys.OneShot.Run, label, m.runAction(), tonePrimary))
	}
	if m.multi {
		out = append(out, bar(keys.OneShot.Step, "", ActionRunStep, toneOffer))
	}
	if m.dryRun {
		out = append(out, bar(keys.OneShot.DryRun, "", ActionDryRun, toneOffer))
	}
	out = append(out,
		bar(keys.OneShot.Edit, "", ActionEdit, toneOffer),
		bar(keys.OneShot.Revise, "", ActionRevise, toneOffer),
	)
	if m.revision > 0 {
		out = append(out, bar(keys.OneShot.Back, "", ActionBack, toneOffer))
	}
	if m.others > 0 {
		// The count is the label rather than the key: what the reader wants
		// to know before pressing is how many there are, and `[a]` is what
		// the key row promises everywhere else — the key printed is the key
		// pressed.
		label := strconv.Itoa(m.others) + " others"
		if m.others == 1 {
			label = "1 other"
		}
		out = append(out, bar(keys.OneShot.Alternatives, label, ActionAlternatives, toneOffer))
	}
	out = append(out,
		bar(keys.OneShot.Explain, "", ActionExplain, toneOffer),
		bar(keys.OneShot.Copy, "", ActionCopy, toneOffer),
		bar(keys.OneShot.Save, "", ActionSave, toneOffer),
		bar(keys.OneShot.Quit, "", ActionCancel, toneQuiet),
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

// Update returns the bar itself rather than a tea.Model, for the same reason
// the stream does: it is a piece of a surface, not a program (S-155).
func (m ActionBarModel) Update(msg tea.Msg) (ActionBarModel, tea.Cmd) {
	msgKey, ok := msg.(tea.KeyPressMsg)
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
		b.WriteString(sty.Dim.Render("revision " + strconv.Itoa(m.revision) + "  "))
	}
	for i, k := range m.keys() {
		if i > 0 {
			b.WriteString(sty.KeyLabel.Render("  "))
		}
		b.WriteString(keyStyle(k.tone).Render("[" + k.shown + "]"))
		b.WriteString(sty.KeyLabel.Render(" " + k.label))
	}
	return sty.Bar.Render(b.String())
}

func keyStyle(t keyTone) lipgloss.Style {
	switch t {
	case tonePrimary:
		return sty.PrimaryKey
	case toneDanger:
		return sty.DangerKey
	case toneQuiet:
		return sty.KeyLabel
	}
	return sty.Key
}
