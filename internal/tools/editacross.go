package tools

// The editor's two forms that reach past one literal quote: a pattern whose
// replacement uses what it matched, and one replacement applied to a list of
// files. Both are what a model otherwise spells as `sed -i` or `perl -pi`
// across a package, and both stay inside the editor for the reasons the
// editor exists: each file is read, changed and recorded on its own, so it is
// reviewed and undone on its own, and the pattern means the same thing on
// every host because it is the search tool's dialect and not the host's sed.
// See docs/capabilities/coding-agent.md#the-built-in-tools-come-before-the-shell.

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxEditPaths bounds an edit's list of files. Fifty covers a rename across a
// package or an argument added at every call site of a function, which is
// what the form is for; a list longer than that is a change across a tree,
// and its card would be a list nobody reads before answering.
const maxEditPaths = 50

// maxReplacementPairs bounds how many before/after pairs a pattern edit's
// result lists. Twenty is enough to check a pattern did what was meant at a
// sample of its places; the rest are counted, and the diff on the card and in
// review holds all of them.
const maxReplacementPairs = 20

// replacement is one place an edit changed: the line it starts on, the text
// that was there, and what replaced it.
type replacement struct {
	line          int
	before, after string
}

// plannedFile is one file of an edit across a list, planned and not yet
// written: what it holds now, what it would hold, and the places that differ.
type plannedFile struct {
	path   string
	before []byte
	after  string
	repls  []replacement
}

// compileEditPattern compiles an edit's old_text in the search tool's dialect
// and refuses what would make the replacement something other than what the
// card shows.
//
// Case is matched exactly, which is the search tool's case_sensitive and not
// its default: a search that finds `Foo` and `foo` is a question, and an edit
// that rewrites both is a change nobody asked for. ^ and $ are line anchors,
// which is what a pattern written from a line of a search result means by
// them.
//
// A pattern that matches the empty string is refused: it inserts new_text
// at positions rather than replacing text, and on a file it matches between
// every pair of characters. And new_text may only name a group the pattern
// has. The template language expands an unknown name to nothing — `$1x` is
// the group named "1x", not group 1 then x — so the card would show `$1x`
// as written while the file lost the text, which is the one thing a card must
// never do.
func compileEditPattern(pattern, template string) (*regexp.Regexp, error) {
	re, err := compileSearch(searchArgs{Pattern: pattern, CaseSensitive: true, Multiline: true})
	if err != nil {
		return nil, err
	}
	if re.MatchString("") {
		return nil, fmt.Errorf("old_text pattern %s can match empty text, which inserts new_text rather than replacing anything; make it match at least one character", snippet(pattern))
	}
	names := re.SubexpNames()
	for i := 0; i < len(template); i++ {
		if template[i] != '$' {
			continue
		}
		if i+1 < len(template) && template[i+1] == '$' {
			i++
			continue
		}
		name, width, ok := templateRef(template[i+1:])
		if !ok {
			// A $ the template language cannot read as a reference is
			// written as itself, which is also what the card shows.
			continue
		}
		i += width
		if groupExists(re, names, name) {
			continue
		}
		return nil, fmt.Errorf("new_text refers to $%s, and the pattern has no group by that number or name; write ${1}x where a group is followed by a letter, digit or underscore, and $$ for a literal $", name)
	}
	return re, nil
}

// templateRef reads the reference after a `$` the way regexp.Expand does: a
// name in braces, or the longest run of letters, digits and underscores. ok
// is false where Expand would write the `$` as text instead, and width is how
// many bytes after the `$` the reference took.
func templateRef(s string) (name string, width int, ok bool) {
	brace := strings.HasPrefix(s, "{")
	rest := s
	if brace {
		rest = s[1:]
	}
	n := 0
	for n < len(rest) {
		r, size := utf8.DecodeRuneInString(rest[n:])
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			break
		}
		n += size
	}
	if n == 0 {
		return "", 0, false
	}
	if !brace {
		return rest[:n], n, true
	}
	if n >= len(rest) || rest[n] != '}' {
		return "", 0, false
	}
	return rest[:n], n + 2, true
}

