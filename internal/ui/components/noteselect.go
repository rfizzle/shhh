package components

import (
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// NoteSelectResult is the note-selector Update result: the chosen option and
// the note text, confirmed together.
type NoteSelectResult struct {
	Index    int
	Note     string
	Canceled bool
}

// NoteSelect is a single-select plus an optional free-text note
// (docs/interface/surfaces.md#selectors): tab moves between the option list
// and the note, enter confirms both, esc cancels. An option with RequireNote
// refuses to confirm with an empty note.
type NoteSelect struct {
	Select    Select
	Note      textarea.Model
	FocusNote bool
	// Require refuses an empty note whatever row the pointer is on, for a
	// card whose whole answer is the note — a question asked as free text
	// has no rows for RequireNote to sit on, and one that says a note is
	// required means it of every row rather than of a chosen few.
	Require bool
	// noteMissing marks a confirm attempt on a note-required option with an
	// empty note; the note border hint turns red until the next key.
	noteMissing bool
}

// NewNoteSelect builds the component with a single-line note field
// (ctrl+j for a rare newline), mirroring the chat input's keymap.
//
// The field takes the shared newline chords unchanged (input.go) and needs
// none of its own: the two keys the card answers for itself, tab and enter,
// are not among them.
func NewNoteSelect(title string, options []SelectOption) *NoteSelect {
	ta := NewTextArea()
	ta.Placeholder = "note (optional)"
	ta.SetHeight(1)
	return &NoteSelect{Select: Select{Title: title, Options: options}, Note: ta}
}

func (s *NoteSelect) Update(msg tea.KeyPressMsg) (done bool, result NoteSelectResult) {
	s.noteMissing = false
	switch pressed := msg.String(); {
	// A card with no options is the field and nothing else, so there is
	// nowhere for the note key to move the keyboard to and it is not
	// offered: a key that cannot act does not act (invariant 5).
	case keys.Is(pressed, keys.Select.Note) && len(s.Select.Options) > 0:
		s.FocusNote = !s.FocusNote
		if s.FocusNote {
			s.Note.Focus()
		} else {
			s.Note.Blur()
		}
		return false, NoteSelectResult{}
	case keys.Is(pressed, keys.Select.Take):
		idx := s.Select.Focus
		note := strings.TrimSpace(s.Note.Value())
		required := s.Require || (idx < len(s.Select.Options) && s.Select.Options[idx].RequireNote)
		if required && note == "" {
			s.noteMissing = true
			return false, NoteSelectResult{}
		}
		return true, NoteSelectResult{Index: idx, Note: note}
	case keys.Is(pressed, keys.Select.Cancel):
		return true, NoteSelectResult{Index: -1, Canceled: true}
	}
	if s.FocusNote {
		s.Note, _ = s.Note.Update(msg)
		return false, NoteSelectResult{}
	}
	// List focus with the query line open: the query line is the surface, so
	// everything but movement is text — the same reading the plain card makes
	//. Tab is still how the note is reached, which is why it is handled
	// above this and not here.
	pressed := msg.String()
	if s.Select.Filtering {
		if !s.Select.moved(pressed) {
			s.Select.editQuery(msg)
		}
		return false, NoteSelectResult{}
	}
	// List focus: reuse the single-select movement; its enter/esc/digit paths
	// are unreachable here (handled above), except digits, which should type
	// nothing but jump focus without confirming.
	switch {
	case s.Select.moved(pressed):
	case keys.Is(pressed, keys.Select.Filter):
		if s.Select.Filterable {
			s.Select.Filtering = true
		}
	default:
		if n := digitIndex(pressed, s.Select.selectable()); n >= 0 {
			s.Select.Focus = s.Select.selectableIndex(n)
		}
	}
	return false, NoteSelectResult{}
}

// NoteMissing reports that the last confirm was refused because the note the
// answer needs is empty. It is how a host puts the keyboard where the missing
// words go: the card states the refusal, and where the reader is standing
// when they read it is the host's, because only the host knows whether this
// card is the whole screen or a row of one.
func (s *NoteSelect) NoteMissing() bool { return s.noteMissing }

func (s *NoteSelect) View(width int) string {
	inner := width - cardFrameWidth

	// The note field and the hints are pinned under the list, so what they
	// spend comes off the list's budget before its window is drawn —
	// otherwise a long list pushes the note itself off the card.
	tail := noteFieldRows(&s.Note, s.FocusNote, s.noteMissing, s.Require, inner)
	// The note field's own key leads, because it is the one this card has
	// that the plain selector does not — on a card that has options for it
	// to move between.
	var hint []string
	if len(s.Select.Options) > 0 {
		hint = append(hint, words(keys.Select.Note, "note/options"))
	}
	hint = append(hint, words(keys.Select.Take, "confirm"))
	switch {
	case s.Select.Filtering:
		hint = append(hint, words(keys.Select.ClearQ, "clear"))
	case s.Select.Filterable:
		hint = append(hint, offer(keys.Select.Filter))
	}
	hint = append(hint, offer(keys.Select.Cancel))
	tail = append(tail, hintRows([]string{strings.Join(hint, " · ")}, width)...)

	// The query line is pinned above the list exactly as it is on a plain
	// card, so the budget order is the artboard's — query line, key hints,
	// note field, and then the options take what is left.
	head := s.Select.queryRows(width)
	rows, shown := s.Select.visibleRows(width, s.Select.bodyBudget(len(head)+len(tail)), true)
	rows = append(head, rows...)
	rows = append(rows, tail...)
	rows = boundRows(rows, s.Select.MaxLines)
	return Card{Title: s.Select.Title, Chips: s.Select.chips(shown)}.Render(rows, width)
}
