package components

import "testing"

// TestInspectorTodo_TheHintIsShedNotFolded: the same promise PLAN's hint row
// makes. The row naming the command that prints the whole backlog is chrome,
// not a backlog item, so a rail short of height takes it first and takes it
// outright rather than folding it behind a count of items.
func TestInspectorTodo_TheHintIsShedNotFolded(t *testing.T) {
	r := InspectorRail{Todo: &InspectorTodo{
		Open: 3,
		Rows: []InspectorTodoRow{
			{Slug: "rail-todo-block", Priority: "H", Grade: "M", State: TodoRunning, Note: "review 1/2"},
			{Slug: "add-todo-runner", Priority: "H", Grade: "L", State: TodoWaiting, Note: "needs rail-todo-block"},
			{Slug: "todo-add-extraction", Priority: "M", Grade: "M", State: TodoReady},
		},
		Hint: "/todo for the whole backlog",
	}}
	// The rows are named by priority, grade and slug together: one row's note
	// names another row's slug, and a bare slug would read as present while
	// the row carrying it is folded away.
	assertHintIsShed(t, r, "/todo for the whole backlog", []string{
		"H M rail-todo-block", "H L add-todo-runner", "M M todo-add-extraction",
	}, 0)
}

// TestInspectorTodo_TheHostsCountJoinsTheMarker: the host's `… N more` row
// is a count of items, not an item. It is never taken on its own, and the
// first item the rail folds takes it along, so the block states one count —
// the host's items and the rail's together — rather than two stacked.
func TestInspectorTodo_TheHostsCountJoinsTheMarker(t *testing.T) {
	r := InspectorRail{Todo: &InspectorTodo{
		Open: 7,
		Rows: []InspectorTodoRow{
			{Slug: "rail-todo-block", Priority: "H", Grade: "M", State: TodoRunning, Note: "review 1/2"},
			{Slug: "add-todo-runner", Priority: "H", Grade: "L", State: TodoWaiting, Note: "needs rail-todo-block"},
			{Slug: "todo-add-extraction", Priority: "M", Grade: "M", State: TodoReady},
		},
		More: 4,
		Hint: "/todo for the whole backlog",
	}}
	assertHintIsShed(t, r, "/todo for the whole backlog", []string{
		"H M rail-todo-block", "H L add-todo-runner", "M M todo-add-extraction",
	}, 4)
}
