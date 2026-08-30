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
	os.WriteFile(filepath.Join(global, "helper.toml"), []byte(`description = "global"`), 0o644)
	os.WriteFile(filepath.Join(proj, "helper.toml"), []byte(`description = "project"`), 0o644)
	defs, err := LoadAgentsFrom(proj, global)
	if err != nil {
		t.Fatal(err)
	}
	if defs["helper"].Description != "project" {
		t.Fatalf("project did not shadow global: %+v", defs["helper"])
	}
}
