package route

import "example.com/router/normalize"

// Table maps canonical paths to handler names.
type Table struct {
	routes map[string]string
}

func New() *Table { return &Table{routes: map[string]string{}} }

// Add registers a handler for a path. The path is canonicalised on the way
// in, so a route registered as "/users/" and one as "/users" are the same.
func (t *Table) Add(path, handler string) {
	t.routes[normalize.Path(path)] = handler
}

// Match is the handler for a request path, or "" when nothing is registered.
func (t *Table) Match(path string) string {
	return t.routes[normalize.Path(path)]
}
