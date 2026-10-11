package chat

import "github.com/rfizzle/shhh/internal/process"

// Processes wires the long-running process supervisor into the chat
// TUI. Manage's presence marks the process tool as registered, which routes
// start actions through the approval queue while status/read/input/stop
// auto-run. The rest is what the process manager (`/ps`, manager.go) and the
// rail's PROCESSES block read and do.
type Processes struct {
	Manage func(args []string) string
	// Contained names the containment mechanism a start actually runs
	// under, empty when nothing contains one. It is the supervisor's own
	// answer and not the runner's: the two command paths are wired from one
	// policy, but a card that read the ordinary path's state would be
	// describing something other than the process about to start. Nil says
	// nothing rather than guessing.
	Contained func() string
	// Snapshot is every process the session started, oldest first. Nil is
	// a session with none to list.
	Snapshot func() []process.Info
	// Tail is what the process tool's read returns for the end of each of
	// a process's streams, which is what opening one shows.
	Tail func(name string) (stdout, stderr string, err error)
	// Stop and Kill end a process — the tool's stop, and the kill without
	// its grace — and say its status after. Both block until it is gone,
	// so the screen runs them off its own goroutine.
	Stop func(name string) (string, error)
	Kill func(name string) (string, error)
}
