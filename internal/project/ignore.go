package project

// What .gitignore says stays out of the file walks. The palette's FILES
// group and the draft's file-mention menu are offers of files a person
// might name in a sentence, and a build artefact git itself refuses to see
// is never that file — offering node_modules in a completion menu buries
// the source file the reader was reaching for.
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
