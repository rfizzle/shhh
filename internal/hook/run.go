package hook

// Firing one seam: what the hooks on it are told, how long they have, and
// how several of them come to one answer.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Exec runs one hook's command line with the payload on stdin and answers
// with what it wrote to stdout and how it exited. The caller supplies it
// because what a command runs under is the session's to decide: contained
// where the session contains its commands, with the session's environment,
// through the shell the session runs commands with. This package decides when
// a hook runs and what it is told, and never how a machine runs a command.
//
// It must return when ctx is done. The ceiling is the caller's context, not a
// signal this package sends: a hook is a command like any other and the code
// that knows how to stop one is the code that started it.
type Exec func(ctx context.Context, command string, stdin []byte) (stdout string, exitCode int, err error)

// DefaultCeiling is how long a hook may take when the caller names no
// ceiling. Short on purpose, for the reason in the configuration's own
// answer: every seam a hook sits on has something waiting on the other side
// of it.
const DefaultCeiling = 30 * time.Second

// Runner is one session's hooks, ready to fire.
type Runner struct {
	set  *Set
	exec Exec
	// ceiling is the longest any hook here may take, whatever its own
	// timeout says. It is the session's command ceiling: a hook is a command
	// the session runs, and the reason that ceiling exists — nobody is
	// watching it — is truest of a hook, which nobody asked for.
	ceiling time.Duration
	// session and cwd are the two facts about the session a hook cannot work
	// out for itself, read once because neither moves while a session runs.
	session string
	cwd     string
}

// NewRunner builds the runner for a session's hooks. A nil or empty set, or a
// nil exec, gives a runner that fires nothing — which every seam is safe to
// call, so no surface has to remember to check.
func NewRunner(set *Set, exec Exec, ceiling time.Duration, cwd string) *Runner {
	if set.Len() == 0 || exec == nil {
		return nil
	}
	if ceiling <= 0 {
		ceiling = DefaultCeiling
	}
	return &Runner{set: set, exec: exec, ceiling: ceiling, cwd: cwd}
}

// SetSession names the record this session is being written to, so a hook
// that keeps its own notes can join them to the table. It is set rather than
// given at construction because the seam that fires first is the session
// opening, and the row it would name is opened as the session finishes
// assembling itself — after that seam has already fired.
func (r *Runner) SetSession(id string) {
	if r != nil {
		r.session = id
	}
}

// Set is the hooks this runner fires, for the surfaces that list them.
func (r *Runner) Set() *Set {
	if r == nil {
		return nil
	}
	return r.set
}

// Has reports whether anything at all would fire for an event, so a seam can
// skip building a payload nothing will read.
func (r *Runner) Has(event, tool string) bool {
	if r == nil {
		return false
	}
	return len(r.set.For(event, tool)) > 0
}

// Verdict is what one seam's hooks came to.
//
// The zero value is the answer of a seam with no hooks on it and of hooks
// that had nothing to say, which are deliberately the same value: a surface
// acts on a verdict without asking whether there were any hooks.
type Verdict struct {
	// Decision is the strongest thing the hooks said — deny where one
	// refused, ask where one asked or failed on a call there is somebody to
	// ask about, and "" where none of them decided anything. Allow is never
	// here: a hook's allow is the absence of an objection, so it leaves the
	// decision exactly where it found it.
	Decision string
	// Reason names the hook behind Decision, which is what the denial row
	// prints. "A rule said no" is only actionable when the reader is told
	// which rule (docs/capabilities/approvals-and-safety.md#denials-are-two-different-facts).
	Reason string
	// Input is the call's arguments as the hooks left them, and nil where
	// none of them rewrote anything. It is arguments and nothing else: this
	// is the field the tier rule lives in.
	Input json.RawMessage
	// Context is what the hooks want the model to read.
	Context string
	// Notes are what the surface says out loud, one line per hook that
	// refused, rewrote, failed or wrote a note of its own.
	Notes []string
	// faults are the subset of Notes that are failures. They are kept apart
	// because a note is written for the person and a failure has to reach the
	// model as well: joining the two would send a hook's note to the model
	// the moment some other hook on the same seam happened to break, which is
	// the opposite of what a note is for.
	faults []string
	// Failed says at least one hook did not come back cleanly. It is
	// reported separately from Decision because a failure that could not ask
	// anybody still has to be visible: nothing decided yes, and nothing said
	// so either, and that combination is how a broken hook goes unnoticed.
	Failed bool
}

