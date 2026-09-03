package project

// What .gitignore says stays out of the file walks. The palette's FILES
// group and the draft's file-mention menu are offers of files a person
// might name in a sentence, and a build artefact git itself refuses to see
// is never that file — offering node_modules in a completion menu buries
// the source file the reader was reaching for. The agent's own walks ask
// the same question for the same reason, through Ignore.
//
// This is a deliberate subset of git's rules, read from the .gitignore
// files the walk passes: comments and blanks, `!` negation with last-match
// wins, a trailing `/` for directories, patterns anchored by a `/` and
// matched from the ignore file's own directory, `*`/`?`/`[...]` per path
// segment, and `**` across segments. What it does not read — a global
// excludes file, .git/info/exclude, character-range corner cases — costs
// an offer at worst, never a file.

import (
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Ignore is the .gitignore rules in force at one point in a walk: those read
// from the directory being walked and from every directory above it, in the
// order they were read.
//
// It is a value rather than a walker of its own because the walkers that need
// it are shaped differently — one recurses, the others run under
// filepath.WalkDir — and the only thing they have to share is the answer. The
// zero value is a walk with no rules, which is what a tree with no .gitignore
// is.
type Ignore struct {
	rules []ignoreRule
}

// LoadIgnore reads the rules that apply inside dir.
//
// Rules above dir are deliberately not consulted. A walk answers for the tree
// it was pointed at, and a directory the caller named is one they have
// already decided to look in — a tool asked to list an ignored directory
// still lists it.
func LoadIgnore(dir string) Ignore {
	return Ignore{rules: parseIgnoreFile(dir)}
}

// Descend returns the rules in force inside dir: these, plus dir's own.
//
// The copy is deliberate. Appending in place lets two sibling directories
// write into the same spare capacity, and the second to descend would be
// matching against the first one's rules with nothing on screen to say so.
func (ig Ignore) Descend(dir string) Ignore {
	own := parseIgnoreFile(dir)
	if len(own) == 0 {
		return ig
	}
	out := make([]ignoreRule, 0, len(ig.rules)+len(own))
	out = append(out, ig.rules...)
	out = append(out, own...)
	return Ignore{rules: out}
}

// Ignored reports whether the entry at the absolute path is one git would not
// see.
func (ig Ignore) Ignored(abs string, isDir bool) bool {
	return ignored(ig.rules, abs, isDir)
}

// ignoreRule is one .gitignore line: the pattern's segments, the directory
// the file lived in (absolute, which anchors what the pattern is matched
// against), and the line's own flags.
type ignoreRule struct {
	segs     []string
	base     string
	negate   bool
	dirOnly  bool
	anchored bool
}

// parseIgnoreFile reads dir/.gitignore into rules, in file order. A missing
// or unreadable file is no rules: the walk is an offer, not a report.
func parseIgnoreFile(dir string) []ignoreRule {
	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		return nil
	}
	var rules []ignoreRule
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		r := ignoreRule{base: dir}
		if strings.HasPrefix(line, "!") {
			r.negate = true
			line = line[1:]
		}
		if strings.HasSuffix(line, "/") {
			r.dirOnly = true
			line = strings.TrimSuffix(line, "/")
		}
		// A separator anywhere in the pattern anchors it to the ignore
		// file's own directory; a bare name matches at any depth.
		if strings.HasPrefix(line, "/") {
			line = strings.TrimPrefix(line, "/")
			r.anchored = true
		} else if strings.Contains(line, "/") {
			r.anchored = true
		}
		if line == "" {
			continue
		}
		r.segs = strings.Split(line, "/")
		rules = append(rules, r)
	}
	return rules
}

// ignored reports whether the entry at absolute path, under the rules
// collected from its ancestor directories, is ignored. Rules are read in
// order and the last that matches wins, which is what lets a later
// `!keep.log` take a file back out of an earlier `*.log`.
func ignored(rules []ignoreRule, abs string, isDir bool) bool {
	out := false
	matched := false
	for _, r := range rules {
		if r.dirOnly && !isDir {
			continue
		}
		rel, err := filepath.Rel(r.base, abs)
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
			continue
		}
		if r.matches(filepath.ToSlash(rel)) {
			out = !r.negate
			matched = true
		}
	}
	return matched && out
}

// matches reports whether the rule matches the slash-separated path,
// relative to the rule's own directory.
func (r ignoreRule) matches(rel string) bool {
	segs := strings.Split(rel, "/")
	if !r.anchored {
		// A bare name matches the entry's own name at any depth.
		return matchSegments(r.segs, segs[len(segs)-1:])
	}
	return matchSegments(r.segs, segs)
}

// matchSegments matches pattern segments against path segments, with `**`
// free to swallow any run of them.
func matchSegments(pat, segs []string) bool {
	if len(pat) == 0 {
		return len(segs) == 0
	}
	if pat[0] == "**" {
		for i := 0; i <= len(segs); i++ {
			if matchSegments(pat[1:], segs[i:]) {
				return true
			}
		}
		return false
	}
	if len(segs) == 0 {
		return false
	}
	ok, err := path.Match(pat[0], segs[0])
	if err != nil || !ok {
		return false
	}
	return matchSegments(pat[1:], segs[1:])
}
