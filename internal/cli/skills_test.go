package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/rfizzle/agentcore/skill"
	"github.com/rfizzle/shhh/internal/config"
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
	return skill.Load([]skill.Root{{Path: root, Scope: skill.ScopeUser}}, builtinSkillFS)
}

// The built-ins are listed after the file skills with the scope word the
// listing uses, and shown as living in the binary.
func TestSkillsListing_TheBuiltinsFollowTheFileSkills(t *testing.T) {
	c := goldenSkills(t)
	got := skillsReport(c).Render(200)
	user := strings.Index(got, "changelog")
	gate := strings.Index(got, "shhh-quality-gate")
	if user < 0 || gate < user || !strings.Contains(got, "[built-in]") || !strings.Contains(got, "5 skills") {
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
	root.InitDefaultVersionFlag() // cobra adds --version at execution
	named := regexp.MustCompile("`shhh ([^`]+)`")
	for _, s := range skill.Builtins(builtinSkillFS) {
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
			// What is left after a command is its arguments when it takes
			// them (`config get <key>` with a real key in the slot).
			if err == nil && len(rest) > 0 && cmd != root && cmd.Args != nil && cmd.Args(cmd, rest) == nil {
				rest = nil
			}
			if len(words) == 0 && len(flags) > 0 {
				cmd, rest, err = root, nil, nil
			}
			if err != nil || len(rest) > 0 || (cmd == root && len(flags) == 0) {
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
		if min := map[string]int{"shhh-checkout-setup": 8, "shhh-global-config": 8, "shhh-about": 8}[s.Name]; found < min {
			t.Errorf("%s names %d commands; the check found too few to be reading it", s.Name, found)
		}
	}
}

// Every settings key a built-in skill names is one the registry has, so a
// renamed or removed setting cannot stay in a body that tells the model to
// read it. A key is a dotted name inside backticks whose first part is a
// section the registry has.
// See docs/capabilities/skills.md#shhh-carries-skills-of-its-own.
func TestBuiltinSkillsNameOnlySettingsThatExist(t *testing.T) {
	sections := map[string]bool{}
	for _, st := range config.Settings() {
		sections[strings.SplitN(st.Key, ".", 2)[0]] = true
	}
	span := regexp.MustCompile("`([^`]+)`")
	key := regexp.MustCompile(`^[a-z_]+\.[a-z_]+$`)
	for _, s := range skill.Builtins(builtinSkillFS) {
		body, err := s.Body()
		if err != nil {
			t.Fatal(err)
		}
		found := 0
		for _, m := range span.FindAllStringSubmatch(body, -1) {
			for _, f := range strings.Fields(m[1]) {
				if !key.MatchString(f) || strings.HasSuffix(f, ".json") || strings.HasSuffix(f, ".toml") || strings.HasSuffix(f, ".md") || !sections[strings.SplitN(f, ".", 2)[0]] {
					continue
				}
				found++
				if _, ok := config.Lookup(f); !ok {
					t.Errorf("%s names the setting %q, which the registry lacks", s.Name, f)
				}
			}
		}
		if s.Name == "shhh-global-config" && found < 5 {
			t.Errorf("%s names %d settings; the check found too few to be reading it", s.Name, found)
		}
	}
}

// The global files the configuration skill names are the ones the config
// package reads, by the name it gives them.
func TestGlobalConfigSkillNamesTheFilesConfigReads(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var body string
	for _, s := range skill.Builtins(builtinSkillFS) {
		if s.Name == "shhh-global-config" {
			body, _ = s.Body()
		}
	}
	for what, paths := range map[string][]string{
		"settings": config.Paths(), "theme": config.ThemePaths(), "keymap": config.KeymapPaths(),
	} {
		if len(paths) == 0 {
			t.Fatalf("no %s path", what)
		}
		if name := filepath.Base(paths[0]); !strings.Contains(body, "`"+name+"`") {
			t.Errorf("the body does not name %s, the %s file", name, what)
		}
	}
	for _, wrong := range []string{"keymap.toml", "`user`"} {
		if strings.Contains(body, wrong) {
			t.Errorf("the body names %s", wrong)
		}
	}
}

// With no file skill loaded the listing says where one is read from, since
// the built-ins keep it from ever being empty.
func TestSkillsListing_NoFileSkillSaysWhereOneIsRead(t *testing.T) {
	got := skillsReport(skill.Load(nil, builtinSkillFS)).Render(200)
	if !strings.Contains(got, "no file skill loaded; in the project, once you trust the checkout: .shhh/skills, .agents/skills, .claude/skills; for you:") ||
		!strings.Contains(got, "4 skills") {
		t.Errorf("listing = %q", got)
	}
	if got := skillsReport(goldenSkills(t)).Render(200); strings.Contains(got, "no file skill") {
		t.Errorf("a file skill is loaded: %q", got)
	}
}
