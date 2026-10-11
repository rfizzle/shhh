package components

import "testing"

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
	}}
	assertFoldCounts(t, r, []string{
		"H M rail-todo-block", "H L add-todo-runner", "M M todo-add-extraction",
	}, 4)
}
