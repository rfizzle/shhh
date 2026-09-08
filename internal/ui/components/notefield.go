package components

// The one-line note field, and the surfaces that carry one beside an answer
// (docs/interface/surfaces.md#selectors).
//
// It started as the note-selector's own three rows. A second card wanted the
// same field and a third wanted it under no card at all, and a note that
// looked slightly different depending on which surface asked for it would be
// three fields to learn rather than one — so the rows are drawn here and the
// hosts differ only in what they do with the text.

import (
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
)

// noteFieldRows renders the field: its label, then the text under it. The
// label says up front whether the note is wanted or needed, and turns red
// when a confirm was refused for an empty required one — which is the only
// thing on the field that is ever an error. Stating it up front is the
// difference between a field the reader may skip and one the answer does not
// work without, and the reader should not have to be refused to learn which.
//
// Unfocused it is a plain-text echo rather than the textarea's own view: the
// widget paints a cursor wherever it is drawn, and a cursor on a field the
// keyboard is not in would say the next character goes there.
func noteFieldRows(field *textarea.Model, focused, missing, required bool, inner int) []string {
	label, style := "note (optional)", sty.Dim
	if required {
		label = "note (required)"
	}
	if missing {
		label, style = "note required", sty.Err
	}
	field.SetWidth(max(inner-2, 8))
	// The placeholder says the same thing the label does, because on an
	// empty focused field it is the only one of the two the eye is on.
	if required {
		field.Placeholder = "note (required)"
	}
	StyleTextArea(field)
	view := field.View()
	if !focused {
		text := field.Value()
		if text == "" {
			text = "(none)"
		}
		view = sty.Dimmer.Render(Clip(text, max(inner-2, 8)))
	}
	rows := []string{style.Render(Clip("┄ "+label, inner))}
	for _, l := range strings.Split(view, "\n") {
		rows = append(rows, Clip("  "+l, inner))
	}
	return rows
}

// NoteBox is the field as a surface of its own, for a host whose answer is
// not a note-selector's: the checkbox list, and the inline confirm a question
// can be shaped as. It owns the focus and the refusal; what a note means is
// the host's.
type NoteBox struct {
	// Field is the text itself.
	Field textarea.Model
	// Focused is whether the keyboard is in the field. While it is, every
	// letter is text — the reading every surface being typed into makes
	// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
	Focused bool
	// Required says the answer does not work without a note, which the label
	// states before anybody is refused for it. Refusing is the host's: what
	// counts as an answer is not something a field can know.
	Required bool
	missing  bool
}

// NewNoteBox builds the field blurred, with the same single-line shape and
// newline chords the note-selector's has.
func NewNoteBox() *NoteBox {
	ta := NewTextArea()
	ta.Placeholder = "note (optional)"
	ta.SetHeight(1)
	return &NoteBox{Field: ta}
}

// Toggle moves the keyboard between the field and whatever is above it.
func (n *NoteBox) Toggle() {
	n.Focused = !n.Focused
	if n.Focused {
		n.Field.Focus()
	} else {
		n.Field.Blur()
	}
}

// Open puts the keyboard in the field, for a host opening it with the card
// because the answer is not usable without a note.
func (n *NoteBox) Open() {
	if !n.Focused {
		n.Toggle()
	}
}

// Update types one key into the field. The host routes here only while the
// field holds the keyboard.
func (n *NoteBox) Update(msg tea.KeyPressMsg) {
	n.Field, _ = n.Field.Update(msg)
}

// Value is the note, trimmed. An empty note is an empty string, never a
// missing one.
func (n *NoteBox) Value() string { return strings.TrimSpace(n.Field.Value()) }

// Refuse marks the field as the reason an answer was not taken, which turns
// the label red until the host clears it.
func (n *NoteBox) Refuse() { n.missing = true }

// Settle clears that mark. Hosts call it as each key arrives, so the refusal
// stands for exactly as long as the reader has not answered it.
func (n *NoteBox) Settle() { n.missing = false }

// Rows renders the field into a body of the given inner width.
func (n *NoteBox) Rows(inner int) []string {
	return noteFieldRows(&n.Field, n.Focused, n.missing, n.Required, inner)
}