// groupExists reports whether a template reference names a group of re, by
// number or by name, by the rules regexp.Expand reads it with: all digits is a
// number, unless it has a leading zero, which makes it a name.
func groupExists(re *regexp.Regexp, names []string, name string) bool {
	num := 0
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c < '0' || c > '9' || num >= 1e8 {
			num = -1
			break
		}
		num = num*10 + int(c-'0')
	}
	if len(name) > 1 && name[0] == '0' {
		num = -1
	}
	if num >= 0 {
		return num <= re.NumSubexp()
	}
	for _, n := range names {
		if n != "" && n == name {
			return true
		}
	}
	return false
}

// checkPathList is what a list of files is refused for before any of them is
// read. The list carries one replacement: several, or an append, are changes
// to particular files and are calls with path.
func checkPathList(args editFileArgs) error {
	if len(args.Edits) > 0 || args.Append != "" {
		return fmt.Errorf("paths takes one old_text/new_text applied to every file listed; edits and append are for one file at a time — make those a call with path")
	}
	if len(args.Paths) > maxEditPaths {
		return fmt.Errorf("paths lists %d files and takes at most %d; split the change into calls of %d or fewer", len(args.Paths), maxEditPaths, maxEditPaths)
	}
	keys := map[string]string{}
	for _, p := range args.Paths {
		if strings.TrimSpace(p) == "" {
			return fmt.Errorf("paths has an empty entry; list each file by name")
		}
		if looksLikeGlob(p) {
			return fmt.Errorf("paths entry %q is a pattern and not a file; paths takes the files themselves — get them from search with files_only, or from fd, and list each by name", p)
		}
		if GitProgramPath("", p) {
			return ErrGitProgramPath
		}
		// A file listed twice would be planned twice against the same
		// content and written twice, the second write discarding the first.
		key := seenKey(p)
		if prev, dup := keys[key]; dup {
			return fmt.Errorf("paths names %s twice (as %q and %q); list each file once", p, prev, p)
		}
		keys[key] = p
	}
	return nil
}

// looksLikeGlob reports an entry that is a pattern rather than a file: one
// carrying glob syntax that names nothing on disk. A file whose name really
// holds a bracket — `[id].tsx` — exists, and is taken.
//
// A glob is refused rather than expanded because its expansion is a list
// nobody has seen: the files it reaches are decided when the call runs, not
// when the card is read, and a file that appears in between is edited on an
// answer given about a list that did not hold it.
func looksLikeGlob(p string) bool {
	if !strings.ContainsAny(p, "*?[") {
		return false
	}
	_, err := os.Stat(p)
	return err != nil
}

// planPaths reads and plans every file of an edit across a list before any
// is written, which is what lets a refusal anywhere in the list leave every
// file as it was: a stale file, one nobody read or searched, a pattern that
// matches one file twice without replace_all. A file the pattern does not
// match is planned with no replacements and left alone; a list where nothing
// matches anywhere is refused, since the call then describes nothing.
func (r *Recorder) planPaths(args editFileArgs) ([]plannedFile, error) {
	plans := make([]plannedFile, 0, len(args.Paths))
	total := 0
	for _, p := range args.Paths {
		content, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("cannot read %s: %w; nothing was written", p, err)
		}
		if err := r.checkListed(p, content); err != nil {
			return nil, fmt.Errorf("%w; nothing was written", err)
		}
		updated, repls, err := applyEdits(string(content), p, args.Edits, args.Regex, true)
		if err != nil {
			return nil, fmt.Errorf("%w; nothing was written", err)
		}
		total += len(repls)
		plans = append(plans, plannedFile{path: p, before: content, after: updated, repls: repls})
	}
	if total == 0 {
		what := "old_text"
		if args.Regex {
			what = "old_text pattern"
		}
		return nil, fmt.Errorf("%s matches nothing in any of the %d files listed", what, len(plans))
	}
	return plans, nil
}

