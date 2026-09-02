package runner

import (
	"context"
	"os"
	"os/exec"
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
	t.Cleanup(func() { _ = os.Setenv("SHELL", original) })

	must(t, os.Setenv("SHELL", "/bin/sh"))
	code := Run("echo hello > /dev/null")
	if code != 0 {
		t.Errorf("expected exit code 0 with /bin/sh, got %d", code)
	}
}

func TestRun_FallbackShell(t *testing.T) {
	original := os.Getenv("SHELL")
	t.Cleanup(func() { _ = os.Setenv("SHELL", original) })

	must(t, os.Unsetenv("SHELL"))
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

func TestSessionEnv_ReachesEveryCapturedCommand(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	SetSessionEnv([]string{"SHHH_TEST_SECRET=s3cret"})
	t.Cleanup(func() { SetSessionEnv(nil) })
	ctx := context.Background()
	const cmd = `printf '%s' "$SHHH_TEST_SECRET"`
	runs := map[string]func() (string, int){
		"RunCapture":         func() (string, int) { return RunCapture(ctx, cmd) },
		"RunCaptureTail":     func() (string, int) { return RunCaptureTail(ctx, cmd, nil) },
		"RunCaptureIn":       func() (string, int) { return RunCaptureIn(ctx, t.TempDir(), cmd) },
		"RunCaptureArgv":     func() (string, int) { return RunCaptureArgv(ctx, []string{"/bin/sh", "-c", cmd}) },
		"RunCaptureArgvTail": func() (string, int) { return RunCaptureArgvTail(ctx, []string{"/bin/sh", "-c", cmd}, nil) },
	}
	for name, run := range runs {
		if out, code := run(); out != "s3cret" || code != 0 {
			t.Errorf("%s: out=%q code=%d", name, out, code)
		}
	}
	SetSessionEnv(nil)
	if Environ() != nil {
		t.Fatal("no session pairs must leave the command to inherit")
	}
}

// must fails the test on an error from setting it up.
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// A captured command does not go through $SHELL. The pointed version of the
// test: $SHELL names a binary that is not there, and the command still runs —
// which it can only do if nothing read $SHELL.
func TestCapturedCommandsIgnoreTheUsersShell(t *testing.T) {
	t.Setenv("SHELL", "/definitely/not/a/shell/fish")
	out, code := RunCapture(context.Background(), "printf ok")
	if code != 0 || out != "ok" {
		t.Fatalf("out=%q code=%d", out, code)
	}
}

// And it is bash where bash is present, not merely something POSIX: the
// constructs a model reaches for by default are bash's.
func TestCapturedCommandsGetBashWhereThereIsOne(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash on PATH")
	}
	t.Setenv("SHELL", "/definitely/not/a/shell/fish")
	// A heredoc, which is how a model writes a file, and which fish has not
	// got at all.
	out, code := RunCapture(context.Background(), "cat <<'EOF'\nheredoc\nEOF")
	if code != 0 || strings.TrimSpace(out) != "heredoc" {
		t.Fatalf("heredoc: out=%q code=%d", out, code)
	}
	if out, code := RunCapture(context.Background(), `[[ 1 == 1 ]] && printf bash`); code != 0 || out != "bash" {
		t.Fatalf("[[: out=%q code=%d", out, code)
	}
}

// The generator keeps the user's shell, because its command is one the user
// runs and keeps: Run reads $SHELL where the captured runners do not.
func TestRunStaysOnTheUsersShell(t *testing.T) {
	t.Setenv("SHELL", "/definitely/not/a/shell/fish")
	if code := Run("true"); code == 0 {
		t.Error("Run succeeded with an unusable $SHELL, so it is no longer reading it")
	}
}
