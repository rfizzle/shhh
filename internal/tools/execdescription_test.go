package tools

import (
	"strings"
	"testing"
)

// The command tool says, in the description both variants share, that a
// server or a watcher is refused with a redirect to the process tool.
func TestExecCommandTool_SaysItRefusesAServer(t *testing.T) {
	for name, d := range map[string]string{
		"with edits":    ExecCommandTool().Description,
		"without edits": ExecCommandToolNoEdits().Description,
	} {
		if !strings.Contains(d, "refused with a redirect there") {
			t.Errorf("%s: the description does not say a server is refused: %s", name, d)
		}
	}
}
