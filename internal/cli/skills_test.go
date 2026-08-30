package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/skill"
)

func TestSkillsListing(t *testing.T) {
	if got := skillsListing(nil); !strings.HasPrefix(got, "No skills found") {
		t.Errorf("nil catalog: %q", got)
	}
	root := t.TempDir()
	dir := filepath.Join(root, "pdf")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: pdf\ndescription: "+strings.Repeat("é", 120)+"\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "bad"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bad", "SKILL.md"), []byte("no frontmatter"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := skill.Discover([]skill.Root{{Path: root, Scope: skill.ScopeUser}})
	got := skillsListing(c)
	if !strings.Contains(got, "pdf") || !strings.Contains(got, "user") || !strings.Contains(got, "1 skill(s)") {
		t.Errorf("listing = %q", got)
	}
	if !strings.Contains(got, "bad") || !strings.Contains(got, "skipped") {
		t.Errorf("diagnostics missing from listing: %q", got)
	}
	if strings.Contains(got, "�") {
		t.Error("clipping split a multi-byte character")
	}
	detail := skillDetail(c.Skills[0])
	if !strings.Contains(detail, "location:") || !strings.Contains(detail, "scope:         user") {
		t.Errorf("detail = %q", detail)
	}
}
