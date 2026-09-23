package run

// A sprint is runs with a session between them: the same machine as a single
// item, driven over the ready list one item at a time, each in a conversation
// of its own. What lives here is only what the loop needs and a single run
// does not — which item is being worked, which have been attempted, and why
// the loop stopped — so the item's own checkpoint goes on meaning exactly
// what it meant before.
//
// The definition of done is the run's terminal state and nothing the model
// says. Every loop runner in the field keys on a sentence the model is asked
// to print, and every one of them has a story about the model printing it
// early; here an item is done when a real commit landed and the item was
// archived, which is a transition the machine makes rather than a word it
// reads.
// See docs/capabilities/todo.md#a-sprint-is-runs-with-a-session-between-them.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/todo"
)

// The closed set of reasons a sprint stops. A driver reports one of these
// and never a sentence of its own, so a script reading the end of a sprint
// reads the same word the transcript shows.
const (
	// SprintEmpty is nothing left that can be started: every remaining item
	// waits on another, is blocked, or the backlog is finished.
	SprintEmpty = "empty"
	// SprintCapped is a cap the sprint was asked for reached: --max items
	// started, or the set's spend at or past its ceiling.
	SprintCapped = "capped"
	// SprintBlocked is an item that blocked. A sprint working one item at a
	// time stops on the first one: a blocked item wrote a follow-up, and the
	// next ready item may depend on the work that did not land. One working
	// several at once goes on with the items that do not, and ends on this
	// word once nothing more can be taken.
	SprintBlocked = "blocked"
	// SprintStopped is the person ending it.
	SprintStopped = "stopped"
)