// editPaths is an edit across a list: every file planned, then each written
// in the order listed, each as one write of its whole planned content. A
// write that fails stops the list there. The files before it stay written —
// each recorded on its own, and undoing them is the review's to offer one by
// one — and the result says which file failed and which were never reached,
// so the model knows exactly what the tree holds.
//
// A list that wrote something before it failed answers with a result rather
// than an error: the files it kept are changes, and the readers that keep a
// run's written files — a headless run's stager and tree reading, a child's
// digest, the language server's hook — skip an error result as one that
// wrote nothing. The failed file's content is unknown after a failed write
// (it may be truncated), so its record is dropped and the model is told to
// read it.
func (r *Recorder) editPaths(args editFileArgs) (string, error) {
	plans, err := r.planPaths(args)
	if err != nil {
		return "", err
	}
	var edited []string
	for i, p := range plans {
		if len(p.repls) == 0 {
			continue
		}
		if err := os.WriteFile(p.path, []byte(p.after), newFileMode); err != nil {
			r.forget(p.path)
			var left []string
			for _, q := range plans[i+1:] {
				if len(q.repls) > 0 {
					left = append(left, q.path)
				}
			}
			msg := fmt.Sprintf("the write to %s failed (%v), and what it holds now is unknown", p.path, err)
			if len(left) > 0 {
				msg += fmt.Sprintf("; not reached: %s", strings.Join(left, ", "))
			}
			msg += fmt.Sprintf(". Read %s before changing it again", p.path)
			if len(edited) == 0 {
				return "", fmt.Errorf("%s", msg)
			}
			return fmt.Sprintf("Edited %d of %d files, then %s. Already edited and kept: %s",
				len(edited), len(plans), msg, strings.Join(edited, ", ")), nil
		}
		r.noteEdited(p.path, p.before, []byte(p.after))
		edited = append(edited, p.path)
	}
	return pathsResult(plans, args.Regex), nil
}

// noteEdited keeps the record honest across a file of a list. A file the
// model had read moves with the edit at the wholeness it had, as a single
// edit's does. A file it had only seen named by a search is recorded as read
// in part and as edited unread: the model knows the lines it changed and has
// never seen the rest, so a later edit of it is not refused as stale, a later
// overwrite is still asked to read it whole first, and a later list does not
// take the edit for a read (checkListed).
func (r *Recorder) noteEdited(path string, before, after []byte) {
	rec, ok := r.lookupSeen(path)
	read := ok && !rec.unknown() && rec.sum == fingerprint(before) && !rec.unread
	r.noteShown(path, after, read && rec.whole)
	if !read {
		r.markUnread(path)
	}
}

// pathsResult is what the model reads after an edit across a list: one
// header naming how many files changed, a line per file, and for a pattern
// the replacements it made.
func pathsResult(plans []plannedFile, regex bool) string {
	edited, total := 0, 0
	for _, p := range plans {
		if len(p.repls) > 0 {
			edited++
			total += len(p.repls)
		}
	}
	var b strings.Builder
	if edited == len(plans) {
		fmt.Fprintf(&b, "Edited %d file(s): %d replacement(s)", edited, total)
	} else {
		fmt.Fprintf(&b, "Edited %d of %d files: %d replacement(s)", edited, len(plans), total)
	}
	for _, p := range plans {
		if len(p.repls) == 0 {
			fmt.Fprintf(&b, "\n  %s: no match, left as it was", p.path)
			continue
		}
		fmt.Fprintf(&b, "\n  %s: %d replacement(s), file now %d bytes (%d lines)", p.path, len(p.repls), len(p.after), countLines(p.after))
	}
	if regex {
		b.WriteString(replacedList(plans, true))
	}
	return b.String()
}

// replacedList lists a pattern edit's replacements as before/after pairs, at
// most maxReplacementPairs of them, and counts the rest. A pattern is the one
// edit whose result the model cannot predict from its own call, so the pairs
// are how it checks the groups landed where it meant.
func replacedList(plans []plannedFile, named bool) string {
	var b strings.Builder
	b.WriteString("\nReplaced:")
	listed, total := 0, 0
	for _, p := range plans {
		for _, rp := range p.repls {
			total++
			if listed >= maxReplacementPairs {
				continue
			}
			listed++
			where := fmt.Sprintf("line %d", rp.line)
			if named {
				where = fmt.Sprintf("%s:%d", p.path, rp.line)
			}
			fmt.Fprintf(&b, "\n  %s: %s → %s", where, snippet(rp.before), snippet(rp.after))
		}
	}
	if more := total - listed; more > 0 {
		fmt.Fprintf(&b, "\n  … %d more not listed; review the diff for all of them", more)
	}
	return b.String()
}
