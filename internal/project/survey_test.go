package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// writeFiles lays out a fixture tree: keys are slash-separated paths relative
// to dir, values their contents.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestSurvey_GoProjectReportsToolchainAndPackages(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"go.mod":                "module example.com/x\n\ngo 1.24\n",
		"main.go":               "package main\n",
		"internal/agent/a.go":   "package agent\n",
		"internal/agent/b.go":   "package agent\n",
		"internal/ui/ui.go":     "package ui\n",
		"vendor/dep/dep.go":     "package dep\n",
		"node_modules/x/x.go":   "package x\n",
		".hidden/hidden.go":     "package hidden\n",
		"internal/ui/README.md": "not go\n",
	})

	info := Survey(dir)
	if info.Language != "go" {
		t.Fatalf("language = %q, want go", info.Language)
	}
	if info.Toolchain != "1.24" {
		t.Fatalf("toolchain = %q, want 1.24", info.Toolchain)
	}
	if info.Unit != "package" {
		t.Fatalf("unit = %q, want package", info.Unit)
	}
	// Three package directories: the root, internal/agent (two files, one
	// package), internal/ui. vendor, node_modules and dotted directories are
	// not this project's packages.
	if info.Packages != 3 {
		t.Fatalf("packages = %d, want 3", info.Packages)
	}
	if info.Partial {
		t.Fatal("a fixture this small cannot have hit the walk bound")
	}
}

func TestSurvey_UnrecognisedProjectCountsNothing(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"notes.txt": "hello\n", "src/thing.txt": "x\n"})

	info := Survey(dir)
	if info.Language != "" {
		t.Fatalf("language = %q, want empty", info.Language)
	}
	// A count with no unit behind it would be a directory count dressed up as
	// a package count.
	if info.Packages != 0 {
		t.Fatalf("packages = %d, want 0", info.Packages)
	}
}

func TestSurvey_LanguageMarkers(t *testing.T) {
	for _, tc := range []struct {
		name           string
		files          map[string]string
		lang, tc_, uni string
		packages       int
	}{
		{
			name:     "rust",
			files:    map[string]string{"Cargo.toml": "[package]\n", "crates/a/Cargo.toml": "[package]\n", "rust-toolchain.toml": "[toolchain]\nchannel = \"1.79.0\"\n"},
			lang:     "rust",
			tc_:      "1.79.0",
			uni:      "crate",
			packages: 2,
		},
		{
			name:     "node",
			files:    map[string]string{"package.json": `{"engines":{"node":">=20"}}`, "pkgs/a/package.json": "{}"},
			lang:     "node",
			tc_:      ">=20",
			uni:      "package",
			packages: 2,
		},
		{
			name:     "node prefers nvmrc",
			files:    map[string]string{"package.json": `{"engines":{"node":">=20"}}`, ".nvmrc": "v22.3.0\n"},
			lang:     "node",
			tc_:      "22.3.0",
			uni:      "package",
			packages: 1,
		},
		{
			name:     "python",
			files:    map[string]string{"pyproject.toml": "[project]\n", ".python-version": "3.12\n", "pkg/__init__.py": "", "pkg/sub/__init__.py": ""},
			lang:     "python",
			tc_:      "3.12",
			uni:      "package",
			packages: 2,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFiles(t, dir, tc.files)
			info := Survey(dir)
			if info.Language != tc.lang || info.Toolchain != tc.tc_ || info.Unit != tc.uni {
				t.Fatalf("got %q/%q/%q, want %q/%q/%q",
					info.Language, info.Toolchain, info.Unit, tc.lang, tc.tc_, tc.uni)
			}
			if info.Packages != tc.packages {
				t.Fatalf("packages = %d, want %d", info.Packages, tc.packages)
			}
		})
	}
}

func TestSurvey_NonRepoReportsNoBranchRatherThanACleanTree(t *testing.T) {
	dir := t.TempDir()
	info := Survey(dir)
	if info.Repo {
		t.Fatal("a temp directory is not a git repository")
	}
	if info.Branch != "" || info.Dirty != 0 {
		t.Fatalf("branch = %q dirty = %d, want both empty for a non-repo", info.Branch, info.Dirty)
	}
}

func TestSurvey_RepoReportsBranchAndDirtyCount(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not on PATH")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(git, append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "--initial-branch=trunk")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "t")
	writeFiles(t, dir, map[string]string{"a.txt": "one\n"})
	run("add", "a.txt")
	run("commit", "-m", "first")
	// One tracked file modified, one untracked: two dirty paths.
	writeFiles(t, dir, map[string]string{"a.txt": "two\n", "b.txt": "new\n"})

	info := Survey(dir)
	if !info.Repo {
		t.Fatal("Repo = false for an initialised repository")
	}
	if info.Branch != "trunk" {
		t.Fatalf("branch = %q, want trunk", info.Branch)
	}
	if info.Detached {
		t.Fatal("a branch checkout is not detached")
	}
	if info.Dirty != 2 {
		t.Fatalf("dirty = %d, want 2", info.Dirty)
	}
}

func TestFind_ReportsThePathItRead(t *testing.T) {
	dir := t.TempDir()
	// The temp directory may itself be a symlink (macOS /var); resolve it so
	// the comparison is against the path Getwd will report.
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	writeFiles(t, dir, map[string]string{"AGENTS.md": "be helpful\n"})
	chdir(t, dir)

	path, content := Find()
	if content != "be helpful\n" {
		t.Fatalf("content = %q", content)
	}
	if filepath.Base(path) != "AGENTS.md" {
		t.Fatalf("path = %q, want an AGENTS.md", path)
	}
	if FindContext() != content {
		t.Fatal("FindContext and Find disagree about the same file")
	}

	info := Survey(dir)
	if len(info.ContextFiles) != 1 || info.ContextFiles[0] != "AGENTS.md" {
		t.Fatalf("context files = %v, want [AGENTS.md]", info.ContextFiles)
	}
}

func TestSurvey_DotShhhWinsOverAgentsMd(t *testing.T) {
	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	writeFiles(t, dir, map[string]string{".shhh": "shhh rules\n", "AGENTS.md": "generic\n"})
	chdir(t, dir)

	if _, content := Find(); content != "shhh rules\n" {
		t.Fatalf("content = %q, want the .shhh file", content)
	}
}

// chdir moves into dir for one test and restores the working directory after.
func chdir(t *testing.T, dir string) {
	t.Helper()
	was, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(was) })
}
