package chat

// What the model's own command changed. The file tools record both sides of
// every write as they make it; a command writes nothing through them, so the
// session reads the tree either side of each command it runs for the model
// and records what became dirty in between as the command's work. That is
// what lets the writing half of git stage a generator's output, and what
// keeps the person's own uncommitted files — dirty before the command
// started — out of it.
// See docs/capabilities/approvals-and-safety.md#the-writing-half-of-git-is-a-tool-too.
//
// The bracket is here, around the runner the approved call is handed to, and
// not at the agent's executor: an execute_command call never reaches the
// executor in a session — it is carded and run by this model (run.go) — and
// this is the one place that holds both ends of one command, its line, and
// the turn it belongs to.

import (
	"fmt"
	"path/filepath"

	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/quality"
)

// commandWork is the bracket's reading, carried from the goroutine that ran
// the command back to the screen that records it.
type commandWork struct {
	records []changeset.Record
	// over says the tree was past the digest's bound, so nothing was read.
	over bool
}

// openCommandBracket reads the tree before a command runs. It reads the
// tree with quality.DirtyPaths, the reading the gate's digest is pinned to:
// raw NUL-separated names from the repository's top, untracked files listed
// one by one and ignored ones not at all — the same tree the person's
// verdicts talk about, rather than the backlog runner's reading, which drops
// the backlog's own paths and unquotes names by hand. Outside a repository
// there is nothing to read, and the bracket records nothing.
func openCommandBracket(workspace string) changeset.CommandBracket {
	if workspace == "" {
		return changeset.CommandBracket{}
	}
	root, before := quality.DirtyPaths(workspace)
	return changeset.CommandBracket{Root: root, Before: before, Over: quality.PastContentBound}
}

// closeCommandBracket reads the tree again and makes the command's records:
// what became dirty, less what a file tool already recorded in this turn,
// each marked generated where the project's generated block claims it, and
// with git's tracking answer taken now.
func closeCommandBracket(b changeset.CommandBracket, workspace, command string, store *changeset.Store, tracker *changeset.Tracker) commandWork {
	if b.Root == "" {
		return commandWork{}
	}
	_, after := quality.DirtyPaths(workspace)
	held := heldBySession(store, workspace)
	recs, over := b.Records(after, command, held)
	if over || len(recs) == 0 {
		return commandWork{over: over}
	}
	rels := make([]string, len(recs))
	for i, r := range recs {
		rels[i] = workspaceRel(workspace, r.Path)
	}
	generated := map[string]bool{}
	for _, g := range (&quality.Runner{Workspace: workspace}).Generated(rels) {
		generated[g] = true
	}
	for i := range recs {
		recs[i].Generated = generated[rels[i]]
		recs[i].Track = tracker.Track(recs[i].Path)
	}
	return commandWork{records: recs}
}

// heldBySession reports a path a retained turn already holds from a write
// that read both sides. Such a path is already the session's, so recording it
// again with no before side would only cost it its diff and its undo. The
// records name a path the way it was written, relative or absolute, so both
// spellings are compared.
func heldBySession(store *changeset.Store, workspace string) func(abs string) bool {
	held := map[string]bool{}
	for _, r := range knownRecords(store) {
		p := r.Path
		if !filepath.IsAbs(p) {
			p = filepath.Join(workspace, p)
		}
		p = filepath.Clean(p)
		held[p] = true
		if real, err := filepath.EvalSymlinks(p); err == nil {
			held[real] = true
		}
	}
	return func(abs string) bool {
		if held[filepath.Clean(abs)] {
			return true
		}
		real, err := filepath.EvalSymlinks(abs)
		return err == nil && held[real]
	}
}

// knownRecords is every retained record that read both sides.
func knownRecords(store *changeset.Store) []changeset.Record {
	var out []changeset.Record
	for _, t := range store.Turns() {
		for _, r := range t.Records {
			if !r.BeforeUnknown() {
				out = append(out, r)
			}
		}
	}
	return out
}

// workspaceRel is an absolute path as the workspace names it, slash-separated,
// read through a link in the workspace's own spelling.
func workspaceRel(workspace, abs string) string {
	roots := []string{workspace}
	if real, err := filepath.EvalSymlinks(workspace); err == nil && real != workspace {
		roots = append(roots, real)
	}
	for _, root := range roots {
		if r, err := filepath.Rel(root, abs); err == nil && r != ".." && !filepath.IsAbs(r) && !startsUp(r) {
			return filepath.ToSlash(r)
		}
	}
	return filepath.ToSlash(abs)
}

func startsUp(rel string) bool {
	return len(rel) >= 3 && rel[:3] == ".."+string(filepath.Separator)
}

// recordCommandWork files the bracket's records under the turn and says what
// it did: the words the command's row carries, and — once a session — the
// line that says a tree past the bound was not read at all.
func (m *Model) recordCommandWork(w commandWork) string {
	if w.over {
		if !m.commandWorkOver {
			m.commandWorkOver = true
			m.appendEntry(entry{kind: entrySystem, text: fmt.Sprintf(
				"what commands change is not being recorded: the working tree is past the %s bound the tree digest reads, so a file a command makes is staged only by a claim on the card",
				quality.ContentBound())})
		}
		return ""
	}
	if len(w.records) == 0 {
		return ""
	}
	var evicted []int64
	for _, r := range w.records {
		evicted = append(evicted, m.changes.Add(m.turnCount, r)...)
	}
	m.noteEvictedTurns(evicted)
	return commandWorkWords(w.records)
}

// commandWorkWords is the command row's account of what it recorded:
// `2 files recorded`, and how many of those the project calls generated.
func commandWorkWords(recs []changeset.Record) string {
	gen := 0
	for _, r := range recs {
		if r.Generated {
			gen++
		}
	}
	words := plural(len(recs), "file") + " recorded"
	if gen > 0 {
		words += fmt.Sprintf(" · %d generated", gen)
	}
	return words
}
