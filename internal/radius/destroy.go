package radius

// The destruction reading: what a command that destroys is pointed at, read
// closely enough to prove it. The rest of this package describes a command
// for a person deciding on it; this half decides nothing on its own either,
// but what it proves is answered by a rule rather than by a person — a
// recursive delete, a permission change over a tree or a write onto a device
// whose target is the filesystem root, the home directory, the workspace
// root, a repository's store, the deny mask or anywhere outside the working
// scope is refused in every mode
// (docs/capabilities/approvals-and-safety.md#some-targets-are-never-destroyed).
//
// Because a refusal is the whole consequence, the reading is honest in the
// direction that lets a command through to the card it always had: a target
// it cannot prove — a variable other than the home directory, a glob, a
// substitution, a relative path after a `cd` — is named as unresolved and
// refused by nothing here.

import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/rfizzle/shhh/internal/sandbox"
	"github.com/rfizzle/shhh/internal/scope"
)

// Where is what a command's targets are read against.
type Where struct {
	// Dir is the directory the command runs in, which a relative target is
	// measured from. Empty means it is not known — a process started with a
	// directory of its own — and a relative target then proves nothing.
	Dir string
	// Root is the workspace root, and Home the home directory. Either may be
	// empty, and then nothing is refused for being it.
	Root string
	Home string
	// Scope is the session's working scope. Nil is a surface with none, and
	// then nothing is refused for being outside it.
	Scope *scope.Scope
}

// DestroyTarget is one thing a destroying command is pointed at, resolved.
type DestroyTarget struct {
	// Word is the target as the command wrote it, which is how a refusal
	// names it: the reader wrote `~`, not the path it expands to.
	Word string
	// Path is where it resolves on this machine, symlinks followed where the
	// command would follow them.
	Path string
	// Whole is whether the command destroys the target itself rather than
	// some of what is under it: `rm -r` and `chmod -R` do, a `find` that
	// deletes only what its tests matched does not.
	Whole bool
	// Linked is whether a symlink stands anywhere between the command's
	// directory and Path, so what the word names is not where it lands.
	Linked bool
	// Irreplaceable is what the target is where this session may not destroy
	// it, as a phrase ("your home directory"), and empty everywhere else.
	Irreplaceable string
}

// Destruction is every destroying command in a line and what each is
// pointed at.
type Destruction struct {
	Targets []DestroyTarget
	// Unresolved names each target that could not be proved, for the same
	// reason Command.Unresolved does: an empty Targets beside it is not a
	// command pointed at nothing.
	Unresolved []string
}

// Refusal is the first target this session may not destroy, as the words a
// refusal names it with (`~ — your home directory`), or "" when none is.
func (d Destruction) Refusal() string {
	for _, t := range d.Targets {
		if t.Irreplaceable != "" {
			return t.Word + " — " + t.Irreplaceable
		}
	}
	return ""
}

// ProvenInside reports whether the line destroys something and every target
// it destroys is proven to sit below the workspace root: resolved, not the
// root itself, not refused, and not reached through a symlink. It is the
// other direction of the same reading — what a bounded clean-up is judged on
// — and it answers false for anything short of that, including a line with
// one unresolved target beside ten proven ones.
func (d Destruction) ProvenInside(root string) bool {
	if len(d.Targets) == 0 || len(d.Unresolved) > 0 || root == "" {
		return false
	}
	r, err := physical(root)
	if err != nil {
		return false
	}
	for _, t := range d.Targets {
		if t.Irreplaceable != "" || t.Linked || !strings.HasPrefix(t.Path, r+string(filepath.Separator)) {
			return false
		}
	}
	return true
}

// Destroys reads a command line for the commands in it that destroy, and
// resolves what each is pointed at against where. It never runs anything:
// the answers come from the text, from stat-ing the paths it names, and from
// asking git where a repository keeps its store.
func Destroys(command string, where Where) Destruction {
	r := destroyReader{where: where, known: where.Dir != ""}
	r.line(command, 0)
	return r.out
}

