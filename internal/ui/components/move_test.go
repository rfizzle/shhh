package components

// The pointer moves on the keys the register declares, and on no others (
// docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
//
// Every list here used to compare `pressed == "j"` under a hint built from
// keys.Screen.Move, so the two agreed only because nobody had moved one. The
// shape of the check is reading mode's: press each keystroke the binding
// declares and watch the pointer, then press a letter the binding does not
// declare and watch it stay.
//
// The last test is the one a keymap file cares about. It moves screen.move
// onto a chord and asks the two screens the same two questions: does the new
// chord move the pointer, and has `j` stopped.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// press builds the keystroke a spelling names, the way a terminal would
// deliver it: a named key carries its own code and no text, and a letter
// carries both.
func press(spelling string) tea.KeyPressMsg {
	msg := tea.KeyPressMsg{}
	rest := spelling
	for {
		mod, after, found := strings.Cut(rest, "+")
		if !found {
			break
		}
		switch mod {
		case "shift":
			msg.Mod |= tea.ModShift
		case "ctrl":
			msg.Mod |= tea.ModCtrl
		case "alt":
			msg.Mod |= tea.ModAlt
		}
		rest = after
	}
	named := map[string]rune{
		"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight,
		"enter": tea.KeyEnter, "esc": tea.KeyEscape, "tab": tea.KeyTab,
		"backspace": tea.KeyBackspace, "space": tea.KeySpace,
		"pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown,
	}
	if code, ok := named[rest]; ok {
		msg.Code = code
		return msg
	}
	msg.Code = []rune(rest)[0]
	if msg.Mod == 0 {
		msg.Text = rest
	}
	return msg
}

// mover is one list host: what it is called, where its pointer is, and the
// binding the register says it moves on.
type mover struct {
	name  string
	bind  keys.Binding
	focus func() int
	send  func(tea.KeyPressMsg)
}

// movers builds one of each host afresh, so a test that moves a pointer
// cannot leave it moved for the next one.
func movers() []mover {
	c := configFixture()
	h := historyScreen()
	d := doctorScreen()
	l := &AgentList{Rows: managerRows()}
	s := &Select{Options: []SelectOption{{Label: "one"}, {Label: "two"}, {Label: "three"}}}
	m := &MultiSelect{
		Options: []SelectOption{{Label: "one"}, {Label: "two"}, {Label: "three"}},
		Checked: make([]bool, 3),
	}
	p := &Select{
		Options:    []SelectOption{{Label: "one"}, {Label: "two"}, {Label: "three"}},
		Unnumbered: true,
	}
	n := NewNoteSelect("why?", []SelectOption{
		{Label: "one"}, {Label: "two"}, {Label: "three"},
	})
	ctx := goldenContextScreen()
	back := goldenBacklogScreen()
	sprint := planScreen()
	prof := briefScreen()
	hosts := []mover{
		{"shhh config", keys.Screen.Move, func() int { return c.Focus },
			func(k tea.KeyPressMsg) { c.Update(k) }},
		{"shhh history", keys.Screen.Move, func() int { return h.Focus },
			func(k tea.KeyPressMsg) { h.Update(k) }},
		{"shhh doctor", keys.Screen.Move, func() int { return d.Focus },
			func(k tea.KeyPressMsg) { d.Update(k) }},
		{"the agent manager", keys.Agent.Move, func() int { return l.Focus },
			func(k tea.KeyPressMsg) { l.Update(k) }},
		{"the selector family", keys.Select.MoveJK, func() int { return s.Focus },
			func(k tea.KeyPressMsg) { s.Update(k) }},
		{"the multi-select", keys.Select.MoveJK, func() int { return m.Focus },
			func(k tea.KeyPressMsg) { m.Update(k) }},
		{"a setting's picker", keys.Select.Move, func() int { return p.Focus },
			func(k tea.KeyPressMsg) { p.Update(k) }},
		{"the note selector", keys.Select.MoveJK, func() int { return n.Select.Focus },
			func(k tea.KeyPressMsg) { n.Update(k) }},
		{"the context surface", keys.Context.Move, func() int { return ctx.Cursor },
			func(k tea.KeyPressMsg) { ctx.Update(k) }},
		{"the backlog screen", keys.Backlog.Move, func() int { return back.focus[back.tab] },
			func(k tea.KeyPressMsg) { back.Update(k) }},
		{"the sprint plan card", keys.Sprint.Move, func() int { return sprint.Plan.focus },
			func(k tea.KeyPressMsg) { sprint.Update(k) }},
		// The drafter's pointer starts on the field, at -1, and the starts
		// are under it, so it is the one surface here whose first row is not
		// where the pointer opens.
		{"the profile drafter", keys.Profile.Move, func() int { return prof.focus },
			func(k tea.KeyPressMsg) { prof.Update(k) }},
	}
	// Every one of them opens with its pointer at the top, where the half of
	// the binding that goes back has nowhere to go: a test asking whether it
	// moved there would be asking about the end of the list instead. So each
	// is stepped on one row first, with the half of its own binding that
	// goes on — the second keystroke of the pair.
	for _, host := range hosts {
		host.send(press(host.bind.Keys()[1]))
	}
	return hosts
}

