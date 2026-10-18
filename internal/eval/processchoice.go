package eval

// One request, put to a real model with both the command tool and the
// process tool in front of it: which does it reach for first?
//
// The redirect in the command tool answers the shapes someone listed, and the
// descriptions and the prompt sentence are what is meant to make the redirect
// rarely needed. Neither is something a unit test can grade: the test can pin
// the words, and never whether a model told them still runs `npm run dev`
// through the command tool and waits out the ceiling. So this shape asks the
// real one. The row's instruction is put to the coding prompt with the
// read-only tools, the command tool and the process tool, and the toolbox the
// session would write; nothing is run, and the row is decided by the first
// tool call the model asks for.
// See docs/capabilities/containment.md#a-command-that-will-not-finish-is-not-waited-on-forever.

import (
	"context"
	"encoding/json"
	"runtime"

	"github.com/rfizzle/llm"
	"github.com/rfizzle/shhh/internal/process"
	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/shell"
	"github.com/rfizzle/shhh/internal/tools"
)

// KindProcessChoice puts a row's instruction to the coding prompt and reads
// the first tool call the model made. It names a model: the choice is the
// model's.
const KindProcessChoice Kind = "process-choice"

// What a process-choice row's answer can be.
const (
	// LabelProcess is a first call that starts a process.
	LabelProcess = "process"
	// LabelCommand is a first call to the command tool.
	LabelCommand = "command"
	// LabelOtherCall is a first call to anything else — a read of the tree
	// before deciding — or none at all. A row is written so that the right
	// first call is one of the two above, and spells out what it needs.
	LabelOtherCall = "other"
)

// askProcessChoice runs one row's instruction for its first round.
func askProcessChoice(ctx context.Context, p llm.Provider, model string, row Row) Answer {
	defs := append(tools.Definitions(), tools.ExecCommandTool(), process.Definition())
	sys := prompt.BuildAgent(shell.Info{OS: runtime.GOOS, Cwd: defaultRowCWD}, prompt.Toolbox(defs, false))
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: sys},
		{Role: llm.RoleUser, Content: row.Instruction},
	}
	a := Answer{Row: row}
	reply, calls, usage, err := oneReply(ctx, p, model, msgs, defs)
	a.Usage.PromptTokens += usage.PromptTokens
	a.Usage.CompletionTokens += usage.CompletionTokens
	if err != nil {
		a.Err = firstLine(err.Error())
		return a
	}
	if len(calls) == 0 {
		a.Label, a.Reason = LabelOtherCall, "no tool call: "+firstLine(reply)
		return a
	}
	a.Label, a.Reason = firstChoice(calls[0])
	return a
}

// firstChoice reads one call: the command tool is a command, the process
// tool's start is a process, and every other call, a process read included,
// is neither.
func firstChoice(c llm.ToolCall) (label, reason string) {
	switch c.Name {
	case tools.ExecCommandName:
		return LabelCommand, "ran " + argOf(c, "command")
	case process.ToolName:
		if argOf(c, "action") == "start" {
			return LabelProcess, "started " + argOf(c, "command")
		}
		return LabelOtherCall, "process " + argOf(c, "action")
	}
	return LabelOtherCall, "called " + c.Name
}

// argOf is one string argument of a call, empty where the arguments do not
// parse or do not name it.
func argOf(c llm.ToolCall, key string) string {
	var args map[string]any
	if err := json.Unmarshal([]byte(c.Arguments), &args); err != nil {
		return ""
	}
	s, _ := args[key].(string)
	return firstLine(s)
}
