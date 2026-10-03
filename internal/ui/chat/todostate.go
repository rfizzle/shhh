package chat

import (
	"context"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// todoState is the project's backlog on the session: what the host wired,
// the store as last read from disk, the run in progress, and the cards and
// the readings that work on it — the proposals card a bare /todo add opens,
// the draft card /todo new opens, the grooming card and the sprint planning
// turn. The todo files and the todo modes in the register all read it.
//
// It is a value, held by value on the Model and copied with it every frame
// like the rest of the Model, so it takes no pointer of its own: the pointer,
// slice and function fields below (store, propose, proposals, draft, groom
// and the two cancels) were fields on the Model before they were gathered
// here, and nothing keys a memo on their identity.
//
// It has no clear. Nothing drops these fields together: setTurnState leaves
// them alone, because a backlog run outlives the turns it starts, and each
// card and reading is retired by its own path — a late reading by its run
// number, a card by its answer (todoadd.go, todogroom.go, todosprint.go).
type todoState struct {
	// wiring backs /todo and the TODO block; store is the backlog as last
	// read from disk, reloaded on the events that can change it.
	wiring Todos
	store  *todo.Store
	// propose is the open proposals card and proposals what it is
	// showing; extractRun numbers readings so a late one is dropped.
	// runner is the backlog run in progress, if any (todorun.go).
	runner        todoRunState
	propose       *components.MultiSelect
	proposals     []todo.Proposal
	extracting    bool
	extractRun    int
	extractCancel context.CancelFunc
	// draft is the open draft card; draftRun numbers draftings so a late
	// one is dropped the way a late reading is. They are the drafting's
	// own rather than the reading's because /clear retires a reading — the
	// conversation it read is gone — and says nothing about a sentence
	// somebody typed.
	draft     *todoDraft
	drafting  bool
	draftRun  int
	draftStop context.CancelFunc
	// groomer is the grooming pass in flight and groom the card its
	// reading is showing on (todogroom.go).
	groomer todoGroomState
	groom   *components.MultiSelect
	// planner is the sprint planning turn in flight (todosprint.go).
	planner todoPlanState
}

// enabled reports whether the host wired a backlog into this session.
func (t todoState) enabled() bool { return t.wiring.Manage != nil }

// todoKey is what the backlog's keyboard made of a key, for updateKey to
// carry out on the session.
type todoKey int

const (
	// todoPass is a key that is not the backlog's; routing goes on.
	todoPass todoKey = iota
	// todoOpenScreen is the backlog chord on a session with a backlog
	// wired: open the backlog screen (openTodoScreen).
	todoOpenScreen
)

// update reads a key for the backlog once no surface has claimed it, and
// says what it is. The todo modes answer their own keys through the
// register (overlay.go); what is left here is the chord that opens the
// screen from wherever the keyboard is.
func (t todoState) update(msg tea.KeyPressMsg) (todoState, todoKey) {
	// The backlog screen reads the project rather than the session, so it
	// opens over a running turn as well as an idle one — and a session with
	// no backlog wired keeps the key's textarea meaning (character forward).
	if keys.Match(msg, keys.Draft.Backlog) && t.enabled() {
		return t, todoOpenScreen
	}
	return t, todoPass
}
