package chat

// The staged strip as reading mode's last target, and the chip as a door
// (docs/interface/surfaces.md#a-staged-attachment).

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// The cursor walks off the last transcript row onto the strip and back; the
// arrows pick along it; the draft is the same sentence throughout. With
// nothing above the strip to stand on — the first screenshot of a session —
// ctrl+o lands on the strip directly, and dropping the last chip hands the
// cursor back to the transcript.
func TestFocus_ReachesTheStagedRail(t *testing.T) {
	t.Run("off the last row and back", func(t *testing.T) {
		m := focusModel(t)
		m = stageImage(t, m, "one.png")
		m = stageImage(t, m, "two.png")
		m = stageText(t, m, "notes.md")
		m.input.SetValue("the parser drops the last")
		draft := m.input.Value()

		m = pressOn(t, m, readingChord())
		last := m.lastRow()
		if m.focusIdx != last || m.atStrip() {
			t.Fatalf("reading mode opens on the last row, got %d (strip %v)", m.focusIdx, m.atStrip())
		}
		m = pressOn(t, m, key('j'))
		if !m.atStrip() || m.pickedChip() != 0 {
			t.Fatalf("j off the last row lands on the strip's first chip: strip %v, chip %d", m.atStrip(), m.pickedChip())
		}
		m = pressOn(t, m, arrow(false))
		if m.pickedChip() != 1 {
			t.Fatalf("→ picks the next chip, got %d", m.pickedChip())
		}
		panel := stripANSI(strings.Join(m.focusHintLines(), "\n"))
		for _, want := range []string{"❯ Image#2", "[←→] chip", "[enter] open", "[x] drop", "[esc] back to the draft", "chip 2 of 3"} {
			if !strings.Contains(panel, want) {
				t.Fatalf("the panel should carry %q:\n%s", want, panel)
			}
		}
		// The one pointer left in the transcript is the sent message's own
		// prompt glyph, which the plain feed draws too.
		if body, _, _ := m.renderFocusHistory(); strings.Count(stripANSI(body), "❯") != strings.Count(stripANSI(m.renderHistory()), "❯") {
			t.Fatalf("on the strip no transcript row is lit:\n%s", stripANSI(body))
		}
		m = pressOn(t, m, key('k'))
		if m.atStrip() || m.focusIdx != last {
			t.Fatalf("k leaves the strip back to the last row, got %d (strip %v)", m.focusIdx, m.atStrip())
		}
		m = pressOn(t, m, key('j'))
		if m.pickedChip() != 1 {
			t.Fatalf("the strip remembers the chip it was on, got %d", m.pickedChip())
		}
		m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
		if m.state != stateInput || m.input.Value() != draft || len(m.attachments) != 3 {
			t.Fatalf("esc goes back to the draft as it was: state %v, draft %q, %d staged", m.state, m.input.Value(), len(m.attachments))
		}
	})

	t.Run("an empty transcript lands on the strip", func(t *testing.T) {
		m := stageImage(t, frameModel(t, 110, 40), "shot.png")
		m.transcript = nil
		m.invalidateRenderCache()
		m.input.SetValue("this is the error I")
		m = pressOn(t, m, readingChord())
		if m.state != stateFocus || !m.atStrip() || m.pickedChip() != 0 {
			t.Fatalf("ctrl+o with nothing above lands on the strip: state %v, strip %v", m.state, m.atStrip())
		}
		if got := stripANSI(m.View().Content); !strings.Contains(got, "❯ Image#1 shot.png") {
			t.Fatalf("the cursor is drawn on the chip:\n%s", got)
		}
	})

	t.Run("dropping the last chip hands the cursor back", func(t *testing.T) {
		m := focusModel(t)
		m = stageImage(t, m, "one.png")
		m = stageImage(t, m, "two.png")
		m.input.SetValue("half a sentence")
		m = pressOn(t, m, readingChord())
		m = pressOn(t, m, key('j'))
		m = pressOn(t, m, arrow(false))

		m = pressOn(t, m, key('x'))
		if len(m.attachments) != 1 || m.attachments[0].Name != "one.png" {
			t.Fatalf("x drops the chip under the cursor, left %v", m.attachments)
		}
		if !m.atStrip() || m.pickedChip() != 0 {
			t.Fatalf("the cursor moves to the chip that is left, strip %v chip %d", m.atStrip(), m.pickedChip())
		}
		if got := stripANSI(m.renderHistory()); !strings.Contains(got, "dropped Image#2 (two.png") {
			t.Fatalf("the drop says what went:\n%s", got)
		}
		m = pressOn(t, m, key('x'))
		if len(m.attachments) != 0 || m.atStrip() {
			t.Fatalf("the last drop empties the strip, %d left, strip %v", len(m.attachments), m.atStrip())
		}
		if m.state != stateFocus || m.focusIdx != m.lastRow() {
			t.Fatalf("the cursor goes back to the transcript's last row, state %v, row %d", m.state, m.focusIdx)
		}
		if m.input.Value() != "half a sentence" {
			t.Fatalf("the draft is untouched, got %q", m.input.Value())
		}
	})
}

