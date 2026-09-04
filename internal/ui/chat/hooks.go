package chat

// The person's own commands at the session's seams: where the runner hangs,
// where its answers are drawn, and what a hook's refusal looks like on the
// transcript.
//
// The seams themselves are elsewhere, in the code that already owned them —
// the approval queue in approval.go, the turn's close in close.go, the
// command run in run.go — because a hook is not a new place where things
// happen. See docs/capabilities/hooks.md.

import (
	"encoding/json"
	"strings"

	"github.com/rfizzle/shhh/internal/hook"
	"github.com/rfizzle/shhh/internal/provider"
)

// WithHooks installs the session's hook runner and puts the tool seams on the
// executor. Nil is a session with no hooks, which every seam below is safe
// under.
//
// It takes the executor rather than reading one back because it must run
// after everything that decides which calls are gated — the wrap asks that
// question of the model it was built from, and a wrap built before the gated
// tools were registered would answer it wrongly for every one of them.
func (m Model) WithHooks(r *hook.Runner, exec ToolExecutor) Model {
	m.hooks = r
	if r != nil && exec != nil {
		m.agent.SetExecutor(m.hookExecutor(exec))
	}
	return m
}

// hookExecutor puts the tool seams inside the read-only dispatcher, through
// the one wrap both surfaces use (internal/hook).
//
// A call this session puts to a person skips the seam in front of it: that
// one is answered at the approval queue, which is the dispatcher for that
// tier, and asking in both places would put one call to one hook twice.
//
// The position it reports is the round the agent has reached and no turn: the
// chain is wrapped once, before the first turn opens, and which turn a call
// belongs to is not a fact a tool dispatcher holds. The seams that do hold
// one report it.
func (m Model) hookExecutor(next ToolExecutor) ToolExecutor {
	a, gated := m.agent, m.requiresApproval
	return ToolExecutor(m.hooks.WrapExecutor(
		func() hook.Pos { return hook.Pos{Round: int64(a.Rounds())} },
		func(name string, args json.RawMessage) bool {
			return gated(provider.ToolCall{Name: name, Arguments: string(args)})
		},
		hook.Executor(next)))
}

// hookPos is where the session is now, in the two numbers the record and the
// event stream carry.
func (m Model) hookPos() hook.Pos {
	return hook.Pos{Turn: m.turnCount, Round: int64(m.agent.Rounds())}
}

// hookNotes turns what the hooks said into transcript rows. They are system
// notices rather than activity rows: a hook that formatted a file or wrote a
// line is not a moment the reader has to be able to find again, and a hook
// that refused draws the denial row instead
// (docs/interface/principles.md#weight-tracks-risk).
func (m *Model) hookNotes(v hook.Verdict) {
	for _, note := range v.Notes {
		m.appendEntry(entry{kind: entrySystem, text: "hook — " + note})
	}
}

// hookWhy is the sentence `/permissions why` prints under a hook's refusal.
// "A rule said no" is only actionable when the reader is told which rule, so
// it names the hook — the reader is the one person who can edit it, and the
// model is deliberately told neither the name of the file nor the way to
// change it.
func hookWhy(name string) string {
	if name == "" {
		return "refused by one of this session's hooks"
	}
	return "refused by the " + name + " hook, which is read before the mode and before the classifier"
}

// hooksStatus is the hooks in words for `/status`, and nothing at all for a
// session with none. It is on the same screen as the tool sources and the
// withheld list because it is the same question — what else is in this
// session — asked of the person's own configuration.
func (m Model) hooksStatus() string {
	set := m.hooks.Set()
	if set.Len() == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("Hooks\n")
	for _, h := range set.All() {
		line := h.Name + " — " + h.Event
		if h.Matcher != "" {
			line += " · " + h.Matcher
		}
		sb.WriteString(line + "\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// execOutcome is how a command came out, in the same two words a tool result
// is read into: a command has an exit code where a tool has a result, and the
// seam behind it must not be the one place a third word for "it failed"
// appears.
func execOutcome(code int) string {
	if code == 0 {
		return hook.OutcomeOK
	}
	return hook.OutcomeError
}
