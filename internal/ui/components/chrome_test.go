package components

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Every surface a host gives a rectangle to answers to one method. A surface
// that grew a second name for its height would otherwise be a host setting
// the wrong field, which draws past the bottom of the terminal with nothing
// failing to say so.
var _ = []Sized{
	(*DoctorScreen)(nil), (*MetricsScreen)(nil), (*ConfigScreen)(nil),
	(*HistoryScreen)(nil), (*RateScreen)(nil), (*ContextScreen)(nil),
	(*ProfileScreen)(nil), (*ReviewView)(nil), (*DiffView)(nil),
	(*OutputView)(nil), (*AttachmentView)(nil),
}

// headerOf is the first row of a screen's render with its colour stripped.
func headerOf(view string) string {
	return ansi.Strip(strings.SplitN(view, "\n", 2)[0])
}

// The drop order, asserted once for the whole family rather than once per
// screen. Every take-over screen's header gives the same ground in the same
// order as the terminal narrows: the reading it is counting goes, and the
// stated way out of the surface stays (invariant 5). Three of these screens
// used to do the opposite, and nothing in their own tests could see it — each
// was self-consistent, and only the seven side by side showed the
// disagreement.
func TestScreenHeader_TheTallyDropsBeforeTheWayOut(t *testing.T) {
	profile := func() *ProfileScreen {
		p := NewProfileScreen("/agents new")
		p.Subject = "a coding agent · reviewer tester"
		p.MaxLines = 12
		p.AskBrief("What should this agent do?", "", nil)
		return p
	}
	context := goldenContextScreen()

	for _, tc := range []struct {
		name string
		// narrow is a width with no room for both halves of this screen's
		// header, which is a different number for each of them: what the row
		// carries is the screen's own.
		narrow int
		// wayOut is what the header must still be offering there.
		wayOut string
		// tally is what the row is expected to have given up to keep it.
		tally string
		view  func(width int) string
	}{
		{"doctor", 44, "[q] quit", "6 checks", func(w int) string { return doctorScreen().View(w) }},
		{"metrics", 44, "[q] quit", "3 models", func(w int) string { return metricsScreen().View(w) }},
		{"config", 44, "[q] quit", "config.toml", func(w int) string { return configFixture().View(w) }},
		{"history", 44, "[q] quit", "4 entries", func(w int) string { return historyScreen().View(w) }},
		{"rate", 30, "[q] quit", "1 of 3", func(w int) string { return rateScreen().View(w) }},
		{"context", 44, "[q] back", "this session", func(w int) string { return context.View(w) }},
		{"profile", 44, "esc leave", "reviewer tester", func(w int) string { return profile().View(w) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			head := headerOf(tc.view(tc.narrow))
			if !strings.Contains(head, tc.wayOut) {
				t.Fatalf("a narrow header dropped the way out: %q", head)
			}
			if strings.Contains(head, tc.tally) {
				t.Fatalf("a narrow header kept the tally instead: %q", head)
			}
			if wide := headerOf(tc.view(130)); !strings.Contains(wide, tc.tally) {
				t.Fatalf("a wide header is missing the tally: %q", wide)
			}
		})
	}
}

// The title is the field a header is clipped down to, not one it drops: a
// header that has given up its own name has stopped being one.
func TestScreenHeader_TheTitleIsClippedRatherThanDropped(t *testing.T) {
	h := ScreenHeader{
		Left: []RailSegment{screenTitle("shhh doctor"), screenField("10 checks")},
		Keys: "[q] quit",
	}
	row := ansi.Strip(h.Row(24))
	if !strings.HasPrefix(row, "shhh d") {
		t.Fatalf("the title did not survive the narrowest row: %q", row)
	}
	if strings.Contains(row, "10 checks") {
		t.Fatalf("the field was clipped rather than dropped: %q", row)
	}
	if lipgloss.Width(row) > 24 {
		t.Fatalf("the header ran past the terminal: %q", row)
	}
}

