package process

import (
	"strings"
	"testing"
)

// The description's first sentence is the instruction a model reads first, so
// it says when to use the tool: a server, a watcher, anything read more than
// once or that does not end on its own.
func TestDefinition_LeadsWithWhenToUseIt(t *testing.T) {
	first, _, _ := strings.Cut(Definition().Description, ". ")
	for _, want := range []string{"server", "watcher", "more than once", "does not end on its own"} {
		if !strings.Contains(first, want) {
			t.Errorf("the first sentence lacks %q: %s", want, first)
		}
	}
}
