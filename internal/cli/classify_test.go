package cli

import (
	"encoding/json"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/ask"
	"github.com/rfizzle/shhh/internal/memory"
	"github.com/rfizzle/shhh/internal/process"
	"github.com/rfizzle/shhh/internal/structural"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/web"
)

// registeredTiers is the tier of every tool name a toolset registers, on the
// surface that registered it: one that answers for the tools it gates and for
// nothing else. A name missing from it fails the test below, so a new tool
// states its tier in the change that registers it rather than taking
// whatever the classifier happens to say.
var registeredTiers = map[string]agent.Tier{
	tools.ExecCommandName:       agent.TierCommand,
	process.ToolName:            agent.TierCommand, // a start; see the args below
	web.FetchToolName:           agent.TierCommand,
	subagent.SpawnToolName:      agent.TierCommand,
	memory.RememberToolName:     agent.TierCommand,
	ask.ToolName:                agent.TierCommand,
	tools.WriteFileName:         agent.TierWrite,
	tools.EditFileName:          agent.TierWrite,
	structural.GitWriteToolName: agent.TierWrite,
	tools.ReadFileName:          agent.TierRead,
	tools.ListDirectoryName:     agent.TierRead,
	tools.SearchName:            agent.TierRead,
	tools.GlobName:              agent.TierRead,
	tools.QueryName:             agent.TierRead,
	tools.SqliteName:            agent.TierRead,
	tools.DocumentSymbolName:    agent.TierRead,
	"definition":                agent.TierRead,
	"references":                agent.TierRead,
	"workspace_symbol":          agent.TierRead,
	"hover":                     agent.TierRead,
	"diagnostics":               agent.TierRead,
	structural.FdToolName:       agent.TierRead,
	structural.AstGrepToolName:  agent.TierRead,
	structural.SdToolName:       agent.TierRead,
	structural.TokeiToolName:    agent.TierRead,
	structural.GitToolName:      agent.TierRead,
	web.SearchToolName:          agent.TierRead,
	"mcp_resource":              agent.TierRead,
	"quality_gate":              agent.TierRead,
	"report":                    agent.TierRead,
	"write_note":                agent.TierRead,
	"read_note":                 agent.TierRead,
	"steps":                     agent.TierRead,
	subagent.ReportToolName:     agent.TierRead,
	subagent.SteerToolName:      agent.TierRead,
	subagent.RetryToolName:      agent.TierRead,
	"evidence":                  agent.TierRead,
	"skill":                     agent.TierRead,
	"backlog_proposals":         agent.TierRead,
	"draft_profile":             agent.TierRead,
	"draft_toolchain":           agent.TierRead,
}

// classifierKnown are the registered names whose tier the classifier reads
// off the call — the process tool's through the reader a surface holding it
// hands in — whichever surface asks. Every other name's tier is the
// surface's answer: the command tier where it answers for the name, and the
// read tier where it does not.
var classifierKnown = map[string]bool{
	tools.ExecCommandName:       true,
	process.ToolName:            true,
	web.FetchToolName:           true,
	tools.WriteFileName:         true,
	tools.EditFileName:          true,
	structural.GitWriteToolName: true,
}

// registeredArgs is a call to each tool the classifier reads the arguments
// of, shaped so a command is one no mode lets through unasked.
var registeredArgs = map[string]string{
	tools.ExecCommandName:       `{"command":"make build"}`,
	process.ToolName:            `{"action":"start","name":"web","command":"make serve"}`,
	web.FetchToolName:           `{"url":"https://example.com/"}`,
	structural.GitWriteToolName: `{"verb":"commit","message":"feat: x"}`,
}

// Every name the toolsets register has a tier, the classifier gives it that
// tier, and a write never resolves below write — nor a command as an edit —
// whichever surface asks, in any mode.
func TestClassifyCall_EveryRegisteredNameHasItsTier(t *testing.T) {
	seen := map[string]bool{}
	var names []string
	for _, d := range append(tools.DefinitionsFull(), registrable(t)...) {
		if !seen[d.Name] {
			seen[d.Name] = true
			names = append(names, d.Name)
		}
	}
	surfaces := []struct {
		name    string
		answers agent.Answers
	}{
		{"answers for what it gates", agent.Answers{Has: func(n string) bool { return registeredTiers[n] != agent.TierRead }, Command: process.CommandOf}},
		{"answers for everything", agent.Answers{Has: func(string) bool { return true }, Command: process.CommandOf}},
		{"answers for nothing", agent.Answers{Command: process.CommandOf}},
	}
	modes := []agent.Mode{agent.ModeManual, agent.ModeAcceptEdits, agent.ModeAuto, agent.ModeReadOnly, agent.ModePlan}
	for _, name := range names {
		want, ok := registeredTiers[name]
		if !ok {
			t.Errorf("%s is registered and has no tier in registeredTiers", name)
			continue
		}
		args := registeredArgs[name]
		if args == "" {
			args = `{}`
		}
		for _, s := range surfaces {
			call, err := agent.ClassifyCall(name, json.RawMessage(args), s.answers)
			if err != nil {
				t.Errorf("%s (%s): %v", name, s.name, err)
				continue
			}
			answered := s.answers.Has != nil && s.answers.Has(name)
			tier := want
			switch {
			case classifierKnown[name]:
			case answered:
				tier = agent.TierCommand
			default:
				tier = agent.TierRead
			}
			if call.Tier != tier {
				t.Errorf("%s (%s): tier %s, want %s", name, s.name, call.Tier, tier)
			}
			if call.Gated != (answered && tier != agent.TierRead) {
				t.Errorf("%s (%s): gated %v", name, s.name, call.Gated)
			}
			switch call.Tier {
			case agent.TierWrite:
				if call.Action.Kind != agent.ActionEdit {
					t.Errorf("%s (%s): a write answered as %s", name, s.name, call.Action.Kind)
				}
			case agent.TierCommand:
				if call.Action.Kind == agent.ActionEdit {
					t.Errorf("%s (%s): a command answered as an edit", name, s.name)
				}
			}
			if !answered {
				continue
			}
			for _, mode := range modes {
				decision, reason := agent.ModePolicy{Mode: mode}.Decide(call.Action)
				switch {
				case mode.ReadOnly() && decision != agent.Deny:
					t.Errorf("%s in %s: %v (%s), want refused", name, mode, decision, reason)
				case call.Tier == agent.TierWrite && mode == agent.ModeManual && decision != agent.Ask:
					t.Errorf("%s in manual: %v (%s), want asked", name, decision, reason)
				case call.Tier == agent.TierCommand && mode == agent.ModeAcceptEdits && decision == agent.Allow:
					t.Errorf("%s in accept-edits: allowed (%s), want asked", name, reason)
				}
			}
		}
	}
}
