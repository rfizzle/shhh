package components

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// A click on the strip names the call whose glyph is drawn in the cell it
// landed on, at every width: with the words leading the glyphs, without
// them, and where the strip keeps only its latest calls behind a ….
func TestCardStrip_CellAtIsTheGlyphDrawnThere(t *testing.T) {
	cells := largeStep().Strip
	for _, width := range []int{110, 40, 24} {
		strip := CardStrip{Cells: cells, Cursor: -1}
		row := []rune(ansi.Strip(strip.View(width)))
		hits := 0
		for x := range row {
			i, ok := strip.CellAt(width, x)
			if !ok {
				continue
			}
			hits++
			if got, want := string(row[x]), ansi.Strip(stripCell(cells[i])); got != want {
				t.Errorf("width %d, column %d: CellAt says call %d (%q), the row draws %q", width, x, i, want, got)
			}
		}
		if hits == 0 {
			t.Errorf("width %d: no column names a call", width)
		}
		if width >= 110 && !strings.Contains(string(row), "in order ") {
			t.Errorf("width %d: the strip should lead with its words: %q", width, string(row))
		}
	}
}

// A directory's row names as many of its files as the pane holds and
// counts the rest, and the reads past the ceiling are counted alone.
func TestCardDirRow_NamesWhatFitsAndCountsTheRest(t *testing.T) {
	files := []string{"keys.go", "render.go", "reply.go", "click.go", "fold.go", "scene.go"}
	cases := []struct {
		name  string
		row   CardDirRow
		width int
		want  string
	}{
		{"every name fits", CardDirRow{Dir: "internal/ui/", Files: files}, 110,
			"internal/ui/ keys.go · render.go · reply.go · click.go · fold.go · scene.go"},
		{"the rest counted", CardDirRow{Dir: "internal/ui/", Files: files}, 50,
			"internal/ui/ keys.go · render.go · … +4"},
		{"past the ceiling", CardDirRow{More: 4}, 60, "… +4"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := strings.TrimSpace(ansi.Strip(c.row.View(c.width))); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}
