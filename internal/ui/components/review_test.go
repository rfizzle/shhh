package components

// Review mode (docs/interface/surfaces.md#the-turns-close). The
// surface is a layout around the shared diff renderer, so these cover what
// is its own: that it is a reading with one way out, and how the two panes
// behave as the terminal changes width.

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/diff"
)

// reviewFixture is a two-file review: the first file has two hunks so the
// hunk cursor has somewhere to go.
func reviewFixture() *ReviewView {
	// The changed lines share a prefix, so the pair carries an intraline
	// emphasis span and the pane has something to tint.
	loop := diff.Compute(
		"one\nreturn results, nil\nthree\nfour\nfive\nsix\nseven\neight\nnine\nreturn err\neleven\n",
		"one\nreturn results, ErrRoundLimit\nthree\nfour\nfive\nsix\nseven\neight\nnine\nreturn wrapped\neleven\n")
	errs := diff.Compute("alpha\n", "alpha\nbeta\n")
	return &ReviewView{
		Title: "turn 7",
		Files: []ReviewFile{
			{Path: "internal/agent/loop.go", Hunks: loop},
			{Path: "internal/agent/errors.go", Hunks: errs, Agent: "writer-1"},
		},
		Verdict: &ReviewVerdict{
			Failed: true, Label: "go test ./internal/agent/...",
			Detail: []string{"--- FAIL: TestRoundLimit (0.03s)"},
		},
		Shield:       "nothing is committed",
		ShieldDetail: "/undo 7 restores the 2 files this turn wrote",
		Height:       20,
	}
}

// Esc is the one way out, and it changes nothing: there is no selection to
// report, and the keys that once staged one do nothing here.
func TestReview_IsAReadingWithOneWayOut(t *testing.T) {
	v := reviewFixture()
	for _, k := range []string{"s", "S", "A", "enter"} {
		if v.Update(key(k)) {
			t.Fatalf("%q should not end a review", k)
		}
	}
	out := ansi.Strip(v.View(130))
	for _, gone := range []string{"[x]", "[ ]", "staged", "[enter]"} {
		if strings.Contains(out, gone) {
			t.Fatalf("a review offers no staging, yet draws %q:\n%s", gone, out)
		}
	}
	if !v.Update(key("esc")) {
		t.Fatal("esc should leave the review")
	}
}

// The hunk pane is a layout around the shared renderer: its body
// rows are the ones every other diff surface shows, so there is one diff
// renderer and not a second one.
func TestReview_PaneBodyComesFromTheSharedRenderer(t *testing.T) {
	v := reviewFixture()
	const width = 70
	v.wide = false
	rows, _ := v.hunkRows(v.Files[0], width)

	shared := UnifiedLines(v.Files[0].Hunks[:1], width, UnifiedOpts{LineNumbers: true, Emphasis: true})
	for i, want := range shared[1:] {
		if rows[i+1] != want {
			t.Fatalf("pane row %d differs from the shared renderer:\n got %q\nwant %q", i+1, rows[i+1], want)
		}
	}
	// The header row is the surface's own.
	if !strings.Contains(ansi.Strip(rows[0]), "@@") {
		t.Fatalf("the hunk header should carry the hunk, got %q", ansi.Strip(rows[0]))
	}
}

// Intraline changes keep the background tint the shared renderer applies, so
// bgParams is a token's background as SGR parameters, without the escape
// around them: an emphasised run carries its foreground in the same escape,
// so the background is a substring of a longer sequence rather than one of
// its own.
func bgParams(t Token) string {
	return strings.Join(ansi.NewStyle().BackgroundColor(t.Color()), ";")
}

// syntax highlighting survives underneath it.
func TestReview_IntralineEmphasisSurvives(t *testing.T) {
	withColorProfile(t, colorprofile.ANSI256)
	v := reviewFixture()
	rows, _ := v.hunkRows(v.Files[0], 70)
	joined := strings.Join(rows, "\n")
	if !strings.Contains(joined, bgParams(Palette.AddBg)) || !strings.Contains(joined, bgParams(Palette.DelBg)) {
		t.Fatalf("the pane should carry the intraline emphasis backgrounds:\n%q", joined)
	}
}

