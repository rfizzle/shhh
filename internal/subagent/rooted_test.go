package subagent

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func rootedPath(t *testing.T, root, name, args string) (string, error) {
	t.Helper()
	out, err := RootArgs(root, name, json.RawMessage(args))
	if err != nil {
		return "", err
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("unmarshal rooted args: %v", err)
	}
	p, _ := m["path"].(string)
	return p, nil
}

func TestRootArgs_RelativeJoinsRoot(t *testing.T) {
	root := t.TempDir()
	p, err := rootedPath(t, root, "read_file", `{"path":"sub/main.go"}`)
	if err != nil {
		t.Fatal(err)
	}
	if p != filepath.Join(root, "sub/main.go") {
		t.Fatalf("relative path not rooted: %s", p)
	}
}

func TestRootArgs_AbsoluteReadAllowed(t *testing.T) {
	p, err := rootedPath(t, t.TempDir(), "read_file", `{"path":"/etc/hostname"}`)
	if err != nil {
		t.Fatal(err)
	}
	if p != "/etc/hostname" {
		t.Fatalf("absolute read path rewritten: %s", p)
	}
}

func TestRootArgs_MutatingEscapeRefused(t *testing.T) {
	root := t.TempDir()
	if _, err := rootedPath(t, root, "write_file", `{"path":"/tmp/other/x.go","content":"x"}`); err == nil {
		t.Fatal("absolute mutating path outside the root must be refused")
	}
	if _, err := rootedPath(t, root, "edit_file", `{"path":"../escape.go","old_text":"a","new_text":"b"}`); err == nil {
		t.Fatal("relative mutating path escaping the root must be refused")
	}
	p, err := rootedPath(t, root, "write_file", `{"path":"ok.go","content":"x"}`)
	if err != nil {
		t.Fatal(err)
	}
	if p != filepath.Join(root, "ok.go") {
		t.Fatalf("in-root mutating path not rooted: %s", p)
	}
}

func TestRootArgs_OptionalPathDefaultsToRoot(t *testing.T) {
	root := t.TempDir()
	for _, tool := range []string{"search", "glob"} {
		p, err := rootedPath(t, root, tool, `{"pattern":"foo"}`)
		if err != nil {
			t.Fatal(err)
		}
		if p != root {
			t.Fatalf("%s: empty path should default to root, got %q", tool, p)
		}
	}
}

func TestRootArgs_UnknownToolUntouched(t *testing.T) {
	raw := `{"url":"https://example.com"}`
	out, err := RootArgs(t.TempDir(), "web_fetch", json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != raw {
		t.Fatalf("non-path tool args rewritten: %s", out)
	}
}

func TestDisplayPath(t *testing.T) {
	root := t.TempDir()
	if got := displayPath(root, filepath.Join(root, "a/b.go")); got != "a/b.go" {
		t.Fatalf("displayPath = %q", got)
	}
	if got := displayPath(root, "/somewhere/else.go"); !strings.HasPrefix(got, "/somewhere") {
		t.Fatalf("out-of-root path should stay absolute: %q", got)
	}
}
