package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProjectAgentDirFindsRepoRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := ProjectAgentDir(nested); got != filepath.Join(root, ".shhh", "agents") {
		t.Fatalf("dir = %q", got)
	}
	// Project shadows global.
	global := t.TempDir()
	proj := ProjectAgentDir(nested)
	for _, dir := range []string{global, proj} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for dir, body := range map[string]string{global: `description = "global"`, proj: `description = "project"`} {
		if err := os.WriteFile(filepath.Join(dir, "helper.toml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	defs, err := LoadAgentsFrom(proj, global)
	if err != nil {
		t.Fatal(err)
	}
	if defs["helper"].Description != "project" {
		t.Fatalf("project did not shadow global: %+v", defs["helper"])
	}
}

// must fails the test on an error from setting it up.
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
