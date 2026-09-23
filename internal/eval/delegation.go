package eval

// One task, put to a real model under a delegation policy: does it spawn,
// and does it divide?
//
// The policy reaches the model as one sentence on the spawn_agent line of the
// toolbox, and under off as that tool's absence. Neither is something a unit
// test can grade: the test can pin the sentence, and never whether a model
// told that a request for depth is not a request to delegate still spawns on
// "be thorough". So this shape asks the real one. The row's instruction is put
// to the coding prompt with the read-only tools, the orchestration tools where
// the policy registers them, and the toolbox the session would write; the
// read-only tools run against the row's files, and the first round that asks
// for a child ends the row, because the card that would follow is the
// person's and not the model's.
// See docs/capabilities/subagents.md#spawning-is-a-decision.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/shell"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/tools"
)

// KindDelegation puts a row's instruction to the coding prompt under the
// row's delegation policy and reads what the model did about children. It
// names a model: the decision is the model's.
const KindDelegation Kind = "delegation"

// What a delegation row's answer can be.
const (
	// LabelAlone is a run that finished, or ran out of rounds, without
	// asking for a child.
	LabelAlone = "alone"
	// LabelSpawned is a round that asked for exactly one child.
	LabelSpawned = "spawned"
	// LabelDivided is a round that asked for two or more children at once,
	// which is the work divided rather than handed off whole.
	LabelDivided = "divided"
)

// delegationRounds bounds how long a row may read before deciding. A model
// that checks the tree before it divides is doing what it should; one that
// has not decided in this many rounds is working alone.
const delegationRounds = 6

// askDelegation runs one row's instruction under its policy.
func askDelegation(ctx context.Context, p provider.Provider, model string, row Row) Answer {
	dir, err := os.MkdirTemp("", "shhh-eval-delegation-")
	if err != nil {
		return Answer{Row: row, Err: "cannot make a workspace: " + err.Error()}
	}
	defer func() { _ = os.RemoveAll(dir) }()
	for path, body := range row.Files {
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return Answer{Row: row, Err: "cannot seed the workspace: " + err.Error()}
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			return Answer{Row: row, Err: "cannot seed the workspace: " + err.Error()}
		}
	}

	// The toolset and the toolbox the session would build under this policy:
	// off registers no orchestration tools, and only proactive leans the
	// spawn line.
	defs := tools.Definitions()
	if row.Delegation != config.DelegationOff {
		defs = append(defs, subagent.Definitions(subagent.BuiltinProfiles())...)
	}
	sys := prompt.BuildAgent(shell.Info{OS: runtime.GOOS, Cwd: dir},
		prompt.Toolbox(defs, row.Delegation == config.DelegationProactive))
	exec := subagent.RootedExecutor(dir, tools.Execute)

	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: sys},
		{Role: provider.RoleUser, Content: row.Instruction},
	}
	a := Answer{Row: row}
	for range delegationRounds {
		reply, calls, usage, err := oneReply(ctx, p, model, msgs, defs)
		a.Usage.PromptTokens += usage.PromptTokens
		a.Usage.CompletionTokens += usage.CompletionTokens
		if err != nil {
			a.Err = firstLine(err.Error())
			return a
		}
		spawns := 0
		for _, c := range calls {
			if c.Name == subagent.SpawnToolName {
				spawns++
			}
		}
		switch {
		case spawns >= 2:
			a.Label, a.Reason = LabelDivided, fmt.Sprintf("asked for %d children in one round", spawns)
			return a
		case spawns == 1:
			a.Label, a.Reason = LabelSpawned, "asked for one child"
			return a
		case len(calls) == 0:
			a.Label, a.Reason = LabelAlone, firstLine(reply)
			return a
		}
		msgs = append(msgs, provider.Message{Role: provider.RoleAssistant, Content: reply, ToolCalls: calls})
		for _, c := range calls {
			out, err := exec(c.Name, json.RawMessage(c.Arguments))
			if err != nil {
				out = "error: " + err.Error()
			}
			msgs = append(msgs, provider.Message{Role: provider.RoleTool, Content: out, ToolCallID: c.ID})
		}
	}
	a.Label, a.Reason = LabelAlone, fmt.Sprintf("read for %d rounds without asking for a child", delegationRounds)
	return a
}

// oneReply is one request's answer text, its whole tool calls and what it
// cost.
func oneReply(ctx context.Context, p provider.Provider, model string, msgs []provider.Message,
	defs []provider.Tool) (string, []provider.ToolCall, provider.Usage, error) {
	events, err := p.StreamCompletion(ctx, msgs, provider.CompletionOpts{Model: model, Tools: defs})
	if err != nil {
		return "", nil, provider.Usage{}, err
	}
	var text strings.Builder
	var calls []provider.ToolCall
	var usage provider.Usage
	for ev := range events {
		if ev.Err != nil {
			return "", nil, usage, ev.Err
		}
		text.WriteString(ev.Token)
		calls = append(calls, ev.ToolCalls...)
		if ev.Usage != nil {
			usage.PromptTokens += ev.Usage.PromptTokens
			usage.CompletionTokens += ev.Usage.CompletionTokens
		}
	}
	return text.String(), provider.CompletedToolCalls(calls), usage, nil
}
