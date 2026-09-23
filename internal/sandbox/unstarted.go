package sandbox

import "strings"

// A contained command is a mechanism with the execution shell behind it, and
// the spawn the runner makes is the mechanism's. When the shell is the part
// that is missing, the spawn succeeds — the mechanism starts, sets up the
// containment, and only then fails to exec the shell — so no spawn error
// reaches the runner, and what comes back is a process that exited. Left at
// that, a missing shell reads as a command that ran and failed, the moment a
// mechanism is in front of it.
//
// Neither mechanism reserves an exit code for it. Bubblewrap reports its own
// failed exec as `bwrap: execvp <path>: <reason>` and exits 1; Seatbelt execs
// env in front of the shell, and env reports `env: <path>: <reason>` and
// exits 127, which is also what a shell answers for a program that is not
// installed. A code alone would therefore file an ordinary failed command
// under a broken machine, so the reading is the mechanism's own line: the
// whole of the output is one line naming the very shell the wrap put there,
// in the form the program in front of it writes, with the code it exits with.
// A command that ran cannot produce that — its shell would have had to start
// to print anything.
//
// The bridge a host list puts in front of the shell under bubblewrap is the
// third program that can be the one whose exec fails, and it is read the same
// way in its own words.
// See docs/capabilities/containment.md#a-command-that-never-started-names-what-it-needed.
var unstartedShell = map[string][]struct {
	prefix string
	code   int
}{
	"bwrap": {
		{"bwrap: execvp ", 1},
		{"shhh: network bridge: fork/exec ", 127},
	},
	"sandbox-exec": {
		{"env: ", 127},
	},
}

// ShellNotStarted reports whether a contained command's ending is the
// mechanism saying it could not exec the execution shell inside the sandbox,
// and returns the mechanism's line as the detail when it is. mechanism is the
// Availability's, output and code are the command's combined output and exit
// status as the runner read them.
func ShellNotStarted(mechanism, output string, code int) (string, bool) {
	line := strings.TrimSuffix(output, "\n")
	if line == "" || strings.Contains(line, "\n") {
		return "", false
	}
	shell := shellPath()
	for _, form := range unstartedShell[mechanism] {
		if code == form.code && strings.HasPrefix(line, form.prefix+shell+": ") {
			return line, true
		}
	}
	return "", false
}
