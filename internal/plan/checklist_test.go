package plan

import (
	"fmt"
	"strings"
	"testing"
)

func tally(l Checklist) string {
	done, total, current := l.Tally()
	return fmt.Sprintf("%d/%d %q", done, total, current)
}

// No list is nothing: a checklist nobody declared tallies to zero steps, which
// every surface draws as no block rather than as zero of zero.
func TestChecklist_NoListIsNothing(t *testing.T) {
	var l Checklist
	if l.Note("I will look at the loop.", true) || l.Note("progress: 1", true) {
		t.Fatal("a message with no list moved an empty checklist")
	}
	if got := tally(l); got != `0/0 ""` {
		t.Fatalf("tally = %s", got)
	}
	if l.Encode() != "" {
		t.Fatal("an empty checklist should store as nothing")
	}
}

// A list is taken from a message that goes on to a call, and its marks move it.
func TestChecklist_TakenBeforeACallAndMarked(t *testing.T) {
	var l Checklist
	if !l.Note("Plan:\n1. Read the loop\n2. Add the flag\n3. Test it", true) {
		t.Fatal("the list was not taken")
	}
	if got := tally(l); got != `0/3 "Read the loop"` {
		t.Fatalf("fresh tally = %s", got)
	}
	l.Note("progress: 1\nprogress: 7", true)
	if got := tally(l); got != `1/3 "Add the flag"` {
		t.Fatalf("after a mark = %s (a number off the list marks nothing)", got)
	}
	// A turn-ending message's marks count too.
	l.Note("All done.\nprogress: 2\nprogress: 3", false)
	if got := tally(l); got != `3/3 ""` {
		t.Fatalf("after the closing marks = %s", got)
	}
}

// A message that ends the turn is a report, and its numbered list is not taken.
func TestChecklist_AReportIsNotAList(t *testing.T) {
	var l Checklist
	l.Note("Changed:\n1. loop.go\n2. loop_test.go", false)
	if _, total, _ := l.Tally(); total != 0 {
		t.Fatalf("a report's list was taken: %s", tally(l))
	}
}

// A second numbered list without the marker is text; under `steps:` it
// replaces the unfinished steps, keeps the finished ones, and numbers the new
// ones after the last finished step.
func TestChecklist_OnlyAMarkedListRevises(t *testing.T) {
	var l Checklist
	l.Note("1. Read\n2. Patch\n3. Test", true)
	l.Note("progress: 1", true)
	l.Note("Files:\n1. a.go\n2. b.go", true)
	if got := tally(l); got != `1/3 "Patch"` {
		t.Fatalf("an unmarked list replaced the checklist: %s", got)
	}
	l.Note("progress: 2\nChanging course.\n**steps:**\n1. Add a flag\n2. Test", true)
	if got := tally(l); got != `2/4 "Add a flag"` {
		t.Fatalf("after the revision = %s, want the two finished kept and two new", got)
	}
	if l.Steps[2].Number != 3 || l.Steps[3].Number != 4 {
		t.Fatalf("the new steps should be numbered after the finished ones: %+v", l.Steps)
	}
	l.Note("progress: 3", true)
	if got := tally(l); got != `3/4 "Test"` {
		t.Fatalf("marking a revised step = %s", got)
	}
}

// A revision in a message that ends the turn revises nothing.
func TestChecklist_ARevisionEndingTheTurnIsText(t *testing.T) {
	var l Checklist
	l.Note("1. Read\n2. Patch", true)
	l.Note("steps:\n1. Something else", false)
	if got := tally(l); got != `0/2 "Read"` {
		t.Fatalf("a closing message revised the list: %s", got)
	}
}

// A list longer than a checklist may be is a list, first time or revised.
func TestChecklist_ATooLongListIsAList(t *testing.T) {
	var long strings.Builder
	for i := 1; i <= MaxWorkingSteps+1; i++ {
		fmt.Fprintf(&long, "%d. step %d\n", i, i)
	}
	var l Checklist
	l.Note(long.String(), true)
	if _, total, _ := l.Tally(); total != 0 {
		t.Fatalf("a %d-item list was taken", MaxWorkingSteps+1)
	}
	l.Note("1. Read", true)
	l.Note("steps:\n"+long.String(), true)
	if got := tally(l); got != `0/1 "Read"` {
		t.Fatalf("a too-long revision replaced the list: %s", got)
	}
}

// A new turn may declare a fresh list in its first message that goes on to a
// call; until then the old list stands, and a later list in that turn is text.
func TestChecklist_ANewTurnMayDeclareAgain(t *testing.T) {
	var l Checklist
	l.Note("1. Read\n2. Patch\nprogress: 1", true)
	l.Reopen()
	if got := tally(l); got != `1/2 "Patch"` {
		t.Fatalf("reopening dropped the list: %s", got)
	}
	l.Note("1. Docs\n2. Changelog", true)
	if got := tally(l); got != `0/2 "Docs"` {
		t.Fatalf("the new turn's list = %s", got)
	}
	l.Note("1. Other\n2. Things", true)
	if got := tally(l); got != `0/2 "Docs"` {
		t.Fatalf("a second list in the turn replaced it: %s", got)
	}

	var m Checklist
	m.Note("1. Read\n2. Patch", true)
	m.Reopen()
	m.Note("Looking first.", true)
	m.Note("1. Other\n2. Things", true)
	if got := tally(m); got != `0/2 "Read"` {
		t.Fatalf("a list after the turn's first call replaced it: %s", got)
	}
}

// A mark writes a new map rather than the one a copy shares.
func TestChecklist_ACopyIsNotMovedByTheOriginal(t *testing.T) {
	var l Checklist
	l.Note("1. Read\n2. Patch\nprogress: 1", true)
	copied := l
	l.Note("progress: 2", true)
	if got := tally(copied); got != `1/2 "Patch"` {
		t.Fatalf("the copy moved with the original: %s", got)
	}
}

// What a slot stores is what comes back.
func TestChecklist_EncodeRoundTrips(t *testing.T) {
	var l Checklist
	l.Note("1. Read\n2. Patch\n3. Test\nprogress: 1", true)
	l.Note("progress: 2\nsteps:\n1. Retest", true)
	back := DecodeChecklist(l.Encode())
	if tally(back) != tally(l) || back.Steps[2].Number != 3 {
		t.Fatalf("round trip = %s %+v, want %s", tally(back), back.Steps, tally(l))
	}
	for _, junk := range []string{"", "{", `{"steps":[]}`} {
		if _, total, _ := DecodeChecklist(junk).Tally(); total != 0 {
			t.Fatalf("%q decoded to a list", junk)
		}
	}
}
