package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/agentcore/skill"
)

// The built-ins parse under the file rules with nothing to warn about, say
// where they live in one word, and stay small: the body is what a session
// pays when the skill is loaded.
func TestBuiltinsParseCleanly(t *testing.T) {
	got := map[string]skill.Skill{}
	for _, s := range skill.Builtins(builtinSkillFS) {
		got[s.Name] = s
		if len(s.Warnings) != 0 {
			t.Errorf("%s warns: %v", s.Name, s.Warnings)
		}
		if s.Location != skill.BuiltinLocation || s.Scope != skill.ScopeBuiltin || s.Dir != "" {
			t.Errorf("%s: location %q, scope %q, dir %q", s.Name, s.Location, s.Scope, s.Dir)
		}
		if !strings.HasPrefix(s.Description, "Use when ") || strings.Count(s.Description, ". ") > 0 {
			t.Errorf("%s: the description is one sentence that says when: %q", s.Name, s.Description)
		}
		body, err := s.Body()
		if err != nil {
			t.Fatalf("%s: %v", s.Name, err)
		}
		if n := len(body); n == 0 || n > 4096 {
			t.Errorf("%s: body is %d bytes; it is kept under 4 KB", s.Name, n)
		}
	}
	for _, want := range []string{"shhh-quality-gate", "shhh-checkout-setup"} {
		if _, ok := got[want]; !ok {
			t.Errorf("no built-in %s among %v", want, got)
		}
	}
}

// The body is served from the binary, and the content names no directory: a
// built-in has none, and naming one would send the model looking for it.
func TestBuiltinBodyComesFromTheBinary(t *testing.T) {
	c := skill.Load(nil, builtinSkillFS)
	s, ok := c.Find("shhh-quality-gate")
	if !ok {
		t.Fatalf("Load with no roots lacks the built-ins: %v", c.Names())
	}
	body, err := s.Body()
	if err != nil || !strings.HasPrefix(body, "# The shhh quality gate") {
		t.Fatalf("body = %.40q, %v", body, err)
	}
	content, err := skill.Content(s)
	if err != nil {
		t.Fatal(err)
	}
	if !skill.IsContent(content) || strings.Contains(content, "Skill directory") || strings.Contains(content, "<skill_resources>") {
		t.Errorf("built-in content:\n%s", content)
	}
	if !strings.Contains(skill.PromptBlock(c), "<location>"+skill.BuiltinLocation+"</location>") {
		t.Error("the list line says where a built-in lives")
	}
}

// File skills come first and the built-ins after them; a file skill of a
// built-in's name, at any scope, takes its place, and the diagnostic names
// both.
func TestFileSkillShadowsABuiltin(t *testing.T) {
	for _, scope := range []skill.Scope{skill.ScopeProject, skill.ScopeUser} {
		root := t.TempDir()
		writeSkill(t, root, "shhh-quality-gate", "name: shhh-quality-gate\ndescription: Ours.", "our gate")
		writeSkill(t, root, "alpha", "name: alpha\ndescription: Alpha.", "alpha")
		c := skill.Load([]skill.Root{{Path: root, Scope: scope}}, builtinSkillFS)
		names := c.Names()
		if names[0] != "alpha" || names[1] != "shhh-quality-gate" || names[len(names)-1] != "shhh-global-config" {
			t.Errorf("%s: order = %v", scope, names)
		}
		s, _ := c.Find("shhh-quality-gate")
		if s.Scope != scope {
			t.Errorf("%s: the file skill should win, got scope %q", scope, s.Scope)
		}
		if len(c.Diagnostics) != 1 || !strings.HasPrefix(c.Diagnostics[0], "built-in skill shhh-quality-gate: shadowed by ") ||
			!strings.HasSuffix(c.Diagnostics[0], s.Location) {
			t.Errorf("%s: diagnostics = %v", scope, c.Diagnostics)
		}
	}
}

func writeSkill(t *testing.T, root, name, frontmatter, body string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\n" + frontmatter + "\n---\n" + body
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}