// A field carries the separator that joins it to what is in front of it, so
// a header whose fields have all dropped does not end in a dangling ` · `.
func TestScreenHeader_ADroppedFieldTakesItsSeparator(t *testing.T) {
	h := ScreenHeader{
		Left: []RailSegment{screenTitle("shhh config"), screenField("~/.config/shhh/config.toml")},
		Keys: "[?] keys · [q] quit",
	}
	row := ansi.Strip(h.Row(40))
	if strings.Contains(row, "shhh config ·") {
		t.Fatalf("the dropped field left its separator behind: %q", row)
	}
}

// A screen with nothing to count states its keys and nothing else — a style
// renders a pair of escapes around an empty string, and a header that read
// the tally as "present" would draw the separator anyway.
func TestScreenHeader_AnEmptyTallyDrawsNoSeparator(t *testing.T) {
	h := ScreenHeader{
		Left:  []RailSegment{screenTitle("shhh metrics")},
		Keys:  "[q] quit",
		Tally: sty.Body.Render(""),
	}
	if row := ansi.Strip(h.Row(80)); !strings.HasSuffix(row, "[q] quit") {
		t.Fatalf("an empty tally left a separator on the row: %q", row)
	}
}

// A screen that draws no footer ends at its body: the blank row above the
// keys belongs to the footer, and a screen without one would otherwise end
// in it.
func TestScreenChrome_NoFooterMeansNoTrailingBlank(t *testing.T) {
	body := func(int) []string { return []string{"one", "two"} }
	rows := strings.Split(ScreenChrome{Header: ScreenHeader{
		Left: []RailSegment{screenTitle("shhh doctor")}, Keys: "[q] quit",
	}}.View(40, body), "\n")
	if got := rows[len(rows)-1]; got != "two" {
		t.Fatalf("the body is not the last row: %q", got)
	}
}

// The budget is the screen's height less everything pinned around the body,
// and the footer's own blank row is part of what is pinned.
func TestScreenChrome_BudgetCountsEverythingPinned(t *testing.T) {
	var got int
	ScreenChrome{
		MaxLines: 20,
		Foot:     []string{"[q] quit"},
		Notice:   "copied the report to the clipboard",
		Head:     []string{"filter"},
		Reserve:  2,
	}.View(40, func(budget int) []string {
		got = budget
		return nil
	})
	// header, rule, blank, the pinned head row, two reserved, the footer's
	// blank and its one row, and the notice.
	if want := 20 - 9; got != want {
		t.Fatalf("the body was given %d rows, want %d", got, want)
	}
}

// Nothing on a key row is truncated to make room: the offers wrap instead
// (docs/interface/principles.md#fold-never-hide).
func TestKeyFooter_OffersWrapRatherThanClip(t *testing.T) {
	offers := []KeyOffer{
		{Key: "[y]", Label: "allow it"},
		{Key: "[n]", Label: "refuse it"},
		{Key: "[a]", Label: "allow every one like it"},
		{Key: "[esc]", Label: "leave, change nothing"},
	}
	rows := KeyFooter{Offers: offers}.Rows(40)
	if len(rows) < 2 {
		t.Fatalf("the offers were not wrapped: %#v", rows)
	}
	joined := ansi.Strip(strings.Join(rows, " · "))
	for _, offer := range offers {
		if !strings.Contains(joined, offer.Label) {
			t.Fatalf("the footer dropped %q: %q", offer.Label, joined)
		}
	}
	if strings.Contains(joined, "…") {
		t.Fatalf("an offer was truncated: %q", joined)
	}
}

// A sub-surface that has taken the footer over is the whole footer: while a
// confirm is up it is what the keyboard is answering, so the keys underneath
// it are not offers.
func TestKeyFooter_ATakenFooterIsTheWholeFooter(t *testing.T) {
	rows := KeyFooter{
		Offers: []KeyOffer{{Key: "[w]", Label: "write"}},
		Taken:  "Write 2 changes to the file? [y/N]",
	}.Rows(80)
	if len(rows) != 1 || !strings.Contains(rows[0], "[y/N]") {
		t.Fatalf("the confirm did not take the footer: %#v", rows)
	}
}
