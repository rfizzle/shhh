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
}

var runCmd = func(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}

func Copy(text string) Result {
	tool := detectTool()
	if tool == "" {
		return Result{Warning: "no clipboard tool found — install xclip, xsel, or wl-copy"}
	}

	cmd := runCmd(tool)
	cmd.Stdin = strings.NewReader(text)

	if err := cmd.Run(); err != nil {
		return Result{Warning: fmt.Sprintf("%s failed: %v", tool, err)}
	}

	return Result{OK: true, Tool: tool}
}

func detectTool() string {
	if runtime.GOOS == "darwin" {
		return "pbcopy"
	}

	for _, tool := range []string{"wl-copy", "xclip", "xsel"} {
		if _, err := exec.LookPath(tool); err == nil {
			return tool
		}
	}
	return ""
}
