//go:build !windows

package sandbox

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// A deny path that is neither a file nor a directory cannot be masked, and
// must be refused rather than silently left reachable.
//
// It is here rather than beside the other resolve tests because it needs a
// fifo to make its point, and syscall.Mkfifo does not exist on Windows — a
// runtime skip cannot rescue a file that names a symbol the platform has
// never defined.
func TestResolveRefusesUnmaskableDenyType(t *testing.T) {
	home := testHome(t)
	fifo := filepath.Join(home, "weird")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("cannot create fifo: %v", err)
	}
	policy, _ := workspacePolicy(t)
	policy.DenyExtra = []string{fifo}

	_, err := resolvePolicy(policy)
	if err == nil || !strings.Contains(err.Error(), "wrap unsupported") {
		t.Fatalf("unmaskable deny path must be refused, got %v", err)
	}
}