// Enter on a chip opens the card, and its way out goes back to the strip
// rather than to the draft, since that is where the reader came from. The
// card's own [x] drops what it is showing and comes back to the strip too.
func TestStrip_TheCardGoesBackToTheStrip(t *testing.T) {
	m := focusModel(t)
	m = stageImage(t, m, "one.png")
	m = stageImage(t, m, "two.png")
	m = pressOn(t, m, readingChord())
	m = pressOn(t, m, key('j'))
	m = pressOn(t, m, arrow(false))
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.state != statePreview || m.preview == nil || m.preview.Name != "two.png" {
		t.Fatalf("enter opens the chip under the cursor: state %v", m.state)
	}
	if hint := stripANSI(m.renderPreviewHint()); !strings.Contains(hint, "[x] remove") || !strings.Contains(hint, "back to the strip") {
		t.Fatalf("the card offers the drop and says where back is: %q", hint)
	}
	m = pressOn(t, m, key('q'))
	if m.state != stateFocus || !m.atStrip() || m.pickedChip() != 1 {
		t.Fatalf("q goes back to the strip on the same chip: state %v strip %v chip %d", m.state, m.atStrip(), m.pickedChip())
	}
	m = pressOn(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = pressOn(t, m, key('x'))
	if m.state != stateFocus || len(m.attachments) != 1 || !m.atStrip() || m.pickedChip() != 0 {
		t.Fatalf("the card's x drops and comes back to the strip: state %v, %d staged, chip %d", m.state, len(m.attachments), m.pickedChip())
	}
}

// The preview's equivalent of the decision cards' check: every key its hint
// row offers does something when pressed.
func TestPreview_EveryOfferedKeyDoesSomething(t *testing.T) {
	open := func(t *testing.T) Model {
		m := stageImage(t, frameModel(t, 110, 40), "shot.png")
		next, _ := m.runPaste([]string{"/paste", "show", "Image#1"})
		return next.(Model)
	}
	for _, b := range keys.Preview.All() {
		for _, k := range b.Keys() {
			m := open(t)
			before := snapshot(m) + m.View().Content
			next := pressSpelling(t, m, k)
			if after := snapshot(next) + next.View().Content; after == before {
				t.Fatalf("%q is offered on the card and changed nothing", k)
			}
		}
	}
	m := pressSpelling(t, open(t), "x")
	if len(m.attachments) != 0 || m.state != stateInput {
		t.Fatalf("x on the card drops what it shows and goes back to the draft: %d staged, state %v", len(m.attachments), m.state)
	}
	if got := stripANSI(m.renderHistory()); !strings.Contains(got, "dropped Image#1 (shot.png") {
		t.Fatalf("the drop says what went:\n%s", got)
	}
}

// A click on a chip is enter on it: the card opens, the draft keeps the
// keyboard, and the same cell pressed again closes the card.
func TestStrip_AClickOnAChipOpensIt(t *testing.T) {
	m := stageImage(t, frameModel(t, 110, 40), "one.png")
	m = stageImage(t, m, "two.png")
	m.input.SetValue("half typed")
	lines := strings.Split(m.screen(), "\n")
	y, x := -1, -1
	for i, l := range lines {
		plain := ansi.Strip(l)
		if at := strings.Index(plain, "Image#2"); at >= 0 && strings.Contains(plain, "Image#1") {
			y, x = i, len([]rune(plain[:at]))
			break
		}
	}
	if y < 0 {
		t.Fatalf("no strip on the screen:\n%s", strings.Join(lines, "\n"))
	}
	next, _ := m.clickAt(x, y)
	m = next.(Model)
	if m.state != statePreview || m.preview == nil || m.preview.Name != "two.png" {
		t.Fatalf("the click opens the chip it landed on: state %v", m.state)
	}
	next, _ = m.clickAt(x, y)
	m = next.(Model)
	if m.state != stateInput || m.preview != nil || m.input.Value() != "half typed" {
		t.Fatalf("the same cell closes the card and the draft is kept: state %v, draft %q", m.state, m.input.Value())
	}
}
