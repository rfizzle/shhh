package skill

import (
	"os"
	"strings"
	"testing"
)

// A written draft is a skill the reader loads with no warning, at the path
// the project scope searches first; a second write of the same name is
// refused rather than replacing the first.
func TestSkillWrite_ADraftIsASkillTheReaderLoads(t *testing.T) {
	root := t.TempDir()
	d := Draft{Name: "check-then-build", Description: `Use when: the tree changed and you want it "checked"`,
		Steps: []string{"go vet ./...", "go build ./...", "go test ./..."}}
	path, err := Write(root, d)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if path != DraftPath(root, d.Name) {
		t.Fatalf("path = %s", path)
	}
	s, err := LoadFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if s.Name != d.Name || s.Description != d.Description || len(s.Warnings) != 0 {
		t.Fatalf("loaded %+v", s)
	}
	body, _ := s.Body()
	if !strings.Contains(body, "2. go build ./...") {
		t.Fatalf("body = %q", body)
	}
	if _, err := Write(root, d); err == nil || !strings.Contains(err.Error(), "already there") {
		t.Fatalf("a second write: %v", err)
	}
}

// A draft the reader would refuse or warn about is never written.
func TestSkillWrite_RefusesADraftTheReaderWouldNotTake(t *testing.T) {
	for name, d := range map[string]Draft{
		"a bad name":       {Name: "Check Then", Description: "x", Steps: []string{"a"}},
		"no description":   {Name: "a", Steps: []string{"a"}},
		"two-line summary": {Name: "a", Description: "x\ny: z", Steps: []string{"a"}},
		"a backslash":      {Name: "a", Description: `x\y`, Steps: []string{"a"}},
		"no steps":         {Name: "a", Description: "x"},
		"a two-line step":  {Name: "a", Description: "x", Steps: []string{"a\nb"}},
	} {
		root := t.TempDir()
		if _, err := Write(root, d); err == nil {
			t.Errorf("%s: written", name)
		}
		if _, err := os.Stat(DraftPath(root, d.Name)); err == nil {
			t.Errorf("%s: a file was left", name)
		}
	}
}
