package sandbox

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ExecArg is the first argument that makes the shhh binary the command
// helper inside a sandbox container rather than the program. main answers it
// before anything else runs, as it answers the network bridge: inside the
// container there is no configuration, no keymap and no state directory, and
// none of them is wanted.
//
// Every program a sandbox session starts in its container is started through
// the helper, because the engine's exec forwards no signal: stopping the
// client on the Mac leaves the command running inside. The helper holds the
// command in a process group of its own and watches its own stdin, which is
// the far end of the client's: when the client goes, or lets go, the stream
// hangs up and the helper stops the group with the sequence a cancelled
// command gets on the host. So the cancel the runner already sends reaches
// the container through the stream, and nothing on the Mac names a process
// inside it.
// See docs/capabilities/containment.md#a-cancelled-command-takes-its-children-with-it.
const ExecArg = "__shhh-sandbox-exec"

// HelperPath is where the released sandbox image carries shhh for the
// helper: out of every PATH, so no command a session runs finds it by name.
const HelperPath = "/usr/local/libexec/shhh/shhh"

// HelperVersion is the helper's protocol — the flags it takes and what a
// hang-up means — rather than shhh's version. A session asks before its first
// turn and refuses a container whose helper speaks another: a flag one side
// sends and the other does not know is a command that never starts, or one
// whose cancel never arrives.
const HelperVersion = "1"

// helperBanner is what `--version` prints, and what the probe expects.
const helperBanner = "shhh-sandbox-exec "

// The helper's flag values.
const (
	// stdinNull gives the child /dev/null, as a captured command on the
	// host has: a command's stdin is the stream the cancel travels on, not
	// input for the command.
	stdinNull = "null"
	// stdinInherit hands the child the helper's own stdin, for a process
	// whose input is written to it.
	stdinInherit = "inherit"
	// hangupStop stops the group when the stream hangs up.
	hangupStop = "stop"
	// hangupIgnore leaves the group alone when it does, for a caller that
	// closes its stdin on purpose once it has written what it had to.
	hangupIgnore = "ignore"
)

// execFlags are the helper's arguments, parsed.
type execFlags struct {
	stdin   string
	hangup  string
	timeout time.Duration
	version bool
	argv    []string
}

// parseExecArgs reads `[--stdin=…] [--hangup=…] [--timeout=<d>] -- argv…`, or
// a lone `--version`. Each flag has a default that is the command's case — a
// stdin of /dev/null, a hang-up that stops it, no ceiling of the helper's own
// — so a caller spells only where it differs.
func parseExecArgs(args []string) (execFlags, error) {
	f := execFlags{stdin: stdinNull, hangup: hangupStop}
	for i, arg := range args {
		name, value, _ := strings.Cut(arg, "=")
		switch name {
		case "--version":
			f.version = true
			return f, nil
		case "--stdin":
			if value != stdinNull && value != stdinInherit {
				return f, fmt.Errorf("--stdin takes null or inherit, not %q", value)
			}
			f.stdin = value
		case "--hangup":
			if value != hangupStop && value != hangupIgnore {
				return f, fmt.Errorf("--hangup takes stop or ignore, not %q", value)
			}
			f.hangup = value
		case "--timeout":
			d, err := time.ParseDuration(value)
			if err != nil || d <= 0 {
				return f, fmt.Errorf("--timeout takes a positive duration, not %q", value)
			}
			f.timeout = d
		case "--":
			f.argv = args[i+1:]
			if len(f.argv) == 0 {
				return f, errors.New("nothing to run after --")
			}
			return f, nil
		default:
			return f, fmt.Errorf("unknown argument %q", arg)
		}
	}
	return f, errors.New("usage: [--stdin=null|inherit] [--hangup=stop|ignore] [--timeout=<d>] -- <command…>")
}
