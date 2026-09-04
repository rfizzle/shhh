package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/todo"
)

// backlog writes items the store can read, one file per slug, and answers
// with the root they live under.
func backlog(t *testing.T, items ...string) string {
	t.Helper()
	root := t.TempDir()
	dir := todo.Dir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, slug := range items {
		body := "---\ntitle: " + slug + "\nsize: S\n---\n## Tests\n- true\n"
		if err := os.WriteFile(filepath.Join(dir, slug+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestSprint_TakesEachReadyItemOnce(t *testing.T) {
	root := backlog(t, "a-one", "b-two")
	store := todo.Load(todo.BuiltinCode(), root)
	sp := StartSprint("s1", "manual", 0, false)

	first, ok := sp.Next(store)
	if !ok || first.Slug != "a-one" {
		t.Fatalf("first = %q/%v, want a-one", first.Slug, ok)
	}
	if sp.Current != "a-one" || sp.ItemStarted.IsZero() {
		t.Fatalf("the sprint should be on the item it took: %+v", sp)
	}
	// The same store again: the item is still open in it, and the sprint
	// must not hand it back a second time whatever the file says.
	second, ok := sp.Next(store)
	if !ok || second.Slug != "b-two" {
		t.Fatalf("second = %q/%v, want b-two", second.Slug, ok)
	}
	if _, ok := sp.Next(store); ok || sp.Ended != SprintEmpty {
		t.Fatalf("a drained backlog should end the sprint empty: %+v", sp)
	}
	if sp.Over() != true {
		t.Fatal("an ended sprint is over")
	}
}

func TestSprint_MaxStopsIt(t *testing.T) {
	root := backlog(t, "a-one", "b-two")
	store := todo.Load(todo.BuiltinCode(), root)
	sp := StartSprint("s1", "manual", 1, false)
	if _, ok := sp.Next(store); !ok {
		t.Fatal("the first item should be taken")
	}
	sp.Finished("a-one")
	if _, ok := sp.Next(store); ok || sp.Ended != SprintCapped {
		t.Fatalf("the cap should end the sprint: %+v", sp)
	}
	if !strings.Contains(sp.Count(), "1 of at most 1 done") {
		t.Fatalf("count = %q", sp.Count())
	}
}

// A sprint file scopes the ready list, so it scopes the sprint: the loop
// takes Store.Ready() and never a list of its own.
func TestSprint_TakesTheSprintFilesSetInItsOrder(t *testing.T) {
	root := backlog(t, "a-one", "b-two", "c-three")
	sprint := "---\nname: caching\nstatus: open\n---\nMake it fast.\n\n## Items\n- c-three\n- a-one\n"
	if err := os.WriteFile(todo.SprintPath(root), []byte(sprint), 0o644); err != nil {
		t.Fatal(err)
	}
	store := todo.Load(todo.BuiltinCode(), root)
	sp := StartSprint("s1", "manual", 0, false)
	var got []string
	for {
		it, ok := sp.Next(store)
		if !ok {
			break
		}
		got = append(got, it.Slug)
	}
	if strings.Join(got, ",") != "c-three,a-one" {
		t.Fatalf("the sprint worked %v; the file names c-three then a-one", got)
	}
}

func TestSprint_BlocksAndStopsAreEndings(t *testing.T) {
	sp := StartSprint("s1", "manual", 0, false)
	sp.Blocks("a-one", "verify failed\nsecond line")
	if sp.Ended != SprintBlocked || !strings.Contains(sp.Reason, "a-one blocked — verify failed") {
		t.Fatalf("blocks = %+v", sp)
	}
	if strings.Contains(sp.Reason, "second line") {
		t.Fatalf("the reason is one line: %q", sp.Reason)
	}
	// The first ending stands: a sprint that blocked and was then stopped
	// stopped because of the block.
	sp.Stop()
	if sp.Ended != SprintBlocked {
		t.Fatalf("a second ending must not overwrite the first: %+v", sp)
	}
}

func TestSprint_CheckpointSurvivesTheProcess(t *testing.T) {
	root := backlog(t, "a-one")
	sp := StartSprint("s1", "manual", 2, true)
	store := todo.Load(todo.BuiltinCode(), root)
	if _, ok := sp.Next(store); !ok {
		t.Fatal("an item should be taken")
	}
	if err := sp.Save(root); err != nil {
		t.Fatal(err)
	}
	back, live := Live(root)
	if !live {
		t.Fatal("a saved sprint should read as live")
	}
	slug, resuming := back.Resume()
	if !resuming || slug != "a-one" || back.Max != 2 || !back.NoCommit {
		t.Fatalf("the checkpoint should carry what the sprint was asked for: %+v", back)
	}
	DiscardSprint(root)
	if _, live := Live(root); live {
		t.Fatal("a discarded sprint is not live")
	}
	// A file that will not parse reads as no sprint rather than as one
	// nobody can end.
	if err := os.WriteFile(sprintPath(root), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, live := Live(root); live {
		t.Fatal("a corrupt checkpoint must not read as a live sprint")
	}
}

// An ended sprint on disk is not one the next command picks up.
func TestSprint_AnEndedCheckpointIsNotLive(t *testing.T) {
	root := backlog(t)
	sp := StartSprint("s1", "manual", 0, false)
	sp.Stop()
	if err := sp.Save(root); err != nil {
		t.Fatal(err)
	}
	if _, live := Live(root); live {
		t.Fatal("an ended sprint must not be resumed")
	}
}

func TestSprint_ExpiredNeedsACapAndAnItem(t *testing.T) {
	sp := StartSprint("s1", "manual", 0, false)
	sp.Current, sp.ItemStarted = "a-one", time.Now().Add(-time.Hour)
	if sp.Expired(0) {
		t.Fatal("no cap is no expiry")
	}
	if !sp.Expired(time.Minute) {
		t.Fatal("an hour is past a minute")
	}
	sp.Finished("a-one")
	if sp.Expired(time.Minute) {
		t.Fatal("between items there is nothing to expire")
	}
	if !strings.Contains(TimedOut(time.Minute), "1m0s") {
		t.Fatalf("the evidence should name the cap: %q", TimedOut(time.Minute))
	}
}

func TestSprint_SummaryStatesWhereItIs(t *testing.T) {
	root := backlog(t, "a-one", "b-two")
	sp := StartSprint("s1", "manual", 0, false)
	store := todo.Load(todo.BuiltinCode(), root)
	sp.Next(store)
	if got := sp.Summary(); !strings.Contains(got, "0 items done") || !strings.Contains(got, "on a-one") {
		t.Fatalf("summary = %q", got)
	}
	sp.Finished("a-one")
	sp.Blocks("b-two", "nope")
	if got := sp.Summary(); !strings.Contains(got, "1 item done") || !strings.Contains(got, "blocked:") {
		t.Fatalf("ended summary = %q", got)
	}
}
