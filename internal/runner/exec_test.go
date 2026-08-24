package runner

import (
	"context"
	"os"
	"strings"
	"sync"
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

func TestRunCaptureArgv_RunsExplicitArgv(t *testing.T) {
	out, code := RunCaptureArgv(context.Background(), []string{"/bin/sh", "-c", "echo wrapped; exit 4"})
	if !strings.Contains(out, "wrapped") {
		t.Errorf("expected output captured, got %q", out)
	}
	if code != 4 {
		t.Errorf("expected exit code 4, got %d", code)
	}
}

func TestRunCaptureArgv_MissingBinaryFailsVisibly(t *testing.T) {
	out, code := RunCaptureArgv(context.Background(), []string{"/nonexistent/bwrap-xyz", "true"})
	if code != -1 {
		t.Errorf("expected exit code -1 for a vanished binary, got %d", code)
	}
	if !strings.Contains(out, "error:") {
		t.Errorf("spawn failure should be reported in the output, got %q", out)
	}
}

func TestRunCaptureArgv_EmptyArgv(t *testing.T) {
	out, code := RunCaptureArgv(context.Background(), nil)
	if code != -1 || !strings.Contains(out, "error") {
		t.Errorf("empty argv should fail, got %q code %d", out, code)
	}
}

func TestRunCaptureTail_ReportsLinesAndCapturesAll(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	out, code := RunCaptureTail(context.Background(), "printf 'one\\ntwo\\n'; printf 'errline\\n' >&2", func(l string) {
		mu.Lock()
		lines = append(lines, l)
		mu.Unlock()
	})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (%q)", code, out)
	}
	if !strings.Contains(out, "one") || !strings.Contains(out, "two") || !strings.Contains(out, "errline") {
		t.Fatalf("combined output should be captured, got %q", out)
	}
	mu.Lock()
	defer mu.Unlock()
	joined := strings.Join(lines, "|")
	if !strings.Contains(joined, "one") || !strings.Contains(joined, "two") {
		t.Fatalf("completed lines should be reported as they appear, got %q", joined)
	}
}

func TestRunCaptureTail_ExitCodeAndNilCallback(t *testing.T) {
	out, code := RunCaptureTail(context.Background(), "echo boom; exit 3", nil)
	if code != 3 || !strings.Contains(out, "boom") {
		t.Fatalf("want boom/3, got %q code %d", out, code)
	}
}

func TestRunCaptureArgvTail_EmptyArgv(t *testing.T) {
	out, code := RunCaptureArgvTail(context.Background(), nil, nil)
	if code != -1 || !strings.Contains(out, "error") {
		t.Fatalf("empty argv should fail, got %q code %d", out, code)
	}
}

func TestRunCaptureArgvTail_RunsExplicitArgv(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	out, code := RunCaptureArgvTail(context.Background(), []string{"/bin/sh", "-c", "echo wrapped"}, func(l string) {
		mu.Lock()
		lines = append(lines, l)
		mu.Unlock()
	})
	if code != 0 || !strings.Contains(out, "wrapped") {
		t.Fatalf("want wrapped/0, got %q code %d", out, code)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(lines) == 0 || lines[0] != "wrapped" {
		t.Fatalf("expected the completed line reported, got %v", lines)
	}
}