// destroyReader walks a line keeping what the text has said so far.
type destroyReader struct {
	where Where
	out   Destruction
	// known is whether a relative path still means Where.Dir: a `cd`
	// anywhere earlier in the line ends that.
	known bool
	// subst is how deep the reading is inside a command substitution, whose
	// words are not operands of the command around it.
	subst int
	// tick is whether a backquoted substitution is open.
	tick bool
}

// maxCarried bounds how deep one command carried inside another is read: a
// line nesting shells deeper than this is refused by nothing and gets its
// card.
const maxCarried = 4

// line reads each command of a line in order.
func (r *destroyReader) line(text string, depth int) {
	for _, seg := range splitSegments(text) {
		r.segment(tokenize(seg.text), depth)
	}
}

// changesDir are the commands after which a relative path no longer means
// the directory the line started in.
var changesDir = map[string]bool{"cd": true, "pushd": true, "popd": true, "chdir": true}

// carriers are the words that stand in front of the command they run, beyond
// the ones the write reading already strips: what follows them is read as a
// command of its own at every word, because their own options cannot be told
// from the command behind them without their option tables — the reading the
// safety table takes of the same words.
var carriers = map[string]bool{
	"sudo": true, "doas": true, "env": true, "xargs": true, "exec": true,
	"nice": true, "ionice": true, "timeout": true, "stdbuf": true, "setsid": true,
}

// segment reads one command.
func (r *destroyReader) segment(toks []token, depth int) {
	words := r.operandWords(toks)
	// A subshell's parenthesis is punctuation, not part of the verb.
	for len(words) > 0 && strings.Trim(words[0].text, "({") == "" {
		words = words[1:]
	}
	if len(words) == 0 {
		return
	}
	words[0].text = strings.TrimLeft(words[0].text, "({")
	carried := false
	for len(words) > 0 {
		base := path.Base(words[0].text)
		if carriers[base] {
			carried = true
		} else if !argPrefixes[base] && (!strings.Contains(words[0].text, "=") || strings.HasPrefix(words[0].text, "-")) {
			break
		}
		words = words[1:]
	}
	if len(words) == 0 {
		return
	}
	if changesDir[path.Base(words[0].text)] {
		r.known = false
		return
	}
	if !carried {
		r.command(words, depth)
		return
	}
	for i := range words {
		r.command(words[i:], depth)
	}
}

// operandWords takes a segment's device redirections as targets and hands
// back the rest, with every word inside a command substitution marked
// unresolvable: what `$(…)` runs is not an operand of the command around it,
// and neither is anything it names.
func (r *destroyReader) operandWords(toks []token) []token {
	var out []token
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		ticks := strings.Count(t.raw, "`")
		inside := r.subst > 0 || r.tick || ticks > 0 || strings.Contains(t.raw, "$(")
		r.subst += strings.Count(t.raw, "$(") - strings.Count(t.raw, ")")
		r.subst = max(r.subst, 0)
		r.tick = r.tick != (ticks%2 == 1)
		if inside {
			t.literal = false
		}
		if op, target := splitRedirect(t.text); op != "" {
			tt := t
			if target == "" && i+1 < len(toks) {
				i++
				tt = toks[i]
				target = tt.text
			}
			if tt.literal && device(target) {
				r.resolved(target, target, true, true)
			}
			continue
		}
		out = append(out, t)
	}
	return out
}

