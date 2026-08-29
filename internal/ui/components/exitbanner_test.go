package components

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// plainBanner is the banner with its colour taken off — everything a reader
// on a monochrome terminal gets, which has to be all of it (invariant 1).
func plainBanner(b ExitBanner, width int) string {
	return ansi.Strip(b.View(width))
}

// fullBanner is the ordinary exit: a priced sitting on a saved conversation.
func fullBanner() ExitBanner {
	return ExitBanner{Session: "(last session)", Turns: 12, Spend: "$0.42",
		Resume: "shhh code --continue"}
}

// A session that never said anything has nothing to resume and nothing to
// report; the shell prompt says more about that than an acknowledgement line.
func TestExitBanner_NothingSaidPrintsNothing(t *testing.T) {
	for _, c := range []struct {
		name  string
		b     ExitBanner
		width int
	}{
		{"no turns", ExitBanner{Session: "(last session)", Resume: "shhh chat --continue"}, 80},
		{"no turns and nothing saved", ExitBanner{Unsaved: true}, 80},
		{"no terminal to print on", fullBanner(), 0},
		{"a terminal narrower than the label column", fullBanner(), 8},
	} {
		if got := plainBanner(c.b, c.width); got != "" {
			t.Fatalf("%s: banner should be empty, got %q", c.name, got)
		}
	}
}

// The three rows and what each carries: the slot and its size, what the
// sitting cost, and the command that reopens it.
func TestExitBanner_SaysWhatTheScreenTookWithIt(t *testing.T) {
	got := plainBanner(fullBanner(), 80)
	want := "session  (last session) · 12 turns\n" +
		"spent    $0.42\n" +
		"resume   shhh code --continue"
	if got != want {
		t.Fatalf("banner =\n%q\nwant\n%q", got, want)
	}
}

// Nothing spent is no row, not a made-up $0.00 — the same rule §17c holds the
// start screen's resume offer to.
func TestExitBanner_UnpricedSittingDropsTheRow(t *testing.T) {
	b := fullBanner()
	b.Spend = ""
	got := plainBanner(b, 80)
	if strings.Contains(got, "spent") || strings.Contains(got, "$") {
		t.Fatalf("an unspent sitting should have no spend row, got %q", got)
	}
	if !strings.Contains(got, "shhh code --continue") {
		t.Fatalf("the resume line survives a missing price, got %q", got)
	}
}

// A conversation nothing could be written for names no slot and offers no
// command: the failure a reader must not find out about by typing a resume
// that quietly reopens something older.
func TestExitBanner_UnsavedNamesNoSlotAndOffersNoCommand(t *testing.T) {
	b := fullBanner()
	b.Unsaved = true
	got := plainBanner(b, 80)
	for _, gone := range []string{"(last session)", "shhh code --continue"} {
		if strings.Contains(got, gone) {
			t.Fatalf("an unsaved conversation should not mention %q, got %q", gone, got)
		}
	}
	if !strings.Contains(got, "12 turns") {
		t.Fatalf("how much was lost is the part still true, got %q", got)
	}
	if !strings.Contains(got, "not saved") {
		t.Fatalf("the resume row has to say why there is no command, got %q", got)
	}
}

// The resume command is the one field a reader has to retype, so it is never
// clipped: a command with its tail eaten is not a shorter command.
func TestExitBanner_ResumeCommandIsNeverClipped(t *testing.T) {
	for width := 10; width <= 80; width++ {
		got := plainBanner(fullBanner(), width)
		if !strings.Contains(got, "shhh code --continue") {
			t.Fatalf("width %d: the resume command was clipped: %q", width, got)
		}
	}
}

// The session row's ladder: the turn count goes first, and the name is what
// the clip eats into last.
func TestExitBanner_SessionRowDropsTheCountBeforeTheName(t *testing.T) {
	b := ExitBanner{Session: "refactor-the-round-accounting", Turns: 4,
		Resume: "shhh code --continue"}
	for _, c := range []struct {
		width int
		want  string
	}{
		{80, "session  refactor-the-round-accounting · 4 turns"},
		{45, "session  refactor-the-round-accounting"},
		{30, "session  refactor-the-round-a…"},
	} {
		got := strings.SplitN(plainBanner(b, c.width), "\n", 2)[0]
		if got != c.want {
			t.Fatalf("width %d: session row = %q, want %q", c.width, got, c.want)
		}
	}
}

// Every distinction on this surface is a word — the three labels and the
// sentence in place of a command — so the mono palette loses the tint and
// keeps all of it (invariant 1).
func TestExitBanner_SaysTheSameThingInMono(t *testing.T) {
	was := Mono()
	t.Cleanup(func() { SetMono(was) })
	for _, b := range []ExitBanner{fullBanner(), {Turns: 3, Unsaved: true}} {
		SetMono(false)
		color := plainBanner(b, 80)
		SetMono(true)
		if mono := plainBanner(b, 80); mono != color {
			t.Fatalf("mono banner =\n%q\nwant\n%q", mono, color)
		}
	}
}
