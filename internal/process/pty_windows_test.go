//go:build windows

package process

import (
	"os/exec"
	"strings"
	"testing"
)

// The refusal is the whole of the feature on this platform, so it is what
// there is to test: a sentence saying there is no terminal to give and what
// to do instead, rather than a build that does not link or a process started
// as though the request had been granted.
func TestStartPTY_RefusesInASentence(t *testing.T) {
	f, err := startPTY(exec.Command("cmd", "/c", "echo hi"))
	if err == nil {
		t.Fatal("a terminal must be refused here, not improvised")
	}
	if f != nil {
		t.Error("a refusal must hand back nothing to close")
	}
	if !strings.Contains(err.Error(), "Windows") || !strings.Contains(err.Error(), "without pty") {
		t.Errorf("the refusal should name the platform and the way on: %v", err)
	}
}
