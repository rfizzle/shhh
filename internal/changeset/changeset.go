// Package changeset records what each turn changed: one record per file with
// the content on both sides, so reviewing a turn, undoing it and summarising
// it all read from one place instead of re-deriving the change from the
// transcript or from git (S-097).
//
// The store is the session's own memory of its edits. It is deliberately not
// a git operation — the records work in a directory that was never a
// repository, and undo restores content from here rather than from an index
// or a stash. Where git is present the store notes whether each file was
// tracked at the time of the edit, which is the input to the reversibility
// line on approval and plan cards.
//
// Nothing here re-diffs: edit_file and write_file already know both sides of
// the edit (S-074), so this is a recording layer. Hunks and the +N −M counts
// are computed once, on the way in, and every read after that is a field.
package changeset

import (
	"sort"
	"sync"
	"time"

	"github.com/rfizzle/shhh/internal/diff"
)

// MainAgent attributes a record to the session's own agent, as opposed to a
// named sub-agent whose patch was applied into the workspace.
const MainAgent = "main"

// DefaultMaxBytes bounds a store's retained content — the before and after
// text of every record it holds. Turns are evicted oldest-first past it.
const DefaultMaxBytes int64 = 16 << 20

// Origin says how an edit came to be applied, which is the difference between
// "you approved this" and "the session approved it for you".
type Origin int

const (
	// Approved: the user answered the approval card.
	Approved Origin = iota
	// AutoApproved: mode policy, a session grant, or the auto-mode
	// classifier waved the call through.
	AutoApproved
	// ChildPatch: a sub-agent's worktree patch was applied to the workspace.
	ChildPatch
)

func (o Origin) String() string {
	switch o {
	case AutoApproved:
		return "auto-approved"
	case ChildPatch:
		return "child patch"
	default:
		return "approved"
	}
}

// Tracking is whether git knew about a file when it was edited. Unknown is
// the honest answer outside a repository — it is not the same as untracked,
// and the surfaces that read it say so differently.
type Tracking int

const (
	TrackUnknown Tracking = iota
	TrackTracked
	TrackUntracked
)

func (t Tracking) String() string {
	switch t {
	case TrackTracked:
		return "tracked"
	case TrackUntracked:
		return "untracked"
	default:
		return "unknown"
	}
}

// Record is one file's net change within one turn. Callers fill everything
// down to At; Hunks and the counts are computed by the store, so a caller
// cannot report a diff that disagrees with the content it recorded.
type Record struct {
	Path string
	// Before and After are the whole file on each side. Exists distinguishes
	// an empty file from a missing one, which is what makes a creation and a
	// deletion tellable apart.
	Before, After             string
	BeforeExists, AfterExists bool
	// Agent is MainAgent for the session's own edits, or the child's name.
	Agent  string
	Origin Origin
	Track  Tracking
	At     time.Time

	Hunks          []diff.Hunk
	Added, Removed int
}

// Created reports a file the turn brought into existence.
func (r Record) Created() bool { return !r.BeforeExists && r.AfterExists }

// Deleted reports a file the turn removed.
func (r Record) Deleted() bool { return r.BeforeExists && !r.AfterExists }

// Changed reports whether the record says anything at all — a call that left
// the file byte-identical is not a change, however it was applied.
func (r Record) Changed() bool {
	return r.Before != r.After || r.BeforeExists != r.AfterExists
}

func (r *Record) compute() {
	r.Hunks = diff.Compute(r.Before, r.After)
	r.Added, r.Removed = diff.Stats(r.Hunks)
}

func (r Record) size() int64 { return int64(len(r.Before) + len(r.After) + len(r.Path)) }

// AgentStat is one agent's share of a turn: which files it authored and how
// much of the turn's +N −M is its doing.
type AgentStat struct {
	Name           string
	Files          int
	Added, Removed int
}

// Turn is everything one turn changed. The aggregates are maintained on
// write, so the summary row and the rail read fields rather than walking the
// records.
type Turn struct {
	N       int64
	At      time.Time
	Records []Record

	Added, Removed int
	// Hunks is the total hunk count across the turn's records.
	Hunks int
	// Agents is per-agent attribution in first-edit order.
	Agents []AgentStat
}

// Files is how many files the turn touched.
func (t Turn) Files() int { return len(t.Records) }

// Record returns the turn's record for path.
func (t Turn) Record(path string) (Record, bool) {
	for _, r := range t.Records {
		if r.Path == path {
			return r, true
		}
	}
	return Record{}, false
}

// snapshot copies the turn so a reader cannot see a later Add mutate it.
func (t *Turn) snapshot() Turn {
	out := *t
	out.Records = append([]Record(nil), t.Records...)
	out.Agents = append([]AgentStat(nil), t.Agents...)
	return out
}

// recount rebuilds the aggregates from the records. It runs on every write —
// a turn holds a handful of files — so that reads stay field accesses.
func (t *Turn) recount() {
	t.Added, t.Removed, t.Hunks = 0, 0, 0
	t.Agents = nil
	at := map[string]int{}
	for _, r := range t.Records {
		t.Added += r.Added
		t.Removed += r.Removed
		t.Hunks += len(r.Hunks)
		i, ok := at[r.Agent]
		if !ok {
			i = len(t.Agents)
			at[r.Agent] = i
			t.Agents = append(t.Agents, AgentStat{Name: r.Agent})
		}
		t.Agents[i].Files++
		t.Agents[i].Added += r.Added
		t.Agents[i].Removed += r.Removed
	}
}

