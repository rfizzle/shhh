package cli

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/ask"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/memory"
	"github.com/rfizzle/shhh/internal/process"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/shell"
	"github.com/rfizzle/shhh/internal/structural"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/chat"
	"github.com/rfizzle/shhh/internal/web"
)

// notOffered is, per surface, the registered tools it never hands the model,
// so there is no call to gate: a tool a surface does not have is the
// dispatcher's unknown tool, not a free one. It is the one place a surface
// may differ from registeredTiers, and a name here is a statement about what
// the surface registers, never about the tool's tier.
var notOffered = map[string][]string{
	"screen":   nil,
	"headless": {memory.RememberToolName},
	"served":   {memory.RememberToolName},
	"child": {memory.RememberToolName, ask.ToolName, process.ToolName,
		structural.GitWriteToolName},
}

// gateSurfaces is each surface's own statement of whether a call to a tool is
// put to a decision, read off the executor that surface builds with every
// toolset it can hold switched on. The screen is read through the card it
// would build: a call it does not gate has none.
func gateSurfaces(t *testing.T) map[string]func(name string) bool {
	t.Helper()
	call := func(name string) provider.ToolCall {
		args := registeredArgs[name]
		if args == "" {
			args = `{}`
		}
		return provider.ToolCall{ID: "c", Name: name, Arguments: args}
	}
	preview := func(json.RawMessage) (chat.GatedPreview, error) { return chat.GatedPreview{Summary: "x"}, nil }
	screen := chat.New(nil, nil, chat.Wiring{
		Workspace: t.TempDir(),
		Runner:    func(context.Context, string) tools.ExecResult { return tools.ExecResult{} },
		Ask:       true,
		Memory:    chat.Memory{Save: func(string, string, string) (string, error) { return "", nil }},
		Processes: chat.Processes{Manage: func([]string) string { return "" }},
		GatedTools: map[string]chat.GatedPreviewFunc{
			structural.GitWriteToolName: preview,
			web.FetchToolName:           preview,
			subagent.SpawnToolName:      preview,
		},
	})
	webTools, procSup, sup := web.NewToolset(nil, nil), &process.Supervisor{}, &subagent.Supervisor{}
	gate := unattendedGate(webTools, procSup, nil, sup)
	approver := headlessApproval{webTools: webTools, procSup: procSup, structTools: &structural.Toolset{}, un: unattended{sup: sup}}

	// A child's gated set is the one its registration builds: the writer's
	// command and two file tools, the fetch where the web is held, and the
	// spawn where it may delegate.
	gated := map[string]bool{}
	builtinEnv(subagent.RoleWriter, subagent.Spec{Role: subagent.RoleWriter}, shell.Info{}, "", webTools, gated)
	withDelegation(subagent.New(context.Background(), subagent.Options{MaxDepth: 2}), &agentProfiles{}, subagent.Offer{},
		config.AgentDefinition{}, subagent.Spec{}, nil, tools.Execute, gated)

	return map[string]func(string) bool{
		"screen": func(name string) bool {
			_, result, _ := screen.StandingAnswer(call(name))
			return !strings.Contains(result, "cannot be approved")
		},
		"headless": func(name string) bool { return gate(call(name)) },
		// A served session builds the same gate and answers with the same
		// approver; the approver's own list of what it answers for is the
		// third copy of the line.
		"served": func(name string) bool {
			// The question is answered by the serve loop itself and never
			// by the approver, so only the gate speaks for it.
			if name == ask.ToolName {
				return gate(call(name))
			}
			c, _ := agent.ClassifyCall(name, json.RawMessage(call(name).Arguments),
				agent.Answers{Has: approver.answers, Command: process.CommandOf})
			return gate(call(name)) && c.Gated
		},
		"child": func(name string) bool { return gated[name] },
	}
}

// Every tool the toolbox registers is held to the one classification on every
// surface: where a surface offers the tool, its executor puts the call to a
// decision exactly when the tier in registeredTiers is above the read tier.
// A tool added without a row fails here by name, and a tool gated on one
// surface and free on another fails naming the surface.
func TestGate_EverySurfaceGatesEveryToolTheSameWay(t *testing.T) {
	seen := map[string]bool{}
	var names []string
	add := func(n string) {
		if !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	for _, d := range append(tools.DefinitionsFull(), registrable(t)...) {
		if _, ok := registeredTiers[d.Name]; !ok {
			t.Errorf("tool %q is registered and has no row in registeredTiers", d.Name)
		}
		add(d.Name)
	}
	for n := range registeredTiers {
		add(n)
	}
	sort.Strings(names)

	surfaces := gateSurfaces(t)
	for _, surface := range []string{"screen", "headless", "served", "child"} {
		absent := map[string]bool{}
		for _, n := range notOffered[surface] {
			absent[n] = true
		}
		for _, name := range names {
			tier, ok := registeredTiers[name]
			if !ok || absent[name] {
				continue
			}
			if want := tier != agent.TierRead; surfaces[surface](name) != want {
				t.Errorf("%s: %s is at the %s tier in the table, and the surface gates it: %v", surface, name, tier, !want)
			}
		}
	}
}
