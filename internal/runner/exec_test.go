package runner

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
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

func TestRunCapture_CapturesOutputAndExitCode(t *testing.T) {
	out, code := RunCapture(context.Background(), "echo captured; exit 3")
	if !strings.Contains(out, "captured") {
		t.Errorf("expected output captured, got %q", out)
	}
	if code != 3 {
		t.Errorf("expected exit code 3, got %d", code)
	}
}

func TestRunCapture_CombinesStderr(t *testing.T) {
	out, code := RunCapture(context.Background(), "echo oops 1>&2")
	if !strings.Contains(out, "oops") {
		t.Errorf("expected stderr captured, got %q", out)
	}
	if code != 0 {
		t.Errorf("expected exit code 0, got %d", code)
	}
}

func TestRunCapture_ContextCancelKills(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, code := RunCapture(ctx, "sleep 5")
	if code == 0 {
		t.Error("killed command should not report success")
	}
}
