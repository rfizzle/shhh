package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
)

// A child's write that adds a credential shape is put to the person in every
// mode that would have run it unasked, with the finding on the routed ask
// (docs/capabilities/approvals-and-safety.md#a-write-that-adds-a-secret-is-always-asked).
func TestRoutedApproval_AChildsWriteWithASecretAsks(t *testing.T) {
	const key = "sk-ant-api03-" + "AbCdEfGhIjKlMnOpQrStUvWxYz0123456789AbCdEfGhIjKlMnOpQrStUvWxYz0123456789_-AbCdEfGh-AA"
	write := func(mode agent.Mode, content string) *Ask {
		repo := initTestRepo(t)
		env := &scriptedEnv{
			steps: []streamStep{
				{calls: []provider.ToolCall{{ID: "w1", Name: "write_file",
					Arguments: fmt.Sprintf(`{"path":"dev.env","content":%q}`, content)}}},
				{text: "done"},
			},
			gated: map[string]bool{"write_file": true},
		}
		sup := New(context.Background(), Options{Root: repo, NewEnv: env.factory()})
		t.Cleanup(sup.Close)
		sup.SetParentMode(mode)
		execTool(t, sup, SpawnToolName, `{"role":"writer","task":"write the file"}`)
		// The child blocks on an ask until it is answered, so its end event
		// without an ask first is the fact that nothing was put to the parent.
		for ev := range sup.Events() {
			switch ev.Kind {
			case EventAsk:
				ev.Ask.Respond(false)
				return ev.Ask
			case EventDone:
				return nil
			}
		}
		return nil
	}
	secretly := "# dev\n# a\nKEY=" + key + "\n"
	for _, mode := range []agent.Mode{agent.ModeManual, agent.ModeAcceptEdits, agent.ModeAuto} {
		ask := write(mode, secretly)
		if ask == nil {
			t.Fatalf("%s: a child's write with a secret was not put to the parent", mode)
		}
		if ask.Kind != AskEdit || ask.Secret != "adds 1 anthropic key · line 3" {
			t.Fatalf("%s: ask = %+v; want the finding by kind and line", mode, ask)
		}
		if b, _ := json.Marshal(ask.Secret); strings.Contains(string(b), "AbCdEfGh") {
			t.Fatalf("%s: the finding carries the value", mode)
		}
	}
	if ask := write(agent.ModeAcceptEdits, "KEY=none\n"); ask != nil {
		t.Fatalf("a clean write should run unasked in accept-edits mode, got %+v", ask)
	}
}
