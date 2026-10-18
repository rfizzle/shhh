package cli

// What a run with nobody in front of it does about a command's work and a
// claim. Its record of what it changed is the list of paths its own calls
// wrote; a command it runs is bracketed by two readings of the tree the way
// a session's is, and what became dirty in between joins that list, so the
// git stager stages a generator's output here as it does on the screen.
// A claim is answered by the classifier over its rows, or by a served
// client answering the call — never by a --yes given before the run.
// See docs/capabilities/approvals-and-safety.md#the-writing-half-of-git-is-a-tool-too.

import (
	"context"
	"encoding/json"

	"github.com/rfizzle/llm"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/structural"
	"github.com/rfizzle/shhh/internal/tools"
)

// bracketRun wraps a run's command runner so what each command changed in
// the tree joins wrote. A tree past the digest's bound records nothing, as
// the session's bracket does; a nil wrote is the runner unwrapped.
func bracketRun(dir string, run func(context.Context, string) tools.ExecResult, wrote func(paths ...string)) func(context.Context, string) tools.ExecResult {
	if run == nil || wrote == nil {
		return run
	}
	return func(ctx context.Context, command string) tools.ExecResult {
		root, before := quality.DirtyPaths(dir)
		result := run(ctx, command)
		if root == "" {
			return result
		}
		_, after := quality.DirtyPaths(dir)
		b := changeset.CommandBracket{Root: root, Before: before, Over: quality.PastContentBound}
		recs, _ := b.Records(after, command, nil)
		paths := make([]string, 0, len(recs))
		for _, r := range recs {
			paths = append(paths, r.Path)
		}
		wrote(paths...)
		return result
	}
}

// readClaims reads the claims a git add carries, and the refusal it meets
// before anything is asked: a reason missing, a path outside the workspace,
// a path the tree shows unchanged, or a run with nowhere to record one.
func (r headlessApproval) readClaims(tc llm.ToolCall) ([]structural.Claim, string) {
	if r.structTools == nil {
		return nil, ""
	}
	claims, err := r.structTools.Claims(json.RawMessage(tc.Arguments))
	if err != nil {
		return nil, "error: " + err.Error()
	}
	if len(claims) == 0 {
		return nil, ""
	}
	if err := r.structTools.CheckClaims(json.RawMessage(tc.Arguments)); err != nil {
		return nil, "error: " + err.Error()
	}
	if r.claimed == nil {
		return nil, "error: this run keeps no record of what it changed, so a claim cannot be staged from it"
	}
	return claims, ""
}

// recordClaims files answered claims in what this run wrote.
func (r headlessApproval) recordClaims(claims []structural.Claim) {
	if len(claims) == 0 || r.claimed == nil {
		return
	}
	paths := make([]string, 0, len(claims))
	for _, c := range claims {
		paths = append(paths, c.Abs)
	}
	r.claimed(paths...)
}