// command reads one command whose first word is its verb.
func (r *destroyReader) command(words []token, depth int) {
	if len(words) == 0 || !words[0].literal {
		return
	}
	verb := path.Base(words[0].text)
	args := words[1:]
	switch {
	case verb == "rm":
		if recursiveDelete(args) {
			r.operands(args, 0, false)
		}
	case verb == "chmod" || verb == "chown" || verb == "chgrp":
		if recursiveChange(args) {
			skip := 1
			if hasFlag(args, "--reference") {
				skip = 0
			}
			r.operands(args, skip, true)
		}
	case verb == "find":
		r.find(args)
	case verb == "git":
		r.gitClean(args)
	case verb == "dd":
		for _, t := range args {
			if of, ok := strings.CutPrefix(t.text, "of="); ok && t.literal && device(of) {
				r.resolved(of, of, true, true)
			}
		}
	case verb == "eval" && depth < maxCarried:
		r.carried(args, depth)
	case shells[verb] && depth < maxCarried:
		// `sh -c 'rm -rf /'` is the line in the quotes, run by a shell
		// standing where this one stands.
		for i, t := range args {
			if strings.HasPrefix(t.text, "-") && !strings.HasPrefix(t.text, "--") && strings.Contains(t.text, "c") {
				if i+1 < len(args) {
					r.line(args[i+1].text, depth+1)
				}
				return
			}
		}
	}
}

// shells are the interpreters whose `-c` is a line of shell.
var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true}

// carried reads the words eval was handed as the line they spell.
func (r *destroyReader) carried(args []token, depth int) {
	parts := make([]string, len(args))
	for i, t := range args {
		parts[i] = t.text
	}
	r.line(strings.Join(parts, " "), depth+1)
}

// recursiveChange reports whether chmod, chown or chgrp was told to descend.
// The letter is -R alone: -r is not one of theirs.
func recursiveChange(args []token) bool {
	for _, t := range args {
		w := t.text
		switch {
		case w == "--":
			return false
		case w == "--recursive":
			return true
		case strings.HasPrefix(w, "--"):
		case len(w) > 1 && w[0] == '-' && strings.ContainsRune(w[1:], 'R'):
			return true
		}
	}
	return false
}

// operands takes every operand after the first skip as a whole target.
// follow is whether the command acts on what a symlink points at; rm
// removes the link itself unless the word ends in a slash.
func (r *destroyReader) operands(args []token, skip int, follow bool) {
	done := false
	var ops []token
	for _, t := range args {
		switch {
		case done:
			ops = append(ops, t)
		case t.text == "--":
			done = true
		case strings.HasPrefix(t.text, "-") && t.text != "-":
		default:
			ops = append(ops, t)
		}
	}
	if skip >= len(ops) {
		return
	}
	for _, t := range ops[skip:] {
		r.word(t, true, follow)
	}
}

// findWhole are the parts of a find expression that select nothing, so a
// search built from them alone deletes everything under where it starts.
var findWhole = map[string]bool{
	"-delete": true, "-depth": true, "-d": true, "-xdev": true, "-mount": true,
	"-print": true, "-print0": true, "-noleaf": true, "-follow": true,
	"-ignore_readdir_race": true, "-noignore_readdir_race": true,
	"-mindepth": true, "-maxdepth": true,
}

// find reads a search that deletes what it finds — with -delete, or with an
// rm it runs over each result — and takes where it starts as its targets.
// A search that selects what it deletes destroys some of those trees rather
// than the trees themselves, so its targets are not whole: it is refused for
// where it reaches and not for what it starts at.
func (r *destroyReader) find(args []token) {
	i := 0
	follow := false
	// The options in front of the starting points are find's own.
	for ; i < len(args); i++ {
		w := args[i].text
		if w == "-L" || w == "-H" {
			follow = true
		} else if w == "-D" {
			i++
		} else if w != "-P" && !strings.HasPrefix(w, "-O") {
			break
		}
	}
	var starts []token
	for ; i < len(args); i++ {
		w := args[i].text
		if strings.HasPrefix(w, "-") || w == "(" || w == "!" || w == ")" || w == "," {
			break
		}
		starts = append(starts, args[i])
	}
	deletes, whole := false, true
	for j := i; j < len(args); j++ {
		w := args[j].text
		switch {
		case w == "-delete":
			deletes = true
		case execFlag(w):
			if j+1 < len(args) && path.Base(args[j+1].text) == "rm" {
				deletes = true
			} else {
				whole = false
			}
			// The command it runs ends at the terminator; nothing in it
			// selects anything.
			for j++; j < len(args) && args[j].text != ";" && args[j].text != `\;` && args[j].text != "+"; j++ {
			}
		case w == "-mindepth" || w == "-maxdepth":
			j++
		case !findWhole[w]:
			whole = false
		}
	}
	if !deletes {
		return
	}
	if len(starts) == 0 {
		starts = []token{{text: ".", literal: true, raw: "."}}
	}
	for _, t := range starts {
		r.word(t, whole, follow)
	}
}

