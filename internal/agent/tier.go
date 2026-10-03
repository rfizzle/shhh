package agent

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/rfizzle/shhh/internal/safety"
	"github.com/rfizzle/shhh/internal/structural"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/web"
)

// Tier is the security tier one tool call sits at.
type Tier int

const (
	// TierRead runs without a decision, through the dispatcher that has no
	// case for anything else.
	TierRead Tier = iota
	// TierWrite changes the tree, and is answered the way an edit is.
	TierWrite
	// TierCommand runs a program or reaches past the tree, and is answered
	// by its own kind; an edit grant never answers it.
	TierCommand
)

// String is the tier's name, for a test's failure message and a log line.
func (t Tier) String() string {
	switch t {
	case TierWrite:
		return "write"
	case TierCommand:
		return "command"
	}
	return "read"
}

// Classified is what the classifier says about one call.
type Classified struct {
	// Tier is the call's tier, which is a property of the call and never of
	// the surface asking.
	Tier Tier
	// Action is what the policy is asked about: the kind the tier is
	// answered as, the command line the call stands for — the command a
	// command runs, the line a git write stands for — and, for a command,
	// whether the safety table flags it, and for a fetch, the reading of its
	// host. The path an edit names and the host a fetch leaves for are the
	// surface's to fill in, read by its own previewer.
	Action Action
	// Gated is whether the call is put to a decision: it sits above the read
	// tier and the surface has an answer for it.
	Gated bool
}

// errInvalidCommand is the error a command call with no command in it is
// classified with, in the words every surface has always skipped one with.
var errInvalidCommand = errors.New("invalid command arguments")

// Answers is a surface's statement of what it holds, which is the one thing
// about a call the classifier cannot read off the name.
type Answers struct {
	// Has reports whether the surface has an answer for a call to name at
	// all: what it registered, and the cards or rules it can answer one with.
	// Nil has none.
	Has func(name string) bool
	// Command reads a call to a tool the surface holds that starts a program
	// of its own, the way the process tool's start does: ok is false for any
	// other name, starts is whether this call runs something, and err is a
	// start that names nothing to run. Nil is a surface that holds no such
	// tool. It is the tool's own reading, handed in by the surface that
	// registered it, because the package that owns it cannot be imported
	// from here.
	Command func(name string, args json.RawMessage) (command string, starts, ok bool, err error)
}

func (a Answers) has(name string) bool { return a.Has != nil && a.Has(name) }

// ClassifyCall is the one reading of which tier a tool call sits at and what
// the policy is asked about it. Every surface that puts a call to a decision —
// the session's card, a scripted run, a served session's gate, a child — asks
// it rather than reading the tier off a chain of names of its own, because a
// second copy of that reading agrees on the day it is written and the next
// tool that has to be answered for is added to whichever copy the author was
// looking at.
//
// What the surface holds decides only two things: whether a call above the
// read tier is gated, and whether a name the classifier does not know sits at
// the command tier, as the strictest kind there is, or at the read tier, where
// the dispatcher answers it as a tool that does not exist. It never lowers the
// tier of a name the classifier knows.
//
// The error is a call whose arguments say nothing the policy could be asked
// about — a command with no command, a start that names nothing — and the
// tier and kind are still set beside it, so a surface that answers the
// containment refusal ahead of the arguments can still do so.
// See docs/capabilities/approvals-and-safety.md#one-classifier-names-a-calls-tier.
func ClassifyCall(name string, args json.RawMessage, answers Answers) (Classified, error) {
	has := answers.has(name)
	c, err := classify(name, args, has, answers.Command)
	c.Gated = has && c.Tier != TierRead
	return c, err
}

// classify is the table itself. The order is the order of precedence, and
// the names it knows are the ones whose tier does not depend on the surface.
func classify(name string, args json.RawMessage, has bool,
	starts func(string, json.RawMessage) (string, bool, bool, error)) (Classified, error) {
	switch {
	case name == tools.ExecCommandName:
		c := Classified{Tier: TierCommand, Action: Action{Kind: ActionCommand}}
		var a struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(args, &a); err != nil || strings.TrimSpace(a.Command) == "" {
			return c, errInvalidCommand
		}
		// The line as the model wrote it: a surface that runs a trimmed one
		// trims it itself, and the safety table reads each line trimmed
		// either way.
		c.Action.Command = a.Command
		c.Action.SafetyFlagged = len(safety.Check(a.Command)) > 0
		return c, nil
	case starts != nil && claims(starts, name, args):
		// A background start's tier is its verb's: a start launches a
		// command and is answered as one, and the verbs that read what is
		// already running are reads. Its kind is a command whichever verb it
		// is, so a surface asked to answer a call that is not a start says so
		// in the tool's own words.
		command, start, _, err := starts(name, args)
		c := Classified{Tier: TierRead, Action: Action{Kind: ActionCommand}}
		if start {
			c.Tier = TierCommand
		}
		if err != nil {
			return c, err
		}
		c.Action.Command = command
		c.Action.SafetyFlagged = len(safety.Check(command)) > 0
		return c, nil
	case name == web.FetchToolName:
		// The reading comes from the function every surface asks, so a host
		// is judged alike wherever the fetch was made.
		return Classified{Tier: TierCommand, Action: Action{Kind: ActionFetch, Reading: web.ReadFetch(args)}}, nil
	case tools.IsMutating(name):
		return Classified{Tier: TierWrite, Action: Action{Kind: ActionEdit}}, nil
	case name == structural.GitWriteToolName:
		// The writing half of git sits at the write tier and carries the line
		// it stands for, so a deny-list entry for `git commit` refuses the
		// commit verb by the match the command path uses.
		// See docs/capabilities/approvals-and-safety.md#the-writing-half-of-git-is-a-tool-too.
		return Classified{Tier: TierWrite, Action: Action{Kind: ActionEdit, Command: structural.WriteLine(args)}}, nil
	case has:
		// A name only the surface knows — a server's tool, a child, a memory,
		// a question — that the surface has an answer for. It is the
		// strictest kind: asked in every mode that asks, refused in the two
		// that write nothing.
		return Classified{Tier: TierCommand, Action: Action{Kind: ActionOther}}, nil
	}
	// Not ActionEdit, which is the zero kind: a read-tier call is never put
	// to the policy, and one that somehow was would be asked about rather
	// than answered as an edit.
	return Classified{Tier: TierRead, Action: Action{Kind: ActionOther}}, nil
}

// claims reports whether a surface's start reader reads name as its own.
func claims(starts func(string, json.RawMessage) (string, bool, bool, error), name string, args json.RawMessage) bool {
	_, _, ok, _ := starts(name, args)
	return ok
}
