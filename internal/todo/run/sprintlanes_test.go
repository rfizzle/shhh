package run

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/todo"
)

// declared writes an item that declares the paths it touches, or none.
func declared(t *testing.T, root, slug string, paths ...string) {
	t.Helper()
	body := "---\ntitle: " + slug + "\nsize: S\n---\n## Tests\n- true\n"
	if len(paths) > 0 {
		body += "\n## Touches\n"
		for _, p := range paths {
			body += "- `" + p + "` — why\n"
		}
	}
	if err := os.WriteFile(filepath.Join(todo.Dir(root), slug+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func laned(n int) *Sprint {
	sp := StartSprint("s1", "", 0, false)
	sp.Parallel = n
	return sp
}

// An item is taken beside the running lanes only where its declared paths
// meet none of theirs, and the list moves on past one that does.
func TestSprint_TakeLaneSkipsAnItemThatOverlapsARunningLane(t *testing.T) {
	root := backlog(t)
	declared(t, root, "a-one", "internal/cache/")
	declared(t, root, "b-two", "internal/cache/ttl.go")
	declared(t, root, "c-three", "internal/report/")
	sp := laned(3)
	store := todo.Load(todo.BuiltinCode(), root)

	var took []string
	for {
		it, ok := sp.TakeLane(store)
		if !ok {
			break
		}
		took = append(took, it.Slug)
	}
	if strings.Join(took, ",") != "a-one,c-three" {
		t.Fatalf("took %v: b-two's file is under a-one's directory", took)
	}
	if sp.Over() {
		t.Fatal("a sprint with lanes running is not over because nothing more fits")
	}
	sp.LaneEnded("a-one", true, "", 3, 1.25)
	if it, ok := sp.TakeLane(store); !ok || it.Slug != "b-two" {
		t.Fatalf("with a-one landed b-two fits: %q/%v", it.Slug, ok)
	}
	if sp.Turns != 3 || sp.Cost != 1.25 || len(sp.Done) != 1 {
		t.Fatalf("a lane's end adds its spend once and records it done: %+v", sp)
	}
}

// An item that declares nothing is serialised rather than refused: nothing
// is taken past it while lanes run, it is taken alone once they drain, and
// nothing is taken beside it.
func TestSprint_TakeLaneWorksAnUndeclaredItemAlone(t *testing.T) {
	root := backlog(t)
	declared(t, root, "a-one", "a.go")
	declared(t, root, "b-two")
	declared(t, root, "c-three", "c.go")
	sp := laned(3)
	store := todo.Load(todo.BuiltinCode(), root)

	if it, ok := sp.TakeLane(store); !ok || it.Slug != "a-one" {
		t.Fatalf("first = %q/%v", it.Slug, ok)
	}
	if it, ok := sp.TakeLane(store); ok {
		t.Fatalf("nothing is taken past an undeclared item while a lane runs, took %s", it.Slug)
	}
	sp.LaneEnded("a-one", true, "", 0, 0)
	if it, ok := sp.TakeLane(store); !ok || it.Slug != "b-two" {
		t.Fatalf("once the lanes drain the undeclared item is taken: %q/%v", it.Slug, ok)
	}
	if it, ok := sp.TakeLane(store); ok {
		t.Fatalf("an undeclared item is worked alone, but %s was taken beside it", it.Slug)
	}
}

// A lane that blocks does not end the sprint; the sprint ends blocked once
// nothing more can be taken, naming what blocked, and a cap reached while
// lanes run waits for them.
func TestSprint_TakeLaneEndsOnlyOnceTheLanesHaveDrained(t *testing.T) {
	root := backlog(t)
	declared(t, root, "a-one", "a.go")
	declared(t, root, "b-two", "b.go")
	sp := laned(2)
	sp.Max = 1
	store := todo.Load(todo.BuiltinCode(), root)

	if _, ok := sp.TakeLane(store); !ok {
		t.Fatal("the first item is taken")
	}
	if _, ok := sp.TakeLane(store); ok || sp.Over() {
		t.Fatalf("the cap stops the next item and waits for the lane: over=%v", sp.Over())
	}
	sp.LaneEnded("a-one", false, "the checks failed\nand more", 0, 0)
	if _, ok := sp.TakeLane(store); ok || sp.Ended != SprintCapped {
		t.Fatalf("with the lane drained the cap ends it: %q", sp.Ended)
	}

	sp = laned(2)
	sp.TakeLane(store)
	sp.TakeLane(store)
	sp.LaneEnded("a-one", false, "the checks failed", 0, 0)
	if sp.Over() {
		t.Fatal("a blocked lane does not stop the sprint")
	}
	sp.LaneEnded("b-two", true, "", 0, 0)
	if _, ok := sp.TakeLane(store); ok || sp.Ended != SprintBlocked || sp.Reason != "a-one blocked — the checks failed" {
		t.Fatalf("the sprint ends blocked on what blocked: %q %q", sp.Ended, sp.Reason)
	}
}

// The lanes a dead process left are taken off the checkpoint, and what they
// had spent on that process's ledger is counted once.
func TestSprint_OrphansAreCountedOnce(t *testing.T) {
	sp := laned(2)
	sp.Lanes = []SprintLane{
		{Slug: "a-one", Ledger: "dead#1/a-one", Turns: 2, Cost: 1.5},
		{Slug: "b-two", Ledger: "s1", Turns: 1, Cost: 0.5},
	}
	if turns, cost := sp.InFlight(); turns != 3 || cost != 2 {
		t.Fatalf("in flight = %d/%v", turns, cost)
	}
	got := sp.Orphans()
	if len(got) != 2 || len(sp.Lanes) != 0 {
		t.Fatalf("every lane is handed back: %+v", got)
	}
	if sp.Turns != 2 || sp.Cost != 1.5 {
		t.Fatalf("only the dead ledger's figure is added: %d/%v", sp.Turns, sp.Cost)
	}
}

// The lanes are part of the checkpoint, so the board and a process picking
// the sprint up read them; a checkpoint written before they existed reads as
// a sprint working one item at a time.
func TestSprint_LanesSurviveTheProcess(t *testing.T) {
	root := backlog(t)
	sp := laned(3)
	sp.Lanes = []SprintLane{{Slug: "a-one", Paths: []string{"a.go"}, Tree: "/tmp/x", Stage: StageImplement}}
	if err := sp.Save(root); err != nil {
		t.Fatal(err)
	}
	back, live := Live(root)
	if !live || !back.Laned() || len(back.Lanes) != 1 || back.Lanes[0].Stage != StageImplement {
		t.Fatalf("read back %+v", back)
	}
	if !strings.Contains(back.Summary(), "on a-one") {
		t.Fatalf("the summary names the lanes: %s", back.Summary())
	}
	var old Sprint
	if err := json.Unmarshal([]byte(`{"session":"s","current":"a-one","done":["x"]}`), &old); err != nil {
		t.Fatal(err)
	}
	if old.Laned() || len(old.Lanes) != 0 || strings.Join(old.Working(), ",") != "a-one" {
		t.Fatalf("an older checkpoint is a serial sprint: %+v", old)
	}
}

// A stop asked for by another surface is a file beside the checkpoint.
func TestSprint_StopIsAskedForThroughAFile(t *testing.T) {
	root := backlog(t)
	if StopRequested(root) {
		t.Fatal("nothing has asked")
	}
	if err := RequestStop(root); err != nil {
		t.Fatal(err)
	}
	if !StopRequested(root) {
		t.Fatal("the request is seen")
	}
	ClearStop(root)
	if StopRequested(root) {
		t.Fatal("the request is taken away")
	}
}
