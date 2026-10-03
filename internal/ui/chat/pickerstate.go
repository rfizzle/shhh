package chat

import (
	"context"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// pickerState is the slash commands' select card on the session: the card
// that is open, the list it opened over and how its rows map back onto that
// list, what taking a row does, and where the card goes back to. The /model
// picker's catalog rides with it, since that card is the one reader of it.
// The interactive slash commands (picker.go), the saved chats (chats.go),
// the palette (palette.go) and the rewind list (rewind.go) all read it.
//
// It is a value, held by value on the Model and copied with it every frame
// like the rest of the Model, so it takes no pointer of its own: the pointer,
// slice and function fields below (card, apply, all, index and the model
// list's) were fields on the Model before they were gathered here, and
// nothing keys a memo on their identity.
//
// What a closed card must not leave behind is cleared in one place:
// closePicker calls clear (chats.go). Where the card goes back to is read
// there before it is reset, and the model list outlives every card — the
// catalog is the session's, fetched at most once. It has no update: the
// card answers its keys through the register (overlay.go), never through
// updateKey.
type pickerState struct {
	// card is the open select card, nil while none is; apply consumes the
	// chosen index and returns the transcript note.
	card  *components.Select
	apply func(*Model, int, bool) (string, tea.Cmd)
	// all is the list the card opened over and index maps the rows it is
	// showing back onto it, so a choice made through the filter row still
	// reaches an apply written against the whole list.
	all   []components.SelectOption
	index []int
	// fromReading is a card opened over reading mode — the block card [c]
	// opens on a reply — which goes back to the mode when it closes, taken
	// or not: the reader asked from a row they were standing on, and the
	// cursor is still on it.
	fromReading bool
	// models is the /model picker's catalog and its live discovery.
	models modelList
}

// modelList is the /model picker's catalog: the curated list, or what the
// provider's /v1/models endpoint reported for an endpoint no curated catalog
// can cover, which then replaces it for the rest of the session.
type modelList struct {
	// options is the catalog the bare /model picker offers.
	options []string
	// lister queries the provider for its models; cancel abandons a query in
	// flight; listed says one has answered, so it is asked at most once.
	lister func(context.Context) ([]string, error)
	cancel context.CancelFunc
	listed bool
}

// stop abandons a query in flight and drops its cancel. It is nil-safe, so
// the esc on the waiting screen (answerModelList) and the quit
// (stopSideJobs) share it with the path that has nothing out.
func (l *modelList) stop() {
	if l.cancel != nil {
		l.cancel()
		l.cancel = nil
	}
}

// clear drops the closed card and everything hanging off it.
func (p *pickerState) clear() {
	p.card = nil
	p.apply = nil
	p.all = nil
	p.index = nil
}