// Denied and Asked are the two questions a seam puts to a verdict.
func (v Verdict) Denied() bool { return v.Decision == DecisionDeny }
func (v Verdict) Asked() bool  { return v.Decision == DecisionAsk }

// SessionStart fires the hooks a session opening has. Their context joins the
// system prompt, which is the one moment in a session where adding to what
// the model has been told is still cheap.
func (r *Runner) SessionStart(ctx context.Context) Verdict {
	return r.fire(ctx, SessionStart, "", Payload{Event: SessionStart}, false)
}

// PreTool fires the hooks in front of one call. gated says the call is one
// the session puts to a person, which is what decides where a failure lands:
// a gated call becomes an ask, because there is somebody to ask; a read
// becomes the read it already was, with a note, because there is not.
//
// It is called from inside one tier's dispatcher and never in front of two.
// What comes back can refuse the call or rewrite its arguments, and there is
// no field in a hook's answer that names a tool, so the call that runs is a
// call of the kind that was dispatched.
// See docs/capabilities/hooks.md#a-hook-cannot-move-a-call-between-tiers.
func (r *Runner) PreTool(ctx context.Context, at Pos, c Call, gated bool) Verdict {
	return r.fire(ctx, PreTool, c.Name, Payload{
		Event: PreTool, Turn: at.Turn, Round: at.Round,
		ID: c.ID, Tool: c.Name, Arguments: c.Arguments,
	}, gated)
}

// PostTool fires the hooks behind one call, with the result the model is
// about to read. A decision here has nothing left to decide — the call has
// run — so what a post-tool hook is for is its context and its note.
func (r *Runner) PostTool(ctx context.Context, at Pos, c Call, result, outcome string) Verdict {
	v := r.fire(ctx, PostTool, c.Name, Payload{
		Event: PostTool, Turn: at.Turn, Round: at.Round,
		ID: c.ID, Tool: c.Name, Arguments: c.Arguments, Result: result, Outcome: outcome,
	}, false)
	v.Decision, v.Reason, v.Input = "", "", nil
	return v
}

// TurnClose fires as a turn's accounting closes, with the answer it closed
// on.
func (r *Runner) TurnClose(ctx context.Context, at Pos, final string) Verdict {
	return r.fire(ctx, TurnClose, "", Payload{
		Event: TurnClose, Turn: at.Turn, Round: at.Round, Final: final,
	}, false)
}

// Stop fires as the session ends.
func (r *Runner) Stop(ctx context.Context, at Pos, final string) Verdict {
	return r.fire(ctx, Stop, "", Payload{
		Event: Stop, Turn: at.Turn, Round: at.Round, Final: final,
	}, false)
}

// fire runs every hook on one seam, in order, and folds their answers into
// one. A refusal stops the rest: the call is not going to happen, and running
// the remaining hooks would be work nothing reads and side effects nobody
// asked for.
func (r *Runner) fire(ctx context.Context, event, tool string, p Payload, gated bool) Verdict {
	var v Verdict
	if r == nil {
		return v
	}
	hooks := r.set.For(event, tool)
	if len(hooks) == 0 {
		return v
	}
	p.Session, p.CWD = r.session, r.cwd
	for _, h := range hooks {
		// Each hook is told the arguments as the one before it left them, so
		// two hooks on a call compose rather than the second undoing the
		// first.
		if v.Input != nil {
			p.Arguments = string(v.Input)
		}
		res, failed := r.one(ctx, h, p)
		if failed {
			v.Failed = true
			v.Notes = append(v.Notes, res.Note)
			v.faults = append(v.faults, res.Note)
			// Nothing decides yes on a failure. A call somebody can be asked
			// about becomes an ask; a read carries the note and runs, since
			// refusing every read because a hook is broken would take the
			// session away over a formatter that will not start.
			if gated && !v.Denied() {
				v.Decision, v.Reason = DecisionAsk, h.Name
			}
			continue
		}
		if res.Note != "" {
			v.Notes = append(v.Notes, h.Name+": "+res.Note)
		}
		if res.Context != "" {
			v.Context = joinContext(v.Context, res.Context)
		}
		if len(res.UpdatedInput) > 0 && json.Valid(res.UpdatedInput) {
			v.Input = res.UpdatedInput
			v.Notes = append(v.Notes, h.Name+" rewrote the arguments")
		}
		switch res.Decision {
		case DecisionDeny:
			v.Decision, v.Reason = DecisionDeny, h.Name
			return v
		case DecisionAsk:
			if !v.Denied() {
				v.Decision, v.Reason = DecisionAsk, h.Name
			}
		}
	}
	return v
}

