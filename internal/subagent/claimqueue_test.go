package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
)

// claimWriters scripts writers over one repository that each make one write
// and answer. What a writer writes, and what it read of its copy before it
// did, is the test's; writer-1 waits for release before writing, so the
// writers queued behind it can be looked at while it holds its claim.
type claimWriters struct {
	release chan struct{}
	write   map[string]func(root string) string

	mu     sync.Mutex
	read   map[string]string
	order  []string
	copies map[string]string
}

func newClaimWriters(repo string) *claimWriters {
	return &claimWriters{
		release: make(chan struct{}),
		read:    map[string]string{},
		copies:  map[string]string{},
		write: map[string]func(root string) string{
			"writer-1": func(root string) string {
				text := take(root, "main.go")
				put(root, "main.go", strings.Replace(text, "var x = 0", "var x = 1", 1))
				return text
			},
			"writer-2": func(root string) string {
				text := take(root, "main.go")
				put(root, "main.go", strings.Replace(text, "var y = 0", "var y = 2", 1))
				return text
			},
			"writer-3": func(root string) string {
				text := take(root, "main.go")
				put(root, "main.go", text+"\nvar z = 3\n")
				return text
			},
			"writer-4": func(root string) string {
				put(root, "other.go", "package main\n\nvar other = 4\n")
				return ""
			},
		},
	}
}

func (w *claimWriters) factory(repo string) EnvFactory {
	return func(ctx context.Context, spec Spec) (Env, error) {
		if spec.Root != repo {
			w.mu.Lock()
			w.copies[spec.Name] = spec.Root
			w.mu.Unlock()
		} else {
			// The environment a spawn builds before it is admitted takes
			// real time, and it is built after the claim is checked: a
			// writer handed over beside this one has that long to check its
			// own claim and find this one absent.
			time.Sleep(20 * time.Millisecond)
		}
		round := 0
		stream := func([]provider.Message, string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
			round++
			ch := make(chan provider.StreamEvent, 2)
			if round == 1 {
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
				if spec.Name == "writer-1" {
					<-w.release
				}
				read := w.write[spec.Name](spec.Root)
				w.mu.Lock()
				w.read[spec.Name] = read
				w.order = append(w.order, spec.Name)
				w.mu.Unlock()
				return "written", nil
			},
		}, nil
	}
}

func (w *claimWriters) copied(name string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, ok := w.copies[name]
	return ok
}

