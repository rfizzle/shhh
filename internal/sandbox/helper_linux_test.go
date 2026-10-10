package sandbox

import (
	"bufio"
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// helperRun is the helper, re-executed from this test binary (TestMain), with
// its stdin a pipe the test holds — the far end of the stream an engine's exec
// would carry — and its output read a line at a time.
type helperRun struct {
	cmd   *exec.Cmd
	stdin *os.File
	lines chan string
	done  chan error
}

func startHelper(t *testing.T, args ...string) *helperRun {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	out, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], append([]string{ExecArg}, args...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = r, outW, outW
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	_ = outW.Close()
	h := &helperRun{cmd: cmd, stdin: w, lines: make(chan string, 64), done: make(chan error, 1)}
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			h.lines <- sc.Text()
		}
		close(h.lines)
		h.done <- cmd.Wait()
	}()
	t.Cleanup(func() {
		_ = w.Close()
		_ = cmd.Process.Kill()
	})
	return h
}

// await reads lines until one starts with prefix, and returns it.
func (h *helperRun) await(t *testing.T, prefix string) string {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case line, ok := <-h.lines:
			if !ok {
				t.Fatalf("the helper's output ended before %q", prefix)
			}
			if strings.HasPrefix(line, prefix) {
				return line
			}
		case <-deadline:
			t.Fatalf("no %q from the helper within 10s", prefix)
		}
	}
}

// rest is everything the helper printed after what was already read, once
// it has exited, and its exit code.
func (h *helperRun) rest(t *testing.T, within time.Duration) (string, int) {
	t.Helper()
	var b strings.Builder
	deadline := time.After(within)
	for {
		select {
		case line, ok := <-h.lines:
			if !ok {
				err := <-h.done
				code := 0
				var exit *exec.ExitError
				if errors.As(err, &exit) {
					code = exit.ExitCode()
				} else if err != nil {
					t.Fatalf("wait: %v", err)
				}
				return b.String(), code
			}
			b.WriteString(line + "\n")
		case <-deadline:
			t.Fatalf("the helper had not exited after %s; it printed:\n%s", within, b.String())
		}
	}
}

// alive reports whether pid is a process that has not finished: absent, or a
// zombie waiting on whoever inherited it, is gone.
func alive(pid int) bool {
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	fields := strings.Fields(string(stat[strings.LastIndexByte(string(stat), ')')+1:]))
	return len(fields) > 0 && fields[0] != "Z" && fields[0] != "X"
}

// The stream hanging up is the cancel. The shell is interrupted first — its
// trap says so — and the sleep it started in the background, which ignores
// an interrupt as every asynchronous command in a non-interactive shell does,
// is frozen and killed after the grace rather than left behind in the
// container. The helper exits with the shell's own status.
func TestSandboxExecStopsTheGroupOnHangup(t *testing.T) {
	h := startHelper(t, "--stdin=null", "--hangup=stop", "--",
		"/bin/sh", "-c", `trap 'echo interrupted; exit 7' INT; sleep 300 & echo "sleeping $!"; wait`)
	pid, err := strconv.Atoi(strings.TrimPrefix(h.await(t, "sleeping "), "sleeping "))
	if err != nil {
		t.Fatal(err)
	}
	if !alive(pid) {
		t.Fatalf("the sleep %d is not running before the hang-up", pid)
	}
	_ = h.stdin.Close()
	out, code := h.rest(t, 10*time.Second)
	if !strings.Contains(out, "interrupted") {
		t.Errorf("the group was not interrupted before it was killed:\n%s", out)
	}
	if code != 7 {
		t.Errorf("exit = %d, want the shell's 7", code)
	}
	for i := 0; i < 20 && alive(pid); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if alive(pid) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Fatalf("the sleep that ignored the interrupt outlived the stop")
	}
}

// A caller that closes its stdin once it has written what it had to — a
// hook's payload — says so with --hangup=ignore, and the command runs to its
// end. --timeout is that caller's ceiling instead, since nothing on the other
// end of the stream will stop the command once it has let go.
func TestSandboxExecIgnoresHangupWhenTold(t *testing.T) {
	t.Run("runs to its end", func(t *testing.T) {
		h := startHelper(t, "--hangup=ignore", "--", "/bin/sh", "-c", "echo ready; sleep 0.5; echo finished; exit 3")
		h.await(t, "ready")
		_ = h.stdin.Close()
		out, code := h.rest(t, 10*time.Second)
		if !strings.Contains(out, "finished") || code != 3 {
			t.Errorf("exit %d, output:\n%s\nwant the command run to its end and its status 3", code, out)
		}
	})
	t.Run("the timeout stops it", func(t *testing.T) {
		started := time.Now()
		h := startHelper(t, "--hangup=ignore", "--timeout=300ms", "--", "/bin/sh", "-c", "echo ready; sleep 30; echo finished")
		h.await(t, "ready")
		_ = h.stdin.Close()
		out, code := h.rest(t, 10*time.Second)
		if strings.Contains(out, "finished") || code == 0 {
			t.Errorf("exit %d, output:\n%s\nwant the command stopped at its timeout", code, out)
		}
		if took := time.Since(started); took > 8*time.Second {
			t.Errorf("the stop took %s", took)
		}
	})
}

// A command's stdin is the stream the cancel travels on and not input for
// it: with --stdin=null the child reads nothing the client writes, as a
// captured command on the host reads nothing. With --stdin=inherit — a
// process whose input is written to it — the child reads the stream.
func TestSandboxExecKeepsTheChildOffItsStdin(t *testing.T) {
	t.Run("null", func(t *testing.T) {
		h := startHelper(t, "--stdin=null", "--", "/bin/sh", "-c", "cat; echo end-of-input")
		if _, err := io.WriteString(h.stdin, "not-for-the-command\n"); err != nil {
			t.Fatal(err)
		}
		out, code := h.rest(t, 10*time.Second)
		if strings.Contains(out, "not-for-the-command") || !strings.Contains(out, "end-of-input") || code != 0 {
			t.Errorf("exit %d, output:\n%s\nwant a child that read /dev/null and finished", code, out)
		}
	})
	t.Run("inherit", func(t *testing.T) {
		h := startHelper(t, "--stdin=inherit", "--", "/bin/sh", "-c", `read line; echo "read $line"`)
		if _, err := io.WriteString(h.stdin, "written-to-it\n"); err != nil {
			t.Fatal(err)
		}
		out, code := h.rest(t, 10*time.Second)
		if !strings.Contains(out, "read written-to-it") || code != 0 {
			t.Errorf("exit %d, output:\n%s\nwant a child that read the stream", code, out)
		}
	})
}

// The flags are refused by name rather than taken as the command, and
// --version answers the probe a session makes before its first turn.
func TestSandboxExecFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--stdin=tty", "--", "true"},
		{"--hangup=later", "--", "true"},
		{"--timeout=soon", "--", "true"},
		{"--"},
		{"true"},
	} {
		if _, err := parseExecArgs(args); err == nil {
			t.Errorf("parseExecArgs(%q) accepted it", args)
		}
	}
	f, err := parseExecArgs([]string{"--hangup=ignore", "--timeout=2s", "--", "sh", "-c", "x"})
	if err != nil || f.stdin != stdinNull || f.hangup != hangupIgnore || f.timeout != 2*time.Second || len(f.argv) != 3 {
		t.Errorf("parseExecArgs = %+v, %v", f, err)
	}
	out, err := exec.Command(os.Args[0], ExecArg, "--version").Output()
	if err != nil || string(out) != helperBanner+HelperVersion+"\n" {
		t.Errorf("--version = %q, %v", out, err)
	}
}
