package observe

// Timing: how long a session took to start, and where a turn's time went.
//
// The record's durations were on tool rows and on the turn row, so a session
// that sat at a blank terminal for twenty seconds left no row at all, and a
// turn that ran eight minutes read as one number. These are the rows that
// give "it hung" a cause: one per startup phase, one per server, the turn's
// time split by what it waited on, and the longest stretch in which nothing
// reached the screen.
//
// Every string they carry is a word declared here or an identifier — a
// server's name, on the footing an MCP tool's name already has in the tool
// column. A server's own words about why it would not start are never among
// them: they are the transport's text, which can carry a URL with a token in
// it, and the record is content-free by construction rather than by scrub.
// See docs/capabilities/sessions-and-memory.md#startup-and-waits-are-timed.

import (
	"sort"
	"sync"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
)

// Startup phases, one row each. A server's connect is one row per server,
// under PhaseMCP with the server's name beside it, because a slow start is
// nearly always one slow server and a sum would hide which.
const (
	// PhaseConfig: the configuration layers and the keymap read.
	PhaseConfig = "config"
	// PhaseStore: the store opened, migrated and pruned.
	PhaseStore = "store"
	// PhaseMCP: one MCP server's connect, from dial to its catalog listed or
	// the wait given up.
	PhaseMCP = "mcp"
	// PhaseLSP: the language servers looked for on this machine.
	PhaseLSP = "lsp"
	// PhaseFirstPaint: the first prompt drawn, measured from the process
	// starting rather than from the phase before it, so it is the whole of
	// what the person waited.
	PhaseFirstPaint = "first-paint"
)

// Server outcomes for a PhaseMCP row. They are the connect's own statuses,
// spelled out here rather than read from the constants they are kept under,
// for the reason the tool lists further up are: this classifies rows written
// by every build that ever wrote one. ServerTimeout is told from
// ServerFailed because the two are fixed differently — a server that never
// answered wants a longer wait or a warmer cache, and one that refused wants
// its definition read.
const (
	ServerConnected  = "connected"
	ServerFailed     = "failed"
	ServerTimeout    = "timeout"
	ServerDisabled   = "disabled"
	ServerUntrusted  = "untrusted"
	ServerMissingEnv = "missing-env"
	ServerExcluded   = "excluded"
	// ServerOther is a status this build has no word for.
	ServerOther = "other"
)

// ServerOutcome is the word a server's connect is filed under: its status,
// with a failure that was the wait running out told apart.
func ServerOutcome(status string, timedOut bool) string {
	switch status {
	case ServerConnected, ServerDisabled, ServerUntrusted, ServerMissingEnv, ServerExcluded:
		return status
	case ServerFailed:
		if timedOut {
			return ServerTimeout
		}
		return ServerFailed
	}
	return ServerOther
}

// StartupRow is one phase's row: the phase, the server's name where the
// phase is a server's, the server's outcome word, and how long it took.
type StartupRow struct {
	Phase, Name, Outcome string
	Took                 time.Duration
}

// Startup holds a process's startup rows until a record is open to take
// them. The phases it times run before the session's row exists — the
// configuration is read before any command, and a session's servers are
// connected before its row is opened — so a row is kept here and handed on
// at Attach, and one that arrives after Attach goes straight through.
//
// It is safe for concurrent use: the servers connect at once and each
// reports from its own goroutine. The zero value is ready, and a nil one
// records nothing.
type Startup struct {
	mu   sync.Mutex
	rows []StartupRow
	sink func(StartupRow)
}

// Add files one row.
func (s *Startup) Add(r StartupRow) {
	if s == nil {
		return
	}
	s.mu.Lock()
	sink := s.sink
	if sink == nil {
		s.rows = append(s.rows, r)
	}
	s.mu.Unlock()
	if sink != nil {
		sink(r)
	}
}

// Attach points the rows at the record: the ones held so far are written
// now, in the order they arrived, and every later one as it comes. Only the
// first attach takes them — a process starts once, and a second session it
// opens did not pay for the first one's start.
func (s *Startup) Attach(sink func(StartupRow)) {
	if s == nil || sink == nil {
		return
	}
	s.mu.Lock()
	if s.sink != nil {
		s.mu.Unlock()
		return
	}
	held := s.rows
	s.rows, s.sink = nil, sink
	s.mu.Unlock()
	for _, r := range held {
		sink(r)
	}
}

// What a turn waited on, as the words a quiet stretch is filed under.
const (
	WaitModelFirst  = "model-first"
	WaitModelStream = "model-stream"
	WaitTool        = "tool"
	WaitPerson      = "person"
)

// WaitWord is a wait's word, read from the loop's own enum the way
// SummaryCode reads its states, so the two can be renamed independently.
func WaitWord(w agent.Wait) string {
	switch w {
	case agent.WaitModelStream:
		return WaitModelStream
	case agent.WaitTool:
		return WaitTool
	case agent.WaitPerson:
		return WaitPerson
	}
	return WaitModelFirst
}

// A quiet stretch is filed as quiet when the stream delivered something in
// it that drew nothing, silent when the turn waited on the model and nothing
// arrived at all, and waiting when it waited on a tool or a person, where no
// stream was open to deliver anything. The first two are different faults: a
// quiet stream is a model thinking where nobody can see it, and a silent one
// is a connection that may be gone. The third is no fault of the stream's,
// and filing it as silent would put every long build and every card left
// unanswered among the dead connections.
const (
	StretchQuiet   = "quiet"
	StretchSilent  = "silent"
	StretchWaiting = "waiting"
)

// StretchWord is a quiet stretch's word.
func StretchWord(q agent.Quiet) string {
	switch {
	case q.Delivered > 0:
		return StretchQuiet
	case q.On == agent.WaitTool || q.On == agent.WaitPerson:
		return StretchWaiting
	}
	return StretchSilent
}

// TurnMillis is a turn's split in whole milliseconds, in the order
// model-first, model-stream, tool, person, adding up to total's milliseconds
// exactly. Each part is rounded down and the milliseconds the rounding lost
// go to the parts that lost the most, so a reader summing the four columns
// gets the duration column and not a figure three milliseconds short of it.
func TurnMillis(total time.Duration, s agent.TurnSplit) [4]int64 {
	parts := [4]time.Duration{s.ModelFirst, s.ModelStream, s.Tool, s.Person}
	var out [4]int64
	var sum int64
	for i, p := range parts {
		out[i] = p.Milliseconds()
		sum += out[i]
	}
	order := []int{0, 1, 2, 3}
	sort.SliceStable(order, func(a, b int) bool {
		return parts[order[a]]%time.Millisecond > parts[order[b]]%time.Millisecond
	})
	for i := 0; sum < total.Milliseconds() && i < len(order); i++ {
		out[order[i]]++
		sum++
	}
	return out
}
