package cli

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rfizzle/shhh/internal/lsp"
	"github.com/rfizzle/shhh/internal/mcp"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/spf13/cobra"
)

// Startup timing: the phases a session pays for before its first prompt is
// drawn, each kept as a row of the session's record so a slow start reads as
// a cause rather than a guess
// (docs/capabilities/sessions-and-memory.md#startup-and-waits-are-timed).
//
// The rows are held on the command's context rather than in a package value,
// because a process here can run more than one command's worth of work — a
// test binary always does — and a row of one command's start written to
// another's record is a figure about the wrong session.

// processStart is as near the process starting as this package can stamp:
// package variables are set before main runs. The first paint is measured
// from here, so its row is the whole of what the person waited.
var processStart = time.Now()

// keymapTook is how long the keymap file took to read. It is read before
// there is a command (cmd/shhh), so it is handed over here and joined to the
// configuration's phase, which is the same act: reading what the person
// wrote down about how the session should behave.
var keymapTook atomic.Int64

// NoteKeymap records how long the keymap took to load, for the startup
// phase that reads the configuration.
func NoteKeymap(took time.Duration) { keymapTook.Store(int64(took)) }

type startupKey struct{}

// withStartup gives a command the holder its startup rows wait in until the
// session's record is open.
func withStartup(ctx context.Context, s *observe.Startup) context.Context {
	return context.WithValue(ctx, startupKey{}, s)
}

// startupFrom is the command's startup holder, or nil — which records
// nothing — for a context the root did not prepare.
func startupFrom(ctx context.Context) *observe.Startup {
	s, _ := ctx.Value(startupKey{}).(*observe.Startup)
	return s
}

// serveStartups hands a server's sessions their startup holders. The first
// session takes the command's own holder, which holds the phases the process
// paid before any session existed (configuration, store); every later one
// gets a holder of its own, so its server and language-server rows reach its
// record and not the first's.
type serveStartups struct {
	command *observe.Startup
	once    sync.Once
}

// forSession is the command that assembles one served session: a copy of cmd
// whose context carries that session's holder. The copy is shallow, which is
// all the assembly reads of it — its context, flags and streams.
func (h *serveStartups) forSession(cmd *cobra.Command) *cobra.Command {
	holder := &observe.Startup{}
	h.once.Do(func() { holder = h.command })
	sub := *cmd
	sub.SetContext(withStartup(cmd.Context(), holder))
	return &sub
}

// notePhase files one phase that is not a server's.
func notePhase(s *observe.Startup, phase string, took time.Duration) {
	s.Add(observe.StartupRow{Phase: phase, Took: took})
}

// serverStartup turns each server's settled connect into its row: the
// server's name, the word its outcome is filed under, and the time the
// connect measured. The report's error text is not read — it is the
// transport's words, and the record holds none.
func serverStartup(s *observe.Startup) func(mcp.Report) {
	if s == nil {
		return nil
	}
	return func(r mcp.Report) {
		s.Add(observe.StartupRow{
			Phase:   observe.PhaseMCP,
			Name:    r.Definition.Name,
			Outcome: observe.ServerOutcome(string(r.Status), r.TimedOut),
			Took:    r.Took,
		})
	}
}

// openSessionStore is openStore timed as the store's phase: the open, every
// migration the store is behind on, and scheduling the prune — which runs
// on a goroutine of its own and so costs the start only what it contends.
func openSessionStore(ctx context.Context) (*storage.DB, error) {
	started := time.Now()
	db, err := openStore()
	notePhase(startupFrom(ctx), observe.PhaseStore, time.Since(started))
	return db, err
}

// openSessionLSP is openLSP timed as the language servers' phase: looking
// for them is a walk of PATH for every server shhh knows, paid before the
// first paint whether or not one is found.
func openSessionLSP(ctx context.Context) *lsp.Toolset {
	started := time.Now()
	ts := openLSP(ConfigFrom(ctx))
	notePhase(startupFrom(ctx), observe.PhaseLSP, time.Since(started))
	return ts
}

// firstPaint is what the chat model calls once its first frame is drawn,
// filing the phase measured from the process start. It runs once however
// often it is called, since the model calls it from every frame it draws.
// The row is a store write and the call comes from the frame being drawn, so
// the write is queued on a goroutine of its own: a frame never waits on the
// store's lock, and the figure is the time at the call, not at the write.
func firstPaint(s *observe.Startup) func() {
	return firstPaintOn(s, func(write func()) { go write() })
}

// firstPaintOn is firstPaint with the queue the write is handed to, so a
// test holds the write and releases it by hand.
func firstPaintOn(s *observe.Startup, queue func(write func())) func() {
	if s == nil {
		return nil
	}
	var once atomic.Bool
	return func() {
		if once.CompareAndSwap(false, true) {
			took := time.Since(processStart)
			queue(func() { notePhase(s, observe.PhaseFirstPaint, took) })
		}
	}
}
