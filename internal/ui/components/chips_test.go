package components

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// staged is the three-kind fixture the strip is read against: one of each
// mark, so a chip that lost its glyph is a failing test rather than a
// screenshot nobody took.
func staged() []AttachmentChip {
	return []AttachmentChip{
		{Kind: ChipImage, Name: "shot.png", Size: "412 KB"},
		{Kind: ChipText, Name: "notes.md", Size: "2 KB"},
		{Kind: ChipDocument, Name: "spec.pdf", Size: "1.1 MB"},
	}
}

func TestAttachmentChips_NothingStagedDrawsNothing(t *testing.T) {
	if got := AttachmentChips(nil, 80); got != "" {
		t.Fatalf("empty staging area = %q", got)
	}
	if got := AttachmentChips(staged(), 0); got != "" {
		t.Fatalf("no room = %q", got)
	}
}

func TestAttachmentChips_EachKindKeepsItsMark(t *testing.T) {
	got := ansi.Strip(AttachmentChips(staged(), 80))
	want := "▣ shot.png 412 KB · ≡ notes.md 2 KB · ▤ spec.pdf 1.1 MB"
	if got != want {
		t.Fatalf("strip = %q, want %q", got, want)
	}
}

// A row that has run out of room gives up whole chips and counts them, rather
// than clipping a name into something `/paste drop` cannot be told.
func TestAttachmentChips_DropsWholeChipsAndCountsThem(t *testing.T) {
	for _, c := range []struct {
		width int
		want  string
	}{
		{80, "▣ shot.png 412 KB · ≡ notes.md 2 KB · ▤ spec.pdf 1.1 MB"},
		{55, "▣ shot.png 412 KB · ≡ notes.md 2 KB · ▤ spec.pdf 1.1 MB"},
		{54, "▣ shot.png 412 KB · ≡ notes.md 2 KB · +1 more"},
		{45, "▣ shot.png 412 KB · ≡ notes.md 2 KB · +1 more"},
		{44, "▣ shot.png 412 KB · +2 more"},
		{27, "▣ shot.png 412 KB · +2 more"},
	} {
		got := ansi.Strip(AttachmentChips(staged(), c.width))
		if got != c.want {
			t.Fatalf("width %d: strip = %q, want %q", c.width, got, c.want)
		}
		if w := lipgloss.Width(AttachmentChips(staged(), c.width)); w > c.width {
			t.Fatalf("width %d: strip is %d columns wide", c.width, w)
		}
	}
}

// The last rung keeps one chip whatever happens: a strip that is only a
// number has lost the thing it is for, so the name clips like any other
// field and the row still says which file it is about.
func TestAttachmentChips_KeepsOneChipAtAnyWidth(t *testing.T) {
	for width := 1; width < 27; width++ {
		got := AttachmentChips(staged(), width)
		if w := lipgloss.Width(got); w > width {
			t.Fatalf("width %d: strip is %d columns wide (%q)", width, w, ansi.Strip(got))
		}
		if plain := ansi.Strip(got); width > 4 && !strings.HasPrefix(plain, "▣") {
			t.Fatalf("width %d: strip = %q, want the first chip's mark", width, plain)
		}
	}
}

// A name long enough to push another chip off the row is cut at its head,
// which is the half that tells two screenshots apart.
func TestAttachmentChips_ClipsALongName(t *testing.T) {
	long := []AttachmentChip{{Kind: ChipImage, Name: "screenshot-2026-08-29-at-14-02-11.png", Size: "412 KB"}}
	got := ansi.Strip(AttachmentChips(long, 80))
	if want := "▣ screenshot-2026-08-… 412 KB"; got != want {
		t.Fatalf("strip = %q, want %q", got, want)
	}
}

