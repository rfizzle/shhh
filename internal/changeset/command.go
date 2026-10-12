package changeset

// What a command changed. The file tools know both sides of every write; a
// command — a generator, a formatter, a sed — writes nothing through them, so
// the only way to know what it changed is to read the tree either side of it.
//
// The window is the command and not the session. A path dirty when the
// command started is left alone however it changed, because that is where
// the person's own uncommitted work and another session's are; a path that
// was clean when the command started and is dirty when it ends is the
// command's, and the command line goes on the record as its evidence. Two
// sessions running commands in one checkout in the same seconds can still
// cross, and the evidence is what makes a wrong attribution readable.
// See docs/capabilities/approvals-and-safety.md#the-writing-half-of-git-is-a-tool-too.

import (
	"os"
	"path/filepath"
)

// CommandBracket is a command's start, as the tree read then: the repository's
// top level and the paths it named dirty, as git names them from that top.
type CommandBracket struct {
	Root   string
	Before []string
	// Over reports a reading past the bound the tree digest keeps to — more
	// dirty paths, or more bytes in the new ones, than it will read. Past
	// it the bracket records nothing at all: a tree that large is one
	// nobody reviews in a turn, and recording part of it would make the
	// record a sample that reads as the whole. Nil never reports over.
	Over func(paths int, bytes int64) bool
}

// Records is the command's work: every path dirty in after that was not dirty
// before, read now, as a record with no before side. held reports a path the
// session already holds from a write that read both sides, which is not
// recorded again. over is true where the reading was past the bound, and then
// nothing is returned.
//
// A path that is gone is recorded as gone: a command that deleted a tracked
// file changed it as much as one that rewrote it.
func (b CommandBracket) Records(after []string, command string, held func(abs string) bool) (recs []Record, over bool) {
	if b.Root == "" {
		return nil, false
	}
	if b.Over != nil && (b.Over(len(b.Before), 0) || b.Over(len(after), 0)) {
		return nil, true
	}
	was := make(map[string]bool, len(b.Before))
	for _, p := range b.Before {
		was[p] = true
	}
	var bytes int64
	for _, rel := range after {
		if rel == "" || was[rel] {
			continue
		}
		abs := filepath.Join(b.Root, filepath.FromSlash(rel))
		if held != nil && held(abs) {
			continue
		}
		r := Record{Path: abs, Agent: MainAgent, Origin: ByCommand, Evidence: command}
		fi, err := os.Lstat(abs)
		switch {
		case err != nil:
			// Gone: recorded as gone.
		case fi.Mode()&os.ModeSymlink != 0:
			// A link is its target's text, which is what git stores for it.
			// Following it would copy whatever it points at — a key under
			// the home directory — into the record and the commit card.
			target, err := os.Readlink(abs)
			if err != nil {
				continue
			}
			r.After, r.AfterExists = target, true
		case !fi.Mode().IsRegular():
			// A directory porcelain names — a submodule — is not a file
			// this record can hold.
			continue
		default:
			// The bound is read off the size before the bytes are, so a
			// tree past it is never read into memory to find that out.
			if b.Over != nil && b.Over(len(recs)+1, bytes+fi.Size()) {
				return nil, true
			}
			data, err := os.ReadFile(abs)
			if err != nil {
				continue
			}
			r.After, r.AfterExists, r.AfterMode = string(data), true, fi.Mode().Perm()
		}
		bytes += int64(len(r.After))
		if b.Over != nil && b.Over(len(recs)+1, bytes) {
			return nil, true
		}
		recs = append(recs, r)
	}
	return recs, false
}