// one runs a single hook and reads its answer. It reports the response and
// whether the hook failed; a failure's Note is the sentence the surface says
// about it, because a hook that broke is a thing that happened to the session
// and the person is the only one who can fix it.
func (r *Runner) one(ctx context.Context, h Hook, p Payload) (Response, bool) {
	body, err := json.Marshal(p)
	if err != nil {
		// A payload this package built itself cannot fail to marshal, and
		// saying so is cheaper than a branch that pretends it can be handled.
		return Response{Note: h.Name + " could not be told what happened: " + err.Error()}, true
	}
	limit := r.ceiling
	if h.Timeout > 0 && h.Timeout < limit {
		limit = h.Timeout
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	out, code, err := r.exec(ctx, h.Command, append(body, '\n'))
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		// A hook that ran past its ceiling is a failure and not a refusal.
		// The seam has to answer either way, and waiting longer is the one
		// thing it cannot do.
		return Response{Note: fmt.Sprintf("%s did not finish within %s", h.Name, limit)}, true
	case err != nil:
		return Response{Note: h.Name + " did not run: " + err.Error()}, true
	}
	res := parse(out)
	switch code {
	case 0:
		return res, false
	case DenyExit:
		res.Decision = DecisionDeny
		if res.Note == "" {
			res.Note = firstLine(out)
		}
		if res.Note == "" {
			res.Note = "refused by " + h.Name
		}
		return res, false
	}
	note := fmt.Sprintf("%s exited %d", h.Name, code)
	if line := firstLine(out); line != "" {
		note += ": " + line
	}
	return Response{Note: note}, true
}

// parse reads a hook's answer off its stdout. Output that is not a JSON
// object is not an answer and is dropped: the common hook prints what it did
// and exits zero, and reading a formatter's file list as a malformed reply
// would make every working hook look broken.
//
// A decision this build has no meaning for is read as nothing said, for the
// reason a word in any other closed set is: a value nobody defined must not
// become one by being written down.
func parse(out string) Response {
	trimmed := strings.TrimSpace(out)
	if !strings.HasPrefix(trimmed, "{") {
		return Response{}
	}
	var res Response
	if err := json.Unmarshal([]byte(trimmed), &res); err != nil {
		return Response{}
	}
	switch res.Decision {
	case DecisionAllow, DecisionDeny, DecisionAsk:
	default:
		res.Decision = ""
	}
	return res
}

func joinContext(a, b string) string {
	if a == "" {
		return b
	}
	return a + "\n" + b
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(line)
}

// Lead puts what a hook wanted the model to read in front of a result, where
// the reduction pipeline and the repeat detector already put theirs: a notice
// under a hundred lines of output is one the model stopped reading before it
// arrived.
//
// A failure is told to the model as well as to the person. The call ran, so
// the result stands; but a session whose formatter is broken should not spend
// the next ten rounds wondering why the file is not formatted.
func (v Verdict) Lead(result string) string {
	var lines []string
	if v.Context != "" {
		lines = append(lines, v.Context)
	}
	if len(v.faults) > 0 {
		lines = append(lines, "[hook] "+strings.Join(v.faults, "; "))
	}
	if len(lines) == 0 {
		return result
	}
	return strings.Join(lines, "\n") + "\n" + result
}

