// Package hook runs the user's own commands at the seams a session already
// has: a session opening, a tool call about to run, a result about to be
// read, a turn closing, a run stopping.
//
// The seams are the ones that were in the code before this package existed —
// the approval queue, the executor chain, the turn's close, the session
// start — and that is the whole design. A hook is not a new place where
// things happen; it is a place that already happened, opened to a command
// line, so that running `gofmt` after an edit or refusing a path costs a
// config entry rather than a fork.
//
// Two rules shape everything here.
//
// A hook sits inside one tier's dispatcher and never in front of two. What a
// hook may change about a call is its arguments, and the answer it writes has
// no field that could name a tool — so a hook on a read can refuse it or add
// to it and cannot turn it into a write, and the reason is that there is
// nothing to say it with rather than a check that could be got around.
// See docs/capabilities/hooks.md#a-hook-cannot-move-a-call-between-tiers.
//
// And nothing decides yes on a failure. A hook that exits non-zero for any
// reason but refusal, or that runs past its ceiling, has failed; a failure
// asks where there is somebody to ask and does nothing where there is not.
// Nor does a hook's allow decide yes: it is the absence of an objection, and
// the mode, the lists and the person decide exactly as they would have.
// See docs/capabilities/hooks.md#nothing-decides-yes-on-a-failure.
package hook

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// The events, in the order a listing names them: the session opening, the two
// either side of a tool call, the turn closing, and the run stopping.
//
// They are the seams that exist, which is why there are five and not more. A
// sixth would mean a new place in the loop for something to happen, and that
// is a change to the product rather than a line in a table.
const (
	SessionStart = "session_start"
	PreTool      = "pre_tool"
	PostTool     = "post_tool"
	TurnClose    = "turn_close"
	Stop         = "stop"
)

// Events is every event a hook may name, in listing order.
func Events() []string {
	return []string{SessionStart, PreTool, PostTool, TurnClose, Stop}
}

// Decisions a hook may answer with. They are the words the record already
// keeps for an approval verdict, so a reader who has seen one has seen the
// other; a test holds them equal to the record's.
//
// Allow is the absence of an objection and not an approval. A hook is a
// config entry, and no config entry turns a card into a call that was never
// asked about — that is the one direction this product does not go.
const (
	DecisionAllow = "allow"
	DecisionDeny  = "deny"
	DecisionAsk   = "ask"
)

// DenyExit is the exit code that refuses a call. Any other non-zero exit is a
// failure rather than a refusal: a hook that crashed and a hook that said no
// are different facts, and reading a crash as a refusal would make every
// broken hook look like a working one.
const DenyExit = 2

// Pos is where in the session the seam fired, in the same two numbers the
// record and the event stream carry.
type Pos struct {
	Turn  int64
	Round int64
}

// Call is the tool call a tool-seam hook is being told about: what the model
// asked for, in the fields the event stream already names it with.
type Call struct {
	ID        string
	Name      string
	Arguments string
}

