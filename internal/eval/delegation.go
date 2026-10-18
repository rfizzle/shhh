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
	"slices"
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
	// LabelDividedInPlace is a round of reads, with no child asked for, that
	// reaches two or more of the row's independent parts at once: the parts
	// taken together by one agent, which is as much the work divided as
	// children are.
	LabelDividedInPlace = "divided in place"
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
		defs = append(defs, subagent.Definitions(subagent.BuiltinProfiles(), subagent.Offer{})...)
	}
	sys := prompt.BuildAgent(shell.Info{OS: runtime.GOOS, Cwd: dir},
		prompt.Toolbox(defs, row.Delegation == config.DelegationProactive))
	exec := subagent.RootedExecutor(dir, tools.Execute)

	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: sys},
		{Role: provider.RoleUser, Content: row.Instruction},
	}
	a := Answer{Row: row}
	var rounds []string
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
			a.Went = "answered in its first turn"
			if len(rounds) > 0 {
				a.Went = clipRunes(strings.Join(rounds, " · ")+" · then answered", maxWent)
			}
			return a
		}
		rounds = append(rounds, roundTools(calls))
		if n := partsRead(calls, row.Parts); n >= 2 {
			a.Label, a.Reason = LabelDividedInPlace, fmt.Sprintf("read %d parts in one round", n)
			a.Went = clipRunes(strings.Join(rounds, " · "), maxWent)
			return a
		}
		msgs = append(msgs, provider.Message{Role: provider.RoleAssistant, Content: reply, ToolCalls: calls})
		for _, c := range calls {
			out, err := exec(ctx, c.Name, json.RawMessage(c.Arguments))
			if err != nil {
				out = "error: " + err.Error()
			}
			msgs = append(msgs, provider.Message{Role: provider.RoleTool, Content: out, ToolCallID: c.ID})
		}
	}
	a.Label, a.Reason = LabelAlone, fmt.Sprintf("read for %d rounds without asking for a child", delegationRounds)
	a.Went = clipRunes(strings.Join(rounds, " · "), maxWent)
	return a
}

// partsRead is how many of the parts one round's calls read. A call reads a
// part when it is a reader of file contents and a path it names has the
// part's name as one of its segments; a glob lists names and reads nothing,
// so a round of globs is not a read of any part.
func partsRead(calls []provider.ToolCall, parts []string) int {
	seen := map[string]bool{}
	for _, c := range calls {
		switch c.Name {
		case tools.ReadFileName, "search", "query":
		default:
			continue
		}
		var args struct {
			Path  string   `json:"path"`
			Paths []string `json:"paths"`
		}
		if json.Unmarshal([]byte(c.Arguments), &args) != nil {
			continue
		}
		for _, p := range append([]string{args.Path}, args.Paths...) {
			for _, seg := range strings.Split(filepath.ToSlash(p), "/") {
				if slices.Contains(parts, seg) {
					seen[seg] = true
				}
			}
		}
	}
	return len(seen)
}

// maxWent bounds the per-round tool list a report line carries.
const maxWent = 120

// roundTools is one round's tool names in first-call order, a name called more
// than once followed by its count: "read_file×3, glob".
func roundTools(calls []provider.ToolCall) string {
	var order []string
	n := map[string]int{}
	for _, c := range calls {
		if n[c.Name] == 0 {
			order = append(order, c.Name)
		}
		n[c.Name]++
	}
	for i, name := range order {
		if n[name] > 1 {
			order[i] = fmt.Sprintf("%s×%d", name, n[name])
		}
	}
	return strings.Join(order, ", ")
}

// clipRunes cuts s to at most max runes, ending in an ellipsis when it cut.
func clipRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
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
