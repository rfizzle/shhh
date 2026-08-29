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
