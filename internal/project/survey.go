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
	"time"
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
	// Head is the commit HEAD points at — empty outside a repository and in
	// one with no commit yet. It is here because a conversation that comes
	// back has to be able to tell the commit it is looking at now from the
	// one it was written down on
	// (docs/capabilities/sessions-and-memory.md#a-resumed-session-sees-the-tree-as-it-is).
	Head string
	// Packages counts the language's own unit of packaging — Go package
	// directories, Cargo crates, package.json manifests, Python packages —
	// and Unit names it. Partial marks a count the walk's bound cut short.
	Packages int
	Unit     string
	Partial  bool
	// ContextFiles are the project files read into the system prompt, in the
	// order the prompt states them — outermost first — as paths relative to
	// Root where they sit inside it. Empty means the model was told nothing
	// about this project, which is worth saying out loud.
	ContextFiles []string
	// Root is the directory this project's own state is keyed on and
	// RootDisplay the same path home-abbreviated. It is Dir in the ordinary
	// case and something above it in a subdirectory, and it is stated
	// because a session that does not say which directory its backlog and
	// its refused offers belong to gives the reader no way to tell one
	// project from two.
	Root        string
	RootDisplay string
	// ConfigFile is the checkout's own settings file where one was read over
	// the user's, stated from Root the way ContextFiles are. Empty is a
	// session running on the user's settings alone — the checkout has none,
	// or has not been trusted to be read.
	//
	// Like Sibling it is filled in by whoever loaded it rather than found
	// here: whether that file was read is a question about trust and about
	// the person's own file, and neither is visible from the tree.
	ConfigFile string
	// Reread is when the git half of this survey was taken again, and zero
	// where it is the reading the session opened on. A conversation that is
	// rebuilt out of a stored message hours later takes the git half afresh,
	// and the workspace block says which of the two counts it is stating:
	// work that predates the session is not the same claim as a count that
	// has the session's own edits in it.
	Reread time.Time
	// Sibling is when another session already open in this checkout started,
	// and zero when there is none. It is the one thing here the survey
	// cannot find out for itself — nothing in the filesystem says who else
	// is working in a directory — so it is filled in by whoever can ask,
	// and every reader of the survey states the same answer.
	Sibling time.Time
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
	info := Info{Dir: dir, Display: Abbreviate(dir)}
	info.Language, info.Toolchain, info.Unit = detectLanguage(dir)
	info.Packages, info.Partial = countPackages(dir, info.Language)
	info.Repo, info.Branch, info.Detached, info.Dirty = surveyGit(dir)
	if info.Repo {
		info.Head = Head(dir)
	}
	// Read from dir, not from the process: a survey of somewhere else that
	// reported the context files of here would be describing two directories
	// at once. The user's own instructions file is not this project's, so it
	// is not counted among what the project said about itself.
	for _, ins := range Instructions(dir, "") {
		info.ContextFiles = append(info.ContextFiles, ins.Display)
	}
	info.Root = Root(dir)
	info.RootDisplay = Abbreviate(info.Root)
	return info
}

// RereadGit asks git again the questions that go stale while a conversation
// is open — the branch, whether HEAD is detached, how much is uncommitted,
// and which commit it is — and keeps everything else info already holds.
//
// The split is what makes this cheap enough to do at a compaction or a load.
// The package walk is the expensive half of a survey and it is also the half
// that does not go stale: a checkout does not change ecosystem while somebody
// is working in it, and its branch changes several times an hour.
//
// A survey nobody took is answered unchanged. There is no directory to ask
// about, and a git reading of the process's own working directory would be a
// finding about somewhere else.
func RereadGit(info Info) Info {
	if info.Dir == "" {
		return info
	}
	info.Repo, info.Branch, info.Detached, info.Dirty = surveyGit(info.Dir)
	info.Head = ""
	if info.Repo {
		info.Head = Head(info.Dir)
	}
	info.Reread = time.Now()
	return info
}

// Abbreviate replaces the home directory prefix with ~, which is how every
// path in the product is printed once it leaves the filesystem.
func Abbreviate(dir string) string {
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
	return Abbreviate(path)
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

// Head is the commit HEAD points at in dir, or empty where there is no
// answer: outside a repository, without a git binary, or before the first
// commit. It is its own call rather than part of the survey because the
// answer is wanted at moments a whole survey is too much to pay for — a
// conversation records the commit it was written down on with every save,
// and only reads the rest of the checkout when it is opened again.
func Head(dir string) string {
	out, err := gitOutput(dir, "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
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