// startClaimWriters is a supervisor over repo with two slots, whose patches
// are all approved as they arrive, and the order they land in.
func startClaimWriters(t *testing.T, repo string, w *claimWriters) (*Supervisor, func() []string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	sup := New(ctx, Options{Root: repo, MaxConcurrent: 2, NewEnv: w.factory(repo)})
	t.Cleanup(sup.Close)
	t.Cleanup(cancel)
	var mu sync.Mutex
	var landed []string
	go func() {
		for {
			select {
			case ev := <-sup.Events():
				switch ev.Kind {
				case EventAsk:
					ev.Ask.Respond(true)
				case EventPatch:
					mu.Lock()
					landed = append(landed, ev.Patch.Agent)
					mu.Unlock()
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	return sup, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), landed...)
	}
}

// Three writers handed over at once over one file run one after another, in
// the order they were spawned: the two queued behind the first hold neither
// a slot nor a copy while they wait — a fourth writer over another file takes
// the second slot and runs — and each is seeded from the checkout as it stands
// when its turn comes, with the patches ahead of it already landed.
func TestWritersQueuedBehindAClaimRunInOrderEachFromTheLastLanding(t *testing.T) {
	repo := reseedRepo(t)
	w := newClaimWriters(repo)
	sup, landed := startClaimWriters(t, repo, w)

	for _, name := range []string{"writer-1", "writer-2", "writer-3"} {
		out := execTool(t, sup, SpawnToolName, `{"role":"writer","task":"edit main","name":"`+name+`","paths":["main.go"],"wait_for_claim":true}`)
		if name != "writer-1" && !strings.Contains(out, "It has not started") {
			t.Fatalf("a queued spawn should say it has not started and behind whom:\n%s", out)
		}
	}
	execTool(t, sup, SpawnToolName, `{"role":"writer","task":"edit other","name":"writer-4","paths":["other.go"]}`)

	waitFor(t, func() bool {
		st2, _ := sup.Get("writer-2")
		st3, _ := sup.Get("writer-3")
		return st2.WaitsOn == "writer-1" && st3.WaitsOn == "writer-2"
	})
	for _, name := range []string{"writer-2", "writer-3"} {
		st := statusOf(t, sup, name)
		if st.State != StateQueued || !strings.HasPrefix(st.Detail, "queued behind ") {
			t.Fatalf("%s should read queued behind a writer, got %s %q", name, st.State, st.Detail)
		}
	}
	// Two slots: writer-1 holds one, and writer-4 can only finish on the
	// other if the queued writers hold none.
	waitState(t, sup, "writer-4", StateDone)
	if w.copied("writer-2") || w.copied("writer-3") {
		t.Fatal("a writer queued behind a claim should have no copy of the tree yet")
	}

	close(w.release)
	waitState(t, sup, "writer-3", StateDone)

	got := landed()
	var mains []string
	for _, name := range got {
		if name != "writer-4" {
			mains = append(mains, name)
		}
	}
	if strings.Join(mains, ",") != "writer-1,writer-2,writer-3" {
		t.Fatalf("the writers over main.go should land in spawn order, got %v", got)
	}
	w.mu.Lock()
	read2, read3 := w.read["writer-2"], w.read["writer-3"]
	w.mu.Unlock()
	if !strings.Contains(read2, "var x = 1") {
		t.Fatalf("writer-2's copy should have been seeded with writer-1's landed line:\n%s", read2)
	}
	if !strings.Contains(read3, "var x = 1") || !strings.Contains(read3, "var y = 2") {
		t.Fatalf("writer-3's copy should have been seeded with both landings:\n%s", read3)
	}
	main := readFrom(t, repo, "main.go")
	for _, line := range []string{"var x = 1", "var y = 2", "var z = 3"} {
		if !strings.Contains(main, line) {
			t.Fatalf("every writer's patch should stand in the checkout; missing %q:\n%s", line, main)
		}
	}
}

// Without wait_for_claim an overlapping claim is refused as it always was:
// the queue is something an orchestrator asks for, never what it gets by
// default.
func TestAnOverlappingClaimIsStillRefusedByDefault(t *testing.T) {
	repo := reseedRepo(t)
	w := newClaimWriters(repo)
	sup, _ := startClaimWriters(t, repo, w)
	t.Cleanup(func() { close(w.release) })

	execTool(t, sup, SpawnToolName, `{"role":"writer","task":"edit main","name":"writer-1","paths":["main.go"]}`)
	exec := sup.WrapExecutor("", func(string, json.RawMessage) (string, error) { return "", nil })
	_, err := exec(SpawnToolName, json.RawMessage(`{"role":"writer","task":"edit main","name":"writer-2","paths":["main.go"]}`))
	if err == nil || !strings.Contains(err.Error(), "writer-1 already claims main.go") {
		t.Fatalf("an overlapping claim should be refused by default, got %v", err)
	}
	if _, ok := sup.Get("writer-2"); ok {
		t.Fatal("a refused spawn should leave no agent behind")
	}
	// A role that claims nothing has nothing to wait behind.
	_, err = exec(SpawnToolName, json.RawMessage(`{"role":"researcher","task":"look","wait_for_claim":true}`))
	if err == nil || !strings.Contains(err.Error(), "wait_for_claim applies to agents that change files") {
		t.Fatalf("wait_for_claim on a reader should be refused, got %v", err)
	}
}

// Asking for a queued writer's report does not wait on the writer it is
// queued behind, which the caller did not name; and a kill takes it out of
// the queue as a kill, with no patch and no copy to keep.
func TestAQueuedWriterAnswersAtOnceAndAKillDropsIt(t *testing.T) {
	repo := reseedRepo(t)
	w := newClaimWriters(repo)
	sup, _ := startClaimWriters(t, repo, w)
	t.Cleanup(func() { close(w.release) })

	execTool(t, sup, SpawnToolName, `{"role":"writer","task":"edit main","name":"writer-1","paths":["main.go"]}`)
	execTool(t, sup, SpawnToolName, `{"role":"writer","task":"edit main","name":"writer-2","paths":["main.go"],"wait_for_claim":true}`)
	waitFor(t, func() bool { st, _ := sup.Get("writer-2"); return st.WaitsOn == "writer-1" })

	out := execTool(t, sup, ReportToolName, `{"name":"writer-2"}`)
	if !strings.Contains(out, "queued behind writer-1") || !strings.Contains(out, "It has not started") {
		t.Fatalf("a report on a queued writer should say it has not started and why:\n%s", out)
	}

	if err := sup.Kill("writer-2"); err != nil {
		t.Fatal(err)
	}
	waitState(t, sup, "writer-2", StateFailed)
	st := statusOf(t, sup, "writer-2")
	if st.End != observe.ChildKilled {
		t.Fatalf("a killed queued writer should end killed, got %q", st.End)
	}
	if st.PatchKept || st.Handoff != "" || w.copied("writer-2") {
		t.Fatalf("a killed queued writer has nothing to keep: %+v", st)
	}
	if st1 := statusOf(t, sup, "writer-1"); st1.State == StateDone || st1.State == StateFailed {
		t.Fatalf("killing the queued writer should not touch the one it waited on: %s", st1.State)
	}
}

// A waiting writer and a plain one handed over together over one file never
// both start: whichever takes its place first holds the claim, and the other
// is refused (the plain one) or queued behind it (the waiting one).
func TestAWaitingAndAPlainWriterTogetherNeverBothStart(t *testing.T) {
	repo := reseedRepo(t)
	w := newClaimWriters(repo)
	close(w.release)
	sup, landed := startClaimWriters(t, repo, w)

	spawned := map[string]bool{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for name, wait := range map[string]bool{"writer-1": true, "writer-2": false} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			exec := sup.WrapExecutor("", func(string, json.RawMessage) (string, error) { return "", nil })
			_, err := exec(SpawnToolName, json.RawMessage(fmt.Sprintf(`{"role":"writer","task":"edit main","name":%q,"paths":["main.go"],"wait_for_claim":%t}`, name, wait)))
			mu.Lock()
			spawned[name] = err == nil
			mu.Unlock()
		}()
	}
	wg.Wait()
	if !spawned["writer-1"] {
		t.Fatal("the waiting writer is never refused for its claim")
	}
	if !spawned["writer-2"] {
		return // writer-1 held the claim first and the plain writer was refused
	}
	waitState(t, sup, "writer-1", StateDone)
	waitState(t, sup, "writer-2", StateDone)
	if got := landed(); strings.Join(got, ",") != "writer-2,writer-1" {
		t.Fatalf("a plain writer that was not refused holds the claim first, got %v", got)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if !strings.Contains(w.read["writer-1"], "var y = 2") {
		t.Fatalf("the waiting writer should have started from the plain writer's landing:\n%s", w.read["writer-1"])
	}
}

// A round's calls run at once, so a batch handed over in one round spawns its
// writers concurrently. They still run one at a time over the file they
// share, in the order they took, each from the tree the one before it left.
func TestWritersHandedOverInOneRoundStillQueue(t *testing.T) {
	repo := reseedRepo(t)
	w := newClaimWriters(repo)
	close(w.release)
	sup, landed := startClaimWriters(t, repo, w)

	names := []string{"writer-1", "writer-2", "writer-3"}
	var wg sync.WaitGroup
	for _, name := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			exec := sup.WrapExecutor("", func(string, json.RawMessage) (string, error) { return "", nil })
			if _, err := exec(SpawnToolName, json.RawMessage(`{"role":"writer","task":"edit main","name":"`+name+`","paths":["main.go"],"wait_for_claim":true}`)); err != nil {
				t.Errorf("%s: %v", name, err)
			}
		}()
	}
	wg.Wait()
	for _, name := range names {
		waitState(t, sup, name, StateDone)
	}

	marks := map[string]string{"writer-1": "var x = 1", "writer-2": "var y = 2", "writer-3": "var z = 3"}
	order := landed()
	if len(order) != 3 {
		t.Fatalf("every writer should land, got %v", order)
	}
	var spawned []string
	for _, st := range sup.Snapshot() {
		spawned = append(spawned, st.Name)
	}
	if strings.Join(order, ",") != strings.Join(spawned, ",") {
		t.Fatalf("the writers should land in the order they were spawned: landed %v, spawned %v", order, spawned)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for i, name := range order {
		for _, before := range order[:i] {
			if !strings.Contains(w.read[name], marks[before]) {
				t.Fatalf("%s should have started from %s's landed line:\n%s", name, before, w.read[name])
			}
		}
	}
}