// Every keystroke the binding declares moves the pointer. A hint that offers
// four and answers two is the drift the register exists to stop.
func TestEveryDeclaredMovementKeyMovesThePointer(t *testing.T) {
	for _, host := range movers() {
		for _, k := range host.bind.Keys() {
			before := host.focus()
			host.send(press(k))
			if host.focus() == before {
				t.Errorf("%s: %q is on %q and moved nothing", host.name, k, keys.Shown(host.bind))
			}
		}
	}
}

// And nothing else does. A letter the register does not put on the movement
// binding is a letter the surface spends on something else — or spends on
// nothing, which is still not the pointer.
func TestNoUndeclaredLetterMovesThePointer(t *testing.T) {
	for _, host := range movers() {
		for _, letter := range []string{"b", "g", "m", "v", "z"} {
			if keys.Is(letter, host.bind) {
				continue
			}
			// Twice: the first press settles any clamp a screen applies
			// when it is first asked about its own rows, and the second is
			// the one the pointer must not answer.
			host.send(press(letter))
			before := host.focus()
			host.send(press(letter))
			if host.focus() != before {
				t.Errorf("%s: %q is on no movement binding and moved the pointer",
					host.name, letter)
			}
		}
	}
}

// A keymap file moves the key, and the pointer goes with it. This is the
// claim the whole register rests on: the hint and the handler are one
// declaration, so a file that changes it changes both.
//
// The chord is shift+↑/shift+↓ and not alt+↑/alt+↓ because the terminal takes
// the alt pair (reserved.go), and a file that asked for it is refused whole.
func TestAKeymapMovesWhatTheScreensAnswer(t *testing.T) {
	was := keys.Screen.Move
	t.Cleanup(func() { keys.Screen.Move = was })

	path := filepath.Join(t.TempDir(), "keybindings.toml")
	body := "[screen]\nmove = [\"shift+up\", \"shift+down\"]\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := keys.Load(path); err != nil {
		t.Fatalf("the keymap was refused: %v", err)
	}

	c, h := configFixture(), historyScreen()
	for _, host := range []mover{
		{"shhh config", keys.Screen.Move, func() int { return c.Focus },
			func(k tea.KeyPressMsg) { c.Update(k) }},
		{"shhh history", keys.Screen.Move, func() int { return h.Focus },
			func(k tea.KeyPressMsg) { h.Update(k) }},
	} {
		before := host.focus()
		host.send(press("shift+down"))
		if host.focus() == before {
			t.Errorf("%s: the moved key does not move the pointer", host.name)
		}
		moved := host.focus()
		host.send(press("j"))
		if host.focus() != moved {
			t.Errorf("%s: j still moves the pointer after the key was moved", host.name)
		}
	}
}
