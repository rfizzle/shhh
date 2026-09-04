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
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/project"
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

// Checkable reports whether the turn changed anything the repository's own
// checks could have an opinion about.
//
// A turn whose every write landed under the state directory changed shhh's
// own bookkeeping — a plan, a backlog item, a run's checkpoint — and no
// suite in any workspace has a verdict about those. Running one anyway
// spends a build and a test suite of an unattended run's wall clock to be
// told again exactly what the run before it was told. A turn that changed
// nothing at all is the same answer for the simpler reason.
func (t Turn) Checkable() bool {
	for _, r := range t.Records {
		if checkable(r.Path) {
			return true
		}
	}
	return false
}

// AnyCheckable is the same question asked of a bare list of paths, for the
// surfaces that keep no changeset and read what they wrote off the calls
// that wrote it.
func AnyCheckable(paths []string) bool {
	for _, p := range paths {
		if checkable(p) {
			return true
		}
	}
	return false
}

// checkable reads the whole path rather than its prefix, because the same
// file arrives here spelled both ways: a tool that was given a relative path
// records one, and a patch applied from a child's worktree records the
// absolute path it landed at.
func checkable(path string) bool {
	for _, seg := range strings.Split(filepath.ToSlash(path), "/") {
		if seg == project.StateDir {
			return false
		}
	}
	return path != ""
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
	// records is where the turns are written down so they outlive the
	// process, and slot names the conversation they are written under. Both
	// empty is a store that only ever remembers, which is what a session
	// without persistence — and every test that does not ask for it — gets.
	records Records
	slot    string
}

// TurnRecords is one turn's records as a store holds them: the number the
// close row shows and what that turn changed.
type TurnRecords struct {
	Turn    int64
	Records []Record
}

// Records is where a store writes what it recorded, so a turn survives the
// terminal being closed. Without one the session's memory of its own edits
// ends with the sitting, which makes shutting the window the same act as
// accepting every edit the session made.
//
// It is an interface here rather than a dependency on the store, because
// nothing about recording a change is about SQLite: the same three questions
// — write this turn, read these turns back, how far did the numbering get —
// are answerable by anything that can keep bytes.
type Records interface {
	// SaveChange writes one file's record for a turn, replacing what that
	// path held there. seq is the record's place in the turn, which is the
	// order it is read back in.
	SaveChange(slot string, turn int64, seq int, r Record) error
	// DropChange removes a path's record from a turn — the turn edited the
	// file back to where it found it, so it has no change of it left.
	DropChange(slot string, turn int64, path string) error
	// LoadChanges reads back the turns from `from` onwards, oldest first; a
	// `to` of zero or less has no upper bound.
	LoadChanges(slot string, from, to int64) ([]TurnRecords, error)
	// LastChangeTurn is the highest turn number the slot holds, or zero.
	LastChangeTurn(slot string) (int64, error)
}

// Persist wires where the store writes what it records. Nothing already in
// memory is written by this call: it is made before the session's first edit,
// and a store that back-filled here would write another conversation's turns
// under this one's name.
func (s *Store) Persist(rec Records) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = rec
}

// SetSlot names the conversation the records are kept under, and is called
// again every time the session's slot moves. It redirects what is written
// from here on and leaves what is already written where it is — the turns
// under the old name belong to the conversation that was in it.
func (s *Store) SetSlot(name string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.slot = name
}

// Persists reports whether the store writes what it records down. The
// surfaces that speak about a turn being dropped need it: with a record
// behind them, eviction costs this process its copy and costs the person
// nothing, and a line saying the turn can no longer be undone would be
// telling them to give up on work they can still have back.
func (s *Store) Persists() bool {
	rec, slot := s.sink()
	return rec != nil && slot != ""
}

// LastTurn is the highest turn number the slot's written-down records carry,
// and zero when there are none or nothing was wired. A resumed conversation
// numbers its next turn past it, so the number on a close row addresses the
// same turn in the sitting that wrote it and in every sitting after.
func (s *Store) LastTurn() int64 {
	rec, slot := s.sink()
	if rec == nil || slot == "" {
		return 0
	}
	last, err := rec.LastChangeTurn(slot)
	if err != nil {
		return 0
	}
	return last
}

// Recall is Turn reaching past what the store still holds in memory into what
// it wrote down: a turn evicted to stay inside the byte bound, and a turn from
// a sitting that has already ended, both come back here. The byte bound is
// about how much this process keeps at once, and it stopped being the bound on
// what can be undone the moment the records were written down.
func (s *Store) Recall(n int64) (Turn, bool) {
	if t, ok := s.Turn(n); ok {
		return t, true
	}
	rec, slot := s.sink()
	if rec == nil || slot == "" {
		return Turn{}, false
	}
	stored, err := rec.LoadChanges(slot, n, n)
	if err != nil || len(stored) == 0 {
		return Turn{}, false
	}
	return turnFrom(stored[0]), true
}

