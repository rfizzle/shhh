package chat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/todo/run"
)

// `/todo run --all --parallel N` hands the sprint to the unattended runner
// with the answers the person gave, and the session goes on following it.
func TestTodoSprint_ParallelHandsTheSprintToTheRunner(t *testing.T) {
	m, _ := runModel(t)
	var asked []string
	m.todos.Parallel = func(args []string) (string, error) {
		asked = args
		return "/r/.shhh/todo/.run/sprint.log", nil
	}
	m.input.SetValue("/todo run --all --parallel 3 --max 5 --cost-cap 2000 --no-commit")
	updated, cmd := m.submitInput()
	m = updated.(Model)

	if got := strings.Join(asked, " "); got != "--all --parallel 3 --max 5 --cost-cap 2000 --no-commit" {
		t.Fatalf("the runner is handed the sprint's answers, got %q", got)
	}
	if m.todoRunner.state != nil || m.working() {
		t.Fatal("the session itself works nothing; the runner does")
	}
	note := m.transcript[len(m.transcript)-1].text
	if !strings.Contains(note, "up to 3 items at once") || !strings.Contains(note, "sprint.log") {
		t.Fatalf("the note says what was started and where its lines go: %q", note)
	}
	if cmd == nil {
		t.Fatal("the session follows the sprint's checkpoint on a tick")
	}
}

// A parallel sprint is another process's, so the session refuses to start a
// second sprint over it, and /todo stop asks it to stop rather than ending
// its checkpoint underneath it.
func TestTodoSprint_AParallelSprintIsAskedToStop(t *testing.T) {
	m, root := runModel(t)
	sp := run.StartSprint("runner", "", 0, false)
	sp.Parallel = 2
	sp.Lanes = []run.SprintLane{{Slug: "do-it", Stage: run.StageImplement}}
	must(t, sp.Save(root))

	m.input.SetValue("/todo run --all")
	updated, _ := m.submitInput()
	m = updated.(Model)
	if m.todoRunner.state != nil || !strings.Contains(m.transcript[len(m.transcript)-1].text, "several items at once is going") {
		t.Fatalf("a second sprint over the running one is refused: %q", m.transcript[len(m.transcript)-1].text)
	}

	m.input.SetValue("/todo stop")
	updated, _ = m.submitInput()
	m = updated.(Model)
	if !run.StopRequested(root) {
		t.Fatal("the stop is asked for through the checkpoint's directory")
	}
	if _, live := run.Live(root); !live {
		t.Fatal("the session does not end another process's sprint underneath it")
	}
}

// The rail and the board read a parallel sprint's lanes off its checkpoint:
// the rail counts them, and the board lists each with its step.
func TestTodoSprint_TheRailAndTheBoardReadTheLanes(t *testing.T) {
	m, root := runModel(t)
	dir := todo.Dir(root)
	for _, slug := range []string{"b-two", "c-three"} {
		must(t, os.WriteFile(filepath.Join(dir, slug+".md"), []byte("---\ntitle: "+slug+"\nsize: S\n---\n"), 0o644))
	}
	must(t, os.WriteFile(filepath.Join(dir, "sprint.md"),
		[]byte("---\nname: lanes\n---\nThree at once.\n\n## Items\n- do-it\n- b-two\n- c-three\n"), 0o644))
	sp := run.StartSprint("runner", "", 0, false)
	sp.Parallel, sp.Turns, sp.Cost, sp.CapCents = 3, 4, 1, 2000
	sp.Lanes = []run.SprintLane{
		{Slug: "do-it", Stage: run.StageImplement, Turns: 2, Cost: 0.5},
		{Slug: "b-two", Stage: run.StageVerify},
		{Slug: "c-three", Stage: run.StageResearch},
	}
	must(t, sp.Save(root))
	m.reloadTodos()

	if block := m.inspectorTodo(); block == nil || block.SprintItem != "3 items" {
		t.Fatalf("the rail counts the lanes: %+v", block)
	}
	board := m.sprintBoard()
	if board == nil || len(board.Lanes) != 3 || board.Lanes[1].Slug != "b-two" || board.Lanes[1].Stage != "verify" {
		t.Fatalf("the board lists the lanes with their steps: %+v", board)
	}
	if board.Spend != "6 turns · "+run.SpendWords(1.5, 2000) {
		t.Fatalf("the spend adds what the lanes have spent so far: %q", board.Spend)
	}
	for _, row := range board.Rows {
		if row.Slug == "c-three" && row.Note != "research" {
			t.Fatalf("a lane's row says its step: %q", row.Note)
		}
	}
}

