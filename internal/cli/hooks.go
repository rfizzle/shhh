package cli

// Wiring the person's own commands to the seams the session already has:
// where a hook is read from, what it runs under, and the read-only
// dispatcher's half of the tool seams.
//
// The other half is in the approval queue and in the headless approver,
// because a call that goes to a person and a call that runs on its own are
// dispatched by different code and a hook sits inside one of them at a time.
// See docs/capabilities/hooks.md#a-hook-cannot-move-a-call-between-tiers.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/hook"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/shell"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/chat"
)

// hookSet is the hooks this checkout and this person declare. The config
// file's entries come first and the checkout's file shadows them by name,
// and the checkout's file is read only where the checkout is trusted: a hook
// is a command line that runs as whoever cloned the repository
// (docs/capabilities/approvals-and-safety.md#a-checkout-declares-what-it-runs).
func hookSet(cfg config.Config) *hook.Set {
	if cfg.Hooks.Disabled {
		return nil
	}
	projectFile := ""
	// The root and the answer about it come from the same reading, so a
	// session cannot load a checkout's hooks under one and report the other.
	if t := projectTrust(); t.Root != "" && t.Allows() {
		projectFile = filepath.Join(t.Root, filepath.FromSlash(project.HooksFile))
	}
	set := hook.Load(cfg.Hooks.Entries, config.WritePath(), projectFile)
	if set.Len() == 0 && len(set.Diagnostics) == 0 {
		return nil
	}
	return set
}

// hookExec is how a hook's command line actually runs: through the session's
// execution shell, with the session's environment, wrapped by whatever
// contains the assistant's own commands — and with the payload on its stdin,
// which is the one thing the shared capture cannot do.
//
// It is not the shared runner for a second reason, and the more important
// one: that runner hands a command still printing at its ceiling to the
// process supervisor, and a hook that has run past its ceiling is a failure
// rather than something to keep alive. A seam has to answer, and waiting
// longer is the one thing it cannot do
// (docs/capabilities/hooks.md#a-hook-that-runs-too-long-has-failed).
func hookExec(wrap func(string) ([]string, error)) hook.Exec {
	return func(ctx context.Context, command string, stdin []byte) (string, int, error) {
		argv := shell.Execution().Argv(command)
		if wrap != nil {
			// The wrap is asked per run rather than resolved once, the way a
			// contained command's own policy is: the working scope grows
			// mid-session, and an argv built from the scope as it stood would
			// go on refusing a directory the person has since granted.
			wrapped, err := wrap(command)
			if err != nil {
				return "", -1, fmt.Errorf("sandbox: %w", err)
			}
			argv = wrapped
		}
		if len(argv) == 0 {
			return "", -1, fmt.Errorf("empty command")
		}
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Env = runner.Environ()
		cmd.Stdin = bytes.NewReader(stdin)
		// stdout is the answer and stderr is the hook's own noise, so they are
		// kept apart: a formatter that logs to stderr must not turn its reply
		// into text that will not parse.
		out := tools.NewCaptureBuffer(tools.MaxCapturedOutputBytes)
		cmd.Stdout, cmd.Stderr = out, nil
		// A hook that leaves a child holding the pipe would otherwise keep the
		// wait open past the kill the deadline sends.
		cmd.WaitDelay = time.Second
		err := cmd.Run()
		var exitErr *exec.ExitError
		switch {
		case err == nil:
			return out.String(), 0, nil
		case errors.As(err, &exitErr):
			return out.String(), exitErr.ExitCode(), nil
		}
		return out.String(), -1, err
	}
}

// buildHooks is the session's runner, or nil where there is nothing to fire.
// contain is what the session's commands run under; a session that contains
// nothing runs its hooks bare, which is the same statement made once.
func buildHooks(cfg config.Config, set *hook.Set, wrap func(string) ([]string, error), cwd string) *hook.Runner {
	if set.Len() == 0 {
		return nil
	}
	return hook.NewRunner(set, hookExec(wrap), cfg.HookCeiling(), cwd)
}

