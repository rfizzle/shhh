package components

import (
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

// NoteSelectResult is the note-selector Update result: the chosen option and
// the note text, confirmed together.
type NoteSelectResult struct {
	Index    int
	Note     string
	Canceled bool
}

// NoteSelect is a single-select plus an optional free-text note
// (DESIGN-TUI.md §4c): tab moves between the option list and the note, enter
// confirms both, esc cancels. An option with RequireNote refuses to confirm
// with an empty note.
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
	ta.KeyMap.InsertNewline.SetKeys("alt+enter")
	return &NoteSelect{Select: Select{Title: title, Options: options}, Note: ta}
}

func (s *NoteSelect) Update(msg tea.KeyMsg) (done bool, result any) {
	s.noteMissing = false
	switch msg.String() {
	case "tab":
		s.FocusNote = !s.FocusNote
		if s.FocusNote {
			s.Note.Focus()
		} else {
			s.Note.Blur()
		}
		return false, nil
	case "enter":
		idx := s.Select.Focus
		note := strings.TrimSpace(s.Note.Value())
		if idx < len(s.Select.Options) && s.Select.Options[idx].RequireNote && note == "" {
			s.noteMissing = true
			return false, nil
		}
		return true, NoteSelectResult{Index: idx, Note: note}
	case "esc", "ctrl+c":
		return true, NoteSelectResult{Index: -1, Canceled: true}
	}
	if s.FocusNote {
		s.Note, _ = s.Note.Update(msg)
		return false, nil
	}
	// List focus: reuse the single-select movement; its enter/esc/digit paths
	// are unreachable here (handled above), except digits, which should type
	// nothing but jump focus without confirming.
	switch key := msg.String(); key {
	case "up", "k":
		if s.Select.Focus > 0 {
			s.Select.Focus--
		}
	case "down", "j":
		if s.Select.Focus < len(s.Select.Options)-1 {
			s.Select.Focus++
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
	rows := s.Select.optionRows(width, true)

	noteLabel := "note (optional)"
	labelStyle := dimStyle
	if s.noteMissing {
		noteLabel = "note required"
		labelStyle = errStyle
	}
	s.Note.SetWidth(max(inner-2, 8))
	noteView := s.Note.View()
	if !s.FocusNote {
		// The unfocused region dims (§4c); a plain-text echo avoids the
		// textarea's cursor artifacts.
		text := s.Note.Value()
		if text == "" {
			text = "(none)"
		}
		noteView = dimmerStyle.Render(clip(text, max(inner-2, 8)))
	}
	rows = append(rows, labelStyle.Render(clip("┄ "+noteLabel, inner)))
	for _, l := range strings.Split(noteView, "\n") {
		rows = append(rows, clip("  "+l, inner))
	}
	rows = append(rows, hintRows([]string{"tab note/options · enter confirm · esc cancel"}, width)...)
	rows = boundRows(rows, s.Select.MaxLines)
	return renderCard(s.Select.Title, rows, width)
}
