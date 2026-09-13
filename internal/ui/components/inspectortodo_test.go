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
	})
}
