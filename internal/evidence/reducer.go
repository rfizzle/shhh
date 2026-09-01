package evidence

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/rfizzle/shhh/internal/tools"
)

// Reducer ties the reduction pipeline to one session's evidence store: it
// reduces bulky tool results, stores the full originals, and tracks how much
// context the reductions saved.
type Reducer struct {
	store *Store

	mu            sync.Mutex
	reductions    int
	originalBytes int64
	reducedBytes  int64
}

func NewReducer(store *Store) *Reducer { return &Reducer{store: store} }

// Store exposes the underlying session store (for /evidence management).
func (r *Reducer) Store() *Store { return r.store }

// Process runs one tool result through the reduction pipeline. Results at or
// below the threshold — and any result the pipeline cannot improve or whose
// original cannot be stored — pass through untouched (fail open). A reduced
// result is display-consistent: the returned text, notice included, is
// exactly what both the model and the transcript record. Safe on a nil
// Reducer, which reduces nothing.
//
// A tool that bounds its own output is exempt, because reducing it is a loss
// with nothing bought back: read_file is told to return a whole file in one
// call and sized to do it, and a head-and-tail cut through that hands back
// sixty lines of four hundred with the middle gone. The reader's only way
// through is then to page the evidence store — which is the paging loop
// read_file's description was rewritten to stop.
// See docs/capabilities/evidence.md#reduction-is-for-unbounded-output.
func (r *Reducer) Process(tool, result string) string {
	if r == nil || r.store == nil {
		return result
	}
	if tools.SelfBounding(tool) {
		return result
	}
	reduced, ok := reduce(result)
	if !ok {
		return result
	}
	id, err := r.store.Put(tool, []byte(result))
	if err != nil {
		return result
	}
	r.mu.Lock()
	r.reductions++
	r.originalBytes += int64(len(result))
	r.reducedBytes += int64(len(reduced))
	r.mu.Unlock()
	notice := fmt.Sprintf(
		"[output reduced: %d → %d bytes; full output stored as evidence %s — retrieve it with the evidence tool (info/read/search)]",
		len(result), len(reduced), id)
	return notice + "\n" + reduced
}

// WrapExecutor wraps a tool executor so every successful result runs through
// the reduction pipeline, and evidence tool calls dispatch against this
// session's store (their output is already bounded and is never reduced).
func (r *Reducer) WrapExecutor(next func(name string, args json.RawMessage) (string, error)) func(string, json.RawMessage) (string, error) {
	return func(name string, args json.RawMessage) (string, error) {
		if name == ToolName {
			return r.store.ExecuteTool(args)
		}
		out, err := next(name, args)
		if err != nil {
			return out, err
		}
		return r.Process(name, out), nil
	}
}

// ReduceStats summarizes what the pipeline saved this session.
type ReduceStats struct {
	Reductions    int
	OriginalBytes int64
	ReducedBytes  int64
}

func (r *Reducer) Stats() ReduceStats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return ReduceStats{Reductions: r.reductions, OriginalBytes: r.originalBytes, ReducedBytes: r.reducedBytes}
}

// StatusReport renders the /evidence status view: store size and reduction
// stats.
func (r *Reducer) StatusReport() string {
	st := r.store.Stats()
	rs := r.Stats()
	var b strings.Builder
	fmt.Fprintf(&b, "Evidence store (session %s):\n", r.store.Session())
	fmt.Fprintf(&b, "  stored originals: %d (%s)\n", st.Entries, formatBytes(st.StoredBytes))
	if rs.Reductions > 0 {
		fmt.Fprintf(&b, "  reductions this session: %d (%s → %s in tool results)\n",
			rs.Reductions, formatBytes(rs.OriginalBytes), formatBytes(rs.ReducedBytes))
	} else {
		b.WriteString("  reductions this session: 0\n")
	}
	b.WriteString("Use /evidence purge to delete the stored originals.")
	return b.String()
}

func formatBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