// With handles, what a narrowing row gives up is ordered and the handle is
// the last of it: every chip's name first, then whole chips from the end,
// then the kept chip's counts, and only then a clip — so three screenshots
// all called clipboard.png stay three words `/paste drop` takes for as long
// as there is room for them.
func TestAttachmentChips_TheHandleIsGivenUpLast(t *testing.T) {
	chips := []AttachmentChip{
		{Kind: ChipImage, Handle: "Image#1", Name: "clipboard.png", Size: "412 KB"},
		{Kind: ChipImage, Handle: "Image#2", Name: "clipboard.png", Size: "380 KB"},
		{Kind: ChipImage, Handle: "Image#3", Name: "clipboard.png", Size: "96 KB"},
		{Kind: ChipText, Handle: "File#1", Name: "notes.md", Size: "2 KB", Lines: 84},
	}
	full := "▣ Image#1 clipboard.png 412 KB · ▣ Image#2 clipboard.png 380 KB · " +
		"▣ Image#3 clipboard.png 96 KB · ≡ File#1 notes.md 2 KB 84 lines"
	for _, c := range []struct {
		width int
		want  string
	}{
		{129, full},
		{128, "▣ Image#1 412 KB · ▣ Image#2 380 KB · ▣ Image#3 96 KB · ≡ File#1 2 KB 84 lines"},
		{78, "▣ Image#1 412 KB · ▣ Image#2 380 KB · ▣ Image#3 96 KB · ≡ File#1 2 KB 84 lines"},
		{77, "▣ Image#1 412 KB · ▣ Image#2 380 KB · ▣ Image#3 96 KB · +1 more"},
		{62, "▣ Image#1 412 KB · ▣ Image#2 380 KB · +2 more"},
		{60, "▣ Image#1 412 KB · ▣ Image#2 380 KB · +2 more"},
		{44, "▣ Image#1 412 KB · +3 more"},
		{25, "▣ Image#1 · +3 more"},
		{19, "▣ Image#1 · +3 more"},
		{9, "▣ Image#1"},
	} {
		got := AttachmentChips(chips, c.width)
		if plain := ansi.Strip(got); plain != c.want {
			t.Fatalf("width %d: strip = %q, want %q", c.width, plain, c.want)
		}
		if w := lipgloss.Width(got); w > c.width {
			t.Fatalf("width %d: strip is %d columns wide", c.width, w)
		}
	}
}

// A chip with no size is still a chip: the notice paths that stage one before
// its bytes are counted must not render a trailing gap.
func TestAttachmentChips_SizeIsOptional(t *testing.T) {
	got := ansi.Strip(AttachmentChips([]AttachmentChip{{Kind: ChipText, Name: "notes.md"}}, 40))
	if want := "≡ notes.md"; got != want {
		t.Fatalf("strip = %q, want %q", got, want)
	}
}

// A text chip carries how far it runs, and nothing else does. For a paste
// there is no name anybody chose, so the height is the field that tells two
// of them apart — and a picture has no lines to count, which is left out
// rather than reported as zero.
func TestAttachmentChips_TextCountsItsLines(t *testing.T) {
	got := ansi.Strip(AttachmentChips([]AttachmentChip{
		{Kind: ChipText, Name: "paste-1.txt", Size: "4 KB", Lines: 178},
		{Kind: ChipText, Name: "one.txt", Size: "12 B", Lines: 1},
		{Kind: ChipImage, Name: "shot.png", Size: "412 KB"},
	}, 120))
	want := "≡ paste-1.txt 4 KB 178 lines · ≡ one.txt 12 B 1 line · ▣ shot.png 412 KB"
	if got != want {
		t.Fatalf("strip = %q, want %q", got, want)
	}
}

// The picked chip is drawn under the reading cursor in its own columns — the
// pointer where its mark was, nothing moved sideways — and every drawn chip
// reports the cells it took, which is what a click is resolved against. A
// picked chip the row would have given up is kept by giving up the ones in
// front of it instead, and the count of what was given up is no target.
func TestAttachmentChipsAt_ThePickedChipIsDrawnAndEveryChipIsMapped(t *testing.T) {
	chips := []AttachmentChip{
		{Kind: ChipImage, Handle: "Image#1", Name: "one.png", Size: "4 KB"},
		{Kind: ChipImage, Handle: "Image#2", Name: "two.png", Size: "4 KB"},
		{Kind: ChipText, Handle: "File#1", Name: "notes.md", Size: "2 KB"},
	}
	plain, _ := AttachmentChipsAt(chips, 200, -1)
	row, hits := AttachmentChipsAt(chips, 200, 1)
	if lipgloss.Width(row) != lipgloss.Width(plain) {
		t.Fatalf("picking a chip moved the row: %q against %q", ansi.Strip(row), ansi.Strip(plain))
	}
	cells := ansi.Strip(row)
	if !strings.Contains(cells, "❯ Image#2 two.png") || strings.Contains(cells, "▣ Image#2") {
		t.Fatalf("the cursor takes the picked chip's mark: %q", cells)
	}
	if len(hits) != 3 {
		t.Fatalf("every drawn chip is mapped, got %v", hits)
	}
	for _, h := range hits {
		if got := ansi.Cut(cells, h.From, h.To); !strings.Contains(got, chips[h.Index].Handle) {
			t.Fatalf("chip %d's cells %d–%d hold %q", h.Index, h.From, h.To, got)
		}
	}

	narrow, hits := AttachmentChipsAt(chips, 30, 2)
	got := ansi.Strip(narrow)
	if !strings.Contains(got, "❯ File#1") || !strings.HasPrefix(got, "+") {
		t.Fatalf("a picked chip past the edge is kept, the ones before it counted: %q", got)
	}
	for _, h := range hits {
		if cut := ansi.Cut(got, h.From, h.To); !strings.Contains(cut, chips[h.Index].Handle) || strings.Contains(cut, "more") {
			t.Fatalf("only chips are targets, never the count: %v on %q", hits, got)
		}
	}
}
