package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/rpc"
)

// The socket is the only door into a served session, so it is created for the
// user alone.
func TestServe_SocketIsPrivate(t *testing.T) {
	dir, err := os.MkdirTemp("", "s")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "serve.sock")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serveOnSocket(ctx, rpc.NewServer(nil), path) }()
	var mode os.FileMode
	for i := 0; i < 300; i++ {
		if fi, err := os.Stat(path); err == nil {
			mode = fi.Mode().Perm()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if mode != 0o600 {
		t.Fatalf("socket mode = %o, want 600", mode)
	}
}
