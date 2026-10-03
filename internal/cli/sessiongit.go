package cli

import (
	"encoding/json"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/logs"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/structural"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/chat"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// treeCheck is the reading that tells a turn the tree moved under it, or nil
// when the config turned it off. The subtrahend is the front-end's: a
// session hands in its changeset, a headless run the paths its calls wrote.
//
// The record of what the model has been shown is not the front-end's — it is
// one record per process, kept by the tools that do the showing — so it is
// wired here, where both front-ends build their reading from one answer
// rather than two that can drift apart.
func treeCheck(cfg config.Config) *agent.TreeCheck {
	if !cfg.TreeCheckEnabled() {
		return nil
	}
	return &agent.TreeCheck{
		Dir:         ".",
		IsCommand:   func(name string) bool { return name == tools.ExecCommandName },
		ReadChanged: tools.SeenChanged,
		Log:         func(msg string) { logs.Logger().Warn(msg) },
	}
}

// gitSnapshot captures the workspace's git state for rewind checkpoints
// , so /rewind can report what diverged since a checkpoint.
//
// Whether the content was digested travels with the digest. Dropping it here
// would hand the rewind view a hash it cannot read the way the gate reads it:
// past the bound, two of them compare equal over different files, and the
// divergence line would report an unchanged tree on the strength of it.
func gitSnapshot() chat.GitSnapshot {
	fp := quality.TakeFingerprint(".")
	return chat.GitSnapshot{
		Repo: fp.Repo, Head: fp.Head, StatusHash: fp.StatusHash,
		DirtyPaths: fp.DirtyPaths, Unhashed: fp.Unhashed,
	}
}

// gitWriteGatedPreview is the card a git write asks through. Its fields are
// the boundaries of the act, and each is something the reader would otherwise
// have to know already: what happens to work that is not the session's,
// whether anything leaves the machine, and — for the one verb that cannot be
// taken back — whether the repository's own checks ran and what the way back
// is.
//
// `push` is stated on every one of them, not only on a commit, because the
// question a person asks when an agent touches git is whether it can reach
// the remote, and an answer that appears on some cards and not others is one
// the reader has to go looking for.
func gitWriteGatedPreview(st *structural.Toolset, args json.RawMessage) (chat.GatedPreview, error) {
	w, err := st.WritePlan(args)
	if err != nil {
		return chat.GatedPreview{}, err
	}
	hooks := chat.GatedField{Label: "hooks", Value: "run", Detail: "the checkout's own commit hooks; a failure cancels and changes nothing"}
	if !w.Hooks {
		hooks = chat.GatedField{Label: "hooks", Value: "pre-commit and commit-msg skipped", Detail: "the checkout is not trusted; its other hooks still run"}
	}
	fields := []chat.GatedField{
		{Label: "stages", Value: "this session's files only", Detail: "work that was already in the tree is never staged"},
		{Label: "push", Value: "no", Detail: "shhh never pushes; the remote is yours"},
	}
	switch w.Verb {
	case structural.CommitVerb:
		fields = append(fields, hooks,
			chat.GatedField{Label: "undo", Value: "git revert", Detail: components.CommitUndoNote})
	case structural.BranchVerb:
		fields = append(fields, chat.GatedField{Label: "undo", Value: "git branch -d",
			Detail: "a new branch moves no file and holds no work; deleting it is a line you type"})
	case structural.SwitchVerb:
		// The session does not come back with the branch: the files under it
		// are the other branch's now, and everything it had read is dropped
		// so nothing is rewritten from a picture taken over here.
		fields = append(fields, chat.GatedField{Label: "undo", Value: "git switch -",
			Detail: "the tree becomes the other branch's, and every file this session had read is re-read"})
	}
	return chat.GatedPreview{
		Title:    w.Title,
		Action:   w.Verb,
		Summary:  w.Summary,
		Fields:   fields,
		DenyLine: structural.WriteLine(args),
	}, nil
}