// hookPostMutation is the post-tool seam for the two tools no executor
// dispatches: a write and an edit go through the mutating dispatcher, which
// is the one place they can be seen. It rides the mutation hook the language
// server already uses, because that is the seam and there is no reason for a
// second one.
func hookPostMutation(r *hook.Runner) chat.MutationHook {
	if r == nil {
		return nil
	}
	return func(name string, args json.RawMessage, result string) string {
		v := r.PostTool(context.Background(), hook.Pos{},
			hook.Call{Name: name, Arguments: string(args)}, result, hook.Outcome(result))
		return v.Lead(result)
	}
}

// chainMutation runs two mutation hooks in order, and answers with whichever
// of them exists when only one does. The seam holds one function and two
// things want it; joining them here keeps the model's field a single hook.
func chainMutation(first, second chat.MutationHook) chat.MutationHook {
	switch {
	case first == nil:
		return second
	case second == nil:
		return first
	}
	return func(name string, args json.RawMessage, result string) string {
		return second(name, args, first(name, args, result))
	}
}

// hookSession is the session id a hook is told, which is the record's own row
// for this session: a hook that keeps its own notes can join them to the
// table without shhh inventing a second identifier for the same sitting. A
// session whose record could not be opened has none, and says so by sending
// nothing.
func hookSession(id int64) string {
	if id == 0 {
		return ""
	}
	return strconv.FormatInt(id, 10)
}

// hookNotes is what the surfaces print about a set that did not fully load,
// one line each. A hook that will not load is a diagnostic and never a reason
// a session does not start.
func hookNotes(set *hook.Set) []string { return set.Notes() }

// hookApprover is both tool seams around a run's gated calls: the approver is
// the dispatcher for that tier here, exactly as the approval queue is in a
// session.
//
// It wraps the approver rather than reaching inside it because the approver's
// own refusals are a branch per tool and a hook is about the call: one seam
// instead of five, at the cost of a hook running on a call a standing refusal
// was going to answer anyway — which decides nothing differently, because a
// hook cannot allow.
//
// An unattended run has nobody to ask, so a hook that asked, or that failed on
// a call it was asked about, does not run it. It is told apart from a refusal
// in what the model reads: nothing about the call is settled, and the same
// call in a session would draw a card
// (docs/capabilities/hooks.md#nothing-decides-yes-on-a-failure).
func hookApprover(r *hook.Runner, at func() hook.Pos, record func(decision, reason string), next func(provider.ToolCall) string) func(provider.ToolCall) string {
	if r == nil {
		return next
	}
	return func(tc provider.ToolCall) string {
		ctx := context.Background()
		call := hook.Call{ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments}
		pre := r.PreTool(ctx, at(), call, true)
		hookNoteLine(pre)
		if pre.Denied() || pre.Asked() {
			if record != nil {
				record(observe.DecisionDeny, observe.ReasonHook)
			}
			if pre.Asked() {
				return hook.AskedResult(pre.Reason)
			}
			return hook.DeniedResult(pre.Reason)
		}
		if pre.Input != nil {
			tc.Arguments = string(pre.Input)
			call.Arguments = tc.Arguments
		}
		out := next(tc)
		// And the seam behind it, for every gated call but a write: a write
		// is dispatched through the mutating tools and meets the seam on the
		// mutation hook, which is the one place a write can be seen. Firing
		// here as well would put one call to one hook twice.
		if tools.IsMutating(tc.Name) {
			return pre.Lead(out)
		}
		post := r.PostTool(ctx, at(), call, out, hook.Outcome(out))
		hookNoteLine(post)
		return pre.Lead(post.Lead(out))
	}
}

// hookPos is where an unattended run is now. It is one turn by construction,
// the same reading the run's own observer takes.
func hookPos(rounds func() int) func() hook.Pos {
	return func() hook.Pos { return hook.Pos{Turn: 1, Round: int64(rounds())} }
}

// hookNoteLine is how a run with nobody in front of it says what a hook said:
// stderr, beside its other activity, because stdout is the answer.
func hookNoteLine(v hook.Verdict) {
	for _, note := range v.Notes {
		fmt.Fprintf(os.Stderr, "» hook %s\n", note)
	}
}