// Payload is the JSON a hook reads on stdin. Its field names are the event
// stream's, because a hook author should learn one shape and it should be the
// one the record already fixes — a test holds the shared fields to the
// stream's spelling.
// See docs/capabilities/hooks.md#the-payload-is-the-event-stream.
type Payload struct {
	// Event is which seam fired, from the five above. It is the first field
	// a hook reads and the one every hook reads, so it leads.
	Event string `json:"event"`
	// Session is the saved conversation this session is writing, and CWD the
	// directory it was opened in. Neither changes while a session runs; both
	// are here because a hook is a separate process and has no other way to
	// know which session it was called for.
	Session string `json:"session,omitempty"`
	CWD     string `json:"cwd,omitempty"`

	Turn  int64 `json:"turn,omitempty"`
	Round int64 `json:"round,omitempty"`

	ID        string `json:"id,omitempty"`
	Tool      string `json:"tool,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Result    string `json:"result,omitempty"`
	Outcome   string `json:"outcome,omitempty"`
	Final     string `json:"final,omitempty"`
}

// Response is what a hook writes on stdout. Every field is optional: a hook
// that writes nothing at all and exits zero has said "I have no objection",
// which is the common case and should cost nothing to spell.
type Response struct {
	// Decision is allow, deny or ask. Anything else is read as nothing said,
	// because a word this build has no meaning for must not become one.
	Decision string `json:"decision,omitempty"`
	// UpdatedInput replaces the call's arguments. It is arguments and never a
	// tool: the tier rule is that there is no field here to move a call with.
	UpdatedInput json.RawMessage `json:"updated_input,omitempty"`
	// Context is text for the model to read — appended to the result at a
	// tool seam, added to the system prompt at a session's start.
	Context string `json:"context,omitempty"`
	// Note is text for the person, drawn on the surface and never sent.
	Note string `json:"note,omitempty"`
}

// Hook is one entry: the seam it fires at, which tools it is about, the
// command line that runs, and how long it may take.
type Hook struct {
	// Name is what the file called it, and what a diagnostic and the doctor
	// row name. Two hooks on one event run in name order, so the order is the
	// person's to decide and is the same on every machine.
	Name    string
	Event   string
	Matcher string
	Command string
	// Timeout is this hook's own ceiling, zero taking the session's. It is
	// capped at the session's either way: a hook may be quicker than an
	// assistant command and may not be slower, because the reason a command
	// is bounded at all is that nobody is watching it.
	Timeout time.Duration
	// Source is the file it was read from, for the diagnostics and the rows
	// that say where a hook came from.
	Source string

	// match is Matcher compiled, and nil for a hook that matches every tool.
	match *regexp.Regexp
}

// Entry is one hook as a file writes it — the same four fields in the config
// file's TOML and in the checkout's JSON, so a hook moved from one to the
// other is the same text.
type Entry struct {
	Event   string `json:"event" toml:"event"`
	Matcher string `json:"matcher,omitempty" toml:"matcher,omitempty"`
	Command string `json:"command" toml:"command"`
	// Timeout is in seconds, the unit every other ceiling in the config file
	// is written in.
	Timeout int `json:"timeout,omitempty" toml:"timeout,omitempty"`
}

// build turns one written entry into a hook, or says why it cannot. A matcher
// that will not compile is refused rather than ignored: ignoring it would
// leave a hook that matches everything where the person wrote one that
// matches a few, which is the failure that runs their formatter over every
// read in the session.
func build(name string, e Entry, source string) (Hook, error) {
	h := Hook{
		Name: name, Event: e.Event, Matcher: e.Matcher,
		Command: strings.TrimSpace(e.Command), Source: source,
	}
	if h.Name == "" {
		return h, fmt.Errorf("a hook needs a name")
	}
	if !knownEvent(h.Event) {
		return h, fmt.Errorf("event %q is not one of %s", h.Event, strings.Join(Events(), ", "))
	}
	if h.Command == "" {
		return h, fmt.Errorf("no command")
	}
	if e.Timeout < 0 {
		return h, fmt.Errorf("timeout %d is negative", e.Timeout)
	}
	h.Timeout = time.Duration(e.Timeout) * time.Second
	if h.Matcher == "" {
		return h, nil
	}
	if !hasTool(h.Event) {
		return h, fmt.Errorf("a matcher matches a tool name, and %s carries none", h.Event)
	}
	// Anchored, because a matcher is read as the name of a tool and not as
	// something to find inside one: `edit` written by somebody who meant
	// edit_file must not also take credit_check.
	re, err := regexp.Compile("^(?:" + h.Matcher + ")$")
	if err != nil {
		return h, fmt.Errorf("matcher %q: %w", h.Matcher, err)
	}
	h.match = re
	return h, nil
}

func knownEvent(event string) bool {
	for _, e := range Events() {
		if e == event {
			return true
		}
	}
	return false
}

// hasTool reports whether an event carries a tool name for a matcher to
// match. The other three are about the session and the turn, which are not
// things a name selects between.
func hasTool(event string) bool { return event == PreTool || event == PostTool }

// matches reports whether this hook is about a call on the named tool.
func (h Hook) matches(tool string) bool {
	if h.match == nil {
		return true
	}
	return h.match.MatchString(tool)
}