// --parallel is a sprint's answer and a count.
func TestParseTodoRunArgs_Parallel(t *testing.T) {
	if opt, ok := parseTodoRunArgs([]string{"--all", "--parallel=4"}); !ok || opt.parallel != 4 {
		t.Fatalf("parsed %+v/%v", opt, ok)
	}
	for _, args := range [][]string{{"--parallel", "2"}, {"--all", "--parallel", "0"}, {"--all", "--parallel"}} {
		if _, ok := parseTodoRunArgs(args); ok {
			t.Errorf("%v should be refused", args)
		}
	}
}

// A session opened beside a parallel sprint it did not start follows it: the
// re-read is armed from the first frame, and a sprint over the whole ready
// list, which has no sprint file, still has a board behind the rail's row.
func TestTodoSprint_ASessionOpenedBesideLanesFollowsThem(t *testing.T) {
	m, root := runModel(t)
	dir := todo.Dir(root)
	for _, slug := range []string{"b-two", "c-three"} {
		must(t, os.WriteFile(filepath.Join(dir, slug+".md"), []byte("---\ntitle: "+slug+"\nsize: S\n---\n"), 0o644))
	}
	sp := run.StartSprint("elsewhere", "", 0, false)
	sp.Parallel = 3
	sp.Lanes = []run.SprintLane{
		{Slug: "do-it", Stage: run.StageImplement},
		{Slug: "b-two", Stage: run.StageVerify},
		{Slug: "c-three", Stage: run.StageResearch},
	}
	must(t, sp.Save(root))
	m = m.WithTodos(m.todos)

	if !m.todoRunner.following || m.Init() == nil {
		t.Fatal("a session opened beside a running parallel sprint arms the re-read")
	}
	if block := m.inspectorTodo(); block == nil || block.SprintItem != "3 items" {
		t.Fatalf("the rail counts the lanes: %+v", block)
	}

	updated, _ := m.openTodoScreen()
	m = updated.(Model)
	if m.backlog == nil || m.backlog.Board == nil {
		t.Fatal("a sprint over the ready list has a board")
	}
	board := m.backlog.Board
	if board.Name != "" || board.Goal != "" || board.Total != 0 || len(board.Lanes) != 3 || len(board.Rows) != 3 {
		t.Fatalf("the board is drawn from the checkpoint alone: %+v", board)
	}
	if board.Rows[1].Slug != "b-two" || board.Rows[1].Note != "verify" {
		t.Fatalf("each lane's item is a row saying its step: %+v", board.Rows[1])
	}
	m.backlog.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if view := m.backlog.View(130); !strings.Contains(view, "working · 3 at once") {
		t.Fatalf("the sprint tab is reachable and lists the lanes:\n%s", view)
	}

	// The sprint ends: the next re-read finds nothing going and stops.
	sp.Ended = run.SprintStopped
	must(t, sp.Save(root))
	updated, cmd := m.followLanes()
	m = updated.(Model)
	if cmd != nil || m.todoRunner.following {
		t.Fatal("the re-read stops when the sprint ends")
	}
}

// A backlog event that finds a parallel sprint going arms the re-read once:
// a second event while one is pending does not start a second chain.
func TestTodoSprint_ABacklogEventArmsTheReReadOnce(t *testing.T) {
	m, root := runModel(t)
	if m.todoRunner.following {
		t.Fatal("nothing is followed while no sprint is going")
	}
	sp := run.StartSprint("elsewhere", "", 0, false)
	sp.Parallel = 2
	sp.Lanes = []run.SprintLane{{Slug: "do-it", Stage: run.StageImplement}}
	must(t, sp.Save(root))

	updated, cmd := m.todoCommand([]string{"/todo", "list"})
	m = updated.(Model)
	if cmd == nil || !m.todoRunner.following {
		t.Fatal("a /todo event arms the re-read")
	}
	if _, again := followingLanes(m, nil); again != nil {
		t.Fatal("an armed re-read is not armed twice")
	}
}
