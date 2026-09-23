package subagent

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// sumGolden is what the fixture generator writes from a.txt and b.txt: each
// value on a line of its own, far apart, and their sum at the end. Two
// changes to different sources each move their own line and move the sum to
// the same text, so a line merge of two regenerations comes out clean — and
// wrong, because the sum it keeps is neither side's.
func sumGolden(a, b string) string {
	ai, _ := strconv.Atoi(a)
	bi, _ := strconv.Atoi(b)
	return fmt.Sprintf("a=%s\n\n\n\n\nb=%s\n\n\n\n\nsum=%d\n", a, b, ai+bi)
}

// writeSum is the generator itself, run in a tree.
func writeSum(dir string) {
	put(dir, "gen/out.txt", sumGolden(strings.TrimSpace(take(dir, "a.txt")), strings.TrimSpace(take(dir, "b.txt"))))
}

// sumGenerator is the project's declaration as a stub: gen/ is generated
// where declared is set, and a run fails where fail is set. It records every
// tree it was run in.
type sumGenerator struct {
	declared bool
	fail     string
	mu       sync.Mutex
	dirs     []string
}

func (g *sumGenerator) Generated(paths []string) []string {
	if !g.declared {
		return nil
	}
	var out []string
	for _, p := range paths {
		if strings.HasPrefix(p, "gen/") {
			out = append(out, p)
		}
	}
	return out
}

func (g *sumGenerator) Regenerate(_ context.Context, dir string, paths []string) ([]string, error) {
	g.mu.Lock()
	g.dirs = append(g.dirs, dir)
	g.mu.Unlock()
	if g.fail != "" {
		return nil, errors.New(g.fail)
	}
	writeSum(dir)
	return []string{"gen sum"}, nil
}

func (g *sumGenerator) ran() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.dirs...)
}

// sumRepo is a repository holding a.txt, b.txt and the golden they generate.
func sumRepo(t *testing.T) string {
	t.Helper()
	repo := initTestRepo(t)
	writeInto(t, repo, "a.txt", "1")
	writeInto(t, repo, "b.txt", "1")
	writeSum(repo)
	if _, err := runGit(repo, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "base"); err != nil {
		t.Fatal(err)
	}
	return repo
}