// execFlag reports whether w hands the rest of a search to another program.
func execFlag(w string) bool {
	return w == "-exec" || w == "-execdir" || w == "-ok" || w == "-okdir"
}

// gitClean reads `git clean` with force, whose target is every pathspec it
// names or, with none, the directory it runs in: it deletes everything
// there git cannot bring back, which is exactly the irreplaceable part.
func (r *destroyReader) gitClean(args []token) {
	dir := token{text: ".", literal: true, raw: "."}
	elsewhere := false
	i := 0
	// git's own options come before the subcommand.
	for ; i < len(args) && strings.HasPrefix(args[i].text, "-"); i++ {
		switch w := args[i].text; {
		case w == "-C" && i+1 < len(args):
			i++
			dir = args[i]
		case w == "-c" && i+1 < len(args):
			i++
		case strings.HasPrefix(w, "--git-dir"), strings.HasPrefix(w, "--work-tree"):
			elsewhere = true
		}
	}
	if i >= len(args) || args[i].text != "clean" {
		return
	}
	rest := args[i+1:]
	force, dry := false, false
	var specs []token
	done := false
	for j := 0; j < len(rest); j++ {
		w := rest[j].text
		switch {
		case done:
			specs = append(specs, rest[j])
		case w == "--":
			done = true
		case w == "--force":
			force = true
		case w == "--dry-run":
			dry = true
		case w == "-e" || w == "--exclude":
			j++
		case strings.HasPrefix(w, "--"):
		case strings.HasPrefix(w, "-"):
			force = force || strings.ContainsRune(w[1:], 'f')
			dry = dry || strings.ContainsRune(w[1:], 'n')
		default:
			specs = append(specs, rest[j])
		}
	}
	if !force || dry {
		return
	}
	// A tree named some other way than the directory the command runs in is
	// one this reading does not follow.
	if elsewhere {
		r.unresolved("git is pointed at another tree")
		return
	}
	base, ok := r.dirOf(dir)
	if !ok {
		r.unresolved("git clean runs in a directory the shell expands")
		return
	}
	if len(specs) == 0 {
		r.resolved(dir.text, base, true, true)
		return
	}
	for _, s := range specs {
		if !s.literal || strings.ContainsAny(s.text, "*?[:") {
			r.unresolved("git clean is handed a pathspec shhh does not expand: " + s.text)
			continue
		}
		p := s.text
		if !filepath.IsAbs(p) {
			p = filepath.Join(base, p)
		}
		r.resolved(s.text, p, true, false)
	}
}

// dirOf is the absolute directory a word names, if it can be proved.
func (r *destroyReader) dirOf(t token) (string, bool) {
	p, ok := r.expand(t)
	if !ok {
		return "", false
	}
	if filepath.IsAbs(p) {
		return p, true
	}
	if !r.known {
		return "", false
	}
	return filepath.Join(r.where.Dir, p), true
}

