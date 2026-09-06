package components

// The text fields. Every place in the product that takes typing — the draft,
// the note under a selector, the profile drafter's brief, the rename rows and
// the one-shot revise/edit/save fields — is one of bubbles' two field types,
// and they are built here rather than at nine call sites.
//
// The reason is colour. A field paints itself from a style table it carries
// inside its own model, and the table it is born with is a set of literal 256
// indices chosen for a dark terminal: a placeholder in grey 240, a prompt in
// colour 7, a cursor line and a selection on backgrounds the palette never
// issued. None of those is a rung of a token, and on a light ground they are
// the dark table's values
// (docs/interface/principles.md#a-colour-is-three-values-and-a-ground). So
// the table comes from the palette, once, and a field cannot be constructed
// without it.
//
// The fields stay bubbles' own types. What is shared is how one is built and
// how it is repainted, not what it is: a wrapper would put a second Update
// between a host and its field, and the hosts reach into theirs for the
// cursor, the value and the height.

import (
	"image/color"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// NewTextArea is the multi-line field, in the palette's colours and with the
// flags every field in the product shares: no line numbers, since no surface
// here draws a numbered gutter, and no character limit, since what a field
// will take is a fact about the surface rather than about the widget.
//
// Two keys insert a line break, one of which the reader can find: shift+enter
// is rewritten to ctrl+j before the field sees it (the chat's newline.go),
// and ctrl+j is the chord that works in a terminal too old to report it.
// Alt+enter used to be a third and went with the follow-up chord it shared,
// because Windows Terminal takes it for full screen
// (docs/interface/reserved-keys.md).
//
// The caller sets what is a fact about its own field: a placeholder, a
// height, a prompt, whether it is focused.
func NewTextArea() textarea.Model {
	f := textarea.New()
	f.ShowLineNumbers = false
	f.CharLimit = 0
	f.KeyMap.InsertNewline.SetKeys(keys.Draft.Newline.Keys()[1:]...)
	f.SetStyles(sty.TextArea)
	return f
}

// NewTextInput is the one-line field, on the same terms. It has no newline to
// declare and no line numbers to turn off, so the palette's table and the
// open character limit are the whole of what it shares.
func NewTextInput() textinput.Model {
	f := textinput.New()
	f.CharLimit = 0
	f.SetStyles(sty.TextInput)
	return f
}

// StyleTextArea and StyleTextInput repaint a live field from the palette as
// it stands now, and are called where a field is drawn.
//
// A palette swap — /ui theme, /ui mono, the terminal answering with its own
// background — rebuilds every style in the product through one door
// (swapPalette, palette.go), and a field's table is the one style that door
// cannot reach: it lives inside a Bubble Tea model, which is a value the
// program owns and hands back a copy of on every update, so there is no
// address for a callback to hold. Repainting where the field is drawn cannot
// go stale, which a registry of models that were copied out from under it
// would.
//
// Both ask before writing, and that is not an optimisation. Handing a field
// a table rebuilds its caret from the table's cursor colour, and rebuilding
// the caret puts it back in its lit phase — so a field written to on every
// frame is one whose caret never blinks off again. Asking first means the
// caret loses a beat only on the frame a palette actually moved.
func StyleTextArea(f *textarea.Model) {
	s := f.Styles()
	if painted(s.Focused.Placeholder, s.Focused.Text, s.Cursor.Color) {
		return
	}
	f.SetStyles(sty.TextArea)
}

// StyleTextInput is StyleTextArea for the one-line field.
func StyleTextInput(f *textinput.Model) {
	s := f.Styles()
	if painted(s.Focused.Placeholder, s.Focused.Text, s.Cursor.Color) {
		return
	}
	f.SetStyles(sty.TextInput)
}

// painted reports whether a field's table was built from the palette that is
// up now, by reading back the three tokens the table spends: chrome on the
// placeholder, body on the text, and the focus background on the caret. A
// table is a pure function of those three, so a field that has all three has
// the whole of it.
func painted(placeholder, text lipgloss.Style, cursor color.Color) bool {
	return placeholder.GetForeground() == Palette.Dim.Color() &&
		text.GetForeground() == Palette.Body.Color() &&
		cursor == Palette.FocusBg.Color()
}
