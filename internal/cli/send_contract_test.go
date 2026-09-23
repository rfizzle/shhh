//go:build contract

package cli

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/rpc"
)

// shortDataHome is a data directory whose socket path fits under the
// 104-byte cap a Unix socket has on macOS, which a test's own temporary
// directory there does not always leave room for.
func shortDataHome(t *testing.T) {
	t.Helper()
	dir, err := os.MkdirTemp("", "s526")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("XDG_DATA_HOME", dir)
}

// The socket a session listens on is the real one: named for the process
// under the state directory, answering one line with what became of it, and
// gone when the session closes it.
func TestInbox_ASessionListensUnderItsPidAndTakesALine(t *testing.T) {
	shortDataHome(t)
	lines, closeInbox := openInbox(context.Background(), "")
	if lines == nil {
		t.Fatal("the inbox did not open")
	}
	path, err := inboxPath(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		line := <-lines
		if line.From == "lane-a" && line.Text == "rebase onto master" {
			line.Taken <- rpc.TakenHeld
		}
	}()
	conn, err := net.DialTimeout("unix", path, 2*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", path, err)
	}
	res, err := rpc.Send(conn, rpc.SendParams{From: "lane-a", Text: "rebase onto master"})
	conn.Close()
	if err != nil || res.Taken != rpc.TakenHeld {
		t.Fatalf("Send = %+v, %v", res, err)
	}
	closeInbox()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the socket outlived the session: %v", err)
	}
}

// A session that cannot bind takes nothing, and carries on.
func TestInbox_ASessionThatCannotBindTakesNothing(t *testing.T) {
	file, err := os.CreateTemp("", "s526")
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	t.Cleanup(func() { _ = os.Remove(file.Name()) })
	// A data directory that is a file cannot hold the inbox directory.
	t.Setenv("XDG_DATA_HOME", file.Name())
	lines, closeInbox := openInbox(context.Background(), "")
	defer closeInbox()
	if lines != nil {
		t.Fatal("an inbox that could not bind still handed back lines")
	}
}
