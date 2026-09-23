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

// A ceiling is read before an item is taken: under it the sprint takes the
// next item, at it Peek has nothing to offer and Next ends the sprint capped
// with both figures in dollars.
func TestSprint_CostCapRefusesTheNextItem(t *testing.T) {
	root := backlog(t, "a-one", "b-two")
	store := todo.Load(todo.BuiltinCode(), root)
	sp := StartSprint("s1", "manual", 0, false)
	sp.CapCents = 2000

	if _, ok := sp.Next(store); !ok {
		t.Fatal("nothing spent yet, so the first item is taken")
	}
	sp.Finished("a-one")
	sp.Spent(3, 19.99)
	if next, ok := sp.Peek(store); !ok || next.Slug != "b-two" {
		t.Fatalf("under the ceiling the next item is offered, got %q/%v", next.Slug, ok)
	}
	sp.Spent(1, 0.01)
	if _, ok := sp.Peek(store); ok {
		t.Fatal("at the ceiling nothing is offered")
	}
	if sp.Over() {
		t.Fatal("peeking must not end the sprint")
	}
	if _, ok := sp.Next(store); ok {
		t.Fatal("at the ceiling no item is taken")
	}
	if sp.Ended != SprintCapped || sp.Reason != "spent $20.00 of the $20 the sprint was allowed" {
		t.Fatalf("ended %q: %q", sp.Ended, sp.Reason)
	}
	if len(sp.Attempts) != 1 {
		t.Fatalf("the refused item was not attempted: %v", sp.Attempts)
	}
}

// The ceiling rides the checkpoint, so a sprint picked up in a fresh process
// is held to the ceiling it was started with.
func TestSprint_CostCapSurvivesTheProcess(t *testing.T) {
	root := backlog(t, "a-one")
	sp := StartSprint("s1", "manual", 0, false)
	sp.CapCents = 1550
	sp.Spent(2, 4.1)
	if err := sp.Save(root); err != nil {
		t.Fatal(err)
	}
	back, live := Live(root)
	if !live || back.CapCents != 1550 || back.Cost != 4.1 {
		t.Fatalf("the ceiling and the total should come back: %+v", back)
	}
}

func TestSprint_SpendWords(t *testing.T) {
	for _, c := range []struct {
		cost     float64
		capCents int64
		words    string
		figure   string
	}{
		{4.1, 2000, "spend $4.10 of $20", "$4.10 of $20"},
		{0, 2050, "spend $0.00 of $20.50", "$0.00 of $20.50"},
		{4.1, 0, "", "$4.10"},
		{0, 0, "", ""},
	} {
		if got := SpendWords(c.cost, c.capCents); got != c.words {
			t.Errorf("SpendWords(%v, %d) = %q, want %q", c.cost, c.capCents, got, c.words)
		}
		if got := SpendFigure(c.cost, c.capCents); got != c.figure {
			t.Errorf("SpendFigure(%v, %d) = %q, want %q", c.cost, c.capCents, got, c.figure)
		}
	}
}

// The flag outranks everything; a continued sprint keeps the ceiling it was
// started under over the setting; a new one takes the setting, and a setting
// below zero is no ceiling.
func TestSprint_Bound(t *testing.T) {
	for _, c := range []struct{ had, flag, setting, want int64 }{
		{0, 0, 0, 0}, {0, 0, 500, 500}, {0, 100, 500, 100}, {0, 0, -5, 0},
		{300, 0, 500, 300}, {300, 100, 500, 100},
	} {
		sp := &Sprint{CapCents: c.had}
		if sp.Bound(c.flag, c.setting); sp.CapCents != c.want {
			t.Errorf("a sprint at %d bound by flag %d, setting %d = %d, want %d", c.had, c.flag, c.setting, sp.CapCents, c.want)
		}
	}
}

// The item in flight's running figure is counted once: a boundary written
// twice replaces rather than adds, a sprint picked up in another session
// takes the dead session's figure into the total, and the picking-up
// session's own figure waits for the item's end.
func TestSprint_TheItemInFlightIsCountedOnce(t *testing.T) {
	sp := StartSprint("dead", "", 0, false)
	sp.Current = "a-one"
	sp.Running("dead", 1, 0.5)
	sp.Running("dead", 2, 1.25)
	if sp.Cost != 0 || sp.ItemCost != 1.25 || sp.ItemTurns != 2 {
		t.Fatalf("a running figure replaces the last one and stays out of the total: %+v", sp)
	}
	// The same session asking again changes nothing: its ledger still holds
	// the figure and the item's end adds it.
	if _, ok := sp.Resume(); !ok || sp.Cost != 0 {
		t.Fatalf("a session's own figure must wait for the item's end: %+v", sp)
	}
	sp.Session = "fresh"
	if _, ok := sp.Resume(); !ok || sp.Cost != 1.25 || sp.Turns != 2 || sp.ItemCost != 0 {
		t.Fatalf("a dead session's figure joins the total when the sprint is picked up: %+v", sp)
	}
	if _, ok := sp.Resume(); !ok || sp.Cost != 1.25 {
		t.Fatalf("asking twice must not count twice: %+v", sp)
	}
	sp.Running("fresh", 1, 0.75)
	sp.Spent(1, 0.75)
	if sp.Cost != 2 || sp.Turns != 3 || sp.ItemCost != 0 || sp.ItemLedger != "" {
		t.Fatalf("the item's end adds its whole figure and clears the running one: %+v", sp)
	}
}

// A checkpoint written before the running figure existed reads as one with
// nothing in flight.
func TestSprint_AnOlderCheckpointStillReads(t *testing.T) {
	root := backlog(t)
	if err := os.MkdirAll(Dir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	old := `{"session":"s1","current":"a-one","cost":1.5,"turns":3}`
	if err := os.WriteFile(sprintPath(root), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	sp, live := Live(root)
	if !live || sp.Cost != 1.5 || sp.ItemCost != 0 {
		t.Fatalf("an older checkpoint should read as it was written: %+v", sp)
	}
	sp.Session = "s2"
	if slug, ok := sp.Resume(); !ok || slug != "a-one" || sp.Cost != 1.5 || sp.Turns != 3 {
		t.Fatalf("resuming an older checkpoint adds nothing: %+v", sp)
	}
}
