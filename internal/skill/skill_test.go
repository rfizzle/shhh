package skill

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestLoadFileReadsEveryField(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "pdf-processing", `name: pdf-processing
description: "Extract PDF text. Use when: the user mentions PDFs."
license: Apache-2.0
compatibility: Requires python3
metadata:
  author: example-org
  version: "1.0"
allowed-tools: Bash(git:*) Read`, "# PDF\n\nDo the thing.\n")
	s, err := LoadFile(filepath.Join(root, "pdf-processing", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "pdf-processing" || s.Description != "Extract PDF text. Use when: the user mentions PDFs." {
		t.Errorf("name/description = %q / %q", s.Name, s.Description)
	}
	if s.License != "Apache-2.0" || s.Compatibility != "Requires python3" || s.AllowedTools != "Bash(git:*) Read" {
		t.Errorf("optional fields = %q %q %q", s.License, s.Compatibility, s.AllowedTools)
	}
	if s.Metadata["author"] != "example-org" || s.Metadata["version"] != "1.0" {
		t.Errorf("metadata = %v", s.Metadata)
	}
	if len(s.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", s.Warnings)
	}
	body, err := s.Body()
	if err != nil || body != "# PDF\n\nDo the thing." {
		t.Errorf("body = %q, %v", body, err)
	}
}

func TestLoadFileLenientOnUnquotedColon(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "colon", "name: colon\ndescription: Use this skill when: the user asks about PDFs", "body")
	s, err := LoadFile(filepath.Join(root, "colon", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Description != "Use this skill when: the user asks about PDFs" {
		t.Errorf("description = %q", s.Description)
	}
}

func TestLoadFileBlockScalarDescription(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "folded", "name: folded\ndescription: >\n  First line\n  second line\n", "body")
	s, err := LoadFile(filepath.Join(root, "folded", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Description != "First line second line" {
		t.Errorf("description = %q", s.Description)
	}
}

func TestLoadFileWarnsButLoads(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "my-dir", "name: Other_Name\ndescription: d", "body")
	s, err := LoadFile(filepath.Join(root, "my-dir", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "Other_Name" {
		t.Errorf("name = %q; the frontmatter name is kept", s.Name)
	}
	joined := strings.Join(s.Warnings, "\n")
	if !strings.Contains(joined, "does not match the directory") || !strings.Contains(joined, "only lowercase") {
		t.Errorf("warnings = %v", s.Warnings)
	}

	writeSkill(t, root, "unnamed", "description: d", "body")
	s, err = LoadFile(filepath.Join(root, "unnamed", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "unnamed" || len(s.Warnings) != 1 {
		t.Errorf("name = %q warnings = %v; a missing name falls back to the directory", s.Name, s.Warnings)
	}
}

func TestLoadFileRejectsUnusable(t *testing.T) {
	root := t.TempDir()
	cases := map[string]string{
		"no-description": "name: no-description",
		"no-frontmatter": "",
	}
	writeSkill(t, root, "no-description", cases["no-description"], "body")
	if err := os.MkdirAll(filepath.Join(root, "no-frontmatter"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "no-frontmatter", "SKILL.md"), []byte("# Just a readme\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name := range cases {
		if _, err := LoadFile(filepath.Join(root, name, "SKILL.md")); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestDiscoverPrecedenceAndDiagnostics(t *testing.T) {
	project := t.TempDir()
	user := t.TempDir()
	writeSkill(t, project, "shared", "name: shared\ndescription: project copy", "p")
	writeSkill(t, project, "broken", "name: broken", "no description")
	writeSkill(t, user, "shared", "name: shared\ndescription: user copy", "u")
	writeSkill(t, user, "only-user", "name: only-user\ndescription: u2", "u")
	// A stray file and a directory without SKILL.md are ignored.
	if err := os.WriteFile(filepath.Join(user, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(user, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}

	c := Discover([]Root{
		{Path: project, Scope: ScopeProject},
		{Path: filepath.Join(project, "missing"), Scope: ScopeProject},
		{Path: user, Scope: ScopeUser},
	})
	if got := strings.Join(c.Names(), ","); got != "shared,only-user" {
		t.Errorf("names = %s", got)
	}
	s, _ := c.Find("shared")
	if s.Description != "project copy" || s.Scope != ScopeProject {
		t.Errorf("project skill did not win: %+v", s)
	}
	diags := strings.Join(c.Diagnostics, "\n")
	if !strings.Contains(diags, "broken") || !strings.Contains(diags, "shadowed") {
		t.Errorf("diagnostics = %v", c.Diagnostics)
	}
}

func TestProjectRootsWalkToRepoRoot(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(repo, "packages", "one")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	roots := ProjectRoots(pkg)
	want := []string{
		filepath.Join(pkg, ".shhh", "skills"),
		filepath.Join(pkg, ".agents", "skills"),
		filepath.Join(pkg, ".claude", "skills"),
		filepath.Join(repo, "packages", "one", ".shhh", "skills"),
	}
	if len(roots) != 9 {
		t.Fatalf("got %d roots, want 9 (three directories, three names each): %v", len(roots), roots)
	}
	for i := 0; i < 3; i++ {
		if roots[i].Path != want[i] || roots[i].Scope != ScopeProject {
			t.Errorf("roots[%d] = %+v, want %s", i, roots[i], want[i])
		}
	}
	if roots[8].Path != filepath.Join(repo, ".claude", "skills") {
		t.Errorf("last root = %s; the repository root should close the walk", roots[8].Path)
	}

	// Outside a repository, only cwd is scanned.
	loose := t.TempDir()
	if got := ProjectRoots(loose); len(got) != 3 {
		t.Errorf("outside a repo got %d roots, want 3: %v", len(got), got)
	}
}

func TestPromptBlockAndContent(t *testing.T) {
	if PromptBlock(nil) != "" || PromptBlock(&Catalog{}) != "" {
		t.Error("an empty catalog must produce no block")
	}
	root := t.TempDir()
	dir := writeSkill(t, root, "docs", "name: docs\ndescription: Writes <docs> & things", "# Docs\n\nSee references/guide.md")
	if err := os.MkdirAll(filepath.Join(dir, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "references", "guide.md"), []byte("g"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := Discover([]Root{{Path: root, Scope: ScopeProject}})

	block := PromptBlock(c)
	for _, want := range []string{"<available_skills>", "<name>docs</name>", "Writes &lt;docs&gt; &amp; things", "<location>" + filepath.Join(dir, "SKILL.md") + "</location>", ToolName} {
		if !strings.Contains(block, want) {
			t.Errorf("prompt block lacks %q:\n%s", want, block)
		}
	}

	out, err := c.Execute(json.RawMessage(`{"name":"docs"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !IsContent(out) {
		t.Error("tool result is not recognised as skill content")
	}
	for _, want := range []string{"# Docs", "Skill directory: " + dir, "<file>references/guide.md</file>", "</skill_content>"} {
		if !strings.Contains(out, want) {
			t.Errorf("content lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "description:") {
		t.Error("frontmatter leaked into the content")
	}

	if _, err := c.Execute(json.RawMessage(`{"name":"nope"}`)); err == nil || !strings.Contains(err.Error(), "docs") {
		t.Errorf("unknown skill error should list the catalog: %v", err)
	}

	def := ToolDefinition(c)
	if !strings.Contains(string(def.Parameters), `"enum": ["docs"]`) {
		t.Errorf("tool schema does not constrain the name: %s", def.Parameters)
	}

	msg, err := UserMessage(c.Skills[0], "write the changelog")
	if err != nil || !strings.HasSuffix(msg, "write the changelog") || !IsContent(msg) {
		t.Errorf("user message = %q, %v", msg, err)
	}
}

func TestResourcesCapped(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < maxResources+5; i++ {
		if err := os.WriteFile(filepath.Join(dir, "f"+strings.Repeat("x", i%7)+string(rune('a'+i%26))+".txt"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "node_modules", "dep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "node_modules", "dep", "index.js"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, partial := Resources(dir)
	if !partial || len(files) != maxResources {
		t.Errorf("got %d files partial=%v", len(files), partial)
	}
	for _, f := range files {
		if strings.HasPrefix(f, "node_modules") {
			t.Errorf("node_modules was listed: %s", f)
		}
	}
}
