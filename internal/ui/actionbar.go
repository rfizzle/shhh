package ui

// The one-shot's action bar (
// docs/interface/surfaces.md#the-one-shot-result). It used to be a navigable
// menu: seven boxes, a cursor, arrow keys to move it and enter to take what
// was under it. That costs two keystrokes to reach a key that was already
// printed on the box, and it makes the front door the one surface in shhh
// where a key hint is not the key. It is now one row of bracketed keys,
// pressed directly, like every hint run in the session UI.
//
// The row has seven keys and the same seven whatever the command is rated:
// enter opens the view of what the command would affect, and running takes a
// deliberate `y`. Nothing has to be re-learned for a dangerous command,
// because no key on the row runs anything but the one that says so. What the
// bar used to spend letters on — the explanation, the other commands, the dry
// run, stepping through several, going back a revise — is a row of the view.

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
	ActionRunAll
	// ActionRunStep is a row of the view, not a key of the bar: run several
	// commands one at a time.
	ActionRunStep
	ActionSave
	// ActionShow is enter: open the view of what the command would affect.
	ActionShow
)

type ActionSelectedMsg struct {
	Action Action
}

// key is one offer on the bar: the binding it answers, the key as it is
// drawn, what it does, and how it is coloured.
type key struct {
	bind  keys.Binding
	shown string
	label string
	do    Action
	tone  keyTone
}

// bar is one offer on the row, built from the register: the key it
// answers to, the spelling it prints and the words beside it all come from
// one place. A caller with better words than the register's passes them; the
// key is never the caller's to choose.
func bar(b keys.Binding, label string, do Action, tone keyTone) key {
	if label == "" {
		label = keys.Words(b)
	}
	return key{bind: b, shown: keys.Shown(b), label: label, do: do, tone: tone}
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
	// danger tints the run, so a destructive command's `y` reads as the
	// deliberate key it is. It moves no key.
	danger bool
	// revision counts revises so far; above zero, the row leads with it.
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

// SetDanger is set from the resolved radius, not from the words in the
// command, so the bar and the containment line above it cannot disagree.
func (m ActionBarModel) SetDanger(danger bool) ActionBarModel {
	m.danger = danger
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

// keys builds the row: the way in to the view first, then the run, then the
// keys that change the command, then the ones that take it elsewhere, then
// the way out. The row is the register's seven and nothing else.
func (m ActionBarModel) keys() []key {
	run := "run it"
	if m.multi {
		run = "run them all"
	}
	tone := toneOffer
	if m.danger {
		tone = toneDanger
	}
	return []key{
		bar(keys.OneShot.Show, "", ActionShow, tonePrimary),
		bar(keys.OneShot.Confirm, run, m.runAction(), tone),
		bar(keys.OneShot.Edit, "", ActionEdit, toneOffer),
		bar(keys.OneShot.Revise, "", ActionRevise, toneOffer),
		bar(keys.OneShot.Copy, "", ActionCopy, toneOffer),
		bar(keys.OneShot.Save, "", ActionSave, toneOffer),
		bar(keys.OneShot.Quit, "", ActionCancel, toneQuiet),
	}
}

func (m ActionBarModel) Reset() ActionBarModel {
	m.selected = ActionNone
	return m
}

func (m ActionBarModel) Init() tea.Cmd {
	return nil
}

// Update returns the bar itself rather than a tea.Model, for the same reason
// the stream does: it is a piece of a surface, not a program.
func (m ActionBarModel) Update(msg tea.Msg) (ActionBarModel, tea.Cmd) {
	msgKey, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	pressed := msgKey.String()
	for _, k := range m.keys() {
		// The whole binding rather than the spelling it prints: a keymap file
		// that moved one moves what the row answers.
		if !keys.Is(pressed, k.bind) {
			continue
		}
		m.selected = k.do
		action := k.do
		return m, func() tea.Msg { return ActionSelectedMsg{Action: action} }
	}
	return m, nil
}

// View draws the row at the width it has to fit in, with the revision counter
// leading it when there is one to state. The counter is chrome and the keys
// are offers, so they are the two colours this row has.
//
// A row too wide for the terminal takes another row rather than losing its
// tail. The renderer holds one cell per column and drops what is past the
// last one, so a key row that overran did not run off the edge — it was
// never drawn, and the keys that go missing are the ones at the end: `[s]
// save` and the `[esc]` that says how to leave
// (docs/interface/principles.md#fold-never-hide).
func (m ActionBarModel) View(width int) string {
	offers := m.keys()
	segs := make([]string, 0, len(offers))
	for _, k := range offers {
		segs = append(segs, keyStyle(k.tone).Render("["+k.shown+"]")+sty.KeyLabel.Render(" "+k.label))
	}
	lead := ""
	if m.revision > 0 {
		lead = sty.Dim.Render("revision " + strconv.Itoa(m.revision) + "  ")
	}
	return strings.Join(packKeys(lead, segs, width), "\n")
}

// packKeys lays the offers out greedily, breaking between one key and the
// next and never inside one: half of `[esc] quit` on a line is not an offer
// anybody can take, and a key whose label wrapped away from it is a key with
// no word beside it (docs/interface/principles.md#colour-never-carries-meaning-alone).
//
// The lead is the revision counter, which stays on the first row because it
// says which command the keys under it belong to. The two spaces between one
// offer and the next are drawn in the label's own colour rather than left
// bare, so the run from a key to the one after it is one escape sequence.
func packKeys(lead string, segs []string, width int) []string {
	var (
		rows []string
		row  = lead
		used = lipgloss.Width(lead)
	)
	for i, seg := range segs {
		w := lipgloss.Width(seg)
		switch {
		case i == 0:
			row += seg
			used += w
		case width > 0 && used+2+w > width:
			rows = append(rows, row)
			row, used = seg, w
		default:
			row += sty.KeyLabel.Render("  ") + seg
			used += 2 + w
		}
	}
	return append(rows, row)
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
