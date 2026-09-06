package components

import (
	"strconv"
	"strings"
	"testing"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

// fieldSGR reads one escape a text field emitted and reports whether the live
// palette issued it for the job it is doing: an attribute that carries
// weight, chrome or body as ink, and the focus background as ground.
// Anything else came from somewhere else — bubbles' own grey 240
// placeholder, its colour 7 prompt, its background 0 cursor line — and so
// does a token of the palette spent on the wrong half, a body ground or a
// focus-background ink.
//
// It walks the parameters rather than matching the whole list, because a
// style with an attribute on it writes both in one escape — the caret arrives
// as reverse video and a colour together.
func fieldSGR(params string, p ColorTokens) bool {
	fields := strings.Split(params, ";")
	for i := 0; i < len(fields); i++ {
		switch fields[i] {
		case "", "0", "1", "2", "3", "4", "7", "22", "23", "24", "27", "39", "49":
			continue
		case "38", "48":
			if i+2 >= len(fields) || fields[i+1] != "5" {
				return false
			}
			ink := fields[i] == "38"
			switch fields[i+2] {
			case index256(p.Dim), index256(p.Body):
				// The two greys of the table are ink and only ink: a body
				// ground would be text read against its own colour.
				if !ink {
					return false
				}
			case index256(p.FocusBg):
				// The focus background goes either way — under a covered
				// run it is the ground, and under the caret it is an ink
				// with reverse video over it.
			default:
				return false
			}
			i += 2
		default:
			return false
		}
	}
	return true
}

// TestTextFieldsFollowThePalette is the claim the factories exist for: a
// field draws in the tokens the live palette issued, through every door that
// swaps one — a theme by name, the terminal reporting its own background, and
// the mono switch. The placeholder is up in each render, because that is the
// row bubbles paints in a literal grey when nobody hands it a table.
func TestTextFieldsFollowThePalette(t *testing.T) {
	withColorProfile(t, colorprofile.ANSI256)

	const placeholder = "say what happened"
	cases := []struct {
		name string
		swap func()
	}{
		{"the shipped table", func() {}},
		{"a theme by name", func() {
			if err := SetTheme(ThemeCharm); err != nil {
				t.Fatalf("charm theme: %v", err)
			}
		}},
		{"the terminal reports a light ground", func() { SetGround(false) }},
		{"mono", func() { SetMono(true) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			themeRestore(t)

			// The field is built first and the palette moves under it, so
			// what is checked is the repaint rather than the construction.
			area := NewTextArea()
			area.Placeholder = placeholder
			area.SetWidth(40)
			area.SetHeight(1)
			area.Focus()
			line := NewTextInput()
			line.Placeholder = placeholder
			line.SetWidth(40)
			line.Focus()
			tc.swap()

			StyleTextArea(&area)
			StyleTextInput(&line)
			live := Palette
			for _, field := range []struct {
				kind, view string
			}{{"textarea", area.View()}, {"textinput", line.View()}} {
				if !strings.Contains(ansi.Strip(field.view), placeholder) {
					t.Fatalf("%s: the placeholder is not on screen: %q", field.kind, ansi.Strip(field.view))
				}
				for _, m := range sgrPattern.FindAllStringSubmatch(field.view, -1) {
					if !fieldSGR(m[1], live) {
						t.Errorf("%s emits SGR %q, which no token of the live palette issued", field.kind, m[1])
					}
				}
			}
		})
	}
}

// TestRepaintLeavesTheCaretBlinking is the cost of repainting where a field
// is drawn. Handing a field a style table rebuilds its caret and puts the
// caret back in its lit phase, so a repaint written on every frame is a caret
// that never blinks off again — which is why the repaint asks before it
// writes. The test drives a real blink through and then repaints under a
// palette that has not moved.
func TestRepaintLeavesTheCaretBlinking(t *testing.T) {
	withColorProfile(t, colorprofile.ANSI256)
	f := NewTextArea()
	f.SetWidth(40)
	f.SetHeight(1)
	f.Focus()
	f.SetValue("a note")

	lit := f.View()
	// The blink is a command that waits and answers with a message only the
	// cursor that asked for it accepts, so it is run rather than faked.
	f, cmd := f.Update(textarea.Blink())
	if cmd == nil {
		t.Fatal("a focused field should have asked for a blink")
	}
	f, _ = f.Update(cmd())
	dark := f.View()
	if dark == lit {
		t.Fatal("the caret should render differently once it has blinked off")
	}

	StyleTextArea(&f)
	if got := f.View(); got != dark {
		t.Error("repainting an unmoved palette relit the caret; the field should have been left alone")
	}
}

// TestTextFieldsCarryTheDraftsNewline covers the other half of the factory:
// the newline chords are declared once so a note field and the draft break a
// line on the same keys, rather than each call site remembering to say so.
func TestTextFieldsCarryTheDraftsNewline(t *testing.T) {
	f := NewTextArea()
	f.SetWidth(40)
	f.Focus()
	f, _ = f.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	f, _ = f.Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
	f, _ = f.Update(tea.KeyPressMsg{Code: 'b', Text: "b"})
	if got, want := f.Value(), "a\nb"; got != want {
		t.Fatalf("ctrl+j should break the line: got %q, want %q", got, want)
	}
	if got := f.LineCount(); got != 2 {
		t.Fatalf("the field should hold 2 lines, got %d", got)
	}
}

// TestTextFieldsShowNoLineNumbers holds the shared flags to a render rather
// than to the fields they are set on: a numbered gutter would shift every
// column of every field in the product, and it is off because no surface
// here draws one.
func TestTextFieldsShowNoLineNumbers(t *testing.T) {
	f := NewTextArea()
	f.SetWidth(40)
	f.SetHeight(2)
	f.SetValue("one\ntwo")
	for _, line := range strings.Split(ansi.Strip(f.View()), "\n") {
		if n := strings.TrimSpace(line); n != "" && strings.HasPrefix(n, strconv.Itoa(1)) {
			t.Fatalf("a line number leaked into the render: %q", line)
		}
	}
}
