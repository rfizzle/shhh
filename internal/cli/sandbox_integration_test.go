//go:build integration

package cli

// A cancel put to a real engine: the claim the helper exists for — that a
// command in a container stops when its client is stopped — is a property of
// the engine's exec as much as of shhh, and nothing hermetic can stand in for
// the engine. The engine is the one SHHH_SANDBOX_IT_ENGINE names, so the same
// test runs on docker and, when it passes there, on podman; the image is
// SHHH_SANDBOX_IT_IMAGE, any image with a POSIX sh and sleep. The helper is
// this checkout's own shhh, built for Linux and bound read-only where the
// released image carries it.

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
	run := exec.Command(enginePath, "run", "--detach", "--name", name, "--entrypoint", "",
		"--volume", ws+":/workspace", "--workdir", "/workspace",
		"--volume", helper+":"+sandbox.HelperPath+":ro",
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--network", "none",
		image, "sleep", "2147483647")
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
	if got := runner.RunCaptureArgvInAttached(ctx, "", "cat", argv); !strings.Contains(got.Output, "written on the host") {
		t.Fatalf("the container did not read the host's file: %+v", got)
	}

	// The cancel: a tailed command that starts a sleep and waits on it is
	// cancelled once it is running, and no sleep is left in the container
	// inside the grace.
	argv, err = c.HelperCommand(sandbox.HelperExec{}, "sleep 300 & echo started; wait")
	if err != nil {
		t.Fatal(err)
	}
	cctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.RunCaptureArgvTailAttached(cctx, "sleep", argv, func(line string) {
			if line == "started" {
				cancel()
			}
		})
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		cancel()
		t.Fatal("the cancelled command did not return")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		out, err := exec.Command(enginePath, "exec", name, "/bin/sh", "-c",
			`for p in /proc/[0-9]*; do cat "$p/comm" 2>/dev/null; done`).CombinedOutput()
		if err != nil {
			t.Fatalf("list the container's processes: %v\n%s", err, out)
		}
		if !strings.Contains(string(out), "sleep\n") || strings.Count(string(out), "sleep\n") == 1 {
			// The one sleep left is the container's own keeper.
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("a sleep outlived the cancel inside the container:\n%s", out)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// A process started in the container is read through the client's stdout and
// stopped by the supervisor signalling the client, and nothing of it is left
// in the container: the stop reaches the helper through the stream.
func TestSandboxProcessStopsInsideTheContainer(t *testing.T) {
	c, enginePath, name := integrationContainer(t)
	sup, err := process.New(c.Record.Workspace, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sup.Close)
	sup.SetContainment(process.Containment{
		Mechanism: sandboxMechanism,
		Wrap: func(dir string, argv, env []string) ([]string, error) {
			return c.HelperCommand(sandbox.HelperExec{Dir: dir, Env: env, Inherit: true}, argv[len(argv)-1])
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
	if _, err := sup.Execute(json.RawMessage(`{"action":"stop","name":"dev"}`)); err != nil {
		t.Fatalf("stop: %v", err)
	}
	deadline = time.Now().Add(10 * time.Second)
	for {
		out, err := exec.Command(enginePath, "exec", name, "/bin/sh", "-c",
			`for p in /proc/[0-9]*; do cat "$p/comm" 2>/dev/null; done`).CombinedOutput()
		if err != nil {
			t.Fatalf("list the container's processes: %v\n%s", err, out)
		}
		if strings.Count(string(out), "sleep\n") <= 1 {
			// The one sleep left is the container's own keeper.
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the stopped process left a sleep in the container:\n%s", out)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
