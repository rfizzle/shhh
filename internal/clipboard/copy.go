package clipboard

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

type Result struct {
	OK      bool
	Tool    string
	Warning string
	// NoTool says the warning is a program that is not installed rather
	// than one that ran and failed. It is the one failure the caller can
	// still do something about: with nothing on this machine to copy with,
	// a terminal that never said it takes a clipboard write is worth
	// asking anyway, because an unanswered write beats a copy that goes
	// nowhere (osc52.go).
	NoTool bool
}

var runCmd = func(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}

// Copy puts text on the clipboard of the machine shhh is running on, through
// whichever external tool this platform has.
//
// It is the middle of three attempts rather than the first. The order a
// copy is offered in is: a terminal that named itself as one that takes a
// clipboard write gets the text directly (osc52.go), because a tool on this
// machine copies to this machine — which over ssh is not the machine the
// reader is sitting at; failing that, this, because a tool that is installed
// is a copy somebody can be told went; and failing both, the sequence goes
// to the terminal anyway under Unconfirmed. The last of those never runs
// ahead of a working tool: it cannot be confirmed, and a copy that is known
// to have landed is worth more than one that probably did.
func Copy(text string) Result {
	tool := detectTool()
	if tool == "" {
		return Result{NoTool: true, Warning: "no clipboard tool found — install xclip, xsel, or wl-copy"}
	}

	cmd := runCmd(tool)
	cmd.Stdin = strings.NewReader(text)

	if err := cmd.Run(); err != nil {
		return Result{Warning: fmt.Sprintf("%s failed: %v", tool, err)}
	}

	return Result{OK: true, Tool: tool}
}

func detectTool() string { return toolFor(runtime.GOOS, exec.LookPath) }

// toolFor is detectTool with the platform and the PATH passed in, so the
// answer for a machine that is not this one can still be asserted. A branch
// tested only on the platform it is for is a branch tested nowhere.
func toolFor(goos string, look func(string) (string, error)) string {
	switch goos {
	case "darwin":
		return "pbcopy"
	case "windows":
		// Built into Windows and takes text on stdin, which is the shape
		// every other tool here already has. Nothing to install and nothing
		// to look for, so there is no fallback and no warning to give.
		return "clip"
	}

	for _, tool := range []string{"wl-copy", "xclip", "xsel"} {
		if _, err := look(tool); err == nil {
			return tool
		}
	}
	return ""
}
