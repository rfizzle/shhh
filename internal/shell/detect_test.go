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
