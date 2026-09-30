//go:build unix

package tools

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// Opening a pipe to read the header blocks until a writer comes, and nothing
// bounds that wait.
func TestSqlite_APipeIsRefusedNotOpened(t *testing.T) {
	f := filepath.Join(t.TempDir(), "pipe")
	if err := syscall.Mkfifo(f, 0o600); err != nil {
		t.Skip(err)
	}
	_, err := runSqlite(t, NewRecorder(), map[string]any{"path": f})
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("want a refusal, got %v", err)
	}
}
