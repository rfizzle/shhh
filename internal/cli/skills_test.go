package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/skill"
)

func TestSkillsListing(t *testing.T) {
	if got := skillsListing(nil); !strings.Contains(got, "⊘ no skills found") {
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
	if !strings.Contains(got, "pdf") || !strings.Contains(got, "user") || !strings.Contains(got, "1 skill") {
		t.Errorf("listing = %q", got)
	}
	if !strings.Contains(got, "bad") || !strings.Contains(got, "skipped") {
		t.Errorf("diagnostics missing from listing: %q", got)
	}
	if strings.Contains(got, "�") {
		t.Error("clipping split a multi-byte character")
	}
	detail := skillDetail(c.Skills[0])
	if !strings.Contains(detail, "location:") || !strings.Contains(detail, "shhh skills pdf — user") {
		t.Errorf("detail = %q", detail)
	}
}

// goldenSkills is a catalog as a session loads it: one skill of the user's
// own, then the skills the binary carries.
func goldenSkills(t *testing.T) *skill.Catalog {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "changelog")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: changelog\ndescription: Use when writing the release notes for a tagged version.\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return skill.Load([]skill.Root{{Path: root, Scope: skill.ScopeUser}})
}

// The built-ins are listed after the file skills with the scope word the
// listing uses, and shown as living in the binary.
func TestSkillsListing_TheBuiltinsFollowTheFileSkills(t *testing.T) {
	c := goldenSkills(t)
	got := skillsReport(c).Render(200)
	user := strings.Index(got, "changelog")
	gate := strings.Index(got, "shhh-quality-gate")
	if user < 0 || gate < user || !strings.Contains(got, "[built-in]") || !strings.Contains(got, "3 skills") {
		t.Errorf("listing = %q", got)
	}
	s, _ := c.Find("shhh-checkout-setup")
	if detail := skillDetail(s); !regexp.MustCompile(`location: +built-in\n`).MatchString(detail) || strings.Contains(detail, "resources") {
		t.Errorf("detail = %q", detail)
	}
}

// Every `shhh …` command the built-in skills name is one the binary has, with
// the flags it is shown with: a skill that teaches a command that is not
// there costs the model a failed call and the reader a wrong answer.
// See docs/capabilities/skills.md#shhh-carries-skills-of-its-own.
func TestBuiltinSkillsNameOnlyCommandsThatExist(t *testing.T) {
	root := NewRootCmd()
	named := regexp.MustCompile("`shhh ([^`]+)`")
	for _, s := range skill.Builtins() {
		body, err := s.Body()
		if err != nil {
			t.Fatal(err)
		}
		found := 0
		// A command can wrap across a line in the body, so it is read with
		// the line breaks as spaces.
		for _, m := range named.FindAllStringSubmatch(strings.Join(strings.Fields(body), " "), -1) {
			var words, flags []string
			for _, f := range strings.Fields(m[1]) {
				switch {
				case strings.HasPrefix(f, "--"):
					flags = append(flags, strings.TrimPrefix(f, "--"))
				case strings.ContainsAny(f, "<[") || len(flags) > 0:
					// An argument: a placeholder, or the value after a flag.
				default:
					words = append(words, f)
				}
			}
			cmd, rest, err := root.Find(words)
			if err != nil || len(rest) > 0 || cmd == root {
				t.Errorf("%s names `shhh %s`, which is not a command (%v, left %v)", s.Name, m[1], err, rest)
				continue
			}
			for _, f := range flags {
				if cmd.Flags().Lookup(f) == nil {
					t.Errorf("%s names `shhh %s`, whose command has no --%s", s.Name, m[1], f)
				}
			}
			found++
		}
		if s.Name == "shhh-checkout-setup" && found < 8 {
			t.Errorf("%s names %d commands; the check found too few to be reading it", s.Name, found)
		}
	}
}
