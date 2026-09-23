package chat

// Routes over the backlog screen (program_routes_test.go says what these are
// for).

import (
	"testing"

	"github.com/rfizzle/shhh/internal/todo"
)

// The sprint tab of the backlog screen, under a spend ceiling: `/todo` opens
// the screen, the tab key reaches the sprint, and the head says what the set
// has spent against the ceiling the loop's checkpoint carries.
func TestProgram_TheSprintTabSaysTheSpendAgainstItsCeiling(t *testing.T) {
	item := func(title string) string {
		return "---\ntitle: " + title + "\nsize: S\n---\n## Tests\n- true\n"
	}
	root := fixtureDir(t, map[string]string{
		".shhh/todo/cache-ttl.md":     item("Give the cache a lifetime"),
		".shhh/todo/cache-evict.md":   item("Evict what the lifetime expired"),
		".shhh/todo/sprint.md":        "---\nname: caching\n---\nMake an entry's lifetime mean something.\n\n## Items\n- cache-ttl\n- cache-evict\n",
		".shhh/todo/.run/sprint.json": `{"session":"s","turns":9,"cost":4.1,"cap_cents":2000}`,
	})
	m, _ := scriptedSession(programTurn{text: "nothing to do"})
	m = m.WithWorkspace(root).WithTodos(Todos{
		Profile: todo.BuiltinCode(), Root: root,
		Manage: func([]string) string { return "" },
	})
	tm := runProgramAt(t, m, 110, 40)

	send(tm, "/todo")
	waitForText(t, tm, "cache-evict")
	programPress(t, tm, "tab")
	waitForText(t, tm, "spend $4.10 of $20")

	frameHas(t, finalFrame(t, tm), "9 turns · spend $4.10 of $20", "next · cache-ttl")
}

// The sprint tab under a sprint working three items at once: the head lists
// the lanes with the step each item is at, read off the loop's checkpoint,
// and each lane's row says its step too.
func TestProgram_TheSprintTabListsTheLanesAtWork(t *testing.T) {
	item := func(title string) string {
		return "---\ntitle: " + title + "\nsize: S\n---\n## Tests\n- true\n"
	}
	root := fixtureDir(t, map[string]string{
		".shhh/todo/cache-ttl.md":    item("Give the cache a lifetime"),
		".shhh/todo/cache-evict.md":  item("Evict what the lifetime expired"),
		".shhh/todo/cache-report.md": item("Say what the cache holds"),
		".shhh/todo/sprint.md":       "---\nname: caching\n---\nMake an entry's lifetime mean something.\n\n## Items\n- cache-ttl\n- cache-evict\n- cache-report\n",
		".shhh/todo/.run/sprint.json": `{"session":"s","parallel":3,"turns":5,"cost":1.2,"lanes":[` +
			`{"slug":"cache-ttl","stage":"implement"},{"slug":"cache-evict","stage":"verify"},{"slug":"cache-report","stage":"research"}]}`,
	})
	m, _ := scriptedSession(programTurn{text: "nothing to do"})
	m = m.WithWorkspace(root).WithTodos(Todos{
		Profile: todo.BuiltinCode(), Root: root,
		Manage: func([]string) string { return "" },
	})
	tm := runProgramAt(t, m, 130, 40)

	send(tm, "/todo")
	waitForText(t, tm, "cache-evict")
	programPress(t, tm, "tab")
	waitForText(t, tm, "working · 3 at once")

	frameHas(t, finalFrame(t, tm), "cache-ttl", "implement", "cache-evict", "verify", "cache-report", "research")
}
