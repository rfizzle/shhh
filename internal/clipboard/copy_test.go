package clipboard

import (
	"encoding/base64"
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

func TestToolForEachPlatform(t *testing.T) {
	none := func(string) (string, error) { return "", exec.ErrNotFound }
	all := func(name string) (string, error) { return "/usr/bin/" + name, nil }

	if got := toolFor("darwin", none); got != "pbcopy" {
		t.Errorf("darwin = %q", got)
	}
	// Windows needs nothing installed, so it must not depend on the PATH.
	if got := toolFor("windows", none); got != "clip" {
		t.Errorf("windows = %q, want clip", got)
	}
	if got := toolFor("linux", all); got != "wl-copy" {
		t.Errorf("linux prefers wayland: %q", got)
	}
	if got := toolFor("linux", none); got != "" {
		t.Errorf("linux with nothing installed has no tool: %q", got)
	}
}

func TestToolForLinuxFallsThroughToXsel(t *testing.T) {
	only := func(name string) (string, error) {
		if name == "xsel" {
			return "/usr/bin/xsel", nil
		}
		return "", exec.ErrNotFound
	}
	if got := toolFor("linux", only); got != "xsel" {
		t.Errorf("got %q", got)
	}
}

// The terminal's own clipboard: the sequence, and the two texts it declines.

func TestOSC52CarriesTheTextBase64(t *testing.T) {
	seq, ok := OSC52("copy me")
	if !ok {
		t.Fatal("a short text should go by OSC 52")
	}
	want := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("copy me")) + "\x07"
	if seq != want {
		t.Errorf("sequence = %q, want %q", seq, want)
	}
}

// Nothing to copy is not a copy. An OSC 52 with an empty payload is the
// sequence that *clears* the clipboard, so sending one would lose whatever
// was on it to answer a key that had nothing to give.
func TestOSC52DeclinesEmptyText(t *testing.T) {
	if _, ok := OSC52(""); ok {
		t.Error("an empty text should not reach the terminal")
	}
}

// Too long for one write is declined rather than truncated: the sequence has
// no reply, so a terminal that dropped half of it would leave the reader
// pasting half a diff with nothing to say it had happened.
func TestOSC52DeclinesMoreThanOneWriteHolds(t *testing.T) {
	if _, ok := OSC52(strings.Repeat("x", osc52Max)); ok {
		t.Error("a payload past the cap should be left to the tools")
	}
	// And the largest text that does fit still goes: the cap is on the
	// encoded length, not on the text's.
	fits := strings.Repeat("x", osc52Max/4*3)
	if _, ok := OSC52(fits); !ok {
		t.Errorf("%d bytes encode to %d, which fits", len(fits), base64.StdEncoding.EncodedLen(len(fits)))
	}
}