// word resolves one target word, or records why it cannot be.
func (r *destroyReader) word(t token, whole, follow bool) {
	// An empty word names nothing — rm refuses it — and joined to the
	// directory it would read as the directory itself.
	if t.text == "" && t.literal {
		return
	}
	// Every entry of the filesystem root or the home directory is a glob,
	// but one whose answer is known without listing it: it is everything
	// under that directory, which is the directory.
	if dir, ok := strings.CutSuffix(t.raw, "/*"); ok && !t.literal {
		if d := tokenize(dir + "/"); len(d) == 1 {
			if p, ok := r.expand(d[0]); ok && filepath.IsAbs(p) && r.everything(p) {
				r.resolved(t.text, p, whole, true)
				return
			}
		}
	}
	p, ok := r.expand(t)
	switch {
	case !ok:
		r.unresolved("the shell expands " + t.text + " before the command sees it")
		return
	case !filepath.IsAbs(p) && !r.known:
		r.unresolved(t.text + " is relative to a directory this line does not settle")
		return
	}
	if !filepath.IsAbs(p) {
		p = r.where.Dir + string(filepath.Separator) + p
	}
	// A trailing slash, and the two names that are always directories, are
	// what the link points at whatever the command.
	base := filepath.Base(p)
	follow = follow || strings.HasSuffix(t.text, "/") || base == "." || base == ".."
	r.resolved(t.text, p, whole, follow)
}

// everything reports whether a glob over every entry of p is proved to be p:
// the filesystem root, answered from the text, and the home directory.
func (r *destroyReader) everything(p string) bool {
	if !strings.Contains(p, "..") && filepath.Clean(p) == string(filepath.Separator) {
		return true
	}
	return r.where.Home != "" && filepath.Clean(p) == filepath.Clean(r.where.Home)
}

// expand is the path a word names once the shell has finished with it, for
// the words whose expansion is known: a literal, and the home directory
// spelled as `~`, `$HOME` or `${HOME}`. Anything else is not proved.
func (r *destroyReader) expand(t token) (string, bool) {
	if t.literal {
		// A literal still carries the brace or parenthesis a subshell or a
		// brace expansion leaves on it, and then names no path as written.
		if strings.ContainsAny(t.text, "(){}") {
			return "", false
		}
		return t.text, true
	}
	if r.where.Home == "" || strings.ContainsAny(t.raw, `'\`) {
		return "", false
	}
	s := strings.ReplaceAll(t.raw, `"`, "")
	var rest string
	switch {
	case t.raw[0] == '~' && (s == "~" || strings.HasPrefix(s, "~/")):
		rest = s[1:]
	case strings.HasPrefix(s, "${HOME}"):
		rest = s[len("${HOME}"):]
	case strings.HasPrefix(s, "$HOME"):
		rest = s[len("$HOME"):]
	default:
		return "", false
	}
	if rest != "" && !strings.HasPrefix(rest, "/") || strings.ContainsAny(rest, "$`*?[~(){}") {
		return "", false
	}
	return r.where.Home + rest, true
}

// resolved classifies one target at an absolute (not yet cleaned) path.
func (r *destroyReader) resolved(word, abs string, whole, follow bool) {
	t := DestroyTarget{Word: word, Whole: whole}
	// The filesystem root is answered from the text: it cannot be a link,
	// and reading it would put the machine's own root in a test's inputs.
	if !strings.Contains(abs, "..") && filepath.Clean(abs) == string(filepath.Separator) {
		t.Path = string(filepath.Separator)
		t.Irreplaceable = "the filesystem root"
		r.out.Targets = append(r.out.Targets, t)
		return
	}
	// So is a device: what a write replaces there is a disk, which no
	// working scope holds, and a surface with no scope must refuse it too.
	if device(abs) {
		t.Path = filepath.Clean(abs)
		t.Irreplaceable = "a device, whose contents the write replaces"
		r.out.Targets = append(r.out.Targets, t)
		return
	}
	p, err := landing(abs, follow)
	if err != nil {
		r.unresolved(word + " does not resolve to a path on this machine")
		return
	}
	t.Path = p
	t.Linked = r.linked(abs, p)
	t.Irreplaceable = r.irreplaceable(p, whole)
	r.out.Targets = append(r.out.Targets, t)
}

// irreplaceable is what p is where this session may not destroy it.
func (r *destroyReader) irreplaceable(p string, whole bool) string {
	sep := string(filepath.Separator)
	if p == sep {
		return "the filesystem root"
	}
	home := ""
	if r.where.Home != "" {
		home, _ = physical(r.where.Home)
	}
	if whole && home != "" && p == home {
		return "your home directory"
	}
	if whole && r.where.Root != "" {
		if root, err := physical(r.where.Root); err == nil && p == root {
			return "the workspace root"
		}
	}
	// A link the command removes rather than follows is destroyed where it
	// stands, whatever it points at: removing a link to the home directory
	// removes a link.
	if info, err := os.Lstat(p); err == nil && info.Mode()&os.ModeSymlink != 0 {
		if r.where.Scope != nil && !r.where.Scope.Contains(filepath.Dir(p)) {
			return "outside the working scope"
		}
		return ""
	}
	if r.where.Scope != nil && !r.where.Scope.Contains(p) {
		return "outside the working scope"
	}
	// The deny mask is a set of stores in the home directory, so only a
	// target there is put to it; a workspace opened in the home directory
	// is the one way such a target is inside the scope.
	if home != "" && strings.HasPrefix(p, home+sep) {
		if class, reason := scope.Classify(p); class == scope.Refused {
			return reason
		}
	}
	if !whole {
		return ""
	}
	if _, ok := sandbox.GitStoreOf(p); ok {
		return "a repository's git store"
	}
	if info, err := os.Lstat(filepath.Join(p, ".git")); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
		return "a repository, git store and all"
	}
	return ""
}

