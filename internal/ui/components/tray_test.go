package components

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// A tray row gives its facts up from the end as the pane narrows — the
// source phrase first, then the dimensions — then clips the name, and never
// gives up the handle or the size.
func TestTrayRow_GivesUpTheFactsBeforeTheHandle(t *testing.T) {
	picture := TrayRow{Kind: ChipImage, Handle: "Image#1", Name: "clipboard.png",
		Facts: []string{"1440×900", "from the clipboard"}, Size: "412 KB"}
	long := picture
	long.Name = "screenshot-reading-mode.png"
	for _, tc := range []struct {
		name  string
		row   TrayRow
		width int
		want  string
	}{
		{"wide", picture, 110, "  ⟨▣ Image#1⟩  clipboard.png · 1440×900 · from the clipboard"},
		{"the source goes first", picture, 60, "  ⟨▣ Image#1⟩  clipboard.png · 1440×900             412 KB  "},
		{"then the dimensions", long, 60, "  ⟨▣ Image#1⟩  screenshot-reading-mode.png          412 KB  "},
		{"then the name is clipped", long, 40, "  ⟨▣ Image#1⟩  screenshot-read… 412 KB  "},
		{"a paste is marked ¶", TrayRow{Kind: ChipText, Handle: "Paste#1", Name: "paste-1.txt",
			Facts: []string{"214 lines", "6.0k tokens"}, Size: "23 KB"}, 80, "  ⟨¶ Paste#1⟩  paste-1.txt · 214 lines · 6.0k tokens"},
	} {
		got := ansi.Strip(tc.row.View(tc.width))
		if !strings.HasPrefix(got, tc.want) {
			t.Errorf("%s: the row is %q, want it to start %q", tc.name, got, tc.want)
		}
		if w := lipgloss.Width(got); w != tc.width {
			t.Errorf("%s: the row is %d cells wide, want the pane's %d", tc.name, w, tc.width)
		}
	}
}

// A card's subtitle follows its title on the border in the chrome grey, and
// a card without one draws its title exactly as before.
func TestCard_TheSubtitleIsChromeAfterTheTitle(t *testing.T) {
	plain := Card{Title: "clipboard.png"}.Render(nil, 60)
	if top := ansi.Strip(strings.Split(plain, "\n")[0]); !strings.HasPrefix(top, "╭─ clipboard.png ─") {
		t.Fatalf("the bare title is %q", top)
	}
	sent := Card{Title: "clipboard.png", subtitle: "sent with turn 3"}.Render(nil, 60)
	top := strings.Split(sent, "\n")[0]
	if !strings.HasPrefix(ansi.Strip(top), "╭─ clipboard.png · sent with turn 3 ─") {
		t.Fatalf("the title with its subtitle is %q", ansi.Strip(top))
	}
	if !strings.Contains(top, sty.dim.Render(" · sent with turn 3 ")) {
		t.Fatalf("the subtitle is not in the chrome grey: %q", top)
	}
}