// Executor runs one tool call and answers with its result text. It is the
// shape every dispatcher in the tree already has, so the wrap below can be
// written once rather than once per surface.
type Executor func(name string, args json.RawMessage) (string, error)

// WrapExecutor puts the tool seams inside one tier's dispatcher.
//
// gated names the calls this surface puts to a decision instead. Those skip
// the seam in front of the call, which the surface's own approval path owns,
// and keep the one behind it — a hook behind a call has no tier to break,
// because the call has already run. What a hook in front may change is the
// arguments: its answer has no field that names a tool, so the call that runs
// is a call of the kind that was dispatched, whatever it said.
// See docs/capabilities/hooks.md#a-hook-cannot-move-a-call-between-tiers.
//
// The predicate is asked when a call is dispatched rather than when the chain
// is built: on both surfaces the thing that knows what is gated is assembled
// after the chain is.
func (r *Runner) WrapExecutor(at func() Pos, gated func(name string, args json.RawMessage) bool, next Executor) Executor {
	if r == nil {
		return next
	}
	return func(name string, args json.RawMessage) (string, error) {
		ctx, call := context.Background(), Call{Name: name, Arguments: string(args)}
		var pre Verdict
		if gated == nil || !gated(name, args) {
			pre = r.PreTool(ctx, at(), call, false)
			if pre.Denied() {
				return DeniedResult(pre.Reason), nil
			}
			if pre.Input != nil {
				args = pre.Input
				call.Arguments = string(args)
			}
		}
		out, err := next(name, args)
		if err != nil {
			return out, err
		}
		// The hook behind the call is told what the call produced, not what
		// the hook in front of it prepended: the outcome and the result are
		// both about the call, and reading them off annotated text would have
		// a failure with a note in front of it come back as a success.
		post := r.PostTool(ctx, at(), call, out, Outcome(out))
		return pre.Lead(post.Lead(out)), nil
	}
}

// The outcome words, and the rule for reading one off a result: the "error:"
// prefix every executor in the tree follows.
//
// They are declared here rather than imported from the package that already
// holds them because this package is imported by the settings, and the
// settings should not acquire the provider and the whole tool set to know two
// words. A test holds them equal to the record's, which is the thing that
// would otherwise silently split.
const (
	OutcomeOK    = "ok"
	OutcomeError = "error"
)

// Outcome is how one call came out.
func Outcome(result string) string {
	if strings.HasPrefix(result, "error:") {
		return OutcomeError
	}
	return OutcomeOK
}

// DeniedResult is what the model reads for a call a hook refused. It says the
// call will not run however it is spelled, so no rounds are spent rephrasing
// it, and it names the hook rather than the file the hook is written in: the
// list is the person's, and a refusal carrying the instructions for editing
// it would be handing over the way around it — the same reason the deny
// list's own refusal names none.
// See docs/capabilities/hooks.md#a-hooks-deny-is-a-rule-denial.
func DeniedResult(reason string) string {
	who := "a hook this session runs"
	if reason != "" {
		who = "the " + reason + " hook"
	}
	return "error: " + who + " refused this call. It is refused in every permission mode and no " +
		"approval can allow it, so retrying it or rephrasing it will not run it. Say what you were " +
		"trying to do and let the user decide."
}

// AskedResult is what the model reads where a hook asked for the call to be
// put to a person and there was none: an unattended run. It is not the
// refusal above, because nothing about the call is settled — the same call in
// a session would draw a card — and telling the model otherwise would have it
// abandon work a person could have approved in a second.
func AskedResult(reason string) string {
	who := "a hook this session runs"
	if reason != "" {
		who = "the " + reason + " hook"
	}
	return "error: " + who + " asked for this call to be put to a person, and this run has nobody to ask. " +
		"It was not run. The same call in an interactive session would be a decision somebody could make."
}

// DenyRule is the short name a refused call's row carries beside who decided.
// It is short because it is drawn in a column; the hook's own name follows it.
func DenyRule(name string) string {
	if name == "" {
		return "hook"
	}
	return "hook " + name
}
