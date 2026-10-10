package tools

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// GitProgramPathSentence is what the model is told when one of its writes
// names a path git runs or reads as configuration. Like the other standing
// refusals it names no setting and points at no file: there is none that
// lifts it, and a sentence that hinted at one would be the way around it.
// It is the one sentence every door says, and it carries no "error:" prefix,
// because the tool dispatcher puts one on a failure and
// GitProgramPathResult is the same words for a door that answers without one.
// See docs/capabilities/containment.md#the-hosts-own-git-runs-nothing-a-command-wrote.
const GitProgramPathSentence = "this path is one git runs or reads as configuration — a hook, the repository's " +
	"config, its info/ directory, a worktree's .git file, or a path core.hooksPath or an include names — and the " +
	"next git run on the host would start what is written there. It is refused in every permission mode and no " +
	"approval can allow it, and retrying it under another spelling — a relative path, a link to it — will be " +
	"refused too. Do the work without writing it, or say what you were trying to do and let the user decide."

// GitProgramPathResult is the tool result the model reads, where a door
// answers without the dispatcher's own "error: ".
const GitProgramPathResult = "error: " + GitProgramPathSentence

// ErrGitProgramPath is the error a write tool returns for such a path; its
// text is GitProgramPathSentence.
var ErrGitProgramPath error = gitProgramPathError{}

type gitProgramPathError struct{}

func (gitProgramPathError) Error() string { return GitProgramPathSentence }

// GitProgramEntries are the entries of a repository's store that git runs or
// reads as configuration, which a contained command cannot write either
// (the sandbox holds each read-only).
var GitProgramEntries = []string{"config", "config.worktree", "hooks", "info"}

// GitProgramPath reports whether a write to path would land on a path git
// runs or reads as configuration. A relative path is read against root, the
// session's directory, or the directory shhh runs in where root is empty;
// the repository whose config names further paths is root's. The path is
// cleaned and its links followed — a link that lands on one of these, even
// one whose target is not there yet, is the path it lands on — and either
// reading is enough to refuse it.
func GitProgramPath(root, path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	if root == "" {
		root, _ = os.Getwd()
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	lexical := filepath.Clean(path)
	named := namedPaths(root)
	for _, p := range []string{lexical, followLinks(lexical)} {
		if inGitStore(p) || named.reach(p) {
			return true
		}
	}
	return false
}

// inGitStore is the lexical reading: a .git component followed by nothing
// (the file a worktree or submodule checkout keeps), by an entry git runs
// (hooks, info), or ending in a config file.
func inGitStore(path string) bool {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for i, c := range parts {
		if !strings.EqualFold(c, ".git") {
			continue
		}
		rest := parts[i+1:]
		if len(rest) == 0 {
			return true
		}
		for _, r := range rest {
			if strings.EqualFold(r, "hooks") || strings.EqualFold(r, "info") {
				return true
			}
		}
		last := strings.ToLower(rest[len(rest)-1])
		if last == "config" || last == "config.worktree" {
			return true
		}
	}
	return false
}

// followLinks is path with every symbolic link in it followed, one component
// at a time, so a link whose target does not exist yet still lands where it
// points: a write through it creates the target. A component that does not
// exist is taken as spelled.
func followLinks(path string) string {
	vol := filepath.VolumeName(path)
	sep := string(filepath.Separator)
	resolved := vol + sep
	pending := splitPath(strings.TrimPrefix(path, vol))
	for hops := 0; len(pending) > 0; {
		c := pending[0]
		pending = pending[1:]
		switch c {
		case "", ".":
			continue
		case "..":
			resolved = filepath.Dir(resolved)
			continue
		}
		next := filepath.Join(resolved, c)
		if fi, err := os.Lstat(next); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			hops++
			if hops > 40 {
				return path
			}
			target, err := os.Readlink(next)
			if err != nil {
				resolved = next
				continue
			}
			if filepath.IsAbs(target) {
				resolved = filepath.VolumeName(target) + sep
				target = strings.TrimPrefix(target, filepath.VolumeName(target))
			}
			pending = append(splitPath(target), pending...)
			continue
		}
		resolved = next
	}
	return resolved
}

func splitPath(p string) []string {
	return strings.FieldsFunc(filepath.ToSlash(p), func(r rune) bool { return r == '/' })
}

// named is what the workspace repository's configuration points git at: the
// hooks directories core.hooksPath names and the files an include or
// includeIf names, each as spelled and as its links resolve.
type named struct {
	hooks []string
	files []string
}

func (n named) reach(path string) bool {
	for _, h := range n.hooks {
		if underDir(path, h) {
			return true
		}
	}
	for _, f := range n.files {
		if path == f {
			return true
		}
	}
	return false
}

