package chat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/tools"
)

// The model's write to a path git runs is refused by a standing rule; the
// person's own save from the edit pane to the same path is theirs, as a
// command typed after `!` is, and lands.
func TestEdit_APersonMayWriteWhatTheModelMayNot(t *testing.T) {
	m, path := editSessionAt(t, 130)
	ws := filepath.Dir(path)
	hook := filepath.Join(ws, ".git", "hooks", "pre-commit")
	if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	args, _ := json.Marshal(map[string]string{"path": hook, "content": "#!/bin/sh\nexit 1\n"})
	d, result, code := m.StandingAnswer(provider.ToolCall{ID: "c1", Name: tools.WriteFileName, Arguments: string(args)})
	if d != agent.Deny || result != tools.GitProgramPathResult || code != observe.ReasonSafety {
		t.Fatalf("the model's write: %v %q %q", d, result, code)
	}

	m = sendText(t, m, "/edit .git/hooks/pre-commit")
	if m.state != stateEditor {
		t.Fatalf("/edit did not open the pane: state %d, last note %q", m.state, lastNote(m))
	}
	m = typeInto(t, m, "# mine\n")
	m = pressKeys(t, m, editSave)
	data, err := os.ReadFile(hook)
	if err != nil || string(data) != "# mine\n#!/bin/sh\n" {
		t.Fatalf("the person's save did not land: %q (%v)", data, err)
	}
}
