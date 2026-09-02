package shell

import (
	"os"
	"runtime"
	"testing"
)

func TestDetect_DefaultShell(t *testing.T) {
	t.Setenv("SHELL", "/bin/zsh")
	info := Detect()

	if info.Shell != "zsh" {
		t.Errorf("expected shell 'zsh', got %q", info.Shell)
	}
}

func TestDetect_FallbackShell(t *testing.T) {
	t.Setenv("SHELL", "")
	if err := os.Unsetenv("SHELL"); err != nil {
		t.Fatal(err)
	}
	info := Detect()

	if info.Shell != "sh" {
		t.Errorf("expected fallback shell 'sh', got %q", info.Shell)
	}
}

func TestDetect_OS(t *testing.T) {
	info := Detect()

	if info.OS != runtime.GOOS {
		t.Errorf("expected OS %q, got %q", runtime.GOOS, info.OS)
	}
}

func TestDetect_Arch(t *testing.T) {
	info := Detect()

	if info.Arch != runtime.GOARCH {
		t.Errorf("expected arch %q, got %q", runtime.GOARCH, info.Arch)
	}
}

func TestDetect_Cwd(t *testing.T) {
	info := Detect()

	cwd, _ := os.Getwd()
	if info.Cwd != cwd {
		t.Errorf("expected cwd %q, got %q", cwd, info.Cwd)
	}
}

// DetectExec names the shell the session's own commands go through, which is
// not the one the user is sitting in. A prompt built from the wrong one is
// the drift the package comment exists to forbid.
func TestDetectExec_NamesTheExecutionShellNotTheUsers(t *testing.T) {
	t.Setenv("SHELL", "/usr/bin/fish")
	if got := Detect().Shell; got != "fish" {
		t.Fatalf("Detect().Shell = %q, want fish", got)
	}
	info := DetectExec()
	if info.Shell != Execution().Name {
		t.Errorf("DetectExec().Shell = %q, want %q", info.Shell, Execution().Name)
	}
	if info.Shell == "fish" {
		t.Error("DetectExec() reported the user's shell")
	}
}

// Everything else about the machine is the same answer, so a caller can swap
// one for the other without losing the rest.
func TestDetectExec_KeepsTheRestOfDetect(t *testing.T) {
	base, exec := Detect(), DetectExec()
	base.Shell, exec.Shell = "", ""
	if base != exec {
		t.Errorf("DetectExec() = %+v, Detect() = %+v", exec, base)
	}
}
