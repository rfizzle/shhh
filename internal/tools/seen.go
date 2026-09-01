package tools

// What the model has actually been shown.
//
// The mutating tools trusted their arguments about the file underneath them.
// edit_file mostly gets away with it — old_text has to match, so a file that
// changed usually fails the match on its own — but write_file carries the
// whole new content and overwrites whatever is there, and neither tool had
// any way to tell "the model read this and is changing it" from "the model
// guessed at it".
//
// Both failures are silent, and both are worse in the runs nobody is
// watching. An interactive session shows a diff before applying anything, so
// a person sees a rewrite built on a stale read. A headless run with edits
// auto-approved sees nothing, and a sub-agent working in parallel with the
// session is exactly the thing that changes a file between one round and the
// next.
//
// So a read records what it showed, and a mutation checks that against what
// is there now. It is a fingerprint of the content rather than a timestamp
// because a modification time is a second-granularity clock on some
// filesystems, and the whole point is to catch two changes that happened
// close together.
// See docs/capabilities/approvals-and-safety.md#a-file-is-changed-from-what-was-read.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sync"
)

// seenFile is one file as a tool last showed it: the fingerprint of the whole
// file at that moment, and whether the model was shown all of it or only a
// window.
type seenFile struct {
	sum string
	// whole is false for a read that was given a line range or that hit the
	// output cap. It is the difference between changing a file and replacing
	// one, which is why write_file asks about it and edit_file does not.
	whole bool
}

// The record is process-wide because the files are. A session, its sub-agents
// and its background work all run here, and a writer in a worktree is keyed
// on its own path anyway — so one map, and a mutex because a round dispatches
// its read-only calls concurrently.
var (
	seenMu sync.Mutex
	seen   = map[string]seenFile{}
)

// seenKey is the path a record is filed under. Symlinks are followed so the
// same file reached two ways is one record; a path that cannot be resolved is
// filed as written, which is no worse than not recording it at all.
func seenKey(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

func fingerprint(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// noteShown records a file as the model has just been shown it.
func noteShown(path string, content []byte, whole bool) {
	seenMu.Lock()
	defer seenMu.Unlock()
	seen[seenKey(path)] = seenFile{sum: fingerprint(content), whole: whole}
}

// forget drops a file's record, for a path whose content is no longer
// knowable — the one case being a write that failed partway.
func forget(path string) {
	seenMu.Lock()
	defer seenMu.Unlock()
	delete(seen, seenKey(path))
}

func lookupSeen(path string) (seenFile, bool) {
	seenMu.Lock()
	defer seenMu.Unlock()
	rec, ok := seen[seenKey(path)]
	return rec, ok
}

// checkSeen reports whether a mutation may proceed against the file's current
// content. A file that does not exist yet is nobody's to be stale about, so
// existed=false always passes.
//
// The two mutating tools are held to different standards, because they carry
// different evidence that the model knows what it is changing.
//
// **Staleness applies to both.** A record that no longer matches the file
// means the model's picture of it is out of date, whatever it intends to do
// next. This is nearly free for edit_file, whose match usually fails anyway,
// and not free at all for replace_all or for an edit whose snippet survived a
// change around it.
//
// **Having read the file at all is asked only of a replacement.** old_text is
// its own evidence: a snippet that matches the file exactly and uniquely came
// from somewhere, and demanding a read_file on top of that would refuse an
// edit the model can make correctly from a search result. A full overwrite
// carries no such evidence — it says what the file will contain and nothing
// about what it contained — so that one has to have looked, and to have
// looked at all of it. Replacing a file from a windowed read is writing over
// the part that was never seen.
func checkSeen(path string, current []byte, existed, replacing bool) error {
	if !existed {
		return nil
	}
	rec, ok := lookupSeen(path)
	if !ok {
		if !replacing {
			return nil
		}
		return fmt.Errorf("%s has not been read in this session; read_file it first — this call would overwrite content that was never looked at", path)
	}
	if rec.sum != fingerprint(current) {
		return fmt.Errorf("%s has changed since it was read; read_file it again and rebase this change on what it says now", path)
	}
	if replacing && !rec.whole {
		return fmt.Errorf("%s was only read in part; read_file it in full before replacing it, or use edit_file to change the part you have read", path)
	}
	return nil
}
