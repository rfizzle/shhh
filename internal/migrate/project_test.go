package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func chdir(t *testing.T, dir string) {
	t.Helper()
	orig, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
}

func TestLegacyProjectFile_NothingWithoutTheFile(t *testing.T) {
	chdir(t, t.TempDir())
	if _, ok := legacyProjectFile(); ok {
		t.Error("detected a migration in an empty directory")
	}
}

func TestLegacyProjectFile_SeesPastANearerDirectory(t *testing.T) {
	root := t.TempDir()
	must(t, os.WriteFile(filepath.Join(root, ".shhh"), []byte("old"), 0o644))
	sub := filepath.Join(root, "child")
	must(t, os.MkdirAll(filepath.Join(sub, ".shhh"), 0o755))
	chdir(t, sub)
	p, ok := legacyProjectFile()
	if !ok || !strings.Contains(p.Steps[0], filepath.Join(root, ".shhh")) {
		t.Errorf("ancestor's file not reported: %v %+v", ok, p)
	}
}

func TestMoveProjectFileInside_RefusesWhenTheAsidePathIsTaken(t *testing.T) {
	root := t.TempDir()
	old := filepath.Join(root, ".shhh")
	must(t, os.WriteFile(old, []byte("old"), 0o644))
	must(t, os.WriteFile(old+".migrating", []byte("stale"), 0o644))
	if _, err := moveProjectFileInside(old); err == nil || !strings.Contains(err.Error(), "in the way") {
		t.Fatalf("err = %v", err)
	}
	if data, _ := os.ReadFile(old); string(data) != "old" {
		t.Error("the old file was touched")
	}
}

func TestLegacyProjectFile_MovesTheFileInside(t *testing.T) {
	root := t.TempDir()
	must(t, os.WriteFile(filepath.Join(root, ".shhh"), []byte("old context"), 0o644))
	sub := filepath.Join(root, "src")
	must(t, os.MkdirAll(sub, 0o755))
	chdir(t, sub)

	p, ok := legacyProjectFile()
	if !ok {
		t.Fatal("not detected")
	}
	if !p.Auto() || len(p.Steps) != 1 || !strings.Contains(p.Steps[0], "project.md") {
		t.Errorf("pending = %+v", p)
	}
	lines, err := p.Apply()
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "moved ") {
		t.Errorf("lines = %v", lines)
	}
	data, err := os.ReadFile(filepath.Join(root, ".shhh", "project.md"))
	if err != nil || string(data) != "old context" {
		t.Errorf("project.md = %q, %v", data, err)
	}
	if _, ok := legacyProjectFile(); ok {
		t.Error("still detected after the move")
	}
}
