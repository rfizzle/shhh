package agent

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/rfizzle/shhh/internal/process"
	"github.com/rfizzle/shhh/internal/structural"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/web"
)

// namedTier is the tier and kind the classifier answered by switching on
// tool names, before each tool declared its own. It is kept as the oracle the
// registry is pinned against: moving a tier to the package that defines the
// tool must not change the tier of any tool.
func namedTier(name string, has bool, isStart bool) (Tier, ActionKind) {
	switch {
	case name == tools.ExecCommandName:
		return TierCommand, ActionCommand
	case isStart:
		return TierCommand, ActionCommand
	case name == web.FetchToolName:
		return TierCommand, ActionFetch
	case tools.IsMutating(name):
		return TierWrite, ActionEdit
	case name == structural.GitWriteToolName:
		return TierWrite, ActionEdit
	case has:
		return TierCommand, ActionOther
	}
	return TierRead, ActionOther
}

func TestClassifyCall_EveryRegisteredToolKeepsItsTier(t *testing.T) {
	names := []string{tools.ExecCommandName, web.FetchToolName, web.SearchToolName,
		structural.GitWriteToolName, structural.GitToolName, structural.FdToolName,
		structural.AstGrepToolName, structural.SdToolName, structural.TokeiToolName,
		process.ToolName, "some_server_tool", ""}
	for _, d := range tools.ReadOnly() {
		names = append(names, d.Tool.Name)
	}
	for _, d := range tools.Mutating() {
		names = append(names, d.Tool.Name)
	}
	// Every name the registry holds is in the list, so a tool added with a
	// tier is pinned here by being added to the oracle on purpose.
	for name := range registry {
		found := false
		for _, n := range names {
			found = found || n == name
		}
		if !found {
			t.Errorf("%s is registered but not pinned against the old answers", name)
		}
	}
	args := json.RawMessage(`{"command":"ls","path":"a.go","url":"https://example.com/x","verb":"commit"}`)
	for _, name := range names {
		for _, has := range []bool{false, true} {
			answers := Answers{Command: process.CommandOf}
			if has {
				answers.Has = func(string) bool { return true }
			}
			_, start, ok, _ := process.CommandOf(name, args)
			wantTier, wantKind := namedTier(name, has, ok && start)
			if ok && !start {
				wantTier = TierRead
			}
			got, _ := ClassifyCall(name, args, answers)
			if got.Tier != wantTier {
				t.Errorf("%q has=%v: tier %v, was %v", name, has, got.Tier, wantTier)
			}
			if wantTier != TierRead && got.Action.Kind != wantKind {
				t.Errorf("%q has=%v: kind %v, was %v", name, has, got.Action.Kind, wantKind)
			}
		}
	}
}

func TestClassifyCall_ADeclaredToolContributesToTheAction(t *testing.T) {
	got, _ := ClassifyCall(tools.Mutating()[0].Tool.Name, json.RawMessage(`{"path":" a/b.go "}`), Answers{})
	if got.Tier != TierWrite || got.Action.Path != "a/b.go" {
		t.Errorf("an edit: %+v", got)
	}
	got, _ = ClassifyCall(structural.GitWriteToolName, json.RawMessage(`{"verb":"commit"}`), Answers{})
	if got.Tier != TierWrite || got.Action.Command != "git commit" {
		t.Errorf("a git write: %+v", got)
	}
	args := json.RawMessage(`{"url":"https://example.com/x"}`)
	got, _ = ClassifyCall(web.FetchToolName, args, Answers{})
	if got.Tier != TierCommand || got.Action.Kind != ActionFetch || !reflect.DeepEqual(got.Action.Reading, web.ReadFetch(args)) {
		t.Errorf("a fetch: %+v", got)
	}
}
