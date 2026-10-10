package run

import (
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/todo"
)

// A halted sprint takes nothing though the list holds more, finishes the
// lanes in flight, and ends blocked naming why it halted.
func TestSprint_AHaltedSprintTakesNothingAndEndsBlocked(t *testing.T) {
	root := backlog(t)
	declared(t, root, "a-one", "a.go")
	declared(t, root, "b-two", "b.go")
	sp := laned(2)
	store := todo.Load(todo.BuiltinCode(), root)
	if it, ok := sp.TakeLane(store); !ok || it.Slug != "a-one" {
		t.Fatalf("first = %q/%v", it.Slug, ok)
	}
	sp.Halt("checkpoint · tui blocked — check smoke failed")
	if it, ok := sp.TakeLane(store); ok {
		t.Fatalf("a halted sprint took %s", it.Slug)
	}
	if sp.Over() {
		t.Fatal("the lane in flight is owed its ending")
	}
	sp.LaneEnded("a-one", true, "", 0, 0)
	if _, ok := sp.TakeLane(store); ok {
		t.Fatal("a halted sprint took an item after its lanes drained")
	}
	if sp.Ended != SprintBlocked || !strings.Contains(sp.Reason, "checkpoint · tui blocked — check smoke failed") {
		t.Fatalf("ended %q: %q", sp.Ended, sp.Reason)
	}
}
