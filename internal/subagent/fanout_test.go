package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
)

// hangingEnv builds children whose stream blocks until the child's context is
// cancelled, so a test can hold several of them in one batch and look at the
// batch rather than at a race to finish.
func hangingEnv() EnvFactory {
	return func(ctx context.Context, spec Spec) (Env, error) {
		stream := func(msgs []provider.Message, _ string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
			ch := make(chan provider.StreamEvent)
			go func() {
				<-ctx.Done()
				close(ch)
			}()
			return ch, func() {}, nil
		}
		return Env{
			SystemPrompt: "sys",
			Stream:       stream,
			Executor:     func(string, json.RawMessage) (string, error) { return "", errors.New("unused") },
		}, nil
	}
}

// TestSpawnBatchGroupsOneRound covers what a fan-out block is built on
// : children spawned between two BeginBatch calls share a batch, and
// the batch a round opened is the one BatchSize counts.
func TestSpawnBatchGroupsOneRound(t *testing.T) {
	sup := New(context.Background(), Options{Root: t.TempDir(), NewEnv: hangingEnv()})
	t.Cleanup(sup.Close)

	first := sup.BeginBatch()
	if _, err := spawnRaw(sup, `{"role":"researcher","task":"survey one"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := spawnRaw(sup, `{"role":"researcher","task":"survey two"}`); err != nil {
		t.Fatal(err)
	}
	second := sup.BeginBatch()
	if _, err := spawnRaw(sup, `{"role":"researcher","task":"survey three"}`); err != nil {
		t.Fatal(err)
	}

	if first == second {
		t.Fatalf("BeginBatch handed out the same number twice: %d", first)
	}
	if got := sup.BatchSize(first); got != 2 {
		t.Fatalf("BatchSize(first) = %d, want 2", got)
	}
	if got := sup.BatchSize(second); got != 1 {
		t.Fatalf("BatchSize(second) = %d, want 1", got)
	}
	if got := sup.Batch(); got != second {
		t.Fatalf("Batch() = %d, want the open batch %d", got, second)
	}
	for _, st := range sup.Snapshot() {
		want := first
		if st.Task == "survey three" {
			want = second
		}
		if st.Batch != want {
			t.Fatalf("%s is in batch %d, want %d", st.Name, st.Batch, want)
		}
	}
}

// TestSpawnBatchWithoutBeginBatch keeps hosts that never open a batch — the
// headless runner, a bare test — working: everything they spawn shares batch
// zero rather than failing.
func TestSpawnBatchWithoutBeginBatch(t *testing.T) {
	sup := New(context.Background(), Options{Root: t.TempDir(), NewEnv: hangingEnv()})
	t.Cleanup(sup.Close)

	if _, err := spawnRaw(sup, `{"role":"researcher","task":"survey"}`); err != nil {
		t.Fatal(err)
	}
	st := sup.Snapshot()
	if len(st) != 1 || st[0].Batch != 0 {
		t.Fatalf("unbatched spawn got batch %v, want 0", st)
	}
	if sup.BatchSize(0) != 1 {
		t.Fatal("BatchSize(0) should count the unbatched child")
	}
}

// TestSpawnDeclaredSteps covers the denominator a lane's progress bar needs
// : a spawn may declare one, and a count nobody could mean is dropped
// rather than clamped into an invented ratio.
func TestSpawnDeclaredSteps(t *testing.T) {
	for _, tc := range []struct {
		name string
		args string
		want int
	}{
		{"declared", `{"role":"researcher","task":"t","steps":5}`, 5},
		{"undeclared", `{"role":"researcher","task":"t"}`, 0},
		{"zero", `{"role":"researcher","task":"t","steps":0}`, 0},
		{"negative", `{"role":"researcher","task":"t","steps":-3}`, 0},
		{"over the ceiling", `{"role":"researcher","task":"t","steps":100}`, 0},
		{"at the ceiling", `{"role":"researcher","task":"t","steps":20}`, 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args, err := parseSpawnArgs(nil, json.RawMessage(tc.args))
			if err != nil {
				t.Fatal(err)
			}
			if args.steps != tc.want {
				t.Fatalf("steps = %d, want %d", args.steps, tc.want)
			}
		})
	}
}

// TestStatusStepsAndElapsed covers the rest of what a lane reads off a
// status: the declared step count comes back on it, progress never runs past
// that denominator, and elapsed keeps moving until the child settles.
func TestStatusStepsAndElapsed(t *testing.T) {
	c := &child{name: "writer-1", steps: 3, started: time.Now().Add(-2 * time.Second)}

	st := c.status()
	if st.Steps != 3 || st.Step != 0 {
		t.Fatalf("fresh child = %d/%d, want 0/3", st.Step, st.Steps)
	}
	if st.Elapsed < time.Second {
		t.Fatalf("elapsed = %v, want the time since it was spawned", st.Elapsed)
	}

	// Two announcements, each followed by a call: two steps entered.
	c.streaming = "look at the loop"
	c.beginToolEntry("read_file", `{"path":"loop.go"}`)
	c.streaming = "now the tests"
	c.beginToolEntry("read_file", `{"path":"loop_test.go"}`)
	if st := c.status(); st.Step != 2 {
		t.Fatalf("step = %d, want 2", st.Step)
	}

	// A child that announces more than the spawn declared reports the
	// denominator it was given, never a ratio over one.
	for range 5 {
		c.streaming = "another"
		c.beginToolEntry("read_file", `{"path":"x.go"}`)
	}
	if st := c.status(); st.Step != 3 {
		t.Fatalf("step = %d, want it clamped to the declared 3", st.Step)
	}

	c.set(StateDone, "done · 7 tools")
	settled := c.status().Elapsed
	time.Sleep(10 * time.Millisecond)
	if again := c.status().Elapsed; again != settled {
		t.Fatalf("a finished child's elapsed moved: %v then %v", settled, again)
	}
}

// TestStatusSummary covers the one line a finished lane keeps: the first line
// of the child's report, and nothing at all before it has one.
func TestStatusSummary(t *testing.T) {
	c := &child{name: "researcher-1", started: time.Now()}
	c.report = "found three callers\nand here is the long version"

	c.set(StateRunning, "running")
	if s := c.status().Summary; s != "" {
		t.Fatalf("a running child reported a summary: %q", s)
	}
	c.set(StateDone, "done · 4 tools")
	// firstLine marks a report it truncated, so the lane never implies the
	// child said only that.
	if s := c.status().Summary; s != "found three callers …" {
		t.Fatalf("summary = %q, want the report's first line", s)
	}
}

// A queued lane is a task waiting, not a checkout waiting. Four lanes over
// three slots leaves one writer in the queue, and a writer given its copy of
// the repository at spawn would hold a whole checkout on disk for as long as
// it waited there — sixteen spawned writers is sixteen copies for three
// running children — seeded from a tree the session has since moved past.
// The copy is taken when the lane starts.
func TestQueuedWriterHasNoWorktreeUntilItRuns(t *testing.T) {
	repo := initTestRepo(t)
	sup := New(context.Background(), Options{Root: repo, NewEnv: hangingEnv(), MaxConcurrent: 1})
	t.Cleanup(sup.Close)

	// The first lane is given the one slot before the second is spawned, so
	// which of the two queues is the test's to decide rather than the
	// scheduler's.
	if _, err := spawnRaw(sup, `{"role":"writer","task":"lane one"}`); err != nil {
		t.Fatalf("the first writer should spawn: %v", err)
	}
	waitState(t, sup, "writer-1", StateRunning)
	if _, err := spawnRaw(sup, `{"role":"writer","task":"lane two"}`); err != nil {
		t.Fatalf("the second writer should spawn: %v", err)
	}

	st, ok := sup.Get("writer-2")
	if !ok || st.State != StateQueued {
		t.Fatalf("the second writer should be queued behind the one slot, got %+v", st)
	}
	if st.Seeded != 0 {
		t.Fatalf("a queued writer reports %d seeded paths, want none — it has no copy yet", st.Seeded)
	}
	// The running lane has a copy and the queued one does not. Counted in
	// the checkout rather than asked of the supervisor, because the point is
	// what is on disk.
	if n := linkedWorktrees(t, repo); n != 1 {
		t.Fatalf("%d worktrees beside the checkout, want the running lane's alone", n)
	}
	if _, err := sup.WorktreeDiff("writer-2"); err == nil {
		t.Fatal("a queued writer answered with a patch against a worktree it does not have")
	}

	// The slot comes free and the queued lane takes its copy then.
	if err := sup.Kill("writer-1"); err != nil {
		t.Fatalf("kill: %v", err)
	}
	waitState(t, sup, "writer-2", StateRunning)
	if _, err := sup.WorktreeDiff("writer-2"); err != nil {
		t.Fatalf("a started writer has no worktree to diff: %v", err)
	}
}
