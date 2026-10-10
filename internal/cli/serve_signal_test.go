//go:build !windows

package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/rpc"
)

// A server stopped with SIGTERM serves out and unlinks its socket, so the
// next start is not refused by a file left behind.
func TestServe_ASignalUnlinksTheSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s")
	done := make(chan error, 1)
	go func() { done <- serveUntilSignal(context.Background(), rpc.NewServer(nil), path) }()

	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the server never listened")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// The handler is registered before the listen, so the signal is the
	// server's to take and does not reach the test binary.
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the server did not stop on SIGTERM")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the socket is still there after the signal: %v", err)
	}
}
