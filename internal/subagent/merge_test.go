package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/provider"
)

// mergeBase is the committed file the merge tests work on: a line the
// checkout moves ("// c") one unchanged line away from the line a writer
// changes ("var y = 0"). Close enough that the writer's hunk carries the
// moved line as context, so its patch no longer applies as written; far
// enough apart that a line merge has no conflict to report.
const mergeBase = "package main\n\nvar x = 0\n\n// a\n// b\n// c\n\nvar y = 0\n"

func mergeRepo(t *testing.T) string {
	t.Helper()
	repo := initTestRepo(t)
	writeInto(t, repo, "main.go", mergeBase)
	if _, err := runGit(repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-am", "base"); err != nil {
		t.Fatal(err)
	}
	return repo
}

// moved is the checkout's change to mergeBase, and ours the writer's.
func moved(text string) string { return strings.Replace(text, "// c", "// C", 1) }
func ours(text string) string  { return strings.Replace(text, "var y = 0", "var y = 2", 1) }

// landLane lands one change to main.go through a copy of its own — the
// ordinary landing a lane or a second writer makes — so the checkout moves
// the way it does when another patch lands.
func landLane(t *testing.T, repo string, edit func(string) string) {
	t.Helper()
	lane, err := NewWorktree(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lane.Remove()
	writeInto(t, lane.Root(), "main.go", edit(readFrom(t, lane.Root(), "main.go")))
	if _, err := lane.Land(); err != nil {
		t.Fatalf("the lane should land: %v", err)
	}
}

// hunkText is a card's diff as one string, context included, each line
// prefixed the way a patch prefixes it.
func hunkText(hunks []diff.Hunk) string {
	var b strings.Builder
	for _, h := range hunks {
		for _, l := range h.Lines {
			switch l.Kind {
			case diff.Add:
				b.WriteString("+")
			case diff.Del:
				b.WriteString("-")
			default:
				b.WriteString(" ")
			}
			b.WriteString(l.Text + "\n")
		}
	}
	return b.String()
}

// A patch whose context the checkout moved is merged over the checkout, and
// the merge is a patch against the checkout as it now stands: it applies
// plainly there and carries the writer's change and nothing of the
// checkout's.
func TestMergeWorktree_APatchOverAMovedFileMergesAgainstTheCheckout(t *testing.T) {
	repo := mergeRepo(t)
	h, err := addWorktree(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer removeWorktree(h.repoTop, h.dir)
	writeInto(t, h.root, "main.go", ours(mergeBase))
	patch, err := worktreePatch(h.dir)
	if err != nil {
		t.Fatal(err)
	}
	writeInto(t, repo, "main.go", moved(mergeBase))
	if checkPatch(repo, patch) == nil {
		t.Fatal("the fixture should move the checkout under the writer's hunk")
	}

	m, err := mergeWorktree(h.dir, repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Conflicts) != 0 || len(m.Moved) != 1 || m.Moved[0] != "main.go" {
		t.Fatalf("one moved file and no conflict, got moved %v conflicts %v", m.Moved, m.Conflicts)
	}
	if !strings.Contains(m.Patch, "+var y = 2") || !strings.Contains(m.Patch, " // C") || strings.Contains(m.Patch, "-// c") {
		t.Fatalf("the merge should be the writer's change over the checkout's text:\n%s", m.Patch)
	}
	if got := readFrom(t, repo, "main.go"); got != moved(mergeBase) {
		t.Fatalf("merging must not touch the checkout:\n%s", got)
	}
	if err := applyPatch(repo, m.Patch); err != nil {
		t.Fatalf("the merge should apply plainly to the checkout: %v", err)
	}
	if got := readFrom(t, repo, "main.go"); got != ours(moved(mergeBase)) {
		t.Fatalf("both changes should stand:\n%s", got)
	}
}

// The files the checkout did not move come through the merge as the writer
// left them, whatever the change was: a new file in a new directory, a
// deletion, and a mode flipped with no byte changed.
func TestMergeWorktree_UnmovedFilesAreTheWritersOutright(t *testing.T) {
	repo := mergeRepo(t)
	writeInto(t, repo, "gone.txt", "bye\n")
	writeInto(t, repo, "run.sh", "#!/bin/sh\n")
	if _, err := runGit(repo, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "more"); err != nil {
		t.Fatal(err)
	}
	h, err := addWorktree(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer removeWorktree(h.repoTop, h.dir)
	writeInto(t, h.root, "main.go", ours(mergeBase))
	writeInto(t, h.root, "pkg/deep/new.go", "package deep\n")
	if err := os.Remove(filepath.Join(h.root, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(h.root, "run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := worktreePatch(h.dir); err != nil {
		t.Fatal(err)
	}
	writeInto(t, repo, "main.go", moved(mergeBase))

	m, err := mergeWorktree(h.dir, repo)
	if err != nil || len(m.Conflicts) != 0 || len(m.Moved) != 1 {
		t.Fatalf("one moved file and no conflict, got moved %v conflicts %v err %v", m.Moved, m.Conflicts, err)
	}
	if err := applyPatch(repo, m.Patch); err != nil {
		t.Fatalf("the merge should apply plainly:\n%s\n%v", m.Patch, err)
	}
	if got := readFrom(t, repo, "main.go"); got != ours(moved(mergeBase)) {
		t.Fatalf("main.go should hold both changes:\n%s", got)
	}
	if got := readFrom(t, repo, "pkg/deep/new.go"); got != "package deep\n" {
		t.Fatalf("the new file should land: %q", got)
	}
	if _, err := os.Stat(filepath.Join(repo, "gone.txt")); !os.IsNotExist(err) {
		t.Fatalf("the deleted file should be gone: %v", err)
	}
	if info, err := os.Stat(filepath.Join(repo, "run.sh")); err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("run.sh should be executable: %v %v", info, err)
	}
}

// Two changes to the same line are a conflict region, and a conflict is not
// merged: no patch, the file named, the checkout untouched.
func TestMergeWorktree_TheSameLinesAreAConflictAndNothingIsMerged(t *testing.T) {
	repo := mergeRepo(t)
	h, err := addWorktree(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer removeWorktree(h.repoTop, h.dir)
	writeInto(t, h.root, "main.go", ours(mergeBase))
	writeInto(t, h.root, "other.go", "package main\n")
	if _, err := worktreePatch(h.dir); err != nil {
		t.Fatal(err)
	}
	theirs := strings.Replace(mergeBase, "var y = 0", "var y = 1", 1)
	writeInto(t, repo, "main.go", theirs)

	m, err := mergeWorktree(h.dir, repo)
	if err != nil {
		t.Fatal(err)
	}
	if m.Patch != "" || len(m.Conflicts) != 1 || m.Conflicts[0] != "main.go" {
		t.Fatalf("a conflict should name main.go and merge nothing, got conflicts %v patch %q", m.Conflicts, m.Patch)
	}
	if got := readFrom(t, repo, "main.go"); got != theirs {
		t.Fatalf("a conflict must leave the checkout as it was:\n%s", got)
	}
}

// A lane landing after another has moved the same file lands the merge, and a
// lane over the same lines lands nothing and says where.
func TestWorktreeLand_MergesOverAnEarlierLaneAndRefusesAConflict(t *testing.T) {
	repo := mergeRepo(t)
	late, err := NewWorktree(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer late.Remove()
	clash, err := NewWorktree(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clash.Remove()
	writeInto(t, late.Root(), "main.go", ours(mergeBase))
	writeInto(t, clash.Root(), "main.go", strings.Replace(mergeBase, "var y = 0", "var y = 3", 1))

	landLane(t, repo, moved)
	files, err := late.Land()
	if err != nil || len(files) != 1 || files[0] != "main.go" {
		t.Fatalf("the later lane should land its merge over main.go, got %v, %v", files, err)
	}
	want := ours(moved(mergeBase))
	if got := readFrom(t, repo, "main.go"); got != want {
		t.Fatalf("both lanes should stand:\n%s", got)
	}

	_, err = clash.Land()
	var conflict *MergeConflict
	if !errors.As(err, &conflict) || len(conflict.Files) != 1 || conflict.Files[0] != "main.go" {
		t.Fatalf("a lane over the same line should be a conflict naming main.go, got %v", err)
	}
	if got := readFrom(t, repo, "main.go"); got != want {
		t.Fatalf("a conflicting lane must land nothing:\n%s", got)
	}
}

// mergeWriter is one scripted writer: write in its one tool round, then in
// the round that answers, run answering first — which is after the last
// round boundary it will ever reach.
type mergeWriter struct {
	write     func(root string)
	answering func()
}

func mergeFactory(ws map[string]mergeWriter) EnvFactory {
	return func(ctx context.Context, spec Spec) (Env, error) {
		w := ws[spec.Name]
		round := 0
		return Env{
			SystemPrompt: "sys",
			Stream: func([]provider.Message, string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
				round++
				ch := make(chan provider.StreamEvent, 2)
				if round == 1 {
					ch <- provider.StreamEvent{ToolCalls: []provider.ToolCall{
						{ID: spec.Name + "-w", Name: "write_file", Arguments: `{"path":"main.go"}`},
					}}
				} else {
					if w.answering != nil {
						w.answering()
					}
					ch <- provider.StreamEvent{Token: spec.Name + " done"}
					ch <- provider.StreamEvent{Done: true}
				}
				close(ch)
				return ch, func() {}, nil
			},
			Executor: func(string, json.RawMessage) (string, error) {
				w.write(spec.Root)
				return "written", nil
			},
		}, nil
	}
}

// nextPatchAsk waits for the named writer's next card, approving every other
// writer's on the way and closing landed when writer-1's patch lands.
func nextPatchAsk(t *testing.T, sup *Supervisor, name string, landed chan struct{}) *Ask {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-sup.Events():
			if !ok {
				t.Fatalf("the supervisor closed before %s's card", name)
			}
			switch {
			case ev.Kind == EventAsk && ev.Ask.Agent == name:
				return ev.Ask
			case ev.Kind == EventAsk:
				ev.Ask.Respond(true)
			case ev.Kind == EventPatch && landed != nil && ev.Patch.Agent == "writer-1":
				close(landed)
				landed = nil
			}
		case <-deadline:
			t.Fatalf("no card from %s", name)
			return nil
		}
	}
}

// drainEvents answers nothing and reads everything, so a child emitting its
// last updates is never blocked on a test that has stopped listening.
func drainEvents(t *testing.T, sup *Supervisor) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		for {
			select {
			case _, ok := <-sup.Events():
				if !ok {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
}

// A patch over a file the person edited elsewhere while the writer worked is
// put to them merged: the card's diff is against their file as it stands,
// it says so, and nothing is written until they approve.
func TestAPatchTheCheckoutMovedUnderIsMergedAndTheMergeIsWhatIsShown(t *testing.T) {
	repo := mergeRepo(t)
	sup := New(context.Background(), Options{Root: repo, NewEnv: mergeFactory(map[string]mergeWriter{
		"writer-1": {
			write:     func(root string) { put(root, "main.go", ours(mergeBase)) },
			answering: func() { put(repo, "main.go", moved(mergeBase)) },
		},
	})})
	t.Cleanup(sup.Close)
	execTool(t, sup, SpawnToolName, `{"role":"writer","task":"set y"}`)

	ask := nextPatchAsk(t, sup, "writer-1", nil)
	if len(ask.Merged) != 1 || ask.Merged[0] != "main.go" {
		t.Fatalf("the card should say it was merged over main.go, got %v", ask.Merged)
	}
	shown := hunkText(ask.Hunks)
	if !strings.Contains(shown, "+var y = 2") || !strings.Contains(shown, " // C") || strings.Contains(shown, "// c") {
		t.Fatalf("the card should show the merge over the checkout, not the writer's original:\n%s", shown)
	}
	if got := readFrom(t, repo, "main.go"); got != moved(mergeBase) {
		t.Fatalf("nothing lands before the answer:\n%s", got)
	}
	ask.Respond(true)
	drainEvents(t, sup)
	waitState(t, sup, "writer-1", StateDone)
	if got := readFrom(t, repo, "main.go"); got != ours(moved(mergeBase)) {
		t.Fatalf("the approved merge should be what landed:\n%s", got)
	}
	if !transcriptHas(sup.Transcript("writer-1"), EntrySystem, "merged over main.go, which moved since it started") {
		t.Fatal("the landing note should say the patch was merged and over which files")
	}
}

// A writer whose last round was an answer never reaches another round
// boundary, so a landing queued during that round is never carried into its
// copy and its patch is written against a tree the landing moved. The merge
// is what covers it: the card is the merge over the landed change.
func TestAWriterThatAnsweredPastALandingIsMergedOverIt(t *testing.T) {
	repo := mergeRepo(t)
	landed := make(chan struct{})
	// writer-1 answers, and so lands, only once writer-2 is in its answering
	// round — past the last boundary a landing could be carried in at, in a
	// copy taken before the landing — and writer-2 answers only once writer-1
	// has landed.
	wrote1, wrote2 := make(chan struct{}), make(chan struct{})
	sup := New(context.Background(), Options{Root: repo, NewEnv: mergeFactory(map[string]mergeWriter{
		"writer-1": {
			write:     func(root string) { put(root, "main.go", moved(mergeBase)); close(wrote1) },
			answering: func() { <-wrote2 },
		},
		"writer-2": {
			write:     func(root string) { put(root, "main.go", ours(mergeBase)) },
			answering: func() { close(wrote2); <-landed },
		},
	})})
	t.Cleanup(sup.Close)
	// One copy at a time: the second is taken once the first has been
	// written in.
	execTool(t, sup, SpawnToolName, `{"role":"writer","task":"move c"}`)
	<-wrote1
	execTool(t, sup, SpawnToolName, `{"role":"writer","task":"set y"}`)

	ask := nextPatchAsk(t, sup, "writer-2", landed)
	if len(ask.Merged) != 1 || ask.Merged[0] != "main.go" {
		t.Fatalf("writer-2's card should be merged over main.go, got %v", ask.Merged)
	}
	if shown := hunkText(ask.Hunks); !strings.Contains(shown, " // C") || !strings.Contains(shown, "+var y = 2") {
		t.Fatalf("writer-2's card should be its change over writer-1's landed text:\n%s", shown)
	}
	ask.Respond(true)
	drainEvents(t, sup)
	waitState(t, sup, "writer-2", StateDone)
	if got := readFrom(t, repo, "main.go"); got != ours(moved(mergeBase)) {
		t.Fatalf("both writers' changes should stand:\n%s", got)
	}
	if st := statusOf(t, sup, "writer-2"); st.Reseeds != 0 {
		t.Fatalf("writer-2 answered before any boundary could carry the landing, yet counts %d reseeds", st.Reseeds)
	}
}

// A landing over the same lines as a writer's own is a conflict, and a
// conflict is not settled by the merge: no card, the patch kept for review
// from the row, the file named, and the checkout exactly as the landing
// left it.
func TestAPatchThatConflictsIsKeptWithTheFilesNamed(t *testing.T) {
	repo := mergeRepo(t)
	landed := make(chan struct{})
	wrote1, wrote2 := make(chan struct{}), make(chan struct{})
	first := strings.Replace(mergeBase, "var y = 0", "var y = 1", 1)
	sup := New(context.Background(), Options{Root: repo, NewEnv: mergeFactory(map[string]mergeWriter{
		"writer-1": {
			write:     func(root string) { put(root, "main.go", first); close(wrote1) },
			answering: func() { <-wrote2 },
		},
		"writer-2": {
			write:     func(root string) { put(root, "main.go", ours(mergeBase)) },
			answering: func() { close(wrote2); <-landed },
		},
	})})
	t.Cleanup(sup.Close)
	execTool(t, sup, SpawnToolName, `{"role":"writer","task":"y is 1"}`)
	<-wrote1
	execTool(t, sup, SpawnToolName, `{"role":"writer","task":"y is 2"}`)

	// writer-1's card is approved on the way; writer-2 raises none.
	done := make(chan struct{})
	go func(landed chan struct{}) {
		defer close(done)
		for ev := range sup.Events() {
			switch {
			case ev.Kind == EventAsk && ev.Ask.Agent == "writer-2":
				t.Errorf("a conflicting patch should not be put on a card: %s", ev.Ask.Title)
				ev.Ask.Respond(false)
			case ev.Kind == EventAsk:
				ev.Ask.Respond(true)
			case ev.Kind == EventPatch && landed != nil:
				close(landed)
				landed = nil
			}
		}
	}(landed)
	waitState(t, sup, "writer-2", StateDone)
	if st := statusOf(t, sup, "writer-2"); !st.PatchKept {
		t.Fatal("a conflicting patch should be kept for review from the row")
	}
	if !transcriptHas(sup.Transcript("writer-2"), EntrySystem, "main.go moved since it started and the changes conflict there") {
		t.Fatal("the note should name the conflicting file")
	}
	if got := readFrom(t, repo, "main.go"); got != first {
		t.Fatalf("a conflict must leave the checkout as the landing left it:\n%s", got)
	}
	sup.Close()
	<-done
}

// A patch approved and then invalidated by a second landing before it could
// land is not forced and not applied stale: it goes back to the card, merged
// over the checkout as it now stands, and the checkout is untouched until
// that card is answered.
func TestAnApprovedPatchALandingInvalidatedGoesBackToTheCard(t *testing.T) {
	repo := mergeRepo(t)
	sup := New(context.Background(), Options{Root: repo, NewEnv: mergeFactory(map[string]mergeWriter{
		"writer-1": {write: func(root string) { put(root, "main.go", ours(mergeBase)) }},
	})})
	t.Cleanup(sup.Close)
	execTool(t, sup, SpawnToolName, `{"role":"writer","task":"set y"}`)

	first := nextPatchAsk(t, sup, "writer-1", nil)
	if len(first.Merged) != 0 {
		t.Fatalf("a patch that applies as written is not merged, got %v", first.Merged)
	}
	landLane(t, repo, moved)
	first.Respond(true)

	again := nextPatchAsk(t, sup, "writer-1", nil)
	if again == first || len(again.Merged) != 1 || again.Merged[0] != "main.go" {
		t.Fatalf("the approval should come back as a new card merged over main.go, got %v", again.Merged)
	}
	if got := readFrom(t, repo, "main.go"); got != moved(mergeBase) {
		t.Fatalf("the stale approval must not have landed anything:\n%s", got)
	}
	if shown := hunkText(again.Hunks); !strings.Contains(shown, " // C") {
		t.Fatalf("the card should be the merge over the second landing:\n%s", shown)
	}
	again.Respond(true)
	drainEvents(t, sup)
	waitState(t, sup, "writer-1", StateDone)
	if got := readFrom(t, repo, "main.go"); got != ours(moved(mergeBase)) {
		t.Fatalf("the merge approved on the second card should be what landed:\n%s", got)
	}
}
