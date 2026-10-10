package cli

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/todo/run"
)

var laneFileIn = regexp.MustCompile(`(?m)^File: (.+)$`)

// tickItem writes an item with two acceptance criteria and its test.
func tickItem(t *testing.T, root, slug string) string {
	t.Helper()
	body := "---\ntitle: " + slug + "\nsize: S\n---\n## Acceptance Criteria\n- [ ] first criterion\n- [ ] second criterion\n\n## Tests\n- true\n"
	path := filepath.Join(todo.Dir(root), slug+".md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The item block a lane's stage is given names a copy of the item inside the
// lane's copy of the checkout, the stage ticks that, and the runner puts the
// tick on the item line by line before it is archived: the checkout's file
// is never the stage's to write.
func TestTodoRunHeadless_ALanesTicksReachTheItem(t *testing.T) {
	root := todoRepo(t)
	tickItem(t, root, "a-one")
	var mu sync.Mutex
	var files []string
	d, out := laneDriverFor(t, root, &laneAnswers{})
	inner := d.turn
	d.turn = func(ctx context.Context, at time.Time, dir string, step run.Step) (todoTurn, error) {
		if m := laneFileIn.FindStringSubmatch(step.Prompt); m != nil {
			mu.Lock()
			files = append(files, m[1])
			mu.Unlock()
			if step.Stage == run.StageImplement {
				data, err := os.ReadFile(m[1])
				if err != nil {
					t.Errorf("the copy the stage was told to tick is not readable: %v", err)
				} else if err := os.WriteFile(m[1], []byte(strings.Replace(string(data), "- [ ] first criterion", "- [x] first criterion", 1)), 0o644); err != nil {
					t.Errorf("the copy could not be ticked: %v", err)
				}
			}
		}
		return inner(ctx, at, dir, step)
	}

	if blocked := d.sprintParallel(context.Background(), 0, 2); blocked {
		t.Fatalf("the sprint should have finished:\n%s", out.String())
	}
	it, ok := todo.Load(todo.BuiltinCode(), root).Find("a-one")
	if !ok || !it.Archived {
		t.Fatalf("a-one should be archived:\n%s", out.String())
	}
	data, err := os.ReadFile(it.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "- [x] first criterion\n") || !strings.Contains(string(data), "- [ ] second criterion\n") {
		t.Fatalf("only the first criterion should be ticked on the archived item:\n%s", data)
	}
	if len(files) == 0 {
		t.Fatalf("no stage was given an item block:\n%s", out.String())
	}
	for _, f := range files {
		if !strings.HasSuffix(filepath.ToSlash(f), "/.shhh/todo/.run/a-one.item.md") {
			t.Errorf("the item block names %s, not the lane's copy", f)
		}
	}
}

// A lane writes nothing in the checkout for its ticks: no stage is told the
// checkout's own item file, that file holds no tick at any stage until the
// runner files it, the copy is not made in the checkout, and the commit the
// lane lands carries none.
func TestTodoRunHeadless_ALaneWritesNothingInTheCheckout(t *testing.T) {
	root := todoRepo(t)
	real := tickItem(t, root, "a-one")
	var mu sync.Mutex
	var moved []string
	a := &laneAnswers{hold: func(_ string, stage run.Stage, _ string) {
		got, err := os.ReadFile(real)
		mu.Lock()
		defer mu.Unlock()
		if err != nil || strings.Contains(string(got), "[x]") {
			moved = append(moved, string(stage))
		}
	}}
	d, out := laneDriverFor(t, root, a)
	inner := d.turn
	d.turn = func(ctx context.Context, at time.Time, dir string, step run.Step) (todoTurn, error) {
		if m := laneFileIn.FindStringSubmatch(step.Prompt); m != nil && m[1] == real {
			t.Errorf("the %s stage was told the checkout's own item file", step.Stage)
		}
		return inner(ctx, at, dir, step)
	}
	if blocked := d.sprintParallel(context.Background(), 0, 2); blocked {
		t.Fatalf("the sprint should have finished:\n%s", out.String())
	}
	if len(moved) > 0 {
		t.Errorf("the checkout's item changed during %v", moved)
	}
	if _, err := os.Stat(run.ItemCopyPath(root, "a-one")); err == nil {
		t.Errorf("the copy of the item was made in the checkout itself")
	}
	if names, _ := run.Git(root, "show", "--name-only", "--format=", "HEAD"); strings.Contains(names, ".item.md") {
		t.Errorf("the lane's commit carries its copy of the item:\n%s", names)
	}
}
