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
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/todo"
)

// The closed set of reasons a sprint stops. A driver reports one of these
// and never a sentence of its own, so a script reading the end of a sprint
// reads the same word the transcript shows.
const (
	// SprintEmpty is nothing left that can be started: every remaining item
	// waits on another, is blocked, or the backlog is finished.
	SprintEmpty = "empty"
	// SprintCapped is --max reached.
	SprintCapped = "capped"
	// SprintBlocked is an item that blocked. The sprint stops on the first
	// one: a blocked item wrote a follow-up, and the next ready item may
	// depend on the work that did not land.
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
	// Ended is one of the words above once the sprint is over, and Reason
	// the evidence behind it.
	Ended  string `json:"ended,omitempty"`
	Reason string `json:"reason,omitempty"`
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
	if s.Over() || (s.Max > 0 && len(s.Attempts) >= s.Max) {
		return todo.Item{}, false
	}
	for _, it := range store.Ready() {
		if !s.attempted(it.Slug) {
			return it, true
		}
	}
	return todo.Item{}, false
}

// Spent adds one item's cost to the set's running total. It is called at the
// session boundary, where what the item cost is still readable and about to
// be reset.
func (s *Sprint) Spent(turns int, cost float64) {
	s.Turns += max(turns, 0)
	if cost > 0 {
		s.Cost += cost
	}
}

// Resume is the item a sprint picked up in a new process goes back to: the
// one it was working when the process died. Its own checkpoint says which
// stage, so the sprint hands the slug back and lets the run continue itself.
func (s *Sprint) Resume() (string, bool) {
	if s.Over() || s.Current == "" {
		return "", false
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
