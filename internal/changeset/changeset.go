// Package changeset records what each turn changed: one record per file with
// the content on both sides, so reviewing a turn, undoing it and summarising
// it all read from one place instead of re-deriving the change from the
// transcript or from git.
//
// The store is the session's own memory of its edits. It is deliberately not
// a git operation — the records work in a directory that was never a
// repository, and undo restores content from here rather than from an index
// or a stash. Where git is present the store notes whether each file was
// tracked at the time of the edit, which is the input to the reversibility
// line on approval and plan cards.
//
// Nothing here re-diffs: edit_file and write_file already know both sides of
// the edit, so this is a recording layer. Hunks and the +N −M counts
// are computed once, on the way in, and every read after that is a field.
package changeset

import (
	"fmt"
	"os"
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
	// BeforeMode is the file's permission bits as the turn found them, and
	// zero where nothing read them: the file was not there, or the record
	// came by a path that does not stat. Zero reads as "unknown" and takes
	// the default, which also swallows a file that genuinely was mode 000 —
	// not worth a second field to tell apart, since nothing the session
	// writes produces one. Permission bits only: an edit never changed an
	// owner or a timestamp, so putting one back is not undo's to do.
	BeforeMode os.FileMode
	// AfterMode is the same reading taken once the edit had landed, and it
	// is what makes a change of permissions visible at all: a patch that
	// made a script executable and moved not a byte has identical content
	// on both sides, and this pair is the only thing that tells them apart.
	// Zero reads as unknown, the way BeforeMode does — the tools that write
	// files do not change modes, so their records leave it alone and say
	// nothing about permissions rather than claiming they were cleared.
	AfterMode os.FileMode
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
// the file byte-identical, and its permissions alone, is not a change,
// however it was applied.
func (r Record) Changed() bool {
	return r.Before != r.After || r.BeforeExists != r.AfterExists || r.ModeChanged()
}

// ModeChanged reports a record whose permission bits differ across the edit.
// The file has to be there on both sides — a creation and a deletion are
// changes of their own and neither has two modes to compare — and both modes
// have to be known: zero means nothing read one, so a record missing one says
// nothing about permissions rather than reporting a file stripped to 000.
func (r Record) ModeChanged() bool {
	return r.BeforeExists && r.AfterExists && modeChanged(r.BeforeMode, r.AfterMode)
}

// modeChanged is the comparison itself, shared by one record and by the fold
// of every record a session holds for a path, so a turn's change of
// permissions and the session's net change of them cannot disagree about what
// counts as one.
func modeChanged(before, after os.FileMode) bool {
	return before != 0 && after != 0 && before.Perm() != after.Perm()
}

// ModeOnly reports a record whose whole change is the permission bits. There
// is no diff to draw for one, so the surfaces state the mode where a file
// with hunks states its counts.
func (r Record) ModeOnly() bool {
	return r.ModeChanged() && r.Before == r.After
}

// ModeChange states the permission change in the one wording every surface
// prints it in, and is empty where there is none to state. The arrow reads
// the way a diff does, and the four digits are the octal a person types back
// into chmod.
func (r Record) ModeChange() string {
	if !r.ModeChanged() {
		return ""
	}
	return modeChange(r.BeforeMode, r.AfterMode)
}

// modeChange is where that wording is spelled, once, so a record and the fold
// of several of them cannot word one change two ways.
func modeChange(before, after os.FileMode) string {
	if !modeChanged(before, after) {
		return ""
	}
	return fmt.Sprintf("mode %04o → %04o", before.Perm(), after.Perm())
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

// ModeChange is the turn's change stated as permissions, for a turn whose
// whole change is one file's mode. Anything else is empty: two such files
// are two changes and a row states one thing, and a turn that also moved
// bytes has counts of its own to state.
func (t Turn) ModeChange() string {
	if len(t.Records) != 1 || !t.Records[0].ModeOnly() {
		return ""
	}
	return t.Records[0].ModeChange()
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
	// session is the last SessionFiles answer, held until a write makes it
	// wrong. Collapsing the session diffs every path it retains, and the
	// surfaces that ask are drawn from a render loop rather than from the
	// edit that changed something — so without this the same diffs are
	// recomputed several times between one edit and the next.
	session []SessionFile
	fresh   bool
}

// New returns a store bounded by maxBytes of retained content; zero or less
// uses DefaultMaxBytes.
func New(maxBytes int64) *Store {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	return &Store{max: maxBytes, turns: map[int64]*Turn{}}
}

// Reset empties the store, keeping the bound it was built with. It is what a
// session boundary does to the records of the conversation it ends: turns are
// numbered from one again on the other side, so a store that kept them would
// answer a review or an undo of turn 1 with the previous conversation's
// edits. The eviction list goes with them — it is the reason a turn is
// missing, and it is no longer true of any turn the store can be asked about.
func (s *Store) Reset() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bytes = 0
	s.order = nil
	s.evicted = nil
	s.turns = map[int64]*Turn{}
	s.session, s.fresh = nil, false
}

// Add records one applied edit against a turn and returns the turns this
// write evicted, so the caller can say so rather than losing them silently.
//
// A file edited several times in one turn collapses to one net record: the
// earliest before side is kept — content, existence and mode together, so
// the three never describe different moments — the latest everything else
// wins, and the hunks are recomputed across the pair. A record that changes
// nothing — the same bytes, the same existence and the same permissions —
// is dropped.
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
	s.session, s.fresh = nil, false

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
		r.Before, r.BeforeExists, r.BeforeMode = prev.Before, prev.BeforeExists, prev.BeforeMode
		if r.AfterMode == 0 && r.AfterExists {
			// The later edit did not read the mode, which for the tools that
			// write files means it did not change one either — so the mode
			// the earlier edit left is still what is on disk, and dropping it
			// here would lose a change the turn really made. A file the later
			// edit removed has no mode to carry forward.
			r.AfterMode = prev.AfterMode
		}
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

// SessionFile is one path's net change across every retained turn: the hunks
// between where the session found the file and where it left it, plus how
// many turns it took to get there. The turn count is what the inspector
// rail's `3t` field states — eight rows for one file is a log, not a state.
type SessionFile struct {
	Path           string
	Hunks          []diff.Hunk
	Added, Removed int
	// Turns is how many retained turns edited this path, and Last is the most
	// recent of them. One record per turn per path is guaranteed by Add, so
	// counting records counts turns.
	Turns int
	Last  int64
	// ModeChange states the session's net change of permissions, in the same
	// wording a single record states one in, and is empty where the session
	// left the mode where it found it. It is the whole reason a file can be
	// here with no hunks: a patch that made a script executable and moved not
	// a byte is a change of the workspace, and a row answering `+0 −0` about
	// it would report a measurement of nothing — which is what dropping the
	// file was avoiding, at the cost of not mentioning the file at all.
	// See docs/interface/principles.md#a-stat-that-cannot-be-reported-is-left-out.
	ModeChange string
}

// Totals aggregates every retained turn: how many distinct files the session
// changed and its net +N −M. A file changed by two turns counts once, and a
// file whose whole change is its permissions counts as a file with no lines
// to count — so a caller with both numbers at zero and a file in hand has
// something to state other than a pair of zeros.
func (s *Store) Totals() (files, added, removed int) {
	for _, f := range s.SessionFiles() {
		files++
		added += f.Added
		removed += f.Removed
	}
	return files, added, removed
}

// Session collapses every retained turn into one net change per file, in
// first-edit order — the session diff /diff renders, computed from the
// session's own records instead of shelling out to git.
func (s *Store) Session() []diff.File {
	fs := s.SessionFiles()
	if len(fs) == 0 {
		return nil
	}
	files := make([]diff.File, 0, len(fs))
	for _, f := range fs {
		files = append(files, diff.File{Path: f.Path, Hunks: f.Hunks})
	}
	return files
}

// SessionFiles is Session with the attribution the rail needs: one net change
// per path, in first-edit order, each carrying how many turns produced it and
// which turn touched it last. A path a turn edited back to where it started —
// content and permissions both — nets to nothing and is left out: the
// session's state, not its history.
func (s *Store) SessionFiles() []SessionFile {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.fresh {
		s.session, s.fresh = s.sessionFilesLocked(), true
	}
	// The rows are handed out as a copy: the walk behind them is held, and a
	// caller that sorted or appended to what it was given would otherwise be
	// editing every later caller's answer.
	return append([]SessionFile(nil), s.session...)
}

// sessionFilesLocked is the walk itself. It reads the turns directly rather
// than through Turns(), because the cache it fills has to be filled under the
// same lock that a write clears it under.
func (s *Store) sessionFilesLocked() []SessionFile {
	type acc struct {
		before, after             string
		beforeExists, afterExists bool
		beforeMode, afterMode     os.FileMode
		turns                     int
		last                      int64
	}
	var paths []string
	at := map[string]*acc{}
	for _, n := range s.order {
		t := s.turns[n]
		for _, r := range t.Records {
			p, ok := at[r.Path]
			if !ok {
				p = &acc{before: r.Before, beforeExists: r.BeforeExists, beforeMode: r.BeforeMode}
				at[r.Path] = p
				paths = append(paths, r.Path)
			}
			p.after, p.afterExists = r.After, r.AfterExists
			if r.AfterMode != 0 || !r.AfterExists {
				// A later turn that did not read the mode did not change one
				// either, so the mode an earlier turn left is still the one on
				// disk and carrying it forward is what keeps that change
				// visible. A turn that removed the file leaves no mode at all.
				p.afterMode = r.AfterMode
			}
			p.turns++
			p.last = t.N
		}
	}
	var files []SessionFile
	for _, path := range paths {
		p := at[path]
		hunks := diff.Compute(p.before, p.after)
		// The two modes are compared only where the file is there on both
		// sides, for the reason one record's are: a file the session created
		// or removed is that, and has no pair of readings to tell apart.
		mode := ""
		if p.beforeExists && p.afterExists {
			mode = modeChange(p.beforeMode, p.afterMode)
		}
		if len(hunks) == 0 && mode == "" {
			continue
		}
		added, removed := diff.Stats(hunks)
		files = append(files, SessionFile{
			Path: path, Hunks: hunks,
			Added: added, Removed: removed,
			Turns: p.turns, Last: p.last,
			ModeChange: mode,
		})
	}
	return files
}
