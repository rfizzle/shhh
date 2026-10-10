package subagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/tools"
)

// A child's write to a path git runs is refused by the standing rule in the
// session's one sentence, in every mode and without a card: nothing is put to
// the parent and nothing is written.
func TestChildWriteToAGitProgramPathIsRefusedByRule(t *testing.T) {
	for _, mode := range []agent.Mode{agent.ModeManual, agent.ModeAcceptEdits, agent.ModeAuto} {
		repo := initTestRepo(t)
		env := &scriptedEnv{
			steps: []streamStep{
				{calls: []provider.ToolCall{{ID: "w1", Name: "write_file",
					Arguments: `{"path":".git/hooks/pre-commit","content":"#!/bin/sh\n"}`}}},
				{text: "done"},
			},
			gated: map[string]bool{"write_file": true},
		}
		sup := New(context.Background(), Options{Root: repo, NewEnv: env.factory()})
		t.Cleanup(sup.Close)
		sup.SetParentMode(mode)
		execTool(t, sup, SpawnToolName, `{"role":"writer","task":"write the hook"}`)
		for ev := range sup.Events() {
			if ev.Kind == EventAsk {
				t.Fatalf("%s: a path no answer can allow was put to the parent: %+v", mode, ev.Ask)
			}
			if ev.Kind == EventDone {
				break
			}
		}
		if _, err := os.Stat(filepath.Join(repo, ".git", "hooks", "pre-commit")); err == nil {
			t.Fatalf("%s: the hook was written", mode)
		}
		refused := false
		for _, e := range sup.Transcript("writer-1") {
			if e.Kind == EntrySystem && strings.HasPrefix(e.Text, "Refused: ") {
				refused = true
				if e.Result != tools.GitProgramPathResult {
					t.Fatalf("%s: the child was told %q", mode, e.Result)
				}
			}
		}
		if !refused {
			t.Fatalf("%s: no refusal row in the child's transcript", mode)
		}
	}
}
