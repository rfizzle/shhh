package sandbox

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

// capture runs one argv and hands back everything it printed, wrapped or
// bare. The deadline is the mechanism's rather than the command's: `cat`
// returns at once, and a wrap that hangs on a kernel that will not have it
// would otherwise take the package's whole timeout.
func capture(t *testing.T, name string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return string(out), err
}