// sumLanes lands one lane changing a.txt and one changing b.txt, each having
// regenerated the golden in its own copy, in that order.
func sumLanes(t *testing.T, repo string, gen Regenerator) error {
	t.Helper()
	one, err := NewWorktree(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer one.Remove()
	two, err := NewWorktree(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer two.Remove()
	one.UseGenerators(gen)
	two.UseGenerators(gen)
	put(one.Root(), "a.txt", "2")
	writeSum(one.Root())
	put(two.Root(), "b.txt", "2")
	writeSum(two.Root())
	if _, err := one.Land(); err != nil {
		t.Fatalf("the first lane should land: %v", err)
	}
	_, err = two.Land()
	return err
}

// Two landings that each regenerate the same golden from different sources
// leave the golden the generator writes over both, never the merge of the
// two — and the generator is never run in the checkout itself.
func TestWorktreeLand_AGeneratedFileIsRegeneratedNeverMerged(t *testing.T) {
	repo := sumRepo(t)
	gen := &sumGenerator{declared: true}
	if err := sumLanes(t, repo, gen); err != nil {
		t.Fatalf("the second lane should land: %v", err)
	}
	if got, want := take(repo, "gen/out.txt"), sumGolden("2", "2"); got != want {
		t.Fatalf("the golden should be what the generator writes over both:\n%s\nwant:\n%s", got, want)
	}
	dirs := gen.ran()
	if len(dirs) != 2 {
		t.Fatalf("each landing should run the generator once, ran in %v", dirs)
	}
	for _, d := range dirs {
		if d == repo {
			t.Fatal("the generator ran in the checkout rather than in a copy of it")
		}
	}
}

// A file nobody declared is text: the same two landings merge it three ways,
// clean and wrong, which is what the declaration exists to prevent.
func TestWorktreeLand_AnUnlistedGeneratedFileStillMergesAsText(t *testing.T) {
	repo := sumRepo(t)
	gen := &sumGenerator{}
	if err := sumLanes(t, repo, gen); err != nil {
		t.Fatalf("the second lane should land its merge: %v", err)
	}
	merged := strings.Replace(sumGolden("2", "2"), "sum=4", "sum=3", 1)
	if got := take(repo, "gen/out.txt"); got != merged {
		t.Fatalf("an undeclared file should merge as text:\n%s", got)
	}
	if dirs := gen.ran(); len(dirs) != 0 {
		t.Fatalf("no generator should run for an undeclared file, ran in %v", dirs)
	}
}

// A generator that fails lands nothing and leaves the checkout as it was.
func TestWorktreeLand_AFailingGeneratorLeavesTheCheckoutUntouched(t *testing.T) {
	repo := sumRepo(t)
	lane, err := NewWorktree(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lane.Remove()
	lane.UseGenerators(&sumGenerator{declared: true, fail: "gen sum failed (exit 1)"})
	put(lane.Root(), "a.txt", "2")
	writeSum(lane.Root())
	if _, err := lane.Land(); err == nil || !strings.Contains(err.Error(), "gen sum failed") {
		t.Fatalf("the landing should fail with the generator, got %v", err)
	}
	if take(repo, "a.txt") != "1" || take(repo, "gen/out.txt") != sumGolden("1", "1") {
		t.Fatal("a failed regeneration must leave the checkout as it was")
	}
}

// A landing carried into a live writer's copy regenerates the golden there
// rather than applying its hunks — which here would collide, since both
// sides moved the sum — and the copy's base takes the landed bytes, so the
// writer's patch is its own work over them.
func TestReseedWorktree_AGeneratedFileIsRegeneratedInTheCopy(t *testing.T) {
	repo := sumRepo(t)
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
	put(other.root, "a.txt", "2")
	writeSum(other.root)
	landed, err := worktreePatch(other.dir)
	if err != nil {
		t.Fatal(err)
	}
	put(mine.root, "b.txt", "2")
	writeSum(mine.root)

	var clash *ReseedCollision
	if _, err := reseedWorktree(context.Background(), mine.dir, landed, nil); !errors.As(err, &clash) {
		t.Fatalf("applied as hunks the golden collides, got %v", err)
	}
	gen := &sumGenerator{declared: true}
	regen, err := reseedWorktree(context.Background(), mine.dir, landed, gen)
	if err != nil || regen.failed != nil || len(regen.ran) != 1 {
		t.Fatalf("the landing should carry with the golden regenerated: %+v, %v", regen, err)
	}
	if dirs := gen.ran(); len(dirs) != 1 || dirs[0] != mine.dir {
		t.Fatalf("the generator should run in the copy, ran in %v", dirs)
	}
	if got := take(mine.root, "gen/out.txt"); got != sumGolden("2", "2") {
		t.Fatalf("the copy's golden should be generated over both:\n%s", got)
	}
	if base, _ := gitOutput(mine.dir, "show", "HEAD:gen/out.txt"); base != sumGolden("2", "1") {
		t.Fatalf("the base should hold the landed golden:\n%s", base)
	}
	patch, err := worktreePatch(mine.dir)
	if err != nil {
		t.Fatal(err)
	}
	if files := PatchFiles(patch); len(files) != 2 || strings.Contains(patch, "+a=2") {
		t.Fatalf("the writer's patch should be its own work over the landing, got %v:\n%s", files, patch)
	}
}

// A writer's patch that touched the golden is put on the card regenerated:
// the hunks are what the generator wrote over the checkout, not the bytes the
// writer left, and the card names the command.
func TestAPatchThatTouchedAGeneratedFileIsShownRegenerated(t *testing.T) {
	repo := sumRepo(t)
	gen := &sumGenerator{declared: true}
	sup := New(context.Background(), Options{Root: repo, Generators: gen, NewEnv: mergeFactory(map[string]mergeWriter{
		"writer-1": {write: func(root string) {
			put(root, "a.txt", "2")
			// A golden no generator would write.
			put(root, "gen/out.txt", strings.Replace(sumGolden("2", "1"), "sum=3", "sum=99", 1))
		}},
	})})
	t.Cleanup(sup.Close)
	execTool(t, sup, SpawnToolName, `{"role":"writer","task":"set a"}`)

	ask := nextPatchAsk(t, sup, "writer-1", nil)
	if len(ask.Regenerated) != 1 || ask.Regenerated[0] != "gen sum" {
		t.Fatalf("the card should name the generator, got %v", ask.Regenerated)
	}
	if shown := hunkText(ask.Hunks); !strings.Contains(shown, "+sum=3") || strings.Contains(shown, "sum=99") {
		t.Fatalf("the card should show the generator's golden, not the writer's:\n%s", shown)
	}
	if take(repo, "a.txt") != "1" {
		t.Fatal("nothing lands before the answer")
	}
	ask.Respond(true)
	drainEvents(t, sup)
	waitState(t, sup, "writer-1", StateDone)
	if got := take(repo, "gen/out.txt"); got != sumGolden("2", "1") {
		t.Fatalf("the regenerated golden should be what landed:\n%s", got)
	}
	if !transcriptHas(sup.Transcript("writer-1"), EntrySystem, "regenerated by gen sum rather than merged") {
		t.Fatal("the landing note should say the generated file was regenerated")
	}
}

// A generator that fails does not land and does not hide: the card carries
// the change without the generated file and says why, with the output named,
// and the checkout is untouched.
func TestAFailingGeneratorGoesToTheCardWithItsOutput(t *testing.T) {
	repo := sumRepo(t)
	gen := &sumGenerator{declared: true, fail: "gen sum failed (exit 1); full output ev-1\nboom"}
	sup := New(context.Background(), Options{Root: repo, Generators: gen, NewEnv: mergeFactory(map[string]mergeWriter{
		"writer-1": {write: func(root string) { put(root, "a.txt", "2"); writeSum(root) }},
	})})
	t.Cleanup(sup.Close)
	execTool(t, sup, SpawnToolName, `{"role":"writer","task":"set a"}`)

	ask := nextPatchAsk(t, sup, "writer-1", nil)
	if len(ask.Warnings) != 1 || !strings.HasPrefix(ask.Warnings[0], "gen/out.txt left as your checkout has them: gen sum failed (exit 1); full output ev-1") {
		t.Fatalf("the card should carry the failure, got %q", ask.Warnings)
	}
	if len(ask.Files) != 1 || ask.Files[0] != "a.txt" || len(ask.Regenerated) != 0 {
		t.Fatalf("the card should hold the source change alone, got %v / %v", ask.Files, ask.Regenerated)
	}
	if take(repo, "a.txt") != "1" || take(repo, "gen/out.txt") != sumGolden("1", "1") {
		t.Fatal("a failed regeneration must leave the checkout untouched")
	}
	ask.Respond(false)
	drainEvents(t, sup)
	waitState(t, sup, "writer-1", StateDone)
}

// A writer's patch kept for review from its row is kept without its generated
// files: that review lands by the plain apply, with no copy left to
// regenerate in, so the writer's own bytes for one would land as written.
func TestAKeptPatchLeavesItsGeneratedFilesToTheGenerator(t *testing.T) {
	repo := sumRepo(t)
	h, err := addWorktree(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer removeWorktree(h.repoTop, h.dir)
	put(h.root, "a.txt", "2")
	put(h.root, "gen/out.txt", "hand-merged\n")
	patch, err := worktreePatch(h.dir)
	if err != nil {
		t.Fatal(err)
	}
	c := &child{}
	note := c.keepWriterPatch(patch, &sumGenerator{declared: true})
	if c.kept == nil || strings.Contains(c.kept.patch, "gen/out.txt") || !strings.Contains(c.kept.patch, "a.txt") {
		t.Fatalf("the kept patch should hold a.txt and not the generated file:\n%+v", c.kept)
	}
	if !strings.Contains(note, "gen/out.txt are generated and left to their generator") {
		t.Fatalf("the note should say the generated file was left out: %q", note)
	}
	undeclared := &child{}
	undeclared.keepWriterPatch(patch, nil)
	if !strings.Contains(undeclared.kept.patch, "gen/out.txt") {
		t.Fatal("with nothing declared the kept patch is the writer's whole patch")
	}
}
