//go:build integration

package cli

// A cancel put to a real engine: the claim the helper exists for — that a
// command in a container stops when its client is stopped — is a property of
// the engine's exec as much as of shhh, and nothing hermetic can stand in for
// the engine. The engine is the one SHHH_SANDBOX_IT_ENGINE names, so the same
// test runs on docker and, when it passes there, on podman; the image is
// SHHH_SANDBOX_IT_IMAGE, any image with a POSIX sh, sleep and sed — CI runs
// the sandbox image's own base. The helper is this checkout's own shhh,
// built for Linux and bound read-only where the released image carries it.

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/process"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/sandbox"
)

// integrationContainer starts a container from the engine and image the
// environment names, with this checkout's helper bound where the released
// image carries it, and probes it. It skips where they are not named.
func integrationContainer(t *testing.T) (sandbox.Container, string, string) {
	t.Helper()
	engineName, image := os.Getenv("SHHH_SANDBOX_IT_ENGINE"), os.Getenv("SHHH_SANDBOX_IT_IMAGE")
	if engineName == "" || image == "" {
		t.Skip("set SHHH_SANDBOX_IT_ENGINE (docker or podman) and SHHH_SANDBOX_IT_IMAGE to put the cancel to an engine")
	}
	enginePath, err := exec.LookPath(engineName)
	if err != nil {
		t.Fatalf("SHHH_SANDBOX_IT_ENGINE names %s, which is not on PATH: %v", engineName, err)
	}
	ctx := context.Background()

	dir := t.TempDir()
	helper := filepath.Join(dir, "shhh")
	build := exec.Command("go", "build", "-o", helper, "./cmd/shhh")
	build.Dir = filepath.Join("..", "..")
	build.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+runtime.GOARCH, "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build the helper: %v\n%s", err, out)
	}
	ws := filepath.Join(dir, "ws")
	if err := os.Mkdir(ws, 0o777); err != nil {
		t.Fatal(err)
	}

	name := "shhh-sbx-it-" + strings.ReplaceAll(t.Name(), "/", "-")
	name = strings.ToLower(name)
	args := []string{"run", "--detach", "--name", name, "--entrypoint", "",
		"--volume", ws + ":/workspace", "--workdir", "/workspace",
		"--volume", helper + ":" + sandbox.HelperPath + ":ro"}
	args = append(args, sandbox.HardeningFlags(sandbox.DefaultContainerPids, false)...)
	args = append(args, image, "sleep", "2147483647")
	run := exec.Command(enginePath, args...)
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("%s run: %v\n%s", engineName, err, out)
	}
	t.Cleanup(func() { _ = exec.Command(enginePath, "rm", "--force", name).Run() })
	c := sandbox.Container{
		Record: sandbox.Record{Name: name, Workspace: ws},
		Engine: sandbox.Engine{Name: engineName, Path: enginePath, OK: true},
	}
	if err := c.ProbeHelper(ctx); err != nil {
		t.Fatalf("the bound helper does not answer: %v", err)
	}

	return c, enginePath, name
}