// Since is every turn from n onwards, oldest first — what a rewind has to put
// back to take the workspace to where it stood before turn n. Memory answers
// for the turns it still holds and the written record for the rest, so a
// rewind reaches turns this process has evicted and turns it never made.
func (s *Store) Since(n int64) []Turn {
	if s == nil {
		return nil
	}
	held := map[int64]bool{}
	var turns []Turn
	for _, t := range s.Turns() {
		if t.N >= n {
			held[t.N] = true
			turns = append(turns, t)
		}
	}
	rec, slot := s.sink()
	if rec != nil && slot != "" {
		if stored, err := rec.LoadChanges(slot, n, 0); err == nil {
			for _, tr := range stored {
				if !held[tr.Turn] {
					turns = append(turns, turnFrom(tr))
				}
			}
		}
	}
	sort.Slice(turns, func(i, j int) bool { return turns[i].N < turns[j].N })
	return turns
}

// sink reads the two fields a written record needs, under the lock, so a
// caller can do the reading and writing outside it.
func (s *Store) sink() (Records, string) {
	if s == nil {
		return nil, ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.records, s.slot
}

// turnFrom rebuilds a turn from records that were read back. The hunks and
// the counts are computed here rather than stored, so what a restored turn
// says about itself cannot disagree with the content it holds.
func turnFrom(tr TurnRecords) Turn {
	t := Turn{N: tr.Turn}
	for _, r := range tr.Records {
		r.compute()
		if t.At.IsZero() || r.At.Before(t.At) {
			t.At = r.At
		}
		t.Records = append(t.Records, r)
	}
	t.recount()
	return t
}

// Fold collapses a run of turns, oldest first, into one turn's worth of net
// records: the earliest before side of each path and the latest after side,
// which together are what putting the whole run back has to restore. A path a
// later turn edited back to where an earlier one found it nets to nothing and
// is dropped — the run's state, not its history.
//
// It is the same merge Add makes within one turn, made across several, so a
// rewind's restore and an undo's are one plan built one way and reviewed
// through one confirm.
func Fold(turns []Turn) Turn {
	out := Turn{}
	if len(turns) == 0 {
		return out
	}
	out.N, out.At = turns[0].N, turns[0].At
	at := map[string]int{}
	for _, t := range turns {
		for _, r := range t.Records {
			i, held := at[r.Path]
			if !held {
				at[r.Path] = len(out.Records)
				r.compute()
				out.Records = append(out.Records, r)
				continue
			}
			prev := out.Records[i]
			r.Before, r.BeforeExists, r.BeforeMode = prev.Before, prev.BeforeExists, prev.BeforeMode
			if r.AfterMode == 0 && r.AfterExists {
				// The later turn did not read the mode, so the mode the
				// earlier one left is still what is on disk (Add).
				r.AfterMode = prev.AfterMode
			}
			r.compute()
			out.Records[i] = r
		}
	}
	kept := out.Records[:0]
	for _, r := range out.Records {
		if r.Changed() {
			kept = append(kept, r)
		}
	}
	out.Records = kept
	out.recount()
	return out
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
			s.dropLocked(t.N, r.Path)
			return s.evict()
		}
		t.Records[i] = r
	} else {
		t.Records = append(t.Records, r)
	}
	s.bytes += r.size()
	t.recount()
	s.writeLocked(t.N, indexOf(t.Records, r.Path), r)
	return s.evict()
}

// writeLocked puts one record where it will outlive the process, and
// dropLocked takes one away again. One record and not the whole turn: the
// store is written to as each edit lands, so rewriting every row on every
// edit would cost a turn the square of the files it touched.
//
// Both run under the store's own lock, which is what keeps the written turn
// and the held one from disagreeing: two edits landing at once take the lock
// in some order, and whichever wrote second is the one both sides end up
// holding. Released first, they could invert and leave a stale record with a
// newer one in memory. The price is that a frame reading the store waits on
// one small statement, which is the cheaper of the two mistakes.
//
// A failure is swallowed on purpose, and it is the one place in this package
// that swallows one. The edit has already landed on disk and the record is
// already in memory, where every surface this sitting draws reads it — so
// refusing here would cost the session its account of what just happened to
// buy back nothing, and the cost of the failure is the narrower one it
// already is: the turn cannot be undone from a later sitting.
func (s *Store) writeLocked(turn int64, seq int, r Record) {
	if s.records == nil || s.slot == "" || seq < 0 {
		return
	}
	_ = s.records.SaveChange(s.slot, turn, seq, r)
}

func (s *Store) dropLocked(turn int64, path string) {
	if s.records == nil || s.slot == "" {
		return
	}
	_ = s.records.DropChange(s.slot, turn, path)
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