func underDir(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// namedPaths reads the configuration of the repository root is in — the
// shared config, and the worktree's own where it is a linked one — and what
// the files it includes add. A repository whose .git is a file is read where
// the file points, and its commondir is followed to the shared config.
func namedPaths(root string) named {
	top, gitDir, common := findRepo(root)
	var n named
	if top == "" {
		return n
	}
	home, _ := os.UserHomeDir()
	seen := map[string]bool{}
	var read func(file string, depth int)
	add := func(v, base string) []string {
		v = strings.TrimSpace(v)
		switch {
		case v == "":
			return nil
		case v == "~" || strings.HasPrefix(v, "~/"):
			v = filepath.Join(home, strings.TrimPrefix(v, "~"))
		case !filepath.IsAbs(v):
			v = filepath.Join(base, v)
		}
		v = filepath.Clean(v)
		if r := followLinks(v); r != v {
			return []string{v, r}
		}
		return []string{v}
	}
	read = func(file string, depth int) {
		if depth > 4 || seen[file] {
			return
		}
		seen[file] = true
		f, err := os.Open(file)
		if err != nil {
			return
		}
		defer func() { _ = f.Close() }()
		parseConfig(f, func(section, key, val string) {
			switch {
			case section == "core" && key == "hookspath":
				n.hooks = append(n.hooks, add(val, top)...)
			case (section == "include" || section == "includeif") && key == "path":
				for _, p := range add(val, filepath.Dir(file)) {
					n.files = append(n.files, p)
					read(p, depth+1)
				}
			}
		})
	}
	for _, f := range []string{
		filepath.Join(common, "config"), filepath.Join(common, "config.worktree"),
		filepath.Join(gitDir, "config"), filepath.Join(gitDir, "config.worktree"),
	} {
		read(f, 0)
	}
	return n
}

// findRepo walks up from dir to the .git a git run there finds. top is the
// directory that holds it, gitDir the directory this checkout keeps its own
// state in, and common the one it shares its config and hooks from — the
// same directory unless the checkout is a linked worktree. top is empty
// where there is no repository.
func findRepo(dir string) (top, gitDir, common string) {
	for d := dir; ; d = filepath.Dir(d) {
		dotgit := filepath.Join(d, ".git")
		fi, err := os.Lstat(dotgit)
		if err == nil {
			if fi.IsDir() {
				return d, dotgit, dotgit
			}
			b, err := os.ReadFile(dotgit)
			if err != nil {
				return "", "", ""
			}
			p, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
			if !ok {
				return "", "", ""
			}
			p = strings.TrimSpace(p)
			if !filepath.IsAbs(p) {
				p = filepath.Join(d, p)
			}
			gitDir = filepath.Clean(p)
			common = gitDir
			if c, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
				cd := strings.TrimSpace(string(c))
				if !filepath.IsAbs(cd) {
					cd = filepath.Join(gitDir, cd)
				}
				common = filepath.Clean(cd)
			}
			return d, gitDir, common
		}
		if filepath.Dir(d) == d {
			return "", "", ""
		}
	}
}

// parseConfig calls fn for each key in a git config file with its section
// (lowercased, without the subsection) and key lowercased and its value
// unquoted. It reads a header and a key on one line, comments after # and ;
// outside quotes, and subsection headers such as [includeIf "gitdir:x"].
func parseConfig(f *os.File, fn func(section, key, val string)) {
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	section := ""
	for sc.Scan() {
		line := strings.TrimSpace(stripComment(sc.Text()))
		for strings.HasPrefix(line, "[") {
			end := headerEnd(line)
			if end < 0 {
				line = ""
				break
			}
			head := line[1:end]
			if i := strings.IndexAny(head, " \t\"."); i >= 0 {
				head = head[:i]
			}
			section = strings.ToLower(head)
			line = strings.TrimSpace(line[end+1:])
		}
		if line == "" {
			continue
		}
		key, val, _ := strings.Cut(line, "=")
		val = strings.TrimSpace(val)
		if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
			val = val[1 : len(val)-1]
		}
		fn(section, strings.ToLower(strings.TrimSpace(key)), val)
	}
}

// stripComment cuts a line at the first # or ; outside double quotes.
func stripComment(line string) string {
	quoted := false
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case c == '\\':
			i++
		case c == '"':
			quoted = !quoted
		case !quoted && (c == '#' || c == ';'):
			return line[:i]
		}
	}
	return line
}

// headerEnd is the index of the ] that closes a section header, or -1.
func headerEnd(line string) int {
	quoted := false
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case c == '\\':
			i++
		case c == '"':
			quoted = !quoted
		case !quoted && c == ']':
			return i
		}
	}
	return -1
}
