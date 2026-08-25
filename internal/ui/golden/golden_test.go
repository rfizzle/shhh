package golden

import (
	"strings"
	"testing"
)

const esc = "\x1b"

func TestFormat_LayoutBlockIsPlainAndAnsiBlockKeepsTheEscapes(t *testing.T) {
	out := Format("row.w80", Case{
		Surface: "activity row",
		Width:   80,
		Panels: []Panel{
			{Label: "done", View: esc + "[38;5;214m⚙" + esc + "[0m read    loop.go"},
			{Label: "failed", View: esc + "[38;5;9m✗" + esc + "[0m run     go test"},
		},
	})

	header, blocks, ok := strings.Cut(out, sectionRule("layout"))
	if !ok {
		t.Fatalf("a golden should carry a layout block:\n%s", out)
	}
	layout, ansiBlock, ok := strings.Cut(blocks, sectionRule("ansi"))
	if !ok {
		t.Fatalf("a golden should carry an ansi block:\n%s", out)
	}
	if !strings.HasPrefix(header, "# golden:") {
		t.Fatalf("the header should come first:\n%s", out)
	}
	if strings.Contains(layout, esc) || strings.Contains(layout, escSymbol) {
		t.Fatalf("the layout block should be plain text:\n%s", layout)
	}
	if !strings.Contains(layout, "⚙ read    loop.go") {
		t.Fatalf("the layout block should hold the render's columns:\n%s", layout)
	}
	if strings.Contains(ansiBlock, esc) {
		t.Fatalf("a raw ESC in the file would not be readable:\n%s", ansiBlock)
	}
	if !strings.Contains(ansiBlock, escSymbol+"[38;5;214m⚙") {
		t.Fatalf("the ansi block should keep the colour assignment:\n%s", ansiBlock)
	}
	// The header says what was captured, so a reviewer reading the diff knows
	// what the right edge should be without opening the test.
	for _, want := range []string{"# golden:  row.w80", "# surface: activity row", "# width:   80 columns", "# palette: color"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the header should state %q:\n%s", want, out)
		}
	}
	// Both blocks label their panels, so a state can be found in either.
	if strings.Count(out, "· failed") != 2 {
		t.Fatalf("each panel should be labelled in both blocks:\n%s", out)
	}
}

func TestFormat_MonoSaysSoInTheHeader(t *testing.T) {
	out := Format("row.w80", Case{Surface: "activity row", Width: 80, Mono: true,
		Panels: []Panel{{View: "⚙ read"}}})
	if !strings.Contains(out, "# palette: mono") {
		t.Fatalf("a mono capture should name its palette:\n%s", out)
	}
}

// A single unlabelled panel renders bare — a one-state surface carries no
// chrome it did not ask for.
func TestRenderPanels_UnlabelledPanelIsBare(t *testing.T) {
	if got := renderPanels([]Panel{{View: "a\nb"}}); got != "a\nb" {
		t.Fatalf("renderPanels = %q", got)
	}
	got := renderPanels([]Panel{{Label: "one", View: "a"}, {Label: "two", View: "b"}})
	if got != "· one\na\n\n· two\nb" {
		t.Fatalf("panels should be separated and labelled, got %q", got)
	}
}

func TestPath_MonoSitsBesideItsColourPair(t *testing.T) {
	if got := Path("diff-view.w80", false); got != "testdata/golden/diff-view.w80.txt" {
		t.Fatalf("Path = %q", got)
	}
	if got := Path("diff-view.w80", true); got != "testdata/golden/diff-view.w80.mono.txt" {
		t.Fatalf("Path = %q", got)
	}
}

// The failure message is the whole value of a golden: it has to point at the
// line that moved, with the difference visible rather than implied.
func TestFirstDifference(t *testing.T) {
	got := firstDifference("a\n  x  \nc\n", "a\n  x\nc\n")
	if !strings.Contains(got, "line 2") {
		t.Fatalf("should name the line, got %q", got)
	}
	// %q keeps a trailing space visible, which is usually the whole story.
	if !strings.Contains(got, `"  x  "`) || !strings.Contains(got, `"  x"`) {
		t.Fatalf("should quote both sides, got %q", got)
	}
	if got := firstDifference("a\nb\n", "a\nb\nc\n"); !strings.Contains(got, "line 3") {
		t.Fatalf("a render that grew should point at the new line, got %q", got)
	}
	if got := firstDifference("a\n", "a\n"); !strings.Contains(got, "trailing newline") {
		t.Fatalf("identical files should say so, got %q", got)
	}
}
