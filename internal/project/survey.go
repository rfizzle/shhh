package project

// Project survey (docs/interface/surfaces.md#the-start-screen). A
// first launch in a repo shhh has never seen used to be a blank viewport and
// a blinking cursor. It knows more than that before the first keystroke: it
// has already read the project context file into the system prompt, and the
// toolchain, the branch and the dirty count are three cheap questions away.
//
// Survey answers them once, at session start, beside the FindContext read
// that already happens there. Nothing here runs per frame, and the one walk
// it does is bounded twice over — by depth and by entries visited — so a
// checkout with a million files costs the same as one with a thousand and
// says so (Partial) rather than stalling the launch.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	// maxWalkEntries bounds the package walk. A survey that hit the bound
	// reports Partial, and the count it carries is a floor rather than a
	// total — the screen renders it as `41+ packages`.
	maxWalkEntries = 20000
	// maxWalkDepth bounds how deep the walk descends from the workspace root.
	maxWalkDepth = 12
)

// skipDirs are directories that never hold the project's own packages. They
// are skipped by name at any depth, along with every dotted directory.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "target": true,
	"dist": true, "build": true, "testdata": true,
}

// Info is what shhh already knows about the working directory when a session
// opens. Every field is either true or absent: an unrecognised language
// leaves Language empty rather than guessing, a workspace that is not a git
// repository leaves Repo false rather than reporting a clean tree, and a
// bounded walk marks itself Partial rather than passing a floor off as a
// total.
type Info struct {
	// Dir is the working directory and Display the same path with the home
	// directory abbreviated to ~, which is what the screen prints.
	Dir     string
	Display string
	// Language is the detected ecosystem ("go", "rust", "node", "python")
	// and Toolchain the version its marker file named, if it named one.
	Language  string
	Toolchain string
	// Repo reports a git checkout; Branch is the current branch, empty on a
	// detached HEAD (which Detached marks) or outside a repository. Dirty
	// counts changed and untracked paths.
	Repo     bool
	Branch   string
	Detached bool
	Dirty    int
	// Packages counts the language's own unit of packaging — Go package
	// directories, Cargo crates, package.json manifests, Python packages —
	// and Unit names it. Partial marks a count the walk's bound cut short.
	Packages int
	Unit     string
	Partial  bool
	// ContextFiles are the project files read into the system prompt, as
	// paths relative to Dir where they sit inside it. Empty means the model
	// was told nothing about this project, which is worth saying out loud.
	ContextFiles []string
}

