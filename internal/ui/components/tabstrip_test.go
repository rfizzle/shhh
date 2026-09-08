package components

// The tab strip against the one promise it makes: everything it says, it says
// in a mark and in a word, so a terminal with one colour reads the same strip.

import (
	"strings"
	"testing"
)

func threeTabs(at int, answered ...int) TabStrip {
	tabs := []Tab{{Label: "1"}, {Label: "2"}, {Label: "3"}, {Label: "submit"}}
	for _, i := range answered {
		tabs[i].Answered, tabs[i].Word = true, "answered"
	}
	long, short := TabTally(at, 3, len(answered), "will be sent as skipped")
	return TabStrip{Tabs: tabs, At: at, Tail: long, ShortTail: short}
}

// The marks say where you are and what is done, and no two tabs wear the same
// one for different reasons.
func TestTabStrip_MarksWhereYouAreAndWhatIsDone(t *testing.T) {
	got := stripANSI(threeTabs(1, 0).View(120))
	for _, want := range []string{tabDone + " 1 answered", tabHere + " 2", tabTodo + " 3", tabTodo + " submit"} {
		if !strings.Contains(got, want) {
			t.Errorf("the strip does not carry %q: %q", want, got)
		}
	}
	// And the tail says the same facts in words, which is the half a
	// monochrome terminal is left with.
	for _, want := range []string{"2 of 3", "2 unanswered will be sent as skipped"} {
		if !strings.Contains(got, want) {
			t.Errorf("the tail does not say %q: %q", want, got)
		}
	}
}

// A row too wide gives up the per-tab words first, then the long tail for the
// short one, and keeps the marks whatever happens: a strip with no marks on it
// has lost the thing it is for.
func TestTabStrip_GivesUpTheWordsBeforeTheMarks(t *testing.T) {
	s := threeTabs(1, 0, 2)
	full := stripANSI(s.View(200))
	if !strings.Contains(full, "1 answered") {
		t.Fatalf("a wide row keeps the words: %q", full)
	}
	for _, width := range []int{60, 40, 24, 12, 6} {
		got := stripANSI(s.View(width))
		if len([]rune(got)) > width {
			t.Errorf("at %d columns the strip is %d wide: %q", width, len([]rune(got)), got)
		}
		if !strings.Contains(got, tabHere+" 2") {
			t.Errorf("at %d columns the strip lost the tab you are standing on: %q", width, got)
		}
	}
	// The middle rung: narrow enough to drop the words, wide enough to keep
	// the whole tail.
	mid := stripANSI(s.View(60))
	if strings.Contains(mid, "1 answered") {
		t.Errorf("the words go first: %q", mid)
	}
	if !strings.Contains(mid, "1 unanswered") {
		t.Errorf("the tail outlives the words: %q", mid)
	}
}

// A set with nothing left open says so rather than counting nought.
func TestTabTally_SaysWhenNothingIsOpen(t *testing.T) {
	long, short := TabTally(1, 3, 3, "will be sent as skipped")
	if !strings.Contains(long, "all answered") || short != "2 of 3" {
		t.Errorf("TabTally = %q, %q", long, short)
	}
	// On the tab that ends the set there is no position among the questions
	// to report, so the word is the tab's own.
	last, _ := TabTally(3, 3, 1, "will be sent as skipped")
	if !strings.HasPrefix(last, "submit ·") {
		t.Errorf("the tail on the last tab = %q", last)
	}
}

// An empty strip draws nothing rather than a bare tail.
func TestTabStrip_DrawsNothingWithNoTabs(t *testing.T) {
	if got := (TabStrip{Tail: "1 of 0"}).View(80); got != "" {
		t.Errorf("View = %q", got)
	}
	if got := threeTabs(0).View(0); got != "" {
		t.Errorf("View at no width = %q", got)
	}
}
