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
	// noteMissing marks a confirm attempt on a note-required option with an
	// empty note; the note border hint turns red until the next key.
	noteMissing bool
}

// NewNoteSelect builds the component with a single-line note field
// (alt+enter for a rare newline), mirroring the chat input's keymap.
func NewNoteSelect(title string, options []SelectOption) *NoteSelect {
	ta := textarea.New()
	ta.Placeholder = "note (optional)"
	ta.SetHeight(1)
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	// The note field takes the draft's newline keys, less the two the card
	// itself answers: tab moves between the note and the options, and enter
	// confirms.
	ta.KeyMap.InsertNewline.SetKeys(keys.Draft.Newline.Keys()[1])
	return &NoteSelect{Select: Select{Title: title, Options: options}, Note: ta}
}

func (s *NoteSelect) Update(msg tea.KeyPressMsg) (done bool, result any) {
	s.noteMissing = false
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Select.Note):
		s.FocusNote = !s.FocusNote
		if s.FocusNote {
			s.Note.Focus()
		} else {
			s.Note.Blur()
		}
		return false, nil
	case keys.Is(pressed, keys.Select.Take):
		idx := s.Select.Focus
		note := strings.TrimSpace(s.Note.Value())
		if idx < len(s.Select.Options) && s.Select.Options[idx].RequireNote && note == "" {
			s.noteMissing = true
			return false, nil
		}
		return true, NoteSelectResult{Index: idx, Note: note}
	case keys.Is(pressed, keys.Select.Cancel):
		return true, NoteSelectResult{Index: -1, Canceled: true}
	}
	if s.FocusNote {
		s.Note, _ = s.Note.Update(msg)
		return false, nil
	}
	// List focus with the query line open: the query line is the surface, so
	// everything but movement is text — the same reading the plain card makes
	//. Tab is still how the note is reached, which is why it is handled
	// above this and not here.
	if s.Select.Filtering {
		switch msg.String() {
		case "up":
			s.Select.move(-1)
		case "down":
			s.Select.move(1)
		default:
			s.Select.editQuery(msg)
		}
		return false, nil
	}
	// List focus: reuse the single-select movement; its enter/esc/digit paths
	// are unreachable here (handled above), except digits, which should type
	// nothing but jump focus without confirming.
	switch key := msg.String(); key {
	case "up", "k":
		s.Select.move(-1)
	case "down", "j":
		s.Select.move(1)
	case "/":
		if s.Select.Filterable {
			s.Select.Filtering = true
		}
	default:
		if n := digitIndex(key, s.Select.selectable()); n >= 0 {
			s.Select.Focus = s.Select.selectableIndex(n)
		}
	}
	return false, nil
}

func (s *NoteSelect) View(width int) string {
	inner := width - cardFrameWidth

	noteLabel := "note (optional)"
	labelStyle := sty.Dim
	if s.noteMissing {
		noteLabel = "note required"
		labelStyle = sty.Err
	}
	s.Note.SetWidth(max(inner-2, 8))
	noteView := s.Note.View()
	if !s.FocusNote {
		// The unfocused region dims; a plain-text echo avoids the
		// textarea's cursor artifacts.
		text := s.Note.Value()
		if text == "" {
			text = "(none)"
		}
		noteView = sty.Dimmer.Render(clip(text, max(inner-2, 8)))
	}
	// The note field and the hints are pinned under the list, so what they
	// spend comes off the list's budget before its window is drawn (S-116) —
	// otherwise a long list pushes the note itself off the card.
	tail := []string{labelStyle.Render(clip("┄ "+noteLabel, inner))}
	for _, l := range strings.Split(noteView, "\n") {
		tail = append(tail, clip("  "+l, inner))
	}
	// The note field's own key leads, because it is the one this card has
	// that the plain selector does not.
	hint := []string{words(keys.Select.Note, "note/options"), words(keys.Select.Take, "confirm")}
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
	return renderChromeCard(cardChrome{title: s.Select.Title, chips: s.Select.chips(shown)}, rows, width)
}
