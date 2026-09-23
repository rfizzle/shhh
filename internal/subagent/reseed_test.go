package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/provider"
)

// put and take are writeInto and readFrom for a child's own goroutine, which
// has no test to fail: a write that went wrong shows up as the assertion on
// what the child read or handed back.
func put(root, rel, body string) {
	path := filepath.Join(root, rel)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, []byte(body), 0o644)
}

func take(root, rel string) string {
	data, _ := os.ReadFile(filepath.Join(root, rel))
	return string(data)
}

// reseedBase is the committed file both writers work on: two lines far enough
// apart that a change to one is not context for a change to the other.
const reseedBase = "package main\n\nvar x = 0\n\n// one\n// two\n// three\n// four\n// five\n// six\n\nvar y = 0\n"

// reseedRepo is a repository whose one committed file is reseedBase.
func reseedRepo(t *testing.T) string {
	t.Helper()
	repo := initTestRepo(t)
	writeInto(t, repo, "main.go", reseedBase)
	if _, err := runGit(repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-am", "base"); err != nil {
		t.Fatal(err)
	}
	return repo
}

// A landed patch carried into a copy moves the copy's base by exactly that
// patch: the file has the landed text, the writer's own work is still there
// on top of it, and the patch the copy hands back is the writer's alone.
func TestReseedWorktree_TheLandedChangeJoinsTheBase(t *testing.T) {
	repo := reseedRepo(t)
	other, err := addWorktree(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer removeWorktree(other.repoTop, other.dir)
	mine, err := addWorktree(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer removeWorktree(mine.repoTop, mine.dir)

	writeInto(t, other.root, "main.go", strings.Replace(reseedBase, "var x = 0", "var x = 1", 1))
	landed, err := worktreePatch(other.dir)
	if err != nil {
		t.Fatal(err)
	}
	writeInto(t, mine.root, "two.go", "package main\n\nvar two = 2\n")

	if err := reseedWorktree(mine.dir, landed); err != nil {
		t.Fatalf("a patch over a file the writer has not touched should carry: %v", err)
	}
	if got := readFrom(t, mine.root, "main.go"); !strings.Contains(got, "var x = 1") {
		t.Fatalf("the landed change is not in the copy:\n%s", got)
	}
	if got := readFrom(t, mine.root, "two.go"); got != "package main\n\nvar two = 2\n" {
		t.Fatalf("the writer's own work did not survive the reseed: %q", got)
	}
	patch, err := worktreePatch(mine.dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(patch, "two.go") || strings.Contains(patch, "var x = 1") {
		t.Fatalf("the copy's patch should be its own work alone:\n%s", patch)
	}
}

// A landed patch that meets the writer's own work is not forced: every file
// in the copy and its base are exactly as they were, and the refusal names
// the file the two met on.
func TestReseedWorktree_ACollisionLeavesTheCopyAsItWas(t *testing.T) {
	repo := reseedRepo(t)
	other, err := addWorktree(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer removeWorktree(other.repoTop, other.dir)
	mine, err := addWorktree(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer removeWorktree(mine.repoTop, mine.dir)

	writeInto(t, other.root, "main.go", strings.Replace(reseedBase, "var x = 0", "var x = 1", 1))
	landed, err := worktreePatch(other.dir)
	if err != nil {
		t.Fatal(err)
	}
	mineText := strings.Replace(reseedBase, "var x = 0", "var x = 2", 1)
	writeInto(t, mine.root, "main.go", mineText)
	head, _ := gitOutput(mine.dir, "rev-parse", "HEAD")

	err = reseedWorktree(mine.dir, landed)
	var clash *ReseedCollision
	if !errors.As(err, &clash) {
		t.Fatalf("a patch over the writer's own line should be refused as a collision, got %v", err)
	}
	if len(clash.Files) != 1 || clash.Files[0] != "main.go" {
		t.Fatalf("the collision should name main.go, got %v", clash.Files)
	}
	if got := readFrom(t, mine.root, "main.go"); got != mineText {
		t.Fatalf("the copy was changed by a refused reseed:\n%s", got)
	}
	if after, _ := gitOutput(mine.dir, "rev-parse", "HEAD"); after != head {
		t.Fatalf("the copy's base moved under a refused reseed: %s → %s", head, after)
	}
}

// landingWriters scripts two writers against one repository. writer-1 edits
// main.go's first variable and answers. writer-2 writes in its first round
// and then waits, inside that round, for writer-1's patch to land — so the
// landing arrives while writer-2 is between rounds, exactly as it does when
// one writer of a fan-out finishes before another. What writer-2 does after
// that is the test's.
type landingWriters struct {
	landed chan struct{}
	// second is writer-2's first-round write, and third its second-round
	// write where it has one; each is handed the root it works in.
	second, third func(root string)

	mu       sync.Mutex
	requests map[string][][]provider.Message
	roots    map[string]string
}

func (w *landingWriters) factory() EnvFactory {
	return func(ctx context.Context, spec Spec) (Env, error) {
		w.mu.Lock()
		w.roots[spec.Name] = spec.Root
		w.mu.Unlock()
		round := 0
		calls := 0
		stream := func(msgs []provider.Message, _ string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
			w.mu.Lock()
			w.requests[spec.Name] = append(w.requests[spec.Name], append([]provider.Message(nil), msgs...))
			w.mu.Unlock()
			round++
			ch := make(chan provider.StreamEvent, 2)
			writes := 1
			if spec.Name == "writer-2" && w.third != nil {
				writes = 2
			}
			if round <= writes {
				ch <- provider.StreamEvent{ToolCalls: []provider.ToolCall{
					{ID: spec.Name + "-w", Name: "write_file", Arguments: `{"path":"main.go"}`},
				}}
			} else {
				ch <- provider.StreamEvent{Token: spec.Name + " done"}
				ch <- provider.StreamEvent{Done: true}
			}
			close(ch)
			return ch, func() {}, nil
		}
		return Env{
			SystemPrompt: "sys",
			Stream:       stream,
			Executor: func(string, json.RawMessage) (string, error) {
				calls++
				switch {
				case spec.Name == "writer-1":
					put(spec.Root, "main.go", strings.Replace(reseedBase, "var x = 0", "var x = 1", 1))
				case calls == 1:
					w.second(spec.Root)
					<-w.landed
				default:
					w.third(spec.Root)
				}
				return "written", nil
			},
		}, nil
	}
}

// asked is what writer-2's n-th request carried, as one string.
func (w *landingWriters) asked(n int) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	reqs := w.requests["writer-2"]
	if n >= len(reqs) {
		return ""
	}
	var b strings.Builder
	for _, m := range reqs[n] {
		b.WriteString(m.Content + "\n")
	}
	return b.String()
}

// runLanding runs both writers: writer-1's patch is approved, and writer-2's
// is answered with approve2, the card itself handed back for the test to
// read.
func runLanding(t *testing.T, w *landingWriters, repo string, approve2 bool) (*Supervisor, chan *Ask) {
	t.Helper()
	w.landed = make(chan struct{})
	w.requests = map[string][][]provider.Message{}
	w.roots = map[string]string{}
	ctx, cancel := context.WithCancel(context.Background())
	sup := New(ctx, Options{Root: repo, NewEnv: w.factory()})
	t.Cleanup(sup.Close)
	t.Cleanup(cancel)
	second := make(chan *Ask, 1)
	go func() {
		for {
			select {
			case ev := <-sup.Events():
				switch {
				case ev.Kind == EventAsk && ev.Ask.Agent == "writer-1":
					ev.Ask.Respond(true)
				case ev.Kind == EventAsk:
					second <- ev.Ask
					ev.Ask.Respond(approve2)
				case ev.Kind == EventPatch && ev.Patch.Agent == "writer-1":
					close(w.landed)
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	execTool(t, sup, SpawnToolName, `{"role":"writer","task":"set x"}`)
	execTool(t, sup, SpawnToolName, `{"role":"writer","task":"set y"}`)
	return sup, second
}

// A writer between rounds when another's patch lands is moved onto the tree
// with that patch in it at its next boundary, and the patch it then hands
// back is its own work alone, which applies to the checkout as it now
// stands.
func TestALandingReseedsALiveWriterAtItsNextBoundary(t *testing.T) {
	repo := reseedRepo(t)
	var sawLanded atomic.Bool
	w := &landingWriters{
		second: func(root string) { put(root, "two.go", "package main\n\nvar two = 2\n") },
		third: func(root string) {
			// Read from the copy as it now stands and written back, the way a
			// model edits a file: the reseed is what puts writer-1's line in
			// what it reads.
			text := take(root, "main.go")
			sawLanded.Store(strings.Contains(text, "var x = 1"))
			put(root, "main.go", strings.Replace(text, "var y = 0", "var y = 2", 1))
		},
	}
	sup, second := runLanding(t, w, repo, true)

	ask := <-second
	waitState(t, sup, "writer-2", StateDone)
	if !sawLanded.Load() {
		t.Fatal("writer-2's round after the landing should have read writer-1's line in its copy")
	}
	var added []string
	for _, h := range ask.Hunks {
		for _, l := range h.Lines {
			if l.Kind == diff.Add {
				added = append(added, l.Text)
			}
		}
	}
	joined := strings.Join(added, "\n")
	if strings.Contains(joined, "var x = 1") || strings.Contains(joined, "var x = 0") {
		t.Fatalf("writer-2's patch carries writer-1's lines:\n%s", joined)
	}
	if !strings.Contains(joined, "var y = 2") || !strings.Contains(joined, "var two = 2") {
		t.Fatalf("writer-2's patch is missing its own work:\n%s", joined)
	}
	main := readFrom(t, repo, "main.go")
	if !strings.Contains(main, "var x = 1") || !strings.Contains(main, "var y = 2") {
		t.Fatalf("both patches should stand in the checkout:\n%s", main)
	}
	st := statusOf(t, sup, "writer-2")
	if st.Reseeds != 1 {
		t.Fatalf("writer-2 should count one reseed, got %d", st.Reseeds)
	}
	if !transcriptHas(sup.Transcript("writer-2"), EntrySystem, "writer-1's landed patch carried into this copy") {
		t.Fatal("the lane should say the landed patch was carried in")
	}
	if strings.Contains(w.asked(1), "could not be carried") {
		t.Fatal("a reseed that carried should be silent to the writer's model")
	}
}

// A landing that meets a writer's own work leaves the writer's copy exactly
// as it was and steers the writer instead, from the landing, naming what
// landed and where the two met — at the same boundary.
func TestALandingThatCollidesSteersTheWriterAndLeavesItsCopy(t *testing.T) {
	repo := reseedRepo(t)
	mine := strings.Replace(reseedBase, "var x = 0", "var x = 2", 1)
	w := &landingWriters{second: func(root string) { put(root, "main.go", mine) }}
	// writer-2's own patch then conflicts with the landing over the same
	// line, so it is kept rather than put on a card.
	sup, _ := runLanding(t, w, repo, false)
	waitState(t, sup, "writer-2", StateDone)

	st := statusOf(t, sup, "writer-2")
	if !st.PatchKept {
		t.Fatal("writer-2's patch over the landed line should be kept")
	}
	if st.SteerFrom != SteerFromLanding {
		t.Fatalf("the steer should come from the landing, got %q", st.SteerFrom)
	}
	if st.Reseeds != 0 || st.LaneSteers != 0 || st.ParentSteers != 0 {
		t.Fatalf("a collision is no reseed and nobody's steer: %+v", st)
	}
	// The round after the boundary carried the steer, worded the way the
	// writer is told it.
	next := w.asked(1)
	for _, want := range []string{"writer-1's patch has landed in the real checkout (main.go)", "collides with your copy over main.go"} {
		if !strings.Contains(next, want) {
			t.Fatalf("the steer should say %q; the next request carried:\n%s", want, next)
		}
	}
	w.mu.Lock()
	root := w.roots["writer-2"]
	w.mu.Unlock()
	if got := readFrom(t, root, "main.go"); got != mine {
		t.Fatalf("a collision changed the writer's copy:\n%s", got)
	}
	patch, err := worktreePatch(filepath.Clean(root))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(patch, "+var x = 2") || !strings.Contains(patch, "-var x = 0") {
		t.Fatalf("the copy's base should not have moved; its patch is:\n%s", patch)
	}
	if !strings.Contains(rosterLine(st), "steered from landing") {
		t.Fatalf("the roster should name the landing: %s", rosterLine(st))
	}
}

// A child whose copy is being moved past a landed patch is parked at the
// hold's boundary for the moment it takes, and reads as held and reseeding
// while it is: the rail draws where the child has stopped, and why.
func TestAReseedingChildReadsHeldAndReseeding(t *testing.T) {
	c := &child{name: "writer-2", reseeding: "writer-1", state: StateRunning}
	st := c.status()
	if !st.Held || !st.Reseeding || !strings.Contains(st.Detail, "reseeding") {
		t.Fatalf("a child being reseeded should read held and reseeding: %+v", st)
	}
}