// linked reports whether a symlink stands between where the command runs and
// where abs lands. It is measured below the command's directory where abs is
// under it, so a temporary directory the platform reaches through a link does
// not mark every path in the workspace.
func (r *destroyReader) linked(abs, landed string) bool {
	lexical := filepath.Clean(abs)
	if r.where.Dir != "" {
		dir := filepath.Clean(r.where.Dir)
		if rel, err := filepath.Rel(dir, lexical); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			if d, err := physical(dir); err == nil {
				return filepath.Join(d, rel) != landed
			}
		}
	}
	return lexical != landed
}

// landing is where a command pointed at abs acts. With follow it is the path
// every link resolved; without, a final element that is a link is the link
// itself, which is what rm removes.
func landing(abs string, follow bool) (string, error) {
	if follow {
		return physical(abs)
	}
	dir, base := filepath.Split(strings.TrimRight(abs, string(filepath.Separator)))
	d, err := physical(dir)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, base), nil
}

// errNoPath is a path that names no place: it climbs out of a directory that
// is not there.
var errNoPath = errors.New("no such path")

// physical resolves every link in p, and for a path not there yet resolves
// what is there and keeps the rest as written.
func physical(p string) (string, error) {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r, nil
	}
	trimmed := strings.TrimRight(p, string(filepath.Separator))
	dir, base := filepath.Split(trimmed)
	if base == "" || base == "." || base == ".." || dir == "" || dir == p {
		return "", errNoPath
	}
	d, err := physical(dir)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, base), nil
}

// device reports whether p is a device a write onto destroys what it holds.
// The streams and the endless sources every script writes to are not: a
// rule that refused `> /dev/null` would refuse half the commands anyone runs.
func device(p string) bool {
	rest, ok := strings.CutPrefix(path.Clean(p), "/dev/")
	if !ok || rest == "" {
		return false
	}
	switch rest {
	case "null", "zero", "full", "random", "urandom", "stdin", "stdout", "stderr", "ptmx", "console":
		return false
	}
	// A terminal is a stream too, whichever one it is.
	for _, pseudo := range []string{"fd/", "pts/", "shm/", "tty"} {
		if strings.HasPrefix(rest, pseudo) {
			return false
		}
	}
	return true
}

// unresolved records a target that could not be proved, once.
func (r *destroyReader) unresolved(reason string) {
	for _, u := range r.out.Unresolved {
		if u == reason {
			return
		}
	}
	r.out.Unresolved = append(r.out.Unresolved, reason)
}
