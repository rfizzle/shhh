package sandbox

import (
	"fmt"
	"os"
)

// RunExec is the helper, which needs a container's process groups and has
// none here: a sandbox image is Linux, and so is every container it runs in.
func RunExec([]string) int {
	fmt.Fprintln(os.Stderr, "shhh: sandbox exec: the command helper runs inside a sandbox container, not on Windows")
	return 126
}
