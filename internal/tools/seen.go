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
//
// The same record answers the same question at a round boundary, where being
// told early is worth a round: a file that moved is named to the model while
// it still has a round to re-read in, rather than at the moment its edit is
// refused. The time is not trusted there either — it only says which files
// are worth opening.
// See docs/capabilities/approvals-and-safety.md#a-file-is-changed-from-what-was-read.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
)

// seenFile is one file as a tool last showed it: the fingerprint of the whole
// file at that moment, the length and modification time it had then, and
// whether the model was shown all of it or only a window.
type seenFile struct {
	sum string
	// size and mod are the cheap half of "is this still what was shown". A
	// file whose length moved holds different content and needs no hash to
	// say so; one whose length and modification time are both unchanged is
	// taken as untouched. Only the remainder — the same length at a new time
	// — is worth opening, which is what keeps the boundary re-check off the
	// critical path of a round.
	size int64
	mod  time.Time
	// whole is false for a read that was given a line range or that hit the
	// output cap. It is the difference between changing a file and replacing
	// one, which is why write_file asks about it and edit_file does not.
	whole bool
}

// unknown reports a record made for a file something says was read without
// saying what it held — a conversation restored from the store, which carries
// its read_file calls and not the files those calls read. It matches no
// content, so the first mutation against such a file is refused as stale, and
// it is left out of the boundary re-check: nothing is known about what the
// file held, so nothing can be said about whether that changed.
func (f seenFile) unknown() bool { return f.sum == "" }

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

// noteShown records a file as the model has just been shown it. content is
// the whole file, so its length is the file's and no stat is asked for it;
// the modification time is, and a stat that fails leaves the zero time, which
// no real time matches — costing the re-check a hash rather than an answer.
//
// The time is read after the content, so a rewrite landing in that gap at the
// same length pairs the old fingerprint with the new time and the boundary
// re-check rules the file out until it moves again. That is the same window
// as a same-second same-length rewrite, and it ends the same way: checkSeen
// hashes unconditionally, so no mutation gets through on it.
func noteShown(path string, content []byte, whole bool) {
	key := seenKey(path)
	rec := seenFile{sum: fingerprint(content), size: int64(len(content)), whole: whole}
	if info, err := os.Stat(key); err == nil {
		rec.mod = info.ModTime()
	}
	seenMu.Lock()
	defer seenMu.Unlock()
	seen[key] = rec
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

// StaleError is the refusal for a file that changed between the read that
// showed it and the mutation built on that read. It is its own type because
// it is the one refusal here that is not the model's fault: another session,
// an editor or a background build moved the file, and the person watching is
// the only one who can say which. A surface that cannot tell it from a
// malformed call reports the wrong thing to the only party able to act.
type StaleError struct{ Path string }

// Error is the sentence the model is given: what happened, and the one move
// that fixes it. Anything less specific and the model retries the same edit.
func (e StaleError) Error() string {
	return e.Path + " has changed since it was read; read_file it again and rebase this change on what it says now"
}

// Skipped is the same refusal as a transcript row, with the path written the
// way the surface asking for it writes paths — workspace-relative, usually,
// rather than as the model spelled it. It lives beside the model's sentence
// because the session and a sub-agent both draw this row and they must draw
// the same one; two packages spelling it separately is how they stop
// agreeing. Weight is a system row rather than a card: nothing happened, and
// a refused call is not a decision anyone has to make.
// See docs/interface/principles.md#weight-tracks-risk.
func (e StaleError) Skipped(display string) string {
	if display == "" {
		display = e.Path
	}
	return "skipped · " + display + " changed since it was read"
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
		return StaleError{Path: path}
	}
	if replacing && !rec.whole {
		return fmt.Errorf("%s was only read in part; read_file it in full before replacing it, or use edit_file to change the part you have read", path)
	}
	return nil
}

// NoteUnknown records a path as one the model has been shown without knowing
// what it was shown, for a conversation taken back out of the store. The
// transcript says which files were read; nothing on this machine says what
// they held, and they have had however long the conversation was closed to
// move.
//
// Recorded this way, the first mutation against such a file is refused as
// stale and the model reads it again, which costs one round. The alternative
// is an edit applied to a picture nobody can vouch for, and that costs
// somebody's work. A path the restored transcript never read is not recorded
// at all and keeps the ordinary rule, because a quoted snippet is its own
// evidence.
// See docs/capabilities/approvals-and-safety.md#a-file-is-changed-from-what-was-read.
func NoteUnknown(path string) {
	seenMu.Lock()
	defer seenMu.Unlock()
	seen[seenKey(path)] = seenFile{}
}

