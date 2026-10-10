package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/todo/run"
)

// checkpointSprint is a checkout of three items on disjoint paths, a quality
// suite "cp" whose one check runs script with the log's path as its first
// argument, committed so every lane's copy carries it, and a driver
// configured to run the suite every `every` landings.
func checkpointSprint(t *testing.T, every int, script string) (*todoDriver, *lockedBuf, string, string) {
	t.Helper()
	root := todoRepo(t)
	for _, slug := range []string{"a-one", "b-two", "c-three"} {
		laneItem(t, root, slug, slug+".go")
	}
	d, _ := laneDriverFor(t, root, &laneAnswers{})
	buf := &lockedBuf{}
	d.out = buf
	log := filepath.Join(t.TempDir(), "checkpoint.log")
	cfg := fmt.Sprintf(`{"suites":{"default":{"checks":[{"name":"ok","exe":"true"}]},"cp":{"checks":[{"name":"scenes","exe":"sh","args":["-c",%q,"sh",%q]}]}}}`, script, log)
	writeQualityConfig(t, root, cfg)
	for _, args := range [][]string{{"add", ".shhh/quality.json"}, {"commit", "-q", "-m", "suite"}} {
		if out, code := run.Git(root, args...); code != 0 {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	d.gate = &quality.Runner{Workspace: root}
	d.checkpointEvery, d.checkpointSuite = every, "cp"
	return d, buf, root, log
}

func checkpointLog(t *testing.T, log string) []string {
	t.Helper()
	data, err := os.ReadFile(log)
	if err != nil {
		return nil
	}
	return strings.Fields(string(data))
}

// After the Nth landing the suite runs on the checkout, with the Nth
// commit on it and not the (N+1)th, and the sprint goes on when it passes;
// the log says so in the words the board's row reads.
func TestTodoRunHeadless_ACheckpointRunsEveryNLandings(t *testing.T) {
	d, buf, root, log := checkpointSprint(t, 2, `echo $(git rev-list --count HEAD) >> "$1"; echo "94/94 scenes passed"`)
	base, _ := run.Git(root, "rev-list", "--count", "HEAD")

	if blocked := d.sprintParallel(context.Background(), 0, 1); blocked {
		t.Fatalf("the sprint should have finished:\n%s", buf.String())
	}
	var want int
	if _, err := fmt.Sscan(strings.TrimSpace(base), &want); err != nil {
		t.Fatal(err)
	}
	got := checkpointLog(t, log)
	if len(got) != 1 || got[0] != fmt.Sprint(want+2) {
		t.Fatalf("the suite should have run once, with two landings on the checkout (%d commits): %v\n%s", want+2, got, buf.String())
	}
	if !strings.Contains(buf.String(), "checkpoint · cp · 94/94\n") {
		t.Fatalf("the log should say the checkpoint passed:\n%s", buf.String())
	}
	for _, slug := range []string{"a-one", "b-two", "c-three"} {
		if it, ok := todo.Load(todo.BuiltinCode(), root).Find(slug); !ok || !it.Archived {
			t.Fatalf("%s should have landed after the checkpoint passed:\n%s", slug, buf.String())
		}
	}
}

// A check that fails is run again, alone; a pass on that run is a pass, and
// the sprint goes on.
func TestTodoRunHeadless_ACheckpointRerunsAFailedCheckAlone(t *testing.T) {
	d, buf, _, log := checkpointSprint(t, 3, `echo x >> "$1"; [ "$(wc -l < "$1")" -ge 2 ]`)
	if blocked := d.sprintParallel(context.Background(), 0, 1); blocked {
		t.Fatalf("a check that passed on its rerun should not end the sprint:\n%s", buf.String())
	}
	if got := checkpointLog(t, log); len(got) != 2 {
		t.Fatalf("the check should have run twice, the second time alone: %v", got)
	}
	if !strings.Contains(buf.String(), "checkpoint · cp · 1/1\n") {
		t.Fatalf("a check with no count of its own counts as one:\n%s", buf.String())
	}
}

// A check that fails twice ends the sprint blocked, naming the checkpoint,
// the check and the evidence of the failing run; the items not yet taken are
// left open.
func TestTodoRunHeadless_AFailedCheckpointEndsTheSprint(t *testing.T) {
	d, buf, root, log := checkpointSprint(t, 1, `echo x >> "$1"; echo "drift in the draw"; exit 1`)
	if blocked := d.sprintParallel(context.Background(), 0, 1); !blocked {
		t.Fatalf("the sprint should have ended blocked:\n%s", buf.String())
	}
	if got := checkpointLog(t, log); len(got) != 2 {
		t.Fatalf("the check should have run, then run again alone: %v", got)
	}
	out := buf.String()
	if !strings.Contains(out, "checkpoint · cp blocked — check scenes failed (evidence ") {
		t.Fatalf("the ending should name the checkpoint, the check and its evidence:\n%s", out)
	}
	if !strings.Contains(out, "sprint over — 1 item done · blocked: checkpoint · cp blocked") {
		t.Fatalf("the sprint should end on the checkpoint:\n%s", out)
	}
	store := todo.Load(todo.BuiltinCode(), root)
	if it, _ := store.Find("a-one"); !it.Archived {
		t.Fatalf("the landing before the checkpoint stands:\n%s", out)
	}
	if it, _ := store.Find("b-two"); it.Archived || it.Status != todo.StatusOpen {
		t.Fatalf("no item is taken after the checkpoint failed: %+v", it)
	}
}

// A checkpoint asked for with no suite named is no checkpoint passed: the
// sprint halts on it.
func TestTodoRunHeadless_ACheckpointWithoutASuiteHaltsTheSprint(t *testing.T) {
	d, buf, _, _ := checkpointSprint(t, 1, "true")
	d.checkpointSuite = ""
	if blocked := d.sprintParallel(context.Background(), 0, 1); !blocked {
		t.Fatalf("the sprint should have ended blocked:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "todo.checkpoint_suite names no suite") {
		t.Fatalf("the ending should say why:\n%s", buf.String())
	}
}