// Store holds the session's turns, bounded by the total content it retains.
// It is safe for concurrent use: records are built off the UI goroutine, on
// the same goroutine that applied the edit.
type Store struct {
	mu      sync.Mutex
	max     int64
	bytes   int64
	order   []int64
	turns   map[int64]*Turn
	evicted []int64
}

// New returns a store bounded by maxBytes of retained content; zero or less
// uses DefaultMaxBytes.
func New(maxBytes int64) *Store {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	return &Store{max: maxBytes, turns: map[int64]*Turn{}}
}

// Add records one applied edit against a turn and returns the turns this
// write evicted, so the caller can say so rather than losing them silently.
//
// A file edited several times in one turn collapses to one net record: the
// earliest Before is kept, the latest everything else wins, and the hunks are
// recomputed across the pair. A record that changes nothing is dropped.
func (s *Store) Add(turn int64, r Record) (evicted []int64) {
	if s == nil || !r.Changed() {
		return nil
	}
	if r.Agent == "" {
		r.Agent = MainAgent
	}
	if r.At.IsZero() {
		r.At = time.Now()
	}
	r.compute()

	s.mu.Lock()
	defer s.mu.Unlock()

	t, ok := s.turns[turn]
	if !ok {
		t = &Turn{N: turn, At: r.At}
		s.turns[turn] = t
		s.order = append(s.order, turn)
		sort.Slice(s.order, func(i, j int) bool { return s.order[i] < s.order[j] })
	}
	if i := indexOf(t.Records, r.Path); i >= 0 {
		prev := t.Records[i]
		s.bytes -= prev.size()
		r.Before, r.BeforeExists = prev.Before, prev.BeforeExists
		r.compute()
		if !r.Changed() {
			// The turn edited the file back to where it started.
			t.Records = append(t.Records[:i], t.Records[i+1:]...)
			t.recount()
			return s.evict()
		}
		t.Records[i] = r
	} else {
		t.Records = append(t.Records, r)
	}
	s.bytes += r.size()
	t.recount()
	return s.evict()
}

func indexOf(records []Record, path string) int {
	for i, r := range records {
		if r.Path == path {
			return i
		}
	}
	return -1
}

// evict drops whole turns, oldest first, until the store is inside its bound.
// The newest turn is never evicted: a single oversized turn is worth more
// than a session with no record of what just happened.
func (s *Store) evict() []int64 {
	var dropped []int64
	for s.bytes > s.max && len(s.order) > 1 {
		n := s.order[0]
		t := s.turns[n]
		for _, r := range t.Records {
			s.bytes -= r.size()
		}
		delete(s.turns, n)
		s.order = s.order[1:]
		s.evicted = append(s.evicted, n)
		dropped = append(dropped, n)
	}
	return dropped
}

// Turn returns one turn's changeset. The second result is false for a turn
// that changed nothing and for one that was evicted — Evicted tells them
// apart, which is how undo refuses with an explanation rather than a shrug.
func (s *Store) Turn(n int64) (Turn, bool) {
	if s == nil {
		return Turn{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.turns[n]
	if !ok {
		return Turn{}, false
	}
	return t.snapshot(), true
}

// Turns returns every retained turn, oldest first.
func (s *Store) Turns() []Turn {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Turn, 0, len(s.order))
	for _, n := range s.order {
		out = append(out, s.turns[n].snapshot())
	}
	return out
}

// Latest is the most recent retained turn.
func (s *Store) Latest() (Turn, bool) {
	if s == nil {
		return Turn{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.order) == 0 {
		return Turn{}, false
	}
	return s.turns[s.order[len(s.order)-1]].snapshot(), true
}

// Evicted lists the turns dropped to stay inside the byte bound, oldest
// first.
func (s *Store) Evicted() []int64 {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int64(nil), s.evicted...)
}

// WasEvicted reports whether turn n was recorded and then dropped.
func (s *Store) WasEvicted(n int64) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.evicted {
		if e == n {
			return true
		}
	}
	return false
}

// Bytes is the content the store currently retains.
func (s *Store) Bytes() int64 {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bytes
}

// Totals aggregates every retained turn: how many distinct files the session
// changed and its net +N −M. A file changed by two turns counts once.
func (s *Store) Totals() (files, added, removed int) {
	for _, f := range s.Session() {
		files++
		a, d := diff.Stats(f.Hunks)
		added += a
		removed += d
	}
	return files, added, removed
}

// Session collapses every retained turn into one net change per file, in
// first-edit order — the session diff /diff renders, computed from the
// session's own records instead of shelling out to git.
func (s *Store) Session() []diff.File {
	if s == nil {
		return nil
	}
	type pair struct {
		before, after             string
		beforeExists, afterExists bool
	}
	var paths []string
	at := map[string]*pair{}
	for _, t := range s.Turns() {
		for _, r := range t.Records {
			p, ok := at[r.Path]
			if !ok {
				p = &pair{before: r.Before, beforeExists: r.BeforeExists}
				at[r.Path] = p
				paths = append(paths, r.Path)
			}
			p.after, p.afterExists = r.After, r.AfterExists
		}
	}
	var files []diff.File
	for _, path := range paths {
		p := at[path]
		hunks := diff.Compute(p.before, p.after)
		if len(hunks) == 0 {
			continue
		}
		files = append(files, diff.File{Path: path, Hunks: hunks})
	}
	return files
}