// NoteRestoredReads records every file a conversation taken back out of the
// store says it read as one whose content is unknown.
//
// It lives here rather than at either door because both doors restore the
// same thing. A session reopened from the command line and a saved chat
// loaded over the one on screen both put a transcript in front of the model
// that names its readings and holds none of them, and a rule written twice is
// a rule that ends up meaning two things: the load door emptied the record
// and stopped, so the first edit to a file the loaded chat had read went
// through on the strength of its old_text alone.
//
// Only a call something answered counts: a call whose result never made it
// into the transcript is a round that was cut short, and its file was never
// put in front of the model. Paths are recorded as the old conversation
// spelled them, which for a relative path means against this process's
// directory — a conversation restored somewhere else is describing another
// checkout, and a record filed there is one nothing will ask about.
// See docs/capabilities/approvals-and-safety.md#a-file-is-changed-from-what-was-read.
func NoteRestoredReads(msgs []provider.Message) {
	answered := map[string]bool{}
	for _, m := range msgs {
		if m.Role == provider.RoleTool && m.ToolCallID != "" {
			answered[m.ToolCallID] = true
		}
	}
	for _, m := range msgs {
		for _, call := range m.ToolCalls {
			if call.Name != ReadFileName || !answered[call.ID] {
				continue
			}
			var args struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil || args.Path == "" {
				continue
			}
			NoteUnknown(args.Path)
		}
	}
}

// SeenChanged names every file the model has been shown whose content is no
// longer what it was shown, as absolute paths in sorted order.
//
// It is the question checkSeen asks at the mutation, asked at a round
// boundary instead. Asked only at the mutation, the model spends a round
// writing a change that cannot land; asked between rounds, it is told while
// there is still a round to re-read in. And it is the one reading git cannot
// do: porcelain names the paths that are dirty and says nothing about what is
// in them, so a file that was already dirty when somebody rewrote it in place
// has the same status line before and after — and the file being worked on is
// nearly always already dirty.
//
// The prefilter is what makes it affordable at that rate. A file whose length
// changed is different content by definition and is never opened; a file
// whose length and modification time are both unchanged is taken as
// untouched. A rewrite that lands in the same second at the same length slips
// past that and is still refused at the mutation, which hashes
// unconditionally.
// See docs/capabilities/approvals-and-safety.md#a-file-is-changed-from-what-was-read.
func SeenChanged() []string {
	seenMu.Lock()
	recs := make(map[string]seenFile, len(seen))
	for path, rec := range seen {
		if !rec.unknown() {
			recs[path] = rec
		}
	}
	seenMu.Unlock()

	// The stats and hashes happen outside the lock: a round dispatches its
	// read-only calls concurrently, and holding the record shut while this
	// walks the disk would serialise them behind it.
	var changed []string
	refreshed := map[string]seenFile{}
	for path, rec := range recs {
		info, err := os.Stat(path)
		switch {
		case err != nil:
			// Deleted, or no longer readable. Either way it is not what was
			// shown, and saying so is the direction that never lets a stale
			// mutation through.
			changed = append(changed, path)
			continue
		case info.Size() != rec.size:
			changed = append(changed, path)
			continue
		case info.ModTime().Equal(rec.mod):
			continue
		}
		content, err := os.ReadFile(path)
		if err != nil || fingerprint(content) != rec.sum {
			changed = append(changed, path)
			continue
		}
		// The same content at a new time: a touch, a rewrite of identical
		// bytes, a checkout that put the file back. Remembering the new time
		// is what stops the next boundary opening it all over again.
		rec.mod = info.ModTime()
		refreshed[path] = rec
	}
	sort.Strings(changed)

	seenMu.Lock()
	defer seenMu.Unlock()
	for path, rec := range refreshed {
		// Only where the record is still the one that was checked: a read
		// that landed while this was hashing knows more recent facts than
		// these, and must not be written over by them.
		if cur, ok := seen[path]; ok && cur.sum == rec.sum && cur.size == rec.size {
			seen[path] = rec
		}
	}
	return changed
}

// ForgetAll drops every record. The callers are the two ways one conversation
// gives way to another in a running process — a session ending and another
// beginning, and a saved conversation loaded over the one on screen. The
// records say what the model was shown, and the model that comes back has
// been shown nothing. Keeping them would let a full overwrite through on the
// strength of a read the new conversation never made — the one thing the
// record exists to refuse.
func ForgetAll() {
	seenMu.Lock()
	defer seenMu.Unlock()
	clear(seen)
}