// Survey gathers everything the start screen states about dir. It never
// fails: each question that cannot be answered leaves its fields zero, and
// the screen prints what is left rather than an error.
func Survey(dir string) Info {
	if dir == "" {
		if wd, err := os.Getwd(); err == nil {
			dir = wd
		}
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	info := Info{Dir: dir, Display: abbreviate(dir)}
	info.Language, info.Toolchain, info.Unit = detectLanguage(dir)
	info.Packages, info.Partial = countPackages(dir, info.Language)
	info.Repo, info.Branch, info.Detached, info.Dirty = surveyGit(dir)
	if path, _ := Find(); path != "" {
		info.ContextFiles = append(info.ContextFiles, relativeTo(dir, path))
	}
	return info
}

// abbreviate replaces the home directory prefix with ~, which is how every
// path in the product is printed once it leaves the filesystem.
func abbreviate(dir string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || dir == "" {
		return dir
	}
	if dir == home {
		return "~"
	}
	if strings.HasPrefix(dir, home+string(filepath.Separator)) {
		return "~" + dir[len(home):]
	}
	return dir
}

// relativeTo renders path as the screen should print it: relative to dir when
// it sits inside it, and home-abbreviated when it does not — a context file
// two directories up is worth seeing as the outside file it is.
func relativeTo(dir, path string) string {
	if rel, err := filepath.Rel(dir, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return abbreviate(path)
}

// goDirective and rustChannel pull the toolchain out of the marker files that
// state one. Neither is required: a marker without a version leaves Toolchain
// empty and the screen drops the clause.
var (
	goDirective = regexp.MustCompile(`(?m)^go\s+([0-9][^\s/]*)`)
	rustChannel = regexp.MustCompile(`(?m)^\s*channel\s*=\s*"([^"]+)"`)
)

// detectLanguage identifies the ecosystem from its marker file and reads the
// toolchain the marker names. The order is the order of certainty: a go.mod
// is unambiguous, a requirements.txt is the weakest signal and comes last.
func detectLanguage(dir string) (lang, toolchain, unit string) {
	switch {
	case exists(dir, "go.mod"):
		return "go", firstSubmatch(goDirective, read(dir, "go.mod")), "package"
	case exists(dir, "Cargo.toml"):
		tc := firstSubmatch(rustChannel, read(dir, "rust-toolchain.toml"))
		return "rust", tc, "crate"
	case exists(dir, "package.json"):
		return "node", nodeVersion(dir), "package"
	case exists(dir, "pyproject.toml"), exists(dir, "setup.py"), exists(dir, "requirements.txt"):
		return "python", strings.TrimSpace(read(dir, ".python-version")), "package"
	}
	return "", "", ""
}

// nodeVersion prefers .nvmrc, which is the file that actually pins a version,
// and falls back to package.json's engines.node range.
func nodeVersion(dir string) string {
	if v := strings.TrimSpace(read(dir, ".nvmrc")); v != "" {
		return strings.TrimPrefix(v, "v")
	}
	var pkg struct {
		Engines struct {
			Node string `json:"node"`
		} `json:"engines"`
	}
	if err := json.Unmarshal([]byte(read(dir, "package.json")), &pkg); err != nil {
		return ""
	}
	return pkg.Engines.Node
}

// countPackages counts the language's packaging unit under dir. An
// unrecognised language has no unit to count, so it reports nothing rather
// than counting directories and calling them packages.
func countPackages(dir, lang string) (count int, partial bool) {
	if lang == "" {
		return 0, false
	}
	visited := 0
	// dirs marks the directories already counted, for the languages whose
	// unit is a directory containing a kind of file rather than a manifest.
	dirs := map[string]bool{}
	root := filepath.Clean(dir)

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable subtree is skipped, not fatal
		}
		visited++
		if visited > maxWalkEntries {
			partial = true
			return filepath.SkipAll
		}
		if d.IsDir() {
			if path == root {
				return nil
			}
			name := d.Name()
			if skipDirs[name] || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			if depth(root, path) > maxWalkDepth {
				partial = true
				return filepath.SkipDir
			}
			return nil
		}
		switch lang {
		case "go":
			if strings.HasSuffix(d.Name(), ".go") && !dirs[filepath.Dir(path)] {
				dirs[filepath.Dir(path)] = true
				count++
			}
		case "rust":
			if d.Name() == "Cargo.toml" {
				count++
			}
		case "node":
			if d.Name() == "package.json" {
				count++
			}
		case "python":
			if d.Name() == "__init__.py" {
				count++
			}
		}
		return nil
	})
	if err != nil {
		return count, partial
	}
	return count, partial
}

// depth is how many directories separate path from root.
func depth(root, path string) int {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return 0
	}
	return len(strings.Split(filepath.ToSlash(rel), "/"))
}

// surveyGit asks git for the branch and the dirty count. Every failure — no
// repository, no git binary, no commit yet — leaves Repo false, which the
// screen states as "not a git repository" rather than as a clean tree.
func surveyGit(dir string) (repo bool, branch string, detached bool, dirty int) {
	head, err := gitOutput(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return false, "", false, 0
	}
	head = strings.TrimSpace(head)
	if head == "HEAD" {
		detached = true
		head = ""
	}
	status, err := gitOutput(dir, "status", "--porcelain")
	if err != nil {
		return true, head, detached, 0
	}
	if s := strings.TrimRight(status, "\n"); s != "" {
		dirty = strings.Count(s, "\n") + 1
	}
	return true, head, detached, dirty
}

func gitOutput(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func exists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

func read(dir, name string) string {
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return ""
	}
	return string(data)
}

func firstSubmatch(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); len(m) > 1 {
		return m[1]
	}
	return ""
}
