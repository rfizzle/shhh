package provider

// The idle deadline every turn's stream runs under.
//
// A request that is accepted, answered with headers and then never written to
// again is indistinguishable, from inside the loop, from a model that is
// thinking hard: both are a blocking read that has not returned. In the TUI a
// person presses Esc. The four unattended drivers — a headless run, a served
// loop, a todo stage, a child — have nobody, and the process holds its
// worktree lock and its CI budget until something external kills it.
//
// So every dialect's stream loop pushes a deadline forward on each event it
// reads, and a deadline that expires cancels the request and ends the turn as
// a network failure — which is a class the retry schedule already waits out
// and asks again (retry.go in internal/agent).
// See docs/capabilities/providers.md#a-stream-that-stops-writing-is-a-failure.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// DefaultStreamIdle is how long a stream may go without an event before the
// turn fails. It is generous on purpose: the gap being bounded is the one
// between two events on an open stream, not the time a reply takes, and a
// model that thinks for a minute before its first token is ordinary. Two
// minutes is past anything a live endpoint does between writes and far short
// of the hours a hung one would otherwise hold.
const DefaultStreamIdle = 120 * time.Second

// ErrStreamIdle is the failure a stream that went quiet ends on. It is a
// sentinel rather than a context deadline because the deadline is enforced by
// cancelling the request: what the transport hands back is a cancellation,
// which reads as the reader pressing Esc, and the reason it was cancelled is
// the one thing the transport cannot know.
var ErrStreamIdle = errors.New("the provider stopped sending")

// idleDeadline is the configured deadline, embedded by every dialect. Zero is
// the value a provider built from a finished client starts with, so zero has
// to mean the built-in default rather than none — a gateway profile is
// exactly the case this mechanism exists for.
type idleDeadline struct {
	// after is the deadline as it was configured: zero for unset, and any
	// negative for a machine that would rather wait than lose a turn.
	after time.Duration
}

// idleDeadlineOf reads the setting as the file spells it — whole seconds,
// zero for unset, a negative to remove the deadline.
func idleDeadlineOf(seconds int) idleDeadline {
	if seconds < 0 {
		return idleDeadline{after: -1}
	}
	return idleDeadline{after: time.Duration(seconds) * time.Second}
}

// SetStreamIdle puts the configured deadline on a provider that was built
// from an already-configured client rather than from the resolve options the
// deadline lives in. A gateway profile (internal/profile) builds all three of
// its dialects that way, and a route through one is no less able to go quiet
// than the direct path.
func (d *idleDeadline) SetStreamIdle(seconds int) { *d = idleDeadlineOf(seconds) }

// idle is the deadline a stream actually runs under: the configured one, the
// built-in default where nothing set one, and none where it was turned off.
func (d idleDeadline) idle() time.Duration {
	switch {
	case d.after > 0:
		return d.after
	case d.after < 0:
		return 0
	default:
		return DefaultStreamIdle
	}
}

// guard derives the context one stream runs under and the watch that ends it.
// Where the deadline is off, the context is handed back untouched and the
// watch's methods do nothing, so a dialect's loop reads the same either way.
//
// The context is derived rather than a timeout put on the request: a request
// with a deadline on it fails a long reply that was arriving perfectly well,
// which is the opposite of what this is for.
func (d idleDeadline) guard(ctx context.Context) (context.Context, *idleWatch) {
	idle := d.idle()
	if idle <= 0 {
		return ctx, &idleWatch{}
	}
	ctx, cancel := context.WithCancel(ctx)
	w := &idleWatch{idle: idle, cancel: cancel, last: time.Now()}
	// The timer is armed under the lock it will be read behind: AfterFunc
	// starts the countdown before it returns the handle, so the assignment
	// races the expiry it is arming without this.
	w.mu.Lock()
	defer w.mu.Unlock()
	w.timer = time.AfterFunc(idle, w.expire)
	return ctx, w
}

// idleWatch is one stream's deadline: when the last event arrived, and the
// cancellation that fires when nothing else does. The zero value is a watch
// over a stream with no deadline, and every method on it does nothing —
// cancel is what says which, because it is the one field that is written once
// and never again.
type idleWatch struct {
	idle   time.Duration
	cancel context.CancelFunc

	mu    sync.Mutex
	last  time.Time
	timer *time.Timer
	fired bool
	// done says the stream has ended and the watch with it. It exists for
	// the one ordering stop cannot prevent: a timer already running when
	// stop takes the lock re-arms itself for the remaining gap, and the
	// watch would keep a timer alive for the length of the deadline after
	// the turn it was watching had finished.
	done bool
}

// alive records that the stream is still writing. Every event resets the
// deadline — a token, a thinking delta, an argument fragment, a keep-alive
// comment the parser had no use for — because what is being watched for is
// silence on the wire and not progress toward an answer.
//
// It writes a timestamp rather than resetting the timer, so a reply arriving
// in ten thousand fragments costs ten thousand clock reads instead of ten
// thousand timer reschedules on the goroutine reading the wire.
func (w *idleWatch) alive() {
	if w.cancel == nil {
		return
	}
	w.mu.Lock()
	w.last = time.Now()
	w.mu.Unlock()
}

// expire fires when the timer runs out, which is not the same as the stream
// having gone quiet: an event may have arrived since the timer was armed. So
// the gap is measured, and a timer that woke early re-arms for the remainder
// rather than cancelling a stream that is writing.
func (w *idleWatch) expire() {
	w.mu.Lock()
	if w.done {
		w.mu.Unlock()
		return
	}
	if left := w.idle - time.Since(w.last); left > 0 {
		w.timer.Reset(left)
		w.mu.Unlock()
		return
	}
	w.fired = true
	w.mu.Unlock()
	w.cancel()
}

// stop ends the watch and releases the derived context. Every stream loop
// defers it, including the ones that end well: the context is a child of the
// caller's and leaks until something cancels it.
func (w *idleWatch) stop() {
	if w.cancel == nil {
		return
	}
	w.mu.Lock()
	w.done = true
	w.timer.Stop()
	w.mu.Unlock()
	w.cancel()
}

// err is the error a stream ends on. Where the deadline expired it is this
// mechanism's own, and not the cancellation the transport reports: the
// cancellation is true and says the reader pressed Esc, which is the one
// thing that did not happen.
//
// It takes nil too, and that is not a decoration. Cancelling a request can
// leave a reader looking at a clean end of body rather than at an error, and
// a turn that reported itself finished and empty because the wire went quiet
// is a worse failure than the hang this replaces. So the readers that can
// reach an ending with no error in hand ask here as well.
func (w *idleWatch) err(err error) error {
	if w.cancel == nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.fired {
		return err
	}
	return fmt.Errorf("%w: nothing arrived on the stream for %s", ErrStreamIdle, w.idle)
}
