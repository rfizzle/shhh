package cli

import (
	"encoding/json"
	"testing"

	"github.com/rfizzle/shhh/internal/ask"
	"github.com/rfizzle/shhh/internal/memory"
	"github.com/rfizzle/shhh/internal/permission"
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
var registeredTiers = map[string]permission.Tier{
	tools.ExecCommandName:       permission.TierCommand,
	process.ToolName:            permission.TierCommand, // a start; see the args below
	web.FetchToolName:           permission.TierCommand,
	subagent.SpawnToolName:      permission.TierCommand,
	memory.RememberToolName:     permission.TierCommand,
	ask.ToolName:                permission.TierCommand,
	tools.WriteFileName:         permission.TierWrite,
	tools.EditFileName:          permission.TierWrite,
	structural.GitWriteToolName: permission.TierWrite,
	tools.ReadFileName:          permission.TierRead,
	tools.ListDirectoryName:     permission.TierRead,
	tools.SearchName:            permission.TierRead,
	tools.GlobName:              permission.TierRead,
	tools.QueryName:             permission.TierRead,
	tools.SqliteName:            permission.TierRead,
	tools.DocumentSymbolName:    permission.TierRead,
	"definition":                permission.TierRead,
	"references":                permission.TierRead,
	"workspace_symbol":          permission.TierRead,
	"hover":                     permission.TierRead,
	"diagnostics":               permission.TierRead,
	structural.FdToolName:       permission.TierRead,
	structural.AstGrepToolName:  permission.TierRead,
	structural.SdToolName:       permission.TierRead,
	structural.TokeiToolName:    permission.TierRead,
	structural.GitToolName:      permission.TierRead,
	web.SearchToolName:          permission.TierRead,
	"mcp_resource":              permission.TierRead,
	"quality_gate":              permission.TierRead,
	"report":                    permission.TierRead,
	"write_note":                permission.TierRead,
	"read_note":                 permission.TierRead,
	"steps":                     permission.TierRead,
	subagent.ReportToolName:     permission.TierRead,
	subagent.SteerToolName:      permission.TierRead,
	subagent.RetryToolName:      permission.TierRead,
	"evidence":                  permission.TierRead,
	"skill":                     permission.TierRead,
	"backlog_proposals":         permission.TierRead,
	"draft_profile":             permission.TierRead,
	"draft_toolchain":           permission.TierRead,
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
		answers permission.Answers
	}{
		{"answers for what it gates", permission.Answers{Has: func(n string) bool { return registeredTiers[n] != permission.TierRead }, Command: process.CommandOf}},
		{"answers for everything", permission.Answers{Has: func(string) bool { return true }, Command: process.CommandOf}},
		{"answers for nothing", permission.Answers{Command: process.CommandOf}},
	}
	modes := []permission.Mode{permission.ModeManual, permission.ModeAcceptEdits, permission.ModeAuto, permission.ModeReadOnly, permission.ModePlan}
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
			call, err := permission.ClassifyCall(name, json.RawMessage(args), s.answers)
			if err != nil {
				t.Errorf("%s (%s): %v", name, s.name, err)
				continue
			}
			answered := s.answers.Has != nil && s.answers.Has(name)
			tier := want
			switch {
			case classifierKnown[name]:
			case answered:
				tier = permission.TierCommand
			default:
				tier = permission.TierRead
			}
			if call.Tier != tier {
				t.Errorf("%s (%s): tier %s, want %s", name, s.name, call.Tier, tier)
			}
			if call.Gated != (answered && tier != permission.TierRead) {
				t.Errorf("%s (%s): gated %v", name, s.name, call.Gated)
			}
			switch call.Tier {
			case permission.TierWrite:
				if call.Action.Kind != permission.ActionEdit {
					t.Errorf("%s (%s): a write answered as %s", name, s.name, call.Action.Kind)
				}
			case permission.TierCommand:
				if call.Action.Kind == permission.ActionEdit {
					t.Errorf("%s (%s): a command answered as an edit", name, s.name)
				}
			}
			if !answered {
				continue
			}
			for _, mode := range modes {
				decision, reason := permission.ModePolicy{Mode: mode}.Decide(call.Action)
				switch {
				case mode.ReadOnly() && decision != permission.Deny:
					t.Errorf("%s in %s: %v (%s), want refused", name, mode, decision, reason)
				case call.Tier == permission.TierWrite && mode == permission.ModeManual && decision != permission.Ask:
					t.Errorf("%s in manual: %v (%s), want asked", name, decision, reason)
				case call.Tier == permission.TierCommand && mode == permission.ModeAcceptEdits && decision == permission.Allow:
					t.Errorf("%s in accept-edits: allowed (%s), want asked", name, reason)
				}
			}
		}
	}
}