// Sprint is the loop's checkpoint, written beside the item checkpoints so a
// sprint that dies with its process is picked up by the same command in a
// fresh one — which is the whole reason the state is a file rather than a
// field on whatever is driving it.
type Sprint struct {
	Session string    `json:"session"`
	Started time.Time `json:"started"`
	Updated time.Time `json:"updated"`
	// Current is the slug being worked, empty between items and once the
	// sprint has ended.
	Current string `json:"current,omitempty"`
	// ItemStarted is when the current item was taken, which is what the
	// wall-clock cap is measured from.
	ItemStarted time.Time `json:"item_started,omitempty"`
	// Done are the slugs whose runs reached done, in the order they did.
	Done []string `json:"done,omitempty"`
	// Attempts are the slugs the sprint has started, however they ended.
	// One attempt per item per sprint: the runner's remediation rounds are
	// the retry, and a second run over an item the first one could not
	// finish is a second chance at the same failure.
	Attempts []string `json:"attempts,omitempty"`
	// Max is how many items the sprint may start, 0 for as many as are
	// ready.
	Max int `json:"max,omitempty"`
	// NoCommit and PrevMode are the answers the sprint was asked for, kept
	// because every item after the first is started from this file rather
	// than from the command that is no longer on screen.
	NoCommit bool   `json:"no_commit,omitempty"`
	PrevMode string `json:"prev_mode,omitempty"`
	// Turns and Cost are what the set has spent so far. They are added up
	// here rather than read back from a ledger because a sprint crosses a
	// session boundary between every two items and the ledger is reset at
	// each one — the running total has to outlive the sessions it was spent
	// in, and this file is the only thing that does.
	Turns int     `json:"turns,omitempty"`
	Cost  float64 `json:"cost,omitempty"`
	// ItemTurns and ItemCost are what the item in flight has spent so far,
	// read off the ledger ItemLedger names and written at every stage
	// boundary, so a process that dies mid-item leaves the stages it paid for
	// on disk. They are a running figure, replaced at each boundary rather
	// than added to, and they are not part of Turns and Cost: the item's end
	// adds its whole figure there and clears them (Spent), and a sprint picked
	// up by anything but the writer adds them there first (Resume), because
	// the writer is gone and its ledger with it. Either way a stage is
	// counted once. A checkpoint written before these existed reads as
	// nothing in flight.
	ItemLedger string  `json:"item_ledger,omitempty"`
	ItemTurns  int     `json:"item_turns,omitempty"`
	ItemCost   float64 `json:"item_cost,omitempty"`
	// CapCents is the most the set may spend, in cents, and 0 for no
	// ceiling. It is checked against Cost before an item is taken and never
	// inside one: what stops a request mid-item is the session's own cap,
	// and a sprint ceiling that cut a stage in half would leave a tree
	// nothing has read. Cents rather than dollars so the ceiling asked for
	// is the ceiling kept, the way every other spend setting is written.
	// See docs/capabilities/todo.md#a-sprint-is-runs-with-a-session-between-them.
	CapCents int64 `json:"cap_cents,omitempty"`
	// Parallel is how many items the sprint may work at once, and 0 or 1
	// for one at a time, which is every sprint written before it existed.
	// Above one the items in flight are Lanes rather than Current.
	// See docs/capabilities/todo.md#a-sprint-can-work-several-items-at-once.
	Parallel int `json:"parallel,omitempty"`
	// Lanes are the items being worked at once, each in its own copy of the
	// checkout. They are written by one writer under a lock (the driver's),
	// the way every other field here is, so two lanes finishing together
	// cannot each write a checkpoint that forgets the other.
	Lanes []SprintLane `json:"lanes,omitempty"`
	// Blocked are the items of a parallel sprint that blocked, one line
	// each with the evidence. A blocked lane does not stop the sprint — the
	// other lanes' items are not resting on it, or they would not have been
	// ready — so what it stopped is remembered here until nothing more can
	// be taken, and the sprint ends blocked on them then.
	Blocked []string `json:"blocked,omitempty"`
	// Ended is one of the words above once the sprint is over, and Reason
	// the evidence behind it.
	Ended  string `json:"ended,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// SprintLane is one item a parallel sprint is working: which, where, how far
// it has got, and what it has spent so far. The spend is the lane's running
// figure in the sense ItemTurns and ItemCost are the serial sprint's —
// replaced at every stage boundary, added to the set's total once, when the
// item ends or when the sprint is picked up by a process that is not the one
// that wrote it.
type SprintLane struct {
	Slug string `json:"slug"`
	// Paths are what the item declared it touches, and none for an item
	// that declared nothing — which is worked alone.
	Paths []string `json:"paths,omitempty"`
	// Tree is the copy of the checkout the item is worked in, and
	// Checkpoint the item's own run checkpoint, which names the stage.
	Tree       string `json:"tree,omitempty"`
	Checkpoint string `json:"checkpoint,omitempty"`
	// Stage is the step the item's run is at, as the lane last wrote it.
	Stage   Stage     `json:"stage,omitempty"`
	Started time.Time `json:"started,omitempty"`
	Ledger  string    `json:"ledger,omitempty"`
	Turns   int       `json:"turns,omitempty"`
	Cost    float64   `json:"cost,omitempty"`
}

// StartSprint begins a sprint. prevMode is the mode to put the session back
// into when the last item is finished.
func StartSprint(session, prevMode string, max int, noCommit bool) *Sprint {
	now := time.Now()
	return &Sprint{
		Session: session, Started: now, Updated: now,
		Max: max, NoCommit: noCommit, PrevMode: prevMode,
	}
}

// sprintFile is the loop's checkpoint, beside the per-item ones.
const sprintFile = "sprint.json"

func sprintPath(root string) string { return filepath.Join(Dir(root), sprintFile) }

// Save writes the checkpoint.
func (s *Sprint) Save(root string) error {
	s.Updated = time.Now()
	if err := os.MkdirAll(Dir(root), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(sprintPath(root), data, 0o644)
}

// LoadSprint reads the checkpoint.
func LoadSprint(root string) (*Sprint, error) {
	data, err := os.ReadFile(sprintPath(root))
	if err != nil {
		return nil, err
	}
	var s Sprint
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", sprintPath(root), err)
	}
	return &s, nil
}

// DiscardSprint removes the checkpoint. A sprint that has ended has nothing
// left to pick up: the item it stopped on keeps its own checkpoint, and the
// command that continues that item names it.
func DiscardSprint(root string) { _ = os.Remove(sprintPath(root)) }

// Live is the sprint on disk when there is one still going, and false
// otherwise — no file, a file that will not parse, or one already ended.
// Every surface asking "is a sprint going" asks it this way, so a corrupt
// file reads as no sprint rather than as a sprint nobody can end.
func Live(root string) (*Sprint, bool) {
	s, err := LoadSprint(root)
	if err != nil || s.Over() {
		return nil, false
	}
	return s, true
}

// Over reports that the sprint has stopped.
func (s *Sprint) Over() bool { return s == nil || s.Ended != "" }

// Next is the item the sprint works next, taken from the store's ready list
// — which is the sprint file's slugs in its order where the backlog holds
// one, and the whole ready list otherwise. It answers false when the sprint
// is over, having recorded which of the closed reasons ended it.
//
// An item the sprint has already attempted is passed over. The runner puts a
// finished item in the archive and a blocked one out of the ready list on its
// own, so this guard only fires for an item some other surface put back — but
// the loop must not be able to start the same item twice whatever else
// happened to it, and that is not a property a status field can hold.
func (s *Sprint) Next(store *todo.Store) (todo.Item, bool) {
	if s.Over() {
		return todo.Item{}, false
	}
	if s.Max > 0 && len(s.Attempts) >= s.Max {
		s.end(SprintCapped, fmt.Sprintf("%s attempted, which is the cap the sprint was asked for", plural(len(s.Attempts), "item")))
		return todo.Item{}, false
	}
	if s.overCap() {
		s.end(SprintCapped, fmt.Sprintf("spent %s of the %s the sprint was allowed", Dollars(s.Cost), CapDollars(s.CapCents)))
		return todo.Item{}, false
	}
	it, ok := s.Peek(store)
	if !ok {
		s.end(SprintEmpty, "nothing is ready: every open item waits on another, or the backlog is empty")
		return todo.Item{}, false
	}
	s.Current, s.ItemStarted = it.Slug, time.Now()
	s.Attempts = append(s.Attempts, it.Slug)
	return it, true
}

// Peek is the item Next would take, without taking it. Two surfaces say
// which item comes next before it is started — the board, and the first row
// on the far side of a session boundary — and neither of them may consume
// the choice, so choosing is here and taking is what Next adds to it.
//
// It answers false wherever Next would end the sprint, so a caller asking
// "what is next" and getting nothing has the same answer the loop is about
// to reach, without the ending being recorded twice.
func (s *Sprint) Peek(store *todo.Store) (todo.Item, bool) {
	if s.Over() || (s.Max > 0 && len(s.Attempts) >= s.Max) || s.overCap() {
		return todo.Item{}, false
	}
	for _, it := range store.Ready() {
		if !s.attempted(it.Slug) {
			return it, true
		}
	}
	return todo.Item{}, false
}

// Laned reports the sprint working several items at once.
func (s *Sprint) Laned() bool { return s != nil && s.Parallel > 1 }

// TakeLane is the item a free lane starts next, taken; false where no lane
// may start one now. An item is taken beside the lanes already running only
// where the paths it declares meet none of theirs, and an item that declares
// none is taken only once every lane has drained, and then alone — it is
// serialised, never refused. The ready list's order holds across that: an
// undeclared item at the head of the list stops later items being taken
// past it, so the lanes drain and it gets its turn, where a declared item
// that overlaps a running lane only waits for that lane and the list moves on
// past it, the way the parallel batch done by hand moves on.
//
// It ends the sprint only when nothing is running and nothing can be taken,
// with the reason the serial loop gives — the cap, the ceiling, or an empty
// ready list — or blocked where items blocked on the way: a lane that blocks
// does not stop the sprint, and what it stopped is said at the end.
func (s *Sprint) TakeLane(store *todo.Store) (todo.Item, bool) {
	if s.Over() || len(s.Lanes) >= max(s.Parallel, 1) {
		return todo.Item{}, false
	}
	switch {
	case s.Max > 0 && len(s.Attempts) >= s.Max:
		s.drained(SprintCapped, fmt.Sprintf("%s attempted, which is the cap the sprint was asked for", plural(len(s.Attempts), "item")))
		return todo.Item{}, false
	case s.overCap():
		s.drained(SprintCapped, fmt.Sprintf("spent %s of the %s the sprint was allowed", Dollars(s.Cost), CapDollars(s.CapCents)))
		return todo.Item{}, false
	}
	for _, l := range s.Lanes {
		if len(l.Paths) == 0 {
			return todo.Item{}, false
		}
	}
	for _, it := range store.Ready() {
		if s.attempted(it.Slug) {
			continue
		}
		paths, declared := it.Touches()
		if !declared && len(s.Lanes) > 0 {
			break
		}
		if declared && s.overlapsLane(paths) {
			continue
		}
		s.Attempts = append(s.Attempts, it.Slug)
		s.Lanes = append(s.Lanes, SprintLane{Slug: it.Slug, Paths: paths, Started: time.Now()})
		return it, true
	}
	if len(s.Blocked) > 0 {
		s.drained(SprintBlocked, strings.Join(s.Blocked, "; "))
	} else {
		s.drained(SprintEmpty, "nothing is ready: every open item waits on another, or the backlog is empty")
	}
	return todo.Item{}, false
}

// drained ends the sprint for why, but only once no lane is running: a
// parallel sprint that has stopped taking items still owes the ones in
// flight their ending.
func (s *Sprint) drained(word, why string) {
	if len(s.Lanes) == 0 {
		s.end(word, why)
	}
}

// overlapsLane reports a declared path list meeting a running lane's, by the
// rule a session's writers are queued by (subagent.ClaimOverlap): a sprint
// serialises two items the way a session queues two writers.
func (s *Sprint) overlapsLane(paths []string) bool {
	for _, l := range s.Lanes {
		if _, ok := subagent.ClaimOverlap(paths, l.Paths); ok {
			return true
		}
	}
	return false
}

// Lane is the running lane working slug, for the driver to write to.
func (s *Sprint) Lane(slug string) (*SprintLane, bool) {
	for i := range s.Lanes {
		if s.Lanes[i].Slug == slug {
			return &s.Lanes[i], true
		}
	}
	return nil, false
}

// LaneEnded retires a lane whose item is over: its spend joins the set's
// total, and the item is recorded as done or, with its evidence, as blocked.
// Neither ends the sprint; TakeLane does that once nothing is left to take.
// The spend handed over is the item's whole figure, which is why the lane's
// running one goes with the lane rather than being added as well.
func (s *Sprint) LaneEnded(slug string, done bool, why string, turns int, cost float64) {
	for i, l := range s.Lanes {
		if l.Slug == slug {
			s.Lanes = append(s.Lanes[:i], s.Lanes[i+1:]...)
			break
		}
	}
	s.Turns += max(turns, 0)
	if cost > 0 {
		s.Cost += cost
	}
	if done {
		if !slices.Contains(s.Done, slug) {
			s.Done = append(s.Done, slug)
		}
		return
	}
	s.Blocked = append(s.Blocked, slug+" blocked — "+oneLine(why))
}

// Orphans takes the lanes a dead process left on the checkpoint off it and
// hands them back for the driver to answer for. What each had spent on a
// ledger other than the picking-up session's is added to the total here, for
// the reason Resume adds the serial item's: that ledger died with its
// process. The lanes themselves cannot be continued — each was working in a
// copy of the checkout that belonged to the process that is gone.
func (s *Sprint) Orphans() []SprintLane {
	out := s.Lanes
	s.Lanes = nil
	for _, l := range out {
		if l.Ledger != s.Session {
			s.Turns += max(l.Turns, 0)
			s.Cost += max(l.Cost, 0)
		}
	}
	return out
}

// InFlight is what the items being worked have spent so far and not yet
// added to the total: the serial item's running figure and every lane's.
func (s *Sprint) InFlight() (turns int, cost float64) {
	turns, cost = s.ItemTurns, s.ItemCost
	for _, l := range s.Lanes {
		turns += l.Turns
		cost += l.Cost
	}
	return turns, cost
}

// Working is the slugs being worked now: the lanes of a parallel sprint in
// the order they were taken, or the one item of a serial one.
func (s *Sprint) Working() []string {
	if len(s.Lanes) == 0 {
		if s.Current != "" {
			return []string{s.Current}
		}
		return nil
	}
	out := make([]string, 0, len(s.Lanes))
	for _, l := range s.Lanes {
		out = append(out, l.Slug)
	}
	return out
}

// stopFile is how a surface that is not the sprint's own process asks it to
// stop: a file beside the checkpoint that the runner looks for, rather than a
// signal to a process id the checkpoint recorded, which the machine may have
// handed to something else since.
const stopFile = "sprint.stop"

// RequestStop asks the process working the sprint to stop.
func RequestStop(root string) error {
	if err := os.MkdirAll(Dir(root), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(Dir(root), stopFile), nil, 0o644)
}

// StopRequested reports a stop having been asked for.
func StopRequested(root string) bool {
	_, err := os.Stat(filepath.Join(Dir(root), stopFile))
	return err == nil
}

// ClearStop takes the request away: once it has been answered, and before a
// sprint starts that a request left over from an earlier one must not end.
func ClearStop(root string) { _ = os.Remove(filepath.Join(Dir(root), stopFile)) }

// Spent adds one item's cost to the set's running total. It is called at the
// session boundary, where what the item cost is still readable and about to
// be reset. The item's running figure is cleared with it, since what it was a
// reading of is in the total now.
func (s *Sprint) Spent(turns int, cost float64) {
	s.Turns += max(turns, 0)
	if cost > 0 {
		s.Cost += cost
	}
	s.ItemLedger, s.ItemTurns, s.ItemCost = "", 0, 0
}

// Running records what the item in flight has spent so far on ledger, at a
// stage boundary. It replaces the figure the last boundary wrote rather than
// adding to it: the driver hands over the item's whole spend on that ledger
// each time, so a boundary written twice is still a stage counted once. The
// ledger is the session's name where the session's own ledger is read — the
// name Resume compares with Session — and something else where the writer
// can never be the one to pick the sprint up.
func (s *Sprint) Running(ledger string, turns int, cost float64) {
	s.ItemLedger, s.ItemTurns, s.ItemCost = ledger, max(turns, 0), max(cost, 0)
}

// overCap reports the set's running total at or past its ceiling. The total
// is the finished items' alone — the one in flight is added at its session
// boundary — which is why the check is made before an item is taken and not
// during one.
func (s *Sprint) overCap() bool {
	return s.CapCents > 0 && s.Cost >= float64(s.CapCents)/100
}

// Bound sets the sprint's ceiling from the places it is asked for: the
// command's flag where one was given, the ceiling a checkpoint already
// carries where the sprint is being continued, the setting otherwise — the
// flag first, as every key with a flag above it resolves. A continued sprint
// keeps its own ceiling over the setting because that ceiling is an answer
// the sprint was started under, and a setting read again in a new process is
// not somebody asking for a different one. Both drivers resolve it here, so a
// session and a script given the same answers carry the same ceiling.
func (s *Sprint) Bound(flag, setting int64) {
	switch {
	case flag > 0:
		s.CapCents = flag
	case s.CapCents <= 0:
		s.CapCents = max(setting, 0)
	}
}

// SpendWords is what the set has spent against its ceiling, in the words
// every surface that states it uses — the board's head, the rail's row for
// the item in flight, and the unattended runner's line between two items:
// `spend $4.10 of $20`. cost is the caller's, because only the caller knows
// whether the item in flight has been added to the checkpoint yet. It is
// empty without a ceiling: a figure with nothing to measure it against is
// the board's own spend reading, not this one.
func SpendWords(cost float64, capCents int64) string {
	if capCents <= 0 {
		return ""
	}
	return "spend " + SpendFigure(cost, capCents)
}

// SpendFigure is the set's spend as the closed set's notes carry it: against
// the ceiling where there was one, on its own where there was not, and empty
// where nothing was spent and nothing was capped.
func SpendFigure(cost float64, capCents int64) string {
	switch {
	case capCents > 0:
		return Dollars(max(cost, 0)) + " of " + CapDollars(capCents)
	case cost > 0:
		return Dollars(cost)
	}
	return ""
}

// Dollars is an amount to the cent, which is what a ceiling in cents is
// compared against.
func Dollars(cost float64) string { return fmt.Sprintf("$%.2f", cost) }

// CapDollars is a ceiling in the dollars it was asked for, without the cents
// where it was a whole number: `$20` rather than `$20.00`, which reads as a
// figure that was measured rather than one that was chosen.
func CapDollars(cents int64) string {
	if cents%100 == 0 {
		return fmt.Sprintf("$%d", cents/100)
	}
	return fmt.Sprintf("$%d.%02d", cents/100, cents%100)
}

// Resume is the item a sprint picked up in a new process goes back to: the
// one it was working when the process died. Its own checkpoint says which
// stage, so the sprint hands the slug back and lets the run continue itself.
//
// What the item had spent on a ledger other than the picking-up session's is
// added to the set's total here: that ledger died with its session, so its
// stages are counted from the running figure it left or not at all. A figure
// the picking-up session wrote itself is left where it is, because that
// session's ledger still holds it and the item's end will add it.
func (s *Sprint) Resume() (string, bool) {
	if s.Over() || s.Current == "" {
		return "", false
	}
	if s.ItemLedger != s.Session {
		s.Spent(s.ItemTurns, s.ItemCost)
	}
	return s.Current, true
}

// Finished records an item whose run reached done.
func (s *Sprint) Finished(slug string) {
	if !s.attempted(slug) {
		s.Attempts = append(s.Attempts, slug)
	}
	for _, done := range s.Done {
		if done == slug {
			return
		}
	}
	s.Done = append(s.Done, slug)
	s.Current, s.ItemStarted = "", time.Time{}
}

// Blocks ends the sprint on an item that blocked, with the run's own
// evidence as the reason.
func (s *Sprint) Blocks(slug, why string) {
	s.Current, s.ItemStarted = "", time.Time{}
	s.end(SprintBlocked, slug+" blocked — "+oneLine(why))
}

// Stop ends the sprint at the person's word. The item in flight is not
// touched here: whether its checkpoint is kept is the caller's to decide,
// because only the caller knows whether there is a session left to keep it
// for.
func (s *Sprint) Stop() { s.end(SprintStopped, "stopped") }

// Expired reports the current item having run past the wall-clock cap. Zero
// is no cap, which is the default: a cap that fires is a run thrown away, and
// the right number for it is a property of the project rather than of shhh.
func (s *Sprint) Expired(limit time.Duration) bool {
	if limit <= 0 || s.Current == "" || s.ItemStarted.IsZero() {
		return false
	}
	return time.Since(s.ItemStarted) > limit
}

// TimedOut is the evidence a capped item blocks with. It is a function rather
// than a method because the cap is per item and both drivers apply it,
// including the one working a single item with no sprint around it.
func TimedOut(limit time.Duration) string {
	return fmt.Sprintf("the item ran past the cap of %s it was given", limit)
}

func (s *Sprint) end(word, why string) {
	if s.Ended == "" {
		s.Ended, s.Reason = word, why
	}
	s.Current, s.ItemStarted = "", time.Time{}
}

func (s *Sprint) attempted(slug string) bool {
	for _, a := range s.Attempts {
		if a == slug {
			return true
		}
	}
	return false
}

// Count is how far the sprint has got, in the words every surface says it in:
// what is finished, against the cap where one was asked for.
func (s *Sprint) Count() string {
	if s.Max > 0 {
		return fmt.Sprintf("%d of at most %d done", len(s.Done), s.Max)
	}
	return plural(len(s.Done), "item") + " done"
}

// Summary is the sprint in one line for /todo status and the record: how far
// it has got, what it is on, and — once it is over — the word it ended with
// and the evidence behind it.
func (s *Sprint) Summary() string {
	var b strings.Builder
	b.WriteString("sprint · " + s.Count())
	if n := len(s.Attempts) - len(s.Done); n > 0 && s.Ended == "" {
		fmt.Fprintf(&b, " · %d attempted", len(s.Attempts))
	}
	if s.Current != "" {
		b.WriteString(" · on " + s.Current)
	}
	if len(s.Lanes) > 0 {
		b.WriteString(" · on " + strings.Join(s.Working(), ", "))
	}
	if s.Ended != "" {
		b.WriteString(" · " + s.Ended + ": " + s.Reason)
	}
	return b.String()
}

// plural is a count with its noun, singular for one.
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func oneLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}
