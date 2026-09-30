package radius

// The scratch reading: the one direction in which what the destruction
// reading proves lets a flagged command be judged rather than always asked. A
// recursive delete whose every target is proved to sit below the workspace
// root, and to hold nothing git could have brought back, is a clean-up of
// scratch, and auto mode's classifier may answer for it as it answers for
// any other command
// (docs/capabilities/approvals-and-safety.md#severity-moves-the-default).
//
// Every question here answers false where it cannot settle the answer — no
// repository, git refusing, a tree too large to walk, a link it cannot
// resolve — because false is the card the command always had.

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/hostgit"
	"github.com/rfizzle/shhh/internal/safety"
)

// ScratchDelete reports whether a command's whole danger is deleting scratch
// inside the workspace: every shape the safety table finds in it is a
// delete, the line read whole and each flagged delete read on its own are
// ProvenInside the workspace root, and every target they name is Untracked.
//
// The flagged deletes are read a second time, one by one, because the safety
// table and the destruction reading split a line differently: a delete the
// table found behind a prefix the destruction reading does not carry
// (`time rm -rf src`), or handed its operands from elsewhere (`xargs rm -rf`),
// would otherwise be a flagged delete with no target proved, beside another
// delete that had one. Read on its own it proves nothing, and the line is not
// scratch.
//
// Every other command on the line must be an inspection command, which
// changes nothing. The proof is taken before anything runs, so a command
// that moves or links a tracked tree to where a delete then points
// (`mv src .tmp/gone && rm -rf .tmp/gone`) would make the delete's target
// something the proof never saw. Both readings read past the shell's flow
// words, so a delete behind `then` or `!` is proved or not like any other;
// the words that close a conditional or a loop (`fi`, `done`) are not
// inspections, so a delete inside one keeps its card.
func ScratchDelete(command string, where Where) bool {
	findings := safety.Findings(command)
	if len(findings) == 0 {
		return false
	}
	for _, w := range findings {
		if !w.Deletes {
			return false
		}
	}
	all := Destroys(command, where)
	if !all.ProvenInside(where.Root) {
		return false
	}
	for _, line := range strings.Split(command, "\n") {
		for _, cmd := range safety.Commands(line) {
			if ws := safety.Check(cmd); len(ws) == 0 || !ws[0].Deletes {
				if !agent.ReadOnlyAllowed(cmd, nil) {
					return false
				}
				continue
			}
			one := Destroys(cmd, where)
			if !one.ProvenInside(where.Root) {
				return false
			}
			all.Targets = append(all.Targets, one.Targets...)
		}
	}
	return all.Untracked(where.Root)
}

// Untracked reports whether nothing the line destroys is something git could
// have brought back or a repository of its own keeps: root is inside a work
// tree whose index names no path at or under any target, no directory
// between root and a target and nothing under a target holds a `.git`, and
// nothing under a target is a link that leaves it. A target that is not there
// holds nothing. It answers false for a target outside root and for anything
// it cannot settle, so a failure to read is never read as scratch.
func (d Destruction) Untracked(root string) bool {
	if len(d.Targets) == 0 || root == "" {
		return false
	}
	r, err := physical(root)
	if err != nil {
		return false
	}
	paths := make([]string, 0, len(d.Targets))
	for _, t := range d.Targets {
		if !strings.HasPrefix(t.Path, r+string(filepath.Separator)) {
			return false
		}
		if nestedRepoAbove(r, t.Path) || !scratchTree(t.Path) {
			return false
		}
		paths = append(paths, t.Path)
	}
	return !gitTracks(r, paths)
}

// nestedRepoAbove reports whether a directory strictly between root and p
// holds a `.git`: p is then inside a repository of its own, whose files the
// outer index does not name and nothing outside it can bring back.
func nestedRepoAbove(root, p string) bool {
	for dir := filepath.Dir(p); dir != root && strings.HasPrefix(dir, root+string(filepath.Separator)); dir = filepath.Dir(dir) {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return true
		}
	}
	return false
}

// scratchTree walks the tree at p, without following links and under the
// describe walk's bound, and reports whether it holds no `.git` at any depth
// and no link that resolves outside it. A link that stays inside the tree is
// removed with it whatever reads it; one that leaves is where a delete that
// follows links would land, and a link nothing resolves is not proved to stay.
func scratchTree(p string) bool {
	if _, err := os.Lstat(p); err != nil {
		return os.IsNotExist(err)
	}
	ok, visited := true, 0
	err := filepath.WalkDir(p, func(at string, d fs.DirEntry, err error) error {
		if err != nil {
			ok = false
			return fs.SkipAll
		}
		visited++
		if visited > walkBound || d.Name() == ".git" {
			ok = false
			return fs.SkipAll
		}
		if d.Type()&fs.ModeSymlink != 0 {
			dest, err := filepath.EvalSymlinks(at)
			if err != nil || (dest != p && !strings.HasPrefix(dest, p+string(filepath.Separator))) {
				ok = false
				return fs.SkipAll
			}
		}
		return nil
	})
	return ok && err == nil
}

// gitTimeout bounds the one git call the scratch reading makes, which runs
// while a card is being decided on.
const gitTimeout = 5 * time.Second

// gitTracks reports whether the index of the work tree holding root names any
// path at or under paths, and answers true wherever git cannot say — outside a
// repository, on an error, past the timeout — since true keeps the card. The
// paths are literal, so a target named with a glob character is the file of
// that name and not a pattern git would widen.
func gitTracks(root string, paths []string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	args := append([]string{"--literal-pathspecs", "ls-files", "-z", "--cached", "--"}, paths...)
	out, err := hostgit.Command(ctx, root, args...).Output()
	return err != nil || len(out) > 0
}
