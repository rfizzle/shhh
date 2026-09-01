package agent

// Collecting the rows a surface has no transcript to read them from.
//
// The chat model builds its digest by walking the transcript it was already
// drawing. A headless run has no transcript — it has two hooks, one before a
// tool call and one after its result — so the rows are accumulated as they
// happen instead of reconstructed afterwards.
//
// It is safe under concurrency because a round's read-only calls run at the
// same time (agent.MaxParallelToolCalls), so several results can land at once.

import (
	"strconv"
	"sync"

	"github.com/rfizzle/shhh/internal/digest"
)

// DefaultDigestRows is how many recent rows a digest carries. It matches what the
// chat model sends, so a reading of a headless run is made of the same amount
// of evidence as a reading of a session.
const DefaultDigestRows = 24

// Recorder accumulates activity rows for a run that has nowhere else to keep
// them. The zero value is not usable; call NewRecorder.
type Recorder struct {
	max int

	mu   sync.Mutex
	rows []string
	// assistant is the last thing the model said in its own words — the one
	// piece of free text a digest carries, and the most useful part of it.
	assistant string
	// calls counts every row ever recorded, not just the ones still kept, so
	// a caller can tell a quiet run from a truncated one.
	calls int
}

func NewRecorder(max int) *Recorder {
	if max <= 0 {
		max = DefaultDigestRows
	}
	return &Recorder{max: max, rows: make([]string, 0, max)}
}

// Tool records one completed call. Safe on a nil Recorder, which records
// nothing — so a surface that takes no readings wires the hook unconditionally
// and pays nothing for it.
func (r *Recorder) Tool(name, args, result string) {
	if r == nil {
		return
	}
	row := SummaryActivity(name, digest.Arg(name, args), digest.Outcome(result))
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if len(r.rows) == r.max {
		r.rows = r.rows[1:]
	}
	r.rows = append(r.rows, row)
}

// Command records an approved shell command by its exit code, which is the
// one thing about a command a row may say beyond what was run.
func (r *Recorder) Command(command string, exitCode int) {
	if r == nil {
		return
	}
	row := SummaryActivity("command", digest.FirstLine(command), ExitOutcome(exitCode))
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if len(r.rows) == r.max {
		r.rows = r.rows[1:]
	}
	r.rows = append(r.rows, row)
}

// Assistant records the model's latest message. Only the last is kept: the
// digest asks what the agent thinks it is doing now.
func (r *Recorder) Assistant(text string) {
	if r == nil || text == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.assistant = text
}

// Rows is the recent activity, oldest first.
func (r *Recorder) Rows() []string {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.rows...)
}

// Calls is how many rows have been recorded in all, kept or dropped.
func (r *Recorder) Calls() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// LastAssistant is the model's latest message, bounded by the caller.
func (r *Recorder) LastAssistant() string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.assistant
}

// ExitOutcome is a command's outcome word. It is a fixed shape rather than
// the command's output, for the reason the package comment gives.
func ExitOutcome(code int) string { return "exit " + strconv.Itoa(code) }
