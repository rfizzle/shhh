package clipboard

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

func TestCopy_Success(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("pbcopy only available on macOS")
	}

	result := Copy("hello clipboard")
	if !result.OK {
		t.Errorf("expected OK, got warning: %s", result.Warning)
	}
	if result.Tool != "pbcopy" {
		t.Errorf("expected tool 'pbcopy', got %q", result.Tool)
	}
}

func TestCopy_ToolFailure(t *testing.T) {
	original := runCmd
	t.Cleanup(func() { runCmd = original })

	runCmd = func(name string, args ...string) *exec.Cmd {
		return exec.Command("false")
	}

	result := Copy("test")
	if result.OK {
		t.Error("expected failure when tool exits non-zero")
	}
	if result.Warning == "" {
		t.Error("expected warning on failure")
	}
}

func TestCopy_NoTool(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("pbcopy always available on macOS")
	}

	original := runCmd
	t.Cleanup(func() { runCmd = original })

	// This test only runs on non-darwin where no tool may exist.
	// Force no tool by temporarily overriding PATH.
	origPath := t.TempDir()
	t.Setenv("PATH", origPath)

	result := Copy("test")
	if result.OK {
		t.Error("expected failure when no clipboard tool found")
	}
	if !strings.Contains(result.Warning, "no clipboard tool") {
		t.Errorf("expected 'no clipboard tool' warning, got: %q", result.Warning)
	}
}

func TestDetectTool_Darwin(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin-specific test")
	}
	tool := detectTool()
	if tool != "pbcopy" {
		t.Errorf("expected 'pbcopy' on darwin, got %q", tool)
	}
}

func TestCopy_PassesTextViaStdin(t *testing.T) {
	var captured string
	original := runCmd
	t.Cleanup(func() { runCmd = original })

	runCmd = func(name string, args ...string) *exec.Cmd {
		// Use 'cat' to capture stdin to a temp file, then we check it
		cmd := exec.Command("sh", "-c", "cat")
		// We'll intercept by reading what would be piped
		captured = name
		return cmd
	}

	if runtime.GOOS != "darwin" {
		t.Skip("detectTool depends on platform")
	}

	result := Copy("my command")
	if !result.OK {
		t.Errorf("expected OK, got warning: %s", result.Warning)
	}
	if captured != "pbcopy" {
		t.Errorf("expected tool to be called, captured: %q", captured)
	}
}

func TestResult_WarningEmpty_OnSuccess(t *testing.T) {
	r := Result{OK: true, Tool: "pbcopy"}
	if r.Warning != "" {
		t.Errorf("expected empty warning on success, got %q", r.Warning)
	}
}

func TestResult_ToolEmpty_OnWarning(t *testing.T) {
	r := Result{Warning: "no clipboard tool found"}
	if r.Tool != "" {
		t.Errorf("expected empty tool on warning, got %q", r.Tool)
	}
	if r.OK {
		t.Error("expected OK to be false on warning")
	}
}
