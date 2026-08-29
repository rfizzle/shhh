package components

// Review mode (S-099, docs/interface/surfaces.md#the-turns-close). The
// surface is a layout around the shared diff renderer, so these cover the two
// things that are its own: what staging selects, and how the two panes behave
// as the terminal changes width.

import (
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/diff"
)

// reviewFixture is a two-file review: the first file has two hunks so hunk
// staging has something to be partial about.
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
			{Path: "internal/agent/loop.go", Hunks: loop, Staged: make([]bool, len(loop))},
			{Path: "internal/agent/errors.go", Hunks: errs, Staged: make([]bool, len(errs)), Agent: "writer-1"},
		},
		Verdict: &ReviewVerdict{
			Failed: true, Label: "go test ./internal/agent/...",
			Detail: []string{"--- FAIL: TestRoundLimit (0.03s)"},
		},
		Shield:       "nothing is committed",
		ShieldDetail: "undo restores the 2 files this turn wrote",
		Height:       20,
	}
}

func TestReview_StagesPerHunkFileAndAll(t *testing.T) {
	v := reviewFixture()
	if len(v.Files[0].Hunks) != 2 {
		t.Fatalf("the fixture needs a two-hunk file, got %d", len(v.Files[0].Hunks))
	}

	// space stages the hunk under the cursor and nothing else.
	v.Update(key("space"))
	if v.Files[0].stagedCount() != 1 {
		t.Fatalf("space should stage one hunk, got %d", v.Files[0].stagedCount())
	}
	// n moves to the next hunk of the same file; space stages that one too.
	v.Update(key("n"))
	v.Update(key("space"))
	if v.Files[0].stagedCount() != 2 {
		t.Fatalf("the second hunk should stage too, got %d", v.Files[0].stagedCount())
	}
	// s on a wholly staged file clears it.
	v.Update(key("s"))
	if v.Files[0].stagedCount() != 0 {
		t.Fatalf("s should clear a wholly staged file, got %d", v.Files[0].stagedCount())
	}
	// A stages everything, then nothing.
	v.Update(key("A"))
	if v.Files[0].stagedCount() != 2 || v.Files[1].stagedCount() != 1 {
		t.Fatalf("A should stage every hunk of every file, got %d and %d",
			v.Files[0].stagedCount(), v.Files[1].stagedCount())
	}
	v.Update(key("a"))
	if v.Files[0].stagedCount() != 0 || v.Files[1].stagedCount() != 0 {
		t.Fatal("a second all/none should clear everything")
	}
}

func TestReview_EnterReportsTheStagedSelection(t *testing.T) {
	v := reviewFixture()

	// Nothing staged: enter says so and stays, rather than applying nothing.
	done, _ := v.Update(key("enter"))
	if done {
		t.Fatal("enter with nothing staged should not finish the surface")
	}
	if !strings.Contains(ansi.Strip(v.View(90)), "nothing staged") {
		t.Fatalf("the surface should say why enter did nothing:\n%s", ansi.Strip(v.View(90)))
	}

	// One hunk of the first file, and the whole second file.
	v.Update(key("space"))
	v.Update(key("j"))
	v.Update(key("s"))
	done, result := v.Update(key("enter"))
	if !done {
		t.Fatal("enter with a staged selection should finish the surface")
	}
	r, ok := result.(ReviewResult)
	if !ok || r.Canceled {
		t.Fatalf("enter should report a selection, got %#v", result)
	}
	if r.Files() != 2 {
		t.Fatalf("both files have a staged hunk, got %d", r.Files())
	}
	if len(r.Staged[0].Hunks) != 1 || r.Staged[0].Hunks[0] != 0 {
		t.Fatalf("the first file staged only its first hunk, got %#v", r.Staged[0])
	}
	if r.Staged[1].File != 1 || len(r.Staged[1].Hunks) != 1 {
		t.Fatalf("the second file should be staged whole, got %#v", r.Staged[1])
	}
}

func TestReview_EscLeavesWithNothingChosen(t *testing.T) {
	v := reviewFixture()
	v.Update(key("A"))
	done, result := v.Update(key("esc"))
	r, ok := result.(ReviewResult)
	if !done || !ok || !r.Canceled {
		t.Fatalf("esc should finish the surface with a cancel, got done=%v %#v", done, result)
	}
	if len(r.Staged) != 0 {
		t.Fatalf("a cancel carries no selection, got %#v", r.Staged)
	}
	// The staging state itself survives — esc dismisses, it does not destroy.
	if v.Files[0].stagedCount() != 2 {
		t.Fatal("esc should leave the staging state alone")
	}
}

// The hunk pane is a layout around the shared renderer (§3, S-074): its body
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
	// The header row is the surface's own: it carries the staging box.
	if !strings.Contains(ansi.Strip(rows[0]), "@@") || !strings.Contains(ansi.Strip(rows[0]), "[ ]") {
		t.Fatalf("the hunk header should carry the hunk and its staging box, got %q", ansi.Strip(rows[0]))
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

// A read-only review — a cumulative diff, where there is nothing to select —
// drops staging entirely rather than offering boxes that mean nothing.
func TestReview_ReadOnlyOffersNoStaging(t *testing.T) {
	v := reviewFixture()
	v.ReadOnly = true
	out := ansi.Strip(v.View(130))
	if strings.Contains(out, "[x]") || strings.Contains(out, "[ ]") {
		t.Fatalf("a read-only review has no staging boxes:\n%s", out)
	}
	if strings.Contains(out, "[enter]") {
		t.Fatalf("a read-only review offers nothing to apply:\n%s", out)
	}
	done, result := v.Update(key("enter"))
	if r, ok := result.(ReviewResult); !done || !ok || !r.Canceled {
		t.Fatalf("enter in a read-only review should just leave, got done=%v %#v", done, result)
	}
}

// The footer's apply offer counts the staged files as they are staged, so
// the confirm is live rather than a fixed label.
func TestReview_ApplyOfferCountsWhatIsStaged(t *testing.T) {
	v := reviewFixture()
	v.ApplyVerb = "undo"
	if got := ansi.Strip(strings.Join(v.footerRows(200), " ")); !strings.Contains(got, "undo 0 files") {
		t.Fatalf("an unstaged review offers nothing to undo, got %q", got)
	}
	v.Update(key("A"))
	if got := ansi.Strip(strings.Join(v.footerRows(200), " ")); !strings.Contains(got, "undo 2 files") {
		t.Fatalf("the offer should count the staged files, got %q", got)
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
