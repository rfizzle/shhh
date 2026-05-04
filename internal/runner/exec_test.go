package runner

import (
	"os"
	"testing"
)

func TestRun_SuccessfulCommand(t *testing.T) {
	code := Run("true")
	if code != 0 {
		t.Errorf("expected exit code 0, got %d", code)
	}
}

func TestRun_FailedCommand(t *testing.T) {
	code := Run("false")
	if code == 0 {
		t.Error("expected non-zero exit code for 'false'")
	}
}

func TestRun_PropagatesExitCode(t *testing.T) {
	code := Run("exit 42")
	if code != 42 {
		t.Errorf("expected exit code 42, got %d", code)
	}
}

func TestRun_UsesShellEnv(t *testing.T) {
	original := os.Getenv("SHELL")
	t.Cleanup(func() { os.Setenv("SHELL", original) })

	os.Setenv("SHELL", "/bin/sh")
	code := Run("echo hello > /dev/null")
	if code != 0 {
		t.Errorf("expected exit code 0 with /bin/sh, got %d", code)
	}
}

func TestRun_FallbackShell(t *testing.T) {
	original := os.Getenv("SHELL")
	t.Cleanup(func() { os.Setenv("SHELL", original) })

	os.Unsetenv("SHELL")
	code := Run("true")
	if code != 0 {
		t.Errorf("expected exit code 0 with fallback shell, got %d", code)
	}
}

func TestRun_InvalidCommand(t *testing.T) {
	code := Run("command_that_does_not_exist_xyz")
	if code == 0 {
		t.Error("expected non-zero exit code for invalid command")
	}
}