func TestSandboxCancelStopsTheContainerGroup(t *testing.T) {
	c, enginePath, name := integrationContainer(t)
	ws := c.Record.Workspace
	ctx := context.Background()

	// The share, read through the helper: a file written here is the file
	// the container's command reads.
	if err := os.WriteFile(filepath.Join(ws, "from-the-host.txt"), []byte("written on the host\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	argv, err := c.HelperCommand(sandbox.HelperExec{}, "cat from-the-host.txt")
	if err != nil {
		t.Fatal(err)
	}
	if got := runner.RunCaptureArgvInAttached(ctx, "", "cat", argv, sandbox.StopArgv(argv)); !strings.Contains(got.Output, "written on the host") {
		t.Fatalf("the container did not read the host's file: %+v", got)
	}

	// The cancel: a tailed command that starts a sleep and waits on it is
	// cancelled once it is running, and no sleep is left in the container
	// inside the grace. With SHHH_SANDBOX_EXEC_DEBUG set here it is set for
	// the helper too, which then says on the command's output whether it
	// saw the hang-up; the output is logged either way.
	var env []string
	if os.Getenv("SHHH_SANDBOX_EXEC_DEBUG") != "" {
		env = []string{"SHHH_SANDBOX_EXEC_DEBUG=1"}
	}
	argv, err = c.HelperCommand(sandbox.HelperExec{Env: env}, "sleep 300 & echo started; wait")
	if err != nil {
		t.Fatal(err)
	}
	cctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	var cancelled time.Time
	go func() {
		defer close(done)
		got := runner.RunCaptureArgvTailAttached(cctx, "sleep", argv, sandbox.StopArgv(argv), func(line string) {
			if line == "started" {
				cancelled = time.Now()
				cancel()
			}
		})
		t.Logf("the cancelled command returned after %s: exit %d, %s\n%s", time.Since(cancelled).Round(time.Millisecond), got.ExitCode, got.Outcome, got.Output)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		cancel()
		t.Fatal("the cancelled command did not return")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		out := containerProcesses(t, enginePath, name)
		if liveSleeps(out) <= 1 {
			// The one sleep left is the container's own keeper, and the
			// container's init reaps what the stop killed: no zombie.
			if strings.Contains(out, " Z") {
				t.Fatalf("a zombie outlived the cancel inside the container:\n%s", out)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("a sleep outlived the cancel inside the container:\n%s", out)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// containerProcesses lists the container's processes, one `name state` a
// line, leaving out the shell that lists them. The state matters: the keeper
// is the container's first process and reaps nothing, so a command the stop
// killed after its shell had gone stays in the list as a zombie, which is
// stopped and not running.
func containerProcesses(t *testing.T, enginePath, name string) string {
	t.Helper()
	out, err := exec.Command(enginePath, "exec", name, "/bin/sh", "-c",
		`for p in /proc/[0-9]*; do
			[ "$p" = "/proc/$$" ] && continue
			c=$(cat "$p/comm" 2>/dev/null) || continue
			s=$(sed -n 's/^State:[[:space:]]*\([A-Z]\).*/\1/p' "$p/status" 2>/dev/null)
			echo "$c $s"
		done`).CombinedOutput()
	if err != nil {
		t.Fatalf("list the container's processes: %v\n%s", err, out)
	}
	return string(out)
}

// liveSleeps counts the sleeps in a listing that are not zombies.
func liveSleeps(listing string) int {
	n := 0
	for _, line := range strings.Split(listing, "\n") {
		if comm, state, _ := strings.Cut(line, " "); comm == "sleep" && state != "Z" {
			n++
		}
	}
	return n
}

// A process started in the container is read through the client's stdout and
// stopped the way a cancelled command is — the stream hung up, the far end's
// grace, then the second exec and the kill — and nothing of it is left in the
// container. With SHHH_SANDBOX_EXEC_DEBUG set the helper says on the
// process's output whether it saw the hang-up, and the output is logged.
func TestSandboxProcessStopsInsideTheContainer(t *testing.T) {
	c, enginePath, name := integrationContainer(t)
	sup, err := process.New(c.Record.Workspace, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sup.Close)
	var debug []string
	if os.Getenv("SHHH_SANDBOX_EXEC_DEBUG") != "" {
		debug = []string{"SHHH_SANDBOX_EXEC_DEBUG=1"}
	}
	sup.SetContainment(process.Containment{
		Mechanism: sandboxMechanism,
		Wrap: func(dir string, argv, env []string) ([]string, []string, error) {
			return containedExec(c, sandbox.HelperExec{Dir: dir, Env: append(env, debug...), Inherit: true},
				[]string{"/bin/sh", "-c", argv[len(argv)-1]})
		},
	})
	if _, err := sup.Execute(json.RawMessage(`{"action":"start","name":"dev","command":"echo ready; sleep 300 & wait"}`)); err != nil {
		t.Fatalf("start: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		got, _ := sup.Execute(json.RawMessage(`{"action":"read","name":"dev"}`))
		if strings.Contains(got, "ready") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the process printed nothing through the container: %q", got)
		}
		time.Sleep(100 * time.Millisecond)
	}
	stopped := time.Now()
	status, err := sup.Execute(json.RawMessage(`{"action":"stop","name":"dev"}`))
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	output, _ := sup.Execute(json.RawMessage(`{"action":"read","name":"dev","stream":"stderr"}`))
	t.Logf("the stopped process returned after %s:\n%s\n%s", time.Since(stopped).Round(time.Millisecond), status, output)
	deadline = time.Now().Add(5 * time.Second)
	for {
		out := containerProcesses(t, enginePath, name)
		if liveSleeps(out) <= 1 {
			// The one sleep left is the container's own keeper.
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the stopped process left a sleep in the container:\n%s", out)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
