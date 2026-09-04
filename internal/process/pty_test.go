//go:build !windows

package process

import (
	"strings"
	"testing"
)

// A terminal is what some commands check before they will do anything useful,
// and a pipe cannot be talked into being one. The test is the check itself:
// the same command answers differently depending on what it was given.
func TestStart_APtyIsATerminalAndAPipeIsNot(t *testing.T) {
	s := newTestSupervisor(t, nil)

	const probe = `if [ -t 0 ]; then echo interactive; else echo piped; fi; cat`
	execute(t, s, `{"action":"start","name":"tty","command":"`+probe+`","pty":true}`)
	execute(t, s, `{"action":"start","name":"pipe","command":"`+probe+`"}`)

	waitFor(t, "the terminal-backed process to answer", func() bool {
		return strings.Contains(execute(t, s, `{"action":"read","name":"tty"}`), "interactive")
	})
	waitFor(t, "the piped process to answer", func() bool {
		return strings.Contains(execute(t, s, `{"action":"read","name":"pipe"}`), "piped")
	})

	// The terminal is the process's input too, so what is written to it
	// arrives as if it had been typed.
	execute(t, s, `{"action":"input","name":"tty","text":"ping\n"}`)
	waitFor(t, "the terminal to carry the input through", func() bool {
		return strings.Contains(execute(t, s, `{"action":"read","name":"tty"}`), "ping")
	})

	// A terminal has one stream, so everything arrived as stdout.
	status := execute(t, s, `{"action":"status","name":"tty"}`)
	if !strings.Contains(status, "stderr: 0 bytes") {
		t.Errorf("a terminal-backed process should capture nothing as stderr, got %q", status)
	}
}