func TestReview_SideBySideIsAutomaticWhenWideAndTogglesBack(t *testing.T) {
	v := reviewFixture()
	wide := ansi.Strip(v.View(sideBySideMinWidth + 10))
	if !strings.Contains(wide, "│") {
		t.Fatalf("a wide review should pair the hunks side by side:\n%s", wide)
	}
	if !v.wide {
		t.Fatal("the surface should have taken the wide layout from its own width")
	}
	// The unified marker column is what side-by-side does not have.
	unified := ansi.Strip(v.View(90))
	if v.wide {
		t.Fatal("below the threshold the layout is unified")
	}
	if !strings.Contains(unified, "- ") || !strings.Contains(unified, "+ ") {
		t.Fatalf("the unified layout keeps its markers:\n%s", unified)
	}
	// [\] forces the pairing at any width.
	v.Update(key("\\"))
	if !v.SideBySide {
		t.Fatal("\\ should toggle side-by-side on")
	}
	v.Update(key("\\"))
	if v.SideBySide {
		t.Fatal("\\ should toggle it back off")
	}
}

func TestReview_StacksBelowTheNarrowWidth(t *testing.T) {
	v := reviewFixture()
	narrow := ansi.Strip(v.View(reviewStackWidth - 4))
	for _, want := range []string{"REVIEW turn 7", "internal/agent/loop.go", "@@", "nothing is committed"} {
		if !strings.Contains(narrow, want) {
			t.Fatalf("the stacked layout should keep %q:\n%s", want, narrow)
		}
	}
	for _, line := range strings.Split(narrow, "\n") {
		if len([]rune(line)) > reviewStackWidth-4 {
			t.Fatalf("a stacked row must not overflow its width: %q", line)
		}
		// Stacked means no vertical divider: the panes are above and below.
		if strings.Contains(line, " │ ") {
			t.Fatalf("the stacked layout should not draw the pane divider: %q", line)
		}
	}
}

func TestReview_ViewFillsExactlyItsHeight(t *testing.T) {
	v := reviewFixture()
	for _, width := range []int{50, 80, 130} {
		for _, height := range []int{8, 14, 20, 30} {
			v.Height = height
			got := len(strings.Split(v.View(width), "\n"))
			if got != height {
				t.Fatalf("width %d height %d rendered %d rows", width, height, got)
			}
		}
	}
}

// The list carries the turn's verdict and who wrote what, beside the files
// themselves.
func TestReview_ListCarriesTheVerdictAndAttribution(t *testing.T) {
	v := reviewFixture()
	out := ansi.Strip(v.View(130))
	for _, want := range []string{
		"go test ./internal/agent/... failing",
		"--- FAIL: TestRoundLimit",
		"writer-1",
		"⛨ nothing is committed",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("the file list should carry %q:\n%s", want, out)
		}
	}
}

// n walks the whole review rather than stopping at a file boundary.
func TestReview_HunkCursorSpillsBetweenFiles(t *testing.T) {
	v := reviewFixture()
	v.Update(key("n")) // second hunk of the first file
	v.Update(key("n")) // spills into the second file
	if v.File != 1 || v.Hunk != 0 {
		t.Fatalf("n should spill into the next file, got file %d hunk %d", v.File, v.Hunk)
	}
	v.Update(key("p"))
	if v.File != 0 || v.Hunk != len(v.Files[0].Hunks)-1 {
		t.Fatalf("p should spill back to the previous file's last hunk, got file %d hunk %d", v.File, v.Hunk)
	}
}

// Both of the surface's lists draw the one pointer every other list draws:
// the ❯ in its own column outside the highlight and the whole row lit inside
// it. What this replaced was a filename in a second colour on an unlit
// ground — the one list in the product that answered "where is the keyboard"
// with a word rather than a row.
func TestReview_BothCursorsAreLitRows(t *testing.T) {
	withColorProfile(t, colorprofile.ANSI256)

	const width = 44
	v := reviewFixture()

	f := v.Files[0]
	for _, list := range []struct {
		name string
		row  string
	}{
		{"the file list", v.fileRows(width)[0]},
		{"the hunk pane", v.hunkHeader(f.Hunks[0], 0, width)},
	} {
		if got := lipgloss.Width(ansi.Strip(list.row)); got != width {
			t.Fatalf("%s's lit row is %d columns, want the pane's %d", list.name, got, width)
		}
		// `48;5;` is how a 256-colour terminal is told to set a background,
		// which is the whole of what "lit" is.
		const background = "48;5;"
		before, after, found := strings.Cut(list.row, "❯")
		if !found {
			t.Fatalf("%s draws no pointer: %q", list.name, list.row)
		}
		if strings.Contains(before, background) {
			t.Fatalf("%s puts the pointer inside the highlight: %q", list.name, list.row)
		}
		if !strings.Contains(after, background) {
			t.Fatalf("%s lights nothing behind the row: %q", list.name, list.row)
		}
	}
}
