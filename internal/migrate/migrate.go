// Package migrate is where a layout change lives once shhh has stopped
// reading the old layout.
//
// Nothing here runs on startup, and nothing here is a command of its own. A
// migration is a check in `shhh doctor`: it detects that this machine is
// still shaped the old way, says what that costs and what would move, and —
// where the move is one shhh can make unattended — offers to make it. That
// is the whole contract for every migration from here on
// (docs/capabilities/configuration.md#a-migration-is-a-doctor-check).
//
// The alternative was migrating silently at startup, and it is worse in both
// directions. A silent move is a directory the reader did not ask for and
// cannot see happening, on the one run where they are least able to reason
// about it; and a startup that reads two layouts forever is a startup that
// never stops paying for a decision that was supposed to be over. Detecting
// and offering costs one stat per run of a command nobody runs in a loop.
package migrate

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/rfizzle/shhh/internal/storage"
)

// Pending is one migration that has work left to do. Every field is a
// sentence for the reader, because the surface that shows this is a
// diagnostic and diagnostics do not editorialise: the doctor row prints
// Summary, the consequence line prints Consequence, and the lines behind
// `[f]` are Steps.
type Pending struct {
	// Name is what the migration is called, for the row that names it.
	Name string
	// Summary is the one line: what is still shaped the old way.
	Summary string
	// Consequence is what it costs to leave it — what the reader will see
	// because this has not been done.
	Consequence string
	// Steps are the moves, one line each, in the order they would happen.
	Steps []string
	// Apply carries the migration out and reports what it did, one line per
	// change, in the order they were made. A partial run reports what it
	// managed before the failure: the lines are the record of what actually
	// changed, and dropping them because the last step failed would leave the
	// reader unable to tell a run that did nothing from one that did most of
	// it.
	//
	// A migration shhh should not make on the reader's behalf leaves this nil.
	// Not every layout change is one a program can make correctly — some need
	// a person to decide which of two files is the one they meant — and the
	// doctor row for one of those states its steps and offers no key, because
	// an offer that cannot be honoured is worse than none (invariant 5).
	Apply func() ([]string, error)
}

// Auto reports whether shhh can carry this one out itself.
func (p Pending) Auto() bool { return p.Apply != nil }

// detectors is every migration shhh knows about, in the order they are
// reported. A detector answers false when this machine has nothing to do,
// which is the ordinary case and the one that must stay cheap. Each is given
// the checkout to look at, because a migration about a project is about the
// one the reader is in rather than wherever the process happens to stand.
var detectors = []func(string) (Pending, bool){
	legacyAppleDirs,
	legacyProjectFile,
}

// Plan is every migration with work outstanding on this machine, for a
// session in dir.
func Plan(dir string) []Pending {
	var pending []Pending
	for _, detect := range detectors {
		if p, ok := detect(dir); ok {
			pending = append(pending, p)
		}
	}
	return pending
}

// move is one path changing hands.
type move struct {
	from string
	to   string
}

// planMoves works out what relocating a set of old roots would do, without
// touching anything. An entry whose destination already holds something is a
// conflict rather than a move: the new layout has its own file there, and the
// whole point of doing this late and by hand is that shhh never silently
// replaces one of those with a copy from a directory it stopped reading.
//
// A destination that exists and holds nothing is not one of those — see
// vacant.
func planMoves(roots []relocation) (moves, conflicts []move) {
	for _, root := range roots {
		entries, err := os.ReadDir(root.from)
		if err != nil {
			// A root that is not there is the ordinary case, and one that
			// cannot be read is a root nothing can be planned from either.
			continue
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		sort.Strings(names)
		for _, name := range names {
			m := move{from: filepath.Join(root.from, name), to: filepath.Join(root.route(name), name)}
			if _, err := os.Lstat(m.to); err == nil && !vacant(m.to) {
				conflicts = append(conflicts, m)
				continue
			}
			moves = append(moves, m)
		}
	}
	return moves, conflicts
}

// relocation is one old root and the rule for where each thing in it belongs
// now. The rule is per-entry because the directory being retired held two
// kinds of state that the new layout keeps apart.
type relocation struct {
	from  string
	route func(name string) string
}

// applyMoves carries out the moves and then removes any old root left empty.
// A root with a conflict still in it is left alone: it is not empty, and
// removing it would take the conflict with it.
func applyMoves(moves []move, roots []relocation) ([]string, error) {
	var done []string
	for _, m := range moves {
		if err := os.MkdirAll(filepath.Dir(m.to), 0o700); err != nil {
			return done, err
		}
		// Anything standing at the destination was checked for emptiness when
		// the move was planned, and re-checked here rather than trusted,
		// because the plan and the apply are two different moments and a
		// session started in between could have written to it. Rename over an
		// existing directory is not portable either way.
		if _, err := os.Lstat(m.to); err == nil {
			if !vacant(m.to) {
				return done, fmt.Errorf("%s now holds something; left both in place", shortHome(m.to))
			}
			if err := os.RemoveAll(m.to); err != nil {
				return done, err
			}
		}
		if err := os.Rename(m.from, m.to); err != nil {
			// A rename across devices is the one failure worth naming: the
			// old directory and the new one can sit on different volumes,
			// and the error the kernel gives for that says nothing useful.
			return done, fmt.Errorf("move %s to %s: %w", m.from, m.to, err)
		}
		done = append(done, "moved "+shortHome(m.from)+" to "+shortHome(m.to))
	}
	for _, root := range roots {
		if err := os.Remove(root.from); err == nil {
			done = append(done, "removed "+shortHome(root.from))
		}
	}
	return done, nil
}

// vacant reports whether something already at a destination holds nothing, so
// that moving onto it loses the reader nothing.
//
// This is not a convenience. The first shhh command run after an upgrade
// opens the local store, which creates an empty one at the new path — so
// without this, the single most common migration on earth would report a
// conflict over a database nobody has ever written to, and every macOS user
// would be asked to delete a file by hand to get their own history back.
//
// It is deliberately narrow. An empty directory and a zero-byte file hold
// nothing by inspection. A store is asked whether it has recorded anything,
// because "empty" for a database is a question about rows and not about
// bytes — a fresh one is tens of kilobytes of schema. Everything else is
// occupied, including anything that will not answer: a destination shhh
// cannot read is one it must not delete.
func vacant(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if info.IsDir() {
		entries, err := os.ReadDir(path)
		return err == nil && len(entries) == 0
	}
	if filepath.Ext(path) == ".db" {
		recorded, err := storage.Recorded(path)
		return err == nil && !recorded
	}
	return info.Size() == 0
}
