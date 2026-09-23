package sandbox

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
)

// BridgeArg is the first argument that makes the shhh binary the network
// bridge inside a bubblewrap namespace rather than the program. main answers
// it before anything else runs, because inside containment the settings, the
// keymap and the state directory are all behind the mask and none of them is
// wanted.
const BridgeArg = "__shhh-net-bridge"

// bridgePort is the port the bridge listens on inside the command's
// namespace. It is fixed because the namespace is the command's own and
// nothing else is in it, and a fixed port is what lets the proxy variables be
// written into the environment before anything has started.
const bridgePort = "3128"

// Where the bridge's two halves are bound inside the namespace: the program
// itself and the proxy's socket, both on the command's private /tmp — the
// tmpfs is the one place bubblewrap can create a mount point, and the
// program's own path may be one the tmpfs or a mask has just hidden.
const (
	bridgeExeName  = ".shhh-net-bridge"
	bridgeSockName = ".shhh-net.sock"
)

// bridgeProgram is the binary the bridge runs as; a variable so an argv test
// does not depend on where the test binary happens to be.
var bridgeProgram = os.Executable

// RunBridge is the bridge: it listens on bridgePort on the namespace's own
// loopback, carries every connection to the proxy's socket, and runs the
// contained command beneath it, returning the command's exit status.
//
// It is a process of its own inside the namespace rather than anything
// bubblewrap could do, because a namespace with no network has no route out
// at all — a socket file bound into it is the one way through, and no tool a
// command runs will speak to a proxy over a file.
//
// args are the socket path, "--", and the command's argv.
func RunBridge(args []string) int {
	if len(args) < 3 || args[1] != "--" {
		fmt.Fprintln(os.Stderr, "shhh: network bridge: usage: <socket> -- <command…>")
		return 126
	}
	sock, argv := args[0], args[2:]
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", bridgePort))
	if err != nil {
		fmt.Fprintf(os.Stderr, "shhh: network bridge: %v\n", err)
		return 126
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				up, err := net.Dial("unix", sock)
				if err != nil {
					_ = c.Close()
					return
				}
				pipe(c, up)
			}()
		}
	}()

	// The command is in the caller's process group, so a signal meant for it
	// already reaches it; the bridge takes them only so it outlives the
	// command and can hand back its status. Caught rather than ignored, which
	// is what makes the command start with the default dispositions.
	sigs := make(chan os.Signal, 8)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	go func() {
		for range sigs {
		}
	}()

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err = cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exit):
		if ws, ok := exit.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			// The status a signal leaves is not an exit code, so the bridge
			// ends the same way to hand it on intact.
			signal.Reset(ws.Signal())
			if self, err := os.FindProcess(os.Getpid()); err == nil {
				_ = self.Signal(ws.Signal())
			}
			return 128 + int(ws.Signal())
		}
		return exit.ExitCode()
	}
	fmt.Fprintf(os.Stderr, "shhh: network bridge: %v\n", err)
	return 127
}

// bridgePaths are where the bridge's program and the proxy's socket are
// bound inside the namespace.
func bridgePaths(tmpdir string) (exe, sock string) {
	return filepath.Join(tmpdir, bridgeExeName), filepath.Join(tmpdir, bridgeSockName)
}
