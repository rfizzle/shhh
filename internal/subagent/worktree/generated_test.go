package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
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
	if _, err := RunGit(repo, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := RunGit(repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "base"); err != nil {
		t.Fatal(err)
	}
	return repo
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
	defer RemoveWorktree(other.RepoTop, other.Dir)
	mine, err := addWorktree(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer RemoveWorktree(mine.RepoTop, mine.Dir)
	put(other.Root, "a.txt", "2")
	writeSum(other.Root)
	landed, err := WorktreePatch(other.Dir)
	if err != nil {
		t.Fatal(err)
	}
	put(mine.Root, "b.txt", "2")
	writeSum(mine.Root)

	var clash *ReseedCollision
	if _, err := ReseedWorktree(context.Background(), mine.Dir, landed, nil); !errors.As(err, &clash) {
		t.Fatalf("applied as hunks the golden collides, got %v", err)
	}
	gen := &sumGenerator{declared: true}
	regen, err := ReseedWorktree(context.Background(), mine.Dir, landed, gen)
	if err != nil || regen.Failed != nil || len(regen.Ran) != 1 {
		t.Fatalf("the landing should carry with the golden regenerated: %+v, %v", regen, err)
	}
	if dirs := gen.ran(); len(dirs) != 1 || dirs[0] != mine.Dir {
		t.Fatalf("the generator should run in the copy, ran in %v", dirs)
	}
	if got := take(mine.Root, "gen/out.txt"); got != sumGolden("2", "2") {
		t.Fatalf("the copy's golden should be generated over both:\n%s", got)
	}
	if base, _ := GitOutput(mine.Dir, "show", "HEAD:gen/out.txt"); base != sumGolden("2", "1") {
		t.Fatalf("the base should hold the landed golden:\n%s", base)
	}
	patch, err := WorktreePatch(mine.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if files := PatchFiles(patch); len(files) != 2 || strings.Contains(patch, "+a=2") {
		t.Fatalf("the writer's patch should be its own work over the landing, got %v:\n%s", files, patch)
	}
}
