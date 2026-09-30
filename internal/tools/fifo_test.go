//go:build unix

package tools

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Opening a pipe blocks until a writer comes, so every reader that opens a
// path refuses one before the open and names what it is. A reader that hung
// would never return, which is why each call runs against a deadline.
func TestReaders_APipeIsRefusedNotOpened(t *testing.T) {
	dir := t.TempDir()
	pipe := filepath.Join(dir, "pipe.json")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Skip(err)
	}
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{ReadFileName, map[string]any{"path": pipe}},
		{SearchName, map[string]any{"pattern": "x", "path": pipe}},
		{QueryName, map[string]any{"paths": []string{pipe}}},
		{SqliteName, map[string]any{"path": pipe}},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			raw, err := json.Marshal(tc.args)
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				_, err := NewRecorder().Execute(tc.tool, raw)
				done <- err
			}()
			select {
			case err := <-done:
				if err == nil || !strings.Contains(err.Error(), "is a named pipe, not a regular file") {
					t.Fatalf("want the refusal naming a pipe, got %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the call opened the pipe and waited for a writer")
			}
		})
	}
}

// The outline reads Markdown by its extension, so a pipe named like a
// document reaches it; it is refused before the open like every other reader.
func TestDocumentSymbol_APipeIsRefusedNotOpened(t *testing.T) {
	pipe := filepath.Join(t.TempDir(), "pipe.md")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Skip(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := NewRecorder().Execute(DocumentSymbolName, json.RawMessage(`{"path":`+mustJSON(t, pipe)+`}`))
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "is a named pipe, not a regular file") {
			t.Fatalf("want the refusal naming a pipe, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the outline opened the pipe and waited for a writer")
	}
}

// A pipe inside a directory is passed over by a search of the directory
// rather than waited on or reported.
func TestSearch_APipeInADirectoryIsPassedOver(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o600); err != nil {
		t.Skip(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := executeSearch(json.RawMessage(`{"pattern":"x","path":` + mustJSON(t, dir) + `}`))
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("search: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the walk opened the pipe and waited for a writer")
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
