package chat

// The calls running now, each with the stop that ends it alone
// (docs/interface/surfaces.md#the-process-manager). A round runs its calls
// side by side, and a stop of the turn stops all of them; this is what lets
// the process manager stop one — a search over a tree too large, a page that
// will not answer — and leave the rest of the round to finish. The model is
// answered for a stopped call as for any call a stop caught.
//
// The table is shared by pointer because the calls register themselves from
// their own goroutines as they start, and the screen reads it on its own.

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// callTable is one round's calls in flight: the round's own stop, and each
// call beside the stop that ends it alone.
type callTable struct {
	// cancel is the round's stop, nil where the table holds one call run
	// on its own (an approved call, a command).
	cancel context.CancelFunc

	mu    sync.Mutex
	calls []*liveCall
}

// liveCall is one call as the manager reads it.
type liveCall struct {
	call    provider.ToolCall
	started time.Time
	// done is the call's own context: it ends when the call returns, or when
	// it is stopped, and stopped says which.
	done    context.Context
	cancel  context.CancelFunc
	stopped bool
	// tail is a command's last line of output, nil for every other tool.
	tail  *commandTail
	where string
}

// add is the per-call seam (agent.CallCancel): each auto-run call of a round
// registers itself as it starts.
func (t *callTable) add(ctx context.Context, tc provider.ToolCall, cancel context.CancelFunc) {
	t.put(ctx, tc, cancel, nil, "host")
}

// put records one call.
func (t *callTable) put(ctx context.Context, tc provider.ToolCall, cancel context.CancelFunc, tail *commandTail, where string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.calls = append(t.calls, &liveCall{call: tc, started: clock(), done: ctx, cancel: cancel, tail: tail, where: where})
}

// stop ends the call with id alone, and reports whether it was still
// running.
func (t *callTable) stop(id string) bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, c := range t.calls {
		if c.call.ID == id && !c.stopped && c.done.Err() == nil {
			c.stopped = true
			c.cancel()
			return true
		}
	}
	return false
}

// drop lets go of the call with id once it has returned, so the manager stops
// listing it even where a person had stopped it.
func (t *callTable) drop(id string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.calls = slices.DeleteFunc(t.calls, func(c *liveCall) bool { return c.call.ID == id })
}

// end stops whatever of the round is still running and lets go of every
// context it holds. A round that finished is ended the same way.
func (t *callTable) end() {
	if t.cancel != nil {
		t.cancel()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, c := range t.calls {
		c.cancel()
	}
}

// liveCalls is what the table holds that the manager lists: every call still
// running, and every call a person stopped, until the round is over. A call
// that returned by itself has left.
func (t *callTable) liveCalls() []liveCall {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []liveCall
	for _, c := range t.calls {
		if c.stopped || c.done.Err() == nil {
			out = append(out, *c)
		}
	}
	return out
}

// callRows is the calls running now as the manager lists them: the tool, its
// arguments in the receipt's words, how long it has run and where.
func (m Model) callRows() []components.ManagerRow {
	var rows []components.ManagerRow
	for _, c := range m.calls.liveCalls() {
		rc := m.callReceipt(c.call.Name, c.call.Arguments, "", nil)
		row := components.ManagerRow{
			Section: components.ManagerCalls, Key: c.call.ID, Name: c.call.Name,
			Summary: strings.TrimSpace(rc.Title()), Age: clock().Sub(c.started),
			Where: c.where, Whole: wholeArguments(c.call.Arguments),
		}
		if c.stopped {
			row.State = components.ManagerStoppedByYou
		}
		if c.tail != nil {
			row.Output = c.tail.Line()
		}
		rows = append(rows, row)
	}
	return rows
}

// wholeArguments is a call's arguments laid out to be read: indented where
// they are JSON, as written where they are not.
func wholeArguments(args string) string {
	var v any
	if err := json.Unmarshal([]byte(args), &v); err != nil {
		return args
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return args
	}
	return string(out)
}
